export const ssr = false;

/** @type {import('./$types').PageLoad} */
export async function load({ fetch }) {
  try {
    const res = await fetch('/api/strategies');
    if (res.ok) return { initial: await res.json() };
  } catch {}
  return { initial: null };
}
