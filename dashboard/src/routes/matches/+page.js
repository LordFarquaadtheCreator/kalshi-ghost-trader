export const ssr = false;

/** @type {import('./$types').PageLoad} */
export async function load({ fetch }) {
  try {
    const [trackedRes, countsRes] = await Promise.all([
      fetch('/api/tracked'),
      fetch('/api/order-counts').catch(() => null),
    ]);
    /** @type {any} */
    const data = { tracked: null, counts: null };
    if (trackedRes.ok) data.tracked = await trackedRes.json();
    if (countsRes && countsRes.ok) data.counts = await countsRes.json();
    return data;
  } catch {
    return { tracked: null, counts: null };
  }
}
