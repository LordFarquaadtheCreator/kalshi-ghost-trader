import adapter from '@sveltejs/adapter-auto';
import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vite';

export default defineConfig({
	plugins: [
		sveltekit({
			compilerOptions: {
				// Force runes mode for the project, except for libraries. Can be removed in svelte 6.
				runes: ({ filename }) =>
					filename.split(/[/\\]/).includes('node_modules') ? undefined : true
			},

			// adapter-auto only supports some environments, see https://svelte.dev/docs/kit/adapter-auto for a list.
			// If your environment is not supported, or you settled on a specific environment, switch out the adapter.
			// See https://svelte.dev/docs/kit/adapters for more information about adapters.
			adapter: adapter()
		})
	],
	build: {
		target: 'es2022',
		cssMinify: true,
		rollupOptions: {
			output: {
				manualChunks: (id) => {
					if (id.includes('node_modules/chart.js')) return 'chart.js';
				},
			},
		},
	},
	server: {
		host: '0.0.0.0',
		proxy: {
			'/api': 'http://127.0.0.1:6060',
			'/metrics': 'http://127.0.0.1:6060',
			'/debug': 'http://127.0.0.1:6060',
		},
	},
});
