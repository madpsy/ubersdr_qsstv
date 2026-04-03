package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"log"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"golang.org/x/image/draw"
)

const rcvBufSize = 16 * 1024 * 1024 // 16 MiB SO_RCVBUF

// wsDialer sets SO_RCVBUF = 16 MiB on the underlying TCP socket.
var wsDialer = &websocket.Dialer{
	HandshakeTimeout: 10 * time.Second,
	NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		nd := &net.Dialer{}
		conn, err := nd.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		if tc, ok := conn.(*net.TCPConn); ok {
			raw, err := tc.SyscallConn()
			if err == nil {
				_ = raw.Control(func(fd uintptr) {
					_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF, rcvBufSize)
				})
			}
		}
		return conn, nil
	},
}

// ---------------------------------------------------------------------------
// Protocol types
// ---------------------------------------------------------------------------

type connectionCheckRequest struct {
	UserSessionID string `json:"user_session_id"`
	Password      string `json:"password,omitempty"`
}

type connectionCheckResponse struct {
	Allowed        bool     `json:"allowed"`
	Reason         string   `json:"reason,omitempty"`
	ClientIP       string   `json:"client_ip,omitempty"`
	Bypassed       bool     `json:"bypassed"`
	AllowedIQModes []string `json:"allowed_iq_modes,omitempty"`
	MaxSessionTime int      `json:"max_session_time"`
}

// descriptionResponse is the subset of /api/description we care about.
type descriptionResponse struct {
	Receiver struct {
		Callsign string `json:"callsign"`
		Name     string `json:"name"`
		Antenna  string `json:"antenna"`
		Location string `json:"location"`
		GPS      struct {
			Lat        float64 `json:"lat"`
			Lon        float64 `json:"lon"`
			Maidenhead string  `json:"maidenhead"`
		} `json:"gps"`
	} `json:"receiver"`
}

// receiverInfo holds the parsed receiver metadata fetched from /api/description.
type receiverInfo struct {
	Callsign   string  `json:"callsign"`
	Name       string  `json:"name"`
	Antenna    string  `json:"antenna"`
	Location   string  `json:"location"`
	Lat        float64 `json:"lat"`
	Lon        float64 `json:"lon"`
	Maidenhead string  `json:"maidenhead"`
}

