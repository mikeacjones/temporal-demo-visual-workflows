import adapter from '@sveltejs/adapter-static';
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';

/** @type {import('@sveltejs/kit').Config} */
const config = {
	preprocess: vitePreprocess(),
	kit: {
		// A single-page app: the Go `app` process serves build/ and falls back to index.html.
		adapter: adapter({ pages: 'build', assets: 'build', fallback: 'index.html' }),
		alias: { $lib: 'src/lib' }
	}
};

export default config;
