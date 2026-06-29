package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/andrey-vk/wdbgp/internal/logging"
	"github.com/andrey-vk/wdbgp/internal/store"
)

// userCtxKey is the context key for the authenticated user ID.
type userCtxKey struct{}

// requireUser is middleware that authenticates a user by source IP or session cookie.
// Sets user ID in context. Returns 401 JSON on failure.
func (s *Server) requireUser(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		clientIP := s.clientIP(r)

		// Try source IP match
		ipUser, ipErr := s.store.UserByIP(ctx, clientIP)
		ipMatch := ipErr == nil && ipUser.Enabled

		// Try session cookie
		sessionID := getUserSessionID(r, s.cfg.SessionSecret, time.Duration(s.cfg.SessionMaxAge)*time.Second)
		var cookieMatch bool
		var cookieUser store.User
		if sessionID > 0 {
			cookieUser, cookieErr := s.store.User(ctx, sessionID)
			cookieMatch = cookieErr == nil && cookieUser.Enabled
		}

		// Authorize based on web_auth mode.
		// Cookie-authenticated users take priority over IP-matched users.
		switch {
		case cookieMatch && (cookieUser.WebAuth == "login" || cookieUser.WebAuth == "any"):
			r = r.WithContext(context.WithValue(ctx, userCtxKey{}, cookieUser))
		case ipMatch && cookieMatch &&
			ipUser.WebAuth == "both" && ipUser.ID == cookieUser.ID:
			r = r.WithContext(context.WithValue(ctx, userCtxKey{}, ipUser))
		case ipMatch && ipUser.WebAuth == "network":
			r = r.WithContext(context.WithValue(ctx, userCtxKey{}, ipUser))
		case ipMatch && ipUser.WebAuth == "any":
			r = r.WithContext(context.WithValue(ctx, userCtxKey{}, ipUser))
		default:
			writeJSON(w, http.StatusUnauthorized, apiResponse{OK: false, Error: "Authentication required"})
			return
		}

		next(w, r)
	}
}

// requireUser extracts the authenticated user from the request context.
// Writes a 401 JSON response and returns false if the user is not authenticated.
func requireUser(w http.ResponseWriter, r *http.Request) (*store.User, bool) {
	u, ok := r.Context().Value(userCtxKey{}).(store.User)
	if !ok || u.ID == 0 {
		writeJSON(w, http.StatusUnauthorized, apiResponse{OK: false, Error: "not authenticated"})
		return nil, false
	}
	return &u, true
}

// userPublic returns a subset of user fields safe for JSON responses (no password).
type userPublic struct {
	ID              int64    `json:"id"`
	Name            string   `json:"name"`
	CatalogModeID   int64    `json:"catalog_mode_id"`
	CatalogModeName string   `json:"catalog_mode_name"`
	SelectionLocked bool     `json:"selection_locked"`
	FilterEditable  bool     `json:"filter_editable"`
	FilterOverride  bool     `json:"filter_override"`
	FilterMode      string   `json:"filter_mode"`
	CatalogEditable bool     `json:"catalog_editable"`
	Networks        []string `json:"networks"`
}

