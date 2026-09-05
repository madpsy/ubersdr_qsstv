package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/szpajder/QSSTV/ubersdr_qsstv/internal/pcmv4"
)

// Conformance test for the launcher's audio protocol version 4 receive path.
//
// testdata/pcmv4_stream.bin is a packet stream the SERVER's encoder produced,
// and pcmv4ExpectedSHA is the SHA-256 of the samples that went into it, little
// endian, exactly as pcmDecoder.decode must render them before they reach the
// qsstv pipe.
//
// It earns its 90 kB. The version 4 predictor is backward adaptive: the two
// ends derive their filter taps independently from the samples already coded
// and never exchange a coefficient, so any arithmetic difference between this
// decoder and the Go one on the server produces plausible noise rather than an
// error. Nothing short of comparing the samples would catch it -- qsstv would
// simply stop decoding SSTV images, with nothing anywhere saying why.
//
// internal/pcmv4 checks the codec itself against the same fixture. This test
// checks the wrapper on top of it: that decode() hands on the codec's
// little-endian samples untouched (versions 1-3 carried radiod's big-endian
// samples and this file reversed them per packet -- doing that now would
// silently destroy the audio), and that it reports the stream parameters the
// WAV preview header and the FFT are built from.
const pcmv4ExpectedSHA = "4875d2185f1ff5a2031386c569cac0c2259e6a827b9e61f813399a19c3b9c903"

// readV4Fixture returns the packets in testdata/pcmv4_stream.bin.
//
// Layout: "UV4F", a format byte, a uint32 packet count, then each packet as a
// uint32 length and that many bytes.
func readV4Fixture(t *testing.T) [][]byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/pcmv4_stream.bin")
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	if len(raw) < 9 || string(raw[:4]) != "UV4F" || raw[4] != 0 {
		t.Fatal("fixture: bad header")
	}
	count := int(binary.LittleEndian.Uint32(raw[5:]))
	off := 9

	packets := make([][]byte, 0, count)
	for i := 0; i < count; i++ {
		if off+4 > len(raw) {
			t.Fatalf("fixture: truncated length at packet %d", i)
		}
		n := int(binary.LittleEndian.Uint32(raw[off:]))
		off += 4
		if off+n > len(raw) {
			t.Fatalf("fixture: truncated packet %d", i)
		}
		packets = append(packets, raw[off:off+n])
		off += n
	}
	if off != len(raw) {
		t.Fatalf("fixture: %d trailing bytes", len(raw)-off)
	}
	return packets
}

func TestPCMDecoderDecodesServerStream(t *testing.T) {
	packets := readV4Fixture(t)
	dec := newPCMDecoder()
	h := sha256.New()

	// Every distinct (rate, channels) the fixture passes through, in order. A
	// decoder that lost the carried-forward metadata could still hash correctly
	// while mislabelling the stream, and the rate and channel count are what
	// the audio preview's WAV header, the FFT and the stereo downmix are built
	// from.
	wantParams := [][2]int{{12000, 1}, {24000, 1}, {384000, 2}}
	var gotParams [][2]int

	for i, pkt := range packets {
		p, err := dec.decode(pkt)
		if err != nil {
			t.Fatalf("packet %d: %v", i, err)
		}
		if len(p.pcm) == 0 || len(p.pcm)%(2*p.channels) != 0 {
			t.Fatalf("packet %d: %d bytes is not whole frames of %d channels",
				i, len(p.pcm), p.channels)
		}
		q := [2]int{p.sampleRate, p.channels}
		if len(gotParams) == 0 || gotParams[len(gotParams)-1] != q {
			gotParams = append(gotParams, q)
		}
		h.Write(p.pcm)
	}

	if got := hex.EncodeToString(h.Sum(nil)); got != pcmv4ExpectedSHA {
		t.Fatalf("decoded samples differ from what the server encoded\n got %s\nwant %s",
			got, pcmv4ExpectedSHA)
	}
	if len(gotParams) != len(wantParams) {
		t.Fatalf("stream parameters: got %v, want %v", gotParams, wantParams)
	}
	for i := range wantParams {
		if gotParams[i] != wantParams[i] {
			t.Fatalf("stream parameters: got %v, want %v", gotParams, wantParams)
		}
	}
}

// The stereo packets in the fixture must still survive the downmix that wfm
// mode depends on: two int16 in, one int16 out.
func TestDecodedStereoDownmixes(t *testing.T) {
	dec := newPCMDecoder()
	downmixed := 0
	for _, pkt := range readV4Fixture(t) {
		p, err := dec.decode(pkt)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if p.channels != 2 {
			continue
		}
		mono := downmixStereoToMono(p.pcm)
		if len(mono) != len(p.pcm)/2 {
			t.Fatalf("%d bytes of stereo became %d bytes of mono, want %d",
				len(p.pcm), len(mono), len(p.pcm)/2)
		}
		downmixed++
	}
	if downmixed == 0 {
		t.Fatal("the fixture carried no stereo packets")
	}
}

