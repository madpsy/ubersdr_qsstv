/* ubersdr_qsstv — gallery + detail panel + SSE client */
'use strict';

// Base path injected by the server (empty string when accessed directly,
// e.g. "/addon/sstv" when behind the ka9q_ubersdr addon proxy).
const BASE_PATH = (typeof window.BASE_PATH === 'string') ? window.BASE_PATH : '';

// ---------------------------------------------------------------------------
// SSTV mode name translation table
// Maps the short names reported by QSSTV to their full human-readable names.
// Source: SSTVTable in src/sstv/sstvparam.cpp
// ---------------------------------------------------------------------------
const SSTV_MODE_NAMES = {
  'M1':      'Martin 1',
  'M2':      'Martin 2',
  'S1':      'Scottie 1',
  'S2':      'Scottie 2',
  'SDX':     'Scottie DX',
  'SC2-60':  'SC2 60',
  'SC2-120': 'SC2 120',
  'SC2-180': 'SC2 180',
  'R24':     'Robot 24',
  'R36':     'Robot 36',
  'R72':     'Robot 72',
  'P3':      'P3',
  'P5':      'P5',
  'P7':      'P7',
  'BW8':     'B/W 8',
  'BW12':    'B/W 12',
  'PD50':    'PD50',
  'PD90':    'PD90',
  'PD120':   'PD120',
  'PD160':   'PD160',
  'PD180':   'PD180',
  'PD240':   'PD240',
  'PD290':   'PD290',
  'MP73':    'MP73',
  'MP115':   'MP115',
  'MP140':   'MP140',
  'MP175':   'MP175',
  'MR73':    'MR73',
  'MR90':    'MR90',
  'MR115':   'MR115',
  'MR140':   'MR140',
  'MR175':   'MR175',
  'ML180':   'ML180',
  'ML240':   'ML240',
  'ML280':   'ML280',
  'ML320':   'ML320',
  'FAX480':  'FAX480',
  'AVT24':   'AVT24',
  'AVT90':   'AVT90',
  'AVT94':   'AVT94',
  'MP73-N':  'MP73-Narrow',
  'MP110-N': 'MP110-Narrow',
  'MP140-N': 'MP140-Narrow',
  'MC110-N': 'MC110-Narrow',
  'MC140-N': 'MC140-Narrow',
  'MC180-N': 'MC180-Narrow',
};

// Returns the full name for a given SSTV short-name, falling back to the
// short name itself if it isn't in the table (future/unknown modes).
function sstvModeName(shortName) {
  if (!shortName) return shortName;
  return SSTV_MODE_NAMES[shortName] || shortName;
}

// ---------------------------------------------------------------------------
// Auth state
// ---------------------------------------------------------------------------
// Whether the server has a password configured (fetched on boot from /api/auth/status).
let authPasswordConfigured = false;
// Whether the current browser session is authenticated.
let authAuthenticated = false;
// Queue of callbacks waiting for the user to authenticate.
let authPendingCallbacks = [];

const AUTH_STORAGE_KEY = 'ubersdr_ui_password';

// Attempt to authenticate silently using a password string.
// Returns a Promise that resolves true on success, false on failure.
function _tryPassword(pw) {
  return fetch(BASE_PATH + '/api/auth/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ password: pw }),
  })
    .then(r => {
      if (!r.ok) return false;
      authAuthenticated = true;
      localStorage.setItem(AUTH_STORAGE_KEY, pw);
      return true;
    })
    .catch(() => false);
}

// Try the password saved in localStorage silently.
// Calls onSuccess() if it works, onFail() if not (or nothing stored).
function tryStoredPassword(onSuccess, onFail) {
  const stored = localStorage.getItem(AUTH_STORAGE_KEY);
  if (!stored) { onFail(); return; }
  _tryPassword(stored).then(ok => {
    if (ok) { onSuccess(); }
    else {
      localStorage.removeItem(AUTH_STORAGE_KEY);
      onFail();
    }
  });
}

// ---------------------------------------------------------------------------
// Auth modal
// ---------------------------------------------------------------------------
function openAuthModal(onSuccess, onCancel) {
  authPendingCallbacks.push({ onSuccess, onCancel });
  if (authPendingCallbacks.length > 1) return; // modal already open

  const modal    = document.getElementById('auth-modal');
  const input    = document.getElementById('auth-password-input');
  const errorEl  = document.getElementById('auth-modal-error');
  const submitBtn = document.getElementById('auth-submit-btn');
  const cancelBtn = document.getElementById('auth-cancel-btn');

  if (!modal) return;
  errorEl.textContent = '';
  input.value = '';
  modal.classList.add('open');
  setTimeout(() => input.focus(), 50);

  function doSubmit() {
    const pw = input.value;
    if (!pw) { errorEl.textContent = 'Please enter a password.'; return; }
    submitBtn.disabled = true;
    submitBtn.textContent = '…';
    fetch(BASE_PATH + '/api/auth/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ password: pw }),
    })
      .then(r => {
        if (!r.ok) return r.json().then(d => { throw new Error(d.error || 'Incorrect password'); });
        return r.json();
      })
      .then(() => {
        authAuthenticated = true;
        localStorage.setItem(AUTH_STORAGE_KEY, pw);
        modal.classList.remove('open');
        submitBtn.disabled = false;
        submitBtn.textContent = 'Unlock';
        input.removeEventListener('keydown', onKeydown);
        // Fire all queued success callbacks.
        const cbs = authPendingCallbacks.splice(0);
        for (const cb of cbs) cb.onSuccess();
      })
      .catch(err => {
        localStorage.removeItem(AUTH_STORAGE_KEY);
        errorEl.textContent = err.message || 'Incorrect password.';
        submitBtn.disabled = false;
        submitBtn.textContent = 'Unlock';
        input.value = '';
        input.focus();
      });
  }

  function doCancel() {
    modal.classList.remove('open');
    input.removeEventListener('keydown', onKeydown);
    const cbs = authPendingCallbacks.splice(0);
    for (const cb of cbs) { if (cb.onCancel) cb.onCancel(); }
  }

  function onKeydown(e) {
    if (e.key === 'Enter') { e.preventDefault(); doSubmit(); }
    if (e.key === 'Escape') { e.preventDefault(); doCancel(); }
  }

  submitBtn.onclick = doSubmit;
  cancelBtn.onclick = doCancel;
  input.addEventListener('keydown', onKeydown);
}

// requireAuth wraps an action: if auth is not needed (no password configured)
// it shows a "not available" notice; if already authenticated it runs the action
// immediately; otherwise tries the stored password silently, then opens the modal.
function requireAuth(action) {
  if (!authPasswordConfigured) {
    // No password set — write actions are disabled.
    showAuthNotice('Write actions are disabled. Set UI_PASSWORD to enable them.');
    return;
  }
  if (authAuthenticated) {
    action();
    return;
  }
  // Try the stored password silently before showing the modal.
  tryStoredPassword(
    () => action(),          // stored password worked — run action directly
    () => openAuthModal(     // no stored password or it failed — show modal
      () => action(),
      () => {},
    ),
  );
}

// Show a brief inline notice (reuses the auth modal error element as a toast).
function showAuthNotice(msg) {
  const modal    = document.getElementById('auth-modal');
  const errorEl  = document.getElementById('auth-modal-error');
  const input    = document.getElementById('auth-password-input');
  const submitBtn = document.getElementById('auth-submit-btn');
  const cancelBtn = document.getElementById('auth-cancel-btn');
  if (!modal) { alert(msg); return; }
  // Show modal in read-only notice mode.
  if (input)    input.style.display = 'none';
  if (submitBtn) submitBtn.style.display = 'none';
  if (cancelBtn) cancelBtn.textContent = 'Close';
  const msgEl = document.getElementById('auth-modal-message');
  if (msgEl) msgEl.textContent = msg;
  if (errorEl) errorEl.textContent = '';
  modal.classList.add('open');
  if (cancelBtn) {
    cancelBtn.onclick = () => {
      modal.classList.remove('open');
      // Restore for next real auth use.
      if (input)    input.style.display = '';
      if (submitBtn) submitBtn.style.display = '';
      cancelBtn.textContent = 'Cancel';
      if (msgEl) msgEl.textContent = 'Enter the UI password to continue.';
    };
  }
}

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------
let allRecords = [];       // all records loaded so far (newest first)
let deletedIDs = new Set(); // IDs explicitly deleted this session — guards prependCard() and SSE image handler
let selectedID = null;
let galleryCompleteOnly = true; // mirrors the "Complete only" checkbox
let galleryShowLatest   = true; // mirrors the "Show latest" checkbox
let gallerySNRFilter    = true; // mirrors the "≥38 dB" SNR filter checkbox

// Infinite-scroll pagination state
const GALLERY_PAGE = 30;   // records per page
let galleryOffset   = 0;   // next offset to fetch
let galleryLoading  = false;
let galleryExhausted = false;
let lastRenderedDate = null; // 'YYYY-MM-DD' of the last card appended (for day headers)
let snrChart = null;
let leafletMap = null;
let txMarker = null;
let rxMarker = null;
let gcLine = null;
let receiverLat = 0;
let receiverLon = 0;
let receiverInfo = null; // {callsign, name, antenna, location, lat, lon, maidenhead}

// Audio preview state — Web Audio API streaming player
let audioPreviewEl = null;   // kept non-null while playing (sentinel only — no longer an <audio> element)
let audioPreviewLabel = '';  // instance label currently streaming
let audioMuted = false;      // true = muted (gainNode at 0), false = audible
let squelchEnabled = false;  // true = squelch active
let squelchOpen = false;     // true = squelch is currently passing audio (SNR high enough)
// Timer: squelch opens only after SNR stays ≥ SQUELCH_THRESHOLD for SQUELCH_HOLD_MS
const SQUELCH_THRESHOLD = 35; // dB — SNR must exceed this to open squelch
const SQUELCH_HOLD_MS   = 1000; // ms SNR must stay above threshold before opening
let squelchAboveTimer = null;  // setTimeout handle — fires when hold period elapses
let audioOutputDeviceId = ''; // selected sink device id (Chrome/Edge only)

// Web Audio streaming state
let audioCtx = null;         // AudioContext (created on first user gesture, reused across reconnects)
let audioGain = null;        // GainNode for mute/unmute
let audioFetchCtrl = null;   // AbortController for the fetch stream
let audioNextTime = 0;       // AudioContext time to schedule the next buffer
let audioSampleRate = 0;     // sample rate parsed from WAV header
let audioHeaderParsed = false; // true once the 44-byte WAV header has been consumed
let audioAccum = new Uint8Array(0); // accumulator for partial PCM data
// Generation counter — incremented on every startAudioPreview() call.
// Captured synchronously in schedulePCMChunk() and checked in the async
// decodeAudioData callback to discard stale decodes from a previous stream.
// Replaces the old capturedCtx !== audioCtx identity check, which no longer
// works now that the AudioContext is reused across reconnects.
let audioStreamGen = 0;

// Live SNR sparkline state
const LIVE_SNR_MAX_POINTS = 120; // 30 s at 250 ms cadence
const LIVE_SNR_MIN = 30;
const LIVE_SNR_MAX = 80;
let liveSNRChart = null;
let liveSNRData = []; // [{x: timestamp_ms, y: snr_db}, ...]

// Live RX preview state
let rxLiveES = null;         // EventSource for /api/rx/live
let rxLiveLabel = '';        // instance label we are subscribed to

// Live RX detail panel state
let rxLiveActive = false;    // true while an image is being received
let rxLiveSNRChart = null;   // Chart.js instance for the live SNR chart
let rxLiveSNRData = [];      // [{x: t_ms, y: snr_db}, ...] accumulated during reception
let rxLiveLeafletMap = null; // Leaflet map instance for the live origin map
let rxLiveTxMarker = null;
let rxLiveRxMarker = null;
let rxLiveGcLine = null;

// Live RX SNR bar state
let rxLiveBarSNRValues = []; // SNR dB value sampled at each received line
let rxLiveBarTotalLines = 0; // total lines in the current SSTV frame (full frame height)
let rxLiveBarCurrentLine = 0; // current scan line (1-based) — tracks how far down the image is
let rxLiveStartMs = 0;      // wall-clock ms when rx_start was received
let rxLiveImageTimeMs = 0;  // total known transmission duration in ms (from image_time_ms)
let rxLiveCountdownTimer = null; // setInterval handle for the countdown tick

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------
// ---------------------------------------------------------------------------
// Geodesic (haversine) distance — returns km between two lat/lon points.
// ---------------------------------------------------------------------------
function haversineKm(lat1, lon1, lat2, lon2) {
  const R = 6371;
  const dLat = (lat2 - lat1) * Math.PI / 180;
  const dLon = (lon2 - lon1) * Math.PI / 180;
  const a = Math.sin(dLat / 2) ** 2 +
            Math.cos(lat1 * Math.PI / 180) * Math.cos(lat2 * Math.PI / 180) *
            Math.sin(dLon / 2) ** 2;
  return R * 2 * Math.atan2(Math.sqrt(a), Math.sqrt(1 - a));
}

function fmtDistance(km) {
  if (km < 1) return Math.round(km * 1000) + ' m';
  return km.toFixed(0) + ' km';
}

function fmtFreq(hz) {
  if (hz >= 1e6) return (hz / 1e6).toFixed(3) + ' MHz';
  if (hz >= 1e3) return (hz / 1e3).toFixed(1) + ' kHz';
  return hz + ' Hz';
}

function fmtTime(iso) {
  if (!iso) return '—';
  const d = new Date(iso);
  return d.toISOString().replace('T', ' ').replace(/\.\d+Z$/, ' UTC');
}

function fmtSNR(v) {
  if (v == null || v === 0) return '—';
  return v.toFixed(1) + ' dB';
}

// ---------------------------------------------------------------------------
// Live SNR colour: red at 30 dB → orange at 40 dB → green at 50+ dB
// Returns a CSS colour string.
// ---------------------------------------------------------------------------
function snrColor(snrDB) {
  // Clamp to [30, 50] then map to hue [0°=red, 120°=green]
  const t = Math.max(0, Math.min(1, (snrDB - 30) / 20));
  const hue = Math.round(t * 120); // 0 → 120
  return `hsl(${hue}, 100%, 50%)`;
}

