<script>
	import favicon from '$lib/assets/favicon.svg';

	let { children } = $props();
</script>

<svelte:head>
	<link rel="icon" href={favicon} />
</svelte:head>

<nav class="topnav">
	<a href="/">Metrics</a>
	<a href="/strategies">Strategies</a>
</nav>

{@render children()}

<style>
	.topnav {
		display: flex;
		gap: 16px;
		padding: 10px 20px;
		background: #0f172a;
		border-bottom: 1px solid #1e293b;
	}
	.topnav a {
		color: #64748b;
		text-decoration: none;
		font-size: 13px;
		font-weight: 600;
		padding: 4px 0;
	}
	.topnav a:hover {
		color: #e2e8f0;
	}
</style>
