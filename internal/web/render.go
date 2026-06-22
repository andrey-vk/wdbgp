package web

import (
	"database/sql"
	"errors"
	"html/template"
	"net/http"
	"strings"

	"github.com/andrey-vk/wdbgp/internal/logging"
)

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

	csrfToken, _ := r.Context().Value(csrfCtxKey{}).(string) // ok if empty — CSRF middleware handles it

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
