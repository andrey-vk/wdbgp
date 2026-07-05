package spa

import (
	"io/fs"
	"net/http"

	embedspa "github.com/andrey-vk/wdbgp"
)

// HTTPFS returns an http.FileSystem rooted at webgui/dist.
// Returns nil if the embed failed (fallback to disk in dev mode).
func HTTPFS() http.FileSystem {
	return embedspa.HTTPFS()
}

// DistFS returns an fs.FS rooted at webgui/dist for use with http.FileServerFS.
func DistFS() (fs.FS, error) {
	return embedspa.DistFS()
}
