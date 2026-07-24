// Statistical analysis helpers for order distributions.
// All functions are pure — take arrays, return numbers.

/**
 * @param {number[]} xs
 * @returns {number}
 */
export function mean(xs) {
  if (xs.length === 0) return 0;
  return xs.reduce((a, b) => a + b, 0) / xs.length;
}

/**
 * @param {number[]} xs
 * @returns {number}
 */
export function median(xs) {
  if (xs.length === 0) return 0;
  const s = [...xs].sort((a, b) => a - b);
  const mid = Math.floor(s.length / 2);
  return s.length % 2 === 0 ? (s[mid - 1] + s[mid]) / 2 : s[mid];
}

/**
 * @param {number[]} xs
 * @returns {number}
 */
export function variance(xs) {
  if (xs.length < 2) return 0;
  const m = mean(xs);
  return xs.reduce((s, x) => s + (x - m) ** 2, 0) / (xs.length - 1);
}

/**
 * @param {number[]} xs
 * @returns {number}
 */
export function stdDev(xs) {
  return Math.sqrt(variance(xs));
}

/**
 * @param {number[]} xs
 * @returns {number}
 */
export function skewness(xs) {
  if (xs.length < 3) return 0;
  const m = mean(xs);
  const sd = stdDev(xs);
  if (sd === 0) return 0;
  const n = xs.length;
  const sum = xs.reduce((s, x) => s + ((x - m) / sd) ** 3, 0);
  return (n / ((n - 1) * (n - 2))) * sum;
}

/**
 * @param {number[]} xs
 * @returns {number}
 */
export function kurtosis(xs) {
  if (xs.length < 4) return 0;
  const m = mean(xs);
  const sd = stdDev(xs);
  if (sd === 0) return 0;
  const n = xs.length;
  const s2 = xs.reduce((s, x) => s + ((x - m) / sd) ** 2, 0);
  const s4 = xs.reduce((s, x) => s + ((x - m) / sd) ** 4, 0);
  const k = (n * (n + 1) * s4 - 3 * (n - 1) ** 2 * s2) / ((n - 1) * (n - 2) * (n - 3));
  return k - 3; // excess kurtosis
}

/**
 * @param {number[]} xs
 * @param {number} p — percentile 0-100
 * @returns {number}
 */
export function percentile(xs, p) {
  if (xs.length === 0) return 0;
  const s = [...xs].sort((a, b) => a - b);
  const idx = (p / 100) * (s.length - 1);
  const lo = Math.floor(idx);
  const hi = Math.ceil(idx);
  if (lo === hi) return s[lo];
  return s[lo] + (s[hi] - s[lo]) * (idx - lo);
}

/**
 * @param {number[]} xs
 * @returns {number}
 */
export function iqr(xs) {
  return percentile(xs, 75) - percentile(xs, 25);
}

/**
 * @param {number[]} xs
 * @returns {number}
 */
export function min(xs) {
  return xs.length === 0 ? 0 : Math.min(...xs);
}

/**
 * @param {number[]} xs
 * @returns {number}
 */
export function max(xs) {
  return xs.length === 0 ? 0 : Math.max(...xs);
}

/**
 * @param {number[]} xs
 * @returns {number}
 */
export function range(xs) {
  return max(xs) - min(xs);
}

/**
 * Coefficient of variation — stdDev / mean.
 * @param {number[]} xs
 * @returns {number}
 */
export function cv(xs) {
  const m = mean(xs);
  if (m === 0) return 0;
  return stdDev(xs) / Math.abs(m);
}

/**
 * 95% confidence interval half-width for the mean.
 * Uses t≈2 (normal approx) for large n.
 * @param {number[]} xs
 * @returns {number}
 */
export function ci95(xs) {
  if (xs.length < 2) return 0;
  return 1.96 * stdDev(xs) / Math.sqrt(xs.length);
}

