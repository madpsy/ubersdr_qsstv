/* metrics.js — Decode statistics modal */
'use strict';

// Inherit BASE_PATH set by app.js (both scripts share the same window scope).
// Falls back to '' when accessed directly without the proxy.
const _metricsBasePath = (typeof BASE_PATH === 'string') ? BASE_PATH : '';

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------
let metricsChart = null;
let metricsCurrentPeriod = '24h';
// '' = every channel.  Otherwise an instance label, passed straight through to
// /api/metrics as `label=` (mutually exclusive with `freq`, which the UI never
// sends).  Deliberately NOT persisted: the modal is a transient view, and it
// always opens showing the whole picture.
let metricsChannelFilter = '';
let metricsChipSig = '';

// The channel list and the accent hash both come from app.js, which is loaded
// first and shares this window scope — one hash, so a channel is the same
// colour in the rail, the gallery and here.
function metricsChannels() {
  return (typeof railChannels !== 'undefined' && railChannels) ? [...railChannels.values()] : [];
}

function metricsAccent(label) {
  return (typeof channelAccent === 'function') ? channelAccent(label) : '#a0c4ff';
}

// Render one by_channel entry's name.  Rows written before per-channel
// attribution have no audio mode at all: the server reports those with `label`
// set to the bare frequency and an empty `audio_mode`.  Showing a naked
// "14230000" or a blank mode would be meaningless, so they are named as the
// frequency plus an explicit "pre-upgrade" qualifier.
function metricsChannelName(c) {
  const hz   = (c && c.freq_hz != null) ? c.freq_hz : null;
  const freq = hz != null ? (hz / 1e6).toFixed(3) + ' MHz' : (c && c.label) || '—';
  const mode = (c && c.audio_mode || '').toUpperCase();
  return mode ? freq + ' ' + mode : freq + ' · pre-upgrade';
}

// ---------------------------------------------------------------------------
// Open / close
// ---------------------------------------------------------------------------
function openMetricsModal() {
  const modal = document.getElementById('metrics-modal');
  if (!modal) return;
  modal.classList.add('open');
  document.body.style.overflow = 'hidden';
  // The channel set can change between opens (a restart with a new config), so
  // rebuild the chips — and drop a filter naming a channel that is now gone.
  if (metricsChannelFilter && !metricsChannels().some(c => c.label === metricsChannelFilter)) {
    metricsChannelFilter = '';
  }
  renderMetricsChannelChips();
  fetchMetrics(metricsCurrentPeriod);
}

function closeMetricsModal() {
  const modal = document.getElementById('metrics-modal');
  if (!modal) return;
  modal.classList.remove('open');
  document.body.style.overflow = '';
}

// ---------------------------------------------------------------------------
// Fetch + render
// ---------------------------------------------------------------------------
function fetchMetrics(period) {
  metricsCurrentPeriod = period;

  // Update active tab
  document.querySelectorAll('.period-tab').forEach(btn => {
    btn.classList.toggle('active', btn.dataset.period === period);
  });

  // Clear summary while loading
  ['metrics-total', 'metrics-complete', 'metrics-partial', 'metrics-snr'].forEach(id => {
    const el = document.getElementById(id);
    if (el) el.textContent = '…';
  });

  // label= narrows the whole modal — summary, chart and breakdowns — to one
  // channel.  It is mutually exclusive with freq=, which the UI never sends.
  const chanParam = metricsChannelFilter
    ? '&label=' + encodeURIComponent(metricsChannelFilter) : '';

  fetch(_metricsBasePath + '/api/metrics?period=' + encodeURIComponent(period) + chanParam)
    .then(r => {
      if (!r.ok) {
        const e = new Error('HTTP ' + r.status);
        e.status = r.status;
        throw e;
      }
      return r.json();
    })
    .then(data => renderMetrics(data))
    .catch(err => {
      console.error('metrics fetch:', err);
      ['metrics-total', 'metrics-complete', 'metrics-partial', 'metrics-snr'].forEach(id => {
        const el = document.getElementById(id);
        if (el) el.textContent = '—';
      });
      // A rejected channel filter must not leave the modal permanently stuck
      // on dashes — fall back to every channel and retry once.
      if (err && err.status === 400 && metricsChannelFilter) {
        metricsChannelFilter = '';
        renderMetricsChannelChips();
        fetchMetrics(period);
      }
    });
}

// ---------------------------------------------------------------------------
// Channel filter chips
// ---------------------------------------------------------------------------
function setMetricsChannelFilter(label) {
  const next = label || '';
  if (next === metricsChannelFilter) return;
  metricsChannelFilter = next;
  updateMetricsChipState();
  fetchMetrics(metricsCurrentPeriod);
}

