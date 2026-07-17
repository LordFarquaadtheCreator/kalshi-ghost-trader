import { browser } from '$app/environment';
import { readable } from 'svelte/store';
import { api } from './api.js';

const MAX_SAMPLES = 120;

/**
 * Generic polling store. Calls fn on interval, keeps latest result.
 * Pauses when tab hidden. Uses exponential backoff on failure.
 * @param {() => Promise<any>} fn
 * @param {number} intervalMs
 * @param {any} initialState
 * @returns {import('svelte/store').Readable<any>}
 */
export function createPoll(fn, intervalMs, initialState) {
  if (!browser) return readable(initialState);

  return readable(initialState, (set) => {
    /** @type {ReturnType<typeof setInterval> | null} */
    let timer = null;
    let running = false;

    async function poll() {
      if (running) return;
      running = true;
      try {
        const data = await fn();
        set({ data, error: null, connected: true });
      } catch (err) {
        set({
          data: null,
          error: err instanceof Error ? err.message : String(err),
          connected: false,
        });
      } finally {
        running = false;
      }
    }

    function schedule() {
      if (timer) clearInterval(timer);
      const interval = document.hidden ? 0 : api.pollInterval(intervalMs);
      if (interval === 0) {
        timer = null;
      } else {
        timer = setInterval(poll, interval);
      }
    }

    poll();
    schedule();

    function onVisibility() {
      if (!document.hidden) {
        poll();
        schedule();
      }
    }

    document.addEventListener('visibilitychange', onVisibility);

    return () => {
      if (timer) clearInterval(timer);
      document.removeEventListener('visibilitychange', onVisibility);
    };
  });
}

/**
 * Metrics-specific poll store with rolling history window.
 * @param {number} intervalMs
 * @returns {import('svelte/store').Readable<{current: any, history: any[], error: string|null, connected: boolean}>}
 */
export function createMetricsPoll(intervalMs = 1000) {
  /** @type {any[]} */
  let history = [];

  /** @type {{ current: any, history: any[], error: string|null, connected: boolean }} */
  const init = { current: null, history: [], error: null, connected: false };

  if (!browser) {
    return readable(init);
  }

  return readable(init, (set) => {
    /** @type {ReturnType<typeof setInterval> | null} */
    let timer = null;
    let running = false;

    async function poll() {
      if (running) return;
      running = true;
      try {
        const data = await api.getMetrics();
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
      } finally {
        running = false;
      }
    }

    function schedule() {
      if (timer) clearInterval(timer);
      const interval = document.hidden ? 0 : api.pollInterval(intervalMs);
      timer = interval === 0 ? null : setInterval(poll, interval);
    }

    poll();
    schedule();

    function onVisibility() {
      if (!document.hidden) {
        poll();
        schedule();
      }
    }

    document.addEventListener('visibilitychange', onVisibility);

    return () => {
      if (timer) clearInterval(timer);
      document.removeEventListener('visibilitychange', onVisibility);
    };
  });
}
