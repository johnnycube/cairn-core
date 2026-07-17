//go:build embedweb

package web

import (
	"embed"
	"io/fs"
	"net/http"
)

// distFS holds the built SvelteKit static output. docker/Dockerfile.core
// populates ./dist from the node build stage before compiling with
// `-tags embedweb`. A placeholder dist/index.html is committed so this
// package always compiles under the tag even without a fresh web build.
//
//go:embed all:dist
var distFS embed.FS

// Handler returns the embedded-SPA handler. ok is true when the frontend
// was compiled in (i.e. this build used `-tags embedweb`).
func Handler() (http.Handler, bool) {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return nil, false
	}
	return spaHandler(sub), true
}