// apiUserMe handles GET /api/user/me.
// Returns the authenticated user's full data: user info, catalog, selections, communities, prefix counts, filters.
func (s *Server) apiUserMe(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := requireUser(w, r)
	if !ok {
		return
	}

	// Catalog
	catalog, err := s.store.CatalogForMode(ctx, user.CatalogModeID, false)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}

	// Selections — only include items visible in the catalog (enabled feeds)
	categories, services, err := s.store.UserModeSelection(ctx, user.ID, user.CatalogModeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}
	// Build sets of visible categories/services from catalog
	visibleCats := make(map[string]bool)
	visibleSvcs := make(map[string]bool)
	for cat, svcList := range catalog {
		visibleCats[cat] = true
		for _, svc := range svcList {
			visibleSvcs[cat+"|"+svc] = true
		}
	}
	catList := make([]string, 0)
	for c := range categories {
		if visibleCats[c] {
			catList = append(catList, c)
		}
	}
	svcList := make([]store.ServiceKey, 0)
	for k := range services {
		if visibleSvcs[k.Category+"|"+k.Service] {
			svcList = append(svcList, k)
		}
	}

	// Communities
	communities, err := s.store.GetCommunities(ctx, user.CatalogModeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}

	// Prefix counts
	v4Prefixes, v6Prefixes, err := s.store.PrefixCounts(ctx, user.CatalogModeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}

	// Filters
	filters, err := s.store.UserRouteFilters(ctx, user.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}

	// Modes list (for catalog mode selector)
	modes, err := s.store.CatalogModes(r.Context(), false)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"user": userPublic{
			ID:              user.ID,
			Name:            user.Name,
			CatalogModeID:   user.CatalogModeID,
			CatalogModeName: user.CatalogModeName,
			SelectionLocked: user.SelectionLocked,
			FilterEditable:  user.FilterEditable,
			FilterOverride:  user.FilterOverride,
			FilterMode:      user.FilterMode,
			CatalogEditable: user.CatalogEditable,
			Networks:        user.Networks,
		},
		"catalog":       catalog,
		"selections":    map[string]any{"categories": catList, "services": svcList},
		"communities":   communities,
		"prefix_counts": map[string]any{"v4": v4Prefixes, "v6": v6Prefixes},
		"filters":       filters,
		"modes":         modes,
	})
}

