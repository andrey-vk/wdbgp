package web

import (
	"context"
	"html/template"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/andrey-vk/wdbgp/internal/config"
	"github.com/andrey-vk/wdbgp/internal/feeds"
	"github.com/andrey-vk/wdbgp/internal/logging"
	"github.com/andrey-vk/wdbgp/internal/store"
)

// spaDistDir is the directory containing the Vue SPA production build.
// Relative to the working directory when the server starts.
const spaDistDir = "webgui/dist"

const degradedTemplateHTML = `<!DOCTYPE html>
<html lang="{{.Lang}}">
<head><meta charset="utf-8"><title>{{.Title}}</title>
<style>body{font-family:system-ui;display:flex;justify-content:center;align-items:center;min-height:100vh;margin:0;background:#f5f5f5}
.card{background:white;padding:2rem;border-radius:8px;box-shadow:0 2px 8px rgba(0,0,0,.1);max-width:500px}
h1{color:#d32f2f}.lang-switch{margin-top:1rem;font-size:.875rem}</style>
</head><body><div class=card>
<h1>{{.Title}}</h1>
<p>Database schema version <strong>{{.CurrentVersion}}</strong> requires server version <strong>{{.ServerVersion}}</strong> or higher.</p>
{{if .Reason}}<p>{{.Reason}}</p>{{end}}
<div class=lang-switch><a href="{{.EnglishURL}}">English</a> · <a href="{{.RussianURL}}">Русский</a></div>
</div></body></html>`

var degradedTemplate = template.Must(template.New("degraded").Parse(degradedTemplateHTML))

