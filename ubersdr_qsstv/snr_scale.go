package main

import "math"

// SNR scales, and why there are two of them
// =========================================
//
// snr_avg_db is `basebandDBFS - noiseDBFS`, but the server changed what it puts
// in the noise field, so that subtraction changed meaning under this program's
// feet when it moved from audio protocol version 2 to version 4.
//
//   - Version 2 sent radiod's NOISE_DENSITY unchanged: N0, a power spectral
//     DENSITY in dBFS/Hz. Baseband power is a power in dBFS integrated over the
//     whole passband, so subtracting one from the other gives S/N0 in dB·Hz --
//     not an SNR, and a figure that moved whenever the filter width did.
//   - Version 3 and later send channelNoisePower(N0, filterBandwidthHz), the
//     noise POWER inside the demodulator passband in dBFS. That is in the same
//     units as the signal, so the subtraction is a real SNR.
//
// (ka9q_ubersdr/websocket.go picks between the two on `version >= 3`;
// ka9q_ubersdr/radiod_status.go documents channelNoisePower.)
//
// The two differ by 10·log10(filterBandwidthHz): a version 4 reading is LOWER
// than a version 2 reading of the same signal, by ~34.7 dB on an SSB filter.
// Every threshold in this program was calibrated against the version 2 figure,
// so the migration would have silently hidden and then DELETED every new image
// (see snrCleanupThreshold in cleanup.go) unless both the thresholds and the
// stored history were dealt with.
//
// The history is dealt with by recording which scale each measurement is on.
// Records written before the migration carry no marker; loadExisting stamps
// them snrScaleDensity so that no record anywhere is on an unstated scale, and
// snrTrueDB converts either kind to the version 4 scale for comparison. All
// thresholds in this program are authored on that scale.

// MEASURED, not assumed
// ----------------------
// The thresholds below were converted arithmetically from the old ones, then
// checked against a live receiver (m9psy, 2026-09-03, ~9000 packets over seven
// captures) so that they rest on observation rather than on the derivation
// alone:
//
//	band / mode                 noise dBFS    SNR dB (min .. max, median)
//	14.230 MHz usb, quiet      -113.7        -5.1 ..  9.3   (-0.4)
//	14.230 MHz usb, signal     -114.1        -3.7 .. 14.0   ( 5.7)
//	 7.043 MHz usb             -109.4        -4.5 ..  8.9   (-1.5)
//	 3.730 MHz lsb             -111.1        -2.6 .. 10.2   (-1.0)
//	13.000 MHz usb (empty)     -116.1        -2.1 .. 10.1   (-0.4)
//	  909 kHz am (broadcast)    -84.0        40.9 .. 54.8   (45.3)
//
// and, on 12 kHz usb through this addon's own decoder (~1500 packets each):
//
//	idle channel                            6 s means -0.65 .. +0.57
//	strong signal              -103..-123   p05 24.0, median 32.7, max 42.4
//
// So idle reads about 0 dB and a working signal about 30. The display ranges in
// static/app.js are set from those two figures directly; deriving them instead
// by shifting the pre-migration window put their top at 15.3 dB, which hid the
// whole 15-42 dB band that real signals occupy.
//
// Three things matter for anyone re-tuning these numbers:
//
//   - There is no floor on the noise reading. It was measured between -79 and
//     -125 dBFS and never came near the -30 an earlier build was said to clamp
//     at; the encoder's only clamp is at ±327.67 dB (PCMQualityFromFloat),
//     which nothing approaches.
//   - On an empty channel the noise reads within about a dB of the baseband
//     power. That is only possible if the field is a power in the passband; a
//     density N0 would sit near -148 dBFS/Hz on a 2950 Hz filter. So the
//     version 2 to version 4 unit change this file corrects for is real, and
//     10·log10(bandwidth) is its size.
//   - A DECISION threshold and a DISPLAY range are set differently here, on
//     purpose. The decision thresholds (snrCleanupThreshold, the gallery
//     minimum) are the old ones minus the unit correction, so the existing
//     gallery keeps the operator's original quality bar; the measurements
//     confirm they still fall between idle and signal. The display ranges are
//     set from the measurements directly, because shifting the pre-migration
//     window carried its clamped-era error along and produced a ramp that
//     saturated at 15 dB.

const (
	// snrScaleTrueSNR marks snr_avg_db as a real SNR in dB: the version 3+
	// noise power subtracted from the baseband power.
	snrScaleTrueSNR = "snr"

	// snrScaleDensity marks snr_avg_db as the pre-migration S/N0 figure in
	// dB·Hz: the version 2 noise DENSITY subtracted from the baseband power.
	// It reads 10·log10(filter bandwidth) too high.
	snrScaleDensity = "s/n0"
)