// apiUserLogin handles POST /api/user/login.
// Authenticates a user by login credentials and sets a session cookie.
func (s *Server) apiUserLogin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Rate limiting
	clientIP := s.clientIP(r)
	if !s.loginLimiter.allow(clientIP) {
		writeJSON(w, http.StatusTooManyRequests, apiResponse{OK: false, Error: "Rate limit exceeded"})
		return
	}

	var body struct {
		Login    string `json:"login"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid request body"})
		return
	}

	if body.Login == "" || body.Password == "" {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Login and password required"})
		return
	}

	user, err := s.store.AuthenticateUser(ctx, body.Login, body.Password)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, apiResponse{OK: false, Error: "Invalid credentials"})
		return
	}

	// Check web_auth allows login
	if user.WebAuth != "login" && user.WebAuth != "any" && user.WebAuth != "both" {
		writeJSON(w, http.StatusUnauthorized, apiResponse{OK: false, Error: "Login not allowed for this user"})
		return
	}

	// For "both" mode, verify client IP matches the authenticated user's networks.
	if user.WebAuth == "both" {
		ipUser, ipErr := s.store.UserByIP(ctx, clientIP)
		if ipErr != nil || ipUser.ID != user.ID {
			writeJSON(w, http.StatusUnauthorized, apiResponse{OK: false, Error: "Authentication required"})
			return
		}
	}

	// Set session cookie
	secure := s.adminCookieSecure(r)
	maxAge := s.cfg.SessionMaxAge
	if maxAge <= 0 {
		maxAge = 28800 // 8 hours default
	}
	setUserSessionCookie(w, user.ID, s.cfg.SessionSecret, maxAge, secure)

	// Load catalog and filtered selections for the response
	loginCatalog, err := s.store.CatalogForMode(ctx, user.CatalogModeID, false)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}
	loginCategories, loginServices, err := s.store.UserModeSelection(ctx, user.ID, user.CatalogModeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}
	visibleCats := make(map[string]bool)
	visibleSvcs := make(map[string]bool)
	for cat, svcList := range loginCatalog {
		visibleCats[cat] = true
		for _, svc := range svcList {
			visibleSvcs[cat+"|"+svc] = true
		}
	}
	loginCatList := make([]string, 0)
	for c := range loginCategories {
		if visibleCats[c] {
			loginCatList = append(loginCatList, c)
		}
	}
	loginSvcList := make([]store.ServiceKey, 0)
	for k := range loginServices {
		if visibleSvcs[k.Category+"|"+k.Service] {
			loginSvcList = append(loginSvcList, k)
		}
	}
	loginFilters, err := s.store.UserRouteFilters(ctx, user.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}

	// Prefix counts (same as /api/user/me)
	v4Prefixes, v6Prefixes, err := s.store.PrefixCounts(ctx, user.CatalogModeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}

	// Modes list (for catalog mode selector)
	modes, err := s.store.CatalogModes(r.Context(), false)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"user": userPublic{
			ID:              user.ID,
			Name:            user.Name,
			CatalogModeID:   user.CatalogModeID,
			CatalogModeName: user.CatalogModeName,
			SelectionLocked: user.SelectionLocked,
			FilterEditable:  user.FilterEditable,
			FilterOverride:  user.FilterOverride,
			FilterMode:      user.FilterMode,
			CatalogEditable: user.CatalogEditable,
			Networks:        user.Networks,
		},
		"catalog":       loginCatalog,
		"selections":    map[string]any{"categories": loginCatList, "services": loginSvcList},
		"filters":       loginFilters,
		"prefix_counts": map[string]any{"v4": v4Prefixes, "v6": v6Prefixes},
		"modes":         modes,
	})
}

// apiUserLogout handles POST /api/user/logout.
// Clears the user session cookie.
func (s *Server) apiUserLogout(w http.ResponseWriter, r *http.Request) {
	secure := r.TLS != nil
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure determined at runtime
		Name:     "wdbgp_user",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
	writeJSON(w, http.StatusOK, apiResponse{OK: true})
}

// apiUserSaveSelections handles POST /api/user/selections.
// Saves the user's category and service selections, then reconciles BGP.
// Only allowed if the user is not selection_locked.
func (s *Server) apiUserSaveSelections(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := requireUser(w, r)
	if !ok {
		return
	}

	if user.SelectionLocked {
		writeJSON(w, http.StatusForbidden, apiResponse{OK: false, Error: "Selections are locked for this user"})
		return
	}

	var body struct {
		Categories []struct {
			Category string `json:"category"`
			Checked  bool   `json:"checked"`
		} `json:"categories"`
		Services []struct {
			Category string `json:"category"`
			Service  string `json:"service"`
			Checked  bool   `json:"checked"`
		} `json:"services"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid request body"})
		return
	}

	err := s.store.Transaction(ctx, func(tx *sql.Tx) error {
		for _, c := range body.Categories {
			if c.Checked {
				if _, err := tx.ExecContext(ctx,
					"INSERT OR IGNORE INTO selected_categories(user_id, mode_id, category) VALUES (?, ?, ?)",
					user.ID, user.CatalogModeID, c.Category); err != nil {
					return err
				}
			} else {
				if _, err := tx.ExecContext(ctx,
					"DELETE FROM selected_categories WHERE user_id = ? AND mode_id = ? AND category = ?",
					user.ID, user.CatalogModeID, c.Category); err != nil {
					return err
				}
			}
		}
		for _, s := range body.Services {
			if s.Checked {
				if _, err := tx.ExecContext(ctx,
					"INSERT OR IGNORE INTO selected_services(user_id, mode_id, category, service) VALUES (?, ?, ?, ?)",
					user.ID, user.CatalogModeID, s.Category, s.Service); err != nil {
					return err
				}
			} else {
				if _, err := tx.ExecContext(ctx,
					"DELETE FROM selected_services WHERE user_id = ? AND mode_id = ? AND category = ? AND service = ?",
					user.ID, user.CatalogModeID, s.Category, s.Service); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}

	if s.bgp != nil {
		if err := s.bgp.Reconcile(ctx); err != nil {
			writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Selection saved but BGP reconciliation failed: " + err.Error()})
			return
		}
	}

	writeJSON(w, http.StatusOK, apiResponse{OK: true})
}

// apiUserSaveFilters handles POST /api/user/filters.
// Saves the user's route filters, then reconciles BGP.
// Only allowed if filter_editable is enabled for the user.
func (s *Server) apiUserSaveFilters(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := requireUser(w, r)
	if !ok {
		return
	}

	if !user.FilterEditable {
		writeJSON(w, http.StatusForbidden, apiResponse{OK: false, Error: "Filter editing is not allowed for this user"})
		return
	}

	var body store.RouteFilters
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid request body"})
		return
	}

	if err := s.store.SetUserRouteFilters(ctx, user.ID, body); err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}

	if s.bgp != nil {
		if err := s.bgp.Reconcile(ctx); err != nil {
			writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Filters saved but BGP reconciliation failed: " + err.Error()})
			return
		}
	}

	writeJSON(w, http.StatusOK, apiResponse{OK: true})
}

