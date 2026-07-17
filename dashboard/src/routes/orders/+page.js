export const ssr = false;

/** @type {import('./$types').PageLoad} */
export async function load({ fetch }) {
  try {
    const res = await fetch('http://127.0.0.1:6060/api/orders');
    if (res.ok) return { initial: await res.json() };
  } catch {}
  return { initial: null };
}
