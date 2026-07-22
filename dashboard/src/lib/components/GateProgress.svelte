<script>
	// GateProgress — promotion-gate progress bars.
	// Shows trades accrued (of 500), days (of 20), and Sharpe values.

	/** @typedef {{ id: number, metrics?: { filled_trades?: number, trading_days?: number, raw_sharpe?: number, deflated_sharpe?: number } }} GateModel */

	/** @type {{ model: GateModel | null }} */
	let { model = null } = $props();

	// Extract metrics from the model's metrics object.
	let metrics = $derived(model?.metrics || {});
	let trades = $derived(metrics.filled_trades || 0);
	let days = $derived(metrics.trading_days || 0);
	let rawSharpe = $derived(metrics.raw_sharpe || 0);
	let deflatedSharpe = $derived(metrics.deflated_sharpe || 0);

	const TRADE_GATE = 500;
	const DAY_GATE = 20;

	/** @param {number} value @param {number} gate */
	function pct(value, gate) {
		return Math.min(100, (value / gate) * 100);
	}
</script>

{#if model}
	<div class="gate-progress">
		<div class="gate-section">
			<div class="gate-label">
				Trades: {trades} / {TRADE_GATE}
			</div>
			<div class="progress-bar">
				<div class="progress-fill" style="width:{pct(trades, TRADE_GATE)}%"></div>
			</div>
		</div>

		<div class="gate-section">
			<div class="gate-label">
				Days: {days} / {DAY_GATE}
			</div>
			<div class="progress-bar">
				<div class="progress-fill" style="width:{pct(days, DAY_GATE)}%"></div>
			</div>
		</div>

		<div class="sharpe-row">
			<div class="sharpe-cell">
				<span class="sharpe-label">Raw Sharpe</span>
				<span class="sharpe-value" data-positive={rawSharpe > 0}>{rawSharpe.toFixed(3)}</span>
			</div>
			<div class="sharpe-cell">
				<span class="sharpe-label">Deflated Sharpe</span>
				<span class="sharpe-value" data-positive={deflatedSharpe > 0}>{deflatedSharpe.toFixed(3)}</span>
			</div>
		</div>

		<div class="gate-status">
			{#if trades >= TRADE_GATE && days >= DAY_GATE && deflatedSharpe > 0}
				<span class="gate-passed">Gate passed — eligible for champion promotion</span>
			{:else}
				<span class="gate-pending">
					Not-obviously-broken: {trades >= TRADE_GATE ? '✓' : '✗'} trades,
					{days >= DAY_GATE ? '✓' : '✗'} days,
					{deflatedSharpe > 0 ? '✓' : '✗'} deflated Sharpe > 0
				</span>
			{/if}
		</div>
	</div>
{:else}
	<div class="empty">Select a model to view gate progress</div>
{/if}

<style>
	.gate-progress {
		display: flex;
		flex-direction: column;
		gap: 0.75rem;
	}
	.gate-section {
		display: flex;
		flex-direction: column;
		gap: 0.25rem;
	}
	.gate-label {
		font-size: 0.85rem;
		color: var(--text-secondary, #888);
	}
	.progress-bar {
		height: 8px;
		background: var(--surface);
		border-radius: 4px;
		overflow: hidden;
	}
	.progress-fill {
		height: 100%;
		background: var(--win, #4caf50);
		transition: width 0.3s ease;
	}
	.sharpe-row {
		display: flex;
		gap: 1rem;
	}
	.sharpe-cell {
		display: flex;
		flex-direction: column;
		gap: 0.1rem;
	}
	.sharpe-label {
		font-size: 0.75rem;
		color: var(--text-secondary, #888);
		text-transform: uppercase;
	}
	.sharpe-value {
		font-size: 1.1rem;
		font-weight: 600;
		font-family: monospace;
	}
	.sharpe-value[data-positive='true'] {
		color: var(--win, #4caf50);
	}
	.sharpe-value[data-positive='false'] {
		color: var(--loss, #f44336);
	}
	.gate-status {
		font-size: 0.85rem;
		padding: 0.5rem;
		border-radius: 4px;
	}
	.gate-passed {
		color: var(--win, #4caf50);
		font-weight: 600;
	}
	.gate-pending {
		color: var(--text-secondary, #888);
	}
	.empty {
		color: var(--text-secondary, #888);
		text-align: center;
		padding: 1rem;
	}
</style>
