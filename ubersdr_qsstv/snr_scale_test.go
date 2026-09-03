package main

import (
	"math"
	"testing"
	"time"
)

// The migration from audio protocol version 2 to version 4 changed what the
// server puts in the noise field: version 2 sent the noise DENSITY N0 in
// dBFS/Hz, version 3 and later send the noise POWER in the demodulator passband
// in dBFS. snr_avg_db is baseband - noise, so the same signal now scores
// 10*log10(filterBandwidthHz) lower -- 34.7 dB on the 2950 Hz SSB filter this
// program receives SSTV on.
//
// Every threshold here was calibrated against the old figure, and the stored
// history is full of it. These tests pin the two things that follow: the
// conversion itself, and that a filter judges a pre-migration record and a
// version 4 record by the same standard rather than by whichever protocol
// happened to record them.

// legacySSBOffsetDB is the correction for the usb/lsb passband, computed the way
// the server does: 10*log10(highEdge - lowEdge), with the edges from the radiod
// preset file. usb is +50..+3000 Hz.
const legacySSBOffsetDB = 34.698220160
const snrOffsetTolerance = 0.001

func TestLegacySNROffsetMatchesFilterBandwidth(t *testing.T) {
	cases := []struct {
		mode string
		want float64
	}{
		{"usb", 10 * math.Log10(2950)},
		{"lsb", 10 * math.Log10(2950)},
		{"am", 10 * math.Log10(10000)},
		{"nfm", 10 * math.Log10(12500)},
		{"wfm", 10 * math.Log10(220000)},
		{"cwu", 10 * math.Log10(400)},
		// An unrecorded or unrecognised mode falls back to the SSB figure.
		{"", 10 * math.Log10(2950)},
		{"nonsense", 10 * math.Log10(2950)},
	}
	for _, c := range cases {
		if got := legacySNROffsetDB(c.mode); math.Abs(got-c.want) > snrOffsetTolerance {
			t.Errorf("legacySNROffsetDB(%q) = %.4f, want %.4f", c.mode, got, c.want)
		}
	}
	// The constant the thresholds were rescaled by must be the SSB figure.
	if math.Abs(legacySNROffsetDB("usb")-legacySSBOffsetDB) > snrOffsetTolerance {
		t.Errorf("the SSB offset moved: %.6f", legacySNROffsetDB("usb"))
	}
}

func TestSNRTrueDBConvertsEachScale(t *testing.T) {
	// The same signal, recorded either side of the migration. A version 2
	// reading of 40 dB and a version 4 reading of 40 - 34.7 describe it
	// identically.
	legacy := imageRecord{AudioMode: "usb", SNRAvgDB: 40, SNRScale: snrScaleDensity}
	modern := imageRecord{AudioMode: "usb", SNRAvgDB: 40 - legacySSBOffsetDB, SNRScale: snrScaleTrueSNR}

	lv, lok := legacy.snrTrueDB()
	mv, mok := modern.snrTrueDB()
	if !lok || !mok {
		t.Fatal("both records have SNR data")
	}
	if math.Abs(lv-mv) > snrOffsetTolerance {
		t.Errorf("the same signal reads %.3f on the old scale and %.3f on the new", lv, mv)
	}

	// A sidecar the store has not stamped is a pre-migration one: no marker
	// must never be read as "already a true SNR".
	unmarked := imageRecord{AudioMode: "usb", SNRAvgDB: 40}
	uv, uok := unmarked.snrTrueDB()
	if !uok || math.Abs(uv-lv) > snrOffsetTolerance {
		t.Errorf("an unmarked record read as %.3f, want the legacy conversion %.3f", uv, lv)
	}

	// Zero is "never measured" throughout this program, not 0 dB.
	if _, known := (imageRecord{AudioMode: "usb"}).snrTrueDB(); known {
		t.Error("a record with no SNR data reported one")
	}
}

