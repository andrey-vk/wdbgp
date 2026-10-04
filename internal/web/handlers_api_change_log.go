package web

import (
	"net/http"
	"time"

	"github.com/andrey-vk/wdbgp/internal/store"
)

// apiUserChangeLog handles GET /api/user/change-log: the changes to this
// user's selection, route filters, and mode over the last 30 days (or the
// audit retention, if shorter), newest first, each attributed to the user, an
// admin, or a feed sync.
func (s *Server) apiUserChangeLog(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	window := store.UserChangeLogWindow(s.settings.AuditLogRetentionDays.Get())
	entries, err := s.store.UserChangeLog(r.Context(), user.ID, time.Now(), window)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}
	if entries == nil {
		entries = []store.UserChangeEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}
