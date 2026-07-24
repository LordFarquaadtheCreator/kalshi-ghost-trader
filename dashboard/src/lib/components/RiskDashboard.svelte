<script>
  import MetricsBar from '$lib/components/MetricsBar.svelte';

  let { pendingOrders = [], liquidityPool = null, settledOrders = [], appConfig = null } = $props();

  // Handle both paper (lowercase) and real (capitalized) field names
  function getPrice(/** @type {any} */ o) { return o.market_price ?? o.MarketPrice ?? 0; }
  function getSize(/** @type {any} */ o) { return o.suggested_size ?? o.SuggestedSize ?? 0; }
  function getResult(/** @type {any} */ o) { return o.result ?? o.Result ?? ''; }
  function getTS(/** @type {any} */ o) { return o.settled_ts ?? o.SettledTS ?? o.ts ?? o.TS ?? 0; }

  let openExposure = $derived(pendingOrders.reduce((s, o) => {
    return s + getSize(o) * getPrice(o);
  }, 0));

  let maxPossibleLoss = $derived(openExposure);

  let poolBalance = $derived(liquidityPool ? liquidityPool.balance_cents / 100 : 0);
  let poolInitial = $derived(liquidityPool ? liquidityPool.initial_balance_cents / 100 : 0);
  let poolPnL = $derived(liquidityPool ? liquidityPool.total_pnl_cents / 100 : 0);
  let poolSpent = $derived(liquidityPool ? liquidityPool.total_spent_cents / 100 : 0);

  let exposureRatio = $derived(poolBalance > 0 ? (openExposure / poolBalance) * 100 : 0);

  // Burn rate: average daily loss from settled orders
  let dailyBurn = $derived.by(() => {
    if (settledOrders.length === 0) return 0;
    const losses = settledOrders.filter((o) => {
      const r = getResult(o);
      const pnl = r === 'yes' ? getSize(o) * (1 - getPrice(o)) : -getSize(o) * getPrice(o);
      return pnl < 0;
    });
    const totalLoss = losses.reduce((s, o) => s + getSize(o) * getPrice(o), 0);
    if (settledOrders.length < 2) return 0;
    const tsRange = settledOrders.map(getTS).filter(Boolean);
    if (tsRange.length < 2) return 0;
    const days = (Math.max(...tsRange) - Math.min(...tsRange)) / 86400000;
    if (days < 1) return 0;
    return totalLoss / days;
  });

  let runwayDays = $derived(dailyBurn > 0 ? poolBalance / dailyBurn : null);

  let kellyFraction = $derived(appConfig?.kelly_fraction ?? 0.25);

  let kellyStat = $derived.by(() => {
    const resolved = settledOrders.filter((o) => getResult(o));
    if (resolved.length < 5) return null;
    const wins = resolved.filter((o) => getResult(o) === 'yes').length;
    const p = wins / resolved.length;
    const q = 1 - p;
    const avgPrice = resolved.reduce((s, o) => s + getPrice(o), 0) / resolved.length;
    const b = avgPrice > 0 && avgPrice < 1 ? (1 - avgPrice) / avgPrice : 0;
    const kelly = b > 0 ? (b * p - q) / b : 0;
    const fractional = kelly * kellyFraction;
    return { kelly, fractional, winRate: p * 100, avgPrice };
  });
</script>

{#if liquidityPool}
  <MetricsBar
    primary={[
      { label: 'Pool Balance', value: '$' + poolBalance.toFixed(2) },
      { label: 'Open Exposure', value: '$' + openExposure.toFixed(2), tone: exposureRatio > 50 ? 'loss' : null },
      { label: 'Exposure %', value: exposureRatio.toFixed(1) + '%', tone: exposureRatio > 50 ? 'loss' : null },
      { label: 'Max Loss', value: '$' + maxPossibleLoss.toFixed(2), tone: 'loss' },
      { label: 'Pool P&L', value: (poolPnL >= 0 ? '+$' : '-$') + Math.abs(poolPnL).toFixed(2), tone: poolPnL > 0 ? 'win' : poolPnL < 0 ? 'loss' : null },
      { label: 'Spent', value: '$' + poolSpent.toFixed(2) },
    ]}
    secondary={[
      { label: 'Initial', value: '$' + poolInitial.toFixed(2) },
      { label: 'Daily Burn', value: '$' + dailyBurn.toFixed(2) + '/d', tone: dailyBurn > 0 ? 'loss' : null },
      ...(runwayDays !== null ? [{ label: 'Runway', value: runwayDays.toFixed(0) + 'd', tone: runwayDays < 7 ? 'loss' : null }] : []),
      ...(kellyStat ? [
        { label: 'Kelly %', value: (kellyStat.kelly * 100).toFixed(1) + '%', tone: kellyStat.kelly < 0 ? 'loss' : null },
        { label: 'Fract Kelly', value: (kellyStat.fractional * 100).toFixed(1) + '%' },
        { label: 'Win Rate', value: kellyStat.winRate.toFixed(1) + '%' },
        { label: 'Avg Price', value: (kellyStat.avgPrice * 100).toFixed(0) + 'c' },
      ] : []),
    ]}
    note={exposureRatio > 50 ? '⚠ high exposure' : ''}
  />
{/if}
