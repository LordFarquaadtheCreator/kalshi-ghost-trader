<script>
	// ModelLineage — model family → versions → status tree.
	// Shows the promotion ladder position of each model version.

	/** @typedef {{ id: number, family: string, version: number, status: string, trial_index: number, strategy_name?: string }} Model */

	/** @type {{ models: Model[] }} */
	let { models = [] } = $props();

	// Group by family, sort by version.
	/** @type {[string, Model[]][]} */
	let families = $derived.by(() => {
		/** @type {Record<string, Model[]>} */
		const map = {};
		for (const m of models) {
			if (!map[m.family]) map[m.family] = [];
			map[m.family].push(m);
		}
		for (const f of Object.keys(map)) {
			map[f].sort((a, b) => b.version - a.version);
		}
		return /** @type {[string, Model[]][]} */ (Object.entries(map).sort((a, b) => a[0].localeCompare(b[0])));
	});

	/** @type {Record<string, string>} */
	const statusColors = {
		candidate: 'var(--surface)',
		shadow: 'var(--border)',
		paper: 'var(--pending, #f0ad4e)',
		champion: 'var(--win, #4caf50)',
		retired: 'var(--loss, #f44336)',
	};
</script>

<div class="model-lineage">
	{#each families as [family, versions]}
		<div class="family-group">
			<div class="family-name">{family}</div>
			<div class="versions">
				{#each versions as m}
					<div class="model-row" data-status={m.status}>
						<span class="version">v{m.version}</span>
						<span class="status-badge" style="background:{statusColors[m.status] || 'var(--surface)'}">
							{m.status}
						</span>
						<span class="trial">trial #{m.trial_index}</span>
						<span class="strategy-name">{m.strategy_name || `rl.${family}.v${m.version}`}</span>
					</div>
				{/each}
			</div>
		</div>
	{/each}
	{#if models.length === 0}
		<div class="empty">No models registered</div>
	{/if}
</div>

<style>
	.model-lineage {
		display: flex;
		flex-direction: column;
		gap: 1rem;
	}
	.family-group {
		border: 1px solid var(--border);
		border-radius: 6px;
		padding: 0.75rem;
	}
	.family-name {
		font-weight: 600;
		font-size: 0.95rem;
		margin-bottom: 0.5rem;
		text-transform: uppercase;
		letter-spacing: 0.05em;
		color: var(--text-secondary, #888);
	}
	.versions {
		display: flex;
		flex-direction: column;
		gap: 0.25rem;
	}
	.model-row {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		padding: 0.25rem 0;
		font-size: 0.85rem;
	}
	.version {
		font-weight: 600;
		min-width: 3rem;
	}
	.status-badge {
		padding: 0.1rem 0.4rem;
		border-radius: 3px;
		font-size: 0.75rem;
		color: #fff;
		text-transform: uppercase;
	}
	.trial {
		color: var(--text-secondary, #888);
		font-size: 0.75rem;
	}
	.strategy-name {
		margin-left: auto;
		font-family: monospace;
		font-size: 0.8rem;
	}
	.empty {
		color: var(--text-secondary, #888);
		text-align: center;
		padding: 2rem;
	}
</style>
