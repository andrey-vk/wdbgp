package web

import (
	"context"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/andrey-vk/wdbgp/internal/config"
	"github.com/andrey-vk/wdbgp/internal/feeds"
	"github.com/andrey-vk/wdbgp/internal/logging"
	"github.com/andrey-vk/wdbgp/internal/store"
)

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
		startTime:    time.Now(),
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

	// Load runtime-mutable settings from DB so they match persisted values
	server.reloadRuntimeSettings(context.Background())

	return server
}

// reloadRuntimeSettings reads runtime-mutable settings from DB and updates the
// Server fields under mutex. Called once at startup and after every saveSettings.
func (s *Server) reloadRuntimeSettings(ctx context.Context) {
	settings, err := s.store.GetAllSettings(ctx)
	if err != nil {
		return // keep old values
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// StatusAllowed: comma-separated CIDRs, fall back to config if DB empty
	if v, ok := settings["status_allowed"]; ok && v != "" {
		s.statusCIDRs = parseCIDRs(v)
	} else if len(s.cfg.StatusAllowed) > 0 {
		s.statusCIDRs = parseCIDRs(strings.Join(s.cfg.StatusAllowed, ","))
	}
	// StatusToken: Bearer token for /status, fall back to config if DB empty
	if v, ok := settings["status_token"]; ok && v != "" {
		s.statusToken = v
	} else {
		s.statusToken = s.cfg.StatusToken
	}
	// Rate limits, fall back to config if DB empty
	if v, ok := settings["rate_limit_login"]; ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			s.loginLimiter.SetMax(n)
		}
	} else {
		s.loginLimiter.SetMax(s.cfg.RateLimitLogin)
	}
	if v, ok := settings["rate_limit_admin"]; ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			s.adminLimiter.SetMax(n)
		}
	} else {
		s.adminLimiter.SetMax(s.cfg.RateLimitAdmin)
	}
}

// parseCIDRs parses comma-separated CIDR strings into []netip.Prefix.
func parseCIDRs(s string) []netip.Prefix {
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
	var out []netip.Prefix
	for _, p := range parts {
		if p == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(p)
		if err != nil {
			continue
		}
		out = append(out, prefix)
	}
	return out
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
		http.SetCookie(w, &http.Cookie{ //nolint:gosec // language cookie is not sensitive, no Secure/HttpOnly needed
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