// The decoder is backward adaptive and carries its header baseline and its
// predictor forward, so a reconnect must start a new one. runOnce() does that
// by calling newPCMDecoder() per connection; this pins the property that makes
// it necessary.
//
// The replay below runs over a PREFIX of the fixture, not the whole of it, and
// that is load-bearing. PCMv4StreamDecoder rebuilds its codec whenever the
// packet's profile changes, and the fixture switches profile partway through
// (the I/Q section uses a different one from the audio section). Replaying the
// entire stream through a carried-over decoder therefore crosses a profile
// change on the first packet of the replay, which incidentally rebuilds the
// predictor and reproduces the expected hash: a test written that way passes
// whether the decoder is reset or not, and proves nothing. A prefix that stays
// inside one profile is what makes stale state visible.
func TestDecoderIsResetOnReconnect(t *testing.T) {
	packets := readV4Fixture(t)
	if len(packets) < 50 {
		t.Fatalf("fixture holds %d packets, too few to replay a prefix", len(packets))
	}
	prefix := packets[:50]

	// Guard the premise: if a regenerated fixture ever put a profile change
	// inside this prefix, the replay would stop discriminating and the test
	// would go quietly green.
	profile := prefix[0][4] & 0x07
	for i, pkt := range prefix {
		if pkt[4]&0x07 != profile {
			t.Fatalf("packet %d changes codec profile; the prefix must stay within one "+
				"profile or the replay below cannot detect carried-over state", i)
		}
	}

	sum := func(d *pcmDecoder, pkts [][]byte) string {
		h := sha256.New()
		for i, pkt := range pkts {
			p, err := d.decode(pkt)
			if err != nil {
				t.Fatalf("packet %d: %v", i, err)
			}
			h.Write(p.pcm)
		}
		return hex.EncodeToString(h.Sum(nil))
	}

	// What a reconnect that builds a fresh decoder produces.
	fresh := sum(newPCMDecoder(), prefix)

	// What a reconnect that reused the previous connection's decoder would
	// produce: the same packets decoded against an adaptation the new stream
	// never generated. It must NOT match -- if it did, the reset would be
	// unobservable and this test could not tell the two apart.
	carried := newPCMDecoder()
	_ = sum(carried, prefix)
	if replayed := sum(carried, prefix); replayed == fresh {
		t.Fatal("a decoder carried across a reconnect produced the same samples as a fresh " +
			"one; this replay cannot detect stale state, so it is not testing anything")
	}

	// A second connection with its own decoder reproduces the first exactly.
	if got := sum(newPCMDecoder(), prefix); got != fresh {
		t.Fatalf("second connection: got %s, want %s", got, fresh)
	}

	// And a fresh decoder holds none of the previous connection's header
	// baseline either: a mid-stream packet, one that omits the metadata the
	// server only re-sends at a resynchronisation point, is refused rather than
	// decoded against a stale rate.
	var delta []byte
	for _, pkt := range packets[1:] {
		if len(pkt) > 4 && pkt[4]&(1<<5) == 0 {
			delta = pkt
			break
		}
	}
	if delta == nil {
		t.Fatal("the fixture carried no delta packets")
	}
	if _, err := newPCMDecoder().decode(delta); err == nil {
		t.Fatal("a fresh decoder accepted a mid-stream packet; it kept state across the reconnect")
	}
}

// A server too old for version 4 answers with the zstd-wrapped version 1 shape.
// Saying so is what stops a silent dead stream.
func TestLegacyServerFrameIsReported(t *testing.T) {
	dec := newPCMDecoder()
	if _, err := dec.decode([]byte{0x28, 0xB5, 0x2F, 0xFD, 0x00}); err == nil ||
		!strings.Contains(err.Error(), "too old") {
		t.Fatalf("a zstd frame gave %v, want a message naming the old server", err)
	}
}

// The launcher must ask for version 4: the server serves version 1 when no
// version is requested, and this build cannot read it.
func TestWSURLRequestsProtocolVersion4(t *testing.T) {
	inst := &instance{
		freqHz:     14230000,
		audioMode:  "usb",
		ubersdrURL: "http://example.invalid:8080",
		sessionID:  "test-session",
	}

	u, err := url.Parse(inst.wsURL())
	if err != nil {
		t.Fatalf("wsURL is not a URL: %v", err)
	}
	q := u.Query()
	if got := q.Get("version"); got != "4" {
		t.Fatalf("version=%q, want \"4\"", got)
	}
	if pcmv4.ProtocolVersion != 4 {
		t.Fatalf("the vendored decoder speaks version %d", pcmv4.ProtocolVersion)
	}
	// The format name did not change with the version; only the payload inside
	// it did. Asking for anything else would get a different codec.
	if got := q.Get("format"); got != "pcm-zstd" {
		t.Fatalf("format=%q, want \"pcm-zstd\"", got)
	}
}
