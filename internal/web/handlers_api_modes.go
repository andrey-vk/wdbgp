package web

import (
	"encoding/json"
	"errors"
	"fmt"
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

// apiModeSave handles PUT /api/admin/modes/{id}/save: renames, enables or
// disables the mode, and replaces its feed membership as one atomic change.
func (s *Server) apiModeSave(w http.ResponseWriter, r *http.Request) {
	extendWriteDeadline(w, r) // synchronous BGP reconcile can outlive WriteTimeout
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid mode ID"})
		return
	}
	var body struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
		Feeds   []struct {
			ID      int64 `json:"id"`
			Exclude bool  `json:"exclude"`
		} `json:"feeds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid request body"})
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Mode name is required"})
		return
	}
	if _, err := s.store.CatalogMode(r.Context(), id); store.IsNotFound(err) {
		writeJSON(w, http.StatusNotFound, apiResponse{OK: false, Error: "Mode not found"})
		return
	}
	links := make([]store.ModeFeedLink, 0, len(body.Feeds))
	for _, f := range body.Feeds {
		links = append(links, store.ModeFeedLink{FeedID: f.ID, Exclude: f.Exclude})
	}
	if err := s.store.SaveModeWithFeeds(r.Context(), store.CatalogMode{ID: id, Name: body.Name, Enabled: body.Enabled}, links); err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}
	_, _ = s.store.GenerateCommunitiesCount(r.Context(), id) //nolint:errcheck,gosec // best-effort community generation
	if s.bgp != nil {
		if err := s.bgp.Reconcile(r.Context()); err != nil {
			logging.FromContext(r.Context()).Debug("bgp reconcile failed after mode save", "error", err)
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
	meta := store.AuditMeta{Actor: s.adminActor(r), UserAgent: r.Header.Get("User-Agent"), Action: "user.mode_changed"}
	_, err = s.store.DeleteCatalogMode(r.Context(), id, meta)
	if err != nil {
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
	_, _ = s.store.GenerateCommunitiesCount(r.Context(), modeID) //nolint:errcheck,gosec // best-effort community generation
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
	// Load existing communities. Structured rows, not GetCommunities'
	// "category|service"-flattened map: a category legitimately containing
	// "|" would collide with that key scheme (e.g. category "a" service
	// "b" vs. group "a|b"), silently showing the wrong number for one of
	// the two in this list.
	rows, err := s.store.CommunityRows(r.Context(), modeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Failed to load communities"})
		return
	}
	communities := make(map[store.ServiceKey]uint32, len(rows))
	for _, row := range rows {
		communities[store.ServiceKey{Category: row.Category, Service: row.Service}] = row.Community
	}
	// Load the catalog twice over: the enabled-feeds-only scope that's
	// actually listed below, and the wider reset scope (including disabled
	// feeds) purely to compute AutoCommunity/AutoGroupCommunity's
	// positional index against the same ordering basis a real reset uses —
	// genCommunitiesRuntime counts every include-linked feed's entries
	// regardless of whether the feed is enabled, so a disabled feed whose
	// category sorts earlier still shifts every later category's real
	// assignment, even though it contributes no row to the list below.
	// Both scopes come from one transaction: two independent reads could
	// each see their own, separately-committed snapshot if a feed sync
	// landed between them, and since the reset scope is a superset of the
	// visible one by construction, any visible category/service is
	// guaranteed to be found in it too only when both come from the same
	// point in time.
	catalog, resetCatalog, err := s.store.CatalogScopesForMode(r.Context(), modeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Failed to load catalog"})
		return
	}
	resetCategories := make([]string, 0, len(resetCatalog))
	for cat := range resetCatalog {
		resetCategories = append(resetCategories, cat)
	}
	sort.Strings(resetCategories)
	categoryIndex := make(map[string]int, len(resetCategories))
	for i, cat := range resetCategories {
		categoryIndex[cat] = i
	}
	// Build sorted list of categories
	categories := make([]string, 0, len(catalog))
	for cat := range catalog {
		categories = append(categories, cat)
	}
	sort.Strings(categories)
	// Build community items
	var items []communityItemJSON
	for _, category := range categories {
		groupIndex := categoryIndex[category]
		services := catalog[category]
		sort.Strings(services)
		resetServices := make([]string, len(resetCatalog[category]))
		copy(resetServices, resetCatalog[category])
		sort.Strings(resetServices)
		serviceIndex := make(map[string]int, len(resetServices))
		for i, svc := range resetServices {
			serviceIndex[svc] = i
		}
		// Group-level community
		grpComm := communities[store.ServiceKey{Category: category}]
		items = append(items, communityItemJSON{
			Category:      category,
			Service:       "",
			Community:     grpComm,
			AutoCommunity: store.AutoGroupCommunity(groupIndex),
		})
		// Service-level communities
		for _, service := range services {
			svcComm := communities[store.ServiceKey{Category: category, Service: service}]
			items = append(items, communityItemJSON{
				Category:      category,
				Service:       service,
				Community:     svcComm,
				AutoCommunity: store.AutoCommunity(groupIndex, serviceIndex[service]),
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
	// Validate no duplicate community numbers within the mode. Keyed by
	// store.ServiceKey, not a "category|service"-joined string: a category
	// legitimately containing "|" could otherwise make two genuinely
	// different (category, service) pairs compare equal, letting a real
	// duplicate community number through unflagged.
	used := make(map[uint32]store.ServiceKey, len(body.Communities))
	for _, c := range body.Communities {
		if c.Community == 0 {
			continue
		}
		key := store.ServiceKey{Category: c.Category, Service: c.Service}
		if existing, ok := used[c.Community]; ok && existing != key {
			writeJSON(w, http.StatusBadRequest, apiResponse{
				OK: false,
				Error: "duplicate community " + strconv.FormatUint(uint64(c.Community), 10) + " between " +
					existing.Category + "/" + existing.Service + " and " + c.Category + "/" + c.Service,
			})
			return
		}
		used[c.Community] = key
	}
	// Save every community and fill any cleared/missing entries in one
	// transaction, returning the before/after rows atomic with that write
	// — either every update lands and the before/after describe exactly
	// this call's result, or (e.g. a later item conflicts with an
	// assignment this batch doesn't otherwise touch) none do.
	updates := make([]store.CommunityUpdate, 0, len(body.Communities))
	for _, c := range body.Communities {
		updates = append(updates, store.CommunityUpdate{Category: c.Category, Service: c.Service, Community: c.Community})
	}
	meta := store.AuditMeta{Actor: s.adminActor(r), UserAgent: r.Header.Get("User-Agent"), Action: "communities.updated"}
	_, _, err = s.store.SetCommunities(r.Context(), modeID, updates, meta)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: err.Error()})
		return
	}
	if s.bgp != nil {
		if err := s.bgp.Reconcile(r.Context()); err != nil {
			logging.FromContext(r.Context()).Debug("bgp reconcile failed after community set", "error", err)
		}
	}
	writeJSON(w, http.StatusOK, apiResponse{OK: true})
}

// apiModeCommunitiesReset handles POST /api/admin/modes/{id}/communities/reset.
//
// A reset renumbers assignments that downstream routers may have hardcoded
// into their filter policies, and the resulting breakage looks like a network
// fault rather than a config change. So the call is two-step: without
// {"confirm": true} it writes nothing and returns the exact renumbering it
// would perform, plus a digest of the state that preview was computed from.
// Confirming must echo that digest back — if a feed sync or another admin
// changed the mode in between, the digest no longer matches what confirm
// would actually apply, and this hands back a fresh preview instead of
// silently renumbering something the operator never reviewed.
func (s *Server) apiModeCommunitiesReset(w http.ResponseWriter, r *http.Request) {
	extendWriteDeadline(w, r) // synchronous BGP reconcile can outlive WriteTimeout
	modeID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid mode ID"})
		return
	}
	// An absent or empty body is treated as "not confirmed" rather than a
	// parse error, so an unconfirmed reset always answers with the preview.
	var body struct {
		Confirm bool   `json:"confirm"`
		Digest  string `json:"digest"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck // absent body means not confirmed
	}

	if !body.Confirm {
		writePreview(w, r, s, modeID, false)
		return
	}

	meta := store.AuditMeta{Actor: s.adminActor(r), UserAgent: r.Header.Get("User-Agent"), Action: "communities.reset"}
	_, _, generated, err := s.store.ResetCommunities(r.Context(), modeID, body.Digest, meta)
	if errors.Is(err, store.ErrCommunityResetStale) {
		writePreview(w, r, s, modeID, true)
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}
	s.logAdminAction(r, "COMMUNITIES_RESET",
		fmt.Sprintf("mode_id=%d generated=%d", modeID, generated))
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

// writePreview answers an unconfirmed (or stale-confirmed) reset request with
// the current renumbering preview and the digest a follow-up confirm must
// echo. stale marks a confirm that was rejected because the mode changed
// since its digest was issued, so the caller can tell "first look at this"
// apart from "the ground moved, look again" and render accordingly.
func writePreview(w http.ResponseWriter, r *http.Request, s *Server, modeID int64, stale bool) {
	changes, digest, err := s.store.PreviewCommunityReset(r.Context(), modeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}
	status := http.StatusOK
	if stale {
		status = http.StatusConflict
	}
	writeJSON(w, status, map[string]any{
		"ok":       false,
		"confirm":  true,
		"stale":    stale,
		"changes":  changes,
		"affected": len(changes),
		"digest":   digest,
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
	meta := store.AuditMeta{Actor: s.adminActor(r), UserAgent: r.Header.Get("User-Agent"), Action: "communities.generated"}
	_, _, generated, err := s.store.GenerateCommunities(r.Context(), modeID, meta)
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
