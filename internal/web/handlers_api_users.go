package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"sort"
	"strconv"
	"strings"

	"github.com/andrey-vk/wdbgp/internal/logging"
	"github.com/andrey-vk/wdbgp/internal/store"
)

var validWebAuthModes = map[string]bool{"network": true, "login": true, "both": true, "any": true}

func isValidWebAuth(mode string) bool {
	return validWebAuthModes[mode]
}

// isActiveWebAuth reports whether a web_auth mode uses IP-based resolution
// for identifying the user — everything except "login". Must name the same
// three values as store.activeWebAuthModesSQL (the SQL-side equivalent used
// by UserByIP and ActiveNetworksOverlap).
func isActiveWebAuth(mode string) bool {
	return mode == "network" || mode == "both" || mode == "any"
}

// validateNetworksNormalized rejects a networks list that isn't already the
// minimal, masked, deduplicated, fully-merged form store.AggregateNetworks
// would produce — same rule the admin UI enforces before allowing a save,
// checked again here so it holds regardless of how the request was made.
// Order and whitespace don't matter; every other difference does.
func validateNetworksNormalized(networks []string) error {
	if len(networks) == 0 {
		return nil
	}
	trimmed := make([]string, len(networks))
	for i, n := range networks {
		trimmed[i] = strings.TrimSpace(n)
	}
	aggregated, err := store.AggregateNetworks(trimmed)
	if err != nil {
		return err
	}
	if !sameStringSet(trimmed, aggregated) {
		return fmt.Errorf("networks must be normalized with no overlapping or adjacent ranges; expected: %s", strings.Join(aggregated, ", "))
	}
	return nil
}

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	sortedA := append([]string(nil), a...)
	sortedB := append([]string(nil), b...)
	sort.Strings(sortedA)
	sort.Strings(sortedB)
	for i := range sortedA {
		if sortedA[i] != sortedB[i] {
			return false
		}
	}
	return true
}

type userJSON struct {
	ID              int64    `json:"id"`
	Name            string   `json:"name"`
	PeerIP          string   `json:"peer_ip"`
	PeerASN         uint32   `json:"peer_asn"`
	NextHop         string   `json:"next_hop"`
	HasPassword     bool     `json:"has_password"`
	SelectionLocked bool     `json:"selection_locked"`
	Enabled         bool     `json:"enabled"`
	FilterOverride  bool     `json:"filter_override"`
	FilterMode      string   `json:"filter_mode"`
	FilterEditable  bool     `json:"filter_editable"`
	CatalogModeID   int64    `json:"catalog_mode_id"`
	CatalogModeName string   `json:"catalog_mode_name"`
	CatalogEditable bool     `json:"catalog_editable"`
	ActiveDial      bool     `json:"active_dial"`
	WebAuth         string   `json:"web_auth"`
	Networks        []string `json:"networks"`
	PeerState       string   `json:"peer_state,omitempty"`
	FilterAllow     []string `json:"filter_allow"`
	FilterDeny      []string `json:"filter_deny"`
}

type userCredentialJSON struct {
	Login string `json:"login"`
}

func userToJSON(u store.User, peerState string) userJSON {
	return userJSON{
		ID:              u.ID,
		Name:            u.Name,
		PeerIP:          u.PeerIP,
		PeerASN:         u.PeerASN,
		NextHop:         u.NextHop,
		HasPassword:     u.BGPPassword != "",
		SelectionLocked: u.SelectionLocked,
		Enabled:         u.Enabled,
		FilterOverride:  u.FilterOverride,
		FilterMode:      u.FilterMode,
		FilterEditable:  u.FilterEditable,
		CatalogModeID:   u.CatalogModeID,
		CatalogModeName: u.CatalogModeName,
		CatalogEditable: u.CatalogEditable,
		ActiveDial:      u.ActiveDial,
		WebAuth:         u.WebAuth,
		Networks:        u.Networks,
		PeerState:       peerState,
	}
}

func credentialsToJSON(creds []store.UserCredential) []userCredentialJSON {
	result := make([]userCredentialJSON, len(creds))
	for i, c := range creds {
		result[i] = userCredentialJSON{Login: c.Login}
	}
	return result
}

func (s *Server) userPeerState(ctx context.Context, u store.User) string {
	if s.bgp == nil {
		return ""
	}
	peerStates, _ := s.bgp.PeerStates(ctx) //nolint:errcheck // best-effort lookup for display
	peerKey := fmt.Sprintf("%s:%d", u.PeerIP, u.PeerASN)
	return peerStates[peerKey]
}

