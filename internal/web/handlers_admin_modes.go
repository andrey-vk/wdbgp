package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/andrey-vk/wdbgp/internal/logging"
	"github.com/andrey-vk/wdbgp/internal/store"
)

func (s *Server) modesPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	modes, err := s.store.CatalogModes(ctx, false)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	feedCounts, _ := s.store.ModeFeedCounts(ctx)
	type modeRow struct {
		Mode      store.CatalogMode
		FeedCount int
	}
	rows := make([]modeRow, 0, len(modes))
	for _, m := range modes {
		rows = append(rows, modeRow{Mode: m, FeedCount: feedCounts[m.ID]})
	}
	s.renderAdmin(w, r, http.StatusOK, "Catalog Modes", "modes", map[string]any{
		"Modes": rows,
		"Saved": r.URL.Query().Get("saved") == "1",
	})
}

func (s *Server) addMode(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.httpError(w, r, "error.bad_request", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Error(w, "mode name is required", http.StatusBadRequest)
		return
	}
	key := strings.TrimSpace(r.FormValue("key"))
	if key == "" {
		key = strings.ToLower(strings.ReplaceAll(name, " ", "-"))
	}
	enabled := r.Form.Has("enabled")
	_, err := s.store.AddCatalogMode(r.Context(), key, name, enabled)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	http.Redirect(w, r, "/admin/modes?saved=1", http.StatusSeeOther)
}

func (s *Server) deleteMode(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.httpError(w, r, "error.bad_mode_id", http.StatusBadRequest)
		return
	}
	if id <= 3 {
		http.Error(w, "built-in modes cannot be deleted", http.StatusBadRequest)
		return
	}
	if err := s.store.DeleteCatalogMode(r.Context(), id); err != nil {
		s.internalError(w, r, err)
		return
	}
	if err := s.bgp.Reconcile(r.Context()); err != nil {
		logger := logging.FromContext(r.Context())
		logger.Error("delete mode reconcile failed", "error", err)
	}
	http.Redirect(w, r, "/admin/modes?saved=1", http.StatusSeeOther)
}

func (s *Server) modeEditPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := pathID(r)
	if err != nil {
		s.httpError(w, r, "error.bad_mode_id", http.StatusBadRequest)
		return
	}
	mode, err := s.store.CatalogMode(ctx, id)
	if store.IsNotFound(err) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	feeds, err := s.store.Feeds(ctx, false)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	modeFeeds, _ := s.store.ModeFeeds(ctx, id)
	modeFeedSet := make(map[int64]bool)
	for _, f := range modeFeeds {
		modeFeedSet[f.ID] = true
	}
	s.renderAdmin(w, r, http.StatusOK, mode.Name, "mode-edit", map[string]any{
		"Mode":        mode,
		"Feeds":       feeds,
		"ModeFeedIDs": modeFeedSet,
	})
}

func (s *Server) modeFeedToggle(w http.ResponseWriter, r *http.Request) {
	modeID, err := pathID(r)
	if err != nil {
		s.httpError(w, r, "error.bad_mode_id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.httpError(w, r, "error.bad_request", http.StatusBadRequest)
		return
	}
	feedID, err := strconv.ParseInt(r.FormValue("feed_id"), 10, 64)
	if err != nil || feedID <= 0 {
		http.Error(w, "invalid feed id", http.StatusBadRequest)
		return
	}
	if r.FormValue("action") == "remove" {
		if err := s.store.RemoveFeedFromMode(r.Context(), modeID, feedID); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	} else {
		if err := s.store.AddFeedToMode(r.Context(), modeID, feedID); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	// Generate communities before reconcile so new categories/services have community values
	s.store.GenerateCommunities(r.Context(), modeID)
	if err := s.bgp.Reconcile(r.Context()); err != nil {
		s.internalError(w, r, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/mode/%d", modeID), http.StatusSeeOther)
}

func (s *Server) updateCatalogMode(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.httpError(w, r, "error.bad_mode_id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.httpError(w, r, "error.bad_request", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Error(w, "mode name is required", http.StatusBadRequest)
		return
	}
	if err := s.store.UpdateCatalogMode(r.Context(), store.CatalogMode{
		ID: id, Name: name, Enabled: r.Form.Has("enabled"),
	}); err != nil {
		s.internalError(w, r, err)
		return
	}
	if err := s.bgp.Reconcile(r.Context()); err != nil {
		s.internalError(w, r, err)
		return
	}
	http.Redirect(w, r, "/admin/modes", http.StatusSeeOther)
}
