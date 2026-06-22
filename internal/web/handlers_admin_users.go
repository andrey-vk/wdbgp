package web

import (
	"context"
	"database/sql"
	"fmt"
	"html/template"
	"net/http"
	"net/netip"
	"strconv"
	"strings"

	"github.com/andrey-vk/wdbgp/internal/store"
)

func (s *Server) addUser(w http.ResponseWriter, r *http.Request) {
	user, _, err := parseUser(r, 0)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Validate peer uniqueness (three-step)
	if err := s.validatePeerUniqueness(r.Context(), user, 0); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	userID, err := s.store.AddUser(r.Context(), user)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	user.ID = userID
	if err := s.bgp.AddPeer(r.Context(), user); err != nil {
		s.internalError(w, r, err)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) adminUserPage(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.httpError(w, r, "error.bad_user_id", http.StatusBadRequest)
		return
	}
	if id == 0 {
		// New user creation form
		lang, _ := requestLocale(r, s.defaultLang)
		emptyUser := store.User{
			WebAuth:       s.cfg.DefaultWebAuth,
			CatalogModeID: store.DefaultCatalogModeID,
			Enabled:       true,
		}
		var dynamicReadonly bool
		if !s.cfg.AllowDynamicPeers {
			dynamicReadonly = true
		}
		activeDialDisabled := !s.cfg.ActiveDial
		activeDialHint := ""
		if activeDialDisabled {
			activeDialHint = "hints.active_dial_system_disabled"
		}
		// Compute form-component attributes
		peerIPAttrs := template.HTMLAttr(" id=peer-ip")
		dynamicIPAttrs := template.HTMLAttr(" id=dynamic-ip")
		if dynamicReadonly {
			dynamicIPAttrs = template.HTMLAttr(fmt.Sprintf(` id=dynamic-ip readonly title="%s"`, translate(lang, "hint.dynamic_peers_disabled")))
		}
		activeDialAttrs := template.HTMLAttr("")
		if activeDialDisabled {
			activeDialAttrs = template.HTMLAttr(fmt.Sprintf(` disabled title="%s"`, translate(lang, activeDialHint)))
		}
		activeDialHintResolved := activeDialHint
		if activeDialHintResolved == "" {
			activeDialHintResolved = "hints.active_dial"
		}
		webAuthOptions := []modeOption{
			{Value: "network", Text: translate(lang, "users.web_auth_network"), Selected: emptyUser.WebAuth == "network"},
			{Value: "login", Text: translate(lang, "users.web_auth_login"), Selected: emptyUser.WebAuth == "login"},
			{Value: "both", Text: translate(lang, "users.web_auth_both"), Selected: emptyUser.WebAuth == "both"},
			{Value: "any", Text: translate(lang, "users.web_auth_any"), Selected: emptyUser.WebAuth == "any"},
		}
		modes, _ := s.store.CatalogModes(r.Context(), false)
		modeOptions := make([]modeOption, len(modes))
		for i, m := range modes {
			modeOptions[i] = modeOption{Value: strconv.FormatInt(m.ID, 10), Text: m.Name, Selected: m.ID == emptyUser.CatalogModeID}
		}
		s.renderAdmin(w, r, http.StatusOK, fmt.Sprintf(translate(lang, "title.user"), translate(lang, "common.add")), "user-edit",
			userEditView{User: emptyUser, DynamicReadonly: dynamicReadonly,
				ActiveDial: true, ActiveDialDisabled: activeDialDisabled, ActiveDialHint: activeDialHint,
				PeerIPAttrs: peerIPAttrs, DynamicIPAttrs: dynamicIPAttrs, ActiveDialAttrs: activeDialAttrs,
				PasswordAttrs: template.HTMLAttr(""), ActiveDialHintResolved: activeDialHintResolved, NetworksStr: "", WebAuthOptions: webAuthOptions, ModeOptions: modeOptions})
		return
	}
	user, err := s.store.User(r.Context(), id)
	if store.IsNotFound(err) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	user, err = s.userWithRequestedMode(r.Context(), r, user, true, true)
	if err != nil {
		s.httpError(w, r, "error.bad_mode_id", http.StatusBadRequest)
		return
	}
	selection, err := s.selection(r.Context(), user, true, true)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	// Add CSRF token to selection view for the template
	csrfToken := ""
	if tokenVal := r.Context().Value(csrfCtxKey{}); tokenVal != nil {
		csrfToken = tokenVal.(string)
	}
	selection.CSRFToken = csrfToken

	credentials, _ := s.store.GetUserCredentials(r.Context(), id)
	lang, _ := requestLocale(r, s.defaultLang)
	var dynamicReadonly bool
	var dynamicChecked bool
	var passwordDisabled bool
	var passwordHint string
	if !s.cfg.AllowDynamicPeers {
		dynamicReadonly = true
	}
	if user.PeerIP == "0.0.0.0" || user.PeerIP == "::" {
		dynamicChecked = true
		passwordDisabled = true
	} else if user.PeerIP != "" {
		var sameIPCount int
		s.store.DB.QueryRowContext(r.Context(),
			"SELECT COUNT(*) FROM users WHERE peer_ip = ? AND id != ?",
			user.PeerIP, id).Scan(&sameIPCount)
		if sameIPCount > 0 {
			passwordHint = "hint.same_ip_password"
		}
	}
	activeDialDisabled := !s.cfg.ActiveDial
	activeDialHint := ""
	if activeDialDisabled {
		activeDialHint = "hints.active_dial_system_disabled"
	}
	// Compute form-component attributes
	peerIPAttrs := template.HTMLAttr(" id=peer-ip")
	if user.PeerIP == "0.0.0.0" || user.PeerIP == "::" {
		peerIPAttrs = template.HTMLAttr(" id=peer-ip readonly")
	}
	dynamicIPAttrs := template.HTMLAttr(" id=dynamic-ip")
	if dynamicReadonly {
		dynamicIPAttrs = template.HTMLAttr(fmt.Sprintf(` id=dynamic-ip readonly title="%s"`, translate(lang, "hint.dynamic_peers_disabled")))
	}
	passwordAttrs := template.HTMLAttr("")
	if passwordDisabled {
		passwordAttrs = template.HTMLAttr(fmt.Sprintf(` disabled title="%s"`, translate(lang, "hint.dynamic_no_password")))
	} else if passwordHint != "" {
		passwordAttrs = template.HTMLAttr(fmt.Sprintf(` title="%s"`, translate(lang, passwordHint)))
	}
	activeDialAttrs := template.HTMLAttr("")
	if activeDialDisabled {
		activeDialAttrs = template.HTMLAttr(fmt.Sprintf(` disabled title="%s"`, translate(lang, activeDialHint)))
	}
	activeDialHintResolved := activeDialHint
	if activeDialHintResolved == "" {
		activeDialHintResolved = "hints.active_dial"
	}
	webAuthOptions := []modeOption{
		{Value: "network", Text: translate(lang, "users.web_auth_network"), Selected: user.WebAuth == "network"},
		{Value: "login", Text: translate(lang, "users.web_auth_login"), Selected: user.WebAuth == "login"},
		{Value: "both", Text: translate(lang, "users.web_auth_both"), Selected: user.WebAuth == "both"},
		{Value: "any", Text: translate(lang, "users.web_auth_any"), Selected: user.WebAuth == "any"},
	}
	modes, _ := s.store.CatalogModes(r.Context(), false)
	modeOptions := make([]modeOption, len(modes))
	for i, m := range modes {
		modeOptions[i] = modeOption{Value: strconv.FormatInt(m.ID, 10), Text: m.Name, Selected: m.ID == user.CatalogModeID}
	}
	networksStr := strings.Join(user.Networks, ", ")
	s.renderAdmin(w, r, http.StatusOK, fmt.Sprintf(translate(lang, "title.user"), user.Name), "user-edit",
		userEditView{User: user, Selection: selection, Credentials: credentials,
			DynamicReadonly: dynamicReadonly, DynamicChecked: dynamicChecked,
			PasswordDisabled: passwordDisabled, PasswordHint: passwordHint,
			ActiveDial: user.ActiveDial, ActiveDialDisabled: activeDialDisabled, ActiveDialHint: activeDialHint,
			PeerIPAttrs: peerIPAttrs, DynamicIPAttrs: dynamicIPAttrs, ActiveDialAttrs: activeDialAttrs,
			PasswordAttrs: passwordAttrs, ActiveDialHintResolved: activeDialHintResolved,
			NetworksStr: networksStr, WebAuthOptions: webAuthOptions, ModeOptions: modeOptions})
}

