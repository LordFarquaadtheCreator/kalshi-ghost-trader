export const ssr = false;

/** @type {import('./$types').PageLoad} */
export async function load({ fetch }) {
  try {
    const resp = await fetch('/api/strategies');
    if (!resp.ok) return { strategies: [], error: `HTTP ${resp.status}` };
    const data = await resp.json();
    return { strategies: data.strategies || [], error: null };
  } catch (err) {
    return { strategies: [], error: String(err) };
  }
}
