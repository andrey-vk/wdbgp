package spa

import (
	"net/http"

	embedspa "github.com/andrey-vk/wdbgp"
)

// HTTPFS returns an http.FileSystem rooted at webgui/dist.
// Returns nil if the embed failed (fallback to disk in dev mode).
func HTTPFS() http.FileSystem {
	return embedspa.HTTPFS()
}