func (s *Server) saveAdminUser(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.httpError(w, r, "error.bad_user_id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.httpError(w, r, "error.bad_request", http.StatusBadRequest)
		return
	}
	if r.FormValue("action") == "settings" {
		user, clearPassword, err := parseUserForm(r, id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// Reload stored password if not clearing and field is empty
		if user.BGPPassword == "" && !clearPassword {
			current, _ := s.store.User(r.Context(), id)
			if current.ID != 0 {
				user.BGPPassword = current.BGPPassword
			}
		}
		// Validate peer uniqueness (three-step), excluding self
		if err := s.validatePeerUniqueness(r.Context(), user, id); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		switch user.WebAuth {
		case "network", "login", "both", "any":
		default:
			http.Error(w, `web_auth must be "network", "login", "both", or "any"`, http.StatusBadRequest)
			return
		}
		if err := s.store.UpdateUser(r.Context(), user, clearPassword); err != nil {
			s.internalError(w, r, err)
			return
		}

		// Process existing credentials
		for i := 0; ; i++ {
			loginKey := fmt.Sprintf("cred_login_%d", i)
			deleteKey := fmt.Sprintf("cred_delete_%d", i)
			passwordKey := fmt.Sprintf("cred_password_%d", i)

			login := r.FormValue(loginKey)
			if login == "" {
				break
			}

			if r.FormValue(deleteKey) == "on" {
				s.store.SetUserCredential(r.Context(), id, login, "")
			} else if pw := r.FormValue(passwordKey); pw != "" {
				s.store.SetUserCredential(r.Context(), id, login, pw)
			}
		}

		// Process new credential
		if newLogin := r.FormValue("cred_login_new"); newLogin != "" {
			if newPassword := r.FormValue("cred_password_new"); newPassword != "" {
				s.store.SetUserCredential(r.Context(), id, newLogin, newPassword)
			}
		}

		if !user.Enabled {
			err = s.bgp.DeletePeer(r.Context(), user.PeerIP, user.ID)
		} else {
			// Reload user to get correct BGPPassword preserved by store when field was empty.
			user, err = s.store.User(r.Context(), id)
			if err != nil {
				s.internalError(w, r, err)
				return
			}
			err = s.bgp.UpdatePeer(r.Context(), user)
		}
		if err == nil {
			err = s.bgp.Reconcile(r.Context())
		}
	} else if r.FormValue("action") == "filters" {
		filters, parseErr := routeFiltersFromForm(r)
		if parseErr != nil {
			http.Error(w, parseErr.Error(), http.StatusBadRequest)
			return
		}
		err = s.store.SetUserRouteFilterConfig(r.Context(), id, r.FormValue("filter_mode"), filters)
		if err == nil {
			err = s.bgp.Reconcile(r.Context())
		}
	} else {
		categories, services, parseErr := selectionFromValues(r.Form)
		if parseErr != nil {
			http.Error(w, parseErr.Error(), http.StatusBadRequest)
			return
		}
		currentUser, userErr := s.store.User(r.Context(), id)
		if userErr != nil {
			s.internalError(w, r, userErr)
			return
		}
		modeID, modeErr := formModeID(r, currentUser.CatalogModeID)
		if modeErr != nil {
			http.Error(w, modeErr.Error(), http.StatusBadRequest)
			return
		}
		err = s.store.SetUserCatalogModeSelection(
			r.Context(), id, modeID, false, categories, services)
		if err == nil {
			err = s.bgp.Reconcile(r.Context())
		}
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/user/%d", id), http.StatusSeeOther)
}

func (s *Server) deleteAdminUser(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.httpError(w, r, "error.bad_user_id", http.StatusBadRequest)
		return
	}
	// Get user first to know the peer IP
	user, err := s.store.User(r.Context(), id)
	if err != nil && !store.IsNotFound(err) {
		s.internalError(w, r, err)
		return
	}
	if err := s.store.DeleteUser(r.Context(), id); err != nil {
		s.internalError(w, r, err)
		return
	}
	// Only delete peer if user exists (not already deleted)
	if err == nil {
		if err := s.bgp.DeletePeer(r.Context(), user.PeerIP, user.ID); err != nil {
			s.internalError(w, r, err)
			return
		}
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) usersList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	users, err := s.store.Users(ctx, false)
	if err != nil {
		s.internalError(w, r, err)
		return
	}

	// Get peer states for status
	peerStates, _ := s.bgp.PeerStates(ctx)

	type userRow struct {
		User      store.User
		PeerState string
		Networks  string
	}

	rows := make([]userRow, 0, len(users))
	for _, u := range users {
		peerKey := fmt.Sprintf("%s:%d", u.PeerIP, u.PeerASN)
		state := peerStates[peerKey]
		if state == "" {
			state = "—"
		}
		rows = append(rows, userRow{
			User:      u,
			PeerState: state,
			Networks:  strings.Join(u.Networks, ", "),
		})
	}

	s.renderAdmin(w, r, http.StatusOK, "Users", "users-list", map[string]any{
		"Users": rows,
	})
}

// validatePeerUniqueness validates a user's BGP peer configuration against
// existing peers. skipUserID is the user's own ID (0 for new users).
// Step A: Same IP + same ASN → reject (UNIQUE constraint)
// Step B: Dynamic peers (0.0.0.0 or ::) require globally unique ASN
// Step C: Shared IP + different ASN → password required when RequirePasswordForNonUniqueIP is ON
func (s *Server) validatePeerUniqueness(ctx context.Context, user store.User, skipUserID int64) error {
	var existingID int64
	var existingName string
	err := s.store.DB.QueryRowContext(ctx,
		"SELECT id, name FROM users WHERE peer_ip = ? AND peer_asn = ? AND id != ?",
		user.PeerIP, user.PeerASN, skipUserID).Scan(&existingID, &existingName)
	if err == nil {
		return fmt.Errorf("peer %s with ASN %d already exists as user %s", user.PeerIP, user.PeerASN, existingName)
	}
	if err != sql.ErrNoRows {
		return fmt.Errorf("failed to check peer uniqueness: %w", err)
	}

	// Step B: Dynamic peers (0.0.0.0 or ::) require globally unique ASN
	if user.PeerIP == "0.0.0.0" || user.PeerIP == "::" {
		var count int
		err := s.store.DB.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM users WHERE peer_asn = ? AND id != ?",
			user.PeerASN, skipUserID).Scan(&count)
		if err != nil {
			return fmt.Errorf("failed to check dynamic peer uniqueness: %w", err)
		}
		if count > 0 {
			return fmt.Errorf("dynamic peer with ASN %d already exists", user.PeerASN)
		}
		return nil
	}

	// Step C: Shared IP + different ASN → require matching password
	var sharedCount int
	err = s.store.DB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM users WHERE peer_ip = ? AND id != ?",
		user.PeerIP, skipUserID).Scan(&sharedCount)
	if err != nil {
		return fmt.Errorf("failed to check shared IP peers: %w", err)
	}
	if sharedCount > 0 {
		if s.cfg.RequirePasswordForNonUniqueIP && user.BGPPassword == "" {
			return fmt.Errorf("BGP password required when sharing IP %s with another ASN", user.PeerIP)
		}
		// If new peer has password, existing same-IP peers must also have passwords
		if user.BGPPassword != "" {
			var pwLessCount int
			err = s.store.DB.QueryRowContext(ctx,
				"SELECT COUNT(*) FROM users WHERE peer_ip = ? AND id != ? AND (bgp_password = '' OR bgp_password IS NULL)",
				user.PeerIP, skipUserID).Scan(&pwLessCount)
			if err != nil {
				return fmt.Errorf("failed to check shared IP passwords: %w", err)
			}
			if pwLessCount > 0 {
				return fmt.Errorf("cannot set BGP password on peer %s: existing peers on same IP have no password", user.PeerIP)
			}
		}
		// If any existing peer on same IP has a password, new peer's must match.
		var existingPwd string
		err = s.store.DB.QueryRowContext(ctx,
			"SELECT DISTINCT bgp_password FROM users WHERE peer_ip = ? AND id != ? AND bgp_password != ''",
			user.PeerIP, skipUserID).Scan(&existingPwd)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("failed to check shared IP passwords: %w", err)
		}
		if existingPwd != "" && user.BGPPassword != existingPwd {
			return fmt.Errorf("BGP password must match existing peer on IP %s", user.PeerIP)
		}
	}

	return nil
}