// ---------------------------------------------------------------------------
// SNR quality bar — vertical canvas strip beside an SSTV image.
//
// snrValues  : array of SNR dB numbers, index 0 = top of image (earliest line).
// totalLines : total number of image lines (full frame height — the denominator).
//              If omitted, the bar fills the full canvas height.
// filledLines: how many lines have actually been decoded so far (the numerator).
//              Defaults to snrValues.length when omitted.  Pass this explicitly
//              during live reception so the bar tracks the current scan line
//              rather than the (potentially smaller) SNR sample count.
// canvas     : the <canvas> element to draw into.
//
// The bar is drawn as a smooth vertical gradient: each SNR sample maps to a
// horizontal band whose colour comes from snrColor().  Bands are blended by
// drawing thin overlapping gradient stops so the transitions look smooth.
// ---------------------------------------------------------------------------
function renderSNRBar(canvas, snrValues, totalLines, filledLines) {
  if (!canvas || !snrValues || snrValues.length === 0) {
    if (canvas) {
      const ctx = canvas.getContext('2d');
      ctx.clearRect(0, 0, canvas.width, canvas.height);
    }
    return;
  }

  const h = canvas.offsetHeight || canvas.height || 200;
  const w = canvas.offsetWidth  || canvas.width  || 10;
  canvas.width  = w;
  canvas.height = h;

  const ctx = canvas.getContext('2d');
  ctx.clearRect(0, 0, w, h);

  const n = snrValues.length;
  // filledLines is the authoritative "how far down" count.
  // Fall back to snrValues.length if not provided.
  const filled = (filledLines != null && filledLines > 0) ? filledLines : n;
  // How far down the bar the received data reaches (0–1).
  const fillFraction = (totalLines && totalLines > 0)
    ? Math.min(1, filled / totalLines)
    : 1;
  const filledH = Math.round(h * fillFraction);

  if (filledH <= 0) return;

  // Build a linear gradient from top to bottom of the filled region.
  // Each SNR sample contributes one colour stop.
  const grad = ctx.createLinearGradient(0, 0, 0, filledH);
  for (let i = 0; i < n; i++) {
    const stop = i / Math.max(n - 1, 1);
    grad.addColorStop(stop, snrColor(snrValues[i]));
  }

  ctx.fillStyle = grad;
  ctx.beginPath();
  // Rounded rectangle for the filled portion
  const r = Math.min(4, w / 2);
  ctx.roundRect(0, 0, w, filledH, r);
  ctx.fill();

  // Unfilled portion (below received data) — dark placeholder
  if (filledH < h) {
    ctx.fillStyle = 'rgba(15,52,96,0.5)';
    ctx.beginPath();
    ctx.roundRect(0, filledH, w, h - filledH, r);
    ctx.fill();
  }
}

// ---------------------------------------------------------------------------
// Live SNR sparkline (header)
// ---------------------------------------------------------------------------
function initLiveSNRChart() {
  const canvas = document.getElementById('live-snr-chart');
  if (!canvas) return;
  liveSNRChart = new Chart(canvas, {
    type: 'line',
    data: {
      datasets: [{
        data: liveSNRData,
        borderColor: '#6fcf97',
        backgroundColor: 'rgba(111,207,151,0.15)',
        fill: true,
        tension: 0.3,
        pointRadius: 0,
        borderWidth: 1.5,
      }],
    },
    options: {
      responsive: false,
      animation: false,
      parsing: false,
      plugins: { legend: { display: false }, tooltip: { enabled: false } },
      scales: {
        x: {
          display: false,
          type: 'linear',
          min: 0,
          max: LIVE_SNR_MAX_POINTS - 1,
        },
        y: {
          display: false,
          min: LIVE_SNR_MIN,
          max: LIVE_SNR_MAX,
        },
      },
    },
  });
}

function pushLiveSNR(snrDB) {
  liveSNRData.push(snrDB);
  if (liveSNRData.length > LIVE_SNR_MAX_POINTS) {
    liveSNRData.shift();
  }

  // Drive squelch logic on every SNR sample.
  updateSquelch(snrDB);

  // Update numeric value + colour
  const valueEl = document.getElementById('live-snr-value');
  if (valueEl) {
    valueEl.textContent = snrDB.toFixed(1) + ' dB';
    valueEl.style.color = snrColor(snrDB);
  }

  // Re-index as 0..N-1 so the x-axis range is always [0, MAX_POINTS-1]
  // and the graph scrolls smoothly without rescaling.
  if (liveSNRChart) {
    liveSNRChart.data.datasets[0].data =
      liveSNRData.map((v, i) => ({ x: i, y: v }));
    liveSNRChart.data.datasets[0].borderColor = snrColor(snrDB);
    liveSNRChart.data.datasets[0].backgroundColor =
      snrColor(snrDB).replace('hsl(', 'hsla(').replace(')', ', 0.15)');
    liveSNRChart.update('none'); // no animation
  }
}

// ---------------------------------------------------------------------------
// Live RX meta table helpers (module-level so pushLiveRxSNR can call them)
// ---------------------------------------------------------------------------
// SNR label names that should receive colour coding in the live meta table.
const SNR_META_LABELS = new Set(['SNR avg', 'SNR min', 'SNR max']);

// Parse a formatted SNR string like "42.3 dB" back to a number, or return null.
function parseSNRValue(str) {
  if (!str || str === '—') return null;
  const n = parseFloat(str);
  return isNaN(n) ? null : n;
}

function buildLiveMeta(fields) {
  const metaEl = document.getElementById('rx-live-meta');
  if (!metaEl) return;
  const rows = [
    ['Mode',     sstvModeName(fields.mode) || '—'],
    ['Callsign', fields.callsign || '—'],
    ['Frequency',fields.freq     || '—'],
    ['RX start', fields.rxStart  || '—'],
    ['RX end',   fields.rxEnd    || '—'],
    ['SNR avg',  fields.snrAvg   || '—'],
    ['SNR min',  fields.snrMin   || '—'],
    ['SNR max',  fields.snrMax   || '—'],
    ['Samples',  fields.samples  != null ? fields.samples : '—'],
  ];
  metaEl.innerHTML = rows.map(([l, v]) => {
    let style = '';
    if (SNR_META_LABELS.has(l)) {
      const n = parseSNRValue(v);
      if (n != null) style = ` style="color:${snrColor(n)}"`;
    }
    return `<span class="label">${l}</span><span class="value"${style}>${v}</span>`;
  }).join('');
}

function updateLiveMetaRow(label, value) {
  const metaEl = document.getElementById('rx-live-meta');
  if (!metaEl) return;
  const labels = metaEl.querySelectorAll('.label');
  for (const lEl of labels) {
    if (lEl.textContent === label) {
      const vEl = lEl.nextElementSibling;
      if (vEl) {
        vEl.textContent = value;
        // Re-apply SNR colour if this is an SNR row.
        if (SNR_META_LABELS.has(label)) {
          const n = parseSNRValue(String(value));
          vEl.style.color = n != null ? snrColor(n) : '';
        }
      }
      return;
    }
  }
}

// ---------------------------------------------------------------------------
// Live RX SNR chart helpers
// ---------------------------------------------------------------------------
function initLiveRxSNRChart() {
  const canvas = document.getElementById('rx-live-snr-chart');
  if (!canvas) return;
  if (rxLiveSNRChart) { rxLiveSNRChart.destroy(); rxLiveSNRChart = null; }
  rxLiveSNRData = [];

  rxLiveSNRChart = new Chart(canvas, {
    type: 'line',
    data: {
      datasets: [{
        label: 'SNR (dB)',
        data: [],
        // Per-segment colour — each segment takes the colour of its start point.
        borderColor: '#888',
        backgroundColor: 'rgba(128,128,128,0.10)',
        fill: true,
        tension: 0.3,
        pointRadius: 0,
        borderWidth: 1.5,
        segment: {
          borderColor: ctx => snrColor(ctx.p0.parsed.y),
          backgroundColor: ctx => snrColor(ctx.p0.parsed.y)
            .replace('hsl(', 'hsla(').replace(')', ', 0.10)'),
        },
      }],
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      animation: false,
      parsing: false,
      plugins: {
        legend: { labels: { color: '#aaa', font: { size: 11 } } },
        tooltip: {
          callbacks: {
            label: ctx => `SNR: ${ctx.parsed.y != null ? ctx.parsed.y.toFixed(1) + ' dB' : '—'}`,
          },
        },
      },
      scales: {
        x: {
          type: 'time',
          time: { unit: 'second', displayFormats: { second: 'HH:mm:ss' } },
          ticks: { color: '#888', font: { size: 10 }, maxTicksLimit: 8 },
          grid: { color: '#1e2d4a' },
        },
        y: {
          title: { display: true, text: 'SNR (dB)', color: '#888', font: { size: 10 } },
          ticks: { color: '#888', font: { size: 10 } },
          grid: { color: '#1e2d4a' },
        },
      },
    },
  });
}

function pushLiveRxSNR(tMs, snrDB) {
  if (!rxLiveSNRChart) return;
  rxLiveSNRData.push({ x: tMs, y: snrDB });
  rxLiveSNRChart.data.datasets[0].data = rxLiveSNRData;
  // Segment callbacks handle per-point colouring automatically — no manual
  // borderColor/backgroundColor mutation needed here.
  rxLiveSNRChart.update('none');

  // Compute running min / max / avg from all accumulated points and update
  // the meta table rows in real-time (pure client-side — no backend needed).
  const n = rxLiveSNRData.length;
  let sum = 0, min = Infinity, max = -Infinity;
  for (const p of rxLiveSNRData) {
    sum += p.y;
    if (p.y < min) min = p.y;
    if (p.y > max) max = p.y;
  }
  const avg = sum / n;
  updateLiveMetaRow('SNR avg', fmtSNR(avg));
  updateLiveMetaRow('SNR min', fmtSNR(min));
  updateLiveMetaRow('SNR max', fmtSNR(max));
  updateLiveMetaRow('Samples', n);
}

// ---------------------------------------------------------------------------
// Live RX origin map
// ---------------------------------------------------------------------------
function renderLiveRxMap(cty) {
  const mapDiv   = document.getElementById('rx-live-map');
  const noCallEl = document.getElementById('rx-live-map-no-callsign');
  if (!mapDiv || !noCallEl) return;

  if (!cty || !cty.latitude || !cty.longitude) {
    mapDiv.style.display = 'none';
    noCallEl.style.display = 'block';
    return;
  }
  mapDiv.style.display = '';
  noCallEl.style.display = 'none';

  const txLat = cty.latitude;
  const txLon = cty.longitude;

  if (!rxLiveLeafletMap) {
    delete L.Icon.Default.prototype._getIconUrl;
    L.Icon.Default.mergeOptions({
      iconUrl:   'data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7',
      shadowUrl: '',
      iconSize:  [0, 0],
      shadowSize:[0, 0],
    });
    rxLiveLeafletMap = L.map('rx-live-map', { zoomControl: true }).setView([txLat, txLon], 3);
    L.tileLayer('https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png', {
      attribution: '© OpenStreetMap contributors',
      maxZoom: 18,
    }).addTo(rxLiveLeafletMap);
  } else {
    rxLiveLeafletMap.setView([txLat, txLon], 3);
  }

  if (rxLiveTxMarker) { rxLiveTxMarker.remove(); rxLiveTxMarker = null; }
  if (rxLiveRxMarker) { rxLiveRxMarker.remove(); rxLiveRxMarker = null; }
  if (rxLiveGcLine)   { rxLiveGcLine.remove();   rxLiveGcLine = null; }

  rxLiveTxMarker = L.marker([txLat, txLon], {
    icon: L.divIcon({ className: '', html: '📻', iconSize: [20, 20], iconAnchor: [10, 10] }),
  })
    .bindPopup(`<b>${cty.country || 'TX'}</b>`)
    .addTo(rxLiveLeafletMap)
    .openPopup();

  const rxLat = receiverInfo ? receiverInfo.lat : receiverLat;
  const rxLon = receiverInfo ? receiverInfo.lon : receiverLon;

  if (rxLat !== 0 || rxLon !== 0) {
    let rxTooltipHtml = '📡 Receiver';
    if (receiverInfo) {
      const parts = [];
      if (receiverInfo.callsign) parts.push(`<b>${receiverInfo.callsign}</b>`);
      if (receiverInfo.name)     parts.push(receiverInfo.name);
      if (parts.length) rxTooltipHtml = parts.join('<br>');
    }
    rxLiveRxMarker = L.marker([rxLat, rxLon], {
      icon: L.divIcon({ className: '', html: '📡', iconSize: [20, 20], iconAnchor: [10, 10] }),
    })
      .bindTooltip(rxTooltipHtml, { permanent: true, direction: 'top', className: 'rx-tooltip' })
      .addTo(rxLiveLeafletMap);

    rxLiveGcLine = L.polyline([[txLat, txLon], [rxLat, rxLon]], {
      color: '#e94560', weight: 2, opacity: 0.7, dashArray: '6 4',
    }).addTo(rxLiveLeafletMap);

    rxLiveLeafletMap.fitBounds([[txLat, txLon], [rxLat, rxLon]], { padding: [30, 30] });
  }

  setTimeout(() => rxLiveLeafletMap.invalidateSize(), 50);
}

function thumbSrc(rec) {
  if (rec.thumb) return BASE_PATH + '/images/' + rec.thumb;
  return BASE_PATH + '/images/' + rec.file;
}

function imageSrc(rec) {
  return BASE_PATH + '/images/' + rec.file;
}

// ---------------------------------------------------------------------------
// Gallery
// ---------------------------------------------------------------------------
// Returns true if rec passes the current gallery filter.
function recPassesFilter(rec) {
  // Complete-only filter
  if (galleryCompleteOnly) {
    // Old sidecars without image_height: always show (no data to filter on).
    if (rec.image_height && rec.lines_decoded < rec.image_height * 0.95) return false;
  }
  // SNR filter — hide images whose average SNR is known and below 38 dB
  if (gallerySNRFilter) {
    if (rec.snr_avg_db != null && rec.snr_avg_db < 38) return false;
  }
  return true;
}

function buildThumbCard(rec) {
  const card = document.createElement('div');
  card.className = 'thumb-card';
  card.dataset.id = rec.id;

  const img = document.createElement('img');
  img.src = thumbSrc(rec);
  img.alt = rec.sstv_mode || '';
  img.loading = 'lazy';
  card.appendChild(img);

  const meta = document.createElement('div');
  meta.className = 'thumb-meta';
  const snrStyle = rec.snr_avg_db ? ` style="color:${snrColor(rec.snr_avg_db)}"` : '';
  // Completeness badge: ✅ complete (≥95%), ❌ partial, nothing if data absent (old sidecar)
  const isComplete = rec.image_height > 0 && rec.lines_decoded >= rec.image_height * 0.95;
  const isPartial  = rec.image_height > 0 && rec.lines_decoded < rec.image_height * 0.95;
  const completeBadge = isComplete ? '<span class="decode-badge complete" title="Complete decode">✅</span>'
                      : isPartial  ? '<span class="decode-badge partial"  title="Partial decode">❌</span>'
                      : '';
  meta.innerHTML =
    `<div class="thumb-meta-top"><span class="mode-group">${completeBadge}<span class="mode">${sstvModeName(rec.sstv_mode) || '?'}</span></span>` +
    (rec.callsign ? `<span class="call">${rec.callsign}</span>` : '') + `</div>` +
    `<div class="freq">${fmtFreq(rec.frequency_hz)} ${(rec.audio_mode || '').toUpperCase()}</div>` +
    (rec.snr_avg_db ? `<div class="snr"${snrStyle}>${fmtSNR(rec.snr_avg_db)} avg SNR</div>` : '');
  card.appendChild(meta);

  // Clicking anywhere on the card selects the record (shows detail panel).
  card.addEventListener('click', () => selectRecord(rec.id));
  return card;
}

// ---------------------------------------------------------------------------
// Day-group helpers
// ---------------------------------------------------------------------------
function recDateKey(rec) {
  if (!rec.rx_start) return 'Unknown';
  return rec.rx_start.slice(0, 10); // 'YYYY-MM-DD'
}

function fmtDateLabel(key) {
  if (key === 'Unknown') return 'Unknown date';
  const d = new Date(key + 'T00:00:00Z');
  return d.toLocaleDateString(undefined, { weekday: 'long', year: 'numeric', month: 'long', day: 'numeric', timeZone: 'UTC' });
}

// ---------------------------------------------------------------------------
// Gallery count helpers
// ---------------------------------------------------------------------------

