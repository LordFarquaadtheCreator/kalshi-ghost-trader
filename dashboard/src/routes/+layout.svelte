<script>
	import '../app.css';
	import '$lib/styles.css';
	import favicon from '$lib/assets/favicon.svg';
	import { onDestroy } from 'svelte';
	import { systemStore } from '$lib/system-store.js';
	import AlertSystem from '$lib/components/AlertSystem.svelte';
	import Toaster from '$lib/components/Toaster.svelte';
	import { api } from '$lib/api.js';

	let { children } = $props();

	let curMetrics = $derived($systemStore.current);
	let lpData = $state(null);

	// Poll liquidity pool for alerts
	/** @type {ReturnType<typeof setInterval> | null} */
	let lpPoll = null;
	if (typeof window !== 'undefined') {
		lpPoll = setInterval(async () => {
			try { lpData = await api.getLiquidityPool(); } catch {}
		}, 10000);
		onDestroy(() => { if (lpPoll) clearInterval(lpPoll); });
	}
</script>

<svelte:head>
	<link rel="icon" href={favicon} />
</svelte:head>

<nav class="topnav">
	<a href="/matches">Matches</a>
	<a href="/orders">Paper Orders</a>
	<a href="/real-orders">Real Orders</a>
	<a href="/simulation">Simulation</a>
	<a href="/analytics">Analytics</a>
	<a href="/strategies">Strategies</a>
	<a href="/config">Config</a>
	<a href="/system">System</a>
</nav>

<AlertSystem systemMetrics={curMetrics} liquidityPool={lpData} />

{@render children()}

<Toaster />

<style>
	.topnav {
		display: flex;
		gap: 16px;
		padding: 10px 20px;
		background: var(--surface);
		border-bottom: 1px solid var(--border);
	}
	.topnav a {
		color: var(--text-muted);
		text-decoration: none;
		font-size: 13px;
		font-weight: 600;
		padding: 4px 0;
	}
	.topnav a:hover {
		color: var(--text);
	}
</style>
