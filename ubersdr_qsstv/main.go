// ubersdr_qsstv — Connect to UberSDR, pipe demodulated audio to qsstv --headless,
// collect decoded SSTV images with SNR metadata, and serve a web gallery.
package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// channelFlag is a repeatable -channel flag value.
type channelFlag []string

func (c *channelFlag) String() string { return strings.Join(*c, ",") }
func (c *channelFlag) Set(v string) error {
	*c = append(*c, v)
	return nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envIntOr(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envFloat64Or(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func main() {
	var channels channelFlag

	rawURL := flag.String("url", envOr("UBERSDR_URL", "http://ubersdr:8080"),
		"UberSDR base URL (env: UBERSDR_URL)")
	flag.Var(&channels, "channel",
		"freq:mode pair, e.g. 14230000:usb (repeatable; env: UBERSDR_CHANNELS)")
	outputDir := flag.String("output-dir", envOr("OUTPUT_DIR", "/data"),
		"Output directory for images and metadata (env: OUTPUT_DIR)")
	pass := flag.String("pass", envOr("UBERSDR_PASS", ""),
		"UberSDR bypass password (env: UBERSDR_PASS)")
	qsstvBin := flag.String("qsstv", envOr("QSSTV_BIN", "qsstv"),
		"Path to qsstv binary (env: QSSTV_BIN)")
	ctyFile := flag.String("cty-file", envOr("CTY_FILE", ""),
		"Path to CTY.DAT override (default: embedded; env: CTY_FILE)")
	webPort := flag.Int("web-port", envIntOr("WEB_PORT", 6091),
		"Web UI port (0 = disabled; env: WEB_PORT)")
	useTLS := flag.Bool("tls", os.Getenv("WEB_TLS") == "1",
		"Enable HTTPS with auto-generated self-signed cert (required for audio output device selection; env: WEB_TLS=1)")
	receiverLat := flag.Float64("receiver-lat", envFloat64Or("RECEIVER_LAT", 0.0),
		"Receiver latitude for origin map (env: RECEIVER_LAT)")
	receiverLon := flag.Float64("receiver-lon", envFloat64Or("RECEIVER_LON", 0.0),
		"Receiver longitude for origin map (env: RECEIVER_LON)")
	uiPassword := flag.String("ui-password", envOr("UI_PASSWORD", ""),
		"Password required for write actions in the web UI (env: UI_PASSWORD; empty = write actions disabled)")

	flag.Parse()

	// Merge UBERSDR_CHANNELS env var into channels slice (only if no -channel flags given)
	if envCh := os.Getenv("UBERSDR_CHANNELS"); envCh != "" && len(channels) == 0 {
		for _, ch := range strings.Split(envCh, ",") {
			ch = strings.TrimSpace(ch)
			if ch != "" {
				channels = append(channels, ch)
			}
		}
	}

	if len(channels) == 0 {
		fmt.Fprintf(os.Stderr, "error: at least one -channel freq:mode is required\n\n")
		fmt.Fprintf(os.Stderr, "Usage: ubersdr_qsstv [flags]\n\n")
		fmt.Fprintf(os.Stderr, "  -url          string   UberSDR base URL (default: http://ubersdr:8080)\n")
		fmt.Fprintf(os.Stderr, "  -channel      freq:mode  e.g. -channel 14230000:usb (repeatable)\n")
		fmt.Fprintf(os.Stderr, "  -output-dir   string   Output directory (default: /data)\n")
		fmt.Fprintf(os.Stderr, "  -pass         string   UberSDR bypass password\n")
		fmt.Fprintf(os.Stderr, "  -qsstv        string   Path to qsstv binary (default: qsstv)\n")
		fmt.Fprintf(os.Stderr, "  -cty-file     string   CTY.DAT override (default: embedded)\n")
		fmt.Fprintf(os.Stderr, "  -web-port     int      Web UI port (default: 6091, 0=disabled)\n")
		fmt.Fprintf(os.Stderr, "  -tls                   Enable HTTPS with auto-generated self-signed cert\n")
		fmt.Fprintf(os.Stderr, "                         (required for audio output device selection in Chrome/Edge)\n")
		fmt.Fprintf(os.Stderr, "  -receiver-lat float    Receiver latitude\n")
		fmt.Fprintf(os.Stderr, "  -receiver-lon float    Receiver longitude\n")
		fmt.Fprintf(os.Stderr, "  -ui-password  string   Password for write actions in the web UI (empty = disabled)\n\n")
		fmt.Fprintf(os.Stderr, "Environment variables: UBERSDR_URL, UBERSDR_CHANNELS, OUTPUT_DIR,\n")
		fmt.Fprintf(os.Stderr, "  UBERSDR_PASS, QSSTV_BIN, CTY_FILE, WEB_PORT, WEB_TLS, RECEIVER_LAT, RECEIVER_LON,\n")
		fmt.Fprintf(os.Stderr, "  UI_PASSWORD\n\n")
		fmt.Fprintf(os.Stderr, "Example:\n")
		fmt.Fprintf(os.Stderr, "  ubersdr_qsstv -url http://sdr.example.com:8080 \\\n")
		fmt.Fprintf(os.Stderr, "                -channel 14230000:usb \\\n")
		fmt.Fprintf(os.Stderr, "                -channel 21335000:usb \\\n")
		fmt.Fprintf(os.Stderr, "                -output-dir /var/sstv/rx\n")
		os.Exit(1)
	}

	// Ensure output directory exists
	if err := os.MkdirAll(*outputDir, 0755); err != nil {
		log.Fatalf("create output dir %s: %v", *outputDir, err)
	}

	// Load CTY database
	if err := InitCTYDatabase(*ctyFile); err != nil {
		log.Printf("warning: CTY database load failed (%v) — callsign geo-lookup disabled", err)
	}

	// Parse channel specs
	type chanSpec struct {
		freqHz    int
		audioMode string
	}
	var specs []chanSpec
	for _, ch := range channels {
		parts := strings.SplitN(ch, ":", 2)
		if len(parts) != 2 {
			log.Fatalf("invalid -channel %q: expected freq:mode", ch)
		}
		freqHz, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil || freqHz <= 0 {
			log.Fatalf("invalid frequency in -channel %q: %v", ch, err)
		}
		mode := strings.TrimSpace(parts[1])
		if mode == "" {
			log.Fatalf("empty mode in -channel %q", ch)
		}
		specs = append(specs, chanSpec{freqHz: freqHz, audioMode: mode})
	}

	log.Printf("ubersdr_qsstv starting: %d channel(s), output=%s, web-port=%d",
		len(specs), *outputDir, *webPort)

	// Shared fan-in event channel → image store
	eventCh := make(chan imageRecord, 256)

	// Build image store first so we can hand the SSE hub to instances
	store := newImageStore(*outputDir)
	store.loadExisting()

	// Build metrics store — loads existing metrics.jsonl from disk.
	// If the file is absent or empty (first run after upgrade), backfill from
	// the existing .json sidecars that imageStore already loaded.
	ms := newMetricsStore(*outputDir)
	ms.load(*outputDir)
	ms.backfillFromStore(store)

	// Shared mutable URL — updated by POST /api/config/url and read by /api/status.
	currentURL := *rawURL
	var urlMu sync.RWMutex

	// Build and start instances
	instances := make([]*instance, len(specs))
	for i, spec := range specs {
		inst := newInstance(spec.freqHz, spec.audioMode, *rawURL, *pass, *outputDir, *qsstvBin, eventCh, ms)
		inst.sseHub = store.sseHub // wire live SNR broadcasts
		instances[i] = inst
		// Use restart() so the initial loopCancel is set consistently.
		inst.mu.Lock()
		inst.restart() // releases the lock internally
		log.Printf("started instance %s", inst.label)
	}

	// Fan-in goroutine: read from eventCh → update image store
	go func() {
		for rec := range eventCh {
			store.add(rec)
		}
	}()

	// Start web server
	if *webPort > 0 {
		go func() {
			addr := fmt.Sprintf(":%d", *webPort)
			var tlsCfg *tls.Config
			if *useTLS {
				var err error
				tlsCfg, err = selfSignedTLSConfig(*outputDir)
				if err != nil {
					log.Fatalf("TLS setup: %v", err)
				}
				log.Printf("web UI listening on https://0.0.0.0%s (self-signed cert in %s)", addr, *outputDir)
				log.Printf("  → accept the certificate warning in your browser once, then use https://")
			} else {
				log.Printf("web UI listening on http://0.0.0.0%s", addr)
			}
			if err := startWebServer(addr, store, instances, *outputDir, *receiverLat, *receiverLon, tlsCfg, &currentURL, &urlMu, ms, *uiPassword); err != nil {
				log.Fatalf("web server: %v", err)
			}
		}()
	}

	// Handle SIGINT / SIGTERM
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	<-sigs
	log.Println("shutting down…")

	for _, inst := range instances {
		inst.stop()
	}
}