func parseUser(r *http.Request, id int64) (store.User, bool, error) {
	if err := r.ParseForm(); err != nil {
		return store.User{}, false, err
	}
	return parseUserForm(r, id)
}

func parseUserForm(r *http.Request, id int64) (store.User, bool, error) {
	peerIP, err := netip.ParseAddr(strings.TrimSpace(r.FormValue("peer_ip")))
	if err != nil {
		return store.User{}, false, fmt.Errorf("invalid BGP peer IP: %w", err)
	}
	peerASN, err := strconv.ParseUint(r.FormValue("peer_asn"), 10, 32)
	if err != nil || peerASN == 0 {
		return store.User{}, false, fmt.Errorf("invalid peer ASN")
	}
	nextHop := strings.TrimSpace(r.FormValue("next_hop"))
	if nextHop != "" {
		if _, err := netip.ParseAddr(nextHop); err != nil {
			return store.User{}, false, fmt.Errorf("invalid next hop: %w", err)
		}
	}
	var networks []string
	for _, raw := range strings.Split(r.FormValue("networks"), ",") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		network, err := store.NormalizePrefix(raw)
		if err != nil {
			return store.User{}, false, fmt.Errorf("invalid client network %q: %w", raw, err)
		}
		networks = append(networks, network)
	}
	if len(networks) == 0 {
		return store.User{}, false, fmt.Errorf("at least one client network is required")
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		return store.User{}, false, fmt.Errorf("user name is required")
	}
	modeID, err := formModeID(r, store.DefaultCatalogModeID)
	if err != nil {
		return store.User{}, false, err
	}
	return store.User{
		ID: id, Name: name, PeerIP: peerIP.String(), PeerASN: uint32(peerASN),
		NextHop: nextHop, BGPPassword: r.FormValue("bgp_password"),
		SelectionLocked: r.Form.Has("locked"), Enabled: id == 0 || r.Form.Has("enabled"),
		FilterOverride:  r.FormValue("filter_override") == "on",
		FilterMode:      r.FormValue("filter_mode"),
		FilterEditable:  r.Form.Has("filter_editable"),
		CatalogModeID:   modeID,
		CatalogEditable: r.Form.Has("catalog_mode_editable"),
		ActiveDial:      r.Form.Has("active_dial"),
		WebAuth:         r.FormValue("web_auth"),
		Networks:        networks,
	}, r.Form.Has("clear_bgp_password"), nil
}
