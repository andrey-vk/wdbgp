package web

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/andrey-vk/wdbgp/internal/logging"
	"github.com/andrey-vk/wdbgp/internal/store"
)

type feedJSON struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	URL           string `json:"url"`
	Enabled       bool   `json:"enabled"`
	SyncInterval  int64  `json:"sync_interval"`
	Data          string `json:"data,omitempty"`
	AdapterID     int64  `json:"adapter_id"`
	AllowedHosts  string `json:"allowed_hosts"`
	RestrictHosts bool   `json:"restrict_hosts"`
	LastSuccess   string `json:"last_success,omitempty"`
	LastError     string `json:"last_error,omitempty"`
}

func feedToJSON(f store.Feed) feedJSON {
	return feedJSON{
		ID: f.ID, Name: f.Name, URL: f.URL, Enabled: f.Enabled,
		SyncInterval: int64(f.SyncInterval), Data: f.Data,
		AdapterID:    f.AdapterID,
		AllowedHosts: f.AllowedHosts, RestrictHosts: f.RestrictHosts,
		LastSuccess: f.LastSuccess, LastError: f.LastError,
	}
}

func (s *Server) apiFeedsList(w http.ResponseWriter, r *http.Request) {
	feeds, err := s.store.Feeds(r.Context(), false)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Failed to load feeds"})
		return
	}
	result := make([]feedJSON, len(feeds))
	for i, f := range feeds {
		result[i] = feedToJSON(f)
	}
	writeJSON(w, http.StatusOK, map[string]any{"feeds": result})
}

func (s *Server) apiFeedsGet(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid feed ID"})
		return
	}
	var f store.Feed
	f, err = s.store.Feed(r.Context(), id)
	if store.IsNotFound(err) {
		writeJSON(w, http.StatusNotFound, apiResponse{OK: false, Error: "Feed not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Failed to load feed"})
		return
	}
	writeJSON(w, http.StatusOK, feedToJSON(f))
}

func (s *Server) apiFeedsCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name          string `json:"name"`
		URL           string `json:"url"`
		Enabled       bool   `json:"enabled"`
		SyncInterval  int64  `json:"sync_interval"`
		Data          string `json:"data"`
		AdapterID     int64  `json:"adapter_id"`
		AllowedHosts  string `json:"allowed_hosts"`
		RestrictHosts bool   `json:"restrict_hosts"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid request body"})
		return
	}
	id, err := s.store.AddFeed(r.Context(), body.Name, body.URL, body.AdapterID, body.Enabled, int(body.SyncInterval), body.Data, body.AllowedHosts, body.RestrictHosts)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}
	f, err := s.store.Feed(r.Context(), id)
	if err != nil {
		logging.FromContext(r.Context()).Debug("feed lookup after create failed", "error", err, "feed_id", id)
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Failed to read created feed"})
		return
	}
	writeJSON(w, http.StatusCreated, feedToJSON(f))
}

func (s *Server) apiFeedsUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid feed ID"})
		return
	}
	var body struct {
		Name          string `json:"name"`
		URL           string `json:"url"`
		Enabled       bool   `json:"enabled"`
		SyncInterval  int64  `json:"sync_interval"`
		Data          string `json:"data"`
		AdapterID     int64  `json:"adapter_id"`
		AllowedHosts  string `json:"allowed_hosts"`
		RestrictHosts bool   `json:"restrict_hosts"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid request body"})
		return
	}
	f := store.Feed{
		ID: id, Name: body.Name, URL: body.URL, Enabled: body.Enabled,
		SyncInterval: int(body.SyncInterval), Data: body.Data,
		AdapterID:    body.AdapterID,
		AllowedHosts: body.AllowedHosts, RestrictHosts: body.RestrictHosts,
	}
	if err := s.store.UpdateFeed(r.Context(), f); err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}
	updated, err := s.store.Feed(r.Context(), id)
	if err != nil {
		logging.FromContext(r.Context()).Debug("feed lookup after update failed", "error", err, "feed_id", id)
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Failed to read updated feed"})
		return
	}
	if s.bgp != nil {
		if err := s.bgp.Reconcile(r.Context()); err != nil {
			logging.FromContext(r.Context()).Debug("bgp reconcile failed after feed update", "error", err)
		}
	}
	writeJSON(w, http.StatusOK, feedToJSON(updated))
}

func (s *Server) apiFeedsDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid feed ID"})
		return
	}
	if err = s.store.DeleteFeed(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{OK: true})
}

func (s *Server) apiFeedsSyncOne(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid feed ID"})
		return
	}
	if s.syncer == nil {
		writeJSON(w, http.StatusServiceUnavailable, apiResponse{OK: false, Error: "Syncer not available"})
		return
	}
	f, err := s.store.Feed(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, apiResponse{OK: false, Error: "Feed not found"})
		return
	}
	if _, err := s.syncer.SyncOne(r.Context(), f); err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}
	if s.bgp != nil {
		if err := s.bgp.Reconcile(r.Context()); err != nil {
			logging.FromContext(r.Context()).Debug("bgp reconcile failed after feed sync", "error", err)
		}
	}
	s.recordFeedSnapshot(r.Context())
	writeJSON(w, http.StatusOK, apiResponse{OK: true})
}

func (s *Server) apiFeedsSyncAll(w http.ResponseWriter, r *http.Request) {
	if s.syncer == nil {
		writeJSON(w, http.StatusServiceUnavailable, apiResponse{OK: false, Error: "Syncer not available"})
		return
	}
	errors := s.syncer.SyncAll(r.Context())
	if len(errors) > 0 {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Some feeds failed to sync"})
		return
	}
	if s.bgp != nil {
		if err := s.bgp.Reconcile(r.Context()); err != nil {
			logging.FromContext(r.Context()).Debug("bgp reconcile failed after feed sync all", "error", err)
		}
	}
	s.recordUserSnapshot(r.Context())
	s.recordFeedSnapshot(r.Context())
	writeJSON(w, http.StatusOK, apiResponse{OK: true})
}
