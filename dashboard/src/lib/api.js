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
  simulation: 300_000,
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

// paperOrderFilterParams builds a query string from filter fields for the
// split paper-orders API. Skips empty/zero values so the URL is clean.
/** @param {{strategies?: string[], min_price?: number, max_price?: number, match?: string, result?: string, after_ts?: number, cursor_ts?: number, cursor_id?: number, limit?: number}} filters */
function paperOrderFilterParams(filters) {
  const params = new URLSearchParams();
  if (filters?.strategies && filters.strategies.length > 0) {
    params.set('strategies', filters.strategies.join(','));
  }
  if (filters?.min_price && filters.min_price > 0) {
    params.set('min_price', String(filters.min_price));
  }
  if (filters?.max_price && filters.max_price > 0) {
    params.set('max_price', String(filters.max_price));
  }
  if (filters?.match) params.set('match', filters.match);
  if (filters?.result) params.set('result', filters.result);
  if (filters?.after_ts !== undefined) params.set('after_ts', String(filters.after_ts));
  if (filters?.cursor_ts !== undefined) {
    params.set('cursor_ts', String(filters.cursor_ts));
    params.set('cursor_id', String(filters.cursor_id ?? 0));
  }
  if (filters?.limit) params.set('limit', String(filters.limit));
  const qs = params.toString();
  return qs ? `?${qs}` : '';
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

  // --- R.2/R.3: split paper-orders API ---

  async getPaperOrdersMeta() {
    return cachedFetch(`${STRATEGY_API_URL}/api/paper-orders/meta`, 60_000);
  },

  /** @param {{strategies?: string[], min_price?: number, max_price?: number, match?: string, result?: string}} [filters] */
  async getPaperOrdersSummary(filters = {}) {
    const qs = paperOrderFilterParams(filters);
    return rawFetch(`${STRATEGY_API_URL}/api/paper-orders/summary${qs}`);
  },

  /** @param {{strategies?: string[], min_price?: number, max_price?: number, match?: string, result?: string, cursor_ts?: number, cursor_id?: number, limit?: number}} [filters] */
  async getPaperOrdersPage(filters = {}) {
    const qs = paperOrderFilterParams(filters);
    return rawFetch(`${STRATEGY_API_URL}/api/paper-orders${qs}`);
  },

  /** @param {number} afterTS @param {{strategies?: string[], min_price?: number, max_price?: number, match?: string, result?: string}} [filters] */
  async getPaperOrdersDelta(afterTS, filters = {}) {
    const qs = paperOrderFilterParams({ ...filters, after_ts: afterTS });
    return rawFetch(`${STRATEGY_API_URL}/api/paper-orders${qs}`);
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

  async getSimulation() {
    return cachedFetch(`${STRATEGY_API_URL}/api/simulation`, TTL.simulation);
  },

  async getPaperOrdersInsights() {
    return cachedFetch(`${STRATEGY_API_URL}/api/paper-orders-insights`, TTL.simulation);
  },

  async getRealOrders() {
    return cachedFetch(`${GHOST_TRADER_URL}/api/real-orders`, TTL.orders);
  },

  /** @param {string} [day] - YYYY-MM-DD or empty for aggregate */
  async getRealOrderMetrics(day) {
    const qs = day ? `?day=${encodeURIComponent(day)}` : '';
    return rawFetch(`${GHOST_TRADER_URL}/api/real-orders/metrics${qs}`);
  },

  async getLiquidityPool() {
    return cachedFetch(`${GHOST_TRADER_URL}/api/liquidity-pool`, TTL.orders);
  },

  async resetLiquidityPool(/** @type {number} */ balanceCents) {
    return mutate(`${GHOST_TRADER_URL}/api/liquidity-pool/reset`, 'POST', { balance_cents: balanceCents });
  },

  async topUpLiquidityPool(/** @type {number} */ addCents) {
    return mutate(`${GHOST_TRADER_URL}/api/liquidity-pool/topup`, 'POST', { add_cents: addCents });
  },

  async getStrategyConfig() {
    return cachedFetch(`${GHOST_TRADER_URL}/api/strategy-config`, TTL.strategies);
  },

  async setStrategyEnabled(/** @type {string} */ strategy, /** @type {boolean} */ enabled) {
    return mutate(`${GHOST_TRADER_URL}/api/strategy-config`, 'PUT', { strategy, enabled });
  },

  async setStrategyLimit(/** @type {string} */ strategy, /** @type {number} */ perMarketMaxOrders) {
    return mutate(`${GHOST_TRADER_URL}/api/strategy-config`, 'PUT', { strategy, per_market_max_orders: perMarketMaxOrders });
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
