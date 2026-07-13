// Metrics polling store. Fetches /metrics from ghost-trader every interval.
// Keeps rolling window of samples for charting.
// Client-only: browser fetch, no SSR.

import { readable } from 'svelte/store';
import { browser } from '$app/environment';

const MAX_SAMPLES = 120; // 2 min at 1s interval

/**
 * @param {string} endpoint - metrics URL, e.g. http://127.0.0.1:6060/metrics
 * @param {number} intervalMs - poll interval
 * @returns {import('svelte/store').Readable<{current: object|null, history: object[], error: string|null, connected: boolean}>}
 */
export function createMetricsStore(endpoint, intervalMs = 1000) {
  let history = [];
  let timer = null;

  const initialState = { current: null, history: [], error: null, connected: false };

  if (!browser) {
    return readable(initialState);
  }

  return readable(initialState, (set) => {
    async function poll() {
      try {
        const res = await fetch(endpoint);
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        const data = await res.json();
        const sample = { ...data, timestamp: Date.now() };
        history = [...history, sample].slice(-MAX_SAMPLES);
        set({ current: sample, history, error: null, connected: true });
      } catch (err) {
        set({
          current: null,
          history,
          error: err instanceof Error ? err.message : String(err),
          connected: false,
        });
      }
    }

    poll();
    timer = setInterval(poll, intervalMs);

    return () => {
      if (timer) clearInterval(timer);
    };
  });
}
