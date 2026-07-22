export const ssr = false;

/** @type {import('./$types').PageLoad} */
export async function load({ fetch }) {
  try {
    const [ordersRes, insightsRes] = await Promise.all([
      fetch('/api/orders?limit=100'),
      fetch('/api/paper-orders-insights'),
    ]);
    /** @type {{initial: any, insights: any}} */
    const out = { initial: null, insights: null };
    if (ordersRes.ok) out.initial = await ordersRes.json();
    if (insightsRes.ok) out.insights = await insightsRes.json();
    return out;
  } catch {}
  return { initial: null, insights: null };
}