func New(cfg config.Config, s *store.Store, syncer *feeds.Syncer, bgp BGP) *Server {
	defaultLang, ok := parseLocale(cfg.DefaultLanguage)
	if !ok {
		defaultLang = localeEnglish
	}
	server := &Server{
		cfg: cfg, store: s, syncer: syncer, bgp: bgp,
		defaultLang:        defaultLang,
		loginLimiter:       newRateLimiter(time.Minute, cfg.RateLimitLogin), // per minute
		adminLimiter:       newRateLimiter(time.Minute, cfg.RateLimitAdmin), // per minute
		startTime:          time.Now(),
		metricsEnabled:     false,
		metricsHistoryDays: 14,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", server.health)
	mux.HandleFunc("GET /status", server.status)

	// === SPA static file serving ===
	mux.Handle("GET /assets/", http.StripPrefix("/assets/",
		http.FileServer(http.Dir(spaDistDir+"/assets"))))
	// Admin SPA at /admin (must be before user catch-all)
	mux.HandleFunc("GET /admin", server.adminSpaHandler)
	mux.HandleFunc("GET /admin/{path...}", server.adminSpaHandler)

	// Catch-all for user SPA: any remaining path serves user.html
	mux.HandleFunc("GET /{path...}", server.userSpaHandler)

	// === API routes ===
	mux.HandleFunc("POST /api/admin/login", server.apiAdminLogin)
	mux.HandleFunc("GET /api/admin/me", server.apiRequireAdmin(server.apiAdminMe))
	mux.HandleFunc("POST /api/admin/logout", server.apiRequireAdmin(server.apiAdminLogout))
	mux.HandleFunc("GET /api/admin/settings", server.apiRequireAdmin(server.apiSettingsGet))
	mux.HandleFunc("PUT /api/admin/settings", server.apiRequireAdmin(server.apiSettingsPut))
	mux.HandleFunc("GET /api/admin/adapters", server.apiRequireAdmin(server.apiAdaptersList))
	mux.HandleFunc("GET /api/admin/adapters/{id}", server.apiRequireAdmin(server.apiAdaptersGet))
	mux.HandleFunc("POST /api/admin/adapters", server.apiRequireAdmin(server.apiAdaptersCreate))
	mux.HandleFunc("PUT /api/admin/adapters/{id}", server.apiRequireAdmin(server.apiAdaptersUpdate))
	mux.HandleFunc("DELETE /api/admin/adapters/{id}", server.apiRequireAdmin(server.apiAdaptersDelete))
	mux.HandleFunc("POST /api/admin/adapters/{id}/fork", server.apiRequireAdmin(server.apiAdaptersFork))
	mux.HandleFunc("POST /api/admin/adapters/{id}/acknowledge", server.apiRequireAdmin(server.apiAdaptersAcknowledge))
	mux.HandleFunc("GET /api/admin/feeds", server.apiRequireAdmin(server.apiFeedsList))
	mux.HandleFunc("GET /api/admin/feeds/{id}", server.apiRequireAdmin(server.apiFeedsGet))
	mux.HandleFunc("POST /api/admin/feeds", server.apiRequireAdmin(server.apiFeedsCreate))
	mux.HandleFunc("PUT /api/admin/feeds/{id}", server.apiRequireAdmin(server.apiFeedsUpdate))
	mux.HandleFunc("DELETE /api/admin/feeds/{id}", server.apiRequireAdmin(server.apiFeedsDelete))
	mux.HandleFunc("POST /api/admin/feeds/{id}/sync", server.apiRequireAdmin(server.apiFeedsSyncOne))
	mux.HandleFunc("POST /api/admin/feeds/sync-all", server.apiRequireAdmin(server.apiFeedsSyncAll))
	mux.HandleFunc("GET /api/admin/modes", server.apiRequireAdmin(server.apiModesList))
	mux.HandleFunc("GET /api/admin/modes/{id}", server.apiRequireAdmin(server.apiModesGet))
	mux.HandleFunc("POST /api/admin/modes", server.apiRequireAdmin(server.apiModesCreate))
	mux.HandleFunc("PUT /api/admin/modes/{id}", server.apiRequireAdmin(server.apiModesUpdate))
	mux.HandleFunc("DELETE /api/admin/modes/{id}", server.apiRequireAdmin(server.apiModesDelete))
	mux.HandleFunc("GET /api/admin/modes/{id}/feeds", server.apiRequireAdmin(server.apiModeFeedsGet))
	mux.HandleFunc("PUT /api/admin/modes/{id}/feeds", server.apiRequireAdmin(server.apiModeFeedsSet))
	mux.HandleFunc("GET /api/admin/modes/{id}/communities", server.apiRequireAdmin(server.apiModeCommunitiesGet))
	mux.HandleFunc("PUT /api/admin/modes/{id}/communities", server.apiRequireAdmin(server.apiModeCommunitiesPut))
	mux.HandleFunc("POST /api/admin/modes/{id}/communities/reset", server.apiRequireAdmin(server.apiModeCommunitiesReset))
	mux.HandleFunc("POST /api/admin/modes/{id}/communities/generate", server.apiRequireAdmin(server.apiModeCommunitiesGenerate))

	mux.HandleFunc("GET /api/admin/users", server.apiRequireAdmin(server.apiUsersList))
	mux.HandleFunc("GET /api/admin/users/{id}", server.apiRequireAdmin(server.apiUsersGet))
	mux.HandleFunc("POST /api/admin/users", server.apiRequireAdmin(server.apiUsersCreate))
	mux.HandleFunc("PUT /api/admin/users/{id}", server.apiRequireAdmin(server.apiUsersUpdate))
	mux.HandleFunc("DELETE /api/admin/users/{id}", server.apiRequireAdmin(server.apiUsersDelete))
	mux.HandleFunc("GET /api/admin/users/{id}/credentials", server.apiRequireAdmin(server.apiUserCredentialsList))
	mux.HandleFunc("PUT /api/admin/users/{id}/credentials", server.apiRequireAdmin(server.apiUserCredentialsSet))
	mux.HandleFunc("DELETE /api/admin/users/{id}/credentials", server.apiRequireAdmin(server.apiUserCredentialsDelete))
	mux.HandleFunc("GET /api/admin/users/{id}/peer-state", server.apiRequireAdmin(server.apiUserPeerState))
	mux.HandleFunc("GET /api/admin/users/statuses", server.apiRequireAdmin(server.apiUserStatuses))
	mux.HandleFunc("GET /api/admin/users/{id}/catalog", server.apiRequireAdmin(server.apiAdminUserCatalog))
	mux.HandleFunc("PUT /api/admin/users/{id}/selections", server.apiRequireAdmin(server.apiAdminUserSaveSelections))
	mux.HandleFunc("POST /api/admin/users/{id}/count-selections", server.apiRequireAdmin(server.apiAdminUserCountPrefixes))
	mux.HandleFunc("GET /api/admin/dashboard", server.apiRequireAdmin(server.apiDashboard))
	mux.HandleFunc("GET /api/admin/debug", server.apiRequireAdmin(server.apiDebugCIDR))
	mux.HandleFunc("POST /api/admin/settings/purge-metrics", server.apiRequireAdmin(server.apiSettingsPurgeMetrics))

	// User API routes (user-facing, cookie-based or IP-based auth)
	mux.HandleFunc("POST /api/user/login", server.apiUserLogin)
	mux.HandleFunc("POST /api/user/logout", server.apiUserLogout)
	mux.HandleFunc("GET /api/user/me", server.requireUser(server.apiUserMe))
	mux.HandleFunc("POST /api/user/selections", server.requireUser(server.apiUserSaveSelections))
	mux.HandleFunc("PUT /api/user/mode", server.requireUser(server.apiUserSwitchMode))
	mux.HandleFunc("POST /api/user/filters", server.requireUser(server.apiUserSaveFilters))
	mux.HandleFunc("POST /api/user/count-prefixes", server.requireUser(server.apiUserCountPrefixes))

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

	// Metrics collection toggle
	if v, ok := settings["metrics_enabled"]; ok && v != "" {
		s.metricsEnabled = v == "true"
	} else {
		s.metricsEnabled = false
	}
	// Metrics history depth
	if v, ok := settings["metrics_history_days"]; ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			s.metricsHistoryDays = n
		}
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

	if err := degradedTemplate.Execute(w, info); err != nil {
		logger := logging.FromContext(r.Context())
		logger.Error("failed to render degraded template", "error", err)
	}
}

