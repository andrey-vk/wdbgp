package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/andrey-vk/wdbgp/internal/logging"
	"github.com/andrey-vk/wdbgp/internal/store"
)

func (s *Server) settingsPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	dbSettings, _ := s.store.GetAllSettings(ctx) //nolint:errcheck // best-effort lookup for display

	sections := buildSettingsSections(s.cfg, dbSettings)

	// Global route filters
	globalFilters, _ := s.store.GlobalRouteFilters(ctx) //nolint:errcheck // best-effort lookup for display

	s.renderAdmin(w, r, http.StatusOK, "Settings", "settings", map[string]any{
		"Sections":           sections,
		"Saved":              r.URL.Query().Get("saved") == "1",
		"AllowDynamicPeers":  s.cfg.AllowDynamicPeers,
		"AutoRestoreEnabled": s.cfg.AutoRestoreEnabled,
		"GlobalFilters": globalFiltersView{
			Allow: strings.Join(globalFilters.Allow, "\n"),
			Deny:  strings.Join(globalFilters.Deny, "\n"),
		},
	})
}

func (s *Server) saveSettings(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.httpError(w, r, "error.bad_request", http.StatusBadRequest)
		return
	}

	settings := make(map[string]string)
	knownKeys := allSettingKeys()
	for _, key := range knownKeys {
		f := fieldByKey(key)
		if f == nil {
			continue
		}
		if isEnvOverridden(f.EnvVar) {
			continue
		}
		if f.Type == "bool" {
			// Unchecked checkbox doesn't send a value; treat missing as "false"
			if r.Form.Has(key) {
				settings[key] = "true"
			} else {
				settings[key] = "false"
			}
		} else {
			if val, ok := r.Form[key]; ok && len(val) > 0 {
				settings[key] = val[0]
			}
		}
	}

	if err := s.store.SaveSettings(r.Context(), settings); err != nil {
		s.internalError(w, r, err)
		return
	}

	// Reload runtime-mutable settings so they take effect without restart
	s.reloadRuntimeSettings(r.Context())

	// Save global route filters if the fields are present in the form
	if r.Form.Has("filter_allow") || r.Form.Has("filter_deny") {
		if filters, err := routeFiltersFromForm(r); err == nil {
			if err := s.store.SetGlobalRouteFilters(r.Context(), filters); err != nil {
				s.internalError(w, r, err)
				return
			}
			_ = s.bgp.Reconcile(r.Context()) //nolint:errcheck // settings page display, best-effort
		}
	}

	http.Redirect(w, r, "/admin/settings?saved=1", http.StatusSeeOther)
}

func (s *Server) communitiesPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	modes, err := s.store.CatalogModes(ctx, false)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if len(modes) == 0 {
		s.httpError(w, r, "error.internal", http.StatusInternalServerError)
		return
	}
	modeID := store.DefaultCatalogModeID
	if rawMode := r.URL.Query().Get("mode"); rawMode != "" {
		if id, parseErr := strconv.ParseInt(rawMode, 10, 64); parseErr == nil && id > 0 {
			for _, m := range modes {
				if m.ID == id {
					modeID = id
					break
				}
			}
		}
	}
	mode, err := s.store.CatalogMode(ctx, modeID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	comms, err := s.store.GetCommunities(ctx, modeID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	// Load categories and services for this mode to know their alphabetical positions.
	catalog, err := s.store.CatalogForMode(ctx, modeID, true)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	catNames := make([]string, 0, len(catalog))
	for name := range catalog {
		catNames = append(catNames, name)
	}
	sortStrings(catNames)

	view := communitiesView{Modes: modes, Mode: mode, Error: r.URL.Query().Get("error"), Saved: r.URL.Query().Get("saved")}
	groupIndex := 0
	for _, catName := range catNames {
		svcNames := catalog[catName]
		sortStrings(svcNames)
		// Auto group community is always (groupIndex+1)*10000

		group := communityGroupView{
			Category:  catName,
			Community: comms[catName],
			AutoGroup: uint32((groupIndex + 1) * 10000),
		}
		svcCounter := 0
		curGroup := groupIndex
		for _, svcName := range svcNames {
			group.Services = append(group.Services, communityServiceView{
				Name:      svcName,
				Community: comms[catName+"|"+svcName],
				AutoSvc:   store.AutoCommunity(curGroup, svcCounter),
			})
			svcCounter++
			if svcCounter >= 9999 {
				curGroup++
				svcCounter = 0
			}
		}
		groupIndex = curGroup + 1
		view.Groups = append(view.Groups, group)
	}
	lang, _ := requestLocale(r, s.defaultLang)
	s.renderAdmin(w, r, http.StatusOK, translate(lang, "communities.title"), "communities", view)
}

func (s *Server) saveCommunities(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		s.httpError(w, r, "error.bad_request", http.StatusBadRequest)
		return
	}
	modeID := store.DefaultCatalogModeID
	if rawMode := r.FormValue("mode"); rawMode != "" {
		if id, err := strconv.ParseInt(rawMode, 10, 64); err == nil && id > 0 {
			modeID = id
		}
	}
	updated := 0
	var saveErrors []string
	for key, values := range r.PostForm {
		if len(values) == 0 {
			continue
		}
		if !strings.HasPrefix(key, "cat_") && !strings.HasPrefix(key, "svc_") {
			continue
		}
		community64, err := strconv.ParseUint(values[0], 10, 32)
		if err != nil || community64 == 0 {
			continue
		}
		community := uint32(community64)
		if strings.HasPrefix(key, "cat_") {
			category := key[4:]
			if err := s.store.SetCommunity(ctx, modeID, category, "", community); err != nil {
				if strings.Contains(err.Error(), "already used") {
					saveErrors = append(saveErrors, category+": "+err.Error())
					continue
				}
				s.internalError(w, r, err)
				return
			}
			updated++
		} else {
			// svc_key = "svc_category|service"
			rest := key[4:]
			parts := strings.SplitN(rest, "|", 2)
			if len(parts) != 2 {
				continue
			}
			category := parts[0]
			service := parts[1]
			if err := s.store.SetCommunity(ctx, modeID, category, service, community); err != nil {
				if strings.Contains(err.Error(), "already used") {
					saveErrors = append(saveErrors, service+": "+err.Error())
					continue
				}
				s.internalError(w, r, err)
				return
			}
			updated++
		}
	}
	if len(saveErrors) > 0 {
		http.Redirect(w, r, fmt.Sprintf("/admin/communities?mode=%d&error=%s", modeID, url.QueryEscape(strings.Join(saveErrors, "; "))), http.StatusSeeOther)
		return
	}
	s.logAdminAction(r, "communities_update", fmt.Sprintf("mode=%d, updated=%d", modeID, updated))
	if err := s.bgp.Reconcile(r.Context()); err != nil {
		logger := logging.FromContext(r.Context())
		logger.Warn("reconcile after communities update failed", "error", err)
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/communities?mode=%d", modeID), http.StatusSeeOther)
}

func (s *Server) resetCommunities(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		s.httpError(w, r, "error.bad_request", http.StatusBadRequest)
		return
	}
	modeID := store.DefaultCatalogModeID
	if rawMode := r.FormValue("mode"); rawMode != "" {
		if id, err := strconv.ParseInt(rawMode, 10, 64); err == nil && id > 0 {
			modeID = id
		}
	}
	// Delete all communities for this mode.
	if _, err := s.store.DB.ExecContext(ctx, "DELETE FROM catalog_communities WHERE mode_id = ?", modeID); err != nil {
		s.internalError(w, r, err)
		return
	}
	// Regenerate from scratch.
	if _, err := s.store.GenerateCommunities(ctx, modeID); err != nil {
		s.internalError(w, r, err)
		return
	}
	if err := s.bgp.Reconcile(r.Context()); err != nil {
		logger := logging.FromContext(r.Context())
		logger.Warn("reconcile after reset communities failed", "error", err)
	}
	s.logAdminAction(r, "communities_reset", fmt.Sprintf("mode=%d", modeID))
	http.Redirect(w, r, fmt.Sprintf("/admin/communities?mode=%d&saved=reset", modeID), http.StatusSeeOther)
}

