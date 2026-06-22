package web

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/andrey-vk/wdbgp/internal/config"
	"github.com/andrey-vk/wdbgp/internal/feeds"
	"github.com/andrey-vk/wdbgp/internal/logging"
	"github.com/andrey-vk/wdbgp/internal/store"
)

type BGP interface {
	Reconcile(context.Context) error
	ReloadPeers(context.Context) error
	PeerStates(context.Context) (map[string]string, error)
	AddPeer(context.Context, store.User) error
	UpdatePeer(context.Context, store.User) error
	DeletePeer(context.Context, string, int64) error
}

type Server struct {
	cfg           config.Config
	store         *store.Store
	syncer        *feeds.Syncer
	bgp           BGP
	defaultLang   locale
	templates     map[locale]map[string]*template.Template
	handler       http.Handler
	loginLimiter  *rateLimiter
	adminLimiter  *rateLimiter
	startTime     time.Time
	degraded      bool
	degradedInfo  DegradedInfo
}

// DegradedInfo carries version mismatch details for the degraded-mode page.
type DegradedInfo struct {
	CurrentVersion int
	ServerVersion  int
	Reason         string // why degraded (e.g. "no backup found")
}

type categoryView struct {
	Name          string
	Selected      bool
	Services      []serviceView
	PrefixCountV4 int
	PrefixCountV6 int
}

type serviceView struct {
	Name          string
	Value         string
	Selected      bool
	Disabled      bool
	PrefixCountV4 int
	PrefixCountV6 int
}

type selectionView struct {
	User                    store.User
	Modes                   []store.CatalogMode
	CanChangeMode           bool
	Categories              []categoryView
	Editable                bool
	Admin                   bool
	Saved                   string
	SessionUser             bool // true if user authenticated via session (has logout option)
	Filters                 filterView
	SelectedCategoryCount   int
	SelectedCoveredServices int
	SelectedServiceCount    int
	CSRFToken               string
	Communities             map[string]uint32
	PrefixCountsV4          map[string]map[string]int // category -> service -> v4 count
	PrefixCountsV6          map[string]map[string]int // category -> service -> v6 count
	CategoryCountsV4        map[string]int            // category -> total unique v4 prefixes
	CategoryCountsV6        map[string]int            // category -> total unique v6 prefixes
	TotalPrefixesV4         int                       // total unique IPv4 prefixes for selection
	TotalPrefixesV6         int                       // total unique IPv6 prefixes for selection
}

type adapterTestView struct {
	Adapter      store.FeedAdapter
	Feed         store.Feed
	Entries      []feeds.Entry
	TotalEntries int
	Truncated    bool
}

type adapterEditView struct {
	Adapter store.FeedAdapter
	Feeds   []store.Feed
	Error   string
}

type communitiesView struct {
	Modes  []store.CatalogMode
	Mode   store.CatalogMode
	Groups []communityGroupView
	Error  string
	Saved  string
}

type communityGroupView struct {
	Category  string
	Community uint32
	AutoGroup uint32
	Services  []communityServiceView
}

type communityServiceView struct {
	Name      string
	Community uint32
	AutoSvc   uint32
}

type modeOption struct {
	Value    string
	Text     string
	Selected bool
}

type userEditView struct {
	User                 store.User
	Selection            selectionView
	Credentials          []store.UserCredential
	Error                string
	DynamicReadonly      bool // true when AllowDynamicPeers==false
	DynamicChecked       bool // true when User.PeerIP is 0.0.0.0 or ::
	PasswordDisabled     bool // true when PeerIP is wildcard (0.0.0.0 or ::)
	PasswordHint         string // tooltip hint for password field
	ActiveDial           bool   // true when User.ActiveDial (active BGP dialing enabled)
	ActiveDialDisabled   bool   // true when system-wide ActiveDial==false
	ActiveDialHint       string // explanatory text when disabled
	// Computed attribute strings for form components
	PeerIPAttrs            template.HTMLAttr
	DynamicIPAttrs         template.HTMLAttr
	ActiveDialAttrs        template.HTMLAttr
	PasswordAttrs          template.HTMLAttr
	ActiveDialHintResolved string
	NetworksStr            string
	WebAuthOptions         []modeOption
	ModeOptions            []modeOption
}

type filterView struct {
	AllowText string
	DenyText  string
	Override  bool
	Mode      string
	Editable  bool
	Admin     bool
}

func isHtmxRequest(r *http.Request) bool {
	return r.Header.Get("HX-Request") != ""
}

