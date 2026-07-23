export const ssr = false;

/** @type {import('./$types').PageLoad} */
export async function load({ fetch }) {
  try {
    const [metaRes, summaryRes, pageRes, insightsRes] = await Promise.all([
      fetch('/api/paper-orders/meta'),
      fetch('/api/paper-orders/summary'),
      fetch('/api/paper-orders?limit=100'),
      fetch('/api/paper-orders-insights'),
    ]);
    /** @type {{meta: any, summary: any, page: any, insights: any}} */
    const out = { meta: null, summary: null, page: null, insights: null };
    if (metaRes.ok) out.meta = await metaRes.json();
    if (summaryRes.ok) out.summary = await summaryRes.json();
    if (pageRes.ok) out.page = await pageRes.json();
    if (insightsRes.ok) out.insights = await insightsRes.json();
    return out;
  } catch {}
  return { meta: null, summary: null, page: null, insights: null };
}
