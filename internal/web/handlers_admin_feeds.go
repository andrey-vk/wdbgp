package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/andrey-vk/wdbgp/internal/feeds"
	"github.com/andrey-vk/wdbgp/internal/logging"
	"github.com/andrey-vk/wdbgp/internal/store"
)

func (s *Server) addFeed(w http.ResponseWriter, r *http.Request) {
	feed, modeIDs, err := parseFeed(r, 0)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Validate all mode IDs before inserting the feed.
	for _, mid := range modeIDs {
		if _, err := s.store.CatalogMode(r.Context(), mid); store.IsNotFound(err) {
			http.Error(w, fmt.Sprintf("mode %d not found", mid), http.StatusBadRequest)
			return
		} else if err != nil {
			s.internalError(w, r, err)
			return
		}
	}
	feedID, err := s.store.AddFeedForModeAdapter(
		r.Context(), feed.Name, feed.URL, feed.ModeID, feed.AdapterID, feed.Enabled, feed.SyncInterval, feed.Data)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if feedID > 0 {
		if len(modeIDs) > 0 {
			if err := s.store.SetFeedModes(r.Context(), feedID, modeIDs); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		}
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) feedEditPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var feed store.Feed
	isNew := true
	var feedModeIDs []int64

	if rawID := r.PathValue("id"); rawID != "" {
		id, err := strconv.ParseInt(rawID, 10, 64)
		if err != nil {
			s.httpError(w, r, "error.bad_feed_id", http.StatusBadRequest)
			return
		}
		feed, err = s.store.Feed(ctx, id)
		if store.IsNotFound(err) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		feedModeIDs, _ = s.store.FeedModes(ctx, id)
		isNew = false
	}

	modes, _ := s.store.CatalogModes(ctx, false)
	adapters, _ := s.store.FeedAdapters(ctx)

	lang, _ := requestLocale(r, s.defaultLang)
	title := translate(lang, "feeds.add")
	if !isNew {
		title = translate(lang, "feeds.edit")
	}

	// Build mode ID set for checkbox checked state
	feedModeSet := make(map[int64]bool)
	for _, mid := range feedModeIDs {
		feedModeSet[mid] = true
	}
	// Default mode selected for new feeds.
	if isNew {
		feedModeSet[store.DefaultCatalogModeID] = true
	}

	s.renderAdmin(w, r, http.StatusOK, title, "feed-edit", map[string]any{
		"Feed": feed, "IsNew": isNew, "Modes": modes, "Adapters": adapters,
		"FeedModeIDs": feedModeSet,
	})
}

func (s *Server) addFeedAdapter(w http.ResponseWriter, r *http.Request) {
	adapter, err := parseFeedAdapter(r, 0, maxSource(s.cfg.JSMaxSourceBytes))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id, err := s.store.AddFeedAdapter(r.Context(), adapter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/adapter/%d", id), http.StatusSeeOther)
}

func (s *Server) feedAdapterPage(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		http.Error(w, "bad adapter id", http.StatusBadRequest)
		return
	}
	if id == 0 {
		// New adapter creation form
		feeds, _ := s.store.Feeds(r.Context(), false)
		lang, _ := requestLocale(r, s.defaultLang)
		emptyAdapter := store.FeedAdapter{
			Language:   "javascript",
			APIVersion: 1,
		}
		s.renderAdmin(w, r, http.StatusOK, translate(lang, "title.adapter_edit"), "adapter-edit", adapterEditView{Adapter: emptyAdapter, Feeds: feeds})
		return
	}
	adapter, err := s.store.FeedAdapter(r.Context(), id)
	if err != nil {
		if store.IsNotFound(err) {
			http.NotFound(w, r)
		} else {
			s.internalError(w, r, err)
		}
		return
	}
	s.renderFeedAdapterEditor(w, r, http.StatusOK, adapter, "")
}

