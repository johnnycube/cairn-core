import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { paraglideVitePlugin } from '@inlang/paraglide-js';
import { defineConfig } from 'vite';

// Where the Go server listens during dev. Override with CAIRN_API_ORIGIN when
// :8080 is taken (e.g. `export CAIRN_API_ORIGIN=http://localhost:8090` to match
// CAIRN_HTTP_ADDR=0.0.0.0:8090). `make dev-web` sources dev.env, which sets it.
const apiOrigin = process.env.CAIRN_API_ORIGIN || 'http://localhost:8080';

export default defineConfig({
	plugins: [
		tailwindcss(),
		paraglideVitePlugin({
			project: './project.inlang',
			outdir: './src/lib/paraglide',
			// cookie + Accept-Language header + baseLocale fallback.
			// No URL strategy: the operator UI lives on a single path tree
			// and the cookie + header are enough.
			strategy: ['cookie', 'preferredLanguage', 'baseLocale']
		}),
		sveltekit()
	],
	server: {
		// CAIRN_WEB_PORT lets this dev server avoid colliding with another
		// project's vite on the same origin (a shared :5173 mixes module graphs
		// in the browser). `make dev-web` sources dev.env, which sets it.
		port: Number(process.env.CAIRN_WEB_PORT) || 5173,
		strictPort: true,
		proxy: {
			// Proxy backend-owned URL prefixes to the Go server during dev.
			// In production a reverse proxy (Caddy / nginx) does the same
			// split. NB: `/admin` is intentionally NOT proxied — the
			// SvelteKit /admin route lives at that path and is reached
			// via the Connect-RPC cairn.v1.AdminService instead of the
			// legacy /admin/* HTTP endpoints. Operators who want the
			// legacy endpoints (smoketest, manual curl) hit the Go server
			// directly on :8080.
			'/auth': apiOrigin,
			'/api': apiOrigin,
			'/webhooks': apiOrigin,
			'/cairn.v1.': apiOrigin,
			'/cairn.worker.v1.': apiOrigin,
			// OAuth 2.1 authorization server (backend). NB: /oauth/consent is a
			// SvelteKit route (the consent UI), so it is intentionally NOT
			// proxied — only the backend endpoints below are.
			'/oauth/authorize': apiOrigin,
			'/oauth/token': apiOrigin,
			'/oauth/register': apiOrigin,
			'/oauth/revoke': apiOrigin,
			'/.well-known': apiOrigin,
			// MCP server (OAuth-protected, read-only).
			'/mcp': apiOrigin
		}
	}
});
