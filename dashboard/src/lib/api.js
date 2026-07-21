import { browser } from '$app/environment';

const GHOST_TRADER_URL = '';
const STRATEGY_API_URL = '';

/** @typedef {{ data: any, timestamp: number, ttl: number }} CacheEntry */
/** @type {Map<string, CacheEntry>} */
const cache = new Map();

/** @type {Map<string, Promise<any>>} */
const inflight = new Map();

let failCount = 0;

/** @type {Record<string, number>} */
const TTL = {
  metrics: 1_000,
  tracked: 2_000,
  orderCounts: 5_000,
  orders: 5_000,
  ticks: 3_000,
  strategies: 300_000,
  backtest: 30_000,
  priceBands: 30_000,
  passedMatches: 10_000,
};

function isCacheFresh(/** @type {CacheEntry} */ entry) {
  return Date.now() - entry.timestamp < entry.ttl;
}

function isCacheStale(/** @type {CacheEntry} */ entry) {
  return Date.now() - entry.timestamp < entry.ttl * 3;
}

async function cachedFetch(/** @type {string} */ url, /** @type {number} */ ttl) {
  if (!browser) return null;

  const cached = cache.get(url);
  if (cached && isCacheFresh(cached)) {
    return cached.data;
  }

  const existing = inflight.get(url);
  if (existing) return existing;

  const promise = (async () => {
    try {
      const res = await fetch(url);
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data = await res.json();
      cache.set(url, { data, timestamp: Date.now(), ttl });
      failCount = 0;
      return data;
    } catch (err) {
      if (cached && isCacheStale(cached)) {
        return cached.data;
      }
      failCount++;
      throw err;
    } finally {
      inflight.delete(url);
    }
  })();

  inflight.set(url, promise);
  return promise;
}

function pollInterval(/** @type {number} */ base) {
  if (failCount === 0) return base;
  const backoff = Math.min(base * Math.pow(2, failCount), 30_000);
  return backoff;
}

async function rawFetch(/** @type {string} */ url) {
  if (!browser) return null;
  const res = await fetch(url);
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

// Any mutation invalidates entire cache — write may affect multiple resources.
async function mutate(/** @type {string} */ url, /** @type {string} */ method, /** @type {any} */ body) {
  if (!browser) return null;
  const res = await fetch(url, {
    method,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  cache.clear();
  return res.json();
}

export const api = {
  get metricsUrl() { return `${GHOST_TRADER_URL}/metrics`; },

  async getMetrics() {
    return cachedFetch(`${GHOST_TRADER_URL}/metrics`, TTL.metrics);
  },

  async getTracked() {
    return cachedFetch(`${GHOST_TRADER_URL}/api/tracked`, TTL.tracked);
  },

  async getOrderCounts() {
    return cachedFetch(`${STRATEGY_API_URL}/api/order-counts`, TTL.orderCounts);
  },

  async getPendingOrderCounts() {
    return cachedFetch(`${STRATEGY_API_URL}/api/pending-order-counts`, TTL.orderCounts);
  },

  async getPassedMatches() {
    return cachedFetch(`${STRATEGY_API_URL}/api/passed-matches?limit=100`, TTL.passedMatches);
  },

  async getOrders(/** @type {{cursor_ts?: number, cursor_id?: number, limit?: number}} */ opts = {}) {
    const params = new URLSearchParams();
    if (opts?.limit) params.set('limit', String(opts.limit));
    if (opts?.cursor_ts !== undefined) {
      params.set('cursor_ts', String(opts.cursor_ts));
      params.set('cursor_id', String(opts.cursor_id ?? 0));
    }
    const qs = params.toString();
    const url = `${STRATEGY_API_URL}/api/orders${qs ? `?${qs}` : ''}`;
    // Bypass cache for paginated cursors — each page is distinct.
    if (opts?.cursor_ts !== undefined) return rawFetch(url);
    return cachedFetch(url, TTL.orders);
  },

  async getTicks(/** @type {string} */ eventTicker) {
    return cachedFetch(
      `${STRATEGY_API_URL}/api/ticks?event=${encodeURIComponent(eventTicker)}`,
      TTL.ticks,
    );
  },

  async getStrategies() {
    return cachedFetch(`${STRATEGY_API_URL}/api/strategies`, TTL.strategies);
  },

  async runBacktest(/** @type {string[]} */ strategies, /** @type {number} */ minPrice) {
    const params = new URLSearchParams({ strategies: strategies.join(',') });
    if (minPrice > 0) params.set('min_price', String(minPrice));
    return cachedFetch(`${STRATEGY_API_URL}/api/backtest?${params}`, TTL.backtest);
  },

  async getPriceBands(/** @type {string[]} */ strategies, /** @type {string} */ metric, /** @type {number} */ minSamples) {
    const params = new URLSearchParams({
      strategies: strategies.join(','),
      metric,
      min_samples: String(minSamples),
    });
    return cachedFetch(`${STRATEGY_API_URL}/api/price-bands?${params}`, TTL.priceBands);
  },

  async getPriceBandsSnapshot() {
    return cachedFetch(`${GHOST_TRADER_URL}/api/price-bands-snapshot`, 300_000);
  },

  async getRealOrders() {
    return cachedFetch(`${GHOST_TRADER_URL}/api/real-orders`, TTL.orders);
  },

  async getLiquidityPool() {
    return cachedFetch(`${GHOST_TRADER_URL}/api/liquidity-pool`, TTL.orders);
  },

  async getStrategyConfig() {
    return cachedFetch(`${GHOST_TRADER_URL}/api/strategy-config`, TTL.strategies);
  },

  async setStrategyEnabled(/** @type {string} */ strategy, /** @type {boolean} */ enabled) {
    return mutate(`${GHOST_TRADER_URL}/api/strategy-config`, 'PUT', { strategy, enabled });
  },

  async getAppConfig() {
    return cachedFetch(`${GHOST_TRADER_URL}/api/app-config`, TTL.strategies);
  },

  async setAppConfig(/** @type {string} */ key, /** @type {string} */ value) {
    return mutate(`${GHOST_TRADER_URL}/api/app-config`, 'PUT', { key, value });
  },

  /** @param {string} [strategy] */
  async getTriggerRanges(strategy) {
    const url = strategy
      ? `${GHOST_TRADER_URL}/api/trigger-ranges?strategy=${encodeURIComponent(strategy)}`
      : `${GHOST_TRADER_URL}/api/trigger-ranges`;
    return cachedFetch(url, TTL.strategies);
  },

  async replaceTriggerRanges(/** @type {string} */ strategy, /** @type {Array<{min_price: number, max_price: number, source?: string, enabled?: boolean}>} */ ranges) {
    return mutate(`${GHOST_TRADER_URL}/api/trigger-ranges`, 'PUT', { strategy, ranges });
  },

  get pollInterval() { return pollInterval; },
};