func (s *Server) updateFeedAdapter(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		http.Error(w, "bad adapter id", http.StatusBadRequest)
		return
	}
	adapter, err := parseFeedAdapter(r, id, maxSource(s.cfg.JSMaxSourceBytes))
	if err != nil {
		s.renderFeedAdapterEditor(w, r, http.StatusBadRequest,
			adapter, feeds.FormatAdapterError(err))
		return
	}
	if s.cfg.AdapterBackupDir != "" {
		if old, err := s.store.FeedAdapter(r.Context(), id); err == nil {
			backupAdapterSource(old, s.cfg.AdapterBackupDir, s.cfg.AdapterBackupMax)
		}
	}
	if err := s.store.UpdateFeedAdapter(r.Context(), adapter); err != nil {
		if store.IsNotFound(err) {
			http.NotFound(w, r)
		} else {
			http.Error(w, err.Error(), http.StatusBadRequest)
		}
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/adapter/%d", id), http.StatusSeeOther)
}

func (s *Server) testFeedAdapter(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		http.Error(w, "bad adapter id", http.StatusBadRequest)
		return
	}
	adapter, err := parseFeedAdapter(r, id, maxSource(s.cfg.JSMaxSourceBytes))
	if err != nil {
		s.renderFeedAdapterEditor(w, r, http.StatusBadRequest,
			adapter, feeds.FormatAdapterError(err))
		return
	}
	feedID, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("feed_id")), 10, 64)
	if err != nil || feedID <= 0 {
		http.Error(w, "invalid feed id", http.StatusBadRequest)
		return
	}
	feed, err := s.store.Feed(r.Context(), feedID)
	if err != nil {
		if store.IsNotFound(err) {
			http.NotFound(w, r)
		} else {
			s.internalError(w, r, err)
		}
		return
	}
	if feed.AdapterID != id {
		http.Error(w, "feed does not use this adapter", http.StatusBadRequest)
		return
	}
	stored, err := s.store.FeedAdapter(r.Context(), id)
	if err != nil {
		if store.IsNotFound(err) {
			http.NotFound(w, r)
		} else {
			s.internalError(w, r, err)
		}
		return
	}
	adapter.Key = stored.Key
	adapter.Revision = stored.Revision
	entries, err := s.syncer.TestAdapter(r.Context(), feed, adapter)
	if err != nil {
		s.renderFeedAdapterEditor(w, r, http.StatusBadRequest,
			adapter, feeds.FormatAdapterError(err))
		return
	}
	const previewLimit = 100
	view := adapterTestView{
		Adapter: adapter, Feed: feed, Entries: entries, TotalEntries: len(entries),
	}
	if len(view.Entries) > previewLimit {
		view.Entries = view.Entries[:previewLimit]
		view.Truncated = true
	}
	lang, _ := requestLocale(r, s.defaultLang)
	s.renderAdmin(w, r, http.StatusOK, translate(lang, "title.adapter_test"), "adapter-test", view)
}

func (s *Server) resetFeedAdapter(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		http.Error(w, "bad adapter id", http.StatusBadRequest)
		return
	}
	// Backup old source before reset
	if s.cfg.AdapterBackupDir != "" {
		if old, err := s.store.FeedAdapter(r.Context(), id); err == nil {
			backupAdapterSource(old, s.cfg.AdapterBackupDir, s.cfg.AdapterBackupMax)
		}
	}
	if err := s.store.ResetFeedAdapter(r.Context(), id); err != nil {
		if store.IsNotFound(err) {
			http.NotFound(w, r)
		} else {
			http.Error(w, err.Error(), http.StatusBadRequest)
		}
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/adapter/%d", id), http.StatusSeeOther)
}

func (s *Server) deleteFeedAdapter(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.httpError(w, r, "error.bad_request", http.StatusBadRequest)
		return
	}
	if s.cfg.AdapterBackupDir != "" {
		if adapter, err := s.store.FeedAdapter(r.Context(), id); err == nil {
			backupAdapterSource(adapter, s.cfg.AdapterBackupDir, s.cfg.AdapterBackupMax)
		}
	}
	if err := s.store.DeleteFeedAdapter(r.Context(), id); err != nil {
		s.internalError(w, r, err)
		return
	}
	http.Redirect(w, r, "/admin/adapters", http.StatusSeeOther)
}

func backupAdapterSource(adapter store.FeedAdapter, backupDir string, maxCopies int) {
	if backupDir == "" || adapter.Source == "" {
		return
	}
	os.MkdirAll(backupDir, 0755)
	name := fmt.Sprintf("%s_r%d_%s.js", adapter.Key, adapter.Revision, time.Now().UTC().Format("20060102T150405Z"))
	os.WriteFile(filepath.Join(backupDir, name), []byte(adapter.Source), 0644)
	pruneAdapterBackups(adapter.Key, backupDir, maxCopies)
}

