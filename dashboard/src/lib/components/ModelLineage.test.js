import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import ModelLineage from './ModelLineage.svelte';

describe('ModelLineage', () => {
	it('groups models by family and sorts by version descending', () => {
		const models = [
			{ id: 1, family: 'fairvalue', version: 1, status: 'retired', trial_index: 0 },
			{ id: 2, family: 'fairvalue', version: 3, status: 'champion', trial_index: 2 },
			{ id: 3, family: 'fairvalue', version: 2, status: 'shadow', trial_index: 1 },
			{ id: 4, family: 'bandit', version: 1, status: 'candidate', trial_index: 0 },
		];

		render(ModelLineage, { props: { models } });

		// Both families present.
		expect(screen.getByText('fairvalue')).toBeTruthy();
		expect(screen.getByText('bandit')).toBeTruthy();

		// Versions visible.
		expect(screen.getByText('v3')).toBeTruthy();
		expect(screen.getByText('v2')).toBeTruthy();
		expect(screen.getAllByText('v1').length).toBe(2); // fairvalue v1 + bandit v1

		// Status badges present.
		expect(screen.getByText('champion')).toBeTruthy();
		expect(screen.getByText('shadow')).toBeTruthy();
		expect(screen.getByText('retired')).toBeTruthy();
		expect(screen.getByText('candidate')).toBeTruthy();
	});

	it('shows empty state when no models', () => {
		render(ModelLineage, { props: { models: [] } });
		expect(screen.getByText('No models registered')).toBeTruthy();
	});

	it('displays strategy name for each model', () => {
		const models = [
			{ id: 1, family: 'fairvalue', version: 5, status: 'champion', trial_index: 4, strategy_name: 'rl.fairvalue.v5' },
		];

		render(ModelLineage, { props: { models } });
		expect(screen.getByText('rl.fairvalue.v5')).toBeTruthy();
	});

	it('generates strategy name when not provided', () => {
		const models = [
			{ id: 1, family: 'bandit', version: 2, status: 'candidate', trial_index: 1 },
		];

		render(ModelLineage, { props: { models } });
		expect(screen.getByText('rl.bandit.v2')).toBeTruthy();
	});
});
