<script>
  import CollapsibleSection from './CollapsibleSection.svelte';
  import { computeStats } from '$lib/stats.js';

  let { orders, title = 'Statistical Analysis', count = null } = $props();

  let stats = $derived(computeStats(orders || []));

  // Group stats into sections for display
  let pnlStats = $derived.by(() => {
    if (!stats.n) return [];
    return [
      { label: 'Mean P&L', value: stats.meanPnl, fmt: 'currency' },
      { label: 'Median P&L', value: stats.medianPnl, fmt: 'currency' },
      { label: 'Std Dev', value: stats.stdDev, fmt: 'currency' },
      { label: 'Variance', value: stats.variance, fmt: 'currency2' },
      { label: 'Skewness', value: stats.skewness, fmt: 'decimal3' },
      { label: 'Kurtosis (excess)', value: stats.kurtosis, fmt: 'decimal3' },
      { label: 'Min', value: stats.minPnl, fmt: 'currency' },
      { label: 'Max', value: stats.maxPnl, fmt: 'currency' },
      { label: 'Range', value: stats.range, fmt: 'currency' },
      { label: 'Q1 (25th)', value: stats.q1, fmt: 'currency' },
      { label: 'Q3 (75th)', value: stats.q3, fmt: 'currency' },
      { label: 'IQR', value: stats.iqr, fmt: 'currency' },
      { label: 'CV (coef var)', value: stats.cv, fmt: 'decimal3' },
      { label: '95% CI half-width', value: stats.ci95, fmt: 'currency' },
      { label: 'Z-score (vs 0)', value: stats.zScore, fmt: 'decimal3' },
    ];
  });

  let riskStats = $derived.by(() => {
    if (!stats.n) return [];
    return [
      { label: 'Sharpe (per-trade)', value: stats.sharpe, fmt: 'decimal4' },
      { label: 'Sortino (per-trade)', value: stats.sortino, fmt: 'decimal4' },
      { label: 'Profit Factor', value: stats.profitFactor, fmt: 'decimal3' },
      { label: 'Kelly Fraction', value: stats.kelly, fmt: 'percent' },
      { label: 'Win Rate', value: stats.winRate, fmt: 'percent' },
      { label: 'Expected Value', value: stats.meanPnl, fmt: 'currency' },
    ];
  });

  let priceStats = $derived.by(() => {
    if (!stats.n) return [];
    return [
      { label: 'Mean Price', value: stats.meanPrice, fmt: 'price' },
      { label: 'Median Price', value: stats.medianPrice, fmt: 'price' },
      { label: 'Std Dev Price', value: stats.stdDevPrice, fmt: 'price' },
      { label: 'Skewness Price', value: stats.skewPrice, fmt: 'decimal3' },
      { label: 'Kurtosis Price', value: stats.kurtPrice, fmt: 'decimal3' },
    ];
  });

  let edgeStats = $derived.by(() => {
    if (!stats.n) return [];
    return [
      { label: 'Mean Edge', value: stats.meanEdge, fmt: 'cents' },
      { label: 'Median Edge', value: stats.medianEdge, fmt: 'cents' },
      { label: 'Std Dev Edge', value: stats.stdDevEdge, fmt: 'cents' },
      { label: 'Skewness Edge', value: stats.skewEdge, fmt: 'decimal3' },
      { label: 'Kurtosis Edge', value: stats.kurtEdge, fmt: 'decimal3' },
    ];
  });

  function fmt(/** @type {number} */ v, /** @type {string} */ format) {
    if (v === Infinity) return '∞';
    if (isNaN(v)) return '—';
    switch (format) {
      case 'currency': return (v >= 0 ? '+' : '') + '$' + v.toFixed(2);
      case 'currency2': return '$' + v.toFixed(3);
      case 'decimal3': return v.toFixed(3);
      case 'decimal4': return v.toFixed(4);
      case 'percent': return (v * 100).toFixed(2) + '%';
      case 'price': return v.toFixed(3);
      case 'cents': return v.toFixed(1) + 'c';
      default: return String(v);
    }
  }
</script>

{#if stats.n}
  <CollapsibleSection {title} count={count ?? stats.n} defaultOpen={false}>
    <div class="stat-analysis">
      <div class="stat-group">
        <h4>P&L Distribution</h4>
        <div class="stat-grid">
          {#each pnlStats as s}
            <div class="stat-item">
              <span class="stat-key">{s.label}</span>
              <span class="stat-val" class:pos={s.value > 0} class:neg={s.value < 0}>{fmt(s.value, s.fmt)}</span>
            </div>
          {/each}
        </div>
      </div>

      <div class="stat-group">
        <h4>Risk Metrics</h4>
        <div class="stat-grid">
          {#each riskStats as s}
            <div class="stat-item">
              <span class="stat-key">{s.label}</span>
              <span class="stat-val" class:pos={s.value > 0} class:neg={s.value < 0}>{fmt(s.value, s.fmt)}</span>
            </div>
          {/each}
        </div>
      </div>

      <div class="stat-group">
        <h4>Entry Price Distribution</h4>
        <div class="stat-grid">
          {#each priceStats as s}
            <div class="stat-item">
              <span class="stat-key">{s.label}</span>
              <span class="stat-val">{fmt(s.value, s.fmt)}</span>
            </div>
          {/each}
        </div>
      </div>

      <div class="stat-group">
        <h4>Edge Distribution</h4>
        <div class="stat-grid">
          {#each edgeStats as s}
            <div class="stat-item">
              <span class="stat-key">{s.label}</span>
              <span class="stat-val">{fmt(s.value, s.fmt)}</span>
            </div>
          {/each}
        </div>
      </div>
    </div>
  </CollapsibleSection>
{/if}

<style>
  .stat-analysis { display: grid; grid-template-columns: repeat(auto-fit, minmax(280px, 1fr)); gap: 16px; }
  .stat-group { background: var(--surface-hover); border: 1px solid var(--border); border-radius: var(--radius-sm); padding: 12px; }
  .stat-group h4 { font-size: 11px; font-weight: 600; color: var(--text-muted); text-transform: uppercase; letter-spacing: 0.5px; margin: 0 0 10px 0; }
  .stat-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 4px 12px; }
  .stat-item { display: flex; justify-content: space-between; align-items: baseline; font-size: 12px; }
  .stat-key { color: var(--text-muted); }
  .stat-val { font-weight: 600; color: var(--text-bright); font-variant-numeric: tabular-nums; }
  .stat-val.pos { color: var(--win); }
  .stat-val.neg { color: var(--loss); }
</style>