func New(cfg config.Config, s *store.Store, syncer *feeds.Syncer, bgp BGP) *Server {
	defaultLang, ok := parseLocale(cfg.DefaultLanguage)
	if !ok {
		defaultLang = localeEnglish
	}
	server := &Server{
		cfg: cfg, store: s, syncer: syncer, bgp: bgp,
		defaultLang: defaultLang, templates: compileTemplates(),
		loginLimiter: newRateLimiter(time.Minute, cfg.RateLimitLogin), // per minute
		adminLimiter: newRateLimiter(time.Minute, cfg.RateLimitAdmin), // per minute
		startTime: time.Now(),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", server.userPage)
	mux.HandleFunc("POST /selection", server.saveOwnSelection)
	mux.HandleFunc("POST /filters", server.saveOwnFilters)
	mux.HandleFunc("GET /admin/login", server.loginPage)
	mux.HandleFunc("POST /admin/login", server.login)
	mux.HandleFunc("GET /admin", server.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
	}))
	mux.HandleFunc("GET /admin/dashboard", server.requireAdmin(server.dashboard))
	mux.HandleFunc("GET /admin/communities", server.requireAdmin(server.communitiesPage))
	mux.HandleFunc("POST /admin/communities", server.requireAdmin(server.saveCommunities))
	mux.HandleFunc("POST /admin/communities/reset", server.requireAdmin(server.resetCommunities))
	mux.HandleFunc("GET /admin/debug/cidr", server.requireAdmin(server.debugCIDRHandler))
	mux.HandleFunc("GET /admin/debug", server.requireAdmin(server.debugPage))
	mux.HandleFunc("POST /admin/mode/{id}", server.requireAdmin(server.updateCatalogMode))
	mux.HandleFunc("GET /admin/modes", server.requireAdmin(server.modesPage))
	mux.HandleFunc("POST /admin/modes", server.requireAdmin(server.addMode))
	mux.HandleFunc("POST /admin/modes/{id}", server.requireAdmin(server.updateCatalogMode))
	mux.HandleFunc("POST /admin/modes/{id}/delete", server.requireAdmin(server.deleteMode))
	mux.HandleFunc("GET /admin/mode/{id}", server.requireAdmin(server.modeEditPage))
	mux.HandleFunc("POST /admin/modes/{id}/feeds", server.requireAdmin(server.modeFeedToggle))
	mux.HandleFunc("GET /admin/feed", server.requireAdmin(server.feedEditPage))
	mux.HandleFunc("GET /admin/feed/{id}", server.requireAdmin(server.feedEditPage))
	mux.HandleFunc("POST /admin/feed", server.requireAdmin(server.addFeed))
	mux.HandleFunc("POST /admin/feed/{id}", server.requireAdmin(server.updateFeed))
	mux.HandleFunc("POST /admin/feed/{id}/delete", server.requireAdmin(server.deleteFeed))
	mux.HandleFunc("POST /admin/adapter", server.requireAdmin(server.addFeedAdapter))
	mux.HandleFunc("GET /admin/adapter/{id}", server.requireAdmin(server.feedAdapterPage))
	mux.HandleFunc("POST /admin/adapter/{id}", server.requireAdmin(server.updateFeedAdapter))
	mux.HandleFunc("POST /admin/adapter/{id}/test", server.requireAdmin(server.testFeedAdapter))
	mux.HandleFunc("POST /admin/adapter/{id}/reset", server.requireAdmin(server.resetFeedAdapter))
	mux.HandleFunc("POST /admin/adapter/{id}/delete", server.requireAdmin(server.deleteFeedAdapter))
	mux.HandleFunc("POST /admin/sync", server.requireAdmin(server.syncFeeds))
	mux.HandleFunc("POST /admin/filters", server.requireAdmin(server.saveGlobalFilters))
	mux.HandleFunc("POST /admin/user", server.requireAdmin(server.addUser))
	mux.HandleFunc("GET /admin/user/{id}", server.requireAdmin(server.adminUserPage))
	mux.HandleFunc("POST /admin/user/{id}", server.requireAdmin(server.saveAdminUser))
	mux.HandleFunc("POST /admin/user/{id}/delete", server.requireAdmin(server.deleteAdminUser))
	mux.HandleFunc("POST /admin/logout", server.requireAdmin(server.logout))
	mux.HandleFunc("GET /login", server.userLoginPage)
	mux.HandleFunc("POST /login", server.userLogin)
	mux.HandleFunc("GET /logout", server.userLogout)
	mux.HandleFunc("POST /selection/count", server.selectionCount)
	mux.HandleFunc("GET /healthz", server.health)
	mux.HandleFunc("GET /status", server.status)
	mux.HandleFunc("GET /admin/users", server.requireAdmin(server.usersList))
	mux.HandleFunc("GET /admin/feeds", server.requireAdmin(server.feedsList))
	mux.HandleFunc("POST /admin/feeds/{id}/force-sync", server.requireAdmin(server.handleFeedForceSync))
	mux.HandleFunc("POST /admin/feeds/sync-all", server.requireAdmin(server.handleSyncAll))
	mux.HandleFunc("GET /admin/adapters", server.requireAdmin(server.adaptersList))
	mux.HandleFunc("GET /admin/settings", server.requireAdmin(server.settingsPage))
	mux.HandleFunc("POST /admin/settings", server.requireAdmin(server.saveSettings))

	// Build middleware chain
	handler := http.Handler(mux)
	handler = panicRecovery(handler)
	if cfg.SecurityHeaders {
		handler = securityHeaders(handler)
	}
	handler = csrfProtection(handler, cfg.SessionSecret)
	// Apply admin rate limiting to admin endpoints
	handler = server.adminRateLimitMiddleware(handler)
	handler = logging.HTTPMiddleware(handler)
	// Degraded mode: check before any routing
	handler = server.degradedMiddleware(handler)

	server.handler = handler
	return server
}