// apiUsersNormalizeNetworks handles POST /api/admin/users/normalize-networks.
// Given a raw list of CIDRs, returns the minimal equivalent set — masked,
// deduplicated, and with overlapping/adjacent entries merged. Used by the
// admin UI to preview the auto-fix transform and to decide whether the
// current input already matches it (see store.AggregateNetworks); the
// authoritative check happens again at save time in apiUsersCreate/Update.
func (s *Server) apiUsersNormalizeNetworks(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Networks []string `json:"networks"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid request body"})
		return
	}
	networks, err := store.AggregateNetworks(body.Networks)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"networks": networks})
}

// apiUsersList handles GET /api/admin/users.
func (s *Server) apiUsersList(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.Users(r.Context(), false)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Failed to load users"})
		return
	}

	// Get peer states for status display
	var peerStates map[string]string
	if s.bgp != nil {
		peerStates, _ = s.bgp.PeerStates(r.Context()) //nolint:errcheck // best-effort lookup for display
	}

	result := make([]userJSON, len(users))
	for i, u := range users {
		peerKey := fmt.Sprintf("%s:%d", u.PeerIP, u.PeerASN)
		state := ""
		if peerStates != nil {
			state = peerStates[peerKey]
		}
		j := userToJSON(u, state)
		filters, err := s.store.UserRouteFilters(r.Context(), u.ID)
		if err != nil {
			logging.FromContext(r.Context()).Debug("route filters lookup in list failed", "error", err, "user_id", u.ID)
		} else {
			j.FilterAllow = filters.Allow
			j.FilterDeny = filters.Deny
		}
		result[i] = j
	}

	writeJSON(w, http.StatusOK, map[string]any{"users": result})
}

// apiUsersGet handles GET /api/admin/users/{id}.
func (s *Server) apiUsersGet(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid user ID"})
		return
	}
	u, err := s.store.User(r.Context(), id)
	if store.IsNotFound(err) {
		writeJSON(w, http.StatusNotFound, apiResponse{OK: false, Error: "User not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Failed to load user"})
		return
	}

	state := s.userPeerState(r.Context(), u)
	j := userToJSON(u, state)
	filters, err := s.store.UserRouteFilters(r.Context(), u.ID)
	if err != nil {
		logging.FromContext(r.Context()).Debug("route filters lookup failed", "error", err, "user_id", u.ID)
		// Return user without filter data — graceful fallback
	} else {
		j.FilterAllow = filters.Allow
		j.FilterDeny = filters.Deny
	}
	writeJSON(w, http.StatusOK, j)
}

// apiUsersCreate handles POST /api/admin/users.
func (s *Server) apiUsersCreate(w http.ResponseWriter, r *http.Request) {
	extendRequestDeadlines(w, r) // large filter upload + reconcile can outlive Read/WriteTimeout
	var body struct {
		Name            string   `json:"name"`
		PeerIP          string   `json:"peer_ip"`
		PeerASN         uint32   `json:"peer_asn"`
		NextHop         string   `json:"next_hop"`
		BGPPassword     string   `json:"bgp_password"`
		PasswordEnabled bool     `json:"password_enabled"`
		SelectionLocked bool     `json:"selection_locked"`
		Enabled         bool     `json:"enabled"`
		FilterOverride  bool     `json:"filter_override"`
		FilterMode      string   `json:"filter_mode"`
		FilterEditable  bool     `json:"filter_editable"`
		CatalogModeID   int64    `json:"catalog_mode_id"`
		CatalogEditable bool     `json:"catalog_editable"`
		ActiveDial      bool     `json:"active_dial"`
		WebAuth         string   `json:"web_auth"`
		Networks        []string `json:"networks"`
		FilterAllow     []string `json:"filter_allow"`
		FilterDeny      []string `json:"filter_deny"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid request body"})
		return
	}

	if body.Name == "" {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Name is required"})
		return
	}
	if body.PeerIP == "" || !isValidIP(body.PeerIP) {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid peer IP address"})
		return
	}
	// buildRoute silently falls back to the default next hop for anything
	// that fails netip.ParseAddr, so a typo here would otherwise be accepted
	// and then quietly ignored at BGP-route-build time instead of erroring.
	if body.NextHop != "" && !isValidIP(body.NextHop) {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid next hop address"})
		return
	}
	for _, raw := range body.Networks {
		if _, err := netip.ParsePrefix(strings.TrimSpace(raw)); err != nil {
			writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid network CIDR: " + raw})
			return
		}
	}

	if body.PeerASN == 0 {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Peer ASN is required"})
		return
	}

	// Handle BGP password with explicit toggle logic.
	if body.PasswordEnabled {
		if body.BGPPassword == "" {
			writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Password must be set to enable"})
			return
		}
	} else {
		if body.BGPPassword != "" {
			writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Cannot set password while disabling"})
			return
		}
		body.BGPPassword = ""
	}

	// Validate web_auth mode
	if body.WebAuth != "" && !isValidWebAuth(body.WebAuth) {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid web_auth mode"})
		return
	}

	user := store.User{
		Name:            body.Name,
		PeerIP:          body.PeerIP,
		PeerASN:         body.PeerASN,
		NextHop:         body.NextHop,
		BGPPassword:     body.BGPPassword,
		SelectionLocked: body.SelectionLocked,
		Enabled:         body.Enabled,
		FilterOverride:  body.FilterOverride,
		FilterMode:      body.FilterMode,
		FilterEditable:  body.FilterEditable,
		CatalogModeID:   body.CatalogModeID,
		CatalogEditable: body.CatalogEditable,
		ActiveDial:      body.ActiveDial,
		WebAuth:         body.WebAuth,
		Networks:        body.Networks,
	}

	// Apply defaults
	if user.CatalogModeID == 0 {
		user.CatalogModeID = store.DefaultCatalogModeID
	}
	if user.WebAuth == "" {
		user.WebAuth = s.settings.DefaultWebAuth.Get()
	}

	// Reject a mode ID that doesn't reference an existing, enabled catalog
	// mode — same check apiUserSwitchMode enforces for a user's own
	// self-service mode switch. Without this, a nonexistent ID surfaces as
	// a raw FK-violation 500 instead of a clean 400, and a disabled mode is
	// silently accepted.
	if mode, err := s.store.CatalogMode(r.Context(), user.CatalogModeID); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Catalog mode not found"})
		return
	} else if !mode.Enabled {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Catalog mode is disabled"})
		return
	}

	// Only network/both actually authenticate by IP match — login and any
	// (credentials, optionally combined with IP for "both") don't need one.
	// Checked against the effective (post-default) mode, not the raw
	// request field, so an omitted web_auth defaulting to "login"/"any"
	// doesn't get wrongly rejected here.
	if (user.WebAuth == "network" || user.WebAuth == "both") && len(user.Networks) == 0 {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "At least one network is required"})
		return
	}

	// A user whose (effective, post-default) web_auth actually uses IP for
	// identification must have networks already in normalized form (see
	// validateNetworksNormalized) — this is purely about the submitted
	// data's own internal validity, so it's checked here regardless of
	// other users. login-mode users are exempt: their networks are inert.
	if isActiveWebAuth(user.WebAuth) {
		if err := validateNetworksNormalized(user.Networks); err != nil {
			writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: err.Error()})
			return
		}
	}

	// Reject dynamic peers when feature flag is off
	if !s.settings.AllowDynamicPeers.Get() && (user.PeerIP == "0.0.0.0" || user.PeerIP == "::") {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Dynamic peers are disabled"})
		return
	}

	// Dynamic peers cannot have active dial (can't dial a wildcard address)
	if (user.PeerIP == "0.0.0.0" || user.PeerIP == "::") && user.ActiveDial {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Dynamic peers cannot use active dial (wildcard IP cannot be dialed)"})
		return
	}

	// Validate peer uniqueness before the cross-user network overlap check
	// below — a duplicate peer identity is a more fundamental conflict than
	// an overlapping web-auth network, so it should surface first.
	if err := s.validatePeerUniqueness(r.Context(), user, 0); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: err.Error()})
		return
	}

	// Must not overlap with another active (network/both/any) user's
	// networks — otherwise UserByIP's resolution becomes ambiguous.
	if isActiveWebAuth(user.WebAuth) {
		if err := s.store.ActiveNetworksOverlap(r.Context(), user.Networks, 0); err != nil {
			writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: err.Error()})
			return
		}
	}

	// Validate route filters before creating the user — otherwise a
	// malformed CIDR here would be caught by SetUserRouteFilters only after
	// AddUser already committed the row, leaving a filterless user behind
	// despite the request having failed.
	if len(body.FilterAllow) > 0 || len(body.FilterDeny) > 0 {
		if _, err := store.NormalizeRouteFilters(store.RouteFilters{Allow: body.FilterAllow, Deny: body.FilterDeny}); err != nil {
			writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: err.Error()})
			return
		}
	}

	userID, err := s.store.AddUser(r.Context(), user)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}
	user.ID = userID

	// Save route filters
	if len(body.FilterAllow) > 0 || len(body.FilterDeny) > 0 {
		if err := s.store.SetUserRouteFilters(r.Context(), userID, store.RouteFilters{
			Allow: body.FilterAllow,
			Deny:  body.FilterDeny,
		}); err != nil {
			logging.FromContext(r.Context()).Debug("route filters save after create failed", "error", err, "user_id", userID)
			writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Failed to save route filters"})
			return
		}
	}

	if s.bgp != nil && user.Enabled {
		if err := s.bgp.AddPeer(r.Context(), user); err != nil {
			writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
			return
		}
		if err := s.bgp.Reconcile(r.Context()); err != nil {
			logging.FromContext(r.Context()).Debug("bgp reconcile failed after user create", "error", err)
		}
	}

	var peerStates map[string]string
	if s.bgp != nil {
		peerStates, _ = s.bgp.PeerStates(r.Context()) //nolint:errcheck // best-effort
	}
	s.store.RecordUserSnapshot(r.Context(), s.settings.MetricsEnabled.Get(), peerStates)

	// Reload to get full data (CatalogModeName, Networks, etc.)
	created, _ := s.store.User(r.Context(), userID) //nolint:errcheck // just created, must exist
	state := s.userPeerState(r.Context(), created)
	j := userToJSON(created, state)
	filters, err := s.store.UserRouteFilters(r.Context(), userID)
	if err != nil {
		logging.FromContext(r.Context()).Debug("route filters lookup after create failed", "error", err, "user_id", userID)
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Failed to read route filters"})
		return
	}
	j.FilterAllow = filters.Allow
	j.FilterDeny = filters.Deny
	writeJSON(w, http.StatusCreated, j)
}

