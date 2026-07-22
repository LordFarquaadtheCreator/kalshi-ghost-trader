<script>
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import PageHeader from '$lib/components/PageHeader.svelte';
	import CollapsibleSection from '$lib/components/CollapsibleSection.svelte';
	import ModelLineage from '$lib/components/ModelLineage.svelte';
	import GateProgress from '$lib/components/GateProgress.svelte';
	import DriftPanel from '$lib/components/DriftPanel.svelte';
	import EmptyState from '$lib/components/EmptyState.svelte';
	import { api } from '$lib/api.js';

	/** @typedef {{ id: number, family: string, version: number, status: string, trial_index: number, strategy_name?: string, feature_hash?: string, artifact_sha?: string, metrics?: any }} Model */

	let { data } = $props();

	/** @type {Model[]} */
	let models = $state(data.models || []);
	let error = $state(data.error || null);
	let connected = $state(!error);
	/** @type {Model | null} */
	let selectedModel = $state(null);
	/** @type {Array<{feature: string, train_mean: number, live_mean: number, drift_score: number}>} */
	let driftData = $state([]);

	onMount(() => {
		if (models.length > 0) {
			selectedModel = models[0];
		}
	});

	async function refresh() {
		try {
			const result = await api.getModels();
			models = result.data || result || [];
			error = null;
			connected = true;
			if (!selectedModel && models.length > 0) {
				selectedModel = models[0];
			}
		} catch (e) {
			error = String(e);
			connected = false;
		}
	}

	/** @param {Model} m */
	function selectModel(m) {
		selectedModel = m;
	}

	// Find champion per family for leaderboard.
	let champions = $derived(
		models.filter((m) => m.status === 'champion')
	);

	let paperModels = $derived(
		models.filter((m) => m.status === 'paper')
	);
</script>

<svelte:head><title>Strategies — Kalshi Ghost Trader</title></svelte:head>

<div class="page-container">
	<PageHeader title="Learned Strategies" {connected} error={error || undefined}>
		<button onclick={refresh} class="refresh-btn">Refresh</button>
	</PageHeader>

	{#if models.length === 0 && !error}
		<EmptyState text="No models registered. Train a model to get started." />
	{:else if error}
		<EmptyState text={error} variant="error" />
	{:else}
		<div class="strategies-layout">
			<div class="left-panel">
				<CollapsibleSection title="Model Lineage" count={models.length} defaultOpen={true}>
					<ModelLineage {models} />
				</CollapsibleSection>

				<CollapsibleSection title="Champion Leaderboard" count={champions.length} defaultOpen={true}>
					{#if champions.length > 0}
						<table class="leaderboard">
							<thead>
								<tr>
									<th>Strategy</th>
									<th>Family</th>
									<th>Version</th>
									<th>Deflated Sharpe</th>
								</tr>
							</thead>
							<tbody>
								{#each champions as m}
									<tr data-testid="champion-row">
										<td class="strategy-name">{m.strategy_name || `rl.${m.family}.v${m.version}`}</td>
										<td>{m.family}</td>
										<td>v{m.version}</td>
										<td class="num">{(m.metrics?.deflated_sharpe || 0).toFixed(3)}</td>
									</tr>
								{/each}
							</tbody>
						</table>
					{:else}
						<EmptyState text="No champion models yet" />
					{/if}
				</CollapsibleSection>
			</div>

			<div class="right-panel">
				<CollapsibleSection title="Gate Progress" defaultOpen={true}>
					<GateProgress model={selectedModel} />
				</CollapsibleSection>

				<CollapsibleSection title="Feature Drift" defaultOpen={false}>
					<DriftPanel driftData={driftData} />
				</CollapsibleSection>

				{#if selectedModel}
				<CollapsibleSection title="Model Details" defaultOpen={true}>
					<dl class="model-details">
						<dt>ID</dt><dd>{selectedModel.id}</dd>
						<dt>Strategy</dt><dd>{selectedModel.strategy_name || `rl.${selectedModel.family}.v${selectedModel.version}`}</dd>
						<dt>Family</dt><dd>{selectedModel.family}</dd>
						<dt>Version</dt><dd>v{selectedModel.version}</dd>
						<dt>Status</dt><dd>{selectedModel.status}</dd>
						<dt>Trial Index</dt><dd>{selectedModel.trial_index}</dd>
						<dt>Feature Hash</dt><dd class="mono">{selectedModel.feature_hash}</dd>
						<dt>Artifact SHA</dt><dd class="mono">{selectedModel.artifact_sha}</dd>
					</dl>
				</CollapsibleSection>
				{/if}
			</div>
		</div>
	{/if}
</div>

<style>
	.page-container {
		padding: 20px;
	}
	.strategies-layout {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 1rem;
	}
	.left-panel, .right-panel {
		display: flex;
		flex-direction: column;
		gap: 1rem;
	}
	.refresh-btn {
		padding: 4px 12px;
		font-size: 12px;
		border: 1px solid var(--border);
		border-radius: 4px;
		background: var(--surface);
		cursor: pointer;
	}
	.refresh-btn:hover {
		background: var(--border);
	}
	.leaderboard {
		width: 100%;
		border-collapse: collapse;
		font-size: 0.85rem;
	}
	.leaderboard th {
		text-align: left;
		padding: 0.4rem 0.5rem;
		border-bottom: 1px solid var(--border);
		color: var(--text-muted);
	}
	.leaderboard td {
		padding: 0.3rem 0.5rem;
		border-bottom: 1px solid var(--border);
	}
	.strategy-name {
		font-family: monospace;
	}
	.num {
		text-align: right;
		font-family: monospace;
	}
	.model-details {
		display: grid;
		grid-template-columns: auto 1fr;
		gap: 0.25rem 1rem;
		font-size: 0.85rem;
	}
	.model-details dt {
		color: var(--text-muted);
		font-weight: 600;
	}
	.model-details dd {
		margin: 0;
	}
	.mono {
		font-family: monospace;
		font-size: 0.8rem;
	}
	@media (max-width: 768px) {
		.strategies-layout {
			grid-template-columns: 1fr;
		}
	}
</style>