/**
 * Z-score of the mean vs zero — tests if mean is significantly different from 0.
 * @param {number[]} xs
 * @returns {number}
 */
export function zScore(xs) {
  const sd = stdDev(xs);
  if (sd === 0 || xs.length === 0) return 0;
  return mean(xs) / (sd / Math.sqrt(xs.length));
}

/**
 * Expected value per trade = mean pnl.
 * @param {number[]} pnls
 * @returns {number}
 */
export function expectedValue(pnls) {
  return mean(pnls);
}

/**
 * Kelly fraction from win rate and payoff ratio.
 * f* = (b*p - q) / b where p=win rate, q=1-p, b=avg win/avg loss.
 * @param {number} winRate — 0-1
 * @param {number[]} pnls — settled pnl array
 * @returns {number}
 */
export function kellyFraction(winRate, pnls) {
  const wins = pnls.filter((p) => p > 0);
  const losses = pnls.filter((p) => p < 0);
  if (wins.length === 0 || losses.length === 0) return 0;
  const avgWin = mean(wins);
  const avgLoss = Math.abs(mean(losses));
  if (avgLoss === 0) return 0;
  const b = avgWin / avgLoss;
  const p = winRate;
  const q = 1 - p;
  return (b * p - q) / b;
}

/**
 * Sortino ratio — mean / downside deviation.
 * @param {number[]} pnls
 * @returns {number}
 */
export function sortino(pnls) {
  if (pnls.length === 0) return 0;
  const m = mean(pnls);
  const downside = pnls.filter((p) => p < 0);
  if (downside.length === 0) return m > 0 ? Infinity : 0;
  const dd = Math.sqrt(downside.reduce((s, p) => s + p * p, 0) / pnls.length);
  if (dd === 0) return 0;
  return m / dd;
}

/**
 * Profit factor — gross win / gross loss.
 * @param {number[]} pnls
 * @returns {number}
 */
export function profitFactor(pnls) {
  const wins = pnls.filter((p) => p > 0);
  const losses = pnls.filter((p) => p < 0);
  const grossWin = wins.reduce((s, p) => s + p, 0);
  const grossLoss = Math.abs(losses.reduce((s, p) => s + p, 0));
  if (grossLoss === 0) return grossWin > 0 ? Infinity : 0;
  return grossWin / grossLoss;
}

/**
 * Group resolved orders by UTC day, sum PnL per day.
 * @param {any[]} orders — orders with .TS (unix ms) and .ResolvedPNLCents
 * @returns {{ day: string, pnl: number }[]}
 */
export function dailyPnLSeries(orders) {
  /** @type {Record<string, number>} */
  const byDay = {};
  for (const o of orders) {
    if (!o.ResolvedPNLCents || !o.TS) continue;
    const d = new Date(o.TS).toISOString().slice(0, 10);
    byDay[d] = (byDay[d] || 0) + o.ResolvedPNLCents;
  }
  return Object.entries(byDay)
    .map(([day, pnl]) => ({ day, pnl }))
    .sort((a, b) => a.day.localeCompare(b.day));
}

/**
 * Generic daily P&L series — takes a getter for ts (unix ms) and pnl (any unit).
 * @param {any[]} items
 * @param {(item: any) => number} getTS
 * @param {(item: any) => number} getPnL
 * @returns {{ day: string, pnl: number }[]}
 */
export function dailySeries(items, getTS, getPnL) {
  /** @type {Record<string, number>} */
  const byDay = {};
  for (const it of items) {
    const ts = getTS(it);
    const pnl = getPnL(it);
    if (!ts || !pnl) continue;
    const d = new Date(ts).toISOString().slice(0, 10);
    byDay[d] = (byDay[d] || 0) + pnl;
  }
  return Object.entries(byDay)
    .map(([day, pnl]) => ({ day, pnl }))
    .sort((a, b) => a.day.localeCompare(b.day));
}