// apiUserCountPrefixes handles POST /api/user/count-prefixes.
// Returns the number of v4/v6 prefixes that would be announced for a given selection,
// along with deltas compared to the user's currently saved selections.
func (s *Server) apiUserCountPrefixes(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := requireUser(w, r)
	if !ok {
		return
	}

	var body struct {
		Categories []struct {
			Category string `json:"category"`
			Checked  bool   `json:"checked"`
		} `json:"categories"`
		Services []struct {
			Category string `json:"category"`
			Service  string `json:"service"`
			Checked  bool   `json:"checked"`
		} `json:"services"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid request body"})
		return
	}

	// Extract checked items for prefix counting
	catList := make([]string, 0)
	for _, c := range body.Categories {
		if c.Checked {
			catList = append(catList, c.Category)
		}
	}
	svcList := make([]store.ServiceKey, 0)
	for _, s := range body.Services {
		if s.Checked {
			svcList = append(svcList, store.ServiceKey{Category: s.Category, Service: s.Service})
		}
	}

	// Count prefixes for the proposed selection
	newV4, newV6, err := s.store.CountPrefixes(ctx, user.CatalogModeID, catList, svcList, user.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}

	// Load current saved selections
	curCats, curSvcs, err := s.store.UserModeSelection(ctx, user.ID, user.CatalogModeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}

	// Convert current selections to slices
	curCatList := make([]string, 0, len(curCats))
	for c := range curCats {
		curCatList = append(curCatList, c)
	}
	curSvcList := make([]store.ServiceKey, 0, len(curSvcs))
	for k := range curSvcs {
		curSvcList = append(curSvcList, k)
	}

	// Count prefixes for current selections
	curV4, curV6, err := s.store.CountPrefixes(ctx, user.CatalogModeID, curCatList, curSvcList, user.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"v4":       newV4,
		"v6":       newV6,
		"delta_v4": newV4 - curV4,
		"delta_v6": newV6 - curV6,
	})
}

// apiUserSwitchMode handles PUT /api/user/mode.
// Changes the user's catalog mode. Only allowed if catalog_editable.
func (s *Server) apiUserSwitchMode(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	if !user.CatalogEditable {
		writeJSON(w, http.StatusForbidden, apiResponse{OK: false, Error: "Catalog mode switching is disabled for this user"})
		return
	}
	var body struct {
		ModeID int64 `json:"mode_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid request body"})
		return
	}
	if body.ModeID <= 0 {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid mode ID"})
		return
	}
	// Verify mode exists and is enabled
	mode, err := s.store.CatalogMode(r.Context(), body.ModeID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Catalog mode not found"})
		return
	}
	if !mode.Enabled {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Catalog mode is disabled"})
		return
	}
	// Update user's catalog_mode_id
	err = s.store.Transaction(r.Context(), func(tx *sql.Tx) error {
		_, execErr := tx.ExecContext(r.Context(),
			"UPDATE users SET catalog_mode_id = ? WHERE id = ?", body.ModeID, user.ID)
		return execErr
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Failed to update catalog mode"})
		return
	}
	if s.bgp != nil {
		if err := s.bgp.Reconcile(r.Context()); err != nil {
			logging.FromContext(r.Context()).Debug("bgp reconcile failed after mode switch", "error", err)
		}
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