// Update the per-day-group count badge and the overall total count.
// Called after any card is added, removed, or shown/hidden by the filter.
function updateGalleryCounts() {
  const grid = document.getElementById('gallery-grid');
  let total = 0;

  grid.querySelectorAll('.day-group').forEach(group => {
    const cards = group.querySelectorAll('.thumb-card');
    const visibleCount = Array.from(cards).filter(c => c.style.display !== 'none').length;

    // Update or create the count badge inside the day-group label.
    let label = group.querySelector('.day-group-label');
    if (label) {
      let badge = label.querySelector('.day-group-count');
      if (!badge) {
        badge = document.createElement('span');
        badge.className = 'day-group-count';
        label.appendChild(badge);
      }
      badge.textContent = visibleCount > 0 ? `${visibleCount}` : '';
    }

    total += visibleCount;
  });

  // Update the total count in the gallery header.
  const totalEl = document.getElementById('gallery-total-count');
  if (totalEl) {
    totalEl.textContent = total > 0 ? `${total}` : '';
  }
}

// Returns the existing day-group wrapper for dateKey, or creates + inserts one
// before the sentinel.
function getOrCreateDayGroup(dateKey) {
  const grid = document.getElementById('gallery-grid');
  const existing = grid.querySelector(`.day-group[data-date="${dateKey}"]`);
  if (existing) return existing;
  const group = document.createElement('div');
  group.className = 'day-group';
  group.dataset.date = dateKey;
  group.innerHTML = `<div class="day-group-label">${fmtDateLabel(dateKey)}<span class="day-group-count"></span></div><div class="day-group-grid"></div>`;
  const sentinel = document.getElementById('gallery-sentinel');
  grid.insertBefore(group, sentinel);
  return group;
}

function appendCardToGroup(rec) {
  const dateKey = recDateKey(rec);
  const group   = getOrCreateDayGroup(dateKey);
  const inner   = group.querySelector('.day-group-grid');
  const card    = buildThumbCard(rec);
  if (!recPassesFilter(rec)) card.style.display = 'none';
  inner.appendChild(card);
  updateGalleryCounts();
}

function prependCard(rec) {
  // New completed image — always goes at the very top of the day-groups.
  // Guard: if this record was deleted in the current session, discard it so
  // it doesn't reappear (e.g. after an SSE reconnect replays the image event).
  if (deletedIDs.has(rec.id)) return;
  // Guard against duplicate insertions (e.g. SSE reconnect replays the event).
  if (allRecords.some(r => r.id === rec.id)) return;
  // Always add to allRecords so selectRecord() can find it by id and so that
  // resetAndReloadGallery() doesn't need to re-fetch when filters are toggled.
  allRecords.unshift(rec);

  // If the record doesn't pass the current filters, don't insert a DOM card
  // at all — this avoids fetching the thumbnail unnecessarily.
  if (!recPassesFilter(rec)) {
    updateGalleryCounts();
    return;
  }

  const dateKey = recDateKey(rec);
  const grid    = document.getElementById('gallery-grid');
  let group     = grid.querySelector(`.day-group[data-date="${dateKey}"]`);
  if (!group) {
    group = document.createElement('div');
    group.className = 'day-group';
    group.dataset.date = dateKey;
    group.innerHTML = `<div class="day-group-label">${fmtDateLabel(dateKey)}<span class="day-group-count"></span></div><div class="day-group-grid"></div>`;
    grid.insertBefore(group, grid.firstChild);
  }
  const inner = group.querySelector('.day-group-grid');
  const card  = buildThumbCard(rec);
  inner.insertBefore(card, inner.firstChild);
  updateGalleryCounts();

  // Auto-select the new completed image only when "Show latest" is enabled
  // and the user is not on the live panel or viewing a specific image.
  // Never yank the user away from the live panel — they stay there to watch
  // for the next incoming image.
  // Never yank the user away from a specific image they are viewing.
  if (galleryShowLatest && selectedID === null) {
    selectRecord(rec.id);
  }
}

// Re-show/hide all gallery cards to match the current filter state.
// Also hides day-group headers when all their cards are hidden.
function applyGalleryFilter() {
  const grid = document.getElementById('gallery-grid');
  grid.querySelectorAll('.thumb-card').forEach(card => {
    const rec = allRecords.find(r => r.id === card.dataset.id);
    card.style.display = (rec && recPassesFilter(rec)) ? '' : 'none';
  });
  // Hide day-group wrappers that have no visible cards.
  grid.querySelectorAll('.day-group').forEach(group => {
    const anyVisible = Array.from(group.querySelectorAll('.thumb-card'))
      .some(c => c.style.display !== 'none');
    group.style.display = anyVisible ? '' : 'none';
  });
  updateGalleryCounts();
}

// ---------------------------------------------------------------------------
// Detail panel
// ---------------------------------------------------------------------------
// ---------------------------------------------------------------------------
// Lightbox — full-screen image viewer
// ---------------------------------------------------------------------------
function openLightbox(src) {
  const lb  = document.getElementById('lightbox');
  const img = document.getElementById('lightbox-img');
  const dl  = document.getElementById('lightbox-download');
  if (!lb || !img) return;

  // Reset any previously forced width so naturalWidth reads correctly after load
  img.style.width = '';
  img.onload = () => {
    if (img.naturalWidth > 0) {
      img.style.width = (img.naturalWidth * 2) + 'px';
    }
  };
  img.src = src;

  // Wire up download button
  if (dl) {
    dl.href = src;
    // Derive a filename from the URL path (last segment), fallback to 'sstv.png'
    const filename = src.split('/').pop().split('?')[0] || 'sstv.png';
    dl.download = filename;
  }

  lb.classList.add('open');
  document.body.style.overflow = 'hidden';
}

function closeLightbox() {
  const lb = document.getElementById('lightbox');
  if (!lb) return;
  lb.classList.remove('open');
  document.getElementById('lightbox-img').src = '';
  document.body.style.overflow = '';
}

// ---------------------------------------------------------------------------
// Prev / Next navigation + Today's Slideshow
// ---------------------------------------------------------------------------
// Returns the list of visible (non-hidden) gallery record IDs in display order
// (newest first, matching the DOM order).
function visibleRecordIDs() {
  const grid = document.getElementById('gallery-grid');
  if (!grid) return [];
  const cards = grid.querySelectorAll('.thumb-card:not([style*="display: none"]):not([style*="display:none"])');
  return Array.from(cards).map(c => c.dataset.id).filter(Boolean);
}

// Returns today's date key 'YYYY-MM-DD' in UTC (matches recDateKey()).
function todayDateKey() {
  return new Date().toISOString().slice(0, 10);
}

// Returns the visible record IDs that belong to today (UTC date).
function todayVisibleRecordIDs() {
  const today = todayDateKey();
  return visibleRecordIDs().filter(id => {
    const rec = allRecords.find(r => r.id === id);
    return rec && recDateKey(rec) === today;
  });
}

// Slideshow state
let slideshowTimer = null;
const SLIDESHOW_INTERVAL_MS = 2000;

function stopSlideshow() {
  if (slideshowTimer) {
    clearInterval(slideshowTimer);
    slideshowTimer = null;
  }
  const cb = document.getElementById('detail-slideshow-cb');
  if (cb) cb.checked = false;
}

function slideshowTick() {
  const ids = todayVisibleRecordIDs();
  if (ids.length === 0) return;
  // If nothing is selected or the current selection isn't in today's list,
  // start from the newest (first) image.
  const idx = ids.indexOf(selectedID);
  if (idx < 0) {
    selectRecord(ids[0]);
    return;
  }
  // Advance to the next image; wrap around to the newest when we reach the end.
  const nextIdx = (idx + 1) % ids.length;
  selectRecord(ids[nextIdx]);
}

function startSlideshow() {
  // Show the first image immediately, then tick every 2 s.
  slideshowTick();
  slideshowTimer = setInterval(slideshowTick, SLIDESHOW_INTERVAL_MS);
}

function toggleSlideshow(enabled) {
  if (enabled) {
    startSlideshow();
  } else {
    stopSlideshow();
  }
}

// Enable/disable the prev/next buttons based on the current selection.
function updateNavButtons() {
  const prevBtn = document.getElementById('detail-prev-btn');
  const nextBtn = document.getElementById('detail-next-btn');
  if (!prevBtn || !nextBtn) return;

  if (!selectedID || selectedID === 'live') {
    prevBtn.disabled = true;
    nextBtn.disabled = true;
    return;
  }

  const ids = visibleRecordIDs();
  const idx = ids.indexOf(selectedID);
  // "Prev" = newer (lower index); "Next" = older (higher index)
  prevBtn.disabled = idx <= 0;
  nextBtn.disabled = idx < 0 || idx >= ids.length - 1;
}

function navigatePrev() {
  const ids = visibleRecordIDs();
  const idx = ids.indexOf(selectedID);
  if (idx > 0) selectRecord(ids[idx - 1]);
}

function navigateNext() {
  const ids = visibleRecordIDs();
  const idx = ids.indexOf(selectedID);
  if (idx >= 0 && idx < ids.length - 1) selectRecord(ids[idx + 1]);
}

function closeDetail() {
  selectedID = null;
  stopSlideshow();
  document.querySelectorAll('.thumb-card').forEach(c => c.classList.remove('selected'));
  const liveCard = document.getElementById('live-gallery-card');
  if (liveCard) liveCard.classList.remove('selected');
  document.getElementById('detail-empty').style.display = '';
  const content = document.getElementById('detail-content');
  content.classList.remove('visible');
  // Also hide the live panel
  const livePanel = document.getElementById('rx-live-panel');
  if (livePanel) livePanel.classList.remove('visible');
  // Destroy SNR chart so it doesn't hold a stale canvas reference
  if (snrChart) { snrChart.destroy(); snrChart = null; }
  // Reset delete button state so it's ready for the next selection
  const delBtn = document.getElementById('detail-delete-btn');
  if (delBtn) { delBtn.disabled = false; delBtn.textContent = '🗑 Delete'; }
  updateNavButtons();
}

function deleteRecord(id) {
  requireAuth(() => _doDeleteRecord(id));
}

// Remove a record from the local state and DOM.  Called both from
// _doDeleteRecord() (the deleting tab) and from the SSE 'delete' event
// handler (all other tabs).
function _removeRecordLocally(id) {
  // Track in the session-level deleted set so prependCard() and the SSE
  // 'image' handler don't re-insert it if the event is replayed.
  deletedIDs.add(id);

  // Remove from local allRecords array and adjust the pagination offset so
  // the next paginated fetch doesn't skip a record to fill the gap.
  const idx = allRecords.findIndex(r => r.id === id);
  if (idx !== -1) {
    allRecords.splice(idx, 1);
    if (galleryOffset > 0) galleryOffset--;
    // Only reset galleryExhausted if there are genuinely more records on the
    // server to fetch.  After decrementing galleryOffset, allRecords.length
    // equals galleryOffset, so the old condition (< galleryOffset) was always
    // true — incorrectly triggering a wasted fetch every time.
    // The correct signal is: we were exhausted AND we still have a full page
    // loaded, meaning there might be a record just beyond the old window.
    if (galleryExhausted && allRecords.length >= GALLERY_PAGE) {
      galleryExhausted = false;
    }
  }

  // Remove the gallery card from the DOM.
  const card = document.querySelector(`.thumb-card[data-id="${id}"]`);
  if (card) {
    const inner = card.parentElement; // .day-group-grid
    card.remove();
    // If the day group is now empty, remove it too.
    if (inner && inner.querySelectorAll('.thumb-card').length === 0) {
      const group = inner.closest('.day-group');
      if (group) group.remove();
    }
  }
  updateGalleryCounts();

  // If this record is currently selected, close the detail panel and then
  // auto-select the next available gallery card so the detail view doesn't
  // go blank after a deletion.
  if (selectedID === id) {
    closeDetail();
    // Find the first visible thumb-card remaining in the gallery and select it.
    const grid = document.getElementById('gallery-grid');
    if (grid) {
      const nextCard = grid.querySelector('.thumb-card:not([style*="display: none"]):not([style*="display:none"])');
      if (nextCard && nextCard.dataset.id) {
        selectRecord(nextCard.dataset.id);
      }
    }
  } else {
    // The deleted record may have been adjacent to the selected one —
    // refresh the prev/next button states.
    updateNavButtons();
  }
}

function _doDeleteRecord(id) {
  const btn = document.getElementById('detail-delete-btn');
  if (btn) { btn.disabled = true; btn.textContent = '…'; }

  fetch(BASE_PATH + `/api/images/${encodeURIComponent(id)}`, { method: 'DELETE' })
    .then(r => {
      if (!r.ok) return r.text().then(t => { throw new Error(t); });
      return r.json();
    })
    .then(() => {
      // The server will broadcast an SSE 'delete' event which will call
      // _removeRecordLocally() on all tabs including this one.  Call it
      // directly here too so the UI responds immediately without waiting
      // for the SSE round-trip.
      _removeRecordLocally(id);
    })
    .catch(err => {
      console.error('delete failed:', err);
      const b = document.getElementById('detail-delete-btn');
      if (b) { b.disabled = false; b.textContent = '🗑 Delete'; }
    });
}

// ResizeObserver kept alive across selectRecord() calls so we can disconnect
// the previous one before attaching a new one.
let detailImgRO = null;

