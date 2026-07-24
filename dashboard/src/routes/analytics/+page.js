export const ssr = false;

export async function load({ fetch }) {
  try {
    const [attrStrat, attrMatch, attrSeries] = await Promise.all([
      fetch('/api/orders/attribution?group_by=strategy'),
      fetch('/api/orders/attribution?group_by=match'),
      fetch('/api/orders/attribution?group_by=series'),
    ]);
    const [strategy, match, series] = await Promise.all([
      attrStrat.json(),
      attrMatch.json(),
      attrSeries.json(),
    ]);
    return { strategy, match, series };
  } catch (/** @type {any} */ e) {
    return { error: e.message };
  }
}