func (s *Server) saveGlobalFilters(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.httpError(w, r, "error.bad_request", http.StatusBadRequest)
		return
	}
	filters, err := routeFiltersFromForm(r)
	if err == nil {
		err = s.store.SetGlobalRouteFilters(r.Context(), filters)
	}
	if err == nil {
		err = s.bgp.Reconcile(r.Context())
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	// Gather stats
	categories, services, totalPrefixes, _ := s.store.Stats(ctx) //nolint:errcheck // best-effort stats for dashboard
	peerStates, _ := s.bgp.PeerStates(ctx)                       //nolint:errcheck // best-effort lookup for display
	connectedPeers := 0
	for _, state := range peerStates {
		if state == "ESTABLISHED" {
			connectedPeers++
		}
	}
	feeds, _ := s.store.Feeds(ctx, false) //nolint:errcheck // best-effort lookup for display
	enabledFeeds := 0
	for _, f := range feeds {
		if f.Enabled {
			enabledFeeds++
		}
	}
	users, _ := s.store.Users(ctx, false) //nolint:errcheck // best-effort lookup for display

	data := map[string]any{
		"Categories":     categories,
		"Services":       services,
		"TotalPrefixes":  totalPrefixes,
		"ConnectedPeers": connectedPeers,
		"TotalPeers":     len(peerStates),
		"EnabledFeeds":   enabledFeeds,
		"TotalFeeds":     len(feeds),
		"TotalUsers":     len(users),
	}

	s.renderAdmin(w, r, http.StatusOK, "Dashboard", "dashboard", data)
}

func (s *Server) debugPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	type debugPageView struct {
		CIDR   string
		Result *cidrDebugResult
		Modes  []store.CatalogMode
		ModeID int64
	}

	cidr := r.URL.Query().Get("cidr")
	modeID := store.DefaultCatalogModeID
	if mid, err := strconv.ParseInt(r.URL.Query().Get("mode"), 10, 64); err == nil && mid > 0 {
		modeID = mid
	}

	modes, _ := s.store.CatalogModes(ctx, false) //nolint:errcheck // best-effort lookup for display

	view := debugPageView{
		CIDR:   cidr,
		Modes:  modes,
		ModeID: modeID,
	}

	if cidr != "" {
		result, err := s.debugCIDR(ctx, cidr, modeID)
		if err == nil {
			view.Result = &result
		}
	}

	s.renderAdmin(w, r, http.StatusOK, "CIDR diagnostics", "debug", view)
}

func (s *Server) debugCIDRHandler(w http.ResponseWriter, r *http.Request) {
	modeID := store.DefaultCatalogModeID
	if rawMode := strings.TrimSpace(r.URL.Query().Get("mode")); rawMode != "" {
		var err error
		modeID, err = strconv.ParseInt(rawMode, 10, 64)
		if err != nil || modeID <= 0 {
			http.Error(w, "invalid catalog mode", http.StatusBadRequest)
			return
		}
	}
	result, err := s.debugCIDR(r.Context(), r.URL.Query().Get("cidr"), modeID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(result); err != nil {
		logger := logging.FromContext(r.Context())
		logger.Error("failed to encode CIDR debug response", "error", err)
	}
}