function selectRecord(id) {
  selectedID = id;

  // Highlight selected gallery card (regular cards)
  document.querySelectorAll('.thumb-card').forEach(c => {
    c.classList.toggle('selected', c.dataset.id === id);
  });

  // Highlight / deselect the live card
  const liveCard = document.getElementById('live-gallery-card');
  if (liveCard) liveCard.classList.toggle('selected', id === 'live');

  const livePanel  = document.getElementById('rx-live-panel');
  const detailContent = document.getElementById('detail-content');
  const detailEmpty   = document.getElementById('detail-empty');

  // ── Live card selected ──────────────────────────────────────────────────
  if (id === 'live') {
    detailEmpty.style.display = 'none';
    detailContent.classList.remove('visible');
    if (livePanel) livePanel.classList.add('visible');
    // Destroy stale SNR chart so it doesn't hold a stale canvas reference
    if (snrChart) { snrChart.destroy(); snrChart = null; }
    updateNavButtons();
    return;
  }

  // ── Regular gallery card selected ──────────────────────────────────────
  if (livePanel) livePanel.classList.remove('visible');

  const rec = allRecords.find(r => r.id === id);
  if (!rec) return;

  detailEmpty.style.display = 'none';
  const content = document.getElementById('detail-content');
  content.classList.add('visible');

  // Image — clicking it opens the lightbox
  const img    = document.getElementById('detail-image');
  const barCanvas = document.getElementById('detail-snr-bar');
  img.src = imageSrc(rec);
  img.alt = rec.sstv_mode || '';
  img.style.cursor = 'zoom-in';
  img.onclick = () => openLightbox(imageSrc(rec));

  // Save button — download link pointing at the full-res image
  const saveBtn = document.getElementById('detail-save-btn');
  if (saveBtn) {
    const src = imageSrc(rec);
    saveBtn.href = src;
    saveBtn.download = src.split('/').pop().split('?')[0] || 'sstv.png';
  }

  // Delete button — listener is wired once at boot; it reads selectedID at click time.

  // Metadata table
  const meta = document.getElementById('detail-meta');
  // SNR rows get inline colour from snrColor() to match the header signal meter.
  const snrAvgStyle = rec.snr_avg_db != null ? ` style="color:${snrColor(rec.snr_avg_db)}"` : '';
  const snrMinStyle = rec.snr_min_db != null ? ` style="color:${snrColor(rec.snr_min_db)}"` : '';
  const snrMaxStyle = rec.snr_max_db != null ? ` style="color:${snrColor(rec.snr_max_db)}"` : '';
  // Decode completeness row
  let decodeValue = '—';
  let decodeStyle = '';
  if (rec.image_height > 0) {
    const pct = Math.round(rec.lines_decoded / rec.image_height * 100);
    if (rec.lines_decoded >= rec.image_height * 0.95) {
      decodeValue = `✅ Complete (${rec.lines_decoded}/${rec.image_height} lines, ${pct}%)`;
      decodeStyle = ' style="color:#6fcf97"';
    } else {
      decodeValue = `❌ Partial (${rec.lines_decoded}/${rec.image_height} lines, ${pct}%)`;
      decodeStyle = ' style="color:#f2c94c"';
    }
  }

  const rows = [
    ['Mode',      sstvModeName(rec.sstv_mode) || '—', ''],
    ['Decode',    decodeValue, decodeStyle],
    ['Callsign',  rec.callsign  || '—', ''],
    ['Frequency', fmtFreq(rec.frequency_hz) + ' ' + (rec.audio_mode || '').toUpperCase(), ''],
    ['RX start',  fmtTime(rec.rx_start), ''],
    ['RX end',    fmtTime(rec.rx_end), ''],
    ['SNR avg',   fmtSNR(rec.snr_avg_db), snrAvgStyle],
    ['SNR min',   fmtSNR(rec.snr_min_db), snrMinStyle],
    ['SNR max',   fmtSNR(rec.snr_max_db), snrMaxStyle],
    ['Samples',   rec.snr_samples != null ? rec.snr_samples : '—', ''],
  ];
  if (rec.cty) {
    rows.push(['Country',   rec.cty.country   || '—', '']);
    rows.push(['Continent', rec.cty.continent || '—', '']);
    rows.push(['CQ zone',   rec.cty.cq_zone   || '—', '']);
    rows.push(['ITU zone',  rec.cty.itu_zone  || '—', '']);
  }
  // Distance between transmitter and receiver (requires both CTY coords and receiver GPS)
  if (rec.cty && rec.cty.latitude && rec.cty.longitude && receiverInfo && receiverInfo.lat) {
    const km = haversineKm(rec.cty.latitude, rec.cty.longitude, receiverInfo.lat, receiverInfo.lon);
    rows.push(['Distance', fmtDistance(km), '']);
  }
  meta.innerHTML = rows.map(([l, v, s]) =>
    `<span class="label">${l}</span><span class="value"${s}>${v}</span>`
  ).join('');

  // SNR chart
  renderSNRChart(rec);

  // SNR quality bar — vertical strip beside the image.
  // Extract SNR values from snr_series (per-second samples, index 0 = earliest).
  const snrValues = rec.snr_series ? rec.snr_series.map(p => p.snr_db) : [];
  // image_height  = full frame height in lines (totalLines — the denominator).
  // lines_decoded = how many lines were actually decoded (filledLines — the numerator).
  // fillFraction  = lines_decoded / image_height.
  // snrValues is stretched to cover the filled region regardless of its length.
  // Old sidecars without these fields: both are 0 → fillFraction = 1 (full bar, unchanged).
  const snrImageHeight  = rec.image_height  || 0;
  const snrLinesDecoded = rec.lines_decoded || 0;

  // Helper: size the bar canvas to match the rendered image height, then draw.
  function drawDetailBar() {
    if (!barCanvas) return;
    const h = img.offsetHeight;
    if (h > 0) {
      barCanvas.style.height = h + 'px';
      // Pass image_height as totalLines and lines_decoded as filledLines.
      // When lines_decoded is 0 (old sidecar), filledLines falls back to
      // snrValues.length inside renderSNRBar, giving fillFraction = 1.
      renderSNRBar(barCanvas, snrValues, snrImageHeight, snrLinesDecoded || undefined);
    }
  }

  // Disconnect any previous observer before attaching a new one.
  if (detailImgRO) { detailImgRO.disconnect(); detailImgRO = null; }

  if (snrValues.length > 0 && barCanvas) {
    barCanvas.style.display = '';
    // Draw once the image has loaded (so offsetHeight is correct).
    img.onload = () => {
      drawDetailBar();
      // Also watch for layout changes (panel resize, etc.)
      detailImgRO = new ResizeObserver(drawDetailBar);
      detailImgRO.observe(img);
    };
    // If the image is already cached and onload won't fire, draw immediately.
    if (img.complete && img.naturalHeight > 0) {
      drawDetailBar();
      detailImgRO = new ResizeObserver(drawDetailBar);
      detailImgRO.observe(img);
    }
  } else if (barCanvas) {
    barCanvas.style.display = 'none';
  }

  // Origin map
  renderMap(rec);

  // Update prev/next button states for the newly selected record.
  updateNavButtons();
}

// ---------------------------------------------------------------------------
// SNR chart (Chart.js)
//
// If snr_series is present (per-second buckets): render as a real time-varying
// SNR curve with a shaded min/max band behind it.
// Fallback (old sidecars without series): flat band showing aggregate stats.
// ---------------------------------------------------------------------------
function renderSNRChart(rec) {
  const canvas = document.getElementById('snr-chart');
  if (snrChart) { snrChart.destroy(); snrChart = null; }

  const hasSeries = rec.snr_series && rec.snr_series.length > 1;
  const hasStats  = rec.snr_avg_db != null && rec.snr_samples > 0;

  if (!hasSeries && !hasStats) {
    canvas.style.display = 'none';
    return;
  }
  canvas.style.display = '';

  let datasets;

  if (hasSeries) {
    // ── Real time-series curve ──────────────────────────────────────────────
    // Colour each segment individually based on the SNR value at that point,
    // matching the header signal meter colour coding (red→orange→green).
    const seriesData = rec.snr_series.map(p => ({ x: p.t, y: p.snr_db }));

    // Build a shaded min/max band using the aggregate stats as constant bounds.
    const tFirst = rec.snr_series[0].t;
    const tLast  = rec.snr_series[rec.snr_series.length - 1].t;
    const bandData = [
      { x: tFirst, y: rec.snr_max_db },
      { x: tLast,  y: rec.snr_max_db },
    ];
    const bandLowData = [
      { x: tFirst, y: rec.snr_min_db },
      { x: tLast,  y: rec.snr_min_db },
    ];

    // Average SNR used only for the static band colour.
    const avgSNR   = rec.snr_avg_db != null ? rec.snr_avg_db : 40;
    const bandBase = snrColor(avgSNR);
    const bandColor = bandBase.replace('hsl(', 'hsla(').replace(')', ', 0.25)');
    const bandFill  = bandBase.replace('hsl(', 'hsla(').replace(')', ', 0.10)');

    datasets = [
      {
        label: 'Max',
        data: bandData,
        borderColor: bandColor,
        backgroundColor: bandFill,
        fill: '+1',
        tension: 0,
        pointRadius: 0,
        borderWidth: 1,
        borderDash: [4, 4],
      },
      {
        label: 'SNR (dB)',
        data: seriesData,
        // borderColor / backgroundColor are overridden per-segment below.
        borderColor: '#888',
        backgroundColor: 'transparent',
        fill: false,
        tension: 0.3,
        // Colour each point dot by its own SNR value.
        pointRadius: 2,
        pointBackgroundColor: seriesData.map(p => snrColor(p.y)),
        pointBorderColor:     seriesData.map(p => snrColor(p.y)),
        borderWidth: 2,
        // Per-segment line colour — each segment takes the colour of its start point.
        segment: {
          borderColor: ctx => snrColor(ctx.p0.parsed.y),
        },
      },
      {
        label: 'Min',
        data: bandLowData,
        borderColor: bandColor,
        backgroundColor: bandFill,
        fill: '-2',
        tension: 0,
        pointRadius: 0,
        borderWidth: 1,
        borderDash: [4, 4],
      },
    ];
  } else {
    // ── Fallback: flat band from aggregate stats ────────────────────────────
    // Colour based on average SNR — matches the header signal meter.
    const avgSNR    = rec.snr_avg_db != null ? rec.snr_avg_db : 40;
    const lineColor = snrColor(avgSNR);
    const bandColor = lineColor.replace('hsl(', 'hsla(').replace(')', ', 0.4)');
    const bandFill  = lineColor.replace('hsl(', 'hsla(').replace(')', ', 0.15)');

    const start = rec.rx_start ? new Date(rec.rx_start).getTime() : 0;
    const end   = rec.rx_end   ? new Date(rec.rx_end).getTime()   : start + 1000;
    datasets = [
      {
        label: 'Max',
        data: [{ x: start, y: rec.snr_max_db }, { x: end, y: rec.snr_max_db }],
        borderColor: bandColor,
        backgroundColor: bandFill,
        fill: '+1',
        tension: 0,
        pointRadius: 3,
      },
      {
        label: 'Avg',
        data: [{ x: start, y: rec.snr_avg_db }, { x: end, y: rec.snr_avg_db }],
        borderColor: lineColor,
        backgroundColor: 'transparent',
        fill: false,
        tension: 0,
        pointRadius: 3,
        borderWidth: 2,
      },
      {
        label: 'Min',
        data: [{ x: start, y: rec.snr_min_db }, { x: end, y: rec.snr_min_db }],
        borderColor: bandColor,
        backgroundColor: bandFill,
        fill: '-1',
        tension: 0,
        pointRadius: 3,
      },
    ];
  }

  snrChart = new Chart(canvas, {
    type: 'line',
    data: { datasets },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      animation: false,
      parsing: false,
      plugins: {
        legend: { labels: { color: '#aaa', font: { size: 11 } } },
        tooltip: {
          callbacks: {
            label: ctx => `${ctx.dataset.label}: ${ctx.parsed.y != null ? ctx.parsed.y.toFixed(1) + ' dB' : '—'}`,
          },
        },
      },
      scales: {
        x: {
          type: 'time',
          time: { unit: 'second', displayFormats: { second: 'HH:mm:ss' } },
          ticks: { color: '#888', font: { size: 10 }, maxTicksLimit: 8 },
          grid: { color: '#1e2d4a' },
        },
        y: {
          title: { display: true, text: 'SNR (dB)', color: '#888', font: { size: 10 } },
          ticks: { color: '#888', font: { size: 10 } },
          grid: { color: '#1e2d4a' },
        },
      },
    },
  });
}

// ---------------------------------------------------------------------------
// Origin map (Leaflet)
// ---------------------------------------------------------------------------
function renderMap(rec) {
  const mapDiv = document.getElementById('map');
  const noCall = document.getElementById('map-no-callsign');

  if (!rec.cty || !rec.cty.latitude || !rec.cty.longitude) {
    mapDiv.style.display = 'none';
    noCall.style.display = 'block';
    return;
  }
  mapDiv.style.display = '';
  noCall.style.display = 'none';

  const txLat = rec.cty.latitude;
  const txLon = rec.cty.longitude;

  if (!leafletMap) {
    // Suppress Leaflet's default PNG marker requests by overriding the icon
    // prototype before any marker is created.
    delete L.Icon.Default.prototype._getIconUrl;
    L.Icon.Default.mergeOptions({
      iconUrl:       'data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7', // 1×1 transparent
      shadowUrl:     '',
      iconSize:      [0, 0],
      shadowSize:    [0, 0],
    });

    leafletMap = L.map('map', { zoomControl: true }).setView([txLat, txLon], 3);
    L.tileLayer('https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png', {
      attribution: '© OpenStreetMap contributors',
      maxZoom: 18,
    }).addTo(leafletMap);
  } else {
    leafletMap.setView([txLat, txLon], 3);
  }

  // Remove old markers/line
  if (txMarker) { txMarker.remove(); txMarker = null; }
  if (rxMarker) { rxMarker.remove(); rxMarker = null; }
  if (gcLine)   { gcLine.remove();   gcLine = null; }

  // Transmitter marker — use a divIcon so no PNG is needed
  txMarker = L.marker([txLat, txLon], {
    icon: L.divIcon({ className: '', html: '📻', iconSize: [20, 20], iconAnchor: [10, 10] }),
  })
    .bindPopup(`<b>${rec.callsign || 'TX'}</b><br>${rec.cty.country || ''}`)
    .addTo(leafletMap)
    .openPopup();

  // Receiver marker — use receiverInfo if available, fall back to legacy lat/lon globals
  const rxLat = receiverInfo ? receiverInfo.lat : receiverLat;
  const rxLon = receiverInfo ? receiverInfo.lon : receiverLon;

  if (rxLat !== 0 || rxLon !== 0) {
    // Build permanent tooltip content from receiverInfo fields
    let rxTooltipHtml = '📡 Receiver';
    if (receiverInfo) {
      const parts = [];
      if (receiverInfo.callsign) parts.push(`<b>${receiverInfo.callsign}</b>`);
      if (receiverInfo.name)     parts.push(receiverInfo.name);
      if (receiverInfo.antenna)  parts.push(`Antenna: ${receiverInfo.antenna}`);
      if (receiverInfo.location) parts.push(receiverInfo.location);
      if (parts.length) rxTooltipHtml = parts.join('<br>');
    }

    rxMarker = L.marker([rxLat, rxLon], {
      icon: L.divIcon({ className: '', html: '📡', iconSize: [20, 20], iconAnchor: [10, 10] }),
    })
      .bindTooltip(rxTooltipHtml, { permanent: true, direction: 'top', className: 'rx-tooltip' })
      .addTo(leafletMap);

    // Great-circle approximation (Leaflet polyline with antimeridian wrapping)
    gcLine = L.polyline([[txLat, txLon], [rxLat, rxLon]], {
      color: '#e94560',
      weight: 2,
      opacity: 0.7,
      dashArray: '6 4',
    }).addTo(leafletMap);

    // Fit map to show both endpoints
    leafletMap.fitBounds([[txLat, txLon], [rxLat, rxLon]], { padding: [30, 30] });
  }

  // Force Leaflet to recalculate size (panel may have been hidden)
  setTimeout(() => leafletMap.invalidateSize(), 50);
}

// ---------------------------------------------------------------------------
// ---------------------------------------------------------------------------
// Audio preview — Web Audio API streaming player.
//
// Uses fetch() + ReadableStream to pull the WAV stream chunk-by-chunk,
// then decodes each PCM chunk via AudioContext.decodeAudioData() and
// schedules AudioBufferSourceNodes for gapless playback.  This bypasses
// Chrome's aggressive <audio> element buffering (which causes ~10 s delay).
// Firefox works fine with either approach.
// ---------------------------------------------------------------------------

// Minimum seconds of audio to pre-buffer before we start the clock.
// Keeps scheduling ahead of the playhead to avoid glitches.
const AUDIO_SCHEDULE_AHEAD_S = 0.1;
// Maximum seconds the scheduler is allowed to run ahead of the playhead.
// Prevents a pre-roll burst from pushing audioNextTime so far ahead that
// there is a long gap before live audio is heard.
const AUDIO_MAX_LEAD_S = 0.4;
// How many PCM bytes to accumulate before decoding one chunk.
// At 11025 Hz mono S16LE: 4410 bytes ≈ 200 ms.  Tune for latency vs. glitch.
const AUDIO_CHUNK_BYTES = 4410;
// Number of pre-roll chunks the server sends at the start of each connection
// (must match audioPrerollChunks in instance.go).  These chunks are stale PCM
// from the ring buffer; we discard them so only live audio enters the timeline.
const AUDIO_PREROLL_CHUNKS = 1;

// How many pre-roll chunks remain to be discarded for the current connection.
let audioPrerollRemaining = 0;

