// Package migrations embeds Cairn's SQL migration files into the binary.
//
// The goose Provider in internal/db reads from this FS, so migrations
// travel with the compiled binary — no separate files to ship.
package migrations

import "embed"

// FS is the embedded migration filesystem. Files are named NNNNN_*.sql
// and processed by goose in ascending numeric order.
//
//go:embed *.sql
var FS embed.FS
