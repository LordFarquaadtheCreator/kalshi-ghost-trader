import { test, expect } from '@playwright/test';

// Playwright test: promoted model appears on champion leaderboard.
// Mocks the API to return a champion model, navigates to /strategies,
// and asserts the model appears in the leaderboard with correct data.

test('promoted model appears on champion leaderboard', async ({ page }) => {
	// Mock the models API endpoint.
	await page.route('**/api/v1/models', (route) => {
		route.fulfill({
			status: 200,
			contentType: 'application/json',
			body: JSON.stringify({
				data: [
					{
						id: 1,
						family: 'fairvalue',
						version: 7,
						status: 'champion',
						trial_index: 6,
						strategy_name: 'rl.fairvalue.v7',
						feature_hash: 'abc123',
						artifact_sha: 'sha456',
						metrics: {
							filled_trades: 600,
							trading_days: 25,
							raw_sharpe: 2.1,
							deflated_sharpe: 1.3,
						},
					},
					{
						id: 2,
						family: 'fairvalue',
						version: 8,
						status: 'candidate',
						trial_index: 7,
						strategy_name: 'rl.fairvalue.v8',
						feature_hash: 'abc123',
						artifact_sha: 'sha789',
						metrics: {},
					},
				],
			}),
		});
	});

	await page.goto('/strategies');

	// Champion row should be visible in the leaderboard.
	const championRow = page.locator('[data-testid="champion-row"]');
	await expect(championRow).toBeVisible();
	await expect(championRow).toContainText('rl.fairvalue.v7');
	await expect(championRow).toContainText('fairvalue');
	await expect(championRow).toContainText('v7');
	await expect(championRow).toContainText('1.300');
});

test('candidate model does not appear on champion leaderboard', async ({ page }) => {
	await page.route('**/api/v1/models', (route) => {
		route.fulfill({
			status: 200,
			contentType: 'application/json',
			body: JSON.stringify({
				data: [
					{
						id: 1,
						family: 'fairvalue',
						version: 1,
						status: 'candidate',
						trial_index: 0,
						strategy_name: 'rl.fairvalue.v1',
						metrics: {},
					},
				],
			}),
		});
	});

	await page.goto('/strategies');

	// No champion rows.
	const championRow = page.locator('[data-testid="champion-row"]');
	await expect(championRow).toHaveCount(0);

	// Empty state for leaderboard.
	await expect(page.getByText('No champion models yet')).toBeVisible();
});

test('model lineage shows all model versions', async ({ page }) => {
	await page.route('**/api/v1/models', (route) => {
		route.fulfill({
			status: 200,
			contentType: 'application/json',
			body: JSON.stringify({
				data: [
					{ id: 1, family: 'fairvalue', version: 1, status: 'retired', trial_index: 0, strategy_name: 'rl.fairvalue.v1' },
					{ id: 2, family: 'fairvalue', version: 2, status: 'shadow', trial_index: 1, strategy_name: 'rl.fairvalue.v2' },
					{ id: 3, family: 'fairvalue', version: 3, status: 'paper', trial_index: 2, strategy_name: 'rl.fairvalue.v3' },
					{ id: 4, family: 'fairvalue', version: 4, status: 'champion', trial_index: 3, strategy_name: 'rl.fairvalue.v4' },
				],
			}),
		});
	});

	await page.goto('/strategies');

	// All versions visible in lineage.
	await expect(page.getByText('rl.fairvalue.v1')).toBeVisible();
	await expect(page.getByText('rl.fairvalue.v2')).toBeVisible();
	await expect(page.getByText('rl.fairvalue.v3')).toBeVisible();
	await expect(page.getByText('rl.fairvalue.v4')).toBeVisible();

	// All statuses visible.
	await expect(page.getByText('retired')).toBeVisible();
	await expect(page.getByText('shadow')).toBeVisible();
	await expect(page.getByText('paper')).toBeVisible();
	await expect(page.getByText('champion')).toBeVisible();
});
