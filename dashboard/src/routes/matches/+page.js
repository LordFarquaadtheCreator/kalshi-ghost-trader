export const ssr = false;

/** @type {import('./$types').PageLoad} */
export async function load({ fetch }) {
  try {
    const [trackedRes, countsRes] = await Promise.all([
      fetch('http://127.0.0.1:6060/api/tracked'),
      fetch('http://127.0.0.1:6060/api/order-counts').catch(() => null),
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