type wsMessage struct {
	Type      string `json:"type"`
	Error     string `json:"error,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
	Frequency int    `json:"frequency,omitempty"`
	Mode      string `json:"mode,omitempty"`
}

// ---------------------------------------------------------------------------
// SNR accumulator
// ---------------------------------------------------------------------------

type snrStats struct {
	AvgDB        float32
	MinDB        float32
	MaxDB        float32
	BasebandAvg  float32
	NoiseAvg     float32
	SampleCount  int
	Series       []snrPoint // 1-second bucketed time series
}

// ---------------------------------------------------------------------------
// fftBroadcastHub — fan-out of FFT magnitude frames to SSE listeners
// ---------------------------------------------------------------------------

type fftBroadcastHub struct {
	mu      sync.Mutex
	clients map[chan []byte]struct{}
}

func newFFTBroadcastHub() *fftBroadcastHub {
	return &fftBroadcastHub{clients: make(map[chan []byte]struct{})}
}

func (h *fftBroadcastHub) subscribe() chan []byte {
	ch := make(chan []byte, 8)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *fftBroadcastHub) unsubscribe(ch chan []byte) {
	h.mu.Lock()
	delete(h.clients, ch)
	h.mu.Unlock()
	close(ch)
}

func (h *fftBroadcastHub) broadcast(data []byte) {
	if len(data) == 0 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.clients {
		select {
		case ch <- data:
		default:
			// Subscriber too slow — drop frame.
		}
	}
}

func (h *fftBroadcastHub) hasListeners() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients) > 0
}

// ---------------------------------------------------------------------------
// audioBroadcastHub — fan-out of raw PCM chunks to preview listeners
// ---------------------------------------------------------------------------

// audioPrerollChunks is the number of recent PCM chunks kept in the ring
// buffer and replayed to each new subscriber so the browser can start
// playing immediately without waiting for its internal buffer to fill.
// At ~200 ms per chunk (AUDIO_CHUNK_BYTES = 4410 at 11025 Hz) this is ~200 ms.
// Keep this at 1: the client discards pre-roll chunks and starts its clock
// only from the first live chunk, so a larger value just adds stale audio
// that causes a content discontinuity (stutter) at the pre-roll/live boundary.
const audioPrerollChunks = 1

type audioBroadcastHub struct {
	mu      sync.Mutex
	clients map[chan []byte]struct{}
	// ring buffer of recent chunks for pre-roll on new subscribers
	ring    [][]byte
	ringPos int // index of the oldest entry (next write position)
	// resetChan is closed when resetClients() fires, signalling all active
	// /api/audio/preview HTTP handlers to return so the browser reconnects
	// and receives a fresh WAV header at the new connection's sample rate.
	resetChan chan struct{}
}

func newAudioBroadcastHub() *audioBroadcastHub {
	return &audioBroadcastHub{
		clients:   make(map[chan []byte]struct{}),
		ring:      make([][]byte, audioPrerollChunks),
		resetChan: make(chan struct{}),
	}
}

func (h *audioBroadcastHub) subscribe() chan []byte {
	// Buffer must be large enough to hold the pre-roll burst plus ongoing chunks.
	ch := make(chan []byte, audioPrerollChunks+64)
	h.mu.Lock()
	// Replay the ring buffer in chronological order (oldest → newest).
	// ringPos points to the oldest slot; iterate wrapping around.
	for i := 0; i < audioPrerollChunks; i++ {
		slot := (h.ringPos + i) % audioPrerollChunks
		if h.ring[slot] != nil {
			ch <- h.ring[slot]
		}
	}
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *audioBroadcastHub) unsubscribe(ch chan []byte) {
	h.mu.Lock()
	delete(h.clients, ch)
	h.mu.Unlock()
	// Only close if it's still in the map — resetClients may have already
	// closed it and removed it.
	// (safe: we already deleted it above, so double-close is not possible here)
}

// resetClients closes all active subscriber channels (causing every
// /api/audio/preview HTTP handler to return), flushes the pre-roll ring
// buffer, and replaces the reset channel so future subscribers get a fresh one.
// Must NOT be called with h.mu held.
func (h *audioBroadcastHub) resetClients() {
	h.mu.Lock()
	defer h.mu.Unlock()
	// Signal all active HTTP handlers to return by closing the reset channel.
	close(h.resetChan)
	h.resetChan = make(chan struct{})
	// Close and drain all subscriber channels.
	for ch := range h.clients {
		close(ch)
	}
	h.clients = make(map[chan []byte]struct{})
	// Flush the pre-roll ring buffer so new subscribers don't receive stale PCM.
	h.ring = make([][]byte, audioPrerollChunks)
	h.ringPos = 0
}

// currentResetChan returns the current reset channel.  The caller should
// capture this once and select on it; when it is closed, resetClients() fired.
func (h *audioBroadcastHub) currentResetChan() chan struct{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.resetChan
}

func (h *audioBroadcastHub) broadcast(pcm []byte) {
	if len(pcm) == 0 {
		return
	}
	// Make a copy so each subscriber gets its own slice.
	buf := make([]byte, len(pcm))
	copy(buf, pcm)
	h.mu.Lock()
	defer h.mu.Unlock()
	// Store in ring buffer for future subscribers.
	h.ring[h.ringPos] = buf
	h.ringPos = (h.ringPos + 1) % audioPrerollChunks
	// Fan out to current subscribers.
	for ch := range h.clients {
		select {
		case ch <- buf:
		default:
			// Subscriber too slow — drop chunk rather than block.
		}
	}
}

func (h *audioBroadcastHub) hasListeners() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients) > 0
}

// snrPoint is one second-bucket in the SNR time series.
type snrPoint struct {
	T     int64   `json:"t"`      // Unix milliseconds (bucket start)
	SNRDB float32 `json:"snr_db"` // average SNR in this bucket
}

// snrSample is a single raw measurement with wall-clock time.
type snrSample struct {
	tMs   int64   // wall-clock Unix ms
	snrDB float32 // basebandDBFS - noiseDBFS
	bb    float32
	noise float32
}

type snrAccumulator struct {
	mu      sync.Mutex
	samples []snrSample
}

func (a *snrAccumulator) add(baseband, noise float32) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.samples = append(a.samples, snrSample{
		tMs:   time.Now().UnixMilli(),
		snrDB: baseband - noise,
		bb:    baseband,
		noise: noise,
	})
}

func (a *snrAccumulator) reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.samples = a.samples[:0]
}

func (a *snrAccumulator) stats() snrStats {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := len(a.samples)
	if n == 0 {
		return snrStats{}
	}
	var sumSNR, sumBB, sumN float32
	minSNR := float32(math.MaxFloat32)
	maxSNR := float32(-math.MaxFloat32)
	for _, s := range a.samples {
		sumSNR += s.snrDB
		sumBB += s.bb
		sumN += s.noise
		if s.snrDB < minSNR {
			minSNR = s.snrDB
		}
		if s.snrDB > maxSNR {
			maxSNR = s.snrDB
		}
	}
	fn := float32(n)

	// Build 1-second bucketed time series
	series := make([]snrPoint, 0, n/50+1)
	if n > 0 {
		bucketStart := a.samples[0].tMs / 1000 * 1000 // floor to second
		var bucketSum float32
		var bucketCount int
		for _, s := range a.samples {
			bkt := s.tMs / 1000 * 1000
			if bkt != bucketStart {
				if bucketCount > 0 {
					series = append(series, snrPoint{
						T:     bucketStart,
						SNRDB: bucketSum / float32(bucketCount),
					})
				}
				bucketStart = bkt
				bucketSum = 0
				bucketCount = 0
			}
			bucketSum += s.snrDB
			bucketCount++
		}
		if bucketCount > 0 {
			series = append(series, snrPoint{
				T:     bucketStart,
				SNRDB: bucketSum / float32(bucketCount),
			})
		}
	}

	return snrStats{
		AvgDB:       sumSNR / fn,
		MinDB:       minSNR,
		MaxDB:       maxSNR,
		BasebandAvg: sumBB / fn,
		NoiseAvg:    sumN / fn,
		SampleCount: n,
		Series:      series,
	}
}

// ---------------------------------------------------------------------------
// Image state machine
// ---------------------------------------------------------------------------

type imgState int

const (
	imgIdle      imgState = iota
	imgReceiving
)

// headlessEvent is one JSON line from QSSTV's events fd.
type headlessEvent struct {
	Event       string `json:"event"`
	SSTVMode    string `json:"sstv_mode"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	File        string `json:"file"`
	Callsign    string `json:"callsign"`
	Reason      string `json:"reason"`
	TS          string `json:"timestamp"`
	ImageTimeMs int    `json:"image_time_ms"` // total transmission duration in ms (from SSTVTable)
	// rx_line fields
	Line    int    `json:"line"`
	Total   int    `json:"total"`
	JPEGB64 string `json:"jpeg_b64"`
}