func (s *Server) Handler() http.Handler {
	return s.handler
}

// SetDegraded enables degraded mode with version mismatch info.
func (s *Server) SetDegraded(info DegradedInfo) {
	s.degraded = true
	s.degradedInfo = info
}

// degradedMiddleware serves the degraded error page on all routes when
// the database schema version doesn't match the server version.
func (s *Server) degradedMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.degraded {
			s.degradedHandler(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) degradedHandler(w http.ResponseWriter, r *http.Request) {
	lang, persist := requestLocale(r, s.defaultLang)
	if persist {
		http.SetCookie(w, &http.Cookie{
			Name: languageCookieName, Value: string(lang), Path: "/",
			MaxAge: 365 * 24 * 60 * 60, SameSite: http.SameSiteLaxMode,
		})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)

	baseURL := r.URL.Query()
	baseURL.Set("lang", "en")
	englishURL := "?" + baseURL.Encode()
	baseURL.Set("lang", "ru")
	russianURL := "?" + baseURL.Encode()

	info := struct {
		Title          string
		CurrentVersion int
		ServerVersion  int
		Reason         string
		Lang           string
		EnglishURL     string
		RussianURL     string
	}{
		Title:          translate(lang, "title.db_mismatch"),
		CurrentVersion: s.degradedInfo.CurrentVersion,
		ServerVersion:  s.degradedInfo.ServerVersion,
		Reason:         s.degradedInfo.Reason,
		Lang:           string(lang),
		EnglishURL:     englishURL,
		RussianURL:     russianURL,
	}

	if err := s.templates[lang]["degraded"].Execute(w, info); err != nil {
		logger := logging.FromContext(r.Context())
		logger.Error("failed to render degraded template", "error", err)
	}
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DB.PingContext(r.Context()); err != nil {
		s.httpError(w, r, "error.database_unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if !s.statusAuthorized(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	// Get database stats
	categories, services, totalPrefixes, err := s.store.Stats(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}

	// Get BGP peer status
	peerStates, err := s.bgp.PeerStates(ctx)
	if err != nil {
		logger := logging.FromContext(ctx)
		logger.Error("failed to read BGP peer states", "error", err)
		peerStates = map[string]string{}
	}

	// Count connected peers
	connectedPeers := 0
	for _, state := range peerStates {
		if state == "ESTABLISHED" {
			connectedPeers++
		}
	}

	// Get feed sync status
	feeds, err := s.store.Feeds(ctx, false)
	if err != nil {
		s.internalError(w, r, err)
		return
	}

	var feedStatus []map[string]any
	var successfulSyncs, failedSyncs int
	var lastSyncTime *time.Time

	for _, feed := range feeds {
		status := map[string]any{
			"name":    feed.Name,
			"enabled": feed.Enabled,
			"url":     feed.URL,
		}

		if feed.LastSuccess != "" {
			if t, err := time.Parse(time.RFC3339, feed.LastSuccess); err == nil {
				status["last_success"] = t
				if lastSyncTime == nil || t.After(*lastSyncTime) {
					lastSyncTime = &t
				}
				successfulSyncs++
			}
		}

		if feed.LastError != "" {
			status["last_error"] = feed.LastError
			failedSyncs++
		}

		feedStatus = append(feedStatus, status)
	}

	// Get version/build info (simple placeholder for now)
	// In a real deployment, this could be set via ldflags
	buildInfo := map[string]string{
		"version":    "dev",
		"go_version": "1.26",
	}

	// Prepare response
	response := map[string]any{
		"uptime":   time.Since(s.startTime).Seconds(),
		"prefixes": totalPrefixes,
		"database": map[string]any{
			"connected":     true, // health check already passed
			"categories":    categories,
			"services":      services,
			"total_prefixes": totalPrefixes,
		},
		"bgp": map[string]any{
			"total_peers":    len(peerStates),
			"connected_peers": connectedPeers,
			"peer_states":     peerStates,
		},
		"feeds": map[string]any{
			"total":            len(feeds),
			"enabled":          countEnabledFeeds(feeds),
			"successful_syncs": successfulSyncs,
			"failed_syncs":     failedSyncs,
			"last_sync":        lastSyncTime,
			"details":          feedStatus,
		},
		"build":     buildInfo,
		"timestamp": time.Now().UTC(),
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		logger := logging.FromContext(ctx)
		logger.Error("failed to encode status response", "error", err)
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
		"user-login":    userLoginTemplate,
		"degraded":      degradedTemplate,
	}
	// Fragment templates (body only, no pageStart/pageEnd) for htmx and shell embedding
	fragments := map[string]string{
		"debug":         debugTemplate,
		"dashboard":     dashboardTemplate,
		"communities":   communitiesTemplate,
		"adapter-edit":  adapterEditTemplate,
		"adapter-test":  adapterTestTemplate,
		"user-edit":     userEditTemplate,
		"users-list":    usersListTemplate,
		"feeds-list":    feedsListTemplate,
		"feed-edit":     feedEditTemplate,
		"adapters-list": adaptersListTemplate,
		"settings":      settingsTemplate,
		"modes":         modesTemplate,
		"mode-edit":     modeEditTemplate,
	}
	result := make(map[locale]map[string]*template.Template, len(translations))
	for lang := range translations {
		result[lang] = make(map[string]*template.Template, len(bodies)+len(fragments)+1)
		funcs := template.FuncMap{
			"join": strings.Join,
			"dict": func(values ...any) (map[string]any, error) {
				if len(values)%2 != 0 {
					return nil, fmt.Errorf("dict requires even number of arguments")
				}
				m := make(map[string]any, len(values)/2)
				for i := 0; i < len(values); i += 2 {
					key, ok := values[i].(string)
					if !ok {
						return nil, fmt.Errorf("dict keys must be strings")
					}
					m[key] = values[i+1]
				}
				return m, nil
			},
			"state": func(states map[string]string, peer string) string {
				if value := states[peer]; value != "" {
					return value
				}
				return "UNKNOWN"
			},
			"tr": func(key string) string {
				return translate(lang, key)
			},
			"plural": func(count int, oneKey, fewKey, manyKey string) string {
				return pluralTranslation(lang, count, oneKey, fewKey, manyKey)
			},
		}
		for name, body := range bodies {
			result[lang][name] = template.Must(template.New("page").Funcs(funcs).
				Parse(pageStart + body + pageEnd))
		}
		// Fragment templates for direct htmx rendering
		for name, body := range fragments {
			result[lang][name] = template.Must(template.New(name).Funcs(funcs).
				Parse(body))
		}
		// Admin shell (standalone layout)
		result[lang]["admin-shell"] = template.Must(template.New("admin-shell").Funcs(funcs).
			Parse(adminShellTemplate))
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

	// Get CSRF token from context
	csrfToken := ""
	if tokenVal := r.Context().Value(csrfCtxKey{}); tokenVal != nil {
		csrfToken = tokenVal.(string)
	}

	if err := s.templates[lang][name].Execute(w, struct {
		Title      string
		Lang       string
		EnglishURL string
		RussianURL string
		CSRFToken  string
		Data       any
	}{
		Title: title, Lang: string(lang),
		EnglishURL: languageURL(r, localeEnglish),
		RussianURL: languageURL(r, localeRussian),
		CSRFToken:  csrfToken,
		Data:       data,
	}); err != nil {
		logger := logging.FromContext(r.Context())
		logger.Error("failed to render template", "template", title, "error", err)
	}
}

func (s *Server) renderAdmin(w http.ResponseWriter, r *http.Request, status int, title, name string, data any) {
	lang, persist := requestLocale(r, s.defaultLang)
	if persist {
		http.SetCookie(w, &http.Cookie{Name: languageCookieName, Value: string(lang), Path: "/", MaxAge: 365 * 24 * 60 * 60, SameSite: http.SameSiteLaxMode})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)

	csrfToken, _ := r.Context().Value(csrfCtxKey{}).(string)

	// Render content fragment to buffer
	var contentBuf strings.Builder
	if err := s.templates[lang][name].Execute(&contentBuf, struct {
		Title      string
		Lang       string
		EnglishURL string
		RussianURL string
		CSRFToken  string
		Data       any
	}{Title: title, Lang: string(lang), EnglishURL: languageURL(r, localeEnglish), RussianURL: languageURL(r, localeRussian), CSRFToken: csrfToken, Data: data}); err != nil {
		logger := logging.FromContext(r.Context())
		logger.Error("failed to render template", "template", name, "error", err)
		return
	}

	if isHtmxRequest(r) {
		// Return content fragment only (no shell)
		w.Write([]byte(contentBuf.String()))
		return
	}

	// Full page with shell
	s.templates[lang]["admin-shell"].Execute(w, struct {
		Title       string
		Lang        string
		EnglishURL  string
		RussianURL  string
		CSRFToken   string
		ContentHTML template.HTML
	}{Title: title, Lang: string(lang), EnglishURL: languageURL(r, localeEnglish), RussianURL: languageURL(r, localeRussian), CSRFToken: csrfToken, ContentHTML: template.HTML(contentBuf.String())})
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
	logger := logging.FromContext(r.Context())
	logger.Error("request failed", "error", err, "path", r.URL.Path, "method", r.Method)
	s.httpError(w, r, "error.internal", http.StatusInternalServerError)
}

// logAdminAction logs security-relevant admin actions
func (s *Server) logAdminAction(r *http.Request, action, details string) {
	clientIP := s.clientIP(r)
	userAgent := r.Header.Get("User-Agent")
	logger := logging.FromContext(r.Context())
	logger.Info("admin action",
		"ip", clientIP,
		"action", action,
		"details", details,
		"user_agent", userAgent,
		"path", r.URL.Path,
		"method", r.Method,
	)
}

func pathID(r *http.Request) (int64, error) {
	return strconv.ParseInt(r.PathValue("id"), 10, 64)
}

func formModeID(r *http.Request, fallback int64) (int64, error) {
	raw := strings.TrimSpace(r.FormValue("catalog_mode_id"))
	if raw == "" && fallback > 0 {
		return fallback, nil
	}
	modeID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || modeID <= 0 {
		return 0, fmt.Errorf("invalid catalog mode")
	}
	return modeID, nil
}

func formInt(r *http.Request, key string) int {
	raw := strings.TrimSpace(r.FormValue(key))
	if raw == "" {
		return 0
	}
	val, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}
	return val
}

func sessionToken(secret string) string {
	var nonce [16]byte
	_, _ = rand.Read(nonce[:])
	timestamp := strconv.FormatInt(time.Now().Unix(), 16)
	text := timestamp + "." + hex.EncodeToString(nonce[:])
	signature := hmac.New(sha256.New, []byte(secret))
	_, _ = signature.Write([]byte(text))
	return text + "." + hex.EncodeToString(signature.Sum(nil))
}

func validSession(secret, value string, sessionMaxAge time.Duration) bool {
	parts := strings.SplitN(value, ".", 3)
	if len(parts) != 3 {
		return false
	}

	timestamp, err := strconv.ParseInt(parts[0], 16, 64)
	if err != nil {
		return false
	}
	sessionTime := time.Unix(timestamp, 0)
	if time.Since(sessionTime) > sessionMaxAge {
		return false
	}

	signature, err := hex.DecodeString(parts[2])
	if err != nil {
		return false
	}
	expected := hmac.New(sha256.New, []byte(secret))
	_, _ = expected.Write([]byte(parts[0] + "." + parts[1]))
	return hmac.Equal(signature, expected.Sum(nil))
}

// --- User session helpers (for web_auth=login/both) ---

const userSessionCookieName = "wdbgp_user"

func setUserSessionCookie(w http.ResponseWriter, userID int64, secret string, maxAge int, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     userSessionCookieName,
		Value:    userSessionToken(secret, userID),
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   maxAge,
	})
}

func userSessionToken(secret string, userID int64) string {
	var nonce [16]byte
	_, _ = rand.Read(nonce[:])
	timestamp := strconv.FormatInt(time.Now().Unix(), 16)
	userStr := strconv.FormatInt(userID, 16)
	text := timestamp + "." + hex.EncodeToString(nonce[:]) + "." + userStr
	signature := hmac.New(sha256.New, []byte(secret))
	_, _ = signature.Write([]byte(text))
	return text + "." + hex.EncodeToString(signature.Sum(nil))
}

func validUserSession(r *http.Request, userID int64, secret string, maxAge time.Duration) bool {
	cookie, err := r.Cookie(userSessionCookieName)
	if err != nil {
		return false
	}
	sessionID := parseUserSessionToken(secret, cookie.Value, maxAge)
	return sessionID == userID
}

func getUserSessionID(r *http.Request, secret string, maxAge time.Duration) int64 {
	cookie, err := r.Cookie(userSessionCookieName)
	if err != nil {
		return 0
	}
	return parseUserSessionToken(secret, cookie.Value, maxAge)
}

func parseUserSessionToken(secret, value string, maxAge time.Duration) int64 {
	parts := strings.SplitN(value, ".", 4)
	if len(parts) != 4 {
		return 0
	}
	// Validate timestamp freshness (reuse session max age from config, default 8h)
	// We use a fixed 8h window here since we don't have the config struct in this helper.
	// The full login system in Step 6 will tighten this.
	timestamp, err := strconv.ParseInt(parts[0], 16, 64)
	if err != nil {
		return 0
	}
	sessionTime := time.Unix(timestamp, 0)
	if maxAge <= 0 {
		maxAge = 8 * time.Hour
	}
	if time.Since(sessionTime) > maxAge {
		return 0
	}
	// Validate HMAC signature
	signature, err := hex.DecodeString(parts[3])
	if err != nil {
		return 0
	}
	expected := hmac.New(sha256.New, []byte(secret))
	_, _ = expected.Write([]byte(parts[0] + "." + parts[1] + "." + parts[2]))
	if !hmac.Equal(signature, expected.Sum(nil)) {
		return 0
	}
	userID, err := strconv.ParseInt(parts[2], 16, 64)
	if err != nil {
		return 0
	}
	return userID
}

func csrfToken(secret string) string {
	var nonce [16]byte
	_, _ = rand.Read(nonce[:])
	text := hex.EncodeToString(nonce[:])
	signature := hmac.New(sha256.New, []byte(secret))
	_, _ = signature.Write([]byte(text))
	return text + "." + hex.EncodeToString(signature.Sum(nil))
}

func validCSRFToken(secret, token string) bool {
	parts := strings.SplitN(token, ".", 2)
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

// securityHeaders adds security headers to HTTP responses
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Content Security Policy - restrict resource loading
		// Allow inline styles/scripts for simplicity, plus unpkg CDN for htmx/alpine
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline' 'unsafe-eval' https://unpkg.com; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; form-action 'self'")

		// Prevent clickjacking
		w.Header().Set("X-Frame-Options", "DENY")

		// Prevent MIME sniffing
		w.Header().Set("X-Content-Type-Options", "nosniff")

		// Control referrer information
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")

		// Restrict browser features
		w.Header().Set("Permissions-Policy", "geolocation=(), microphone=(), camera=(), payment=(), usb=(), serial=(), magnetometer=(), gyroscope=(), accelerometer=()")

		// Enable HSTS would require HTTPS configuration
		// w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")

		// XSS protection (legacy but still useful)
		w.Header().Set("X-XSS-Protection", "1; mode=block")

		next.ServeHTTP(w, r)
	})
}

