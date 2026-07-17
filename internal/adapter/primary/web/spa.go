// Package web serves the SvelteKit frontend embedded into the Go binary
// for production single-artifact deployments.
//
// Build modes:
//
//   - default build (no tag): Handler() returns (nil, false). The binary
//     ships no frontend; in dev the SvelteKit Node container + Caddy serve
//     the UI (dev: vite serves it; prod: the embedweb binary does — see
//     docs/run-local.md).
//   - `-tags embedweb`: Handler() returns an http.Handler that serves the
//     static SPA embedded from ./dist with client-side-routing fallback.
//     docker/Dockerfile.core builds the SvelteKit static output into ./dist
//     and compiles with this tag, yielding one binary that serves both the
//     API and the UI — no separate web deployment in prod.
package web

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// spaHandler serves a single-page app out of root: real files are served
// as-is; any other path falls back to index.html so client-side routing
// (e.g. /activities/<id>) resolves. Missing asset-looking paths (those with
// a file extension) 404 rather than masquerading as the SPA shell.
func spaHandler(root fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}

		if f, err := root.Open(name); err == nil {
			_ = f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}

		// No matching file. A path whose last segment has an extension is an
		// asset request — return 404 instead of the HTML shell so broken
		// asset URLs are visible rather than silently 200-ing.
		if strings.Contains(path.Base(name), ".") {
			http.NotFound(w, r)
			return
		}

		// Client route: serve the SPA shell.
		shell := r.Clone(r.Context())
		shell.URL.Path = "/"
		fileServer.ServeHTTP(w, shell)
	})
}