// imageRecord is the companion JSON sidecar written alongside each saved image.
type imageRecord struct {
	ID              string           `json:"id"`
	File            string           `json:"file"`
	Thumb           string           `json:"thumb,omitempty"`
	SSTVMode        string           `json:"sstv_mode"`
	Callsign        string           `json:"callsign,omitempty"`
	FrequencyHz     int              `json:"frequency_hz"`
	AudioMode       string           `json:"audio_mode"`
	RxStart         time.Time        `json:"rx_start"`
	RxEnd           time.Time        `json:"rx_end"`
	SNRAvgDB        float32          `json:"snr_avg_db"`
	SNRMinDB        float32          `json:"snr_min_db"`
	SNRMaxDB        float32          `json:"snr_max_db"`
	BasebandAvgDBFS float32          `json:"baseband_avg_dbfs"`
	NoiseAvgDBFS    float32          `json:"noise_avg_dbfs"`
	SNRSamples      int              `json:"snr_samples"`
	SNRSeries       []snrPoint       `json:"snr_series,omitempty"`
	CTY             *CTYLookupResult `json:"cty,omitempty"`
	// Decode completeness — how many lines the mode produces vs. how many were
	// actually decoded before the image was saved.  A partial image (e.g. signal
	// lost mid-frame) will have LinesDecoded < ImageHeight.
	ImageHeight  int `json:"image_height,omitempty"`
	LinesDecoded int `json:"lines_decoded,omitempty"`
}

type imageTracker struct {
	state      imgState
	accum      *snrAccumulator
	startTime  time.Time
	freqHz     int
	audioMode  string
	outputDir  string
	eventCh    chan<- imageRecord
	rxLiveHub  *sseHub      // fan-out of partial-image SSE events; may be nil
	metrics    *metricsStore // may be nil
	// dimensions of the image currently being received (from rx_start)
	rxWidth  int
	rxHeight int
	// last rx_line line number seen — used to record how many lines were decoded
	lastLine int
	// callsign decoded from FSK-ID mid-image (rx_callsign event).
	// Stored here so it is available even if the queued Qt signal fires after
	// the rx_saved event (cross-thread AutoConnection race in QSSTV).
	pendingCallsign string
	// watchdog: cancelled when rx_saved/rx_discarded arrives; fires a synthetic
	// rx_end to web clients if the QSSTV sync processor never declares SYNCLOST.
	watchdogCancel context.CancelFunc
}

func newImageTracker(freqHz int, audioMode, outputDir string, eventCh chan<- imageRecord, rxLiveHub *sseHub, ms *metricsStore) *imageTracker {
	return &imageTracker{
		state:     imgIdle,
		accum:     &snrAccumulator{},
		freqHz:    freqHz,
		audioMode: audioMode,
		outputDir: outputDir,
		eventCh:   eventCh,
		rxLiveHub: rxLiveHub,
		metrics:   ms,
	}
}

