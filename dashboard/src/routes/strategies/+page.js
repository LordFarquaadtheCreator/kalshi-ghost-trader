export const ssr = false;

/** @type {import('./$types').PageLoad} */
export async function load({ fetch }) {
	try {
		const resp = await fetch('/api/v1/models');
		if (!resp.ok) return { models: [], error: `HTTP ${resp.status}` };
		const data = await resp.json();
		return { models: data.data || data || [], error: null };
	} catch (err) {
		return { models: [], error: String(err) };
	}
}