// apiUsersUpdate handles PUT /api/admin/users/{id}.
func (s *Server) apiUsersUpdate(w http.ResponseWriter, r *http.Request) {
	extendRequestDeadlines(w, r) // large filter upload + reconcile can outlive Read/WriteTimeout
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid user ID"})
		return
	}

	var body struct {
		Name            *string   `json:"name"`
		PeerIP          *string   `json:"peer_ip"`
		PeerASN         *uint32   `json:"peer_asn"`
		NextHop         *string   `json:"next_hop"`
		BGPPassword     *string   `json:"bgp_password"`
		PasswordEnabled *bool     `json:"password_enabled"`
		SelectionLocked *bool     `json:"selection_locked"`
		Enabled         *bool     `json:"enabled"`
		FilterOverride  *bool     `json:"filter_override"`
		FilterMode      *string   `json:"filter_mode"`
		FilterEditable  *bool     `json:"filter_editable"`
		CatalogModeID   *int64    `json:"catalog_mode_id"`
		CatalogEditable *bool     `json:"catalog_editable"`
		ActiveDial      *bool     `json:"active_dial"`
		WebAuth         *string   `json:"web_auth"`
		Networks        *[]string `json:"networks"`
		FilterAllow     *[]string `json:"filter_allow"`
		FilterDeny      *[]string `json:"filter_deny"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid request body"})
		return
	}

	// Load current user to merge partial updates
	current, err := s.store.User(r.Context(), id)
	if store.IsNotFound(err) {
		writeJSON(w, http.StatusNotFound, apiResponse{OK: false, Error: "User not found"})
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
	if body.PeerIP != nil {
		if *body.PeerIP == "" || !isValidIP(*body.PeerIP) {
			writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid peer IP address"})
			return
		}
		current.PeerIP = *body.PeerIP
	}
	if body.PeerASN != nil {
		current.PeerASN = *body.PeerASN
	}
	if body.NextHop != nil {
		if *body.NextHop != "" && !isValidIP(*body.NextHop) {
			writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid next hop address"})
			return
		}
		current.NextHop = *body.NextHop
	}
	if body.SelectionLocked != nil {
		current.SelectionLocked = *body.SelectionLocked
	}
	if body.Enabled != nil {
		current.Enabled = *body.Enabled
	}
	if body.FilterOverride != nil {
		current.FilterOverride = *body.FilterOverride
	}
	if body.FilterMode != nil {
		current.FilterMode = *body.FilterMode
	}
	if body.FilterEditable != nil {
		current.FilterEditable = *body.FilterEditable
	}
	if body.CatalogModeID != nil {
		current.CatalogModeID = *body.CatalogModeID
	}
	if body.CatalogEditable != nil {
		current.CatalogEditable = *body.CatalogEditable
	}
	if body.ActiveDial != nil {
		current.ActiveDial = *body.ActiveDial
	}
	if body.WebAuth != nil {
		if !isValidWebAuth(*body.WebAuth) {
			writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid web_auth mode"})
			return
		}
		current.WebAuth = *body.WebAuth
	}
	if body.Networks != nil {
		for _, raw := range *body.Networks {
			if _, err := netip.ParsePrefix(strings.TrimSpace(raw)); err != nil {
				writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid network CIDR: " + raw})
				return
			}
		}
		current.Networks = *body.Networks
	}

	if body.PeerASN != nil && *body.PeerASN == 0 {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Peer ASN cannot be zero"})
		return
	}

	// Validate name if provided
	if body.Name != nil && *body.Name == "" {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Name is required"})
		return
	}

	// Handle BGP password with explicit toggle logic.
	if body.PasswordEnabled != nil {
		if *body.PasswordEnabled {
			// Enable: set new password or keep existing.
			if body.BGPPassword != nil && *body.BGPPassword != "" {
				current.BGPPassword = *body.BGPPassword
			} else if current.BGPPassword == "" {
				writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Password must be set to enable"})
				return
			}
		} else {
			// Disable: clear password.
			if body.BGPPassword != nil && *body.BGPPassword != "" {
				writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Cannot set password while disabling"})
				return
			}
			current.BGPPassword = ""
		}
	}

	// Apply defaults
	if current.CatalogModeID == 0 {
		current.CatalogModeID = store.DefaultCatalogModeID
	}
	if current.WebAuth == "" {
		current.WebAuth = s.settings.DefaultWebAuth.Get()
	}

	// Only validate the mode when this request actually asks to change it —
	// an update that doesn't touch catalog_mode_id must still succeed even
	// if the user's *current* mode was disabled sometime after assignment
	// (same reasoning as apiAdminUserSaveSelections' switchingMode gate).
	if body.CatalogModeID != nil {
		if mode, err := s.store.CatalogMode(r.Context(), current.CatalogModeID); err != nil {
			writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Catalog mode not found"})
			return
		} else if !mode.Enabled {
			writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Catalog mode is disabled"})
			return
		}
	}

	// Only network/both actually authenticate by IP match — see the
	// matching check in apiUsersCreate for why login/any are exempt.
	if (current.WebAuth == "network" || current.WebAuth == "both") && len(current.Networks) == 0 {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "At least one network is required"})
		return
	}

	// Same normalization check as apiUsersCreate, against the final
	// effective state — this fires even when a request only changes
	// web_auth back to an active mode without touching networks at all:
	// current.Networks still holds whatever was already stored (possibly
	// untouched for a long time while this user sat in login mode, or
	// dating from before this check existed), and it needs to be
	// re-validated the moment it becomes active again.
	if isActiveWebAuth(current.WebAuth) {
		if err := validateNetworksNormalized(current.Networks); err != nil {
			writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: err.Error()})
			return
		}
	}

	// Reject dynamic peers when feature flag is off
	if !s.settings.AllowDynamicPeers.Get() && (current.PeerIP == "0.0.0.0" || current.PeerIP == "::") {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Dynamic peers are disabled"})
		return
	}

	// Dynamic peers cannot have active dial (can't dial a wildcard address)
	if (current.PeerIP == "0.0.0.0" || current.PeerIP == "::") && current.ActiveDial {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Dynamic peers cannot use active dial (wildcard IP cannot be dialed)"})
		return
	}

	// Validate peer uniqueness before the cross-user network overlap check
	// below — see apiUsersCreate for why.
	if err := s.validatePeerUniqueness(r.Context(), current, id); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: err.Error()})
		return
	}

	// Same cross-user overlap check as apiUsersCreate, against the final
	// effective state — see the normalization check above for why this
	// must run even when the request doesn't touch networks directly.
	if isActiveWebAuth(current.WebAuth) {
		if err := s.store.ActiveNetworksOverlap(r.Context(), current.Networks, id); err != nil {
			writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: err.Error()})
			return
		}
	}

	// Merge and validate route filters before writing the user record — the
	// same ordering rationale as apiUsersCreate: a malformed CIDR must not
	// leave the user record updated (and, on the enabled path below, BGP
	// peers refreshed) while the filter save itself still fails afterward.
	// SetUserRouteFilters replaces both sides unconditionally, so a partial
	// update touching only one of filter_allow/filter_deny must start from
	// the currently saved value for the untouched side, not an empty slice
	// — otherwise omitting one field from the request silently wipes the
	// other.
	var filterAllow, filterDeny []string
	filtersProvided := body.FilterAllow != nil || body.FilterDeny != nil
	if filtersProvided {
		existing, err := s.store.UserRouteFilters(r.Context(), id)
		if err != nil {
			logging.FromContext(r.Context()).Debug("route filters lookup before update failed", "error", err, "user_id", id)
			writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Failed to load existing route filters"})
			return
		}
		filterAllow = existing.Allow
		filterDeny = existing.Deny
		if body.FilterAllow != nil {
			filterAllow = *body.FilterAllow
		}
		if body.FilterDeny != nil {
			filterDeny = *body.FilterDeny
		}
		if _, err := store.NormalizeRouteFilters(store.RouteFilters{Allow: filterAllow, Deny: filterDeny}); err != nil {
			writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: err.Error()})
			return
		}
	}

	if err := s.store.UpdateUser(r.Context(), current); err != nil {
		if store.IsNotFound(err) {
			writeJSON(w, http.StatusNotFound, apiResponse{OK: false, Error: "User not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}

	if filtersProvided {
		if err := s.store.SetUserRouteFilters(r.Context(), id, store.RouteFilters{Allow: filterAllow, Deny: filterDeny}); err != nil {
			logging.FromContext(r.Context()).Debug("route filters save after update failed", "error", err, "user_id", id)
			writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Failed to save route filters"})
			return
		}
	}

	if s.bgp != nil {
		if !current.Enabled {
			if err := s.bgp.DeletePeer(r.Context(), current.PeerIP, current.ID); err != nil {
				logging.FromContext(r.Context()).Debug("bgp delete peer failed", "error", err, "peer_ip", current.PeerIP, "user_id", current.ID)
			}
		} else {
			// Reload user to get correct BGPPassword preserved by store
			reloaded, err := s.store.User(r.Context(), id)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
				return
			}
			if err := s.bgp.UpdatePeer(r.Context(), reloaded); err != nil {
				writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
				return
			}
		}
		if err := s.bgp.Reconcile(r.Context()); err != nil {
			logging.FromContext(r.Context()).Debug("bgp reconcile failed after user update", "error", err)
		}
	}

	var peerStates map[string]string
	if s.bgp != nil {
		peerStates, _ = s.bgp.PeerStates(r.Context()) //nolint:errcheck // best-effort
	}
	s.store.RecordUserSnapshot(r.Context(), s.settings.MetricsEnabled.Get(), peerStates)

	// Reload to get full data
	updated, _ := s.store.User(r.Context(), id) //nolint:errcheck // just updated, must exist
	state := s.userPeerState(r.Context(), updated)
	j := userToJSON(updated, state)
	filters, err := s.store.UserRouteFilters(r.Context(), id)
	if err != nil {
		logging.FromContext(r.Context()).Debug("route filters lookup in update response failed", "error", err, "user_id", id)
	} else {
		j.FilterAllow = filters.Allow
		j.FilterDeny = filters.Deny
	}
	writeJSON(w, http.StatusOK, j)
}

// apiUsersDelete handles DELETE /api/admin/users/{id}.
func (s *Server) apiUsersDelete(w http.ResponseWriter, r *http.Request) {
	extendWriteDeadline(w, r) // synchronous BGP reconcile can outlive WriteTimeout
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid user ID"})
		return
	}

	// Get user first to know the peer IP for BGP
	user, err := s.store.User(r.Context(), id)
	if err != nil && !store.IsNotFound(err) {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}

	if err := s.store.DeleteUser(r.Context(), id); err != nil {
		if store.IsNotFound(err) {
			writeJSON(w, http.StatusNotFound, apiResponse{OK: false, Error: "User not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}

	// Only delete peer if user existed (not already deleted)
	if err == nil {
		if s.bgp != nil {
			if err := s.bgp.DeletePeer(r.Context(), user.PeerIP, user.ID); err != nil {
				logging.FromContext(r.Context()).Debug("bgp delete peer failed on user delete", "error", err, "peer_ip", user.PeerIP, "user_id", user.ID)
			}
			if err := s.bgp.Reconcile(r.Context()); err != nil {
				logging.FromContext(r.Context()).Debug("bgp reconcile failed after user delete", "error", err)
			}
		}
		var peerStates map[string]string
		if s.bgp != nil {
			peerStates, _ = s.bgp.PeerStates(r.Context()) //nolint:errcheck // best-effort
		}
		s.store.RecordUserSnapshot(r.Context(), s.settings.MetricsEnabled.Get(), peerStates)
	}

	writeJSON(w, http.StatusOK, apiResponse{OK: true})
}

// apiUserCredentialsList handles GET /api/admin/users/{id}/credentials.
func (s *Server) apiUserCredentialsList(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid user ID"})
		return
	}

	creds, err := s.store.GetUserCredentials(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Failed to load credentials"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"credentials": credentialsToJSON(creds)})
}

// apiUserCredentialsSet handles PUT /api/admin/users/{id}/credentials.
func (s *Server) apiUserCredentialsSet(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid user ID"})
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

	if err := s.store.SetUserCredential(r.Context(), id, body.Login, body.Password); err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}

	// Return updated credentials list
	creds, err := s.store.GetUserCredentials(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Failed to load credentials"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"credentials": credentialsToJSON(creds)})
}

// apiUserCredentialsDelete handles DELETE /api/admin/users/{id}/credentials.
// Uses request body {login: "..."} to identify the credential to delete.
func (s *Server) apiUserCredentialsDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid user ID"})
		return
	}

	var body struct {
		Login string `json:"login"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid request body"})
		return
	}

	// Empty password deletes the credential
	if err := s.store.SetUserCredential(r.Context(), id, body.Login, ""); err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}

	// Return updated credentials list
	creds, err := s.store.GetUserCredentials(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Failed to load credentials"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"credentials": credentialsToJSON(creds)})
}

