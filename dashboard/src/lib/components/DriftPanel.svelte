<script>
	// DriftPanel — shows feature drift between training and live.
	// Compares feature distributions from training vs recent live data.

	/** @typedef {{ feature: string, train_mean: number, live_mean: number, drift_score: number }} DriftRow */

	/** @type {{ driftData: DriftRow[] }} */
	let { driftData = [] } = $props();

	// driftData: array of { feature, train_mean, live_mean, drift_score }
</script>

<div class="drift-panel">
	{#if driftData.length > 0}
		<table class="drift-table">
			<thead>
				<tr>
					<th>Feature</th>
					<th>Train Mean</th>
					<th>Live Mean</th>
					<th>Drift Score</th>
				</tr>
			</thead>
			<tbody>
				{#each driftData as row}
					<tr data-drift={row.drift_score > 2 ? 'high' : row.drift_score > 1 ? 'medium' : 'low'}>
						<td class="feature-name">{row.feature}</td>
						<td class="num">{row.train_mean.toFixed(4)}</td>
						<td class="num">{row.live_mean.toFixed(4)}</td>
						<td class="num drift-score">{row.drift_score.toFixed(3)}</td>
					</tr>
				{/each}
			</tbody>
		</table>
	{:else}
		<div class="empty">No drift data available</div>
	{/if}
</div>

<style>
	.drift-panel {
		overflow-x: auto;
	}
	.drift-table {
		width: 100%;
		border-collapse: collapse;
		font-size: 0.85rem;
	}
	.drift-table th {
		text-align: left;
		padding: 0.4rem 0.5rem;
		border-bottom: 1px solid var(--border);
		color: var(--text-muted);
		font-weight: 600;
	}
	.drift-table td {
		padding: 0.3rem 0.5rem;
		border-bottom: 1px solid var(--border);
	}
	.feature-name {
		font-family: monospace;
	}
	.num {
		text-align: right;
		font-family: monospace;
	}
	tr[data-drift='high'] .drift-score {
		color: var(--loss, #f44336);
		font-weight: 600;
	}
	tr[data-drift='medium'] .drift-score {
		color: var(--pending, #f0ad4e);
	}
	tr[data-drift='low'] .drift-score {
		color: var(--win, #4caf50);
	}
	.empty {
		color: var(--text-muted);
		text-align: center;
		padding: 1rem;
	}
</style>