// adminSpaHandler serves the Vue admin SPA at /admin.
// It tries to serve the exact file from dist/ first; if not found,
// falls back to admin.html for client-side Vue Router navigation.
func (s *Server) adminSpaHandler(w http.ResponseWriter, r *http.Request) {
	if _, err := os.Stat(spaDistDir); os.IsNotExist(err) {
		http.Error(w, "SPA build directory not found", http.StatusServiceUnavailable)
		return
	}

	// Serve admin.html for all admin routes (client-side Vue Router handles the rest)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(w, r, filepath.Join(spaDistDir, "admin.html"))
}

// userSpaHandler serves the Vue user SPA at root.
// Uses http.FileServer for static files (CodeQL-safe via http.Dir path sanitization)
// and falls back to user.html for client-side Vue Router navigation.
func (s *Server) userSpaHandler(w http.ResponseWriter, r *http.Request) {
	if _, err := os.Stat(spaDistDir); os.IsNotExist(err) {
		http.Error(w, "SPA build directory not found", http.StatusServiceUnavailable)
		return
	}

	catcher := &notFoundCatcher{ResponseWriter: w}
	http.FileServer(http.Dir(spaDistDir)).ServeHTTP(catcher, r)
	if catcher.notFound {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(w, r, filepath.Join(spaDistDir, "user.html"))
	}
}

// notFoundCatcher intercepts 404 responses for SPA fallback.
type notFoundCatcher struct {
	http.ResponseWriter
	headerWritten bool
	notFound      bool
}

func (c *notFoundCatcher) WriteHeader(code int) {
	if c.headerWritten {
		return
	}
	c.headerWritten = true
	if code == http.StatusNotFound {
		c.notFound = true
		return
	}
	c.ResponseWriter.WriteHeader(code)
}

func (c *notFoundCatcher) Write(b []byte) (int, error) {
	if c.notFound {
		return len(b), nil
	}
	return c.ResponseWriter.Write(b)
}
