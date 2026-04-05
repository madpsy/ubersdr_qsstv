package main

import (
	"crypto/rand"
	"crypto/tls"
	"embed"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Session store — in-memory set of valid session tokens
// ---------------------------------------------------------------------------

type sessionStore struct {
	mu     sync.RWMutex
	tokens map[string]struct{}
}

func newSessionStore() *sessionStore {
	return &sessionStore{tokens: make(map[string]struct{})}
}

func (s *sessionStore) create() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("session token generation failed: " + err.Error())
	}
	tok := hex.EncodeToString(b)
	s.mu.Lock()
	s.tokens[tok] = struct{}{}
	s.mu.Unlock()
	return tok
}

func (s *sessionStore) valid(tok string) bool {
	if tok == "" {
		return false
	}
	s.mu.RLock()
	_, ok := s.tokens[tok]
	s.mu.RUnlock()
	return ok
}

//go:embed static/*
var staticFiles embed.FS

// ---------------------------------------------------------------------------
// imageStore — in-memory store of all image records
// ---------------------------------------------------------------------------

type imageStore struct {
	mu        sync.RWMutex
	records   []imageRecord // newest first
	byID      map[string]*imageRecord
	deleted   map[string]struct{} // IDs explicitly deleted — prevents fan-in race re-insert
	outputDir string
	sseHub    *sseHub
}

func newImageStore(outputDir string) *imageStore {
	return &imageStore{
		byID:      make(map[string]*imageRecord),
		deleted:   make(map[string]struct{}),
		outputDir: outputDir,
		sseHub:    newSSEHub(),
	}
}

// loadExisting scans the output directory for existing .json sidecar files
// and populates the store on startup.  Records whose IDs are already in the
// deleted set (populated before this call) are silently skipped so that
// images deleted before a restart do not reappear.
func (s *imageStore) loadExisting() {
	entries, err := os.ReadDir(s.outputDir)
	if err != nil {
		log.Printf("imageStore.loadExisting: %v", err)
		return
	}

	// Collect all valid records without holding the lock (pure I/O phase).
	// Do NOT update byID here: each append may reallocate the backing array,
	// making any pointer stored in byID immediately stale.  byID is rebuilt
	// once after the sort, when the slice address is final.
	var recs []imageRecord
	s.mu.RLock()
	deletedSnap := make(map[string]struct{}, len(s.deleted))
	for k := range s.deleted {
		deletedSnap[k] = struct{}{}
	}
	s.mu.RUnlock()

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.outputDir, e.Name()))
		if err != nil {
			continue
		}
		var rec imageRecord
		if err := json.Unmarshal(data, &rec); err != nil {
			continue
		}
		if _, wasDel := deletedSnap[rec.ID]; wasDel {
			continue
		}
		recs = append(recs, rec)
	}

	// Sort newest first, then commit to the store and build byID in one shot.
	sort.Slice(recs, func(i, j int) bool {
		return recs[i].RxEnd.After(recs[j].RxEnd)
	})

	s.mu.Lock()
	s.records = recs
	s.byID = make(map[string]*imageRecord, len(recs))
	for i := range s.records {
		s.byID[s.records[i].ID] = &s.records[i]
	}
	s.mu.Unlock()

	log.Printf("imageStore: loaded %d existing records from %s", len(recs), s.outputDir)
}

// add inserts a new record at the front and notifies SSE clients.
// If the record's ID was already explicitly deleted (via the DELETE handler),
// the late fan-in delivery is silently discarded to prevent the record from
// reappearing in the gallery.
func (s *imageStore) add(rec imageRecord) {
	s.mu.Lock()
	if _, wasDel := s.deleted[rec.ID]; wasDel {
		s.mu.Unlock()
		return
	}
	// Prepend by building a new slice.  This reallocates the backing array, so
	// all existing byID pointers (which point into the old array) become stale.
	// Rebuild the entire byID map after the reallocation.
	s.records = append([]imageRecord{rec}, s.records...)
	s.byID = make(map[string]*imageRecord, len(s.records))
	for i := range s.records {
		s.byID[s.records[i].ID] = &s.records[i]
	}
	s.mu.Unlock()

	// Broadcast SSE event
	data, _ := json.Marshal(rec)
	s.sseHub.broadcast(fmt.Sprintf("event: image\ndata: %s\n\n", data))
}