// legacySNRBandwidthHz is the demodulator passband width in Hz for each audio
// mode this program can be pointed at, from the filter edges in the radiod
// preset file (ubersdr-radiod/config/presets.conf), which is what radiod
// reports back as LOW_EDGE/HIGH_EDGE and therefore what
// ChannelStatus.FilterBandwidthHz returned when the old figures were recorded.
//
//	usb   +50 .. +3000      2950 Hz    ->  34.70 dB
//	lsb  -3000 ..   -50     2950 Hz    ->  34.70 dB
//	am   -5000 .. +5000    10000 Hz    ->  40.00 dB
//	nfm  -6250 .. +6250    12500 Hz    ->  40.97 dB
//	fm   -8000 .. +8000    16000 Hz    ->  42.04 dB
//	wfm -110000 .. +110000 220000 Hz   ->  53.42 dB
//	cwu  -200 ..  +200       400 Hz    ->  26.02 dB
//
// This table is consulted only for records written before the migration, whose
// sidecars did record the audio mode but not the filter width. Records written
// from now on carry their scale and need no correction at all, so the table
// does not have to track future preset changes -- only what the presets were
// when those old records were made.
var legacySNRBandwidthHz = map[string]float64{
	"usb": 2950,
	"lsb": 2950,
	"am":  10000,
	"nfm": 12500,
	"fm":  16000,
	"wfm": 220000,
	"cwu": 400,
	"cwl": 400,
}

// legacySNRDefaultBandwidthHz is used for a pre-migration record whose audio
// mode is missing or unrecognised. SSTV is received on usb almost without
// exception, and an unknown mode is far more likely to be an SSB variant than
// anything else on the list.
const legacySNRDefaultBandwidthHz = 2950

// legacySNROffsetDB is how much higher a pre-migration S/N0 reading is than the
// true SNR of the same signal: 10·log10 of the passband width.
func legacySNROffsetDB(audioMode string) float64 {
	bw, ok := legacySNRBandwidthHz[audioMode]
	if !ok {
		bw = legacySNRDefaultBandwidthHz
	}
	return 10 * math.Log10(bw)
}

// snrTrueDB returns the record's average SNR converted to the version 4 true-SNR
// scale, and whether it is known at all.
//
// Zero means "no SNR data" throughout this program -- old sidecars predate the
// SNR fields entirely -- so it is reported as unknown rather than as 0 dB.
func (r imageRecord) snrTrueDB() (float64, bool) {
	if r.SNRAvgDB == 0 {
		return 0, false
	}
	if r.SNRScale == snrScaleTrueSNR {
		return float64(r.SNRAvgDB), true
	}
	// No marker, or an explicit density marker: a pre-migration reading.
	return float64(r.SNRAvgDB) - legacySNROffsetDB(r.AudioMode), true
}

// onTrueSNRScale returns a copy of the record with every SNR figure -- the
// average, the minimum, the maximum and each point of the per-second series --
// converted to the true-SNR scale, and marked as being on it.
//
// This is what the HTTP API serves, so a client sees one scale across the whole
// gallery and is told which one it is, instead of having to correct each record
// itself and getting the per-mode bandwidth right to do so. The sidecars on disk
// are left exactly as they were written: they are the record of what was
// measured, and rewriting a history to fit a later convention loses the ability
// to tell the two apart at all.
func (r imageRecord) onTrueSNRScale() imageRecord {
	if r.SNRScale == snrScaleTrueSNR {
		return r
	}
	offset := float32(legacySNROffsetDB(r.AudioMode))

	// Zero is the "not measured" sentinel everywhere in this program, so it must
	// stay zero rather than becoming -34.7.
	shift := func(v float32) float32 {
		if v == 0 {
			return 0
		}
		return v - offset
	}

	r.SNRAvgDB = shift(r.SNRAvgDB)
	r.SNRMinDB = shift(r.SNRMinDB)
	r.SNRMaxDB = shift(r.SNRMaxDB)
	if r.SNRSeries != nil {
		series := make([]snrPoint, len(r.SNRSeries))
		for i, p := range r.SNRSeries {
			p.SNRDB = shift(p.SNRDB)
			series[i] = p
		}
		r.SNRSeries = series
	}
	// NoiseAvgDBFS is the other half of the same change. Before the migration it
	// held N0, a density in dBFS/Hz; the field now holds the noise POWER in the
	// passband, which is N0 + 10·log10(bandwidth). So this one moves UP by the
	// offset while the SNR figures move down, and that is what keeps
	// BasebandAvgDBFS - NoiseAvgDBFS equal to the SNRAvgDB reported alongside
	// it. BasebandAvgDBFS was a power in dBFS all along and does not move.
	if r.NoiseAvgDBFS != 0 {
		r.NoiseAvgDBFS += offset
	}
	r.SNRScale = snrScaleTrueSNR
	return r
}