function updateMetricsChipState() {
  const wrap = document.getElementById('metrics-channel-filter');
  if (!wrap) return;
  wrap.querySelectorAll('.metrics-channel-chip').forEach(chip => {
    const on = (chip.dataset.label || '') === metricsChannelFilter;
    chip.classList.toggle('active', on);
    chip.setAttribute('aria-pressed', on ? 'true' : 'false');
  });
}

function buildMetricsChip(label, text, accent) {
  const chip = document.createElement('span');
  chip.className = 'metrics-channel-chip';
  chip.dataset.label = label;
  chip.textContent = text;
  chip.setAttribute('role', 'button');
  chip.setAttribute('tabindex', '0');
  chip.setAttribute('aria-pressed', 'false');
  chip.title = label ? 'Show statistics for this channel only'
                     : 'Show statistics for every channel';
  if (accent && chip.style && chip.style.setProperty) {
    chip.style.setProperty('--chip-accent', accent);
  }
  chip.addEventListener('click', () => setMetricsChannelFilter(label));
  chip.addEventListener('keydown', ev => {
    if (ev.key === 'Enter' || ev.key === ' ' || ev.key === 'Spacebar') {
      ev.preventDefault();
      setMetricsChannelFilter(label);
    }
  });
  return chip;
}

function renderMetricsChannelChips() {
  const wrap = document.getElementById('metrics-channel-filter');
  if (!wrap) return;
  const chans = metricsChannels();

  // One channel: the filter would be a no-op, so it stays out of the way —
  // the same rule the rail and the gallery chips follow.
  if (chans.length < 2) {
    wrap.hidden = true;
    wrap.innerHTML = '';
    metricsChipSig = '';
    return;
  }

  wrap.hidden = false;
  const sig = chans.map(c => c.label).join('|');
  if (sig === metricsChipSig) {
    updateMetricsChipState();
    return;
  }
  metricsChipSig = sig;
  wrap.innerHTML = '';
  wrap.appendChild(buildMetricsChip('', 'All channels', ''));
  for (const ch of chans) {
    wrap.appendChild(buildMetricsChip(ch.label, metricsChannelName(ch), metricsAccent(ch.label)));
  }
  updateMetricsChipState();
}

function renderMetrics(data) {
  // Summary stats
  const totalEl    = document.getElementById('metrics-total');
  const completeEl = document.getElementById('metrics-complete');
  const partialEl  = document.getElementById('metrics-partial');
  const snrEl      = document.getElementById('metrics-snr');

  if (totalEl)    totalEl.textContent    = data.total    != null ? data.total    : '—';
  if (completeEl) completeEl.textContent = data.complete != null ? data.complete : '—';
  if (partialEl)  partialEl.textContent  = data.partial  != null ? data.partial  : '—';
  if (snrEl) {
    if (data.avg_snr_db) {
      snrEl.textContent  = data.avg_snr_db.toFixed(1) + ' dB';
      snrEl.style.color  = snrColor(data.avg_snr_db);
    } else {
      snrEl.textContent  = '—';
      snrEl.style.color  = '';
    }
  }

  // By-mode chips
  const modeEl = document.getElementById('metrics-by-mode');
  if (modeEl) {
    modeEl.innerHTML = '';
    if (data.by_mode && Object.keys(data.by_mode).length > 0) {
      // Sort by count descending
      const sorted = Object.entries(data.by_mode).sort((a, b) => b[1] - a[1]);
      for (const [mode, count] of sorted) {
        const chip = document.createElement('span');
        chip.className = 'metrics-mode-chip';
        chip.textContent = mode + ' × ' + count;
        modeEl.appendChild(chip);
      }
    } else {
      modeEl.innerHTML = '<span style="color:#555;font-size:0.8rem">No decodes in this period</span>';
    }
  }

  // By-channel breakdown
  renderMetricsByChannel(data);

  // Chart
  renderMetricsChart(data);
}

// `by_channel` is a SORTED ARRAY (count desc, then label asc), not a map, so it
// is rendered in the order the server gives it.
function renderMetricsByChannel(data) {
  const el = document.getElementById('metrics-by-channel');
  if (!el) return;
  const rows = Array.isArray(data.by_channel) ? data.by_channel : [];

  // With a single entry the breakdown just restates the summary above it.
  if (rows.length < 2) {
    el.hidden = true;
    el.innerHTML = '';
    return;
  }
  el.hidden = false;
  el.innerHTML = '';

  const head = document.createElement('div');
  head.className = 'metrics-section-label';
  head.textContent = 'By channel';
  el.appendChild(head);

  for (const c of rows) {
    const legacy = !(c.audio_mode || '');
    const row = document.createElement('div');
    row.className = 'metrics-channel-row' + (legacy ? ' legacy' : '');
    if (row.style && row.style.setProperty) {
      row.style.setProperty('--chip-accent', metricsAccent(c.label));
    }

    const name = document.createElement('span');
    name.className = 'metrics-channel-name';
    name.textContent = metricsChannelName(c);
    if (legacy) {
      name.title = 'Images decoded before per-channel attribution — the audio mode was not recorded';
    }

    const counts = document.createElement('span');
    counts.className = 'metrics-channel-counts';
    counts.textContent = `${c.count != null ? c.count : 0} · ✅ ${c.complete != null ? c.complete : 0} · ❌ ${c.partial != null ? c.partial : 0}`;

    const snr = document.createElement('span');
    snr.className = 'metrics-channel-snr';
    if (c.avg_snr_db) {
      snr.textContent = c.avg_snr_db.toFixed(1) + ' dB';
      snr.style.color = snrColor(c.avg_snr_db);
    } else {
      snr.textContent = '—';
    }

    row.appendChild(name);
    row.appendChild(counts);
    row.appendChild(snr);
    el.appendChild(row);
  }
}