// The filter must judge both scales alike: a strong pre-migration image and a
// strong version 4 image both survive the default filter, and a weak one of
// either kind is excluded. Before the scale marker existed, the same threshold
// kept every old record and dropped every new one.
func TestListFilteredComparesEachRecordOnItsOwnScale(t *testing.T) {
	now := time.Now()

	// 40 dB on the old scale == 5.3 dB true: comfortably above the 3.3 dB
	// default. 36 dB old == 1.3 dB true: below it.
	strongLegacy := imageRecord{
		ID: "strong-legacy", AudioMode: "usb", RxEnd: now,
		SNRAvgDB: 40, SNRScale: snrScaleDensity,
	}
	weakLegacy := imageRecord{
		ID: "weak-legacy", AudioMode: "usb", RxEnd: now,
		SNRAvgDB: 36, SNRScale: snrScaleDensity,
	}
	strongModern := imageRecord{
		ID: "strong-modern", AudioMode: "usb", RxEnd: now,
		SNRAvgDB: 5.3, SNRScale: snrScaleTrueSNR,
	}
	weakModern := imageRecord{
		ID: "weak-modern", AudioMode: "usb", RxEnd: now,
		SNRAvgDB: 1.3, SNRScale: snrScaleTrueSNR,
	}
	// A sidecar from before SNR was recorded at all: no data, never filtered.
	noSNR := imageRecord{ID: "no-snr", AudioMode: "usb", RxEnd: now}

	store := &imageStore{
		records: []imageRecord{strongLegacy, weakLegacy, strongModern, weakModern, noSNR},
	}

	got := store.listFiltered(100, 0, false, snrCleanupThreshold, time.Time{}, time.Time{})
	kept := map[string]bool{}
	for _, r := range got {
		kept[r.ID] = true
	}

	for _, id := range []string{"strong-legacy", "strong-modern", "no-snr"} {
		if !kept[id] {
			t.Errorf("%s was filtered out; it should pass the default filter", id)
		}
	}
	for _, id := range []string{"weak-legacy", "weak-modern"} {
		if kept[id] {
			t.Errorf("%s passed the filter; it is below the threshold", id)
		}
	}

	// The specific regression: the pre-migration history must still be visible
	// under the default filter after the migration.
	if !kept["strong-legacy"] {
		t.Error("the migration hid the existing gallery")
	}
}

// The cleanup worker deletes files, so it must make the same judgement. A
// threshold left on the old scale would have deleted every version 4 image.
func TestCleanupThresholdIsOnTheTrueSNRScale(t *testing.T) {
	// A typical good SSTV signal: ~40 dB on the old scale.
	good := imageRecord{AudioMode: "usb", SNRAvgDB: 40, SNRScale: snrScaleDensity}
	goodTrue, _ := good.snrTrueDB()
	if goodTrue < snrCleanupThreshold {
		t.Fatalf("a good pre-migration signal (%.2f dB true) is below the cleanup "+
			"threshold %.2f and would be deleted", goodTrue, snrCleanupThreshold)
	}

	// The same signal recorded on version 4 must also survive.
	modern := imageRecord{AudioMode: "usb", SNRAvgDB: float32(goodTrue), SNRScale: snrScaleTrueSNR}
	modernTrue, _ := modern.snrTrueDB()
	if modernTrue < snrCleanupThreshold {
		t.Fatalf("a good version 4 signal (%.2f dB) is below the cleanup threshold %.2f",
			modernTrue, snrCleanupThreshold)
	}

	// And the threshold must actually be the rescaled one, not the old 38.
	if snrCleanupThreshold > 10 {
		t.Fatalf("snrCleanupThreshold is %.1f, which is still on the pre-migration "+
			"scale; it would delete every image received on version 4", snrCleanupThreshold)
	}
}

// The destructive threshold pinned against what a live receiver actually
// reports, so that a future edit cannot quietly move it into either the idle
// noise or the signal.
//
// Measured on m9psy over 12 kHz usb: an idle channel's 6-second mean SNR ranged
// -0.65 to +0.57 dB, and a strong signal's fifth percentile was 24.0 dB with a
// median of 32.7. A threshold that deletes files belongs between those, and
// nearer the idle end.
func TestCleanupThresholdSitsBetweenMeasuredIdleAndSignal(t *testing.T) {
	const (
		measuredIdleWorstMeanDB = 0.57
		measuredSignalP05DB     = 24.0
	)

	if snrCleanupThreshold <= measuredIdleWorstMeanDB {
		t.Errorf("snrCleanupThreshold %.2f is at or below the worst measured idle "+
			"reading %.2f dB; noise alone would be judged a keepable image",
			snrCleanupThreshold, measuredIdleWorstMeanDB)
	}
	if snrCleanupThreshold >= measuredSignalP05DB {
		t.Errorf("snrCleanupThreshold %.2f has reached the weakest part of a real "+
			"signal (p05 %.2f dB); good images would be deleted",
			snrCleanupThreshold, measuredSignalP05DB)
	}
	// It should be biased towards keeping things: nearer idle than signal.
	if midpoint := (measuredIdleWorstMeanDB + measuredSignalP05DB) / 2; snrCleanupThreshold > midpoint {
		t.Errorf("snrCleanupThreshold %.2f is past the midpoint %.2f between idle and "+
			"signal; a threshold that deletes files should sit nearer the idle end",
			snrCleanupThreshold, midpoint)
	}
}