func (t *imageTracker) handleEvent(ev headlessEvent) {
	switch ev.Event {
	case "rx_start":
		t.state = imgReceiving
		t.accum.reset()
		t.startTime = time.Now()
		t.rxWidth = ev.Width
		t.rxHeight = ev.Height
		t.lastLine = 0
		t.pendingCallsign = ""

		// Cancel any previous watchdog (shouldn't be active, but be safe).
		if t.watchdogCancel != nil {
			t.watchdogCancel()
			t.watchdogCancel = nil
		}

		// Arm a watchdog timer: if rx_saved/rx_discarded doesn't arrive within
		// image_time_ms + 10 s, synthesise an rx_end to web clients.  This
		// handles the case where the QSSTV sync processor stays INSYNC after
		// the image ends (e.g. noise/FSK-ID keeps producing valid sync pulses),
		// preventing endSSTVImageRX from ever firing.
		if ev.ImageTimeMs > 0 && t.rxLiveHub != nil {
			watchdogMs := ev.ImageTimeMs + 10000
			ctx, cancel := context.WithCancel(context.Background())
			t.watchdogCancel = cancel
			go func() {
				select {
				case <-ctx.Done():
					return // cancelled by rx_saved or rx_discarded
				case <-time.After(time.Duration(watchdogMs) * time.Millisecond):
				}
				log.Printf("imageTracker watchdog: rx_saved never arrived for %s after %d ms — synthesising rx_end to web clients",
					ev.SSTVMode, watchdogMs)
				stats := t.accum.stats()
				payload, _ := json.Marshal(map[string]interface{}{
					"event":         "rx_end",
					"sstv_mode":     ev.SSTVMode,
					"callsign":      t.pendingCallsign,
					"snr_avg_db":    stats.AvgDB,
					"snr_min_db":    stats.MinDB,
					"snr_max_db":    stats.MaxDB,
					"snr_samples":   stats.SampleCount,
					"image_height":  t.rxHeight,
					"lines_decoded": t.lastLine,
					"rx_end":        time.Now().UnixMilli(),
					"t":             time.Now().UnixMilli(),
					"watchdog":      true, // diagnostic flag
				})
				t.rxLiveHub.broadcast(fmt.Sprintf("event: rx_end\ndata: %s\n\n", payload))
			}()
		}

		// Notify live clients that a new image has started
		if t.rxLiveHub != nil {
			p := map[string]interface{}{
				"event":      "rx_start",
				"width":      ev.Width,
				"height":     ev.Height,
				"sstv_mode":  ev.SSTVMode,
				"freq_hz":    t.freqHz,
				"audio_mode": t.audioMode,
				"rx_start":   t.startTime.UnixMilli(),
				"t":          time.Now().UnixMilli(),
			}
			if ev.ImageTimeMs > 0 {
				p["image_time_ms"] = ev.ImageTimeMs
			}
			payload, _ := json.Marshal(p)
			t.rxLiveHub.broadcast(fmt.Sprintf("event: rx_start\ndata: %s\n\n", payload))
		}

	case "rx_callsign":
		// A callsign was decoded mid-image — store it and forward to live preview clients.
		// We keep a local copy because the Qt AutoConnection (cross-thread queued signal)
		// that delivers rx_callsign from the RX thread can race with the rx_saved event:
		// if rx_saved is processed first, ev.Callsign in that event will be empty.
		// pendingCallsign is used as a fallback in writeSidecar().
		if ev.Callsign != "" {
			t.pendingCallsign = ev.Callsign
		}
		if t.rxLiveHub != nil && ev.Callsign != "" {
			cty := GetCallsignInfo(ev.Callsign)
			payload, _ := json.Marshal(map[string]interface{}{
				"event":    "rx_callsign",
				"callsign": ev.Callsign,
				"cty":      cty,
				"t":        time.Now().UnixMilli(),
			})
			t.rxLiveHub.broadcast(fmt.Sprintf("event: rx_callsign\ndata: %s\n\n", payload))
		}

	case "rx_line":
		// Track the highest line number decoded so far.
		if ev.Line+1 > t.lastLine {
			t.lastLine = ev.Line + 1
		}
		// Forward partial JPEG to any live preview clients.
		if t.rxLiveHub != nil && ev.JPEGB64 != "" {
			payload, _ := json.Marshal(map[string]interface{}{
				"event":    "rx_line",
				"line":     ev.Line,
				"total":    ev.Total,
				"jpeg_b64": ev.JPEGB64,
			})
			t.rxLiveHub.broadcast(fmt.Sprintf("event: rx_line\ndata: %s\n\n", payload))
		}

	case "rx_saved":
		// Cancel the watchdog — the real rx_saved arrived in time.
		if t.watchdogCancel != nil {
			t.watchdogCancel()
			t.watchdogCancel = nil
		}
		if t.state == imgReceiving {
			// If QSSTV's queued callsign signal lost the race with rx_saved
			// (AutoConnection cross-thread delivery), fall back to the callsign
			// we stored when rx_callsign arrived earlier in this image.
			if ev.Callsign == "" && t.pendingCallsign != "" {
				log.Printf("rx_saved: callsign missing from event, using pendingCallsign %q", t.pendingCallsign)
				ev.Callsign = t.pendingCallsign
			}
			t.writeSidecar(ev)
		}
		t.state = imgIdle
		// Notify live clients that the image is complete, including final SNR stats.
		if t.rxLiveHub != nil {
			stats := t.accum.stats()
			var cty interface{}
			if ev.Callsign != "" {
				cty = GetCallsignInfo(ev.Callsign)
			}
			log.Printf("rx_saved: broadcasting rx_end to live clients (lines decoded: %d/%d, mode: %s)",
				t.lastLine, t.rxHeight, ev.SSTVMode)
			payload, _ := json.Marshal(map[string]interface{}{
				"event":         "rx_end",
				"file":          ev.File,
				"sstv_mode":     ev.SSTVMode,
				"callsign":      ev.Callsign,
				"cty":           cty,
				"snr_avg_db":    stats.AvgDB,
				"snr_min_db":    stats.MinDB,
				"snr_max_db":    stats.MaxDB,
				"snr_samples":   stats.SampleCount,
				"image_height":  t.rxHeight,
				"lines_decoded": t.lastLine,
				"rx_end":        time.Now().UnixMilli(),
				"t":             time.Now().UnixMilli(),
			})
			t.rxLiveHub.broadcast(fmt.Sprintf("event: rx_end\ndata: %s\n\n", payload))
		}

	case "rx_discarded":
		// Cancel the watchdog — the image was discarded cleanly.
		if t.watchdogCancel != nil {
			t.watchdogCancel()
			t.watchdogCancel = nil
		}
		t.state = imgIdle
		t.pendingCallsign = ""
		if t.rxLiveHub != nil {
			payload, _ := json.Marshal(map[string]interface{}{
				"event": "rx_discarded",
				"t":     time.Now().UnixMilli(),
			})
			t.rxLiveHub.broadcast(fmt.Sprintf("event: rx_discarded\ndata: %s\n\n", payload))
		}
	}
}