function stopAudioPreview() {
  if (audioFetchCtrl) {
    audioFetchCtrl.abort();
    audioFetchCtrl = null;
  }
  // Invalidate any in-flight decodeAudioData callbacks from the current stream.
  audioStreamGen++;
  // Suspend (don't close) the AudioContext so currentTime keeps advancing
  // monotonically across reconnects.  A closed context resets currentTime to 0,
  // which breaks the audioNextTime < now guard in schedulePCMChunk().
  if (audioCtx) {
    audioCtx.suspend().catch(() => {});
    // Keep audioCtx and audioGain alive — they are reused on the next
    // startAudioPreview() call (same user gesture, same context).
  }
  audioPreviewEl = null;
  audioSampleRate = 0;
  audioHeaderParsed = false;
  audioAccum = new Uint8Array(0);
  audioPrerollRemaining = 0;
}

function startAudioPreview(label) {
  // Abort any in-flight fetch but keep the AudioContext alive.
  if (audioFetchCtrl) {
    audioFetchCtrl.abort();
    audioFetchCtrl = null;
  }
  // Reset per-stream state without touching the AudioContext.
  audioPreviewEl = null;
  audioSampleRate = 0;
  audioHeaderParsed = false;
  audioAccum = new Uint8Array(0);
  // audioNextTime is intentionally NOT reset here: keeping it at its current
  // value (or letting the < now guard reset it) avoids scheduling new chunks
  // in the past when the AudioContext currentTime has advanced.
  audioPrerollRemaining = AUDIO_PREROLL_CHUNKS;

  // Increment the generation counter so any in-flight decodeAudioData callbacks
  // from the previous stream know to discard their results.
  audioStreamGen++;

  audioPreviewLabel = label || '';
  const url = BASE_PATH + '/api/audio/preview' + (audioPreviewLabel ? '?label=' + encodeURIComponent(audioPreviewLabel) : '');

  // Create the AudioContext on the very first call (requires a user gesture).
  // On subsequent calls (reconnects) we reuse the existing context so that
  // currentTime is monotonically increasing and the scheduler stays coherent.
  if (!audioCtx) {
    audioCtx = new (window.AudioContext || window.webkitAudioContext)();
    audioGain = audioCtx.createGain();
    audioGain.connect(audioCtx.destination);
    // Apply selected output device if one was chosen before playback started.
    if (audioOutputDeviceId && typeof audioCtx.setSinkId === 'function') {
      audioCtx.setSinkId(audioOutputDeviceId).catch(err => {
        console.warn('setSinkId on new AudioContext failed:', err);
      });
    }
  } else {
    // Resume in case it was suspended by a previous stopAudioPreview() call.
    audioCtx.resume().catch(() => {});
  }
  applyGain(); // respect current mute + squelch state

  audioPreviewEl = true; // sentinel — non-null means "playing"

  // Capture a generation counter so that stream-end / error callbacks from a
  // previous startAudioPreview() call cannot trigger a reconnect after a new
  // call has already started.
  const myCtrl = new AbortController();
  audioFetchCtrl = myCtrl;

  updateMuteBtn();

  console.log('[audio] starting preview for label:', audioPreviewLabel);

  fetch(url, { signal: myCtrl.signal })
    .then(resp => {
      if (!resp.ok) throw new Error('audio preview HTTP ' + resp.status);
      const reader = resp.body.getReader();

      function pump() {
        return reader.read().then(({ done, value }) => {
          // Stop if this fetch was superseded (a new startAudioPreview() call
          // replaced audioFetchCtrl) or explicitly aborted.
          if (done || audioFetchCtrl !== myCtrl) return;
          processAudioChunk(value);
          return pump();
        });
      }
      return pump();
    })
    .then(() => {
      // Stream ended cleanly (server closed the response — e.g. on instance
      // restart).  Only reconnect if this fetch is still the active one.
      if (audioFetchCtrl !== myCtrl) return; // superseded — do nothing
      console.log('[audio] stream ended cleanly — reconnecting in 500 ms…');
      setTimeout(() => {
        if (audioFetchCtrl !== myCtrl) return; // superseded during the delay
        startAudioPreview(audioPreviewLabel);
      }, 500);
    })
    .catch(err => {
      if (err.name === 'AbortError') return; // explicit stop — do nothing
      console.warn('[audio] preview error:', err);
      if (audioFetchCtrl !== myCtrl) return; // superseded — do nothing
      // Reconnect after a short delay on transient errors.
      setTimeout(() => {
        if (audioFetchCtrl !== myCtrl) return;
        startAudioPreview(audioPreviewLabel);
      }, 1000);
    });
}

// Accumulate incoming bytes; once we have the WAV header + enough PCM,
// decode and schedule AudioBufferSourceNodes.
function processAudioChunk(bytes) {
  if (!bytes || bytes.length === 0) return;

  // Append to accumulator.
  const merged = new Uint8Array(audioAccum.length + bytes.length);
  merged.set(audioAccum);
  merged.set(bytes, audioAccum.length);
  audioAccum = merged;

  // Parse the 44-byte WAV header once.
  if (!audioHeaderParsed) {
    if (audioAccum.length < 44) return; // wait for more data
    const view = new DataView(audioAccum.buffer);
    audioSampleRate = view.getUint32(24, true);
    audioHeaderParsed = true;
    audioAccum = audioAccum.slice(44); // strip header
    // Do NOT set audioNextTime here — it is set in schedulePCMChunk() using
    // the live AudioContext.currentTime at the moment the first live chunk
    // (after pre-roll is discarded) is ready to schedule.
  }

  // Decode and schedule complete chunks.
  const chunkBytes = AUDIO_CHUNK_BYTES;
  while (audioAccum.length >= chunkBytes) {
    const pcm = audioAccum.slice(0, chunkBytes);
    audioAccum = audioAccum.slice(chunkBytes);

    // Discard pre-roll chunks — they are stale PCM from the server's ring
    // buffer.  Scheduling them would push audioNextTime ahead by their
    // duration before any live audio arrives, causing a content discontinuity
    // (stutter) at the pre-roll/live boundary.
    if (audioPrerollRemaining > 0) {
      audioPrerollRemaining--;
      console.log('[audio] discarding pre-roll chunk (' + (AUDIO_PREROLL_CHUNKS - audioPrerollRemaining) + '/' + AUDIO_PREROLL_CHUNKS + ')');
      continue;
    }

    schedulePCMChunk(pcm);
  }
}

// Wrap raw S16LE PCM bytes in a minimal WAV container, decode via
// AudioContext.decodeAudioData, then schedule the resulting AudioBuffer.
//
// The start time is assigned SYNCHRONOUSLY at call time so that chunks
// submitted in a burst are always scheduled in order, even if the async
// decodes complete out of order.  Stale callbacks from a stopped/replaced
// stream are discarded via the capturedCtx guard.
function schedulePCMChunk(pcm) {
  if (!audioCtx || !audioGain) return;
  const sr = audioSampleRate || 11025;

  // Capture the current AudioContext and generation counter synchronously.
  // The AudioContext is reused across reconnects (never closed), so we cannot
  // use capturedCtx !== audioCtx to detect stale callbacks.  Instead we use
  // audioStreamGen: if it has changed by the time decodeAudioData resolves,
  // the stream was replaced and we discard the result.
  const capturedCtx  = audioCtx;
  const capturedGain = audioGain;
  const capturedGen  = audioStreamGen;

  // Assign the scheduled start time NOW (synchronously), before the async
  // decode.  This guarantees ordering even when decodes complete out of order.
  const now = capturedCtx.currentTime;
  // Reset if we've fallen behind the playhead OR if we've run too far ahead
  // (e.g. after a burst of chunks).  The max-lead cap prevents a pre-roll
  // burst from pushing audioNextTime so far ahead that live audio is delayed.
  if (audioNextTime < now || audioNextTime > now + AUDIO_MAX_LEAD_S) {
    audioNextTime = now + AUDIO_SCHEDULE_AHEAD_S;
  }
  // Duration of this chunk in seconds (S16LE mono: 2 bytes per sample).
  const chunkDuration = pcm.length / 2 / sr;
  const startTime = audioNextTime;
  audioNextTime += chunkDuration;

  // Build a complete WAV file in memory.
  const wavBuf = new ArrayBuffer(44 + pcm.length);
  const view = new DataView(wavBuf);
  const enc = new TextEncoder();
  const wr4 = (off, s) => { const b = enc.encode(s); for (let i = 0; i < 4; i++) view.setUint8(off + i, b[i]); };
  wr4(0,  'RIFF');
  view.setUint32(4,  36 + pcm.length, true);
  wr4(8,  'WAVE');
  wr4(12, 'fmt ');
  view.setUint32(16, 16, true);          // PCM chunk size
  view.setUint16(20, 1,  true);          // PCM format
  view.setUint16(22, 1,  true);          // mono
  view.setUint32(24, sr, true);          // sample rate
  view.setUint32(28, sr * 2, true);      // byte rate
  view.setUint16(32, 2,  true);          // block align
  view.setUint16(34, 16, true);          // bits per sample
  wr4(36, 'data');
  view.setUint32(40, pcm.length, true);
  new Uint8Array(wavBuf, 44).set(pcm);

  capturedCtx.decodeAudioData(wavBuf).then(audioBuf => {
    // Bail out if the stream was replaced while we were decoding.
    // Use the generation counter rather than object identity because the
    // AudioContext is reused across reconnects.
    if (audioStreamGen !== capturedGen) return;

    const src = capturedCtx.createBufferSource();
    src.buffer = audioBuf;
    src.connect(capturedGain);
    // Use the pre-assigned start time — ordering is guaranteed regardless of
    // decode completion order.
    src.start(startTime);
  }).catch(err => {
    console.warn('decodeAudioData:', err);
  });
}

// ---------------------------------------------------------------------------
// Squelch — applies on top of the user mute state.
// The gain node is set to 0 when either muted OR squelch is closed.
// The mute button visual state is never changed by squelch.
// ---------------------------------------------------------------------------

// Recompute the gain node value from the current mute + squelch state.
function applyGain() {
  if (!audioGain) return;
  // Audio passes only when: not muted AND (squelch disabled OR squelch open)
  const pass = !audioMuted && (!squelchEnabled || squelchOpen);
  audioGain.gain.value = pass ? 1 : 0;
}

// Called every time a new SNR sample arrives (from pushLiveSNR).
function updateSquelch(snrDB) {
  if (!squelchEnabled) return;

  if (snrDB >= SQUELCH_THRESHOLD) {
    // SNR is high enough — start (or keep) the hold timer.
    if (!squelchOpen && squelchAboveTimer === null) {
      squelchAboveTimer = setTimeout(() => {
        squelchAboveTimer = null;
        squelchOpen = true;
        applyGain();
        updateSquelchBtn(); // go green
      }, SQUELCH_HOLD_MS);
    }
  } else {
    // SNR dropped below threshold — cancel any pending open and close immediately.
    if (squelchAboveTimer !== null) {
      clearTimeout(squelchAboveTimer);
      squelchAboveTimer = null;
    }
    if (squelchOpen) {
      squelchOpen = false;
      applyGain();
      updateSquelchBtn(); // back to amber
    }
  }
}

function updateSquelchBtn() {
  const btn = document.getElementById('squelch-btn');
  if (!btn) return;
  if (!squelchEnabled) {
    // Squelch off — plain grey
    btn.classList.remove('active', 'open');
    btn.setAttribute('aria-pressed', 'false');
  } else if (squelchOpen) {
    // Squelch on AND open (signal above threshold) — green
    btn.classList.remove('active');
    btn.classList.add('open');
    btn.setAttribute('aria-pressed', 'true');
  } else {
    // Squelch on but closed (signal too low) — amber
    btn.classList.remove('open');
    btn.classList.add('active');
    btn.setAttribute('aria-pressed', 'true');
  }
}

function toggleSquelch() {
  squelchEnabled = !squelchEnabled;
  if (!squelchEnabled) {
    // Squelch turned off — cancel any pending timer, reset open state, restore gain.
    if (squelchAboveTimer !== null) {
      clearTimeout(squelchAboveTimer);
      squelchAboveTimer = null;
    }
    squelchOpen = false;
    applyGain();
  } else {
    // Squelch just enabled — start closed; will open once SNR holds above threshold.
    squelchOpen = false;
    applyGain();
  }
  updateSquelchBtn();
}

function updateMuteBtn() {
  const btn = document.getElementById('mute-btn');
  if (!btn) return;
  if (!audioPreviewEl) {
    // Not yet started — show muted icon, no active class
    btn.textContent = '🔇';
    btn.setAttribute('aria-pressed', 'false');
    btn.classList.remove('unmuted');
  } else if (audioMuted) {
    btn.textContent = '🔇';
    btn.setAttribute('aria-pressed', 'true');
    btn.classList.remove('unmuted');
  } else {
    btn.textContent = '🔊';
    btn.setAttribute('aria-pressed', 'false');
    btn.classList.add('unmuted');
  }
}

function toggleMute() {
  if (!audioPreviewEl) {
    // First click — start playback unmuted (this IS the user gesture).
    audioMuted = false;
    const label = audioPreviewLabel ||
      (window._instanceStatuses && window._instanceStatuses.length > 0
        ? window._instanceStatuses[0].label
        : '');
    startAudioPreview(label);
    return;
  }
  // Subsequent clicks — toggle mute; use applyGain() so squelch state is respected.
  audioMuted = !audioMuted;
  applyGain();
  updateMuteBtn();
}

// ---------------------------------------------------------------------------
// Audio output device selector (Chrome/Edge only — requires setSinkId support)
//
// Chrome requires a user gesture before it will grant speaker-selection
// permission.  Strategy:
//   1. On page load: call enumerateDevices() silently.  If the browser already
//      has permission (labels are non-empty) show the dropdown immediately.
//   2. If labels are empty (permission not yet granted), show the dropdown with
//      a single "🔊 Choose output…" placeholder and request permission the
//      first time the user interacts with it (focus/click triggers setSinkId('')
//      on a silent Audio element, which is the documented permission-request
//      path in Chrome 110+).
// ---------------------------------------------------------------------------
async function initAudioOutputSelector() {
  const sel = document.getElementById('audio-output-select');
  if (!sel) return;

  // AudioContext.setSinkId is Chrome 110+ on secure contexts (HTTPS/localhost).
  // Check via prototype to avoid creating an AudioContext at page load
  // (which would trigger Chrome's autoplay policy warning).
  if (typeof AudioContext === 'undefined') return;
  if (typeof AudioContext.prototype.setSinkId !== 'function') return;

  // Populate the dropdown with labelled audiooutput devices.
  // Returns true if labels were available, false if permission is still needed.
  const populate = async () => {
    const devices = await navigator.mediaDevices.enumerateDevices();
    const outputs = devices.filter(d => d.kind === 'audiooutput');
    if (outputs.length === 0) return false;
    const hasLabels = outputs.some(d => d.label !== '');
    if (!hasLabels) return false;

    const prev = audioOutputDeviceId || 'default';
    sel.innerHTML = '';
    for (const dev of outputs) {
      const opt = document.createElement('option');
      opt.value = dev.deviceId;
      opt.textContent = dev.label || (dev.deviceId === 'default' ? 'Default' : `Output ${dev.deviceId.slice(0, 8)}`);
      if (dev.deviceId === prev) opt.selected = true;
      sel.appendChild(opt);
    }
    sel.style.display = '';
    return true;
  };

  // In Chrome, audiooutput labels require microphone permission.
  // We request it via getUserMedia (audio only, tracks stopped immediately).
  const unlockLabels = async () => {
    try {
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true, video: false });
      stream.getTracks().forEach(t => t.stop());
    } catch (_) { /* user denied — labels stay empty */ }
    await populate();
  };

  // Try to populate immediately (works if permission was already granted).
  const ready = await populate();

  if (!ready) {
    // Show a "Grant permission" button-style option.
    // Selecting it triggers getUserMedia from a user gesture.
    sel.innerHTML = '<option value="">🔊 Grant permission…</option>';
    sel.style.display = '';

    sel.addEventListener('mousedown', function onMousedown() {
      sel.removeEventListener('mousedown', onMousedown);
      // Use setTimeout so the mousedown gesture is still active when
      // getUserMedia fires its permission prompt.
      setTimeout(unlockLabels, 0);
    }, { once: true });
  }

  // Re-populate when devices change (headphones plugged in, etc.).
  navigator.mediaDevices.addEventListener('devicechange', populate);

  // Handle device selection.
  sel.addEventListener('change', () => {
    if (!sel.value) return;
    audioOutputDeviceId = sel.value;
    // Apply to the live AudioContext if one is running.
    if (audioCtx && typeof audioCtx.setSinkId === 'function') {
      audioCtx.setSinkId(audioOutputDeviceId).catch(err => {
        console.warn('setSinkId failed:', err);
      });
    }
  });
}

