package web

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/andrey-vk/wdbgp/internal/logging"
	"github.com/andrey-vk/wdbgp/internal/store"
)

type modeJSON struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled"`
	FeedCount int    `json:"feed_count"`
}

type modeFeedJSON struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	Enabled     bool   `json:"enabled"`
	AdapterName string `json:"adapter_name"`
	Exclude     bool   `json:"exclude"`
}

// modeFeedsJSON converts mode feed links to their wire form, resolving
// adapter names best-effort for display.
func (s *Server) modeFeedsJSON(r *http.Request, feeds []store.ModeFeed) []modeFeedJSON {
	result := make([]modeFeedJSON, len(feeds))
	for i, f := range feeds {
		adapter, _ := s.store.FeedAdapter(r.Context(), f.AdapterID) //nolint:errcheck // best-effort lookup for display
		result[i] = modeFeedJSON{
			ID:          f.ID,
			Name:        f.Name,
			URL:         f.URL,
			Enabled:     f.Enabled,
			AdapterName: adapter.Name,
			Exclude:     f.Exclude,
		}
	}
	return result
}

type communityItemJSON struct {
	Category      string `json:"category"`
	Service       string `json:"service"`
	Community     uint32 `json:"community"`
	AutoCommunity uint32 `json:"auto_community"`
}

// --- Mode CRUD handlers ---

// apiModesList handles GET /api/admin/modes.
func (s *Server) apiModesList(w http.ResponseWriter, r *http.Request) {
	modes, err := s.store.CatalogModes(r.Context(), false)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Failed to load modes"})
		return
	}
	feedCounts, _ := s.store.ModeFeedCounts(r.Context()) //nolint:errcheck // best-effort lookup for display
	result := make([]modeJSON, len(modes))
	for i, m := range modes {
		result[i] = modeJSON{
			ID:        m.ID,
			Name:      m.Name,
			Enabled:   m.Enabled,
			FeedCount: feedCounts[m.ID],
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"modes": result})
}

// apiModesGet handles GET /api/admin/modes/{id}.
func (s *Server) apiModesGet(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid mode ID"})
		return
	}
	mode, err := s.store.CatalogMode(r.Context(), id)
	if store.IsNotFound(err) {
		writeJSON(w, http.StatusNotFound, apiResponse{OK: false, Error: "Mode not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Failed to load mode"})
		return
	}
	feedCounts, _ := s.store.ModeFeedCounts(r.Context()) //nolint:errcheck // best-effort lookup for display
	feeds, _ := s.store.ModeFeeds(r.Context(), id)       //nolint:errcheck // best-effort lookup for display
	feedList := s.modeFeedsJSON(r, feeds)
	writeJSON(w, http.StatusOK, map[string]any{
		"mode": modeJSON{
			ID:        mode.ID,
			Name:      mode.Name,
			Enabled:   mode.Enabled,
			FeedCount: feedCounts[mode.ID],
		},
		"feeds": feedList,
	})
}

// apiModesCreate handles POST /api/admin/modes.
func (s *Server) apiModesCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid request body"})
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Mode name is required"})
		return
	}
	id, err := s.store.AddCatalogMode(r.Context(), body.Name, body.Enabled)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}
	mode, _ := s.store.CatalogMode(r.Context(), id) //nolint:errcheck // just created, must exist
	writeJSON(w, http.StatusCreated, modeJSON{
		ID:        mode.ID,
		Name:      mode.Name,
		Enabled:   mode.Enabled,
		FeedCount: 0,
	})
}

