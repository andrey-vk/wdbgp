package embedspa

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed webgui/dist
var distFS embed.FS

// HTTPFS returns an http.FileSystem rooted at webgui/dist.
// Returns nil if the embed failed (fallback to disk in dev mode).
func HTTPFS() http.FileSystem {
	sub, err := fs.Sub(distFS, "webgui/dist")
	if err != nil {
		return nil
	}
	return http.FS(sub)
}
