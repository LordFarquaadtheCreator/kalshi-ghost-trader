import { browser } from '$app/environment';
import { readable } from 'svelte/store';
import { api } from './api.js';

const MAX_SAMPLES = 120;

/** @type {{ current: any, history: any[], error: string|null, connected: boolean }} */
const init = { current: null, history: [], error: null, connected: false };

function createSystemStore() {
  if (!browser) return readable(init);

  /** @type {any[]} */
  let history = [];

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
      const interval = document.hidden ? 0 : api.pollInterval(1000);
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

export const systemStore = createSystemStore();