func (s *imageStore) list(limit, offset int) []imageRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if offset >= len(s.records) {
		return nil
	}
	end := offset + limit
	if end > len(s.records) || limit <= 0 {
		end = len(s.records)
	}
	out := make([]imageRecord, end-offset)
	copy(out, s.records[offset:end])
	return out
}

func (s *imageStore) get(id string) (*imageRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.byID[id]
	if !ok {
		return nil, false
	}
	cp := *r
	return &cp, true
}

// ---------------------------------------------------------------------------
// SSE hub
// ---------------------------------------------------------------------------

type sseHub struct {
	mu      sync.Mutex
	clients map[chan string]struct{}
}

func newSSEHub() *sseHub {
	return &sseHub{clients: make(map[chan string]struct{})}
}

func (h *sseHub) subscribe() chan string {
	ch := make(chan string, 16)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *sseHub) unsubscribe(ch chan string) {
	h.mu.Lock()
	delete(h.clients, ch)
	h.mu.Unlock()
}

func (h *sseHub) broadcast(msg string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.clients {
		select {
		case ch <- msg:
		default:
		}
	}
}

// ---------------------------------------------------------------------------
// Web server
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// WAV streaming helpers
// ---------------------------------------------------------------------------

// writeStreamingWAVHeader writes a WAV RIFF header with a near-infinite data
// length (0x7FFFFFFF) so the browser treats the response as a live stream.
// Format: PCM S16LE, mono or stereo, at the given sample rate.
func writeStreamingWAVHeader(w http.ResponseWriter, sampleRate, channels int) {
	const bitsPerSample = 16
	byteRate := sampleRate * channels * bitsPerSample / 8
	blockAlign := channels * bitsPerSample / 8
	const dataSize = 0x7FFFFFFF // "infinite" stream

	hdr := make([]byte, 44)
	copy(hdr[0:4], "RIFF")
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(dataSize+36))
	copy(hdr[8:12], "WAVE")
	copy(hdr[12:16], "fmt ")
	binary.LittleEndian.PutUint32(hdr[16:20], 16) // PCM chunk size
	binary.LittleEndian.PutUint16(hdr[20:22], 1)  // PCM format
	binary.LittleEndian.PutUint16(hdr[22:24], uint16(channels))
	binary.LittleEndian.PutUint32(hdr[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(hdr[28:32], uint32(byteRate))
	binary.LittleEndian.PutUint16(hdr[32:34], uint16(blockAlign))
	binary.LittleEndian.PutUint16(hdr[34:36], uint16(bitsPerSample))
	copy(hdr[36:40], "data")
	binary.LittleEndian.PutUint32(hdr[40:44], uint32(dataSize))

	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(http.StatusOK)
	w.Write(hdr) //nolint:errcheck
}

const sessionCookieName = "ui_session"

// requiresAuth checks the session cookie against the store.
// If the password is empty, write actions are disabled entirely (returns false with a 403).
// If the password is set and the session is valid, returns true.
// Otherwise returns false and writes the appropriate HTTP error.
func requiresAuth(w http.ResponseWriter, r *http.Request, uiPassword string, sessions *sessionStore) bool {
	if uiPassword == "" {
		http.Error(w, `{"error":"write actions are disabled — set UI_PASSWORD to enable them"}`, http.StatusForbidden)
		return false
	}
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || !sessions.valid(cookie.Value) {
		http.Error(w, `{"error":"authentication required"}`, http.StatusUnauthorized)
		return false
	}
	return true
}

func startWebServer(addr string, store *imageStore, instances []*instance, outputDir string, receiverLat, receiverLon float64, tlsCfg *tls.Config, currentURL *string, urlMu *sync.RWMutex, ms *metricsStore, uiPassword string) error {
	sessions := newSessionStore()
	mux := http.NewServeMux()

	// ---------------------------------------------------------------------------
	// Auth endpoints
	// ---------------------------------------------------------------------------

	// GET /api/auth/status — returns whether a password is configured and whether
	// the current session is authenticated.
	mux.HandleFunc("/api/auth/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		configured := uiPassword != ""
		authed := false
		if configured {
			if cookie, err := r.Cookie(sessionCookieName); err == nil {
				authed = sessions.valid(cookie.Value)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"password_configured": configured,
			"authenticated":       authed,
		})
	})

	// POST /api/auth/login — verify password and issue a session cookie.
	// Body: {"password": "..."}
	mux.HandleFunc("/api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if uiPassword == "" {
			http.Error(w, `{"error":"no password configured"}`, http.StatusForbidden)
			return
		}
		var body struct {
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		if body.Password != uiPassword {
			http.Error(w, `{"error":"incorrect password"}`, http.StatusUnauthorized)
			return
		}
		tok := sessions.create()
		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookieName,
			Value:    tok,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
	})

	// POST /api/auth/logout — clear the session cookie.
	mux.HandleFunc("/api/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookieName,
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
	})

	// Static files (embedded)
	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return fmt.Errorf("static fs: %w", err)
	}
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	// Root → index.html (served as a template so BASE_PATH can be injected)
	indexTmpl, indexTmplErr := func() (*template.Template, error) {
		data, err := staticFiles.ReadFile("static/index.html")
		if err != nil {
			return nil, err
		}
		return template.New("index").Parse(string(data))
	}()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if indexTmplErr != nil {
			http.Error(w, "index.html not found", 500)
			return
		}
		// Derive the base path from X-Forwarded-Prefix (set by the addon proxy
		// when strip_prefix is enabled).  Falls back to "" for direct access.
		basePath := strings.TrimRight(r.Header.Get("X-Forwarded-Prefix"), "/")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		indexTmpl.Execute(w, map[string]string{"BasePath": basePath}) //nolint:errcheck
	})

	// GET /api/images?limit=N&offset=N
	mux.HandleFunc("/api/images", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		limit := 50
		offset := 0
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				limit = n
			}
		}
		if v := r.URL.Query().Get("offset"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				offset = n
			}
		}
		records := store.list(limit, offset)
		if records == nil {
			records = []imageRecord{}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(records)
	})

	// GET /api/images/{id}
	// DELETE /api/images/{id} — remove image, thumbnail, sidecar and store entry
	mux.HandleFunc("/api/images/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/api/images/")
		if id == "" {
			http.NotFound(w, r)
			return
		}

		switch r.Method {
		case http.MethodGet:
			rec, ok := store.get(id)
			if !ok {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(rec)

		case http.MethodDelete:
			if !requiresAuth(w, r, uiPassword, sessions) {
				return
			}
			rec, ok := store.get(id)
			if !ok {
				http.NotFound(w, r)
				return
			}

			// Remove files from disk (image, thumbnail, sidecar).
			// Ignore individual errors so a missing file doesn't abort the rest.
			for _, name := range []string{rec.File, rec.Thumb} {
				if name != "" {
					os.Remove(filepath.Join(outputDir, name)) //nolint:errcheck
				}
			}

			// Sidecar JSON: try the base-name-derived path first, then fall back
			// to a full directory scan matching by ID.  Both paths are always
			// attempted so that partial-name-match failures don't leave orphaned
			// sidecars that would cause the record to reappear after a restart.
			sidecarPath := ""
			if rec.File != "" {
				base := strings.TrimSuffix(rec.File, filepath.Ext(rec.File))
				candidate := filepath.Join(outputDir, base+".json")
				if err := os.Remove(candidate); err == nil {
					sidecarPath = candidate
				}
			}
			if sidecarPath == "" {
				// Fallback: scan for any .json whose decoded ID matches — handles
				// edge cases where File is empty or the base name doesn't match.
				if entries, err := os.ReadDir(outputDir); err == nil {
					for _, e := range entries {
						if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
							continue
						}
						p := filepath.Join(outputDir, e.Name())
						data, err := os.ReadFile(p)
						if err != nil {
							continue
						}
						var tmp struct {
							ID string `json:"id"`
						}
						if json.Unmarshal(data, &tmp) == nil && tmp.ID == id {
							os.Remove(p) //nolint:errcheck
							sidecarPath = p
							break
						}
					}
				}
			}

			// Remove from in-memory store and mark as deleted so fan-in goroutines
			// and loadExisting() on restart cannot re-insert this record.
			store.mu.Lock()
			store.deleted[id] = struct{}{}
			delete(store.byID, id)
			// Allocate a fresh slice — do NOT use store.records[:0] which shares
			// the same backing array and causes aliasing corruption when the range
			// loop reads positions that have already been overwritten by append.
			filtered := make([]imageRecord, 0, len(store.records)-1)
			for _, r := range store.records {
				if r.ID != id {
					filtered = append(filtered, r)
				}
			}
			store.records = filtered
			// Rebuild byID pointers after the slice was reallocated.
			store.byID = make(map[string]*imageRecord, len(filtered))
			for i := range store.records {
				store.byID[store.records[i].ID] = &store.records[i]
			}
			store.mu.Unlock()

			// Broadcast a delete event to all SSE clients so every open browser
			// tab removes the card immediately without needing a page refresh.
			deleteEvt, _ := json.Marshal(map[string]string{"id": id})
			store.sseHub.broadcast(fmt.Sprintf("event: delete\ndata: %s\n\n", deleteEvt))

			log.Printf("deleted image %s", id)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "id": id})

		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// GET /images/{filename} — serve actual image/thumb files from outputDir
	mux.HandleFunc("/images/", func(w http.ResponseWriter, r *http.Request) {
		filename := strings.TrimPrefix(r.URL.Path, "/images/")
		if filename == "" || strings.Contains(filename, "..") {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(outputDir, filename))
	})

	// GET /api/live — SSE stream
	mux.HandleFunc("/api/live", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no") // disable nginx buffering if behind proxy
		w.Header().Set("Access-Control-Allow-Origin", "*")

		ch := store.sseHub.subscribe()
		defer store.sseHub.unsubscribe(ch)

		// Send an immediate comment so the browser sees the stream is alive
		fmt.Fprint(w, ": connected\n\n")
		flusher.Flush()

		// Send a keepalive comment every 5 s to prevent browser/proxy timeouts
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case msg := <-ch:
				fmt.Fprint(w, msg)
				flusher.Flush()
			case <-ticker.C:
				fmt.Fprint(w, ": keepalive\n\n")
				flusher.Flush()
			case <-r.Context().Done():
				return
			}
		}
	})

	// GET /api/audio/preview?label=<label> — stream live PCM as WAV audio
	mux.HandleFunc("/api/audio/preview", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		label := r.URL.Query().Get("label")

		// Find the matching instance (or use the first running one).
		var inst *instance
		for _, candidate := range instances {
			if label == "" || candidate.label == label {
				inst = candidate
				break
			}
		}
		if inst == nil {
			http.Error(w, "no matching instance", http.StatusNotFound)
			return
		}

		// Wait up to 3 s for the stream format to be known (first packet).
		var sampleRate, channels int
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			inst.streamMu.RLock()
			sampleRate = inst.streamSampleRate
			channels = inst.streamChannels
			inst.streamMu.RUnlock()
			if sampleRate > 0 {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if sampleRate == 0 {
			// Fall back to a sensible default so the browser still gets audio.
			sampleRate = 11025
			channels = 1
		}
		// After downmix, preview is always mono.
		channels = 1

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}

		writeStreamingWAVHeader(w, sampleRate, channels)
		flusher.Flush()

		ch := inst.audioHub.subscribe()
		defer inst.audioHub.unsubscribe(ch)

		// Capture the reset channel before entering the loop.  When
		// resetClients() fires (on instance restart / URL change), this channel
		// is closed, causing the select below to return immediately.  The HTTP
		// response ends, the browser's fetch stream terminates, and the
		// client-side reconnect logic opens a fresh connection that receives a
		// new WAV header at the correct sample rate.
		resetCh := inst.audioHub.currentResetChan()

		log.Printf("[%s] audio preview client connected (%s)", inst.label, r.RemoteAddr)
		defer log.Printf("[%s] audio preview client disconnected (%s)", inst.label, r.RemoteAddr)

		for {
			select {
			case <-r.Context().Done():
				return
			case <-resetCh:
				// Instance restarted — end this response so the browser reconnects.
				return
			case chunk, ok := <-ch:
				if !ok {
					// Channel was closed by resetClients() — same as above.
					return
				}
				if _, err := w.Write(chunk); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	})

	// GET /api/fft?label=<label> — SSE stream of FFT magnitude frames for the audio panel
	mux.HandleFunc("/api/fft", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}

		label := r.URL.Query().Get("label")
		var inst *instance
		for _, candidate := range instances {
			if label == "" || candidate.label == label {
				inst = candidate
				break
			}
		}
		if inst == nil {
			http.Error(w, "no matching instance", http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		ch := inst.fftHub.subscribe()
		defer inst.fftHub.unsubscribe(ch)

		fmt.Fprint(w, ": connected\n\n")
		flusher.Flush()

		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case data, ok := <-ch:
				if !ok {
					return
				}
				fmt.Fprintf(w, "event: fft\ndata: %s\n\n", data)
				flusher.Flush()
			case <-ticker.C:
				fmt.Fprint(w, ": keepalive\n\n")
				flusher.Flush()
			case <-r.Context().Done():
				return
			}
		}
	})

	// GET /api/rx/live?label=<label> — SSE stream of partial-image events for live preview
	mux.HandleFunc("/api/rx/live", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}

		label := r.URL.Query().Get("label")
		var inst *instance
		for _, candidate := range instances {
			if label == "" || candidate.label == label {
				inst = candidate
				break
			}
		}
		if inst == nil {
			http.Error(w, "no matching instance", http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		ch := inst.rxLiveHub.subscribe()
		defer inst.rxLiveHub.unsubscribe(ch)

		fmt.Fprint(w, ": connected\n\n")
		flusher.Flush()

		// If a decode is already in progress when this client connects, send a
		// synthetic rx_start (and the latest partial image if available) so the
		// browser can show the live panel immediately without waiting for the
		// next rx_start event.
		if snap := inst.liveRxSnapshot(); snap != nil {
			startPayload := map[string]interface{}{
				"event":      "rx_start",
				"width":      snap.Width,
				"height":     snap.Height,
				"sstv_mode":  snap.SSTVMode,
				"freq_hz":    snap.FreqHz,
				"audio_mode": snap.AudioMode,
				"rx_start":   snap.RxStartMs,
				"t":          snap.RxStartMs,
				// line/total let the client initialise the progress bar and
				// countdown to the correct mid-decode position.
				"line":    snap.LatestLine,
				"total":   snap.TotalLines,
				"catchup": true, // signals the client this is a mid-decode catch-up
			}
			if snap.ImageTimeMs > 0 {
				startPayload["image_time_ms"] = snap.ImageTimeMs
			}
			if data, err := json.Marshal(startPayload); err == nil {
				fmt.Fprintf(w, "event: rx_start\ndata: %s\n\n", data)
			}
			// Send the latest partial image so the panel shows something immediately.
			if snap.LatestJPEGB64 != "" {
				linePayload := map[string]interface{}{
					"event":    "rx_line",
					"line":     snap.LatestLine,
					"total":    snap.TotalLines,
					"jpeg_b64": snap.LatestJPEGB64,
				}
				if data, err := json.Marshal(linePayload); err == nil {
					fmt.Fprintf(w, "event: rx_line\ndata: %s\n\n", data)
				}
			}
			// Send callsign if already decoded.
			if snap.Callsign != "" {
				cty := GetCallsignInfo(snap.Callsign)
				callPayload := map[string]interface{}{
					"event":    "rx_callsign",
					"callsign": snap.Callsign,
					"cty":      cty,
					"t":        snap.RxStartMs,
				}
				if data, err := json.Marshal(callPayload); err == nil {
					fmt.Fprintf(w, "event: rx_callsign\ndata: %s\n\n", data)
				}
			}
			flusher.Flush()
		}

		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case msg, ok := <-ch:
				if !ok {
					return
				}
				fmt.Fprint(w, msg)
				flusher.Flush()
			case <-ticker.C:
				fmt.Fprint(w, ": keepalive\n\n")
				flusher.Flush()
			case <-r.Context().Done():
				return
			}
		}
	})

	// POST /api/instances/{label}/frequency — retune a running instance
	// Body: {"freq_hz": 14230000}
	mux.HandleFunc("/api/instances/", func(w http.ResponseWriter, r *http.Request) {
		// Only handle .../frequency sub-path
		path := strings.TrimPrefix(r.URL.Path, "/api/instances/")
		parts := strings.SplitN(path, "/", 2)
		if len(parts) != 2 || parts[1] != "frequency" {
			http.NotFound(w, r)
			return
		}
		label := parts[0]
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !requiresAuth(w, r, uiPassword, sessions) {
			return
		}

		var body struct {
			FreqHz int `json:"freq_hz"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.FreqHz <= 0 {
			http.Error(w, "invalid body: expected {\"freq_hz\": <hz>}", http.StatusBadRequest)
			return
		}

		var target *instance
		for _, inst := range instances {
			if inst.label == label {
				target = inst
				break
			}
		}
		if target == nil {
			http.Error(w, "instance not found", http.StatusNotFound)
			return
		}

		log.Printf("[%s] retuning to %d Hz via web UI", target.label, body.FreqHz)
		target.setFrequency(body.FreqHz)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":      true,
			"freq_hz": body.FreqHz,
			"label":   target.label,
		})
	})

	// GET /api/status — instance status + receiver config
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		statuses := make([]map[string]interface{}, len(instances))
		for i, inst := range instances {
			statuses[i] = inst.statusSnapshot()
		}
		urlMu.RLock()
		url := *currentURL
		urlMu.RUnlock()
		resp := map[string]interface{}{
			"instances":    statuses,
			"receiver_lat": receiverLat,
			"receiver_lon": receiverLon,
			"ubersdr_url":  url,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	// GET /api/metrics?period=1h|24h|7d|30d — aggregated decode metrics
	mux.HandleFunc("/api/metrics", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		period := r.URL.Query().Get("period")
		result := ms.query(period)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		json.NewEncoder(w).Encode(result)
	})

	// GET /api/ubersdr/instances — server-side proxy to instances.ubersdr.org
	// Returns the raw JSON from https://instances.ubersdr.org/api/instances?online_only=true
	// so the browser doesn't need to make a cross-origin HTTPS request.
	mux.HandleFunc("/api/ubersdr/instances", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		const registryURL = "https://instances.ubersdr.org/api/instances?online_only=true"
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, registryURL, nil)
		if err != nil {
			http.Error(w, "failed to build upstream request", http.StatusInternalServerError)
			return
		}
		req.Header.Set("User-Agent", "ubersdr_qsstv/1.0")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			http.Error(w, "upstream request failed: "+err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body) //nolint:errcheck
	})

	// POST /api/config/url — update the UberSDR base URL and reconnect all instances
	// Body: {"url": "http://new-host:8080"}
	mux.HandleFunc("/api/config/url", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !requiresAuth(w, r, uiPassword, sessions) {
			return
		}
		var body struct {
			URL string `json:"url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.URL == "" {
			http.Error(w, "invalid body: expected {\"url\": \"<url>\"}", http.StatusBadRequest)
			return
		}
		// Validate: must be a well-formed http:// or https:// URL with a non-empty host.
		parsed, parseErr := url.Parse(body.URL)
		if parseErr != nil ||
			(parsed.Scheme != "http" && parsed.Scheme != "https") ||
			parsed.Host == "" {
			http.Error(w, "url must be a valid http:// or https:// URL with a host", http.StatusBadRequest)
			return
		}
		newURL := strings.TrimRight(body.URL, "/")

		urlMu.Lock()
		*currentURL = newURL
		urlMu.Unlock()

		log.Printf("ubersdr URL updated to %s — reconnecting %d instance(s)", newURL, len(instances))
		for _, inst := range instances {
			inst.setURL(newURL)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":  true,
			"url": newURL,
		})
	})

	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		TLSConfig:    tlsCfg,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // SSE connections are long-lived
		IdleTimeout:  120 * time.Second,
	}
	if tlsCfg != nil {
		// Cert and key are already embedded in tlsCfg — pass empty strings to
		// ListenAndServeTLS so it uses the TLSConfig.Certificates field.
		return srv.ListenAndServeTLS("", "")
	}
	return srv.ListenAndServe()
}
