package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/andrey-vk/wdbgp/internal/logging"
	"github.com/andrey-vk/wdbgp/internal/store"
)

func (s *Server) userPage(w http.ResponseWriter, r *http.Request) {
	clientIP := s.clientIP(r)

	// Try IP match first
	user, err := s.store.UserByIP(r.Context(), clientIP)

	if err != nil {
		// No IP match — try session-based auth for login-mode users
		sessionID := getUserSessionID(r, s.cfg.SessionSecret, time.Duration(s.cfg.SessionMaxAge)*time.Second)
		if sessionID > 0 {
			user, err = s.store.User(r.Context(), sessionID)
			if err == nil && user.Enabled && (user.WebAuth == "login" || user.WebAuth == "any") {
				// Fall through to serve page
			} else {
				s.render(w, r, http.StatusForbidden, "title.access_denied", "access-denied", clientIP)
				return
			}
		} else {
			s.render(w, r, http.StatusForbidden, "title.access_denied", "access-denied", clientIP)
			return
		}
	} else {
		// IP matched — but a valid session for a different user takes priority
		sessionID := getUserSessionID(r, s.cfg.SessionSecret, time.Duration(s.cfg.SessionMaxAge)*time.Second)
		if sessionID > 0 {
			if sessionUser, sessionErr := s.store.User(r.Context(), sessionID); sessionErr == nil && sessionUser.Enabled {
				if sessionUser.ID != user.ID && (sessionUser.WebAuth == "login" || sessionUser.WebAuth == "any") {
					user = sessionUser
				}
			}
		}
		// Check web_auth mode for the (possibly overridden) user
		switch user.WebAuth {
		case "network", "any":
			// Serve page
		case "login", "both":
			if !validUserSession(r, user.ID, s.cfg.SessionSecret, time.Duration(s.cfg.SessionMaxAge)*time.Second) {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
		}
	}

	user, err = s.userWithRequestedMode(r.Context(), r, user, user.CatalogEditable, false)
	if err != nil {
		s.httpError(w, r, "error.bad_mode_id", http.StatusBadRequest)
		return
	}
	view, err := s.selection(r.Context(), user, !user.SelectionLocked, false)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	// Add CSRF token to selection view for the template
	csrfToken := ""
	if tokenVal := r.Context().Value(csrfCtxKey{}); tokenVal != nil {
		csrfToken = tokenVal.(string)
	}
	view.CSRFToken = csrfToken
	view.Saved = r.URL.Query().Get("saved")
	view.SessionUser = validUserSession(r, user.ID, s.cfg.SessionSecret, time.Duration(s.cfg.SessionMaxAge)*time.Second)
	s.render(w, r, http.StatusOK, "title.selection", "selection", view)
}

func (s *Server) saveOwnSelection(w http.ResponseWriter, r *http.Request) {
	user, err := s.store.UserByIP(r.Context(), s.clientIP(r))
	if store.IsNotFound(err) {
		s.httpError(w, r, "error.forbidden", http.StatusForbidden)
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	// For credential-based users, require valid session
	if user.WebAuth == "login" || user.WebAuth == "both" {
		if !validUserSession(r, user.ID, s.cfg.SessionSecret, time.Duration(s.cfg.SessionMaxAge)*time.Second) {
			s.httpError(w, r, "error.forbidden", http.StatusForbidden)
			return
		}
	}
	modeID, err := formModeID(r, user.CatalogModeID)
	if err != nil {
		s.httpError(w, r, "error.bad_mode_id", http.StatusBadRequest)
		return
	}
	if user.SelectionLocked {
		if modeID == user.CatalogModeID {
			s.httpError(w, r, "error.selection_locked", http.StatusForbidden)
			return
		}
		if !user.CatalogEditable {
			s.httpError(w, r, "error.catalog_managed", http.StatusForbidden)
			return
		}
		err = s.store.SetUserCatalogMode(r.Context(), user.ID, modeID, true)
		if err == nil {
			err = s.bgp.Reconcile(r.Context())
		}
		if err != nil {
			logger := logging.FromContext(r.Context())
			logger.Error("failed to save catalog mode", "user_id", user.ID, "error", err)
			http.Redirect(w, r, "/?saved=0", http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/?saved=1", http.StatusSeeOther)
		return
	}
	categories, services, err := parseSelection(r)
	if err == nil && modeID != user.CatalogModeID && !user.CatalogEditable {
		s.httpError(w, r, "error.catalog_managed", http.StatusForbidden)
		return
	}
	if err == nil {
		err = s.store.SetUserCatalogModeSelection(
			r.Context(), user.ID, modeID, true, categories, services)
	}
	if err == nil {
		err = s.bgp.Reconcile(r.Context())
	}
	if err != nil {
		logger := logging.FromContext(r.Context())
		logger.Error("failed to save selection", "user_id", user.ID, "error", err)
		http.Redirect(w, r, "/?saved=0", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/?saved=1", http.StatusSeeOther)
}

func (s *Server) selectionCount(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	user, err := s.identifyUser(r)
	if err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	categories, services, err := selectionFromValues(r.Form)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	modeID := user.CatalogModeID
	if rawMode := r.FormValue("catalog_mode_id"); rawMode != "" {
		if id, parseErr := strconv.ParseInt(rawMode, 10, 64); parseErr == nil && id > 0 {
			modeID = id
		}
	}

	v4, v6, err := s.store.CountPrefixes(r.Context(), modeID, categories, services, user.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, "IPv4: <strong id=total-prefix-v4>%d</strong> pref. · IPv6: <strong id=total-prefix-v6>%d</strong> pref.", v4, v6)
}

func (s *Server) saveOwnFilters(w http.ResponseWriter, r *http.Request) {
	user, err := s.store.UserByIP(r.Context(), s.clientIP(r))
	if store.IsNotFound(err) {
		s.httpError(w, r, "error.forbidden", http.StatusForbidden)
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	// For credential-based users, require valid session
	if user.WebAuth == "login" || user.WebAuth == "both" {
		if !validUserSession(r, user.ID, s.cfg.SessionSecret, time.Duration(s.cfg.SessionMaxAge)*time.Second) {
			s.httpError(w, r, "error.forbidden", http.StatusForbidden)
			return
		}
	}
	if !user.FilterEditable {
		s.httpError(w, r, "error.filters_managed", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.httpError(w, r, "error.bad_request", http.StatusBadRequest)
		return
	}
	filters, err := routeFiltersFromForm(r)
	if err == nil {
		err = s.store.SetUserRouteFilterConfig(r.Context(), user.ID, r.FormValue("filter_mode"), filters)
	}
	if err == nil {
		err = s.bgp.Reconcile(r.Context())
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/?saved=1", http.StatusSeeOther)
}

func (s *Server) selection(ctx context.Context, user store.User, editable, admin bool) (selectionView, error) {
	modes, err := s.store.CatalogModes(ctx, !admin)
	if err != nil {
		return selectionView{}, err
	}
	if admin {
		// Administrators can inspect retained selections for disabled modes.
	} else if !containsMode(modes, user.CatalogModeID) {
		if current, currentErr := s.store.CatalogMode(ctx, user.CatalogModeID); currentErr == nil {
			modes = append(modes, current)
		}
	}
	catalog, err := s.store.CatalogForMode(ctx, user.CatalogModeID, admin)
	if err != nil {
		return selectionView{}, err
	}
	selectedCategories, selectedServices, err := s.store.UserModeSelection(
		ctx, user.ID, user.CatalogModeID)
	if err != nil {
		return selectionView{}, err
	}
	names := make([]string, 0, len(catalog))
	for name := range catalog {
		names = append(names, name)
	}
	sortStrings(names)
	userFilters, err := s.store.UserRouteFilters(ctx, user.ID)
	if err != nil {
		return selectionView{}, err
	}
	view := selectionView{
		User: user, Modes: modes, CanChangeMode: admin || user.CatalogEditable,
		Editable: editable, Admin: admin,
		Filters: filterView{
			AllowText: strings.Join(userFilters.Allow, "\n"),
			DenyText:  strings.Join(userFilters.Deny, "\n"),
			Override:  user.FilterOverride,
			Mode:      user.FilterMode,
			Editable:  admin || user.FilterEditable,
			Admin:     admin,
		},
	}
	comms, _ := s.store.GetCommunities(ctx, user.CatalogModeID)
	view.Communities = comms
	prefixCountsV4, prefixCountsV6, err := s.store.PrefixCounts(ctx, user.CatalogModeID)
	if err != nil {
		return selectionView{}, err
	}
	view.PrefixCountsV4 = prefixCountsV4
	view.PrefixCountsV6 = prefixCountsV6
	view.TotalPrefixesV4, view.TotalPrefixesV6, err = s.store.CountSelectionPrefixes(ctx, user.ID)
	if err != nil {
		return selectionView{}, err
	}
	categoryCountsV4, categoryCountsV6, err := s.store.CategoryPrefixCounts(ctx, user.CatalogModeID)
	if err != nil {
		view.CategoryCountsV4 = map[string]int{}
		view.CategoryCountsV6 = map[string]int{}
	} else {
		view.CategoryCountsV4 = categoryCountsV4
		view.CategoryCountsV6 = categoryCountsV6
	}
	for _, category := range names {
		categorySelected := selectedCategories[category]
		item := categoryView{Name: category, Selected: selectedCategories[category], PrefixCountV4: view.CategoryCountsV4[category], PrefixCountV6: view.CategoryCountsV6[category]}
		if categorySelected {
			view.SelectedCategoryCount++
			view.SelectedCoveredServices += len(catalog[category])
		}
		for _, service := range catalog[category] {
			key := store.ServiceKey{Category: category, Service: service}
			if !categorySelected && selectedServices[key] {
				view.SelectedServiceCount++
			}
			svcPrefixCountV4 := 0
			svcPrefixCountV6 := 0
			if svcCounts, ok := prefixCountsV4[category]; ok {
				svcPrefixCountV4 = svcCounts[service]
			}
			if svcCounts, ok := prefixCountsV6[category]; ok {
				svcPrefixCountV6 = svcCounts[service]
			}
			item.Services = append(item.Services, serviceView{
				Name: service, Value: serviceValue(category, service),
				Selected: !categorySelected && selectedServices[key],
				Disabled: categorySelected,
				PrefixCountV4: svcPrefixCountV4,
				PrefixCountV6: svcPrefixCountV6,
			})
		}
		view.Categories = append(view.Categories, item)
	}
	return view, nil
}

func (s *Server) userWithRequestedMode(
	ctx context.Context,
	r *http.Request,
	user store.User,
	canChange bool,
	admin bool,
) (store.User, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("mode"))
	if raw == "" {
		return user, nil
	}
	if !canChange {
		return user, fmt.Errorf("catalog mode is managed by the administrator")
	}
	modeID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || modeID <= 0 {
		return store.User{}, fmt.Errorf("invalid catalog mode")
	}
	mode, err := s.store.CatalogMode(ctx, modeID)
	if err != nil {
		return store.User{}, err
	}
	if !admin && !mode.Enabled {
		return store.User{}, fmt.Errorf("catalog mode is disabled")
	}
	user.CatalogModeID = mode.ID
	user.CatalogModeName = mode.Name
	return user, nil
}

func containsMode(modes []store.CatalogMode, id int64) bool {
	for _, mode := range modes {
		if mode.ID == id {
			return true
		}
	}
	return false
}

func parseSelection(r *http.Request) ([]string, []store.ServiceKey, error) {
	if err := r.ParseForm(); err != nil {
		return nil, nil, err
	}
	return selectionFromValues(r.Form)
}

func selectionFromValues(values url.Values) ([]string, []store.ServiceKey, error) {
	categories := uniqueNonEmpty(values["category"])
	categorySelected := map[string]bool{}
	for _, category := range categories {
		categorySelected[category] = true
	}
	var services []store.ServiceKey
	seen := map[store.ServiceKey]bool{}
	for _, value := range values["service"] {
		parts := strings.SplitN(value, ":", 2)
		if len(parts) != 2 {
			return nil, nil, fmt.Errorf("invalid service selection")
		}
		category, err := url.QueryUnescape(parts[0])
		if err != nil {
			return nil, nil, err
		}
		service, err := url.QueryUnescape(parts[1])
		if err != nil {
			return nil, nil, err
		}
		key := store.ServiceKey{Category: category, Service: service}
		if category != "" && service != "" && !categorySelected[category] && !seen[key] {
			seen[key] = true
			services = append(services, key)
		}
	}
	return categories, services, nil
}
