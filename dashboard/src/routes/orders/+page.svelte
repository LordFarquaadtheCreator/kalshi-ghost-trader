<script>
  import { createPoll } from '$lib/poll.js';
  import { api } from '$lib/api.js';
  import { fmtTime, fmtTicker, seriesFromTicker, fmtPnL, fmtPct, vibrantColor } from '$lib/utils.js';
  import { setupChart } from '$lib/chart-init.js';
  import { browser } from '$app/environment';
  import { goto } from '$app/navigation';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import Badge from '$lib/components/Badge.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import CollapsibleSection from '$lib/components/CollapsibleSection.svelte';
  import ChartLoading from '$lib/components/ChartLoading.svelte';
  import StatAnalysis from '$lib/components/StatAnalysis.svelte';

  const PAGE_SIZE = 200;
  const store = createPoll(() => api.getOrders({ limit: PAGE_SIZE }), 5000, { data: null, error: null, connected: false });

  let data = $derived($store.data);
  let loading = $derived(!$store.data && $store.connected === false && !$store.error);
  let selectedStrategies = $state(new Set());
  let minPrice = $state(0);
  let maxPrice = $state(0);
  let filterMatch = $state('');
  let filterResult = $state('');

  // Pagination: recent page is polled. Older pages are loaded on demand via
  // "Load more" and held outside the poll cycle. Dedup by id guards against
  // a row shifting from older into recent as new orders land.
  /** @type {any[]} */
  let olderOrders = $state([]);
  /** @type {{ts: number, id: number} | null} */
  let olderCursor = $state(null);
  let olderHasMore = $state(false);
  let loadingMore = $state(false);

  // When the polled recent page advances, drop older rows that have crossed
  // into the recent window so we don't double-render them.
  $effect(() => {
    if (!data?.next_cursor) return;
    const b = data.next_cursor;
    olderOrders = olderOrders.filter((o) => o.ts < b.ts || (o.ts === b.ts && o.id <= b.id));
    if (olderOrders.length === 0) {
      olderCursor = b;
      olderHasMore = data.has_more;
    }
  });

  /** @type {any[]} */
  let allOrders = $derived.by(() => {
    const recent = data?.orders || [];
    const seen = new Set();
    const out = [];
    for (const o of recent) { seen.add(o.id); out.push(o); }
    for (const o of olderOrders) {
      if (!seen.has(o.id)) { seen.add(o.id); out.push(o); }
    }
    return out;
  });

  let totalOrders = $derived(data?.summary?.total_orders ?? 0);
  let loadedCount = $derived(allOrders.length);

  async function loadMore() {
    if (!olderCursor || loadingMore) return;
    loadingMore = true;
    try {
      const page = await api.getOrders({ cursor_ts: olderCursor.ts, cursor_id: olderCursor.id, limit: PAGE_SIZE });
      if (page?.orders) olderOrders = [...olderOrders, ...page.orders];
      olderCursor = page?.next_cursor || null;
      olderHasMore = page?.has_more || false;
    } catch (err) {
      console.error('load more orders failed', err);
    } finally {
      loadingMore = false;
    }
  }

  // Price band state
  let bandMetric = $state('winrate');
  let bandMinSamples = $state(5);
  let bandLoading = $state(false);
  /** @type {any} */ let bandChart = null;
  let bandReady = $state(false);
  /** @type {HTMLCanvasElement | null} */ let bandCanvas = $state(null);
  /** @type {Record<string, any>} */ let priceBandsData = $state({});

  /** @type {Record<string, string>} */
  const metricLabels = {
    winrate: 'Win Rate Score',
    pnl: 'Net P&L ($)',
    roi: 'ROI Score',
    sharpe: 'Sharpe Score',
  };

  // Mirror of strategies page color map so toggles stay consistent across pages.
  /** @type {Record<string, string>} */
  const strategyColors = {
    'matchpoint': '#60a5fa',
    'matchpoint-aggro': '#a78bfa',
    'setpoint': '#34d399',
    'setpoint-serve': '#fbbf24',
    'setpoint-cheap': '#f472b0',
    'fadelongshot': '#f87171',
  };

  function colorFor(/** @type {string} */ name) {
    return strategyColors[name] || vibrantColor(name);
  }

  let strategies = $derived.by(() => {
    if (!data || !data.orders) return [];
    return [...new Set(allOrders.map((/** @type {any} */ o) => o.strategy).filter(Boolean))].sort();
  });

  // Initialize selection once when strategies first appear.
  let strategiesInitialized = false;
  $effect(() => {
    if (!strategiesInitialized && strategies.length > 0) {
      strategiesInitialized = true;
      selectedStrategies = new Set(strategies);
    }
  });

  /** @type {any[]} */
  let filteredOrders = $derived.by(() => {
    if (!data || !data.orders) return [];
    return allOrders.filter((/** @type {any} */ o) => {
      if (selectedStrategies.size > 0 && !selectedStrategies.has(o.strategy)) return false;
      if (minPrice > 0 && o.market_price < minPrice) return false;
      if (maxPrice > 0 && o.market_price > maxPrice) return false;
      if (filterMatch && !o.match_ticker.toLowerCase().includes(filterMatch.toLowerCase())) return false;
      if (filterResult === 'won' && !o.won) return false;
      if (filterResult === 'lost' && o.won) return false;
      if (filterResult === 'pending' && o.result) return false;
      return true;
    }).sort((/** @type {any} */ a, /** @type {any} */ b) => (b.ts || 0) - (a.ts || 0));
  });

  let settledOrders = $derived(filteredOrders.filter((/** @type {any} */ o) => o.result));
  let pendingOrders = $derived(filteredOrders.filter((/** @type {any} */ o) => !o.result));

  let filteredSummary = $derived.by(() => {
    /** @type {{ total: number, resolved: number, wins: number, losses: number, pending: number, win_rate: number, invested: number, net_pnl: number, roi: number }} */
    const s = { total: 0, resolved: 0, wins: 0, losses: 0, pending: 0, win_rate: 0, invested: 0, net_pnl: 0, roi: 0 };
    for (const o of filteredOrders) {
      s.total++;
      s.invested += o.suggested_size * o.market_price;
      if (o.result) {
        s.resolved++;
        if (o.won) { s.wins++; s.net_pnl += o.suggested_size * (1.0 - o.market_price); }
        else { s.losses++; s.net_pnl += -o.suggested_size * o.market_price; }
      } else {
        s.pending++;
      }
    }
    if (s.resolved > 0) s.win_rate = (s.wins / s.resolved) * 100;
    if (s.invested > 0) s.roi = (s.net_pnl / s.invested) * 100;
    return s;
  });

  function toggleStrategy(/** @type {string} */ name) {
    const next = new Set(selectedStrategies);
    if (next.has(name)) next.delete(name);
    else next.add(name);
    selectedStrategies = next;
  }

  function toggleAllStrategies() {
    if (selectedStrategies.size === strategies.length) selectedStrategies = new Set();
    else selectedStrategies = new Set(strategies);
  }

  // Chart refs
  /** @type {HTMLCanvasElement | null} */ let pnlCanvas = $state(null);
  /** @type {HTMLCanvasElement | null} */ let stratPnlCanvas = $state(null);
  /** @type {HTMLCanvasElement | null} */ let winlossCanvas = $state(null);
  /** @type {HTMLCanvasElement | null} */ let priceDistCanvas = $state(null);
  /** @type {HTMLCanvasElement | null} */ let byDayCanvas = $state(null);
  /** @type {HTMLCanvasElement | null} */ let byHourCanvas = $state(null);
  /** @type {any} */ let pnlChart = null;
  /** @type {any} */ let stratPnlChart = null;
  /** @type {any} */ let winlossChart = null;
  /** @type {any} */ let priceDistChart = null;
  /** @type {any} */ let byDayChart = null;
  /** @type {any} */ let byHourChart = null;
  let pnlReady = $state(false);
  let stratPnlReady = $state(false);
  let winlossReady = $state(false);
  let priceDistReady = $state(false);
  let byDayReady = $state(false);
  let byHourReady = $state(false);

  // Bucket orders by calendar day (YYYY-MM-DD) and by hour-of-day (0-23, 24hr).
  // Count bars use all filtered orders; P&L line sums pnl of settled orders only.
  /** @returns {{labels: string[], counts: number[], pnl: number[]}} */
  function bucketByDay(/** @type {any[]} */ orders) {
    /** @type {Record<string, {count: number, pnl: number}>} */
    const m = {};
    for (const o of orders) {
      const d = new Date(o.ts);
      const key = `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
      if (!m[key]) m[key] = { count: 0, pnl: 0 };
      m[key].count++;
      if (o.result) m[key].pnl += o.pnl;
    }
    const labels = Object.keys(m).sort();
    return { labels, counts: labels.map((k) => m[k].count), pnl: labels.map((k) => Math.round(m[k].pnl * 100) / 100) };
  }

  /** @returns {{labels: string[], counts: number[], pnl: number[]}} */
  function bucketByHour(/** @type {any[]} */ orders) {
    /** @type {Record<number, {count: number, pnl: number}>} */
    const m = {};
    for (let h = 0; h < 24; h++) m[h] = { count: 0, pnl: 0 };
    for (const o of orders) {
      const h = new Date(o.ts).getHours();
      m[h].count++;
      if (o.result) m[h].pnl += o.pnl;
    }
    const labels = Array.from({ length: 24 }, (_, h) => String(h).padStart(2, '0'));
    return { labels, counts: labels.map((_, h) => m[h].count), pnl: labels.map((_, h) => Math.round(m[h].pnl * 100) / 100) };
  }

  $effect(() => {
    if (!browser || !pnlCanvas || settledOrders.length === 0) return;
    (async () => {
      pnlReady = false;
      const Chart = await setupChart();
      if (!Chart) return;
      if (pnlChart) pnlChart.destroy();

      const sorted = [...settledOrders].sort((a, b) => a.ts - b.ts);
      let cum = 0;
      const cumData = sorted.map((o) => { cum += o.pnl; return Math.round(cum * 100) / 100; });

      pnlChart = new Chart(pnlCanvas, {
        type: 'line',
        data: {
          labels: sorted.map((_, i) => i + 1),
          datasets: [{
            label: 'Cumulative P&L',
            data: cumData,
            borderColor: '#60a5fa',
            backgroundColor: '#60a5fa20',
            borderWidth: 2, pointRadius: 0, tension: 0.2, fill: true,
          }],
        },
        options: {
          responsive: true, maintainAspectRatio: false, animation: false,
          plugins: { legend: { labels: { color: '#94a3b8', font: { size: 11 } } } },
          scales: {
            x: { ticks: { color: '#64748b', font: { size: 10 } }, grid: { color: '#1e293b' }, title: { display: true, text: 'Order #', color: '#64748b' } },
            y: { ticks: { color: '#64748b', font: { size: 10 }, callback: (/** @type {number} */ v) => '$' + v }, grid: { color: '#1e293b' }, title: { display: true, text: 'P&L ($)', color: '#64748b' } },
          },
        },
      });
      pnlReady = true;
    })();
  });

  $effect(() => {
    if (!browser || !stratPnlCanvas || filteredOrders.length === 0) return;
    (async () => {
      stratPnlReady = false;
      const Chart = await setupChart();
      if (!Chart) return;
      if (stratPnlChart) stratPnlChart.destroy();

      /** @type {Record<string, number>} */
      const byStrat = {};
      for (const o of filteredOrders) {
        if (!o.result) continue;
        byStrat[o.strategy] = (byStrat[o.strategy] || 0) + o.pnl;
      }
      const labels = Object.keys(byStrat);
      const values = labels.map((k) => Math.round(byStrat[k] * 100) / 100);

      stratPnlChart = new Chart(stratPnlCanvas, {
        type: 'bar',
        data: {
          labels,
          datasets: [{
            label: 'Net P&L',
            data: values,
            backgroundColor: values.map((v) => v >= 0 ? '#34d39980' : '#f8717180'),
            borderColor: values.map((v) => v >= 0 ? '#34d399' : '#f87171'),
            borderWidth: 1,
          }],
        },
        options: {
          responsive: true, maintainAspectRatio: false, animation: false,
          plugins: { legend: { display: false } },
          scales: {
            x: { ticks: { color: '#64748b', font: { size: 10 } }, grid: { color: '#1e293b' } },
            y: { ticks: { color: '#64748b', font: { size: 10 }, callback: (/** @type {number} */ v) => '$' + v }, grid: { color: '#1e293b' } },
          },
        },
      });
      stratPnlReady = true;
    })();
  });

  $effect(() => {
    if (!browser || !winlossCanvas || filteredOrders.length === 0) return;
    (async () => {
      winlossReady = false;
      const Chart = await setupChart();
      if (!Chart) return;
      if (winlossChart) winlossChart.destroy();

      /** @type {Record<string, {wins: number, losses: number}>} */
      const byStrat = {};
      for (const o of filteredOrders) {
        if (!o.result) continue;
        if (!byStrat[o.strategy]) byStrat[o.strategy] = { wins: 0, losses: 0 };
        if (o.won) byStrat[o.strategy].wins++;
        else byStrat[o.strategy].losses++;
      }
      const labels = Object.keys(byStrat);

      winlossChart = new Chart(winlossCanvas, {
        type: 'bar',
        data: {
          labels,
          datasets: [
            { label: 'Wins', data: labels.map((k) => byStrat[k].wins), backgroundColor: '#34d399' },
            { label: 'Losses', data: labels.map((k) => byStrat[k].losses), backgroundColor: '#f87171' },
          ],
        },
        options: {
          responsive: true, maintainAspectRatio: false, animation: false,
          plugins: { legend: { labels: { color: '#94a3b8', font: { size: 11 } } } },
          scales: {
            x: { ticks: { color: '#64748b', font: { size: 10 } }, grid: { color: '#1e293b' } },
            y: { ticks: { color: '#64748b', font: { size: 10 } }, grid: { color: '#1e293b' }, beginAtZero: true },
          },
        },
      });
      winlossReady = true;
    })();
  });

  $effect(() => {
    if (!browser || !priceDistCanvas || filteredOrders.length === 0) return;
    (async () => {
      priceDistReady = false;
      const Chart = await setupChart();
      if (!Chart) return;
      if (priceDistChart) priceDistChart.destroy();

      const bins = new Array(10).fill(0);
      for (const o of filteredOrders) {
        const idx = Math.min(Math.floor(o.market_price * 10), 9);
        bins[idx]++;
      }

      priceDistChart = new Chart(priceDistCanvas, {
        type: 'bar',
        data: {
          labels: Array.from({ length: 10 }, (_, i) => `${i * 10}-${(i + 1) * 10}c`),
          datasets: [{
            label: 'Orders',
            data: bins,
            backgroundColor: '#60a5fa80',
            borderColor: '#60a5fa',
            borderWidth: 1,
          }],
        },
        options: {
          responsive: true, maintainAspectRatio: false, animation: false,
          plugins: { legend: { display: false } },
          scales: {
            x: { ticks: { color: '#64748b', font: { size: 10 } }, grid: { color: '#1e293b' }, title: { display: true, text: 'Entry Price', color: '#64748b' } },
            y: { ticks: { color: '#64748b', font: { size: 10 } }, grid: { color: '#1e293b' }, beginAtZero: true },
          },
        },
      });
      priceDistReady = true;
    })();
  });

  $effect(() => {
    if (!browser || !byDayCanvas || filteredOrders.length === 0) return;
    (async () => {
      byDayReady = false;
      const Chart = await setupChart();
      if (!Chart) return;
      if (byDayChart) byDayChart.destroy();

      const { labels, counts, pnl } = bucketByDay(filteredOrders);

      byDayChart = new Chart(byDayCanvas, {
        type: 'bar',
        data: {
          labels,
          datasets: [
            { type: 'bar', label: 'Orders', data: counts, backgroundColor: '#60a5fa80', borderColor: '#60a5fa', borderWidth: 1, yAxisID: 'y' },
            { type: 'line', label: 'Net P&L', data: pnl, borderColor: '#fbbf24', backgroundColor: '#fbbf2420', borderWidth: 2, pointRadius: 2, tension: 0.2, yAxisID: 'y1' },
          ],
        },
        options: {
          responsive: true, maintainAspectRatio: false, animation: false,
          plugins: { legend: { labels: { color: '#94a3b8', font: { size: 11 } } } },
          scales: {
            x: { ticks: { color: '#64748b', font: { size: 10 }, maxRotation: 45, minRotation: 45 }, grid: { color: '#1e293b' }, title: { display: true, text: 'Day', color: '#64748b' } },
            y: { type: 'linear', position: 'left', ticks: { color: '#64748b', font: { size: 10 } }, grid: { color: '#1e293b' }, title: { display: true, text: 'Orders', color: '#64748b' }, beginAtZero: true },
            y1: { type: 'linear', position: 'right', ticks: { color: '#64748b', font: { size: 10 }, callback: (/** @type {number} */ v) => '$' + v }, grid: { drawOnChartArea: false }, title: { display: true, text: 'Net P&L ($)', color: '#64748b' } },
          },
        },
      });
      byDayReady = true;
    })();
  });

  // --- Price band analysis (client-side, from settled orders) ---

  /** @type {Record<string, (orders: any[]) => number>} */
  const scoreFns = {
    winrate: (orders) => {
      if (orders.length === 0) return 0;
      const wins = orders.filter((o) => o.won).length;
      return (wins / orders.length) * Math.log(orders.length + 1);
    },
    pnl: (orders) => {
      if (orders.length === 0) return 0;
      const pnl = orders.reduce((s, o) => s + o.pnl, 0);
      return (pnl / orders.length) * Math.log(orders.length + 1);
    },
    roi: (orders) => {
      if (orders.length === 0) return 0;
      const invested = orders.reduce((s, o) => s + o.suggested_size * o.market_price, 0);
      const pnl = orders.reduce((s, o) => s + o.pnl, 0);
      if (invested <= 0) return 0;
      return (pnl / invested) * Math.log(orders.length + 1);
    },
    sharpe: (orders) => {
      if (orders.length < 2) return 0;
      const n = orders.length;
      const sum = orders.reduce((s, o) => s + o.pnl, 0);
      const mean = sum / n;
      const sumSq = orders.reduce((s, o) => s + o.pnl * o.pnl, 0);
      const variance = sumSq / n - mean * mean;
      if (variance <= 0) return 0;
      return (mean / Math.sqrt(variance)) * Math.sqrt(n);
    },
  };

  /** @param {any[]} orders @param {(orders: any[]) => number} scoreFn @param {boolean} isPeak */
  /** @returns {{min_price: number, max_price: number, signals: number, wins: number, win_rate: number, net_pnl: number, roi: number, avg_edge: number, score: number, is_peak: boolean}} */
  function makeBand(orders, scoreFn, isPeak) {
    const b = { min_price: 0, max_price: 0, signals: orders.length, wins: 0, win_rate: 0, net_pnl: 0, roi: 0, avg_edge: 0, score: 0, is_peak: isPeak };
    if (orders.length === 0) return b;
    b.min_price = orders[0].market_price;
    b.max_price = orders[orders.length - 1].market_price;
    let invested = 0, pnl = 0, edgeSum = 0;
    for (const o of orders) {
      if (o.won) b.wins++;
      pnl += o.pnl;
      invested += o.suggested_size * o.market_price;
      edgeSum += o.edge_cents;
    }
    b.net_pnl = Math.round(pnl * 100) / 100;
    b.win_rate = (b.wins / b.signals) * 100;
    if (invested > 0) b.roi = (pnl / invested) * 100;
    b.avg_edge = edgeSum / b.signals;
    b.score = scoreFn(orders);
    return b;
  }

  /** @param {any[]} orders @param {(orders: any[]) => number} scoreFn @param {number} minSamples @returns {any[]} */
  function partition(orders, scoreFn, minSamples) {
    if (orders.length < 2 * minSamples) return [makeBand(orders, scoreFn, false)];
    const currentScore = scoreFn(orders);
    let bestIdx = -1, bestImprovement = 0;
    const threshold = Math.max(Math.abs(currentScore) * 0.10, 1e-9);
    for (let i = minSamples; i <= orders.length - minSamples; i++) {
      if (orders[i].market_price === orders[i - 1].market_price) continue;
      const left = scoreFn(orders.slice(0, i));
      const right = scoreFn(orders.slice(i));
      const improvement = left + right - currentScore;
      if (improvement > bestImprovement) { bestImprovement = improvement; bestIdx = i; }
    }
    if (bestIdx < 0 || bestImprovement < threshold) return [makeBand(orders, scoreFn, false)];
    return [...partition(orders.slice(0, bestIdx), scoreFn, minSamples), ...partition(orders.slice(bestIdx), scoreFn, minSamples)];
  }

  /** @param {any[]} bands */
  function detectPeaks(bands) {
    if (bands.length < 3) return;
    const scores = [...bands].map((b) => b.score).sort((a, b) => a - b);
    const median = scores[Math.floor(scores.length / 2)];
    for (let i = 0; i < bands.length; i++) {
      if (bands[i].signals < 2) continue;
      if (bands[i].score <= median) continue;
      const left = i > 0;
      const right = i < bands.length - 1;
      const leftOK = !left || bands[i].score > bands[i - 1].score;
      const rightOK = !right || bands[i].score > bands[i + 1].score;
      if (leftOK && rightOK) bands[i].is_peak = true;
    }
  }

  /** @param {any[]} orders @param {string} metricName @param {number} minSamples @returns {{bands: any[], peaks: any[]}} */
  function computePriceBands(orders, metricName, minSamples) {
    if (minSamples < 2) minSamples = 2;
    const scoreFn = scoreFns[metricName] || scoreFns.winrate;
    if (orders.length < 2 * minSamples) {
      return { bands: [makeBand(orders, scoreFn, false)], peaks: [] };
    }
    const sorted = [...orders].sort((a, b) => a.market_price - b.market_price);
    const bands = partition(sorted, scoreFn, minSamples);
    detectPeaks(bands);
    const peaks = bands.filter((b) => b.is_peak).sort((a, b) => b.score - a.score);
    return { bands, peaks };
  }

  $effect(() => {
    if (!browser || settledOrders.length === 0) return;
    bandLoading = true;
    try {
      /** @type {Record<string, any[]>} */
      const grouped = {};
      for (const o of settledOrders) {
        if (!grouped[o.strategy]) grouped[o.strategy] = [];
        grouped[o.strategy].push(o);
      }
      /** @type {Record<string, any>} */
      const result = {};
      for (const [name, orders] of Object.entries(grouped)) {
        result[name] = computePriceBands(orders, bandMetric, bandMinSamples);
      }
      priceBandsData = result;
    } finally {
      bandLoading = false;
    }
  });

  $effect(() => {
    if (!browser || !bandCanvas || Object.keys(priceBandsData).length === 0) return;
    (async () => {
      bandReady = false;
      const Chart = await setupChart();
      if (!Chart) return;
      if (bandChart) { bandChart.destroy(); bandChart = null; }

      const selNames = Object.keys(priceBandsData);
      /** @type {any[]} */
      const allPeaks = [];

      const datasets = selNames.map((name) => {
        const r = priceBandsData[name];
        if (!r || !r.bands || r.bands.length === 0) return null;
        for (const p of r.peaks || []) {
          allPeaks.push({ min_price: p.min_price, max_price: p.max_price, strategy: name });
        }
        const points = [];
        for (const b of r.bands) {
          points.push({ x: b.min_price, y: b.score });
          points.push({ x: b.max_price, y: b.score });
        }
        return {
          label: name,
          data: points,
          borderColor: colorFor(name),
          backgroundColor: colorFor(name) + '20',
          borderWidth: 2, pointRadius: 0, stepped: 'after', tension: 0,
        };
      }).filter(Boolean);

      if (datasets.length === 0) return;

      const peakPlugin = {
        id: 'peakRects',
        beforeDatasetsDraw(/** @type {any} */ chart) {
          const { ctx, scales } = chart;
          if (!scales.x || !scales.y) return;
          for (const peak of chart.$peaks || []) {
            const x1 = scales.x.getPixelForValue(peak.min_price);
            const x2 = scales.x.getPixelForValue(peak.max_price);
            ctx.fillStyle = 'rgba(34, 197, 94, 0.35)';
            ctx.fillRect(x1, scales.y.top, x2 - x1, scales.y.bottom - scales.y.top);
            ctx.strokeStyle = 'rgba(34, 197, 94, 0.8)';
            ctx.lineWidth = 1.5;
            ctx.strokeRect(x1, scales.y.top, x2 - x1, scales.y.bottom - scales.y.top);
          }
        },
      };

      bandChart = new Chart(bandCanvas, {
        type: 'line',
        data: { datasets },
        options: {
          responsive: true, maintainAspectRatio: false, animation: false,
          plugins: {
            legend: { labels: { color: '#94a3b8', font: { size: 11 } } },
            tooltip: {
              callbacks: {
                title: (/** @type {any[]} */ items) => `Price: ${((items[0]?.parsed?.x ?? 0) * 100).toFixed(1)}c`,
                label: (/** @type {any} */ item) => `${item.dataset.label}: ${item.parsed.y.toFixed(3)}`,
              },
            },
          },
          scales: {
            x: { type: 'linear', min: 0, max: 1, ticks: { color: '#64748b', font: { size: 10 }, callback: (/** @type {number} */ v) => (v * 100).toFixed(0) + 'c' }, grid: { color: '#1e293b' }, title: { display: true, text: 'Entry Price', color: '#64748b' } },
            y: { ticks: { color: '#64748b', font: { size: 10 } }, grid: { color: '#1e293b' }, title: { display: true, text: metricLabels[bandMetric] || 'Score', color: '#64748b' } },
          },
        },
        plugins: [peakPlugin],
      });
      bandChart.$peaks = allPeaks;
      bandReady = true;
    })();
  });

  $effect(() => {
    if (!browser || !byHourCanvas || filteredOrders.length === 0) return;
    (async () => {
      byHourReady = false;
      const Chart = await setupChart();
      if (!Chart) return;
      if (byHourChart) byHourChart.destroy();

      const { labels, counts, pnl } = bucketByHour(filteredOrders);

      byHourChart = new Chart(byHourCanvas, {
        type: 'bar',
        data: {
          labels,
          datasets: [
            { type: 'bar', label: 'Orders', data: counts, backgroundColor: '#a78bfa80', borderColor: '#a78bfa', borderWidth: 1, yAxisID: 'y' },
            { type: 'line', label: 'Net P&L', data: pnl, borderColor: '#fbbf24', backgroundColor: '#fbbf2420', borderWidth: 2, pointRadius: 2, tension: 0.2, yAxisID: 'y1' },
          ],
        },
        options: {
          responsive: true, maintainAspectRatio: false, animation: false,
          plugins: { legend: { labels: { color: '#94a3b8', font: { size: 11 } } } },
          scales: {
            x: { ticks: { color: '#64748b', font: { size: 10 } }, grid: { color: '#1e293b' }, title: { display: true, text: 'Hour (24hr)', color: '#64748b' } },
            y: { type: 'linear', position: 'left', ticks: { color: '#64748b', font: { size: 10 } }, grid: { color: '#1e293b' }, title: { display: true, text: 'Orders', color: '#64748b' }, beginAtZero: true },
            y1: { type: 'linear', position: 'right', ticks: { color: '#64748b', font: { size: 10 }, callback: (/** @type {number} */ v) => '$' + v }, grid: { drawOnChartArea: false }, title: { display: true, text: 'Net P&L ($)', color: '#64748b' } },
          },
        },
      });
      byHourReady = true;
    })();
  });
</script>

<svelte:head>
  <title>Paper Orders — Ghost Trader</title>
</svelte:head>

<div class="page-container wide">
  <PageHeader title="Paper Orders" connected={$store.connected} error={$store.error || ''} />

  {#if data}
    <div class="summary-bar">
      <div class="summary-stat">
        <span class="label">Total</span>
        <span class="value">{filteredSummary.total}</span>
      </div>
      <div class="summary-stat">
        <span class="label">Resolved</span>
        <span class="value">{filteredSummary.resolved}</span>
      </div>
      <div class="summary-stat">
        <span class="label">Wins</span>
        <span class="value value-win">{filteredSummary.wins}</span>
      </div>
      <div class="summary-stat">
        <span class="label">Losses</span>
        <span class="value value-loss">{filteredSummary.losses}</span>
      </div>
      <div class="summary-stat">
        <span class="label">Pending</span>
        <span class="value">{filteredSummary.pending}</span>
      </div>
      <div class="summary-stat">
        <span class="label">Win Rate</span>
        <span class="value">{filteredSummary.win_rate.toFixed(1)}%</span>
      </div>
      <div class="summary-stat">
        <span class="label">Invested</span>
        <span class="value">${filteredSummary.invested.toFixed(2)}</span>
      </div>
      <div class="summary-stat">
        <span class="label">Net P&L</span>
        <span class="value {filteredSummary.net_pnl >= 0 ? 'value-win' : 'value-loss'}">
          {fmtPnL(filteredSummary.net_pnl)}
        </span>
      </div>
      <div class="summary-stat">
        <span class="label">ROI</span>
        <span class="value {filteredSummary.roi >= 0 ? 'value-win' : 'value-loss'}">
          {fmtPct(filteredSummary.roi)}
        </span>
      </div>
    </div>
  {/if}

  {#if loading}
    <EmptyState text="Loading paper orders..." />
  {:else if $store.error}
    <EmptyState text={$store.error} variant="error" />
  {:else if !data || data.orders.length === 0}
    <EmptyState text="No paper orders yet." />
  {:else}
    <div class="layout">
      <div class="main-content">
        <div class="filter-count">
          {filteredOrders.length} shown ({settledOrders.length} settled, {pendingOrders.length} pending)
          — {loadedCount} of {totalOrders} loaded
          {#if totalOrders > loadedCount}<span class="filter-count-note"> (charts reflect loaded subset)</span>{/if}
        </div>

        {#if filteredOrders.length > 0}
          <CollapsibleSection title="Analysis" count={filteredOrders.length}>
            <div class="chart-grid">
              <div class="chart-card">
                <h3>Cumulative P&L</h3>
                <div class="chart-container" style="position: relative;"><canvas bind:this={pnlCanvas}></canvas>{#if !pnlReady}<ChartLoading />{/if}</div>
              </div>
              <div class="chart-card">
                <h3>P&L by Strategy</h3>
                <div class="chart-container" style="position: relative;"><canvas bind:this={stratPnlCanvas}></canvas>{#if !stratPnlReady}<ChartLoading />{/if}</div>
              </div>
              <div class="chart-card">
                <h3>Win / Loss by Strategy</h3>
                <div class="chart-container" style="position: relative;"><canvas bind:this={winlossCanvas}></canvas>{#if !winlossReady}<ChartLoading />{/if}</div>
              </div>
              <div class="chart-card">
                <h3>Entry Price Distribution</h3>
                <div class="chart-container" style="position: relative;"><canvas bind:this={priceDistCanvas}></canvas>{#if !priceDistReady}<ChartLoading />{/if}</div>
              </div>
              <div class="chart-card">
                <h3>Orders by Day</h3>
                <div class="chart-container" style="position: relative;"><canvas bind:this={byDayCanvas}></canvas>{#if !byDayReady}<ChartLoading />{/if}</div>
              </div>
              <div class="chart-card">
                <h3>Orders by Hour (24hr)</h3>
                <div class="chart-container" style="position: relative;"><canvas bind:this={byHourCanvas}></canvas>{#if !byHourReady}<ChartLoading />{/if}</div>
              </div>
            </div>
          </CollapsibleSection>
        {/if}

        {#if settledOrders.length > 0}
          <CollapsibleSection title="Price Band Performance" count={settledOrders.length}>
            <h3 style="font-size: 13px; font-weight: 600; color: var(--text-bright); margin: 0 0 10px;">
              Price Band Performance <span style="font-weight: 400; color: var(--text-muted);">— {metricLabels[bandMetric] || bandMetric}</span>
            </h3>
            <div style="height: 300px; width: 100%; position: relative;"><canvas bind:this={bandCanvas}></canvas>{#if !bandReady && !bandLoading}<ChartLoading />{/if}{#if bandLoading}<ChartLoading />{/if}</div>
            {#if Object.keys(priceBandsData).length > 0}
              <div class="peak-cards">
                {#each Object.keys(priceBandsData) as name}
                  {@const r = priceBandsData[name]}
                  {#if r && r.peaks && r.peaks.length > 0}
                    <div class="peak-card">
                      <div class="peak-card-header">
                        <span class="dot" style="background: {colorFor(name)}"></span>
                        {name}
                        <span class="peak-count">{r.peaks.length} peak{r.peaks.length > 1 ? 's' : ''}</span>
                      </div>
                      {#each r.peaks as p}
                        <div class="peak-row">
                          <span class="peak-range">{(p.min_price * 100).toFixed(1)}c–{(p.max_price * 100).toFixed(1)}c</span>
                          <span class="peak-stat">{p.win_rate.toFixed(1)}% WR</span>
                          <span class="peak-stat">{p.signals} sig</span>
                          <span class="peak-stat" class:positive={p.net_pnl > 0} class:negative={p.net_pnl < 0}>${p.net_pnl.toFixed(2)}</span>
                          <span class="peak-stat">score {p.score.toFixed(3)}</span>
                        </div>
                      {/each}
                    </div>
                  {/if}
                {/each}
              </div>
            {/if}
          </CollapsibleSection>
        {/if}

        {#if settledOrders.length > 0}
          <StatAnalysis orders={settledOrders} title="Statistical Analysis" count={settledOrders.length} />
        {/if}

        {#if pendingOrders.length > 0}
          <CollapsibleSection title="Open Positions" count={pendingOrders.length}>
            <div class="table-wrap">
              <table class="data-table">
                <thead>
                  <tr>
                    <th>Time</th>
                    <th>Match</th>
                    <th>Series</th>
                    <th>Player</th>
                    <th>Context</th>
                    <th>Strategy</th>
                    <th>Price</th>
                    <th>Edge</th>
                    <th>Size</th>
                    <th>Status</th>
                  </tr>
                </thead>
                <tbody>
                  {#each pendingOrders as o}
                    <tr class="row-pending clickable" onclick={() => goto(`/matches/${o.match_ticker}`)}>
                      <td class="mono">{fmtTime(o.ts)}</td>
                      <td>{fmtTicker(o.match_ticker)}</td>
                      <td class="series">{seriesFromTicker(o.match_ticker)}</td>
                      <td>{o.player_name || o.market_ticker}</td>
                      <td>{o.context}</td>
                      <td>{o.strategy}</td>
                      <td>{(o.market_price * 100).toFixed(0)}c</td>
                      <td>{o.edge_cents}c</td>
                      <td>{o.suggested_size}</td>
                      <td><Badge variant="pending" text="PENDING" /></td>
                    </tr>
                  {/each}
                </tbody>
              </table>
            </div>
          </CollapsibleSection>
        {/if}

        {#if settledOrders.length > 0}
          <CollapsibleSection title="Settled Trades" count={settledOrders.length}>
            <div class="table-wrap">
              <table class="data-table">
                <thead>
                  <tr>
                    <th>Time</th>
                    <th>Match</th>
                    <th>Series</th>
                    <th>Player</th>
                    <th>Context</th>
                    <th>Strategy</th>
                    <th>Price</th>
                    <th>Edge</th>
                    <th>Size</th>
                    <th>Result</th>
                    <th>P&L</th>
                  </tr>
                </thead>
                <tbody>
                  {#each settledOrders as o}
                    <tr class={`${o.won ? 'row-win' : 'row-loss'} clickable`} onclick={() => goto(`/matches/${o.match_ticker}`)}>
                      <td class="mono">{fmtTime(o.ts)}</td>
                      <td>{fmtTicker(o.match_ticker)}</td>
                      <td class="series">{seriesFromTicker(o.match_ticker)}</td>
                      <td>{o.player_name || o.market_ticker}</td>
                      <td>{o.context}</td>
                      <td>{o.strategy}</td>
                      <td>{(o.market_price * 100).toFixed(0)}c</td>
                      <td>{o.edge_cents}c</td>
                      <td>{o.suggested_size}</td>
                      <td>
                        <Badge variant={o.won ? 'ok' : 'err'} text={o.won ? 'WON' : 'LOST'} />
                      </td>
                      <td class={o.pnl >= 0 ? 'pnl-win' : 'pnl-loss'}>
                        {fmtPnL(o.pnl)}
                      </td>
                    </tr>
                  {/each}
                </tbody>
              </table>
            </div>
          </CollapsibleSection>
        {/if}

        {#if pendingOrders.length === 0 && settledOrders.length === 0}
          <EmptyState text="No orders match current filters." />
        {/if}

        {#if olderHasMore}
          <div class="load-more-row">
            <button class="load-more-btn" onclick={loadMore} disabled={loadingMore}>
              {loadingMore ? 'Loading…' : 'Load more'}
            </button>
            <span class="load-more-count">
              {loadedCount} of {totalOrders} loaded
            </span>
          </div>
        {:else if totalOrders > 0}
          <div class="load-more-row">
            <span class="load-more-count">
              {loadedCount} of {totalOrders} loaded{olderCursor ? '' : ' — end of feed'}
            </span>
          </div>
        {/if}
      </div>

      <aside class="filter-sidebar">
        <div class="filter-group">
          <h3>Strategies</h3>
          <button class="toggle-all" onclick={toggleAllStrategies}>
            {selectedStrategies.size === strategies.length ? 'Deselect All' : 'Select All'}
          </button>
          <div class="strategy-list">
            {#each strategies as name}
              <button
                class="toggle-btn"
                class:active={selectedStrategies.has(name)}
                style="--btn-color: {colorFor(name)}"
                onclick={() => toggleStrategy(name)}
              >
                <span class="dot" style="background: {colorFor(name)}"></span>
                {name}
              </button>
            {/each}
          </div>
        </div>

        <div class="filter-group">
          <h3>Price Band Analysis</h3>
          <label class="filter-label">Score Metric
            <select bind:value={bandMetric}>
              <option value="winrate">Win Rate</option>
              <option value="pnl">Net P&L</option>
              <option value="roi">ROI</option>
              <option value="sharpe">Sharpe</option>
            </select>
          </label>
          <label class="filter-label">Min Samples
            <input type="number" bind:value={bandMinSamples} min="2" max="50" step="1" />
          </label>
        </div>

        <div class="filter-group">
          <h3>Filters</h3>
          <label class="filter-label">Min Price
            <input type="number" bind:value={minPrice} min="0" max="1" step="0.05" placeholder="0 (off)" />
          </label>
          <label class="filter-label">Max Price
            <input type="number" bind:value={maxPrice} min="0" max="1" step="0.05" placeholder="0 (off)" />
          </label>
          <label class="filter-label">Match
            <input type="text" placeholder="Search match..." bind:value={filterMatch} />
          </label>
          <label class="filter-label">Result
            <select bind:value={filterResult}>
              <option value="">All Results</option>
              <option value="won">Won</option>
              <option value="lost">Lost</option>
              <option value="pending">Pending</option>
            </select>
          </label>
        </div>
      </aside>
    </div>
  {/if}
</div>

<style>
  .layout { display: flex; gap: 20px; align-items: flex-start; }
  .main-content { flex: 1; min-width: 0; }
  .filter-sidebar { width: 240px; flex-shrink: 0; position: sticky; top: 16px; background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius); padding: 16px; max-height: calc(100vh - 32px); overflow-y: auto; }
  .filter-group { margin-bottom: 20px; }
  .filter-group:last-child { margin-bottom: 0; }
  .filter-group h3 { font-size: 12px; font-weight: 600; color: var(--text-muted); text-transform: uppercase; letter-spacing: 0.5px; margin: 0 0 10px 0; }
  .strategy-list { display: flex; flex-direction: column; gap: 6px; margin-bottom: 8px; }
  .toggle-all { background: var(--surface-hover); border: 1px solid var(--border-strong); color: #94a3b8; padding: 6px 12px; border-radius: var(--radius-sm); font-size: 12px; cursor: pointer; margin-bottom: 8px; width: 100%; text-align: left; }
  .toggle-all:hover { background: var(--border-strong); }
  .toggle-btn { background: var(--surface-hover); border: 1px solid var(--border-strong); color: var(--text-muted); padding: 6px 10px; border-radius: var(--radius-sm); font-size: 12px; cursor: pointer; display: flex; align-items: center; gap: 6px; transition: all 0.15s; text-align: left; }
  .toggle-btn.active { border-color: var(--btn-color); color: var(--text); }
  .toggle-btn:hover { border-color: var(--btn-color); }
  .filter-label { display: flex; flex-direction: column; gap: 4px; font-size: 12px; color: var(--text-muted); margin-bottom: 10px; }
  .filter-label input, .filter-label select { background: var(--surface-hover); border: 1px solid var(--border-strong); color: var(--text); padding: 5px 10px; border-radius: var(--radius-xs); font-size: 13px; }
  .filter-label input { width: 100%; box-sizing: border-box; }
  .filter-count { font-size: 12px; color: var(--text-muted); margin-bottom: 16px; }
  .load-more-row { display: flex; align-items: center; gap: 12px; justify-content: center; padding: 16px 0; border-top: 1px solid var(--border); margin-top: 16px; }
  .load-more-btn { background: var(--surface-hover); border: 1px solid var(--border-strong); color: var(--text); padding: 8px 20px; border-radius: var(--radius-sm); font-size: 13px; cursor: pointer; }
  .load-more-btn:hover:not(:disabled) { background: var(--border-strong); }
  .load-more-btn:disabled { opacity: 0.5; cursor: not-allowed; }
  .load-more-count { font-size: 12px; color: var(--text-muted); }
  .filter-count-note { color: var(--text-muted); font-style: italic; }
  .chart-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(380px, 1fr)); gap: 16px; }
  .chart-card { background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius); padding: 14px; }
  .chart-card h3 { font-size: 13px; font-weight: 600; color: var(--text-bright); margin: 0 0 10px; }
  .chart-container { height: 240px; position: relative; }
  .clickable { cursor: pointer; }
  .clickable:hover { background: var(--surface-hover); }
  .dot { width: 8px; height: 8px; border-radius: 50%; flex-shrink: 0; }
  .peak-cards { display: flex; flex-direction: column; gap: 12px; margin-top: 16px; }
  .peak-card { background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius); padding: 12px; }
  .peak-card-header { display: flex; align-items: center; gap: 8px; font-size: 13px; font-weight: 600; color: var(--text-bright); margin-bottom: 8px; }
  .peak-count { font-size: 11px; color: var(--text-muted); font-weight: 400; }
  .peak-row { display: flex; align-items: center; gap: 12px; padding: 4px 0; font-size: 12px; color: var(--text-muted); }
  .peak-range { font-family: monospace; color: var(--text); min-width: 90px; }
  .peak-stat { min-width: 60px; }
  .peak-stat.positive { color: #34d399; }
  .peak-stat.negative { color: #f87171; }
</style>