// ---------------------------------------------------------------------------
// UberSDR URL widget
// ---------------------------------------------------------------------------

// Validate that a string is a well-formed http:// or https:// URL with a host.
// Port is optional.  Returns true if valid.
function isValidUberSDRURL(raw) {
  try {
    const u = new URL(raw.trim());
    return (u.protocol === 'http:' || u.protocol === 'https:') && u.hostname !== '';
  } catch (_) {
    return false;
  }
}

// Fetch the known-instances list from the server-side proxy and populate the
// <datalist id="ubersdr-instances-list">.  Each <option> has value=public_url
// and label="callsign - name" so the browser shows the human-readable label
// in the dropdown while inserting the URL into the input on selection.
function loadInstancesList() {
  const dl = document.getElementById('ubersdr-instances-list');
  if (!dl) return;
  // Only fetch once per page load (guard against repeated calls).
  if (dl.dataset.loaded) return;
  dl.dataset.loaded = '1';

  fetch(BASE_PATH + '/api/ubersdr/instances')
    .then(r => r.ok ? r.json() : Promise.reject(r.status))
    .then(data => {
      const instances = data.instances || [];
      dl.innerHTML = '';
      for (const inst of instances) {
        if (!inst.public_url) continue;
        const opt = document.createElement('option');
        // value is what gets inserted into the input when selected
        opt.value = inst.public_url.replace(/\/$/, '');
        // label is what the browser shows in the dropdown list
        opt.label = `${inst.callsign || '?'} — ${inst.name || inst.host || ''}`;
        dl.appendChild(opt);
      }
    })
    .catch(err => console.warn('ubersdr instances list:', err));
}

function initURLWidget() {
  const input    = document.getElementById('ubersdr-url-input');
  const setBtn   = document.getElementById('ubersdr-url-set-btn');
  const statusEl = document.getElementById('ubersdr-url-status');

  if (!input) return;

  // Load the instances list as soon as the user focuses the input so the
  // datalist dropdown is populated before they start typing.
  input.addEventListener('focus', () => {
    input._userEditing = true;
    loadInstancesList();
    // Save current value then clear so the datalist shows all options.
    input._valueBeforeFocus = input.value;
    input.value = '';
  });

  input.addEventListener('blur', () => {
    input._userEditing = false;
    // If the user left the field empty, restore the previous value.
    if (input.value.trim() === '') {
      input.value = input._valueBeforeFocus || '';
    }
  });

  // Fires when the user picks an option from the datalist dropdown.
  input.addEventListener('change', () => {
    if (input.value.trim() !== '') applyURL();
  });

  input.addEventListener('keydown', e => {
    if (e.key === 'Enter') { e.preventDefault(); applyURL(); }
    if (e.key === 'Escape') { input.blur(); }
  });

  if (setBtn) setBtn.addEventListener('click', applyURL);

  function applyURL() {
    const raw = input.value.trim();
    if (!isValidUberSDRURL(raw)) {
      if (statusEl) { statusEl.textContent = '✗ invalid URL'; statusEl.className = 'url-err'; }
      return;
    }
    requireAuth(() => _doApplyURL(raw, statusEl));
  }

  function _doApplyURL(raw, statusEl) {
    if (statusEl) { statusEl.textContent = '…'; statusEl.className = ''; }

    fetch(BASE_PATH + '/api/config/url', {
      method:  'POST',
      headers: { 'Content-Type': 'application/json' },
      body:    JSON.stringify({ url: raw }),
    })
      .then(r => {
        if (!r.ok) return r.text().then(t => { throw new Error(t); });
        return r.json();
      })
      .then(data => {
        setURLDisplay(data.url);
        if (statusEl) {
          statusEl.textContent = '✓';
          statusEl.className = 'url-ok';
          setTimeout(() => { if (statusEl) statusEl.textContent = ''; }, 3000);
        }
        // The server has reconnected all instances to the new UberSDR URL.
        // Always restart the audio preview stream (whether or not it was
        // already playing) so the browser fetches a fresh WAV header at the
        // new connection's actual sample rate.
        // Re-fetch status to get the updated instance label first.
        fetch(BASE_PATH + '/api/status')
          .then(r => r.json())
          .then(statusData => {
            applyStatusData(statusData);
            const newLabel = statusData.instances && statusData.instances.length > 0
              ? statusData.instances[0].label
              : audioPreviewLabel;
            console.log('[audio] URL changed — restarting preview for label:', newLabel);
            startAudioPreview(newLabel);
          })
          .catch(() => {
            console.log('[audio] URL changed — restarting preview (status fetch failed)');
            startAudioPreview(audioPreviewLabel);
          });
      })
      .catch(err => {
        if (statusEl) { statusEl.textContent = '✗ ' + err.message; statusEl.className = 'url-err'; }
      });
  }
}

// Update the URL input value to reflect the current connected URL.
function setURLDisplay(urlStr) {
  const input = document.getElementById('ubersdr-url-input');
  if (input && !input._userEditing) {
    input.value = urlStr || '';
    input._valueBeforeFocus = urlStr || '';
  }
}

// ---------------------------------------------------------------------------
// Status badges
// ---------------------------------------------------------------------------
function renderBadges(statuses) {
  // Cache for audio preview label selection.
  window._instanceStatuses = statuses;

  const container = document.getElementById('status-badges');
  container.innerHTML = '';
  for (const s of statuses) {
    const b = document.createElement('span');
    b.className = 'badge ' + (s.status || 'stopped');
    b.dataset.label = s.label;
    const icon = s.status === 'running' ? '●' : s.status === 'reconnecting' ? '↻' : '✕';
    b.textContent = `${icon} ${(s.freq_hz / 1e6).toFixed(3)} MHz ${(s.audio_mode || '').toUpperCase()}`;
    container.appendChild(b);
  }

  // Pre-populate the tune box with the first instance's current frequency.
  if (statuses.length > 0) {
    const first = statuses[0];
    const freqInput = document.getElementById('freq-input');
    if (freqInput) {
      const display = (first.freq_hz / 1e6).toFixed(3);
      freqInput._lastKnownFreq = display;
      // Only update the visible value when the user isn't actively editing
      if (!freqInput._userEditing) {
        freqInput.value = display;
      }
    }
  }
}

// ---------------------------------------------------------------------------
// Frequency tuning
// ---------------------------------------------------------------------------
// Minimum and maximum tunable frequency in Hz
const FREQ_MIN_HZ = 10e3;   // 10 kHz
const FREQ_MAX_HZ = 30e6;   // 30 MHz

function applyFrequency() {
  const freqInput = document.getElementById('freq-input');
  const statusEl  = document.getElementById('freq-status');

  if (!freqInput) return;

  const mhz = parseFloat(freqInput.value.trim());
  if (isNaN(mhz)) {
    if (statusEl) { statusEl.textContent = '✗ invalid frequency'; statusEl.className = 'freq-err'; }
    return;
  }
  const freqHz = Math.round(mhz * 1e6);
  if (freqHz < FREQ_MIN_HZ || freqHz > FREQ_MAX_HZ) {
    if (statusEl) {
      statusEl.textContent = '✗ must be 10 kHz – 30 MHz';
      statusEl.className = 'freq-err';
    }
    return;
  }

  // Pick the target instance label — use the first available instance.
  const statuses = window._instanceStatuses || [];
  if (statuses.length === 0) {
    if (statusEl) { statusEl.textContent = '✗ no instance'; statusEl.className = 'freq-err'; }
    return;
  }
  const label = statuses[0].label;

  requireAuth(() => {
    if (statusEl) { statusEl.textContent = '…'; statusEl.className = ''; }
    _doApplyFrequency(label, freqHz, statusEl);
  });
}

function _doApplyFrequency(label, freqHz, statusEl) {
  fetch(BASE_PATH + `/api/instances/${encodeURIComponent(label)}/frequency`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ freq_hz: freqHz }),
  })
    .then(r => {
      if (!r.ok) return r.text().then(t => { throw new Error(t); });
      return r.json();
    })
    .then(data => {
      if (statusEl) {
        statusEl.textContent = `✓ ${(data.freq_hz / 1e6).toFixed(3)} MHz`;
        statusEl.className = 'freq-ok';
        setTimeout(() => { if (statusEl) statusEl.textContent = ''; }, 3000);
      }
      // Update the cached status so the badge reflects the new freq immediately.
      if (window._instanceStatuses && window._instanceStatuses.length > 0) {
        window._instanceStatuses[0].freq_hz = data.freq_hz;
        window._instanceStatuses[0].label   = data.label;
        renderBadges(window._instanceStatuses);
      }
    })
    .catch(err => {
      if (statusEl) {
        statusEl.textContent = '✗ ' + err.message;
        statusEl.className = 'freq-err';
      }
    });
}

// ---------------------------------------------------------------------------
// SSE live feed
// ---------------------------------------------------------------------------
function connectSSE() {
  const dot = document.getElementById('live-dot');
  const es = new EventSource(BASE_PATH + '/api/live');

  es.addEventListener('image', e => {
    try {
      const rec = JSON.parse(e.data);
      // prependCard() itself checks deletedIDs, but guard here too so we
      // never even parse/process a record that was deleted this session.
      if (!deletedIDs.has(rec.id)) prependCard(rec);
    } catch (err) {
      console.error('SSE parse error', err);
    }
  });

  es.addEventListener('delete', e => {
    try {
      const d = JSON.parse(e.data);
      if (d.id) _removeRecordLocally(d.id);
    } catch (err) {
      console.error('SSE delete parse error', err);
    }
  });

  es.addEventListener('snr', e => {
    if (!e.data) return;
    try {
      const d = JSON.parse(e.data);
      if (d.snr_db != null) {
        // Always update the header sparkline.
        pushLiveSNR(d.snr_db);
        // Also feed the live RX detail chart while a reception is in progress.
        if (rxLiveActive) pushLiveRxSNR(d.t || Date.now(), d.snr_db);
      }
    } catch (err) {
      console.error('SSE snr parse error', err);
    }
  });

  es.onopen = () => {
    dot.textContent = '● live';
    dot.classList.remove('offline');
  };

  es.onerror = () => {
    dot.textContent = '○ offline';
    dot.classList.add('offline');
    es.close();
    setTimeout(connectSSE, 5000);
  };
}

// ---------------------------------------------------------------------------
// Live RX preview — connects to /api/rx/live and updates the live gallery
// card thumbnail and the "Now Receiving" detail panel.
// ---------------------------------------------------------------------------

// Helper: update the live gallery card thumbnail and meta strip.
function updateLiveCard(jpegB64, mode, callsign, freq) {
  const card    = document.getElementById('live-gallery-card');
  const cardImg = document.getElementById('live-card-img');
  const modeEl  = document.getElementById('live-card-mode');
  const callEl  = document.getElementById('live-card-call');
  const freqEl  = document.getElementById('live-card-freq');
  if (cardImg && jpegB64 != null) {
    cardImg.src = jpegB64 ? 'data:image/jpeg;base64,' + jpegB64 : '';
  }
  if (modeEl  && mode     != null) modeEl.textContent  = mode     || '—';
  if (callEl  && callsign != null) callEl.textContent   = callsign || '';
  if (freqEl  && freq     != null) freqEl.textContent   = freq     || '';
  if (card    && jpegB64  != null) {
    // Show/hide the idle overlay based on whether we have image data.
    if (jpegB64) card.classList.add('receiving');
    else         card.classList.remove('receiving');
  }
}