function renderMetricsChart(data) {
  const canvas = document.getElementById('metrics-chart');
  if (!canvas) return;

  if (metricsChart) {
    metricsChart.destroy();
    metricsChart = null;
  }

  const buckets = data.by_hour || [];

  if (buckets.length === 0) {
    // Nothing to show — draw a placeholder message
    const ctx = canvas.getContext('2d');
    ctx.clearRect(0, 0, canvas.width, canvas.height);
    ctx.fillStyle = '#555';
    ctx.font = '14px sans-serif';
    ctx.textAlign = 'center';
    ctx.fillText('No decodes in this period', canvas.width / 2, canvas.height / 2);
    return;
  }

  const completeData = buckets.map(b => ({ x: b.t, y: b.complete }));
  const partialData  = buckets.map(b => ({ x: b.t, y: b.partial  }));

  // Choose time unit based on period
  let timeUnit = 'hour';
  let displayFormat = { hour: 'HH:mm', day: 'MMM d' };
  if (metricsCurrentPeriod === '7d' || metricsCurrentPeriod === '30d') {
    timeUnit = 'day';
  }

  metricsChart = new Chart(canvas, {
    type: 'bar',
    data: {
      datasets: [
        {
          label: 'Complete',
          data: completeData,
          backgroundColor: 'rgba(111, 207, 151, 0.75)',
          borderColor: '#6fcf97',
          borderWidth: 1,
          stack: 'decodes',
        },
        {
          label: 'Partial',
          data: partialData,
          backgroundColor: 'rgba(242, 201, 76, 0.65)',
          borderColor: '#f2c94c',
          borderWidth: 1,
          stack: 'decodes',
        },
      ],
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      animation: false,
      parsing: false,
      plugins: {
        legend: {
          labels: { color: '#aaa', font: { size: 11 }, boxWidth: 12 },
        },
        tooltip: {
          callbacks: {
            title: ctx => {
              const d = new Date(ctx[0].parsed.x);
              if (timeUnit === 'day') {
                return d.toLocaleDateString(undefined, { weekday: 'short', month: 'short', day: 'numeric' });
              }
              return d.toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
            },
            label: ctx => `${ctx.dataset.label}: ${ctx.parsed.y}`,
          },
        },
      },
      scales: {
        x: {
          type: 'time',
          time: {
            unit: timeUnit,
            displayFormats: displayFormat,
          },
          stacked: true,
          ticks: { color: '#888', font: { size: 10 }, maxTicksLimit: 12 },
          grid: { color: '#1e2d4a' },
        },
        y: {
          stacked: true,
          beginAtZero: true,
          ticks: {
            color: '#888',
            font: { size: 10 },
            stepSize: 1,
            precision: 0,
          },
          grid: { color: '#1e2d4a' },
          title: { display: true, text: 'Decodes', color: '#888', font: { size: 10 } },
        },
      },
    },
  });
}

// ---------------------------------------------------------------------------
// Boot — wire up once DOM is ready
// ---------------------------------------------------------------------------
document.addEventListener('DOMContentLoaded', () => {
  // Open button
  const btn = document.getElementById('metrics-btn');
  if (btn) btn.addEventListener('click', openMetricsModal);

  // Close button
  const closeBtn = document.getElementById('metrics-modal-close');
  if (closeBtn) closeBtn.addEventListener('click', closeMetricsModal);

  // Backdrop click
  const modal = document.getElementById('metrics-modal');
  if (modal) {
    modal.addEventListener('click', e => {
      if (e.target === modal) closeMetricsModal();
    });
  }

  // Escape key
  document.addEventListener('keydown', e => {
    if (e.key === 'Escape') {
      const m = document.getElementById('metrics-modal');
      if (m && m.classList.contains('open')) closeMetricsModal();
    }
  });

  // Period tabs
  document.querySelectorAll('.period-tab').forEach(tab => {
    tab.addEventListener('click', () => fetchMetrics(tab.dataset.period));
  });
});
