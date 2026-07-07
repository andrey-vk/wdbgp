package web

import (
	"net/http"
	"time"

	"github.com/andrey-vk/wdbgp/internal/logging"
	"github.com/andrey-vk/wdbgp/internal/store"
)

// extendWriteDeadline lifts the server-wide WriteTimeout for a handler that
// legitimately runs longer than it: the synchronous feed syncs (minutes for
// large feeds) and the BGP reload (full speaker rebuild + reconcile over all
// desired prefixes). Without this the work completes server-side but the
// response write happens after the connection's deadline and the admin sees
// a broken reply instead of the result. Reaches the real connection through
// wrapper writers via their Unwrap methods (http.ResponseController).
// Best-effort: an error just means the writer doesn't support deadlines
// (e.g. httptest recorders), which is fine.
func extendWriteDeadline(w http.ResponseWriter, r *http.Request) {
	if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil {
		logging.FromContext(r.Context()).Debug("extend write deadline failed", "error", err)
	}
}

func countEnabledFeeds(feeds []store.Feed) int {
	count := 0
	for _, feed := range feeds {
		if feed.Enabled {
			count++
		}
	}
	return count
}
