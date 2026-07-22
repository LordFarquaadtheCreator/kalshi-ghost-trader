import { defineConfig } from 'vitest/config';
import { sveltekit } from '@sveltejs/kit/vite';

export default defineConfig({
	plugins: [
		sveltekit({
			compilerOptions: {
				runes: ({ filename }) =>
					filename.split(/[/\\]/).includes('node_modules') ? undefined : true
			}
		})
	],
	resolve: {
		conditions: ['browser'],
	},
	test: {
		environment: 'jsdom',
		globals: true,
		include: ['src/**/*.test.{js,ts}'],
		setupFiles: ['src/test-setup.js'],
	},
});
