package web

import (
	"net/http"
	"time"

	"github.com/andrey-vk/wdbgp/internal/logging"
	"github.com/andrey-vk/wdbgp/internal/store"
)

// extendWriteDeadline lifts the server-wide WriteTimeout for a handler
// that legitimately outlives it: BGP reload and every handler that runs a
// synchronous reconcile. (Manual feed syncs used to be the main case, but
// they run asynchronously now.)
// Without this the work completes server-side but the response write
// happens after the connection's deadline and the admin sees a broken
// reply. Deliberately leaves the read deadline alone — these handlers have
// small bodies, and ReadTimeout is what stops an authenticated client from
// dripping a body forever. Reaches the real connection through wrapper
// writers via their Unwrap methods (http.ResponseController).
// Best-effort: an error just means the writer doesn't support deadlines
// (e.g. httptest recorders), which is fine.
func extendWriteDeadline(w http.ResponseWriter, r *http.Request) {
	if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil {
		logging.FromContext(r.Context()).Debug("extend write deadline failed", "error", err)
	}
}

// extendRequestDeadlines additionally lifts ReadTimeout, for the routes
// whose request body is legitimately large — i.e. every route bodyLimit
// puts in an elevated tier: selection/count payloads up to
// selectionBodyLimit (32 MiB, > ~9 Mbit/s of uplink needed inside the 30s
// ReadTimeout), route-filter carriers (8 MiB), and adapter saves (~6 MiB).
// Must run before the handler reads the body. Everything else keeps the
// read deadline — see extendWriteDeadline.
func extendRequestDeadlines(w http.ResponseWriter, r *http.Request) {
	if err := http.NewResponseController(w).SetReadDeadline(time.Time{}); err != nil {
		logging.FromContext(r.Context()).Debug("extend read deadline failed", "error", err)
	}
	extendWriteDeadline(w, r)
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
