package web

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/andrey-vk/wdbgp/internal/store"
)

// apiFeedSyncChanges handles GET /api/admin/feeds/{id}/sync-changes: the
// feed's most recent syncs that changed its entries, newest first.
func (s *Server) apiFeedSyncChanges(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid feed ID"})
		return
	}
	changes, err := s.store.RecentFeedSyncChanges(r.Context(), id, 20)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}
	if changes == nil {
		changes = []store.FeedSyncChange{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"changes": changes})
}

// apiUserFeedChanges handles GET /api/user/feed-changes: services that feed
// syncs added to categories this user has selected, not yet acknowledged.
func (s *Server) apiUserFeedChanges(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	changes, err := s.store.UserFeedChanges(r.Context(), user.ID, time.Now())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}
	if changes == nil {
		changes = []store.UserFeedChange{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"changes": changes})
}

// apiUserFeedChangesAck handles POST /api/user/feed-changes/ack. mode_id and
// through are the mode and the newest change_id the user was shown; changes up
// to it are marked seen in that mode.
func (s *Server) apiUserFeedChangesAck(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	var body struct {
		ModeID  int64 `json:"mode_id"`
		Through int64 `json:"through"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ModeID <= 0 || body.Through <= 0 {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid request body"})
		return
	}
	if err := s.store.AckUserFeedChanges(r.Context(), user.ID, body.ModeID, body.Through); err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{OK: true})
}