func (t *imageTracker) writeSidecar(ev headlessEvent) {
	stats := t.accum.stats()
	rxEnd := time.Now()

	base := strings.TrimSuffix(filepath.Base(ev.File), filepath.Ext(ev.File))
	thumbFile := base + "_thumb.jpg"

	rec := imageRecord{
		ID:              base,
		File:            filepath.Base(ev.File),
		Thumb:           thumbFile,
		SSTVMode:        ev.SSTVMode,
		Callsign:        ev.Callsign,
		FrequencyHz:     t.freqHz,
		AudioMode:       t.audioMode,
		RxStart:         t.startTime,
		RxEnd:           rxEnd,
		SNRAvgDB:        stats.AvgDB,
		SNRMinDB:        stats.MinDB,
		SNRMaxDB:        stats.MaxDB,
		BasebandAvgDBFS: stats.BasebandAvg,
		NoiseAvgDBFS:    stats.NoiseAvg,
		SNRSamples:      stats.SampleCount,
		SNRSeries:       stats.Series,
		ImageHeight:     t.rxHeight,
		LinesDecoded:    t.lastLine,
	}

	if ev.Callsign != "" {
		rec.CTY = GetCallsignInfo(ev.Callsign)
	}

	// Write JSON sidecar
	jsonPath := filepath.Join(t.outputDir, base+".json")
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		log.Printf("marshal sidecar: %v", err)
		return
	}
	if err := os.WriteFile(jsonPath, data, 0644); err != nil {
		log.Printf("write sidecar %s: %v", jsonPath, err)
	}

	// Record decode metric
	if t.metrics != nil {
		t.metrics.append(rec)
	}

	// Generate thumbnail
	go generateThumbnail(ev.File, filepath.Join(t.outputDir, thumbFile))

	// Fan-in to shared image store
	select {
	case t.eventCh <- rec:
	default:
		log.Printf("eventCh full, dropping record %s", rec.ID)
	}
}

// generateThumbnail creates a 160×120 JPEG thumbnail from a PNG image.
func generateThumbnail(srcPath, dstPath string) {
	f, err := os.Open(srcPath)
	if err != nil {
		log.Printf("thumbnail open %s: %v", srcPath, err)
		return
	}
	defer f.Close()

	src, err := png.Decode(f)
	if err != nil {
		log.Printf("thumbnail decode %s: %v", srcPath, err)
		return
	}

	dst := image.NewRGBA(image.Rect(0, 0, 160, 120))
	draw.BiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)

	out, err := os.Create(dstPath)
	if err != nil {
		log.Printf("thumbnail create %s: %v", dstPath, err)
		return
	}
	defer out.Close()

	if err := jpeg.Encode(out, dst, &jpeg.Options{Quality: 80}); err != nil {
		log.Printf("thumbnail encode %s: %v", dstPath, err)
	}
}

// ---------------------------------------------------------------------------
// instance
// ---------------------------------------------------------------------------

type instance struct {
	freqHz    int
	audioMode string
	label     string // e.g. "14230000_usb"

	ubersdrURL string
	password   string
	outputDir  string
	qsstvPath  string
	sessionID  string

	eventCh    chan<- imageRecord
	sseHub     *sseHub            // may be nil until set after store creation
	audioHub   *audioBroadcastHub // fan-out of live PCM to preview listeners
	fftHub     *fftBroadcastHub   // fan-out of FFT magnitude frames to SSE listeners
	rxLiveHub  *sseHub            // fan-out of rx_line partial-image SSE events
	metrics    *metricsStore      // may be nil

	mu            sync.Mutex
	running       bool
	stopping      bool
	startedAt     time.Time
	reconnections int
	status        string // "running" | "reconnecting" | "stopped"
	receiver      *receiverInfo // populated from /api/description after connect

	// loopCancel cancels the context passed to the current start() goroutine,
	// allowing setFrequency/setURL to interrupt a sleeping backoff immediately.
	loopCancel context.CancelFunc

	// Live stream format — set once the first packet arrives, read by /api/audio/preview.
	streamMu         sync.RWMutex
	streamSampleRate int
	streamChannels   int

	// fftMu guards instFFT so runOnce() goroutines don't race on it.
	fftMu   sync.Mutex
	instFFT *audioFFT // persists across runOnce() calls to preserve averaging state
}

func newInstance(freqHz int, audioMode, ubersdrURL, password, outputDir, qsstvPath string, eventCh chan<- imageRecord, ms *metricsStore) *instance {
	label := fmt.Sprintf("%d_%s", freqHz, audioMode)
	return &instance{
		freqHz:     freqHz,
		audioMode:  audioMode,
		label:      label,
		ubersdrURL: ubersdrURL,
		password:   password,
		outputDir:  outputDir,
		qsstvPath:  qsstvPath,
		sessionID:  uuid.New().String(),
		eventCh:    eventCh,
		audioHub:   newAudioBroadcastHub(),
		fftHub:     newFFTBroadcastHub(),
		rxLiveHub:  newSSEHub(),
		metrics:    ms,
		status:     "stopped",
	}
}

