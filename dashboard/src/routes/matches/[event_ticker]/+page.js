export const ssr = false;

/** @type {import('./$types').PageLoad} */
export async function load({ fetch, params }) {
  const eventTicker = /** @type {string} */ (params.event_ticker);
  try {
    const res = await fetch(`/api/ticks?event=${encodeURIComponent(eventTicker)}`);
    if (res.ok) return { initial: await res.json(), eventTicker };
  } catch {}
  return { initial: null, eventTicker };
}