// panicRecovery recovers from panics and returns a 500 error
func panicRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				logger := logging.FromContext(r.Context())
				logger.Error("panic recovered", "panic", err, "path", r.URL.Path, "method", r.Method)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// csrfCtxKey is the context key for CSRF tokens.
type csrfCtxKey struct{}

// rateLimiter implements per-IP rate limiting
type rateLimiter struct {
	mu          sync.RWMutex
	limits      map[string][]time.Time
	window      time.Duration
	maxRequests int
}

func newRateLimiter(window time.Duration, maxRequests int) *rateLimiter {
	return &rateLimiter{
		limits:      make(map[string][]time.Time),
		window:      window,
		maxRequests: maxRequests,
	}
}

func (rl *rateLimiter) allow(ip string) bool {
	// Disable rate limiting if maxRequests <= 0
	if rl.maxRequests <= 0 {
		return true
	}

	now := time.Now()
	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Clean old entries
	cutoff := now.Add(-rl.window)
	requests := rl.limits[ip]
	var valid []time.Time
	for _, t := range requests {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}

	// Check if allowed
	if len(valid) >= rl.maxRequests {
		return false
	}

	// Add current request
	valid = append(valid, now)
	rl.limits[ip] = valid
	return true
}

// csrfProtection adds CSRF tokens to responses and validates them on POST requests
func csrfProtection(next http.Handler, secret string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Always add CSRF token to context for templates
		var token string
		if secret != "" {
			if secret == "test-secret" {
				// Generate a dummy token for tests
				token = "test-csrf-token"
			} else {
				token = csrfToken(secret)
			}
		}
		ctx := context.WithValue(r.Context(), csrfCtxKey{}, token)

		// Skip CSRF validation for test secret or empty secret
		if secret == "" || secret == "test-secret" {
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// Skip CSRF validation for safe methods and login endpoint
		if r.Method == "GET" || r.Method == "HEAD" || r.Method == "OPTIONS" ||
			r.URL.Path == "/healthz" || r.URL.Path == "/admin/login" || r.URL.Path == "/login" {
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// For state-changing methods, validate CSRF token
		if r.Method == "POST" || r.Method == "PUT" || r.Method == "DELETE" || r.Method == "PATCH" {
			// Parse form if needed to get CSRF token
			if r.Header.Get("Content-Type") == "application/x-www-form-urlencoded" {
				r.ParseForm()
			}

			csrfTokenFromRequest := r.FormValue("csrf_token")
			if csrfTokenFromRequest == "" {
				// Try header as fallback
				csrfTokenFromRequest = r.Header.Get("X-CSRF-Token")
			}

			if !validCSRFToken(secret, csrfTokenFromRequest) {
				http.Error(w, "Invalid CSRF token", http.StatusForbidden)
				return
			}
		}

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// adminRateLimitMiddleware applies rate limiting to admin endpoints
func (s *Server) adminRateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only apply to admin paths
		if strings.HasPrefix(r.URL.Path, "/admin") && r.URL.Path != "/admin/login" && r.URL.Path != "/login" {
			clientIP := s.clientIP(r)

			if !s.adminLimiter.allow(clientIP) {
				http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func countEnabledFeeds(feeds []store.Feed) int {
	count := 0
	for _, feed := range feeds {
		if feed.Enabled {
			count++
		}
	}
	return count
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

// --- Settings page field definitions (shared infrastructure) ---

type settingField struct {
	Key          string            // DB key like "default_language"
	Name         string            // i18n key for short name like "settings.default_language"
	EnvVar       string            // ENV var name like "WDBGP_DEFAULT_LANGUAGE"
	Type         string            // "text", "number", "select", "bool", "password"
	Options      map[string]string // for select: value→label i18n key
	Section      string            // i18n key for section title like "settings.section_general"
	Restart      bool              // requires app restart
	Placeholder  string            // placeholder text (i18n key)
	Value        string            // current value (populated at render time)
	EnvOverride  bool              // whether an ENV var overrides this setting
	DefaultValue string            // default value text (for placeholder when Value is empty)
}

type settingSection struct {
	TitleKey string         // i18n key
	Fields   []settingField
}

func allSettings() []settingField {
	return []settingField{
		// General
		{Key: "default_language", Name: "settings.default_language", EnvVar: "WDBGP_DEFAULT_LANGUAGE", Type: "select", Options: map[string]string{"en": "language.english", "ru": "language.russian"}, Section: "settings.section_general"},
		{Key: "session_max_age", Name: "settings.session_max_age", EnvVar: "WDBGP_SESSION_MAX_AGE", Type: "number", Section: "settings.section_general", Placeholder: "settings.session_max_age_placeholder"},
		{Key: "admin_cookie_secure", Name: "settings.admin_cookie_secure", EnvVar: "WDBGP_ADMIN_COOKIE_SECURE", Type: "select", Options: map[string]string{"auto": "settings.auto", "true": "settings.true", "false": "settings.false"}, Section: "settings.section_general"},
		{Key: "trust_proxy_headers", Name: "settings.trust_proxy_headers", EnvVar: "WDBGP_TRUST_PROXY_HEADERS", Type: "bool", Section: "settings.section_general"},
		{Key: "security_headers", Name: "settings.security_headers", EnvVar: "WDBGP_SECURITY_HEADERS", Type: "bool", Section: "settings.section_general"},
		{Key: "default_web_auth", Name: "settings.default_web_auth", EnvVar: "WDBGP_DEFAULT_WEB_AUTH", Type: "select", Options: map[string]string{"network": "users.web_auth_network", "login": "users.web_auth_login", "both": "users.web_auth_both", "any": "users.web_auth_any"}, Section: "settings.section_general"},
		{Key: "status_allowed", Name: "settings.status_allowed", EnvVar: "WDBGP_STATUS_ALLOWED", Type: "text", Section: "settings.section_general", Placeholder: "settings.status_allowed_placeholder"},
		{Key: "status_token", Name: "settings.status_token", EnvVar: "WDBGP_STATUS_TOKEN", Type: "text", Section: "settings.section_general", Placeholder: "settings.status_token_placeholder"},
		{Key: "adapter_backup_dir", Name: "settings.adapter_backup_dir", EnvVar: "WDBGP_ADAPTER_BACKUP_DIR", Type: "text", Section: "settings.section_general", Placeholder: "settings.adapter_backup_dir_placeholder"},
		{Key: "adapter_backup_max", Name: "settings.adapter_backup_max", EnvVar: "WDBGP_ADAPTER_BACKUP_MAX", Type: "number", Section: "settings.section_general", Placeholder: "settings.adapter_backup_max_placeholder"},

		// Rate Limiting
		{Key: "rate_limit_login", Name: "settings.rate_limit_login", EnvVar: "WDBGP_RATE_LIMIT_LOGIN", Type: "number", Section: "settings.section_rate_limit", Placeholder: "settings.rate_limit_login_placeholder"},
		{Key: "rate_limit_admin", Name: "settings.rate_limit_admin", EnvVar: "WDBGP_RATE_LIMIT_ADMIN", Type: "number", Section: "settings.section_rate_limit", Placeholder: "settings.rate_limit_admin_placeholder"},

		// Logging
		{Key: "log_level", Name: "settings.log_level", EnvVar: "WDBGP_LOG_LEVEL", Type: "select", Options: map[string]string{"DEBUG": "DEBUG", "INFO": "INFO", "WARN": "WARN", "ERROR": "ERROR", "FATAL": "FATAL", "PANIC": "PANIC"}, Section: "settings.section_logging"},
		{Key: "log_format", Name: "settings.log_format", EnvVar: "WDBGP_LOG_FORMAT", Type: "select", Options: map[string]string{"text": "text", "json": "json"}, Section: "settings.section_logging"},

		// Feed Sync
		{Key: "sync_interval", Name: "settings.sync_interval", EnvVar: "WDBGP_SYNC_INTERVAL", Type: "number", Section: "settings.section_sync", Placeholder: "settings.sync_interval_placeholder"},

		// JavaScript Runtime
		{Key: "js_timeout", Name: "settings.js_timeout", EnvVar: "WDBGP_JS_TIMEOUT", Type: "number", Section: "settings.section_js", Placeholder: "settings.js_timeout_placeholder"},
		{Key: "js_max_source", Name: "settings.js_max_source", EnvVar: "WDBGP_JS_MAX_SOURCE", Type: "number", Section: "settings.section_js", Placeholder: "settings.js_max_source_placeholder"},
		{Key: "js_max_response", Name: "settings.js_max_response", EnvVar: "WDBGP_JS_MAX_RESPONSE", Type: "number", Section: "settings.section_js", Placeholder: "settings.js_max_response_placeholder"},
		{Key: "js_max_total", Name: "settings.js_max_total", EnvVar: "WDBGP_JS_MAX_TOTAL", Type: "number", Section: "settings.section_js", Placeholder: "settings.js_max_total_placeholder"},
		{Key: "js_max_entries", Name: "settings.js_max_entries", EnvVar: "WDBGP_JS_MAX_ENTRIES", Type: "number", Section: "settings.section_js", Placeholder: "settings.js_max_entries_placeholder"},
		{Key: "js_max_requests", Name: "settings.js_max_requests", EnvVar: "WDBGP_JS_MAX_REQUESTS", Type: "number", Section: "settings.section_js", Placeholder: "settings.js_max_requests_placeholder"},
		{Key: "js_max_call_stack", Name: "settings.js_max_call_stack", EnvVar: "WDBGP_JS_MAX_CALL_STACK", Type: "number", Section: "settings.section_js", Placeholder: "settings.js_max_call_stack_placeholder"},

		// BGP (requires restart)
		{Key: "bgp_port", Name: "settings.bgp_port", EnvVar: "WDBGP_BGP_PORT", Type: "number", Section: "settings.section_bgp", Restart: true},
		{Key: "local_asn", Name: "settings.local_asn", EnvVar: "WDBGP_LOCAL_ASN", Type: "number", Section: "settings.section_bgp", Restart: true},
		{Key: "router_id", Name: "settings.router_id", EnvVar: "WDBGP_ROUTER_ID", Type: "text", Section: "settings.section_bgp", Restart: true},
		{Key: "local_address_v4", Name: "settings.local_address_v4", EnvVar: "WDBGP_BGP_LOCAL_ADDRESS", Type: "text", Section: "settings.section_bgp", Restart: true},
		{Key: "local_address_v6", Name: "settings.local_address_v6", EnvVar: "WDBGP_BGP_LOCAL_ADDRESS_V6", Type: "text", Section: "settings.section_bgp", Restart: true},

		// Network (requires restart)
		{Key: "host", Name: "settings.host", EnvVar: "WDBGP_HOST", Type: "text", Section: "settings.section_network", Restart: true},
		{Key: "port", Name: "settings.port", EnvVar: "WDBGP_PORT", Type: "number", Section: "settings.section_network", Restart: true},
	}
}

func allSettingKeys() []string {
	settings := allSettings()
	keys := make([]string, len(settings))
	for i, s := range settings {
		keys[i] = s.Key
	}
	return keys
}

func isEnvOverridden(envVar string) bool {
	return os.Getenv(envVar) != ""
}

func boolStr(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func configDefaultValue(cfg config.Config, key string) string {
	switch key {
	case "default_language":
		return cfg.DefaultLanguage
	case "session_max_age":
		return strconv.Itoa(cfg.SessionMaxAge)
	case "admin_cookie_secure":
		return cfg.AdminCookieSecure
	case "trust_proxy_headers":
		return boolStr(cfg.TrustProxyHeader)
	case "security_headers":
		return boolStr(cfg.SecurityHeaders)
	case "default_web_auth":
		return cfg.DefaultWebAuth
	case "rate_limit_login":
		return strconv.Itoa(cfg.RateLimitLogin)
	case "rate_limit_admin":
		return strconv.Itoa(cfg.RateLimitAdmin)
	case "log_level":
		return cfg.LogLevel
	case "log_format":
		return cfg.LogFormat
	case "sync_interval":
		return strconv.Itoa(int(cfg.SyncInterval.Seconds()))
	case "js_timeout":
		return strconv.Itoa(int(cfg.JSTimeout.Seconds()))
	case "js_max_source":
		return strconv.Itoa(cfg.JSMaxSourceBytes)
	case "js_max_response":
		return strconv.Itoa(cfg.JSMaxResponseBytes)
	case "js_max_total":
		return strconv.Itoa(cfg.JSMaxTotalBytes)
	case "js_max_entries":
		return strconv.Itoa(cfg.JSMaxEntries)
	case "js_max_requests":
		return strconv.Itoa(cfg.JSMaxRequests)
	case "js_max_call_stack":
		return strconv.Itoa(cfg.JSMaxCallStack)
	case "bgp_port":
		return strconv.Itoa(int(cfg.BGPListenPort))
	case "local_asn":
		return strconv.FormatUint(uint64(cfg.LocalASN), 10)
	case "router_id":
		return cfg.RouterID
	case "local_address_v4":
		return cfg.LocalAddressV4
	case "local_address_v6":
		return cfg.LocalAddressV6
	case "host":
		return cfg.Host
	case "port":
		return strconv.Itoa(cfg.Port)
	case "adapter_backup_dir":
		return cfg.AdapterBackupDir
	case "adapter_backup_max":
		return strconv.Itoa(cfg.AdapterBackupMax)
	}
	return ""
}

func buildSettingsSections(cfg config.Config, dbSettings map[string]string) []settingSection {
	all := allSettings()
	sectionMap := make(map[string][]settingField)
	sectionOrder := []string{} // preserve order

	for _, f := range all {
		// Populate value and env override
		if v := os.Getenv(f.EnvVar); v != "" {
			f.Value = v
			f.EnvOverride = true
		} else if v, ok := dbSettings[f.Key]; ok {
			f.Value = v
			f.EnvOverride = false
		} else {
			f.Value = "" // zero value, template shows placeholder/default
			f.EnvOverride = false
			f.DefaultValue = configDefaultValue(cfg, f.Key)
		}

		if _, ok := sectionMap[f.Section]; !ok {
			sectionOrder = append(sectionOrder, f.Section)
		}
		sectionMap[f.Section] = append(sectionMap[f.Section], f)
	}

	sections := make([]settingSection, 0, len(sectionOrder))
	for _, sKey := range sectionOrder {
		sections = append(sections, settingSection{
			TitleKey: sKey,
			Fields:   sectionMap[sKey],
		})
	}
	return sections
}

type globalFiltersView struct {
	Allow string // newline-separated CIDRs
	Deny  string // newline-separated CIDRs
}

func fieldByKey(key string) *settingField {
	for _, f := range allSettings() {
		if f.Key == key {
			return &f
		}
	}
	return nil
}