func (inst *instance) httpBase() string {
	u, _ := url.Parse(inst.ubersdrURL)
	scheme := u.Scheme
	switch scheme {
	case "ws":
		scheme = "http"
	case "wss":
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s", scheme, u.Host)
}

func (inst *instance) wsURL() string {
	u, _ := url.Parse(inst.ubersdrURL)
	wsScheme := "ws"
	if u.Scheme == "https" || u.Scheme == "wss" {
		wsScheme = "wss"
	}
	path := strings.TrimRight(u.Path, "/")
	if path == "" {
		path = "/ws"
	}
	q := url.Values{}
	q.Set("frequency", fmt.Sprintf("%d", inst.freqHz))
	q.Set("mode", inst.audioMode)
	q.Set("format", "pcm-zstd")
	q.Set("version", "2") // request v2 full-header with basebandDBFS + noiseDBFS
	q.Set("user_session_id", inst.sessionID)
	if inst.password != "" {
		q.Set("password", inst.password)
	}
	return fmt.Sprintf("%s://%s%s?%s", wsScheme, u.Host, path, q.Encode())
}

// fetchDescription calls /api/description on the UberSDR instance and stores
// the receiver metadata (callsign, name, antenna, GPS) on the instance.
// Errors are non-fatal — the instance will still connect without this info.
func (inst *instance) fetchDescription() {
	endpoint := inst.httpBase() + "/api/description"
	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		log.Printf("[%s] description request build: %v", inst.label, err)
		return
	}
	req.Header.Set("User-Agent", "ubersdr_qsstv/1.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("[%s] description fetch: %v", inst.label, err)
		return
	}
	defer resp.Body.Close()

	var desc descriptionResponse
	if err := json.NewDecoder(resp.Body).Decode(&desc); err != nil {
		log.Printf("[%s] description decode: %v", inst.label, err)
		return
	}

	r := desc.Receiver
	if r.GPS.Lat == 0 && r.GPS.Lon == 0 {
		log.Printf("[%s] description: no GPS coordinates available", inst.label)
		return
	}

	info := &receiverInfo{
		Callsign:   r.Callsign,
		Name:       r.Name,
		Antenna:    r.Antenna,
		Location:   r.Location,
		Lat:        r.GPS.Lat,
		Lon:        r.GPS.Lon,
		Maidenhead: r.GPS.Maidenhead,
	}
	inst.mu.Lock()
	inst.receiver = info
	inst.mu.Unlock()
	log.Printf("[%s] receiver: %s (%s) at %.4f,%.4f", inst.label, r.Callsign, r.Location, r.GPS.Lat, r.GPS.Lon)
}

func (inst *instance) checkConnection() (bool, error) {
	endpoint := inst.httpBase() + "/connection"
	body, _ := json.Marshal(connectionCheckRequest{
		UserSessionID: inst.sessionID,
		Password:      inst.password,
	})
	req, err := http.NewRequest("POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "ubersdr_qsstv/1.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("[%s] connection check failed (%v), attempting anyway", inst.label, err)
		return true, nil
	}
	defer resp.Body.Close()

	var cr connectionCheckResponse
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return false, fmt.Errorf("decode /connection response: %w", err)
	}
	if !cr.Allowed {
		return false, fmt.Errorf("server rejected connection: %s", cr.Reason)
	}
	log.Printf("[%s] connection allowed (IP: %s, bypassed: %v, max session: %ds)",
		inst.label, cr.ClientIP, cr.Bypassed, cr.MaxSessionTime)
	return true, nil
}

