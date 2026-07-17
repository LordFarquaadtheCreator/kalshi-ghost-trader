import { browser } from '$app/environment';

const GHOST_TRADER_URL = 'http://127.0.0.1:6060';
const STRATEGY_API_URL = 'http://127.0.0.1:6060';

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

  async getOrders() {
    return cachedFetch(`${STRATEGY_API_URL}/api/orders`, TTL.orders);
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

  get pollInterval() { return pollInterval; },
};
