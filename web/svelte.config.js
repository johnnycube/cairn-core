import adapterNode from '@sveltejs/adapter-node';
import adapterStatic from '@sveltejs/adapter-static';
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';

// Dev (default): adapter-node — the SvelteKit Node server, SSR on, run in
// the cairn-web container behind Caddy.
//
// Prod: CAIRN_WEB_TARGET=static → adapter-static, a self-contained SPA
// (fallback index.html) that the Go binary embeds and serves. Pages opt
// out of SSR for this target via src/routes/+layout.ts so there's nothing
// to prerender against a live backend at build time.
const target = process.env.CAIRN_WEB_TARGET ?? 'node';

const adapter =
	target === 'static'
		? adapterStatic({
				pages: 'build',
				assets: 'build',
				fallback: 'index.html',
				precompress: false,
				strict: false
			})
		: adapterNode({ out: 'build' });

/** @type {import('@sveltejs/kit').Config} */
const config = {
	preprocess: vitePreprocess(),
	kit: {
		adapter,
		alias: {
			$proto: './src/lib/proto'
		}
	}
};

export default config;
