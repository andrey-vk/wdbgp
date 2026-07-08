package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

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
	// Live sync status — filled in list responses from the Syncer's
	// in-flight tracking, so the SPA can poll while a sync runs.
	// sync_attempted_at is when the last attempt finished (success or
	// failure) since process start; the SPA detects completion by watching
	// it change, so it uses RFC3339Nano — with 1s resolution two quick
	// failed retries could land on the same timestamp and be missed.
	Syncing         bool   `json:"syncing"`
	SyncStartedAt   string `json:"sync_started_at,omitempty"`
	SyncAttemptedAt string `json:"sync_attempted_at,omitempty"`
}

func feedToJSON(f store.Feed) feedJSON {
	// last_success is stored as Unix epoch seconds (schema >= 34); the
	// JSON API keeps the RFC3339 string shape the frontend renders.
	lastSuccess := ""
	if f.LastSuccess > 0 {
		lastSuccess = time.Unix(f.LastSuccess, 0).UTC().Format(time.RFC3339)
	}
	return feedJSON{
		ID: f.ID, Name: f.Name, URL: f.URL, Enabled: f.Enabled,
		SyncInterval: int64(f.SyncInterval), Data: f.Data,
		AdapterID:    f.AdapterID,
		AllowedHosts: f.AllowedHosts, RestrictHosts: f.RestrictHosts,
		LastSuccess: lastSuccess, LastError: f.LastError,
	}
}

func (s *Server) apiFeedsList(w http.ResponseWriter, r *http.Request) {
	feeds, err := s.store.Feeds(r.Context(), false)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Failed to load feeds"})
		return
	}
	var inFlight, attempted map[int64]time.Time
	if s.syncer != nil {
		inFlight = s.syncer.SyncingFeeds()
		attempted = s.syncer.AttemptedFeeds()
	}
	result := make([]feedJSON, len(feeds))
	for i, f := range feeds {
		result[i] = feedToJSON(f)
		if started, ok := inFlight[f.ID]; ok {
			result[i].Syncing = true
			result[i].SyncStartedAt = started.UTC().Format(time.RFC3339)
		}
		if finished, ok := attempted[f.ID]; ok {
			result[i].SyncAttemptedAt = finished.UTC().Format(time.RFC3339Nano)
		}
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
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Feed name is required"})
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
	// Validate adapter_id before creating the feed — otherwise a stale/invalid
	// adapter_id only fails on the feeds.adapter_id foreign-key constraint,
	// surfacing as a raw driver error under a 500 instead of a clean 400.
	if _, err := s.store.FeedAdapter(r.Context(), body.AdapterID); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid adapter"})
		return
	}
	var modeID int64
	if body.ModeID != nil {
		modeID = *body.ModeID
	}
	// AddFeedWithMode inserts the feed row and the catalog_mode_feeds
	// assignment in one transaction — mode_id was already validated to
	// exist above, so a failure here is a genuine store/DB error, and the
	// whole create rolls back instead of leaving an orphaned feed row with
	// no mode assignment.
	id, err := s.store.AddFeedWithMode(r.Context(), body.Name, body.URL, body.AdapterID, body.Enabled, int(body.SyncInterval), body.Data, body.AllowedHosts, body.RestrictHosts, modeID)
	if err != nil {
		logging.FromContext(r.Context()).Error("feed create failed", "error", err, "mode_id", modeID)
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
	extendWriteDeadline(w, r) // synchronous BGP reconcile can outlive WriteTimeout
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
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Feed name is required"})
		return
	}
	u, uErr := url.Parse(body.URL)
	if uErr != nil || u.Scheme == "" || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid feed URL"})
		return
	}
	// Validate adapter_id before updating — otherwise a stale/invalid
	// adapter_id only fails on the feeds.adapter_id foreign-key constraint,
	// surfacing as a raw driver error under a 500 instead of a clean 400.
	if _, err := s.store.FeedAdapter(r.Context(), body.AdapterID); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid adapter"})
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
		if store.IsNotFound(err) {
			writeJSON(w, http.StatusNotFound, apiResponse{OK: false, Error: "Feed not found"})
			return
		}
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
	extendWriteDeadline(w, r) // synchronous BGP reconcile can outlive WriteTimeout
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid feed ID"})
		return
	}
	if err = s.store.DeleteFeed(r.Context(), id); err != nil {
		if store.IsNotFound(err) {
			writeJSON(w, http.StatusNotFound, apiResponse{OK: false, Error: "Feed not found"})
			return
		}
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

// apiFeedsSyncOne handles POST /api/admin/feeds/{id}/sync. Asynchronous:
// the sync runs in a background goroutine and the handler answers 202
// immediately (409 if a sync for this feed is already running) — a large
// feed takes minutes, and the SPA follows progress by polling the feeds
// list, which carries live syncing/sync_started_at fields. Errors land in
// the feed's last_error exactly as scheduled syncs record them.
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
	mu, ok := s.syncer.TryLockFeed(id)
	if !ok {
		writeJSON(w, http.StatusConflict, apiResponse{OK: false, Error: "Sync already in progress"})
		return
	}
	// Baseline for the SPA's completion watch, read while holding the feed
	// lock so no other sync can bump it before the response: the sync we
	// are about to run is done once the feed's sync_attempted_at moves
	// past this value. Diffing last_error can't do that job — a failed
	// retry may rewrite the identical text.
	baseline := ""
	if finished, ok := s.syncer.LastAttempt(id); ok {
		baseline = finished.UTC().Format(time.RFC3339Nano)
	}
	ctx, cancel := s.backgroundCtx(r)
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		defer cancel()
		defer s.syncer.UnlockFeed(mu)
		if _, err := s.syncer.SyncOneLocked(ctx, f); err != nil {
			if _, execErr := s.store.DB.ExecContext(ctx,
				"UPDATE feeds SET last_error = ? WHERE id = ? AND url = ? AND enabled = 1",
				err.Error(), id, f.URL); execErr != nil {
				logging.FromContext(ctx).Debug("failed to write last_error", "feed_id", id, "error", execErr)
			}
			logging.FromContext(ctx).Error("manual feed sync failed", "feed_id", id, "error", err)
			return
		}
		if s.bgp != nil {
			if err := s.bgp.Reconcile(ctx); err != nil {
				logging.FromContext(ctx).Debug("bgp reconcile failed after feed sync", "error", err)
			}
		}
		s.store.RecordFeedSnapshot(ctx, s.settings.MetricsEnabled.Get())
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "sync_attempted_at": baseline})
}

