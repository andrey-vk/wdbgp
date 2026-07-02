package web

import (
	"crypto/hmac"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/andrey-vk/wdbgp/internal/logging"
)

// apiResponse is a standard JSON response envelope.
type apiResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// adminCookieSecure determines whether admin cookies should have the Secure flag.
func (s *Server) adminCookieSecure(r *http.Request) bool {
	switch s.settings.AdminCookieSecure.Get() {
	case "true":
		return true
	case "false":
		return false
	}
	if r.TLS != nil {
		return true
	}
	if s.settings.TrustProxyHeaders.Get() && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		return true
	}
	return false
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		logging.Error("failed to write JSON response", "error", err)
	}
}

// apiAdminLogin handles POST /api/admin/login.
func (s *Server) apiAdminLogin(w http.ResponseWriter, r *http.Request) {
	// Rate limiting
	clientIP := s.clientIP(r)
	if !s.loginLimiter.allow(clientIP) {
		s.logAdminAction(r, "API_LOGIN_RATE_LIMIT", "Rate limit exceeded")
		writeJSON(w, http.StatusTooManyRequests, apiResponse{OK: false, Error: "Rate limit exceeded"})
		return
	}

	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid request body"})
		return
	}

	if !hmac.Equal([]byte(body.Password), []byte(s.settings.AdminPassword.Get())) {
		s.logAdminAction(r, "API_LOGIN_FAILED", "Invalid password")
		writeJSON(w, http.StatusUnauthorized, apiResponse{OK: false, Error: "Invalid password"})
		return
	}

	// Create session cookie (same token format as legacy)
	maxAge := 0
	if s.settings.SessionMaxAge.Get() > 0 {
		maxAge = s.settings.SessionMaxAge.Get()
	}

	var expires time.Time
	if maxAge > 0 {
		expires = time.Now().Add(time.Duration(maxAge) * time.Second)
	}

	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure determined at runtime
		Name:     "wdbgp_admin",
		Value:    sessionToken(s.settings.SessionSecret.Get()),
		Path:     "/",
		HttpOnly: true,
		Secure:   s.adminCookieSecure(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   maxAge,
		Expires:  expires,
	})
	setCSRFCookie(w, s.adminCookieSecure(r), maxAge, expires)

	s.logAdminAction(r, "API_LOGIN_SUCCESS", "Admin logged in via API")
	writeJSON(w, http.StatusOK, apiResponse{OK: true})
}

// apiAdminMe handles GET /api/admin/me.
// Requires valid admin session. Returns whether the user is authenticated.
func (s *Server) apiAdminMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": true})
}

// apiAdminLogout handles POST /api/admin/logout.
// Clears the admin session cookie.
func (s *Server) apiAdminLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure determined at runtime
		Name:     "wdbgp_admin",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   s.adminCookieSecure(r),
		MaxAge:   -1,
	})
	clearCSRFCookie(w, s.adminCookieSecure(r))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// apiRequireAdmin is middleware that validates the admin session cookie.
// Returns JSON 401 instead of HTML redirect (unlike requireAdmin which is for legacy pages).
func (s *Server) apiRequireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("wdbgp_admin")
		sessionMaxAge := time.Duration(s.settings.SessionMaxAge.Get()) * time.Second
		if sessionMaxAge <= 0 {
			sessionMaxAge = 8 * time.Hour
		}
		if err != nil || !validSession(s.settings.SessionSecret.Get(), cookie.Value, sessionMaxAge) {
			writeJSON(w, http.StatusUnauthorized, apiResponse{OK: false, Error: "unauthorized"})
			return
		}

		// Renew session if it's more than 1 hour old
		parts := strings.SplitN(cookie.Value, ".", 3)
		if len(parts) == 3 {
			timestamp, err := strconv.ParseInt(parts[0], 16, 64)
			if err == nil {
				sessionTime := time.Unix(timestamp, 0)
				if time.Since(sessionTime) > time.Hour && time.Since(sessionTime) < sessionMaxAge-time.Hour {
					maxAge := 0
					if s.settings.SessionMaxAge.Get() > 0 {
						maxAge = s.settings.SessionMaxAge.Get()
					}

					var expires time.Time
					if maxAge > 0 {
						expires = time.Now().Add(time.Duration(maxAge) * time.Second)
					}

					http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure determined at runtime
						Name:     "wdbgp_admin",
						Value:    sessionToken(s.settings.SessionSecret.Get()),
						Path:     "/",
						HttpOnly: true,
						Secure:   s.adminCookieSecure(r),
						SameSite: http.SameSiteStrictMode,
						MaxAge:   maxAge,
						Expires:  expires,
					})
					setCSRFCookie(w, s.adminCookieSecure(r), maxAge, expires)
				}
			}
		}

		// Self-heal a missing CSRF cookie (e.g. a session that predates
		// this feature), so the next request from the same browser has a
		// token to echo back. This request itself still fails the check
		// below if it's a mutating one, since the browser hasn't seen the
		// new cookie yet.
		if _, err := r.Cookie(csrfCookieName); err != nil {
			setCSRFCookie(w, s.adminCookieSecure(r), 0, time.Time{})
		}

		if !validCSRFRequest(r) {
			writeJSON(w, http.StatusForbidden, apiResponse{OK: false, Error: "CSRF token missing or invalid"})
			return
		}

		next(w, r)
	}
}