function connectRxLive(label) {
  // Disconnect any existing subscription first.
  if (rxLiveES) {
    rxLiveES.close();
    rxLiveES = null;
  }

  rxLiveLabel = label || '';
  const url = BASE_PATH + '/api/rx/live' + (rxLiveLabel ? '?label=' + encodeURIComponent(rxLiveLabel) : '');
  rxLiveES = new EventSource(url);

  const labelEl  = document.getElementById('rx-live-label');
  const bar      = document.getElementById('rx-live-progress-bar');
  const imgWrap  = document.getElementById('rx-live-image-wrap');
  const img      = document.getElementById('rx-live-image');
  const liveBar  = document.getElementById('rx-live-snr-bar');

  // ResizeObserver that redraws the live SNR bar whenever the canvas is resized
  // by the browser (e.g. as the image grows during reception).
  let liveBarRO = null;

  function redrawLiveBar() {
    // Pass rxLiveBarCurrentLine as filledLines so the bar tracks the actual
    // scan-line position rather than the (smaller) SNR sample count.
    renderSNRBar(liveBar, rxLiveBarSNRValues, rxLiveBarTotalLines, rxLiveBarCurrentLine);
  }

  // Track current mode/callsign/freq so we can update the live card meta.
  let currentMode = '';
  let currentCallsign = '';
  let currentFreq = '';

  function updateLiveLabel() {
    const parts = [];
    if (currentMode) parts.push(currentMode);
    if (currentCallsign) parts.push(currentCallsign);
    if (labelEl) labelEl.textContent = parts.length > 0 ? '— ' + parts.join(' · ') : '';
    // Keep the gallery card meta in sync.
    updateLiveCard(null, currentMode, currentCallsign, currentFreq);
  }

  rxLiveES.addEventListener('rx_start', e => {
    try {
      const d = JSON.parse(e.data);
      // Reset state.
      currentMode = sstvModeName(d.sstv_mode) || '';
      currentCallsign = '';
      currentFreq = d.freq_hz
        ? fmtFreq(d.freq_hz) + (d.audio_mode ? ' ' + d.audio_mode.toUpperCase() : '')
        : '';
      rxLiveActive = true;

      // Mark the live detail panel as actively receiving (shows content, hides idle msg).
      const liveDetailPanel = document.getElementById('rx-live-panel');
      if (liveDetailPanel) liveDetailPanel.classList.add('receiving');

      // Update live gallery card — clear image, set mode, start receiving state.
      updateLiveCard('', currentMode, '', currentFreq);

      // Auto-select the live card only if nothing is currently selected.
      // Never yank the user away from a specific image or the live panel they
      // are already viewing — the live gallery card thumbnail updates regardless.
      if (selectedID === null) {
        selectRecord('live');
      }

      updateLiveLabel();
      // For catch-up rx_start events, initialise the progress bar to the
      // current scan-line position rather than resetting to 0%.
      if (d.catchup && d.line > 0 && d.total > 0) {
        bar.style.width = Math.round((d.line + 1) / d.total * 100) + '%';
      } else {
        bar.style.width = '0%';
      }
      img.src = '';

      // Lock the wrapper to the known SSTV frame dimensions so the box
      // never reflows as the partial PNG streams in.
      if (imgWrap) {
        imgWrap.style.aspectRatio = d.width && d.height ? `${d.width}/${d.height}` : '';
      }
      img.style.aspectRatio = '';

      // Populate meta table with what we know so far.
      const freqStr = currentFreq || '—';
      const rxStartStr = d.rx_start
        ? fmtTime(new Date(d.rx_start).toISOString())
        : fmtTime(new Date().toISOString());
      buildLiveMeta({
        mode:    currentMode,
        callsign:'',
        freq:    freqStr,
        rxStart: rxStartStr,
        rxEnd:   '—',
        snrAvg:  '—',
        snrMin:  '—',
        snrMax:  '—',
        samples: null,
      });

      // Reset live SNR bar state and attach a ResizeObserver so the bar
      // redraws automatically whenever its CSS height changes.
      rxLiveBarSNRValues = [];
      rxLiveBarCurrentLine = 0;
      rxLiveBarTotalLines = (d.height && d.height > 0) ? d.height : 0;
      // For catch-up rx_start events (page loaded mid-decode), use the
      // original start timestamp from the server so the countdown reflects
      // how much time has already elapsed.  Fall back to Date.now() for a
      // genuine new rx_start where d.rx_start is absent or zero.
      rxLiveStartMs = (d.rx_start && d.rx_start > 0) ? d.rx_start : Date.now();
      rxLiveImageTimeMs = (d.image_time_ms && d.image_time_ms > 0) ? d.image_time_ms : 0;

      // Start a 1-second countdown ticker if we know the total duration.
      if (rxLiveCountdownTimer) { clearInterval(rxLiveCountdownTimer); rxLiveCountdownTimer = null; }
      const countdownEl = document.getElementById('rx-live-countdown');
      if (countdownEl) countdownEl.textContent = '';
      if (rxLiveImageTimeMs > 0 && countdownEl) {
        function tickCountdown() {
          const remainingMs = Math.max(0, rxLiveImageTimeMs - (Date.now() - rxLiveStartMs));
          const remainingSec = Math.round(remainingMs / 1000);
          if (remainingSec >= 60) {
            const m = Math.floor(remainingSec / 60);
            const s = remainingSec % 60;
            countdownEl.textContent = m + 'm ' + String(s).padStart(2, '0') + 's';
          } else {
            countdownEl.textContent = remainingSec + 's';
          }
          if (remainingMs === 0) {
            clearInterval(rxLiveCountdownTimer);
            rxLiveCountdownTimer = null;
            // The image transmission is mathematically complete.  The QSSTV sync
            // processor may take additional time to declare SYNCLOST (it stays
            // INSYNC while noise/FSK-ID produces valid-looking sync pulses), so
            // rx_end from the server may arrive late or not at all.  Stop the
            // SNR chart from accumulating further and mark reception as done so
            // the UI doesn't hang indefinitely.  The real rx_end (if it arrives)
            // will still be processed normally by the rx_end event handler.
            if (rxLiveActive) {
              console.warn('[rx] countdown reached 0 but rx_end not yet received — stopping SNR accumulation');
              rxLiveActive = false;
            }
          }
        }
        tickCountdown(); // show immediately
        rxLiveCountdownTimer = setInterval(tickCountdown, 1000);
      }
      if (liveBar) {
        liveBar.style.display = '';
        const ctx = liveBar.getContext('2d');
        ctx.clearRect(0, 0, liveBar.width, liveBar.height);
        if (liveBarRO) { liveBarRO.disconnect(); liveBarRO = null; }
        liveBarRO = new ResizeObserver(redrawLiveBar);
        liveBarRO.observe(liveBar);
      }

      // Reset and init the live SNR chart.
      initLiveRxSNRChart();

      // Hide map until a callsign arrives.
      const mapDiv   = document.getElementById('rx-live-map');
      const noCallEl = document.getElementById('rx-live-map-no-callsign');
      if (mapDiv)   mapDiv.style.display = 'none';
      if (noCallEl) { noCallEl.textContent = 'Waiting for callsign…'; noCallEl.style.display = 'block'; }
    } catch (err) {
      console.error('rx_start parse error', err);
    }
  });

  rxLiveES.addEventListener('rx_callsign', e => {
    try {
      const d = JSON.parse(e.data);
      if (d.callsign) {
        currentCallsign = d.callsign;
        updateLiveLabel();
        updateLiveMetaRow('Callsign', d.callsign);
        // Render the live origin map now that we have a callsign + CTY data.
        if (d.cty) renderLiveRxMap(d.cty);
      }
    } catch (err) {
      console.error('rx_callsign parse error', err);
    }
  });

  rxLiveES.addEventListener('rx_line', e => {
    try {
      const d = JSON.parse(e.data);
      // Update progress bar.
      if (d.total > 0) {
        bar.style.width = Math.round((d.line + 1) / d.total * 100) + '%';
      }
      // Update both the detail panel image and the live gallery card thumbnail.
      if (d.jpeg_b64) {
        img.src = 'data:image/jpeg;base64,' + d.jpeg_b64;
        updateLiveCard(d.jpeg_b64, null, null, null);
      }
      // Track the current scan-line position (1-based) for the SNR bar fill.
      if (d.total > 0) rxLiveBarCurrentLine = d.line + 1;
      // Sample the latest SNR value for this line and repaint the bar.
      // rxLiveSNRData is the live SNR accumulator fed by the SSE 'snr' events.
      if (liveBar) {
        const latestSNR = rxLiveSNRData.length > 0
          ? rxLiveSNRData[rxLiveSNRData.length - 1].y
          : null;
        if (latestSNR != null) {
          rxLiveBarSNRValues.push(latestSNR);
        }
        // totalLines: prefer the value from rx_start; fall back to d.total.
        if (!rxLiveBarTotalLines && d.total) rxLiveBarTotalLines = d.total;
        redrawLiveBar();
      }
    } catch (err) {
      console.error('rx_line parse error', err);
    }
  });

  rxLiveES.addEventListener('rx_end', e => {
    // Image complete — fill progress bar and update meta with final stats.
    rxLiveActive = false;
    bar.style.width = '100%';
    try {
      const d = JSON.parse(e.data);
      // Update RX end time and final SNR stats in the meta table.
      if (d.rx_end)        updateLiveMetaRow('RX end',  fmtTime(new Date(d.rx_end).toISOString()));
      if (d.snr_avg_db != null) updateLiveMetaRow('SNR avg', fmtSNR(d.snr_avg_db));
      if (d.snr_min_db != null) updateLiveMetaRow('SNR min', fmtSNR(d.snr_min_db));
      if (d.snr_max_db != null) updateLiveMetaRow('SNR max', fmtSNR(d.snr_max_db));
      if (d.snr_samples != null) updateLiveMetaRow('Samples', d.snr_samples);
      // Update callsign + map if decoded late (not already set by rx_callsign).
      if (d.callsign && !currentCallsign) {
        currentCallsign = d.callsign;
        updateLiveLabel();
        updateLiveMetaRow('Callsign', d.callsign);
        if (d.cty) renderLiveRxMap(d.cty);
      }
      // Finalise the SNR bar with authoritative decode completeness from the decoder.
      // image_height  = full frame height (totalLines denominator).
      // lines_decoded = how many lines were actually saved (filledLines numerator).
      // rxLiveBarCurrentLine was tracking the scan position during reception; replace
      // it with the authoritative lines_decoded value from the decoder so the final
      // bar position is exact even if some rx_line events were dropped.
      if (d.image_height  && d.image_height  > 0) rxLiveBarTotalLines  = d.image_height;
      if (d.lines_decoded && d.lines_decoded > 0) rxLiveBarCurrentLine = d.lines_decoded;
      redrawLiveBar();
      // Stop countdown timer and clear display.
      if (rxLiveCountdownTimer) { clearInterval(rxLiveCountdownTimer); rxLiveCountdownTimer = null; }
      const countdownEl = document.getElementById('rx-live-countdown');
      if (countdownEl) countdownEl.textContent = '';
      rxLiveStartMs = 0;
      rxLiveImageTimeMs = 0;
    } catch (_) { /* e.data may be absent on older server */ }

    // Disconnect the bar ResizeObserver — no more redraws needed.
    if (liveBarRO) { liveBarRO.disconnect(); liveBarRO = null; }

    // Reset the live gallery card and detail panel to idle state after a short
    // delay so the user sees the final frame before it clears.
    setTimeout(() => {
      // Only clear if we're not already in a new reception.
      if (!rxLiveActive) {
        currentMode = '';
        currentCallsign = '';
        currentFreq = '';
        updateLiveCard('', '', '', '');
        const card = document.getElementById('live-gallery-card');
        if (card) card.classList.remove('receiving');
        // Remove receiving class from detail panel so idle message shows.
        const liveDetailPanel = document.getElementById('rx-live-panel');
        if (liveDetailPanel) liveDetailPanel.classList.remove('receiving');
      }
      img.src = '';
      bar.style.width = '0%';
      if (labelEl) labelEl.textContent = '';
    }, 4000);
  });

  rxLiveES.addEventListener('rx_discarded', () => {
    // Image was discarded (too short) — reset immediately.
    rxLiveActive = false;
    if (liveBarRO) { liveBarRO.disconnect(); liveBarRO = null; }
    updateLiveCard('', '', '', '');
    const card = document.getElementById('live-gallery-card');
    if (card) card.classList.remove('receiving');
    // Remove receiving class from detail panel so idle message shows.
    const liveDetailPanel = document.getElementById('rx-live-panel');
    if (liveDetailPanel) liveDetailPanel.classList.remove('receiving');
    img.src = '';
    bar.style.width = '0%';
    currentMode = '';
    currentCallsign = '';
    currentFreq = '';
    if (labelEl) labelEl.textContent = '';
  });

  rxLiveES.onerror = () => {
    // Reconnect after 5 s — the server may have restarted.
    rxLiveES.close();
    rxLiveES = null;
    setTimeout(() => connectRxLive(rxLiveLabel), 5000);
  };
}

// ---------------------------------------------------------------------------
// Status polling
// ---------------------------------------------------------------------------
function applyStatusData(data) {
  if (data.instances) renderBadges(data.instances);
  if (data.receiver_lat != null) receiverLat = data.receiver_lat;
  if (data.receiver_lon != null) receiverLon = data.receiver_lon;
  if (data.ubersdr_url  != null) setURLDisplay(data.ubersdr_url);
  // Pick receiver info from the first instance that has it.
  if (data.instances) {
    for (const inst of data.instances) {
      if (inst.receiver && inst.receiver.lat) {
        receiverInfo = inst.receiver;
        // Also keep the legacy globals in sync for any code that still reads them.
        receiverLat = inst.receiver.lat;
        receiverLon = inst.receiver.lon;
        break;
      }
    }
  }
}

function pollStatus() {
  fetch(BASE_PATH + '/api/status')
    .then(r => r.json())
    .then(applyStatusData)
    .catch(() => {});
}

// ---------------------------------------------------------------------------
// Paginated gallery load
// ---------------------------------------------------------------------------
// Build the query-string for /api/images reflecting the current filter state.
function galleryFilterParams() {
  const params = new URLSearchParams({
    limit:  GALLERY_PAGE,
    offset: galleryOffset,
  });
  if (galleryCompleteOnly) params.set('complete', '1');
  if (gallerySNRFilter)    params.set('min_snr', '38');
  return params.toString();
}

function loadMoreImages() {
  if (galleryLoading || galleryExhausted) return;
  galleryLoading = true;

  fetch(BASE_PATH + `/api/images?${galleryFilterParams()}`)
    .then(r => r.json())
    .then(records => {
      if (!records || records.length === 0) {
        galleryExhausted = true;
        galleryLoading = false;
        return;
      }
      for (const rec of records) {
        allRecords.push(rec);
        appendCardToGroup(rec);
      }
      galleryOffset += records.length;
      if (records.length < GALLERY_PAGE) galleryExhausted = true;
      galleryLoading = false;

      // On initial load, select the live card by default (it's always first).
      // Only fall back to the first gallery record if nothing is selected yet
      // and the live card hasn't already been selected by an rx_start event.
      if (galleryOffset === records.length && selectedID === null) {
        selectRecord('live');
      }
    })
    .catch(err => {
      console.error('load images:', err);
      galleryLoading = false;
    });
}

// Clear the gallery and reload from offset 0 with the current filter params.
// Called whenever a filter checkbox changes.
function resetAndReloadGallery() {
  // Check before resetting allRecords whether the currently-selected record
  // still passes the new filter.  If not, close the detail panel now so the
  // user doesn't see a stale image while the gallery reloads.
  if (selectedID !== null && selectedID !== 'live') {
    const selectedRec = allRecords.find(r => r.id === selectedID);
    if (!selectedRec || !recPassesFilter(selectedRec)) {
      closeDetail();
    }
  }

  // Reset pagination state.
  allRecords = [];
  galleryOffset = 0;
  galleryLoading = false;
  galleryExhausted = false;
  lastRenderedDate = null;

  // Clear all day-group cards from the DOM (keep the sentinel and live card).
  const grid = document.getElementById('gallery-grid');
  if (grid) {
    grid.querySelectorAll('.day-group').forEach(g => g.remove());
  }
  updateGalleryCounts();

  loadMoreImages();
}

function initGalleryScroll() {
  const grid = document.getElementById('gallery-grid');

  // Load the first page immediately.
  loadMoreImages();

  // Load subsequent pages only when the user scrolls within 200px of the bottom.
  if (grid) {
    grid.addEventListener('scroll', () => {
      if (galleryLoading || galleryExhausted) return;
      const nearBottom = grid.scrollTop + grid.clientHeight >= grid.scrollHeight - 200;
      if (nearBottom) loadMoreImages();
    }, { passive: true });
  }
}

// ---------------------------------------------------------------------------
// Boot
// ---------------------------------------------------------------------------
// ---------------------------------------------------------------------------
// Audio panel — spectrum + waterfall + VU meter
// ---------------------------------------------------------------------------

// Display parameters (kept in sync with the control inputs)
const audioPanel = {
  maxDb:  -25,   // top of dB scale  (must match #ctrl-maxdb value attr)
  range:   60,   // dB span          (must match #ctrl-range value attr)
  avg:    0.90,  // smoothing factor (not used client-side — server applies it)
  // SSTV marker frequencies (Hz) — same as QSSTV SSTV mode defaults
  markers: [1200, 1500, 2300],
  // Frequency display range (must match fft.go constants)
  fftLow:  200,
  fftHigh: 2900,
};

// Waterfall image buffer (off-screen)
let waterfallImg = null;   // ImageData
let waterfallCtx = null;   // CanvasRenderingContext2D for waterfall-canvas

// FFT SSE connection
let fftES = null;
let fftLabel = '';

