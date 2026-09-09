package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// metricRow — one compact record written to metrics.jsonl per decode
// ---------------------------------------------------------------------------

type metricRow struct {
	T      int64  `json:"t"` // Unix ms of rx_end
	Mode   string `json:"mode"`
	FreqHz int    `json:"freq_hz"`
	// AudioMode is the receiver's audio mode ("usb", "lsb", …), not the SSTV
	// mode in Mode. Together with FreqHz it names the channel the decode came
	// from — freq_hz alone is not unique, since two instances may share a
	// frequency with different audio modes. Rows appended from now on carry it;
	// rows already in metrics.jsonl predate the field and leave it empty.
	AudioMode    string  `json:"audio_mode,omitempty"`
	SNRAvgDB     float32 `json:"snr_avg_db"`
	LinesDecoded int     `json:"lines_decoded"`
	ImageHeight  int     `json:"image_height"`
	Complete     bool    `json:"complete"` // lines_decoded >= 95% of image_height (and image_height > 0)
	// SNRScale states which scale SNRAvgDB is on, exactly as on imageRecord.
	// Rows appended from now on are snrScaleTrueSNR; rows already in
	// metrics.jsonl carry no marker and are the pre-migration S/N0 figure.
	// Those legacy rows are also the ones without an AudioMode, so the
	// correction below uses the default SSB bandwidth. See snr_scale.go.
	SNRScale string `json:"snr_scale,omitempty"`
}

// channelLabel is the row's channel identity: the owning instance's label
// ("14230000_usb") when the audio mode is known, and the bare frequency
// ("14230000") for a legacy row written before audio_mode was recorded. The two
// never collide, so a legacy row is bucketed on its own rather than being
// silently attributed to one of the audio modes on that frequency.
func (r metricRow) channelLabel() string {
	if r.AudioMode == "" {
		return fmt.Sprintf("%d", r.FreqHz)
	}
	return fmt.Sprintf("%d_%s", r.FreqHz, r.AudioMode)
}

// snrTrueDB returns the row's SNR on the true-SNR scale, and whether it is
// known. It is the metricRow counterpart of imageRecord.snrTrueDB.
func (r metricRow) snrTrueDB() (float64, bool) {
	if r.SNRAvgDB == 0 {
		return 0, false
	}
	if r.SNRScale == snrScaleTrueSNR {
		return float64(r.SNRAvgDB), true
	}
	return float64(r.SNRAvgDB) - legacySNROffsetDB(""), true
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

// channelBucket is one entry of the by_channel breakdown: the decodes that came
// from a single radio channel within the queried period.
type channelBucket struct {
	// Label is the bucket key — see metricRow.channelLabel. It is the instance
	// label ("14230000_usb") for rows that record an audio mode, and the bare
	// frequency ("14230000") for legacy rows that do not.
	Label     string  `json:"label"`
	FreqHz    int     `json:"freq_hz"`
	AudioMode string  `json:"audio_mode"` // "" for a legacy row's bucket
	Count     int     `json:"count"`
	Complete  int     `json:"complete"`
	Partial   int     `json:"partial"`
	AvgSNRDB  float32 `json:"avg_snr_db"` // true-SNR scale, 0 when no row in the bucket has a known SNR
}

type metricsQueryResult struct {
	Period    string                 `json:"period"`
	Total     int                    `json:"total"`
	Complete  int                    `json:"complete"`
	Partial   int                    `json:"partial"`
	AvgSNRDB  float32                `json:"avg_snr_db"`
	ByMode    map[string]int         `json:"by_mode"`
	ByHour    []hourBucket           `json:"by_hour"`
	ByChannel []channelBucket        `json:"by_channel"`
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
		AudioMode:    rec.AudioMode,
		SNRAvgDB:     rec.SNRAvgDB,
		LinesDecoded: rec.LinesDecoded,
		ImageHeight:  rec.ImageHeight,
		Complete:     complete,
		SNRScale:     rec.SNRScale,
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

// query returns aggregated metrics for the given period string, across every
// channel. It is queryFiltered with no channel constraint.
func (ms *metricsStore) query(period string) metricsQueryResult {
	return ms.queryFiltered(period, channelFilter{})
}

// queryFiltered returns aggregated metrics for the given period string,
// counting only rows the channel filter admits (a zero filter admits all).
// Note that a filter naming an audio mode never admits a legacy row, since such
// a row does not record one; a frequency-only filter does. See channelFilter.
func (ms *metricsStore) queryFiltered(period string, ch channelFilter) metricsQueryResult {
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

	// Bucket map: channel label → *channelBucket, plus its running SNR sum.
	chanBuckets := make(map[string]*channelBucket)
	chanSNRSum := make(map[string]float64)
	chanSNRCount := make(map[string]int)

	var snrSum float64
	var snrCount int

	for _, row := range ms.rows {
		if row.T < sinceMs {
			continue
		}
		if !ch.matches(row.FreqHz, row.AudioMode) {
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
		// Average on one scale. Rows written before the audio protocol version
		// 4 migration hold the S/N0 figure, which is tens of dB higher than a
		// true SNR; summing the two kinds together would produce a mean that
		// describes neither.
		snr, snrKnown := row.snrTrueDB()
		if snrKnown {
			snrSum += snr
			snrCount++
		}

		// Per-channel bucket, keyed as metricRow.channelLabel describes. The
		// same snrTrueDB accessor as the aggregate above, so a channel mean and
		// the overall mean are on one scale.
		cb, ok := chanBuckets[row.channelLabel()]
		if !ok {
			cb = &channelBucket{
				Label:     row.channelLabel(),
				FreqHz:    row.FreqHz,
				AudioMode: row.AudioMode,
			}
			chanBuckets[cb.Label] = cb
		}
		cb.Count++
		if row.Complete {
			cb.Complete++
		} else {
			cb.Partial++
		}
		if snrKnown {
			chanSNRSum[cb.Label] += snr
			chanSNRCount[cb.Label]++
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

	// by_channel: busiest channel first, ties broken by label so the order is
	// stable between polls.
	result.ByChannel = make([]channelBucket, 0, len(chanBuckets))
	for _, cb := range chanBuckets {
		if n := chanSNRCount[cb.Label]; n > 0 {
			cb.AvgSNRDB = float32(math.Round(chanSNRSum[cb.Label]/float64(n)*10) / 10)
		}
		result.ByChannel = append(result.ByChannel, *cb)
	}
	sort.Slice(result.ByChannel, func(i, j int) bool {
		if result.ByChannel[i].Count != result.ByChannel[j].Count {
			return result.ByChannel[i].Count > result.ByChannel[j].Count
		}
		return result.ByChannel[i].Label < result.ByChannel[j].Label
	})

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