// apiUserPeerState handles GET /api/admin/users/{id}/peer-state.
func (s *Server) apiUserPeerState(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid user ID"})
		return
	}

	u, err := s.store.User(r.Context(), id)
	if store.IsNotFound(err) {
		writeJSON(w, http.StatusNotFound, apiResponse{OK: false, Error: "User not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Failed to load user"})
		return
	}

	state := s.userPeerState(r.Context(), u)
	writeJSON(w, http.StatusOK, map[string]string{"state": state})
}

// apiUserStatuses handles GET /api/admin/users/statuses.
// Returns raw BGP peer states keyed by "ip:asn" for the frontend to match against user data.
func (s *Server) apiUserStatuses(w http.ResponseWriter, r *http.Request) {
	if s.bgp == nil {
		writeJSON(w, http.StatusOK, map[string]any{"peer_states": map[string]string{}})
		return
	}
	peerStates, err := s.bgp.PeerStates(r.Context())
	if err != nil {
		logging.FromContext(r.Context()).Debug("peer states lookup failed", "error", err)
		writeJSON(w, http.StatusOK, map[string]any{"peer_states": map[string]string{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"peer_states": peerStates})
}

// apiAdminUserCatalog handles GET /api/admin/users/{id}/catalog.
// Returns catalog + selections for any user (admin-only). Accepts optional ?mode= query.
func (s *Server) apiAdminUserCatalog(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid user ID"})
		return
	}

	user, err := s.store.User(r.Context(), id)
	if store.IsNotFound(err) {
		writeJSON(w, http.StatusNotFound, apiResponse{OK: false, Error: "User not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}

	modeID := user.CatalogModeID
	if rawMode := r.URL.Query().Get("mode"); rawMode != "" {
		if mid, parseErr := strconv.ParseInt(rawMode, 10, 64); parseErr == nil && mid > 0 {
			modeID = mid
			user.CatalogModeID = modeID // override for response
		}
	}

	catalog, err := s.store.CatalogForMode(r.Context(), modeID, true)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}

	categories, services, err := s.store.UserModeSelection(r.Context(), id, modeID)
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
	catList := make([]string, 0, len(categories))
	for c := range categories {
		if visibleCats[c] {
			catList = append(catList, c)
		}
	}
	svcList := make([]store.ServiceKey, 0, len(services))
	for k := range services {
		if visibleSvcs[k.Category+"|"+k.Service] {
			svcList = append(svcList, k)
		}
	}

	prefixCountsV4, prefixCountsV6, err := s.store.PrefixCounts(r.Context(), modeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}

	modes, err := s.store.CatalogModes(r.Context(), false)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"user":       userToJSON(user, s.userPeerState(r.Context(), user)),
		"catalog":    catalog,
		"selections": map[string]any{"categories": catList, "services": svcList},
		"prefix_counts": map[string]any{
			"v4": prefixCountsV4,
			"v6": prefixCountsV6,
		},
		"modes": modes,
	})
}

// apiAdminUserSaveSelections handles PUT /api/admin/users/{id}/selections.
// Saves category/service selections for any user (admin-only), bypassing selection_locked.
func (s *Server) apiAdminUserSaveSelections(w http.ResponseWriter, r *http.Request) {
	extendRequestDeadlines(w, r) // large selection upload + reconcile can outlive Read/WriteTimeout
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid user ID"})
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
		ModeID int64 `json:"mode_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid body"})
		return
	}

	user, err := s.store.User(r.Context(), id)
	if store.IsNotFound(err) {
		writeJSON(w, http.StatusNotFound, apiResponse{OK: false, Error: "User not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}

	modeID := user.CatalogModeID
	if body.ModeID > 0 {
		modeID = body.ModeID
	}
	switchingMode := body.ModeID > 0 && body.ModeID != user.CatalogModeID

	err = s.store.Transaction(r.Context(), func(tx *sql.Tx) error {
		if switchingMode {
			// Persist the mode switch alongside the selection rows below, in the
			// same transaction, so a save that changes mode doesn't leave the
			// user's active catalog_mode_id pointing at the old mode.
			if err := store.SetUserCatalogModeTx(r.Context(), tx, id, modeID, false); err != nil {
				return err
			}
		}
		for _, c := range body.Categories {
			if err := store.ToggleSelectedCategory(r.Context(), tx, id, modeID, c.Category, c.Checked); err != nil {
				return err
			}
		}
		for _, svc := range body.Services {
			if err := store.ToggleSelectedService(r.Context(), tx, id, modeID, svc.Category, svc.Service, svc.Checked); err != nil {
				return err
			}
		}
		return nil
	})
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid or disabled mode"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}

	if s.bgp != nil {
		if err := s.bgp.Reconcile(r.Context()); err != nil {
			writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Selection saved but BGP reconciliation failed: " + err.Error()})
			return
		}
	}

	var peerStates map[string]string
	if s.bgp != nil {
		peerStates, _ = s.bgp.PeerStates(r.Context()) //nolint:errcheck // best-effort
	}
	s.store.RecordUserSnapshot(r.Context(), s.settings.MetricsEnabled.Get(), peerStates)

	writeJSON(w, http.StatusOK, apiResponse{OK: true})
}

// apiAdminUserCountPrefixes handles POST /api/admin/users/{id}/count-selections.
// Returns the number of v4/v6 prefixes for a given selection with deltas against saved selections.
func (s *Server) apiAdminUserCountPrefixes(w http.ResponseWriter, r *http.Request) {
	extendRequestDeadlines(w, r) // large request body can outlive ReadTimeout
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid user ID"})
		return
	}

	user, err := s.store.User(r.Context(), id)
	if store.IsNotFound(err) {
		writeJSON(w, http.StatusNotFound, apiResponse{OK: false, Error: "User not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
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
		ModeID int64 `json:"mode_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid body"})
		return
	}

	modeID := user.CatalogModeID
	if body.ModeID > 0 {
		modeID = body.ModeID
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
	newV4, newV6, err := s.store.CountPrefixes(r.Context(), modeID, catList, svcList, id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}

	// Load current saved selections
	curCats, curSvcs, err := s.store.UserModeSelection(r.Context(), id, modeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}

	curCatList := make([]string, 0, len(curCats))
	for c := range curCats {
		curCatList = append(curCatList, c)
	}
	curSvcList := make([]store.ServiceKey, 0, len(curSvcs))
	for k := range curSvcs {
		curSvcList = append(curSvcList, k)
	}

	curV4, curV6, err := s.store.CountPrefixes(r.Context(), modeID, curCatList, curSvcList, id)
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

// isValidIP returns true if s is a valid IPv4 or IPv6 address.
func isValidIP(s string) bool {
	_, err := netip.ParseAddr(s)
	return err == nil
}
