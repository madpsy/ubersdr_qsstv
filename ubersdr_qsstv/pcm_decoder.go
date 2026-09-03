package main

import (
	"encoding/binary"
	"fmt"

	"github.com/szpajder/QSSTV/ubersdr_qsstv/internal/pcmv4"
)

// ---------------------------------------------------------------------------
// PCM binary packet decoder (audio protocol version 4)
// ---------------------------------------------------------------------------
//
// The launcher asks the UberSDR server for protocol version 4, which replaced
// the zstd-wrapped versions 1-3 shape this file used to parse. Two things
// changed on the wire and neither is visible past this file:
//
//   - the payload is no longer zstd around big-endian int16 samples but a
//     predictive lossless code (internal/pcmv4/pcm_predictive.go) that emits
//     little-endian int16 directly, so the per-packet byte swap is gone;
//   - the fixed 29- or 37-byte header became a variable one carrying only what
//     changed since the last packet, so there is no "full" and "minimal" packet
//     distinction to branch on. Sample rate, channels and signal quality are
//     carried forward by the header decoder and are populated on every packet.
//
// zstd never compressed this material -- it is an LZ77 matcher over bytes, and
// a band-limited RF signal has no repeated byte strings -- so the wrapper cost
// CPU on both ends for nothing.
//
// The decoder is stateful and backward adaptive: it derives its predictor from
// the samples already decoded and never receives a coefficient, so it belongs
// to exactly one WebSocket connection. runOnce() builds a fresh one per
// connection and drops it when the socket closes; carrying one across a
// reconnect would decode the new stream against the old stream's adaptation and
// produce plausible noise rather than an error.

// pcmPacket is the result of decoding one binary WebSocket message.
type pcmPacket struct {
	pcm          []byte // little-endian int16 PCM samples
	sampleRate   int
	channels     int
	hasSigInfo   bool    // true when the server reported signal quality
	basebandDBFS float32 // baseband power dBFS
	noiseDBFS    float32 // noise density dBFS
}

type pcmDecoder struct {
	v4 *pcmv4.PCMv4StreamDecoder
}

// newPCMDecoder returns a decoder for one connection. It holds no state until
// the first packet carrying metadata arrives, which the server sends at the
// head of every stream and every five seconds after.
func newPCMDecoder() *pcmDecoder {
	return &pcmDecoder{v4: pcmv4.NewPCMv4StreamDecoder()}
}

// decode parses one binary WebSocket message into little-endian int16 PCM plus
// the stream parameters.
func (d *pcmDecoder) decode(data []byte) (pcmPacket, error) {
	// A server older than 0.1.63 clamps a version request to 1-3 and answers
	// with version 1 without saying so. Naming that is what turns a dead stream
	// into a message the operator can act on.
	if pcmv4.IsZstdFrame(data) {
		return pcmPacket{}, fmt.Errorf("server sent a zstd (protocol version 1) frame; it is too old for audio protocol version %d", pcmv4.ProtocolVersion)
	}
	if !pcmv4.PCMv4IsHeader(data) {
		return pcmPacket{}, fmt.Errorf("not a version %d packet (%d bytes)", pcmv4.ProtocolVersion, len(data))
	}

	pcmLE, rate, channels, power, noise, err := d.v4.DecodePacketLE(data)
	if err != nil {
		return pcmPacket{}, err
	}

	pkt := pcmPacket{
		pcm:          pcmLE,
		sampleRate:   rate,
		channels:     channels,
		basebandDBFS: power,
		noiseDBFS:    noise,
	}
	// -999 is the "radiod reported nothing" sentinel; only a real reading may
	// reach the SNR accumulator.
	pkt.hasSigInfo = power > -998 && noise > -998
	return pkt, nil
}

// downmixStereoToMono converts 2-channel S16LE PCM to mono S16LE.
// Used for wfm mode which delivers stereo 48 kHz audio.
func downmixStereoToMono(stereo []byte) []byte {
	n := len(stereo) / 4 // 2 bytes per sample × 2 channels
	mono := make([]byte, n*2)
	for i := 0; i < n; i++ {
		l := int32(int16(binary.LittleEndian.Uint16(stereo[i*4:])))
		r := int32(int16(binary.LittleEndian.Uint16(stereo[i*4+2:])))
		m := int16((l + r) / 2)
		binary.LittleEndian.PutUint16(mono[i*2:], uint16(m))
	}
	return mono
}
