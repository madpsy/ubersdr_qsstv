package main

import (
	"bufio"
	"encoding/json"
	"log"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// metricRow — one compact record written to metrics.jsonl per decode
// ---------------------------------------------------------------------------

type metricRow struct {
	T            int64   `json:"t"`             // Unix ms of rx_end
	Mode         string  `json:"mode"`
	FreqHz       int     `json:"freq_hz"`
	SNRAvgDB     float32 `json:"snr_avg_db"`
	LinesDecoded int     `json:"lines_decoded"`
	ImageHeight  int     `json:"image_height"`
	Complete     bool    `json:"complete"` // lines_decoded >= 95% of image_height (and image_height > 0)
}

// ---------------------------------------------------------------------------
// metricsQueryResult — returned by query() and serialised to JSON for the API
// ---------------------------------------------------------------------------

type hourBucket struct {
	T        int64 `json:"t"`         // Unix ms of the hour start (floor to hour)
	Count    int   `json:"count"`     // total decodes in this hour
	Complete int   `json:"complete"`  // complete decodes
	Partial  int   `json:"partial"`   // partial decodes
}

type metricsQueryResult struct {
	Period    string                 `json:"period"`
	Total     int                    `json:"total"`
	Complete  int                    `json:"complete"`
	Partial   int                    `json:"partial"`
	AvgSNRDB  float32                `json:"avg_snr_db"`
	ByMode    map[string]int         `json:"by_mode"`
	ByHour    []hourBucket           `json:"by_hour"`
}

// ---------------------------------------------------------------------------
// metricsStore
// ---------------------------------------------------------------------------

type metricsStore struct {
	mu   sync.RWMutex
	rows []metricRow
	f    *os.File // opened in O_APPEND|O_CREATE|O_WRONLY mode; nil if unavailable
}

// newMetricsStore opens (or creates) metrics.jsonl in outputDir for appending.
func newMetricsStore(outputDir string) *metricsStore {
	path := filepath.Join(outputDir, "metrics.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("metricsStore: could not open %s for appending: %v — metrics will not be persisted", path, err)
		return &metricsStore{}
	}
	return &metricsStore{f: f}
}

// backfillFromStore populates metrics.jsonl from existing imageStore records when
// the metrics log is empty (e.g. first run after upgrade).  It is a no-op if
// metrics.jsonl already contains data.
func (ms *metricsStore) backfillFromStore(store *imageStore) {
	ms.mu.RLock()
	alreadyLoaded := len(ms.rows)
	ms.mu.RUnlock()

	if alreadyLoaded > 0 {
		return // metrics.jsonl already has data — nothing to backfill
	}

	store.mu.RLock()
	records := make([]imageRecord, len(store.records))
	copy(records, store.records)
	store.mu.RUnlock()

	if len(records) == 0 {
		return
	}

	log.Printf("metricsStore: backfilling %d records from existing sidecars", len(records))
	// store.records is newest-first; append in reverse so metrics.jsonl is
	// chronological (oldest first), matching the natural append order going forward.
	for i := len(records) - 1; i >= 0; i-- {
		ms.append(records[i])
	}
}

// load reads metrics.jsonl from outputDir into memory.
// Call once at startup before any append() calls.
func (ms *metricsStore) load(outputDir string) {
	path := filepath.Join(outputDir, "metrics.jsonl")
	f, err := os.Open(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("metricsStore.load: %v", err)
		}
		return // first run — file doesn't exist yet
	}
	defer f.Close()

	var loaded int
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var row metricRow
		if err := json.Unmarshal(line, &row); err != nil {
			log.Printf("metricsStore.load: parse error: %v", err)
			continue
		}
		ms.rows = append(ms.rows, row)
		loaded++
	}
	log.Printf("metricsStore: loaded %d rows from %s", loaded, path)
}

// append adds a new row to the in-memory slice and writes it to metrics.jsonl.
func (ms *metricsStore) append(rec imageRecord) {
	complete := rec.ImageHeight > 0 && rec.LinesDecoded*100 >= rec.ImageHeight*95
	row := metricRow{
		T:            rec.RxEnd.UnixMilli(),
		Mode:         rec.SSTVMode,
		FreqHz:       rec.FrequencyHz,
		SNRAvgDB:     rec.SNRAvgDB,
		LinesDecoded: rec.LinesDecoded,
		ImageHeight:  rec.ImageHeight,
		Complete:     complete,
	}

	ms.mu.Lock()
	ms.rows = append(ms.rows, row)
	ms.mu.Unlock()

	if ms.f != nil {
		data, err := json.Marshal(row)
		if err != nil {
			log.Printf("metricsStore.append: marshal: %v", err)
			return
		}
		data = append(data, '\n')
		if _, err := ms.f.Write(data); err != nil {
			log.Printf("metricsStore.append: write: %v", err)
		}
	}
}

// parsePeriod converts a period string ("1h", "24h", "7d", "30d") to a
// time.Time representing the start of the window (now - period).
// Unknown values default to 24 h.
func parsePeriod(period string) (since time.Time, label string) {
	now := time.Now()
	switch period {
	case "1h":
		return now.Add(-1 * time.Hour), "1h"
	case "7d":
		return now.Add(-7 * 24 * time.Hour), "7d"
	case "30d":
		return now.Add(-30 * 24 * time.Hour), "30d"
	default:
		return now.Add(-24 * time.Hour), "24h"
	}
}

// query returns aggregated metrics for the given period string.
func (ms *metricsStore) query(period string) metricsQueryResult {
	since, label := parsePeriod(period)
	sinceMs := since.UnixMilli()

	ms.mu.RLock()
	defer ms.mu.RUnlock()

	result := metricsQueryResult{
		Period: label,
		ByMode: make(map[string]int),
	}

	// Bucket map: hour-start-ms → *hourBucket
	buckets := make(map[int64]*hourBucket)

	var snrSum float64
	var snrCount int

	for _, row := range ms.rows {
		if row.T < sinceMs {
			continue
		}
		result.Total++
		if row.Complete {
			result.Complete++
		} else {
			result.Partial++
		}
		if row.Mode != "" {
			result.ByMode[row.Mode]++
		}
		if row.SNRAvgDB != 0 {
			snrSum += float64(row.SNRAvgDB)
			snrCount++
		}

		// Floor to hour
		hourMs := (row.T / 3_600_000) * 3_600_000
		b, ok := buckets[hourMs]
		if !ok {
			b = &hourBucket{T: hourMs}
			buckets[hourMs] = b
		}
		b.Count++
		if row.Complete {
			b.Complete++
		} else {
			b.Partial++
		}
	}

	if snrCount > 0 {
		result.AvgSNRDB = float32(math.Round(float64(snrSum)/float64(snrCount)*10) / 10)
	}

	// Sort buckets by time
	result.ByHour = make([]hourBucket, 0, len(buckets))
	for _, b := range buckets {
		result.ByHour = append(result.ByHour, *b)
	}
	// Simple insertion sort (bucket count is small — at most 720 for 30d)
	for i := 1; i < len(result.ByHour); i++ {
		key := result.ByHour[i]
		j := i - 1
		for j >= 0 && result.ByHour[j].T > key.T {
			result.ByHour[j+1] = result.ByHour[j]
			j--
		}
		result.ByHour[j+1] = key
	}

	return result
}
