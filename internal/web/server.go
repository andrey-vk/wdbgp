package web

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"github.com/andrey-vk/wdbgp/internal/config"
	"github.com/andrey-vk/wdbgp/internal/feeds"
	"github.com/andrey-vk/wdbgp/internal/store"
)

type BGP interface {
	Reconcile(context.Context) error
	ReloadPeers(context.Context) error
	PeerStates(context.Context) (map[string]string, error)
}

type Server struct {
	cfg         config.Config
	store       *store.Store
	syncer      *feeds.Syncer
	bgp         BGP
	defaultLang locale
	templates   map[locale]map[string]*template.Template
	handler     http.Handler
}

type categoryView struct {
	Name     string
	Selected bool
	Services []serviceView
}

type serviceView struct {
	Name     string
	Value    string
	Selected bool
	Disabled bool
}

type selectionView struct {
	User          store.User
	Categories    []categoryView
	Editable      bool
	Admin         bool
	Saved         string
	Filters       filterView
	SelectedCount int
}

type adminView struct {
	Feeds         []store.Feed
	Users         []store.User
	PeerStates    map[string]string
	GlobalFilters filterView
}

type userEditView struct {
	User      store.User
	Selection selectionView
}

type filterView struct {
	AllowText string
	DenyText  string
	Override  bool
	Mode      string
	Editable  bool
	Admin     bool
}

func New(cfg config.Config, s *store.Store, syncer *feeds.Syncer, bgp BGP) *Server {
	defaultLang, ok := parseLocale(cfg.DefaultLanguage)
	if !ok {
		defaultLang = localeEnglish
	}
	server := &Server{
		cfg: cfg, store: s, syncer: syncer, bgp: bgp,
		defaultLang: defaultLang, templates: compileTemplates(),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", server.userPage)
	mux.HandleFunc("POST /selection", server.saveOwnSelection)
	mux.HandleFunc("POST /filters", server.saveOwnFilters)
	mux.HandleFunc("GET /admin/login", server.loginPage)
	mux.HandleFunc("POST /admin/login", server.login)
	mux.HandleFunc("GET /admin", server.requireAdmin(server.adminPage))
	mux.HandleFunc("POST /admin/feed", server.requireAdmin(server.addFeed))
	mux.HandleFunc("POST /admin/feed/{id}", server.requireAdmin(server.updateFeed))
	mux.HandleFunc("POST /admin/feed/{id}/delete", server.requireAdmin(server.deleteFeed))
	mux.HandleFunc("POST /admin/sync", server.requireAdmin(server.syncFeeds))
	mux.HandleFunc("POST /admin/filters", server.requireAdmin(server.saveGlobalFilters))
	mux.HandleFunc("POST /admin/user", server.requireAdmin(server.addUser))
	mux.HandleFunc("GET /admin/user/{id}", server.requireAdmin(server.adminUserPage))
	mux.HandleFunc("POST /admin/user/{id}", server.requireAdmin(server.saveAdminUser))
	mux.HandleFunc("POST /admin/user/{id}/delete", server.requireAdmin(server.deleteAdminUser))
	mux.HandleFunc("GET /healthz", server.health)
	server.handler = requestLogger(mux)
	return server
}

func (s *Server) Handler() http.Handler {
	return s.handler
}

func (s *Server) userPage(w http.ResponseWriter, r *http.Request) {
	user, err := s.store.UserByIP(r.Context(), s.clientIP(r))
	if store.IsNotFound(err) {
		s.render(w, r, http.StatusForbidden, "title.access_denied", "access-denied", s.clientIP(r))
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	view, err := s.selection(r.Context(), user, !user.SelectionLocked, false)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	view.Saved = r.URL.Query().Get("saved")
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
	if user.SelectionLocked {
		s.httpError(w, r, "error.selection_locked", http.StatusForbidden)
		return
	}
	categories, services, err := parseSelection(r)
	if err == nil {
		err = s.store.Transaction(r.Context(), func(tx *sql.Tx) error {
			return store.SetUserSelection(r.Context(), tx, user.ID, categories, services)
		})
	}
	if err == nil {
		err = s.bgp.Reconcile(r.Context())
	}
	if err != nil {
		log.Printf("save selection for user %d: %v", user.ID, err)
		http.Redirect(w, r, "/?saved=0", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/?saved=1", http.StatusSeeOther)
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

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "title.login", "login", "")
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.httpError(w, r, "error.bad_request", http.StatusBadRequest)
		return
	}
	if !hmac.Equal([]byte(r.FormValue("password")), []byte(s.cfg.AdminPassword)) {
		lang, _ := requestLocale(r, s.defaultLang)
		s.render(w, r, http.StatusUnauthorized, "title.login", "login",
			translate(lang, "login.invalid_password"))
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "wdbgp_admin",
		Value:    sessionToken(s.cfg.SessionSecret),
		Path:     "/",
		HttpOnly: true,
		Secure:   s.adminCookieSecure(r),
		SameSite: http.SameSiteStrictMode,
	})
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) adminCookieSecure(r *http.Request) bool {
	switch s.cfg.AdminCookieSecure {
	case "true":
		return true
	case "false":
		return false
	}
	if r.TLS != nil {
		return true
	}
	if s.cfg.TrustProxyHeader && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		return true
	}
	return false
}