func pruneAdapterBackups(key, dir string, max int) {
	if max <= 0 {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return // no entries to prune
	}
	var files []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), key+"_") {
			files = append(files, e.Name())
		}
	}
	if len(files) <= max {
		return
	}
	sort.Strings(files)
	for _, f := range files[:len(files)-max] {
		os.Remove(filepath.Join(dir, f))
	}
}

func (s *Server) renderFeedAdapterEditor(
	w http.ResponseWriter,
	r *http.Request,
	status int,
	adapter store.FeedAdapter,
	errorMessage string,
) {
	stored, err := s.store.FeedAdapter(r.Context(), adapter.ID)
	if err != nil {
		if store.IsNotFound(err) {
			http.NotFound(w, r)
		} else {
			s.internalError(w, r, err)
		}
		return
	}
	if adapter.Key == "" {
		adapter.Key = stored.Key
	}
	adapter.Revision = stored.Revision
	adapter.BuiltIn = stored.BuiltIn
	feedList, err := s.store.Feeds(r.Context(), false)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	var adapterFeeds []store.Feed
	for _, feed := range feedList {
		if feed.AdapterID == adapter.ID {
			adapterFeeds = append(adapterFeeds, feed)
		}
	}
	lang, _ := requestLocale(r, s.defaultLang)
	s.renderAdmin(w, r, status, translate(lang, "title.adapter_edit"), "adapter-edit", adapterEditView{
		Adapter: adapter, Feeds: adapterFeeds, Error: errorMessage,
	})
}

