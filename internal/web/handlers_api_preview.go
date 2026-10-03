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
	extendRequestDeadlines(w, r) // large filter upload can outlive ReadTimeout — same as apiSettingsPut
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

// apiUserPreview handles POST /api/admin/users/{id}/preview. Reports the
// combined blast radius of the admin user-edit form's three route-affecting
// fields — filter_mode/filter_override, route filters, and catalog_mode_id —
// changed together in one save, without persisting anything. A single
// combined preview rather than one per field: the admin edit dialog saves
// all three in one PUT, and simulating each field's change independently
// against the original state can miss an impact the combination actually
// produces (or report one that the combination actually avoids).
func (s *Server) apiUserPreview(w http.ResponseWriter, r *http.Request) {
	extendRequestDeadlines(w, r) // large filter upload can outlive ReadTimeout — same as apiUsersUpdate
	userID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid user ID"})
		return
	}
	var body struct {
		FilterMode     string   `json:"filter_mode"`
		FilterOverride bool     `json:"filter_override"`
		Allow          []string `json:"allow"`
		Deny           []string `json:"deny"`
		CatalogModeID  int64    `json:"catalog_mode_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid request body"})
		return
	}
	filters := store.RouteFilters{Allow: body.Allow, Deny: body.Deny}
	if _, err := store.NormalizeRouteFilters(filters); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: err.Error()})
		return
	}
	if body.CatalogModeID <= 0 {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid mode ID"})
		return
	}
	current, err := s.store.User(r.Context(), userID)
	if store.IsNotFound(err) {
		writeJSON(w, http.StatusNotFound, apiResponse{OK: false, Error: "User not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}
	// Same validation apiUsersUpdate applies, and under the same condition
	// — only when catalog_mode_id is actually changing, so an update that
	// doesn't touch it still works even if the user's current mode was
	// disabled sometime after assignment.
	if body.CatalogModeID != current.CatalogModeID {
		if mode, err := s.store.CatalogMode(r.Context(), body.CatalogModeID); err != nil {
			writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Catalog mode not found"})
			return
		} else if !mode.Enabled {
			writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Catalog mode is disabled"})
			return
		}
	}
	preview, err := s.store.PreviewUserEdit(r.Context(), userID, body.FilterMode, body.FilterOverride, filters, body.CatalogModeID)
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
// submitted links AND its enabled flag with the submitted value, without
// persisting anything — ModesPage.vue's Save button changes both in one
// click (as two separate real requests), and previewing the feed change
// alone against a mode that is or stays disabled would always measure
// 0 -> 0 regardless of the feed edit. Feed body shape matches
// apiModeFeedsSet; enabled matches the mode PUT's own body field.
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
		Enabled bool    `json:"enabled"`
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
	preview, err := s.store.PreviewModeFeedChange(r.Context(), modeID, links, body.Enabled)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, preview)
}