func (s *Server) adminPage(w http.ResponseWriter, r *http.Request) {
	feedList, err := s.store.Feeds(r.Context(), false)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	users, err := s.store.Users(r.Context(), false)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	states, err := s.bgp.PeerStates(r.Context())
	if err != nil {
		log.Printf("read BGP peer states: %v", err)
		states = map[string]string{}
	}
	globalFilters, err := s.store.GlobalRouteFilters(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "title.admin", "admin", adminView{
		Feeds: feedList, Users: users, PeerStates: states,
		GlobalFilters: filterView{
			AllowText: strings.Join(globalFilters.Allow, "\n"),
			DenyText:  strings.Join(globalFilters.Deny, "\n"),
			Editable:  true,
			Admin:     true,
		},
	})
}

func (s *Server) addFeed(w http.ResponseWriter, r *http.Request) {
	feed, err := parseFeed(r, 0)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.store.AddFeed(r.Context(), feed.Name, feed.URL, feed.Enabled); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) updateFeed(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.httpError(w, r, "error.bad_feed_id", http.StatusBadRequest)
		return
	}
	feed, err := parseFeed(r, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.store.UpdateFeed(r.Context(), feed); err != nil {
		if store.IsNotFound(err) {
			http.NotFound(w, r)
		} else {
			http.Error(w, err.Error(), http.StatusBadRequest)
		}
		return
	}
	if err := s.bgp.Reconcile(r.Context()); err != nil {
		s.internalError(w, r, err)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) deleteFeed(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.httpError(w, r, "error.bad_feed_id", http.StatusBadRequest)
		return
	}
	if err := s.store.DeleteFeed(r.Context(), id); err != nil {
		if store.IsNotFound(err) {
			http.NotFound(w, r)
		} else {
			s.internalError(w, r, err)
		}
		return
	}
	if err := s.bgp.Reconcile(r.Context()); err != nil {
		s.internalError(w, r, err)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) syncFeeds(w http.ResponseWriter, r *http.Request) {
	for _, err := range s.syncer.SyncAll(r.Context()) {
		log.Printf("feed sync: %v", err)
	}
	if err := s.bgp.Reconcile(r.Context()); err != nil {
		s.internalError(w, r, err)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func parseFeed(r *http.Request, id int64) (store.Feed, error) {
	if err := r.ParseForm(); err != nil {
		return store.Feed{}, err
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		return store.Feed{}, fmt.Errorf("feed name is required")
	}
	rawURL := strings.TrimSpace(r.FormValue("url"))
	parsedURL, err := url.ParseRequestURI(rawURL)
	if err != nil || parsedURL.Host == "" ||
		(parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		return store.Feed{}, fmt.Errorf("feed URL must be an absolute HTTP or HTTPS URL")
	}
	return store.Feed{
		ID: id, Name: name, URL: rawURL, Enabled: r.Form.Has("enabled"),
	}, nil
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

func (s *Server) addUser(w http.ResponseWriter, r *http.Request) {
	user, _, err := parseUser(r, 0)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := s.store.AddUser(r.Context(), user); err != nil {
		s.internalError(w, r, err)
		return
	}
	if err := s.bgp.ReloadPeers(r.Context()); err != nil {
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
	user, err := s.store.User(r.Context(), id)
	if store.IsNotFound(err) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	selection, err := s.selection(r.Context(), user, true, true)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	lang, _ := requestLocale(r, s.defaultLang)
	s.renderTitle(w, r, http.StatusOK, fmt.Sprintf(translate(lang, "title.user"), user.Name), "user-edit",
		userEditView{User: user, Selection: selection})
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
		if err := s.store.UpdateUser(r.Context(), user, clearPassword); err != nil {
			s.internalError(w, r, err)
			return
		}
		err = s.bgp.ReloadPeers(r.Context())
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
		err = s.store.Transaction(r.Context(), func(tx *sql.Tx) error {
			return store.SetUserSelection(r.Context(), tx, id, categories, services)
		})
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
	if err := s.store.DeleteUser(r.Context(), id); err != nil {
		s.internalError(w, r, err)
		return
	}
	if err := s.bgp.ReloadPeers(r.Context()); err != nil {
		s.internalError(w, r, err)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DB.PingContext(r.Context()); err != nil {
		s.httpError(w, r, "error.database_unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}

func (s *Server) selection(ctx context.Context, user store.User, editable, admin bool) (selectionView, error) {
	catalog, err := s.store.Catalog(ctx)
	if err != nil {
		return selectionView{}, err
	}
	selectedCategories, selectedServices, err := s.store.UserSelection(ctx, user.ID)
	if err != nil {
		return selectionView{}, err
	}
	names := make([]string, 0, len(catalog))
	for name := range catalog {
		names = append(names, name)
	}
	sortStrings(names)
	selectedCount := len(selectedCategories)
	for service := range selectedServices {
		if !selectedCategories[service.Category] {
			selectedCount++
		}
	}
	userFilters, err := s.store.UserRouteFilters(ctx, user.ID)
	if err != nil {
		return selectionView{}, err
	}
	view := selectionView{
		User: user, Editable: editable, Admin: admin,
		SelectedCount: selectedCount,
		Filters: filterView{
			AllowText: strings.Join(userFilters.Allow, "\n"),
			DenyText:  strings.Join(userFilters.Deny, "\n"),
			Override:  user.FilterOverride,
			Mode:      user.FilterMode,
			Editable:  admin || user.FilterEditable,
			Admin:     admin,
		},
	}
	for _, category := range names {
		categorySelected := selectedCategories[category]
		item := categoryView{Name: category, Selected: categorySelected}
		for _, service := range catalog[category] {
			key := store.ServiceKey{Category: category, Service: service}
			item.Services = append(item.Services, serviceView{
				Name: service, Value: serviceValue(category, service),
				Selected: !categorySelected && selectedServices[key],
				Disabled: categorySelected,
			})
		}
		view.Categories = append(view.Categories, item)
	}
	return view, nil
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

func routeFiltersFromForm(r *http.Request) (store.RouteFilters, error) {
	filters := store.RouteFilters{
		Allow: splitCIDRs(r.FormValue("filter_allow")),
		Deny:  splitCIDRs(r.FormValue("filter_deny")),
	}
	return store.NormalizeRouteFilters(filters)
}

func splitCIDRs(value string) []string {
	return strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
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
	return store.User{
		ID: id, Name: name, PeerIP: peerIP.String(), PeerASN: uint32(peerASN),
		NextHop: nextHop, BGPPassword: r.FormValue("bgp_password"),
		SelectionLocked: r.Form.Has("locked"), Enabled: id == 0 || r.Form.Has("enabled"),
		FilterOverride: r.FormValue("filter_override") == "on",
		FilterMode:     r.FormValue("filter_mode"),
		FilterEditable: r.Form.Has("filter_editable"),
		Networks:       networks,
	}, r.Form.Has("clear_bgp_password"), nil
}

func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("wdbgp_admin")
		if err != nil || !validSession(s.cfg.SessionSecret, cookie.Value) {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

func (s *Server) clientIP(r *http.Request) string {
	if s.cfg.TrustProxyHeader {
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			return strings.TrimSpace(strings.SplitN(forwarded, ",", 2)[0])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func compileTemplates() map[locale]map[string]*template.Template {
	bodies := map[string]string{
		"access-denied": accessDeniedTemplate,
		"login":         loginTemplate,
		"selection":     selectionTemplate,
		"admin":         adminTemplate,
		"user-edit":     userEditTemplate,
	}
	result := make(map[locale]map[string]*template.Template, len(translations))
	for lang := range translations {
		result[lang] = make(map[string]*template.Template, len(bodies))
		for name, body := range bodies {
			funcs := template.FuncMap{
				"join": strings.Join,
				"state": func(states map[string]string, peer string) string {
					if value := states[peer]; value != "" {
						return value
					}
					return "UNKNOWN"
				},
				"tr": func(key string) string {
					return translate(lang, key)
				},
			}
			result[lang][name] = template.Must(template.New("page").Funcs(funcs).
				Parse(pageStart + body + pageEnd))
		}
	}
	return result
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, titleKey, name string, data any) {
	lang, _ := requestLocale(r, s.defaultLang)
	s.renderTitle(w, r, status, translate(lang, titleKey), name, data)
}

func (s *Server) renderTitle(w http.ResponseWriter, r *http.Request, status int, title, name string, data any) {
	lang, persist := requestLocale(r, s.defaultLang)
	if persist {
		http.SetCookie(w, &http.Cookie{
			Name: languageCookieName, Value: string(lang), Path: "/",
			MaxAge: 365 * 24 * 60 * 60, SameSite: http.SameSiteLaxMode,
		})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := s.templates[lang][name].Execute(w, struct {
		Title      string
		Lang       string
		EnglishURL string
		RussianURL string
		Data       any
	}{
		Title: title, Lang: string(lang),
		EnglishURL: languageURL(r, localeEnglish),
		RussianURL: languageURL(r, localeRussian),
		Data:       data,
	}); err != nil {
		log.Printf("render %s: %v", title, err)
	}
}

func (s *Server) httpError(w http.ResponseWriter, r *http.Request, key string, status int) {
	lang, _ := requestLocale(r, s.defaultLang)
	http.Error(w, translate(lang, key), status)
}

func (s *Server) internalError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	log.Printf("request failed: %v", err)
	s.httpError(w, r, "error.internal", http.StatusInternalServerError)
}

func pathID(r *http.Request) (int64, error) {
	return strconv.ParseInt(r.PathValue("id"), 10, 64)
}

func sessionToken(secret string) string {
	var nonce [16]byte
	_, _ = rand.Read(nonce[:])
	text := hex.EncodeToString(nonce[:])
	signature := hmac.New(sha256.New, []byte(secret))
	_, _ = signature.Write([]byte(text))
	return text + "." + hex.EncodeToString(signature.Sum(nil))
}

func validSession(secret, value string) bool {
	parts := strings.SplitN(value, ".", 2)
	if len(parts) != 2 {
		return false
	}
	signature, err := hex.DecodeString(parts[1])
	if err != nil {
		return false
	}
	expected := hmac.New(sha256.New, []byte(secret))
	_, _ = expected.Write([]byte(parts[0]))
	return hmac.Equal(signature, expected.Sum(nil))
}

func serviceValue(category, service string) string {
	return url.QueryEscape(category) + ":" + url.QueryEscape(service)
}

func uniqueNonEmpty(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sortStrings(result)
	return result
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s from %s", r.Method, r.URL.Path, r.RemoteAddr)
		next.ServeHTTP(w, r)
	})
}
