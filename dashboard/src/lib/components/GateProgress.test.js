import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import GateProgress from './GateProgress.svelte';

describe('GateProgress', () => {
	it('shows progress bars for trades and days', () => {
		const model = {
			id: 1,
			metrics: { filled_trades: 250, trading_days: 10, raw_sharpe: 1.5, deflated_sharpe: 0.8 },
		};

		render(GateProgress, { props: { model } });

		expect(screen.getByText('Trades: 250 / 500')).toBeTruthy();
		expect(screen.getByText('Days: 10 / 20')).toBeTruthy();
	});

	it('shows raw and deflated Sharpe values', () => {
		const model = {
			id: 1,
			metrics: { filled_trades: 500, trading_days: 20, raw_sharpe: 2.1, deflated_sharpe: 1.3 },
		};

		render(GateProgress, { props: { model } });

		expect(screen.getByText('2.100')).toBeTruthy();
		expect(screen.getByText('1.300')).toBeTruthy();
	});

	it('shows gate passed when all criteria met', () => {
		const model = {
			id: 1,
			metrics: { filled_trades: 600, trading_days: 25, raw_sharpe: 2.0, deflated_sharpe: 1.5 },
		};

		render(GateProgress, { props: { model } });

		expect(screen.getByText(/Gate passed/)).toBeTruthy();
	});

	it('shows gate pending when criteria not met', () => {
		const model = {
			id: 1,
			metrics: { filled_trades: 100, trading_days: 5, raw_sharpe: 0.5, deflated_sharpe: -0.2 },
		};

		render(GateProgress, { props: { model } });

		expect(screen.getByText(/Not-obviously-broken/)).toBeTruthy();
	});

	it('shows empty state when no model selected', () => {
		render(GateProgress, { props: { model: null } });
		expect(screen.getByText('Select a model to view gate progress')).toBeTruthy();
	});

	it('handles missing metrics gracefully', () => {
		const model = { id: 1, metrics: {} };

		render(GateProgress, { props: { model } });

		expect(screen.getByText('Trades: 0 / 500')).toBeTruthy();
		expect(screen.getByText('Days: 0 / 20')).toBeTruthy();
	});
});