/**
 * Max drawdown from a daily P&L series (in cents).
 * Tracks cumulative P&L, finds worst peak-to-trough drop.
 * @param {{ pnl: number }[]} series
 * @returns {number}
 */
export function maxDrawdown(series) {
  if (series.length === 0) return 0;
  let peak = 0;
  let cum = 0;
  let maxDD = 0;
  for (const s of series) {
    cum += s.pnl;
    if (cum > peak) peak = cum;
    const dd = peak - cum;
    if (dd > maxDD) maxDD = dd;
  }
  return maxDD;
}

/**
 * Sharpe ratio of daily P&L series (per-trade sharpe is misleading).
 * mean(daily) / std(daily). Annualized × sqrt(365) optional.
 * @param {{ pnl: number }[]} series
 * @param {boolean} [annualize]
 * @returns {number}
 */
export function sharpeDaily(series, annualize = false) {
  const pnls = series.map((s) => s.pnl);
  if (pnls.length < 2) return 0;
  const m = mean(pnls);
  const sd = stdDev(pnls);
  if (sd === 0) return 0;
  const r = m / sd;
  return annualize ? r * Math.sqrt(365) : r;
}

/**
 * Sortino ratio of daily P&L series.
 * @param {{ pnl: number }[]} series
 * @returns {number}
 */
export function sortinoDaily(series) {
  const pnls = series.map((s) => s.pnl);
  if (pnls.length === 0) return 0;
  const m = mean(pnls);
  const downside = pnls.filter((p) => p < 0);
  if (downside.length === 0) return m > 0 ? Infinity : 0;
  const dd = Math.sqrt(downside.reduce((s, p) => s + p * p, 0) / pnls.length);
  if (dd === 0) return 0;
  return m / dd;
}

/**
 * Daily average P&L (cents) — total PnL / number of distinct trading days.
 * @param {{ pnl: number }[]} series
 * @returns {number}
 */
export function dailyAvgPnL(series) {
  if (series.length === 0) return 0;
  const total = series.reduce((s, d) => s + d.pnl, 0);
  return total / series.length;
}

/**
 * Compute all stats for a set of orders.
 * @param {any[]} orders — settled orders with .pnl, .price, .edge_cents, .won
 * @returns {Record<string, number>}
 */
export function computeStats(orders) {
  if (orders.length === 0) {
    return {};
  }
  const pnls = orders.map((o) => o.pnl);
  const prices = orders.map((o) => o.price ?? o.market_price ?? 0);
  const edges = orders.map((o) => o.edge_cents ?? 0);
  const wins = orders.filter((o) => o.won);
  const winRate = wins.length / orders.length;
  const ev = expectedValue(pnls);
  const sd = stdDev(pnls);
  const sharpe = sd > 0 ? ev / sd : 0;

  return {
    n: orders.length,
    winRate: winRate * 100,
    meanPnl: ev,
    medianPnl: median(pnls),
    stdDev: sd,
    variance: variance(pnls),
    skewness: skewness(pnls),
    kurtosis: kurtosis(pnls),
    minPnl: min(pnls),
    maxPnl: max(pnls),
    range: range(pnls),
    q1: percentile(pnls, 25),
    q3: percentile(pnls, 75),
    iqr: iqr(pnls),
    cv: cv(pnls),
    ci95: ci95(pnls),
    zScore: zScore(pnls),
    sharpe,
    sortino: sortino(pnls),
    profitFactor: profitFactor(pnls),
    kelly: kellyFraction(winRate, pnls),
    meanPrice: mean(prices),
    medianPrice: median(prices),
    stdDevPrice: stdDev(prices),
    skewPrice: skewness(prices),
    kurtPrice: kurtosis(prices),
    meanEdge: mean(edges),
    medianEdge: median(edges),
    stdDevEdge: stdDev(edges),
    skewEdge: skewness(edges),
    kurtEdge: kurtosis(edges),
  };
}
