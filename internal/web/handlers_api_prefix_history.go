package web

import (
	"net/http"
	"strconv"

	"github.com/andrey-vk/wdbgp/internal/store"
)

// apiUserPrefixHistory handles GET /api/admin/users/{id}/prefix-history: a
// user's periodically-measured effective prefix count (see
// internal/alerts), for the admin "prefix history" chart — issue #49 item
// #10's history half, alongside the webhook alerting half.
func (s *Server) apiUserPrefixHistory(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid user ID"})
		return
	}
	if _, err := s.store.User(r.Context(), id); store.IsNotFound(err) {
		writeJSON(w, http.StatusNotFound, apiResponse{OK: false, Error: "User not found"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Failed to load user"})
		return
	}

	days := s.settings.MetricsHistoryDays.Get()
	history, err := s.store.UserPrefixHistory(r.Context(), id, days)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Failed to load prefix history"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"history": history})
}