// runOnce performs one full connect → QSSTV launch → stream → disconnect cycle.
// Returns true if the caller should reconnect.
func (inst *instance) runOnce() (reconnect bool) {
	allowed, err := inst.checkConnection()
	if err != nil {
		log.Printf("[%s] error: %v", inst.label, err)
		return true
	}
	if !allowed {
		return false
	}

	// Fetch receiver metadata (non-fatal if unavailable).
	inst.fetchDescription()

	wsAddr := inst.wsURL()
	log.Printf("[%s] connecting to %s", inst.label, wsAddr)

	hdr := http.Header{}
	hdr.Set("User-Agent", "ubersdr_qsstv/1.0")
	conn, _, err := wsDialer.Dial(wsAddr, hdr)
	if err != nil {
		log.Printf("[%s] websocket dial: %v", inst.label, err)
		return true
	}
	defer conn.Close()

	log.Printf("[%s] connected — freq=%d Hz, mode=%s", inst.label, inst.freqHz, inst.audioMode)

	dec, err := newPCMDecoder()
	if err != nil {
		log.Printf("[%s] decoder init: %v", inst.label, err)
		return false
	}
	defer dec.close()

	// Create pipes: audioR → qsstv stdin; eventsW → qsstv fd 3; eventsR → Go reader
	audioR, audioW, err := os.Pipe()
	if err != nil {
		log.Printf("[%s] audio pipe: %v", inst.label, err)
		return true
	}
	eventsR, eventsW, err := os.Pipe()
	if err != nil {
		audioR.Close()
		audioW.Close()
		log.Printf("[%s] events pipe: %v", inst.label, err)
		return true
	}

	// Launch qsstv --headless
	cmd := exec.Command(inst.qsstvPath,
		"--headless",
		"--output-dir", inst.outputDir,
		"--events-fd", "3",
		"--freq-label", inst.label,
	)
	cmd.Stdin = audioR
	cmd.Stderr = os.Stderr
	cmd.ExtraFiles = []*os.File{eventsW} // fd 3

	if err := cmd.Start(); err != nil {
		audioR.Close()
		audioW.Close()
		eventsR.Close()
		eventsW.Close()
		log.Printf("[%s] qsstv start: %v", inst.label, err)
		return true
	}
	log.Printf("[%s] qsstv started (pid %d)", inst.label, cmd.Process.Pid)

	// Close the write-end of eventsW in the parent — qsstv owns it now.
	eventsW.Close()
	// Close the read-end of audioR in the parent — qsstv owns it now.
	audioR.Close()

	// Goroutine: wait for qsstv to exit
	qsstvDone := make(chan error, 1)
	go func() { qsstvDone <- cmd.Wait() }()

	// Goroutine: read JSON events from eventsR
	tracker := newImageTracker(inst.freqHz, inst.audioMode, inst.outputDir, inst.eventCh, inst.rxLiveHub, inst.metrics)
	eventsScanner := bufio.NewScanner(eventsR)
	go func() {
		defer eventsR.Close()
		for eventsScanner.Scan() {
			line := eventsScanner.Bytes()
			var ev headlessEvent
			if err := json.Unmarshal(line, &ev); err != nil {
				log.Printf("[%s] event parse: %v", inst.label, err)
				continue
			}
			tracker.handleEvent(ev)
		}
	}()

	// Keepalive goroutine
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := conn.WriteJSON(map[string]string{"type": "ping"}); err != nil {
					log.Printf("[%s] keepalive: %v", inst.label, err)
					return
				}
			}
		}
	}()

	// Throughput accounting (counters kept for future use; logging removed).
	var totalBytes atomic.Int64
	var totalPackets atomic.Int64

	firstPacket := true
	var lastSNRBroadcast time.Time // throttle live SNR SSE to 250 ms
	var instFFT *audioFFT          // lazily created once sample rate is known

	inst.mu.Lock()
	inst.status = "running"
	inst.startedAt = time.Now()
	inst.mu.Unlock()

	defer func() {
		cancel()
		// Kill qsstv child
		if cmd.Process != nil {
			_ = cmd.Process.Signal(syscall.SIGTERM)
			select {
			case <-qsstvDone:
			case <-time.After(500 * time.Millisecond):
				_ = cmd.Process.Kill()
			}
		}
		audioW.Close()
	}()

	for {
		inst.mu.Lock()
		running := inst.running
		inst.mu.Unlock()
		if !running {
			return false
		}

		// Check if qsstv died
		select {
		case exitErr := <-qsstvDone:
			log.Printf("[%s] qsstv exited: %v", inst.label, exitErr)
			return true
		default:
		}

		msgType, msg, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				log.Printf("[%s] server closed connection", inst.label)
			} else {
				log.Printf("[%s] read error: %v", inst.label, err)
			}
			return true
		}

		switch msgType {
		case websocket.BinaryMessage:
			pkt, err := dec.decode(msg, true /* pcm-zstd */)
			if err != nil {
				log.Printf("[%s] decode: %v", inst.label, err)
				continue
			}
			if len(pkt.pcm) == 0 {
				continue
			}
			if firstPacket {
					log.Printf("[%s] receiving audio: %d Hz, %d channel(s)", inst.label, pkt.sampleRate, pkt.channels)
					firstPacket = false
					// Store stream format for /api/audio/preview WAV header.
					inst.streamMu.Lock()
					inst.streamSampleRate = pkt.sampleRate
					inst.streamChannels   = pkt.channels
					inst.streamMu.Unlock()
				}

			// Accumulate SNR from v2 full-header packets
				if pkt.hasSigInfo {
					snrDB := pkt.basebandDBFS - pkt.noiseDBFS
					tracker.accum.add(pkt.basebandDBFS, pkt.noiseDBFS)
					// Broadcast live SNR to SSE clients at ~250 ms cadence
					if inst.sseHub != nil && time.Since(lastSNRBroadcast) >= 250*time.Millisecond {
						lastSNRBroadcast = time.Now()
						payload, _ := json.Marshal(map[string]interface{}{
							"label":  inst.label,
							"snr_db": snrDB,
							"t":      time.Now().UnixMilli(),
						})
						inst.sseHub.broadcast(fmt.Sprintf("event: snr\ndata: %s\n\n", payload))
					}
				}

			// Downmix stereo (wfm) to mono
			pcmData := pkt.pcm
			if pkt.channels == 2 {
				pcmData = downmixStereoToMono(pcmData)
			}

			// Write PCM to qsstv stdin
			n, err := audioW.Write(pcmData)
			if err != nil {
				log.Printf("[%s] stdin write: %v", inst.label, err)
				return true
			}
			totalBytes.Add(int64(n))
			totalPackets.Add(1)

			// Tee PCM to any active audio preview listeners (non-blocking).
			if inst.audioHub.hasListeners() {
				inst.audioHub.broadcast(pcmData)
			}

			// Compute FFT and broadcast magnitude frames to spectrum listeners.
			if inst.fftHub.hasListeners() {
				if instFFT == nil {
					instFFT = newAudioFFT(pkt.sampleRate)
				}
				if frame := instFFT.push(pcmData); frame != nil {
					if data, err := json.Marshal(frame); err == nil {
						inst.fftHub.broadcast(data)
					}
				}
			}

		case websocket.TextMessage:
			var m wsMessage
			if err := json.Unmarshal(msg, &m); err != nil {
				log.Printf("[%s] json parse: %v", inst.label, err)
				continue
			}
			switch m.Type {
			case "error":
				log.Printf("[%s] server error: %s", inst.label, m.Error)
				inst.mu.Lock()
				inst.running = false
				inst.mu.Unlock()
				return false
			case "status":
				log.Printf("[%s] status: session=%s freq=%d mode=%s",
					inst.label, m.SessionID, m.Frequency, m.Mode)
			case "pong":
				// keepalive ack — ignore
			}
		}
	}
}