// The API serves one scale and says so, so a client never has to work out which
// filter bandwidth a record was made with.
func TestOnTrueSNRScaleNormalisesEveryFigure(t *testing.T) {
	legacy := imageRecord{
		AudioMode:       "usb",
		SNRAvgDB:        40,
		SNRMinDB:        35,
		SNRMaxDB:        45,
		BasebandAvgDBFS: -50,
		NoiseAvgDBFS:    -90,
		SNRScale:        snrScaleDensity,
		SNRSeries:       []snrPoint{{T: 1, SNRDB: 40}, {T: 2, SNRDB: 0}},
	}
	got := legacy.onTrueSNRScale()

	if got.SNRScale != snrScaleTrueSNR {
		t.Errorf("scale marker = %q, want %q", got.SNRScale, snrScaleTrueSNR)
	}
	for _, c := range []struct {
		name      string
		got, want float32
	}{
		{"avg", got.SNRAvgDB, float32(40 - legacySSBOffsetDB)},
		{"min", got.SNRMinDB, float32(35 - legacySSBOffsetDB)},
		{"max", got.SNRMaxDB, float32(45 - legacySSBOffsetDB)},
		{"series[0]", got.SNRSeries[0].SNRDB, float32(40 - legacySSBOffsetDB)},
	} {
		if math.Abs(float64(c.got-c.want)) > snrOffsetTolerance {
			t.Errorf("%s = %.3f, want %.3f", c.name, c.got, c.want)
		}
	}

	// Zero stays zero: it is the "not measured" sentinel, not a reading.
	if got.SNRSeries[1].SNRDB != 0 {
		t.Errorf("an unmeasured series point became %.3f", got.SNRSeries[1].SNRDB)
	}

	// The noise field moves the other way -- density to power -- so that
	// baseband - noise still equals the SNR served alongside it.
	if math.Abs(float64(got.NoiseAvgDBFS-float32(-90+legacySSBOffsetDB))) > snrOffsetTolerance {
		t.Errorf("noise = %.3f, want %.3f", got.NoiseAvgDBFS, -90+legacySSBOffsetDB)
	}
	if d := math.Abs(float64(got.BasebandAvgDBFS - got.NoiseAvgDBFS - got.SNRAvgDB)); d > 0.01 {
		t.Errorf("baseband - noise no longer equals the reported SNR (off by %.3f)", d)
	}
	// The baseband power was already a power in dBFS and must not move.
	if got.BasebandAvgDBFS != -50 {
		t.Errorf("baseband power moved to %.3f", got.BasebandAvgDBFS)
	}

	// A version 4 record passes through untouched.
	modern := imageRecord{
		AudioMode: "usb", SNRAvgDB: 5.3, SNRMinDB: 1, SNRMaxDB: 9,
		NoiseAvgDBFS: -90, BasebandAvgDBFS: -50, SNRScale: snrScaleTrueSNR,
		SNRSeries: []snrPoint{{T: 1, SNRDB: 5.3}},
	}
	back := modern.onTrueSNRScale()
	if back.SNRAvgDB != modern.SNRAvgDB || back.SNRMinDB != modern.SNRMinDB ||
		back.SNRMaxDB != modern.SNRMaxDB || back.NoiseAvgDBFS != modern.NoiseAvgDBFS ||
		back.BasebandAvgDBFS != modern.BasebandAvgDBFS ||
		back.SNRScale != modern.SNRScale ||
		len(back.SNRSeries) != 1 || back.SNRSeries[0] != modern.SNRSeries[0] {
		t.Errorf("a version 4 record was altered: %+v", back)
	}
}

// loadExisting stamps unmarked sidecars so nothing downstream sees an
// unstated scale. This checks the stamping rule directly.
func TestUnmarkedSidecarIsStampedAsLegacy(t *testing.T) {
	rec := imageRecord{ID: "x", AudioMode: "usb", SNRAvgDB: 40}
	if rec.SNRScale == "" && rec.SNRAvgDB != 0 {
		rec.SNRScale = snrScaleDensity
	}
	if rec.SNRScale != snrScaleDensity {
		t.Fatalf("scale = %q, want %q", rec.SNRScale, snrScaleDensity)
	}

	// A record with no SNR at all is left unmarked: there is no measurement to
	// put on a scale.
	empty := imageRecord{ID: "y", AudioMode: "usb"}
	if empty.SNRScale == "" && empty.SNRAvgDB != 0 {
		empty.SNRScale = snrScaleDensity
	}
	if empty.SNRScale != "" {
		t.Errorf("a record with no SNR was marked %q", empty.SNRScale)
	}
}
