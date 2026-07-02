package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

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
		ModeID        *int64 `json:"mode_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid request body"})
		return
	}
	u, uErr := url.Parse(body.URL)
	if uErr != nil || u.Scheme == "" || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid feed URL"})
		return
	}
	// Validate mode_id before creating the feed — otherwise a stale/invalid
	// mode_id would only fail the catalog_mode_feeds insert afterward, and
	// that failure was only logged at Debug level: the API still returned
	// 201 with a feed assigned to no mode, silently excluded from
	// Feeds(ctx, true) and therefore from every future automatic sync.
	if body.ModeID != nil && *body.ModeID > 0 {
		if _, err := s.store.CatalogMode(r.Context(), *body.ModeID); err != nil {
			writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid catalog mode"})
			return
		}
	}
	id, err := s.store.AddFeed(r.Context(), body.Name, body.URL, body.AdapterID, body.Enabled, int(body.SyncInterval), body.Data, body.AllowedHosts, body.RestrictHosts)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}
	if body.ModeID != nil && *body.ModeID > 0 {
		if _, err := s.store.DB.ExecContext(r.Context(),
			"INSERT INTO catalog_mode_feeds(mode_id, feed_id) VALUES (?, ?)",
			*body.ModeID, id); err != nil {
			// mode_id was already validated to exist above, so this can now
			// only fail on a genuine store/DB error — surface it rather than
			// silently returning 201 with the feed assigned to no mode.
			logging.FromContext(r.Context()).Error("feed mode assignment failed", "error", err, "feed_id", id, "mode_id", *body.ModeID)
			writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Feed created but mode assignment failed"})
			return
		}
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
	u, uErr := url.Parse(body.URL)
	if uErr != nil || u.Scheme == "" || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid feed URL"})
		return
	}
	// Merge adapter's declared additional hosts with user-provided hosts.
	if extraHosts := s.store.BuiltinAdapterAllowedHosts(r.Context(), body.AdapterID); extraHosts != "" {
		body.AllowedHosts = mergeAllowedHosts(body.AllowedHosts, extraHosts)
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
	if s.bgp != nil {
		if err := s.bgp.Reconcile(r.Context()); err != nil {
			logging.FromContext(r.Context()).Debug("bgp reconcile failed after feed delete", "error", err)
		}
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
	if !f.Enabled {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Feed is disabled"})
		return
	}
	if _, err := s.syncer.SyncOne(r.Context(), f); err != nil {
		if _, execErr := s.store.DB.ExecContext(r.Context(),
			"UPDATE feeds SET last_error = ? WHERE id = ? AND url = ? AND enabled = 1",
			err.Error(), id, f.URL); execErr != nil {
			logging.FromContext(r.Context()).Debug("failed to write last_error", "feed_id", id, "error", execErr)
		}
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}
	if s.bgp != nil {
		if err := s.bgp.Reconcile(r.Context()); err != nil {
			logging.FromContext(r.Context()).Debug("bgp reconcile failed after feed sync", "error", err)
		}
	}
	s.store.RecordFeedSnapshot(r.Context(), s.settings.MetricsEnabled.Get())
	writeJSON(w, http.StatusOK, apiResponse{OK: true})
}

func (s *Server) apiFeedsSyncAll(w http.ResponseWriter, r *http.Request) {
	if s.syncer == nil {
		writeJSON(w, http.StatusServiceUnavailable, apiResponse{OK: false, Error: "Syncer not available"})
		return
	}
	errors := s.syncer.SyncAll(r.Context())
	if s.bgp != nil {
		s.bgp.Reconcile(r.Context()) //nolint:errcheck,gosec // best-effort; successful feeds need route updates
	}
	if len(errors) > 0 {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Some feeds failed to sync"})
		return
	}
	var peerStates map[string]string
	if s.bgp != nil {
		peerStates, _ = s.bgp.PeerStates(r.Context()) //nolint:errcheck // best-effort
	}
	s.store.RecordUserSnapshot(r.Context(), s.settings.MetricsEnabled.Get(), peerStates)
	s.store.RecordFeedSnapshot(r.Context(), s.settings.MetricsEnabled.Get())
	writeJSON(w, http.StatusOK, apiResponse{OK: true})
}

// mergeAllowedHosts adds host to a comma-separated hosts string if not already present.
func mergeAllowedHosts(hosts, host string) string {
	host = strings.TrimSpace(host)
	for _, h := range strings.Split(hosts, ",") {
		if strings.TrimSpace(h) == host {
			return hosts
		}
	}
	if hosts == "" {
		return host
	}
	return hosts + "," + host
}