// start runs the instance loop with exponential-backoff reconnect.
// ctx is used solely to interrupt the backoff sleep when a retune/URL-change
// is requested; it does NOT stop the loop — inst.running controls that.
func (inst *instance) start(ctx context.Context) {
	inst.mu.Lock()
	inst.running = true
	inst.status = "reconnecting"
	inst.mu.Unlock()

	retries := 0
	maxBackoff := 60 * time.Second

	for {
		inst.mu.Lock()
		running := inst.running
		inst.mu.Unlock()
		if !running {
			break
		}

		// Also exit if our context was cancelled (a retune/URL-change spawned a
		// new start() goroutine and cancelled us).
		// Do NOT write status here — the new goroutine owns the status field.
		select {
		case <-ctx.Done():
			return
		default:
		}

		inst.mu.Lock()
		inst.status = "reconnecting"
		inst.mu.Unlock()

		reconnect := inst.runOnce()

		inst.mu.Lock()
		running = inst.running
		inst.mu.Unlock()

		if !reconnect || !running {
			break
		}

		// Check again after runOnce returns — a retune may have fired.
		// Do NOT write status here — the new goroutine owns the status field.
		select {
		case <-ctx.Done():
			return
		default:
		}

		retries++
		backoff := time.Duration(1<<uint(retries)) * time.Second
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
		inst.mu.Lock()
		inst.reconnections++
		inst.mu.Unlock()
		log.Printf("[%s] reconnecting in %.0fs (attempt %d)…", inst.label, backoff.Seconds(), retries)

		// Interruptible sleep: wake immediately if ctx is cancelled.
		timer := time.NewTimer(backoff)
		select {
		case <-timer.C:
		case <-ctx.Done():
			// Superseded by a restart — do NOT write status.
			timer.Stop()
			return
		}
	}

	// Only write "stopped" when inst.running was set to false by stop() —
	// i.e. this is a genuine shutdown, not a restart/retune.
	inst.mu.Lock()
	running := inst.running
	inst.mu.Unlock()
	if !running {
		inst.mu.Lock()
		inst.status = "stopped"
		inst.mu.Unlock()
		log.Printf("[%s] stopped", inst.label)
	}
}

// stop signals the instance to stop after the current connection ends.
func (inst *instance) stop() {
	inst.mu.Lock()
	defer inst.mu.Unlock()
	inst.running = false
	inst.stopping = true
}

// restart cancels the current start() loop and launches a fresh one.
// Must be called with inst.mu already held; releases it before returning.
// The caller must have already updated whatever field (freqHz, ubersdrURL, …)
// needs to change before calling restart.
func (inst *instance) restart() {
	// Cancel the old loop so its backoff sleep wakes up immediately.
	if inst.loopCancel != nil {
		inst.loopCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	inst.loopCancel = cancel
	inst.running = true
	inst.status = "reconnecting"

	// Reset stream format so the next /api/audio/preview client waits for the
	// new connection's actual sample rate rather than reusing the old one.
	inst.streamMu.Lock()
	inst.streamSampleRate = 0
	inst.streamChannels = 0
	inst.streamMu.Unlock()

	// Kick all active /api/audio/preview HTTP handlers so they return, ending
	// their HTTP responses.  The browser's fetch stream will then end, causing
	// the client-side reconnect logic to open a fresh connection and receive a
	// new WAV header at the correct sample rate.
	// resetClients() also flushes the pre-roll ring buffer.
	inst.audioHub.resetClients()

	inst.mu.Unlock()

	go inst.start(ctx)
}

// setFrequency atomically updates the instance frequency (in Hz) and triggers
// an immediate reconnect by restarting the run loop.  The label is updated to
// reflect the new frequency so status badges stay consistent.
func (inst *instance) setFrequency(newFreqHz int) {
	inst.mu.Lock()
	inst.freqHz = newFreqHz
	inst.label = fmt.Sprintf("%d_%s", newFreqHz, inst.audioMode)
	// restart() releases the lock.
	inst.restart()
}

// setURL atomically updates the UberSDR base URL for all future connections and
// triggers an immediate reconnect by interrupting the current run loop.
func (inst *instance) setURL(newURL string) {
	inst.mu.Lock()
	inst.ubersdrURL = newURL
	// restart() releases the lock.
	inst.restart()
}

// statusSnapshot returns a copy of the instance's current status fields.
func (inst *instance) statusSnapshot() map[string]interface{} {
	inst.mu.Lock()
	defer inst.mu.Unlock()
	snap := map[string]interface{}{
		"freq_hz":       inst.freqHz,
		"audio_mode":    inst.audioMode,
		"label":         inst.label,
		"status":        inst.status,
		"started_at":    inst.startedAt,
		"reconnections": inst.reconnections,
	}
	if inst.receiver != nil {
		snap["receiver"] = inst.receiver
	}
	return snap
}
