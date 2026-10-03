package web

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/andrey-vk/wdbgp/internal/store"
)

// apiSettingsPreviewFilters handles POST /api/admin/settings/preview-filters.
// Reports the blast radius of replacing the global filter_allow/filter_deny
// with the submitted values, without persisting anything — issue #49 item
// #3's shared preview primitive applied to global route filters.
func (s *Server) apiSettingsPreviewFilters(w http.ResponseWriter, r *http.Request) {
	var body struct {
		FilterAllow string `json:"filter_allow"`
		FilterDeny  string `json:"filter_deny"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid request body"})
		return
	}
	if err := s.settings.FilterAllow.Validate(body.FilterAllow); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: err.Error()})
		return
	}
	if err := s.settings.FilterDeny.Validate(body.FilterDeny); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: err.Error()})
		return
	}
	preview, err := s.store.PreviewGlobalRouteFilterChange(r.Context(), body.FilterAllow, body.FilterDeny)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

// apiUserRouteFiltersPreview handles
// POST /api/admin/users/{id}/route-filters/preview. Reports the blast
// radius of replacing userID's own route filters with the submitted
// values, without persisting anything.
func (s *Server) apiUserRouteFiltersPreview(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid user ID"})
		return
	}
	var body store.RouteFilters
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid request body"})
		return
	}
	if _, err := store.NormalizeRouteFilters(body); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: err.Error()})
		return
	}
	preview, err := s.store.PreviewUserRouteFilterChange(r.Context(), userID, body)
	if err != nil {
		if store.IsNotFound(err) {
			writeJSON(w, http.StatusNotFound, apiResponse{OK: false, Error: "User not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

// apiModeFeedsPreview handles POST /api/admin/modes/{id}/feeds/preview.
// Reports the blast radius of replacing modeID's feed membership with the
// submitted links, without persisting anything. Same body shape as
// apiModeFeedsSet.
func (s *Server) apiModeFeedsPreview(w http.ResponseWriter, r *http.Request) {
	modeID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid mode ID"})
		return
	}
	var body struct {
		Feeds []struct {
			ID      int64 `json:"id"`
			Exclude bool  `json:"exclude"`
		} `json:"feeds"`
		FeedIDs []int64 `json:"feed_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
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
	preview, err := s.store.PreviewModeFeedChange(r.Context(), modeID, links)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

// apiUserModePreview handles POST /api/admin/users/{id}/mode/preview.
// Reports the blast radius of moving userID to the submitted
// catalog_mode_id, without persisting anything.
func (s *Server) apiUserModePreview(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid user ID"})
		return
	}
	var body struct {
		CatalogModeID int64 `json:"catalog_mode_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid request body"})
		return
	}
	if body.CatalogModeID <= 0 {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid mode ID"})
		return
	}
	// Same validation apiUsersUpdate applies before actually changing
	// catalog_mode_id — without it, a disabled or nonexistent target mode
	// would reach previewBlastRadius's trial UPDATE, which affects zero
	// rows and surfaces as a misleading 404 "User not found" rather than
	// the real save's own 400 for this exact case.
	if mode, err := s.store.CatalogMode(r.Context(), body.CatalogModeID); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Catalog mode not found"})
		return
	} else if !mode.Enabled {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Catalog mode is disabled"})
		return
	}
	preview, err := s.store.PreviewUserModeMove(r.Context(), userID, body.CatalogModeID)
	if err != nil {
		if store.IsNotFound(err) {
			writeJSON(w, http.StatusNotFound, apiResponse{OK: false, Error: "User not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, preview)
}
