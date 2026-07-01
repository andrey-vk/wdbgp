package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"strconv"
	"strings"

	"github.com/andrey-vk/wdbgp/internal/logging"
	"github.com/andrey-vk/wdbgp/internal/store"
)

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
	if body.WebAuth != "login" && len(body.Networks) == 0 {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "At least one network is required"})
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

	// Validate peer uniqueness
	if err := s.validatePeerUniqueness(r.Context(), user, 0); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: err.Error()})
		return
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

	// Validate peer uniqueness (skip self)
	if err := s.validatePeerUniqueness(r.Context(), current, id); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: err.Error()})
		return
	}

	if err := s.store.UpdateUser(r.Context(), current); err != nil {
		if store.IsNotFound(err) {
			writeJSON(w, http.StatusNotFound, apiResponse{OK: false, Error: "User not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}

	// Save route filters
	if body.FilterAllow != nil || body.FilterDeny != nil {
		allow := []string{}
		deny := []string{}
		if body.FilterAllow != nil {
			allow = *body.FilterAllow
		}
		if body.FilterDeny != nil {
			deny = *body.FilterDeny
		}
		if err := s.store.SetUserRouteFilters(r.Context(), id, store.RouteFilters{Allow: allow, Deny: deny}); err != nil {
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

	err = s.store.Transaction(r.Context(), func(tx *sql.Tx) error {
		for _, c := range body.Categories {
			if c.Checked {
				if _, err := tx.ExecContext(r.Context(),
					"INSERT OR IGNORE INTO selected_categories(user_id, mode_id, category) VALUES (?, ?, ?)",
					id, modeID, c.Category); err != nil {
					return err
				}
			} else {
				if _, err := tx.ExecContext(r.Context(),
					"DELETE FROM selected_categories WHERE user_id = ? AND mode_id = ? AND category = ?",
					id, modeID, c.Category); err != nil {
					return err
				}
			}
		}
		for _, s := range body.Services {
			if s.Checked {
				if _, err := tx.ExecContext(r.Context(),
					"INSERT OR IGNORE INTO selected_services(user_id, mode_id, category, service) VALUES (?, ?, ?, ?)",
					id, modeID, s.Category, s.Service); err != nil {
					return err
				}
			} else {
				if _, err := tx.ExecContext(r.Context(),
					"DELETE FROM selected_services WHERE user_id = ? AND mode_id = ? AND category = ? AND service = ?",
					id, modeID, s.Category, s.Service); err != nil {
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