function initAudioPanel() {
  const spectrumCanvas   = document.getElementById('spectrum-canvas');
  const waterfallCanvas  = document.getElementById('waterfall-canvas');
  const markerSpectrum   = document.getElementById('marker-spectrum');
  const markerWaterfall  = document.getElementById('marker-waterfall');

  if (!spectrumCanvas || !waterfallCanvas) return;

  // Sync control inputs → audioPanel object
  const ctrlMaxDb = document.getElementById('ctrl-maxdb');
  const ctrlRange = document.getElementById('ctrl-range');
  const ctrlAvg   = document.getElementById('ctrl-avg');

  // Read initial values from HTML inputs (so JS state matches what's displayed)
  if (ctrlMaxDb) audioPanel.maxDb = parseFloat(ctrlMaxDb.value) || audioPanel.maxDb;
  if (ctrlRange) audioPanel.range = parseFloat(ctrlRange.value) || audioPanel.range;
  if (ctrlAvg)   audioPanel.avg   = parseFloat(ctrlAvg.value)   || audioPanel.avg;

  if (ctrlMaxDb) ctrlMaxDb.addEventListener('input', () => { audioPanel.maxDb = parseFloat(ctrlMaxDb.value) || -25; });
  if (ctrlRange) ctrlRange.addEventListener('input', () => { audioPanel.range = parseFloat(ctrlRange.value) || 60; });
  if (ctrlAvg)   ctrlAvg.addEventListener('input',   () => { audioPanel.avg   = parseFloat(ctrlAvg.value)   || 0.90; });

  // Draw marker ticks on a canvas
  function drawMarkers(canvas) {
    const ctx = canvas.getContext('2d');
    const w = canvas.offsetWidth || canvas.width;
    canvas.width = w;
    ctx.clearRect(0, 0, w, canvas.height);
    ctx.fillStyle = '#555';
    const span = audioPanel.fftHigh - audioPanel.fftLow;
    for (const hz of audioPanel.markers) {
      const x = Math.round(((hz - audioPanel.fftLow) / span) * w);
      ctx.fillRect(x, 0, 1, canvas.height);
      ctx.fillStyle = '#888';
      ctx.font = '6px monospace';
      ctx.fillText((hz / 1000).toFixed(1) + 'k', x + 2, canvas.height - 1);
      ctx.fillStyle = '#555';
    }
  }

  // Resize observer — keep canvases pixel-perfect
  const ro = new ResizeObserver(() => {
    const w  = spectrumCanvas.offsetWidth;
    const sh = spectrumCanvas.offsetHeight;
    const wh = waterfallCanvas.offsetHeight;

    // Guard: skip if layout hasn't happened yet
    if (w <= 0 || sh <= 0 || wh <= 0) return;

    if (spectrumCanvas.width !== w || spectrumCanvas.height !== sh) {
      spectrumCanvas.width  = w;
      spectrumCanvas.height = sh;
    }
    if (waterfallCanvas.width !== w || waterfallCanvas.height !== wh) {
      waterfallCanvas.width  = w;
      waterfallCanvas.height = wh;
      // Re-create waterfall image buffer at new size
      waterfallCtx = waterfallCanvas.getContext('2d');
      waterfallImg = waterfallCtx.createImageData(w, wh);
      // Fill black
      for (let i = 0; i < waterfallImg.data.length; i += 4) {
        waterfallImg.data[i]   = 0;
        waterfallImg.data[i+1] = 0;
        waterfallImg.data[i+2] = 0;
        waterfallImg.data[i+3] = 255;
      }
    }
    drawMarkers(markerSpectrum);
    drawMarkers(markerWaterfall);
  });
  ro.observe(spectrumCanvas);
  ro.observe(waterfallCanvas);

  // Initial marker draw (deferred so layout is complete)
  requestAnimationFrame(() => {
    drawMarkers(markerSpectrum);
    drawMarkers(markerWaterfall);
  });
}

// Render one FFT frame onto the spectrum and waterfall canvases.
function renderFFTFrame(frame) {
  const bins = frame.bins;          // Float32Array-like (plain array from JSON)
  const nBins = bins.length;

  // ── Spectrum canvas ──────────────────────────────────────────────────────
  const specCanvas = document.getElementById('spectrum-canvas');
  // Lazy-init: set canvas backing dimensions from CSS layout if not yet done.
  if (specCanvas && (specCanvas.width === 0 || specCanvas.height === 0)) {
    const sw = specCanvas.offsetWidth;
    const sh = specCanvas.offsetHeight;
    if (sw > 0 && sh > 0) {
      specCanvas.width  = sw;
      specCanvas.height = sh;
    }
  }
  if (specCanvas && specCanvas.width > 0 && specCanvas.height > 0) {
    const ctx = specCanvas.getContext('2d');
    const w = specCanvas.width;
    const h = specCanvas.height;

    ctx.fillStyle = '#00007f';
    ctx.fillRect(0, 0, w, h);

    // Draw marker lines (red)
    ctx.strokeStyle = '#e94560';
    ctx.lineWidth = 1;
    const span = audioPanel.fftHigh - audioPanel.fftLow;
    for (const hz of audioPanel.markers) {
      const x = Math.round(((hz - audioPanel.fftLow) / span) * w);
      ctx.beginPath();
      ctx.moveTo(x, 0);
      ctx.lineTo(x, h);
      ctx.stroke();
    }

    // Draw spectrum polyline (green)
    ctx.strokeStyle = '#00ff00';
    ctx.lineWidth = 1;
    ctx.beginPath();
    for (let j = 0; j < nBins; j++) {
      const x = Math.round((j / (nBins - 1)) * (w - 1));
      const db = bins[j];
      // Map dB to y: maxDb → top (0), maxDb-range → bottom (h)
      let t = (audioPanel.maxDb - db) / audioPanel.range;
      if (t < 0) t = 0;
      if (t > 1) t = 1;
      const y = Math.round(t * (h - 1));
      if (j === 0) ctx.moveTo(x, y);
      else ctx.lineTo(x, y);
    }
    ctx.stroke();
  }

  // ── Waterfall canvas ─────────────────────────────────────────────────────
  // Lazy-init: if the ResizeObserver fired before layout was complete, the
  // buffer will still be null.  Initialise it now from the canvas dimensions.
  if (!waterfallCtx || !waterfallImg) {
    const wc = document.getElementById('waterfall-canvas');
    if (wc && wc.offsetWidth > 0 && wc.offsetHeight > 0) {
      wc.width  = wc.offsetWidth;
      wc.height = wc.offsetHeight;
      waterfallCtx = wc.getContext('2d');
      waterfallImg = waterfallCtx.createImageData(wc.width, wc.height);
      for (let i = 0; i < waterfallImg.data.length; i += 4) {
        waterfallImg.data[i]   = 0;
        waterfallImg.data[i+1] = 0;
        waterfallImg.data[i+2] = 0;
        waterfallImg.data[i+3] = 255;
      }
    }
  }
  if (waterfallCtx && waterfallImg) {
    const w = waterfallImg.width;
    const h = waterfallImg.height;

    // Scroll existing image down by 1 row
    const rowBytes = w * 4;
    waterfallImg.data.copyWithin(rowBytes, 0, (h - 1) * rowBytes);

    // Write new top row
    for (let j = 0; j < w; j++) {
      // Map bin index to output pixel
      const binIdx = Math.round((j / (w - 1)) * (nBins - 1));
      const db = bins[binIdx];
      // Normalise to [0,1]: 0 = noise floor, 1 = full scale
      let t = 1 - (audioPanel.maxDb - db) / audioPanel.range;
      if (t < 0) t = 0;
      if (t > 1) t = 1;
      // HSV: hue 240° (blue) → 180° (cyan) as t increases (matches QSSTV)
      const hue = 240 - t * 60;
      const [r, g, b] = hsvToRgb(hue, 1, t);
      const idx = j * 4;
      waterfallImg.data[idx]   = r;
      waterfallImg.data[idx+1] = g;
      waterfallImg.data[idx+2] = b;
      waterfallImg.data[idx+3] = 255;
    }

    waterfallCtx.putImageData(waterfallImg, 0, 0);
  }

  // ── VU meter ─────────────────────────────────────────────────────────────
  const vuBar = document.getElementById('vu-bar');
  if (vuBar) {
    // volume_db is in dBFS; map [-60, 0] → [0%, 100%]
    const vdb = frame.volume_db;
    let pct = Math.max(0, Math.min(100, (vdb + 60) / 60 * 100));
    vuBar.style.width = pct + '%';
    // Colour: green → yellow → red
    if (pct > 85) {
      vuBar.style.backgroundColor = '#eb5757';
    } else if (pct > 60) {
      vuBar.style.backgroundColor = '#f2c94c';
    } else {
      vuBar.style.backgroundColor = '#6fcf97';
    }
  }
}

// HSV → RGB helper (h in [0,360], s/v in [0,1]) → [r,g,b] in [0,255]
function hsvToRgb(h, s, v) {
  h = ((h % 360) + 360) % 360;
  const c = v * s;
  const x = c * (1 - Math.abs((h / 60) % 2 - 1));
  const m = v - c;
  let r = 0, g = 0, b = 0;
  if      (h < 60)  { r = c; g = x; b = 0; }
  else if (h < 120) { r = x; g = c; b = 0; }
  else if (h < 180) { r = 0; g = c; b = x; }
  else if (h < 240) { r = 0; g = x; b = c; }
  else if (h < 300) { r = x; g = 0; b = c; }
  else              { r = c; g = 0; b = x; }
  return [Math.round((r + m) * 255), Math.round((g + m) * 255), Math.round((b + m) * 255)];
}

function connectFFT(label) {
  if (fftES) { fftES.close(); fftES = null; }
  fftLabel = label || '';
  const url = BASE_PATH + '/api/fft' + (fftLabel ? '?label=' + encodeURIComponent(fftLabel) : '');
  fftES = new EventSource(url);

  fftES.addEventListener('fft', e => {
    try {
      const frame = JSON.parse(e.data);
      renderFFTFrame(frame);
    } catch (err) {
      console.error('FFT parse error', err);
    }
  });

  fftES.onerror = () => {
    fftES.close();
    fftES = null;
    setTimeout(() => connectFFT(fftLabel), 5000);
  };
}

// ---------------------------------------------------------------------------
// Boot
// ---------------------------------------------------------------------------
document.addEventListener('DOMContentLoaded', () => {
  // "Complete only" gallery filter checkbox
  const completeOnlyCb = document.getElementById('gallery-complete-only');
  if (completeOnlyCb) {
    galleryCompleteOnly = completeOnlyCb.checked; // true by default (checked in HTML)
    completeOnlyCb.addEventListener('change', () => {
      galleryCompleteOnly = completeOnlyCb.checked;
      resetAndReloadGallery();
    });
  }

  // "Show latest" checkbox — jump to newest image after each live decode
  const showLatestCb = document.getElementById('gallery-show-latest');
  if (showLatestCb) {
    galleryShowLatest = showLatestCb.checked; // true by default (checked in HTML)
    showLatestCb.addEventListener('change', () => {
      galleryShowLatest = showLatestCb.checked;
    });
  }

  // "≥38 dB" SNR filter checkbox
  const snrFilterCb = document.getElementById('gallery-snr-filter');
  if (snrFilterCb) {
    gallerySNRFilter = snrFilterCb.checked; // true by default (checked in HTML)
    snrFilterCb.addEventListener('change', () => {
      gallerySNRFilter = snrFilterCb.checked;
      resetAndReloadGallery();
    });
  }

  initLiveSNRChart();
  initAudioOutputSelector();
  initAudioPanel();
  initURLWidget();
  initGalleryScroll();
  connectSSE();

  // Poll status once immediately; on the first successful response start the
  // live RX preview and audio panel for the first instance we find.
  fetch(BASE_PATH + '/api/status')
    .then(r => r.json())
    .then(data => {
      applyStatusData(data);
      const firstLabel = data.instances && data.instances.length > 0
        ? data.instances[0].label
        : '';
      // Cache the label so toggleMute() knows which instance to connect to.
      audioPreviewLabel = firstLabel;
      connectRxLive(firstLabel);
      connectFFT(firstLabel);
      // Do NOT call startAudioPreview() here — browser blocks autoplay.
      // The mute button click is the user gesture that starts playback.
      updateMuteBtn();
    })
    .catch(() => {
      connectRxLive('');
      connectFFT('');
      updateMuteBtn();
    });

  setInterval(pollStatus, 15000);

  // Fetch auth status on boot so requireAuth() knows whether a password is
  // configured and whether the current session is already authenticated.
  // If the session has expired but we have a stored password, re-authenticate
  // silently so the user doesn't have to type it again.
  fetch(BASE_PATH + '/api/auth/status')
    .then(r => r.json())
    .then(d => {
      authPasswordConfigured = !!d.password_configured;
      authAuthenticated      = !!d.authenticated;
      if (authPasswordConfigured && !authAuthenticated) {
        // Session expired (or new tab) — try stored password silently.
        tryStoredPassword(() => {}, () => {});
      }
    })
    .catch(() => {});

  // Detail navigation buttons
  const prevBtn = document.getElementById('detail-prev-btn');
  if (prevBtn) prevBtn.addEventListener('click', navigatePrev);
  const nextBtn = document.getElementById('detail-next-btn');
  if (nextBtn) nextBtn.addEventListener('click', navigateNext);

  // Today's Slideshow checkbox
  const slideshowCb = document.getElementById('detail-slideshow-cb');
  if (slideshowCb) {
    slideshowCb.addEventListener('change', () => toggleSlideshow(slideshowCb.checked));
  }

  // Live gallery card click — select the live detail view
  const liveGalleryCard = document.getElementById('live-gallery-card');
  if (liveGalleryCard) {
    liveGalleryCard.addEventListener('click', () => selectRecord('live'));
  }

  // Detail close button
  const closeBtn = document.getElementById('detail-close-btn');
  if (closeBtn) {
    closeBtn.addEventListener('click', closeDetail);
  }

  // Delete button — single listener; reads selectedID at click time
  // Guard: don't try to delete the live sentinel.
  const deleteBtnBoot = document.getElementById('detail-delete-btn');
  if (deleteBtnBoot) {
    deleteBtnBoot.addEventListener('click', () => {
      if (selectedID && selectedID !== 'live') deleteRecord(selectedID);
    });
  }

  // Lightbox — wire up close button, backdrop click, and Escape key
  const lightbox      = document.getElementById('lightbox');
  const lightboxClose = document.getElementById('lightbox-close');
  if (lightboxClose) lightboxClose.addEventListener('click', closeLightbox);
  if (lightbox) {
    // Click on the backdrop (not the image) closes the lightbox
    lightbox.addEventListener('click', e => {
      if (e.target === lightbox) closeLightbox();
    });
  }
  document.addEventListener('keydown', e => {
    if (e.key === 'Escape') closeLightbox();
  });

  // Detail panel image — click to open lightbox
  const detailImg = document.getElementById('detail-image');
  if (detailImg) {
    detailImg.addEventListener('click', () => {
      if (detailImg.src) openLightbox(detailImg.src);
    });
  }


  // Squelch button
  const squelchBtn = document.getElementById('squelch-btn');
  if (squelchBtn) {
    squelchBtn.addEventListener('click', toggleSquelch);
  }

  // Mute button
  const muteBtn = document.getElementById('mute-btn');
  if (muteBtn) {
    muteBtn.addEventListener('click', toggleMute);
  }

  // Frequency tune controls
  const freqInput = document.getElementById('freq-input');
  const freqBtn   = document.getElementById('freq-set-btn');

  if (freqInput) {
    freqInput.addEventListener('focus', () => {
      freqInput._userEditing = true;
      // Save whatever is currently shown, then clear so datalist shows all options
      freqInput._valueBeforeFocus = freqInput.value;
      freqInput.value = '';
    });
    freqInput.addEventListener('blur', () => {
      freqInput._userEditing = false;
      // If the user left the field empty (didn't pick or type anything), restore
      // the last known frequency so the box doesn't go blank.
      if (freqInput.value.trim() === '') {
        freqInput.value = freqInput._lastKnownFreq || freqInput._valueBeforeFocus || '';
      }
    });
    // Fires when the user picks an option from the datalist dropdown
    freqInput.addEventListener('change', () => {
      if (freqInput.value.trim() !== '') applyFrequency();
    });
    freqInput.addEventListener('keydown', e => {
      if (e.key === 'Enter') { e.preventDefault(); applyFrequency(); }
    });
  }
  if (freqBtn) {
    freqBtn.addEventListener('click', applyFrequency);
  }
});
