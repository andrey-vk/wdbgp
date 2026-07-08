package web

import (
	"net/http"
	"time"

	"github.com/andrey-vk/wdbgp/internal/logging"
	"github.com/andrey-vk/wdbgp/internal/store"
)

// extendRequestDeadlines lifts the server-wide ReadTimeout and WriteTimeout
// for a handler that legitimately outlives them. Write side: synchronous
// feed syncs (minutes for large feeds), BGP reload, and every handler that
// runs a synchronous reconcile — without this the work completes
// server-side but the response write happens after the connection's
// deadline and the admin sees a broken reply. Read side: the selection and
// count endpoints accept payloads up to selectionBodyLimit (32 MiB), which
// an admin on a slow uplink (< ~9 Mbit/s) cannot finish uploading within
// the 30s ReadTimeout. Must run before the handler reads the body. Reaches
// the real connection through wrapper writers via their Unwrap methods
// (http.ResponseController). Best-effort: an error just means the writer
// doesn't support deadlines (e.g. httptest recorders), which is fine.
func extendRequestDeadlines(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	if err := rc.SetReadDeadline(time.Time{}); err != nil {
		logging.FromContext(r.Context()).Debug("extend read deadline failed", "error", err)
	}
	if err := rc.SetWriteDeadline(time.Time{}); err != nil {
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
