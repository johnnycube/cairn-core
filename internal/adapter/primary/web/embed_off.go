//go:build !embedweb

package web

import "net/http"

// Handler reports that no frontend is embedded in this build. The default
// (dev) build serves the UI via the SvelteKit Node container + Caddy.
func Handler() (http.Handler, bool) { return nil, false }