// apiModesUpdate handles PUT /api/admin/modes/{id}.
func (s *Server) apiModesUpdate(w http.ResponseWriter, r *http.Request) {
	extendWriteDeadline(w, r) // synchronous BGP reconcile can outlive WriteTimeout
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid mode ID"})
		return
	}
	var body struct {
		Name    *string `json:"name"`
		Enabled *bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid request body"})
		return
	}

	// Load current mode to merge partial updates
	current, err := s.store.CatalogMode(r.Context(), id)
	if store.IsNotFound(err) {
		writeJSON(w, http.StatusNotFound, apiResponse{OK: false, Error: "Mode not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}

	// Apply only provided fields
	if body.Name != nil {
		current.Name = *body.Name
	}
	if body.Enabled != nil {
		current.Enabled = *body.Enabled
	}

	// Validate name if provided
	if body.Name != nil && strings.TrimSpace(*body.Name) == "" {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Mode name is required"})
		return
	}

	if err := s.store.UpdateCatalogMode(r.Context(), current); err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}
	if s.bgp != nil {
		if err := s.bgp.Reconcile(r.Context()); err != nil {
			logging.FromContext(r.Context()).Debug("bgp reconcile failed after mode update", "error", err)
		}
	}
	mode, _ := s.store.CatalogMode(r.Context(), id)      //nolint:errcheck // just updated, must exist
	feedCounts, _ := s.store.ModeFeedCounts(r.Context()) //nolint:errcheck // best-effort lookup for display
	writeJSON(w, http.StatusOK, modeJSON{
		ID:        mode.ID,
		Name:      mode.Name,
		Enabled:   mode.Enabled,
		FeedCount: feedCounts[mode.ID],
	})
}

// apiModesDelete handles DELETE /api/admin/modes/{id}.
func (s *Server) apiModesDelete(w http.ResponseWriter, r *http.Request) {
	extendWriteDeadline(w, r) // synchronous BGP reconcile can outlive WriteTimeout
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid mode ID"})
		return
	}
	if id <= 3 {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Built-in catalog modes cannot be deleted"})
		return
	}
	if err := s.store.DeleteCatalogMode(r.Context(), id); err != nil {
		if store.IsNotFound(err) {
			writeJSON(w, http.StatusNotFound, apiResponse{OK: false, Error: "Mode not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}
	if s.bgp != nil {
		if err := s.bgp.Reconcile(r.Context()); err != nil {
			logging.FromContext(r.Context()).Debug("bgp reconcile failed after mode delete", "error", err)
		}
	}
	writeJSON(w, http.StatusOK, apiResponse{OK: true})
}

// --- Mode-Feed assignment handlers ---

// apiModeFeedsGet handles GET /api/admin/modes/{id}/feeds.
func (s *Server) apiModeFeedsGet(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid mode ID"})
		return
	}
	feeds, err := s.store.ModeFeeds(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Failed to load mode feeds"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"feeds": s.modeFeedsJSON(r, feeds)})
}

// apiModeFeedsSet handles PUT /api/admin/modes/{id}/feeds.
func (s *Server) apiModeFeedsSet(w http.ResponseWriter, r *http.Request) {
	extendWriteDeadline(w, r) // synchronous BGP reconcile can outlive WriteTimeout
	modeID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid mode ID"})
		return
	}
	var body struct {
		// feeds carries roles; feed_ids is the legacy include-only form,
		// used when feeds is absent.
		Feeds []struct {
			ID      int64 `json:"id"`
			Exclude bool  `json:"exclude"`
		} `json:"feeds"`
		FeedIDs []int64 `json:"feed_ids"`
	}
	if err = json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid request body"})
		return
	}
	links := make([]store.ModeFeedLink, 0, len(body.Feeds)+len(body.FeedIDs))
	if body.Feeds != nil {
		for _, f := range body.Feeds {
			links = append(links, store.ModeFeedLink{FeedID: f.ID, Exclude: f.Exclude})
		}
	} else {
		for _, feedID := range body.FeedIDs {
			links = append(links, store.ModeFeedLink{FeedID: feedID})
		}
	}
	// Links and the materialized rebuild land in ONE transaction: a rebuild
	// failure rolls the membership change back, so catalog_mode_feeds and
	// catalog_mode_entries can never disagree.
	if err := s.store.ReplaceModeFeeds(r.Context(), modeID, links); err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Failed to save feed assignments: " + err.Error()})
		return
	}
	// Generate communities and reconcile (best-effort side effects, after commit)
	s.store.GenerateCommunities(r.Context(), modeID) //nolint:errcheck,gosec // best-effort community generation
	if s.bgp != nil {
		if err := s.bgp.Reconcile(r.Context()); err != nil {
			logging.FromContext(r.Context()).Debug("bgp reconcile failed after mode feeds save", "error", err)
		}
	}
	var peerStates map[string]string
	if s.bgp != nil {
		peerStates, _ = s.bgp.PeerStates(r.Context()) //nolint:errcheck // best-effort
	}
	s.store.RecordUserSnapshot(r.Context(), s.settings.MetricsEnabled.Get(), peerStates)
	// Return updated feed list
	feeds, err := s.store.ModeFeeds(r.Context(), modeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Failed to load mode feeds"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"feeds": s.modeFeedsJSON(r, feeds)})
}

// --- Communities handlers ---

// apiModeCommunitiesGet handles GET /api/admin/modes/{id}/communities.
func (s *Server) apiModeCommunitiesGet(w http.ResponseWriter, r *http.Request) {
	modeID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid mode ID"})
		return
	}
	mode, err := s.store.CatalogMode(r.Context(), modeID)
	if store.IsNotFound(err) {
		writeJSON(w, http.StatusNotFound, apiResponse{OK: false, Error: "Mode not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Failed to load mode"})
		return
	}
	// Load existing communities
	communities, err := s.store.GetCommunities(r.Context(), modeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Failed to load communities"})
		return
	}
	// Load catalog (categories and services) for this mode
	catalog, err := s.store.CatalogForMode(r.Context(), modeID, false)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Failed to load catalog"})
		return
	}
	// Build sorted list of categories
	categories := make([]string, 0, len(catalog))
	for cat := range catalog {
		categories = append(categories, cat)
	}
	sort.Strings(categories)
	// Build community items
	var items []communityItemJSON
	for groupIndex, category := range categories {
		services := catalog[category]
		sort.Strings(services)
		// Group-level community
		grpComm := communities[category]
		items = append(items, communityItemJSON{
			Category:      category,
			Service:       "",
			Community:     grpComm,
			AutoCommunity: store.AutoGroupCommunity(groupIndex),
		})
		// Service-level communities
		for svcIndex, service := range services {
			key := category + "|" + service
			svcComm := communities[key]
			items = append(items, communityItemJSON{
				Category:      category,
				Service:       service,
				Community:     svcComm,
				AutoCommunity: store.AutoCommunity(groupIndex, svcIndex),
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"communities": items,
		"mode_id":     mode.ID,
		"mode_name":   mode.Name,
	})
}

// apiModeCommunitiesPut handles PUT /api/admin/modes/{id}/communities.
func (s *Server) apiModeCommunitiesPut(w http.ResponseWriter, r *http.Request) {
	extendWriteDeadline(w, r) // synchronous BGP reconcile can outlive WriteTimeout
	modeID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid mode ID"})
		return
	}
	var body struct {
		Communities []struct {
			Category  string `json:"category"`
			Service   string `json:"service"`
			Community uint32 `json:"community"`
		} `json:"communities"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid request body"})
		return
	}
	// Validate no duplicate community numbers within the mode
	used := make(map[uint32]string) // community value -> "category|service"
	for _, c := range body.Communities {
		key := c.Category + "|" + c.Service
		if c.Community == 0 {
			continue
		}
		if existing, ok := used[c.Community]; ok && existing != key {
			writeJSON(w, http.StatusBadRequest, apiResponse{
				OK:    false,
				Error: "duplicate community " + strconv.FormatUint(uint64(c.Community), 10) + " between " + existing + " and " + key,
			})
			return
		}
		used[c.Community] = key
	}
	// Save each community
	updated := 0
	for _, c := range body.Communities {
		if c.Community == 0 {
			// Clear the manual override — delete row so auto value takes over.
			if err := s.store.DeleteCommunity(r.Context(), modeID, c.Category, c.Service); err != nil {
				writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
				return
			}
			continue
		}
		if err := s.store.SetCommunity(r.Context(), modeID, c.Category, c.Service, c.Community); err != nil {
			writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: err.Error()})
			return
		}
		updated++
	}
	// Auto-generate communities for any cleared or missing entries.
	s.store.GenerateCommunities(r.Context(), modeID) //nolint:errcheck,gosec // best-effort, already called elsewhere
	if s.bgp != nil {
		if err := s.bgp.Reconcile(r.Context()); err != nil {
			logging.FromContext(r.Context()).Debug("bgp reconcile failed after community set", "error", err)
		}
	}
	writeJSON(w, http.StatusOK, apiResponse{OK: true})
}

// apiModeCommunitiesReset handles POST /api/admin/modes/{id}/communities/reset.
func (s *Server) apiModeCommunitiesReset(w http.ResponseWriter, r *http.Request) {
	extendWriteDeadline(w, r) // synchronous BGP reconcile can outlive WriteTimeout
	modeID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid mode ID"})
		return
	}
	if _, err := s.store.DB.ExecContext(r.Context(),
		"DELETE FROM catalog_communities WHERE mode_id = ?", modeID); err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}
	generated, err := s.store.GenerateCommunities(r.Context(), modeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}
	if s.bgp != nil {
		if err := s.bgp.Reconcile(r.Context()); err != nil {
			logging.FromContext(r.Context()).Debug("bgp reconcile failed after community reset", "error", err)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"generated": generated,
	})
}

// apiModeCommunitiesGenerate handles POST /api/admin/modes/{id}/communities/generate.
func (s *Server) apiModeCommunitiesGenerate(w http.ResponseWriter, r *http.Request) {
	extendWriteDeadline(w, r) // synchronous BGP reconcile can outlive WriteTimeout
	modeID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid mode ID"})
		return
	}
	generated, err := s.store.GenerateCommunities(r.Context(), modeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}
	if s.bgp != nil {
		if err := s.bgp.Reconcile(r.Context()); err != nil {
			logging.FromContext(r.Context()).Debug("bgp reconcile failed after community generate", "error", err)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"generated": generated,
	})
}