// apiFeedsSyncAll handles POST /api/admin/feeds/sync-all. Asynchronous —
// see apiFeedsSyncOne; a single in-flight guard prevents overlapping
// sync-all runs (individual feeds are additionally serialized by their
// per-feed locks).
func (s *Server) apiFeedsSyncAll(w http.ResponseWriter, r *http.Request) {
	if s.syncer == nil {
		writeJSON(w, http.StatusServiceUnavailable, apiResponse{OK: false, Error: "Syncer not available"})
		return
	}
	if !s.syncAllInFlight.CompareAndSwap(false, true) {
		writeJSON(w, http.StatusConflict, apiResponse{OK: false, Error: "Sync already in progress"})
		return
	}
	// Completion-watch baselines (see apiFeedsSyncOne), snapshotted before
	// the run starts so every attempt it makes lands after them. Keys are
	// feed IDs; feeds never attempted since process start are simply
	// absent, and the SPA treats a missing baseline as "".
	baselines := make(map[int64]string)
	for id, finished := range s.syncer.AttemptedFeeds() {
		baselines[id] = finished.UTC().Format(time.RFC3339Nano)
	}
	ctx, cancel := s.backgroundCtx(r)
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		defer cancel()
		defer s.syncAllInFlight.Store(false)
		if errs := s.syncer.SyncAll(ctx); len(errs) > 0 {
			logging.FromContext(ctx).Error("manual sync-all completed with errors", "error_count", len(errs))
		}
		if s.bgp != nil {
			s.bgp.Reconcile(ctx) //nolint:errcheck,gosec // best-effort; successful feeds need route updates
		}
		var peerStates map[string]string
		if s.bgp != nil {
			peerStates, _ = s.bgp.PeerStates(ctx) //nolint:errcheck // best-effort
		}
		s.store.RecordUserSnapshot(ctx, s.settings.MetricsEnabled.Get(), peerStates)
		s.store.RecordFeedSnapshot(ctx, s.settings.MetricsEnabled.Get())
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "baselines": baselines})
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