func (s *Server) updateFeed(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.httpError(w, r, "error.bad_feed_id", http.StatusBadRequest)
		return
	}
	feed, modeIDs, err := parseFeed(r, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Validate mode IDs exist before updating feed
	if len(modeIDs) > 0 {
		for _, mid := range modeIDs {
			if _, err := s.store.CatalogMode(r.Context(), mid); store.IsNotFound(err) {
				http.Error(w, fmt.Sprintf("mode %d not found", mid), http.StatusBadRequest)
				return
			} else if err != nil {
				s.internalError(w, r, err)
				return
			}
		}
	}
	if err := s.store.UpdateFeed(r.Context(), feed); err != nil {
		if store.IsNotFound(err) {
			http.NotFound(w, r)
		} else {
			http.Error(w, err.Error(), http.StatusBadRequest)
		}
		return
	}
	if err := s.store.SetFeedModes(r.Context(), feed.ID, modeIDs); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	for _, mid := range modeIDs {
		s.store.GenerateCommunities(r.Context(), mid)
	}
	if err := s.bgp.Reconcile(r.Context()); err != nil {
		s.internalError(w, r, err)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) deleteFeed(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.httpError(w, r, "error.bad_feed_id", http.StatusBadRequest)
		return
	}
	if err := s.store.DeleteFeed(r.Context(), id); err != nil {
		if store.IsNotFound(err) {
			http.NotFound(w, r)
		} else {
			s.internalError(w, r, err)
		}
		return
	}
	s.syncer.RemoveFeedLock(id)
	if err := s.bgp.Reconcile(r.Context()); err != nil {
		s.internalError(w, r, err)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) syncFeeds(w http.ResponseWriter, r *http.Request) {
	logger := logging.FromContext(r.Context())
	for _, err := range s.syncer.SyncAll(r.Context()) {
		logger.Error("feed sync error", "error", err)
	}
	if err := s.bgp.Reconcile(r.Context()); err != nil {
		s.internalError(w, r, err)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func parseFeed(r *http.Request, id int64) (store.Feed, []int64, error) {
	if err := r.ParseForm(); err != nil {
		return store.Feed{}, nil, err
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		return store.Feed{}, nil, fmt.Errorf("feed name is required")
	}
	rawURL := strings.TrimSpace(r.FormValue("url"))
	parsedURL, err := url.ParseRequestURI(rawURL)
	if err != nil || parsedURL.Host == "" ||
		(parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		return store.Feed{}, nil, fmt.Errorf("feed URL must be an absolute HTTP or HTTPS URL")
	}
	// Parse mode_ids from checkboxes; only use catalog_mode_id as fallback
	// when present. If neither is in the form, return empty (admin wants to
	// unassign all modes).
	var modeIDs []int64
	if r.Form["mode_ids"] != nil {
		for _, raw := range r.Form["mode_ids"] {
			if id, err := strconv.ParseInt(raw, 10, 64); err == nil && id > 0 {
				modeIDs = append(modeIDs, id)
			}
		}
	}
	if len(modeIDs) == 0 && r.Form.Has("catalog_mode_id") {
		modeID, err := formModeID(r, store.DefaultCatalogModeID)
		if err != nil {
			return store.Feed{}, nil, err
		}
		modeIDs = []int64{modeID}
	}
	adapterID := int64(1)
	if rawAdapterID := strings.TrimSpace(r.FormValue("adapter_id")); rawAdapterID != "" {
		adapterID, err = strconv.ParseInt(rawAdapterID, 10, 64)
		if err != nil || adapterID <= 0 {
			return store.Feed{}, nil, fmt.Errorf("invalid adapter id")
		}
	}
	data := strings.TrimSpace(r.FormValue("data"))
	if data != "" && !json.Valid([]byte(data)) {
		return store.Feed{}, nil, fmt.Errorf("feed data must be valid JSON")
	}
	modeID := int64(0)
	if len(modeIDs) > 0 {
		modeID = modeIDs[0]
	}
	return store.Feed{
		ID: id, Name: name, URL: rawURL, ModeID: modeID, AdapterID: adapterID,
		Enabled:      r.Form.Has("enabled"),
		SyncInterval: formInt(r, "sync_interval"),
		Data:         data,
	}, modeIDs, nil
}

func maxSource(configured int) int {
	if configured <= 0 {
		return 1 << 20 // default 1MB
	}
	return configured
}

func parseFeedAdapter(r *http.Request, id int64, maxSourceBytes int) (store.FeedAdapter, error) {
	if err := r.ParseForm(); err != nil {
		return store.FeedAdapter{ID: id}, err
	}
	adapter := store.FeedAdapter{
		ID:           id,
		Key:          strings.TrimSpace(r.FormValue("key")),
		Name:         strings.TrimSpace(r.FormValue("name")),
		Language:     "javascript",
		APIVersion:   1,
		Source:       r.FormValue("source"),
		AllowedHosts: strings.TrimSpace(r.FormValue("allowed_hosts")),
	}
	if err := store.ValidateFeedAdapter(adapter); err != nil {
		return adapter, err
	}
	if err := feeds.ValidateAdapterSource(adapter.Source, maxSourceBytes); err != nil {
		return adapter, err
	}
	return adapter, nil
}

func (s *Server) feedsList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	feeds, err := s.store.Feeds(ctx, false)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	modes, err := s.store.CatalogModes(ctx, false)
	if err != nil {
		modes = nil
	}

	type feedRow struct {
		Feed      store.Feed
		ModeNames string
		LastSync  string
	}

	modeMap := make(map[int64]string)
	for _, m := range modes {
		modeMap[m.ID] = m.Name
	}

	// Build feed→mode multi-mapping
	feedModes := make(map[int64][]int64)
	for _, f := range feeds {
		modeIDs, err := s.store.FeedModes(ctx, f.ID)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		feedModes[f.ID] = modeIDs
	}

	rows := make([]feedRow, 0, len(feeds))
	for _, f := range feeds {
		names := make([]string, 0)
		for _, mid := range feedModes[f.ID] {
			if name, ok := modeMap[mid]; ok {
				names = append(names, name)
			}
		}
		modeNames := strings.Join(names, ", ")
		if modeNames == "" {
			modeNames = "—"
		}
		rows = append(rows, feedRow{
			Feed:      f,
			ModeNames: modeNames,
			LastSync:  f.LastSuccess,
		})
	}

	s.renderAdmin(w, r, http.StatusOK, "Feeds", "feeds-list", map[string]any{
		"Feeds": rows,
		"Modes": modes,
	})
}

func (s *Server) handleFeedForceSync(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid feed ID", http.StatusBadRequest)
		return
	}
	feed, err := s.store.Feed(r.Context(), id)
	if err != nil {
		if store.IsNotFound(err) {
			http.Error(w, "feed not found", http.StatusNotFound)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	if !feed.Enabled {
		http.Error(w, "feed is disabled", http.StatusBadRequest)
		return
	}

	// Double-click prevention: if a sync is already in progress for this feed,
	// Skip without launching a new goroutine.
	// Hold the lock until the goroutine finishes to prevent TOCTOU races.
	mu, ok := s.syncer.TryLockFeed(id)
	if !ok {
		http.Redirect(w, r, "/admin/feeds", http.StatusSeeOther)
		return
	}

	// Only clear last_error to trigger re-sync; keep last_success
	_, err = s.store.DB.ExecContext(r.Context(),
		"UPDATE feeds SET last_error = NULL WHERE id = ?", id)
	if err != nil {
		s.syncer.UnlockFeed(mu)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	go func() {
		defer s.syncer.UnlockFeed(mu)
		// SyncOneLocked skips the internal lock since we already hold it.
		var syncErr error
		executedRevision, err := s.syncer.SyncOneLocked(context.Background(), feed)
		if err != nil {
			syncErr = err
			// Verify feed and adapter haven't been edited since the
			// goroutine was launched. If any field (url, adapter_id,
			// data, mode_id, enabled, name, or adapter revision)
			// changed, the error is from a stale sync and must not
			// overwrite the new feed's status.
			var currentURL, currentData, currentName string
			var currentAdapterID, currentRevision int64
			var currentEnabled bool
			checkErr := s.store.DB.QueryRowContext(context.Background(),
				"SELECT f.url, f.adapter_id, f.data, f.enabled, f.name, a.revision FROM feeds f JOIN feed_adapters a ON a.id = f.adapter_id WHERE f.id = ?", id).
				Scan(&currentURL, &currentAdapterID, &currentData, &currentEnabled, &currentName, &currentRevision)
			if checkErr == nil &&
				currentURL == feed.URL &&
				currentAdapterID == feed.AdapterID &&
				currentData == feed.Data &&
				currentEnabled == feed.Enabled &&
				currentName == feed.Name &&
				currentRevision == executedRevision {
				s.store.DB.ExecContext(context.Background(),
					"UPDATE feeds SET last_error = ? WHERE id = ?", err.Error(), id)
			}
		}
		if err := s.bgp.Reconcile(context.Background()); err != nil {
			// Re-check guard before writing BGP error: if the feed or
			// adapter was edited between sync and reconcile, the error
			// must not overwrite the new feed's status.
			var currentURL, currentData, currentName string
			var currentAdapterID, currentRevision int64
			var currentEnabled bool
			checkErr := s.store.DB.QueryRowContext(context.Background(),
				"SELECT f.url, f.adapter_id, f.data, f.enabled, f.name, a.revision FROM feeds f JOIN feed_adapters a ON a.id = f.adapter_id WHERE f.id = ?", id).
				Scan(&currentURL, &currentAdapterID, &currentData, &currentEnabled, &currentName, &currentRevision)
			if checkErr == nil &&
				currentURL == feed.URL &&
				currentAdapterID == feed.AdapterID &&
				currentData == feed.Data &&
				currentEnabled == feed.Enabled &&
				currentName == feed.Name &&
				currentRevision == executedRevision {
				msg := "BGP reconcile failed: " + err.Error()
				if syncErr != nil {
					msg = syncErr.Error() + "; " + msg
				}
				s.store.DB.ExecContext(context.Background(),
					"UPDATE feeds SET last_error = ? WHERE id = ?", msg, id)
			}
		}
	}()
	http.Redirect(w, r, "/admin/feeds", http.StatusSeeOther)
}

func (s *Server) handleSyncAll(w http.ResponseWriter, r *http.Request) {
	go func() {
		_ = s.syncer.SyncAll(context.Background())
		if err := s.bgp.Reconcile(context.Background()); err != nil {
			// logged inside
		}
	}()
	http.Redirect(w, r, "/admin/feeds", http.StatusSeeOther)
}

func (s *Server) adaptersList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	adapters, err := s.store.FeedAdapters(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.renderAdmin(w, r, http.StatusOK, "Adapters", "adapters-list", map[string]any{
		"Adapters": adapters,
	})
}
