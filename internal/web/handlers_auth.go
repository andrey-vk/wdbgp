package web

import (
	"crypto/hmac"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/andrey-vk/wdbgp/internal/store"
)

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "title.login", "login", "")
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	// Apply rate limiting
	clientIP := s.clientIP(r)
	if !s.loginLimiter.allow(clientIP) {
		s.logAdminAction(r, "LOGIN_RATE_LIMIT", "Rate limit exceeded")
		lang, _ := requestLocale(r, s.defaultLang)
		s.render(w, r, http.StatusTooManyRequests, "title.login", "login",
			translate(lang, "login.rate_limit"))
		return
	}

	if err := r.ParseForm(); err != nil {
		s.httpError(w, r, "error.bad_request", http.StatusBadRequest)
		return
	}
	if !hmac.Equal([]byte(r.FormValue("password")), []byte(s.cfg.AdminPassword)) {
		s.logAdminAction(r, "LOGIN_FAILED", "Invalid password")
		lang, _ := requestLocale(r, s.defaultLang)
		s.render(w, r, http.StatusUnauthorized, "title.login", "login",
			translate(lang, "login.invalid_password"))
		return
	}

	// Create session with expiration
	maxAge := 0 // session cookie
	if s.cfg.SessionMaxAge > 0 {
		maxAge = s.cfg.SessionMaxAge
	}

	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure determined at runtime via adminCookieSecure
		Name:     "wdbgp_admin",
		Value:    sessionToken(s.cfg.SessionSecret),
		Path:     "/",
		HttpOnly: true,
		Secure:   s.adminCookieSecure(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   maxAge,
		Expires:  time.Now().Add(time.Duration(s.cfg.SessionMaxAge) * time.Second),
	})

	// Log successful login
	s.logAdminAction(r, "LOGIN_SUCCESS", "Admin logged in")

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

func (s *Server) userCookieSecure(r *http.Request) bool {
	return s.adminCookieSecure(r)
}

func (s *Server) userLoginPage(w http.ResponseWriter, r *http.Request) {
	// If already logged in, redirect to /
	cookie, err := r.Cookie("wdbgp_user")
	if err == nil {
		// Check if the cookie is valid for any user
		parts := strings.SplitN(cookie.Value, ".", 4)
		if len(parts) == 4 {
			userID, err := strconv.ParseInt(parts[2], 16, 64)
			if err == nil {
				user, err := s.store.User(r.Context(), userID)
				if err == nil && user.Enabled && validUserSession(r, user.ID, s.cfg.SessionSecret, time.Duration(s.cfg.SessionMaxAge)*time.Second) {
					http.Redirect(w, r, "/", http.StatusSeeOther)
					return
				}
			}
		}
	}
	// Show login form with optional error message
	lang, _ := requestLocale(r, s.defaultLang)
	errorMsg := r.URL.Query().Get("error")
	s.renderTitle(w, r, http.StatusOK, translate(lang, "title.login"), "user-login", map[string]string{
		"Error": errorMsg,
	})
}

func (s *Server) userLogin(w http.ResponseWriter, r *http.Request) {
	lang, _ := requestLocale(r, s.defaultLang)

	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/login?error=bad_request", http.StatusSeeOther)
		return
	}

	// Rate-limit login attempts
	clientIP := s.clientIP(r)
	if !s.loginLimiter.allow(clientIP) {
		http.Redirect(w, r, "/login?error="+url.QueryEscape(translate(lang, "login.rate_limit")), http.StatusSeeOther)
		return
	}

	login := strings.TrimSpace(r.FormValue("login"))
	password := r.FormValue("password")

	if login == "" || password == "" {
		http.Redirect(w, r, "/login?error="+url.QueryEscape(translate(lang, "login.error_empty")), http.StatusSeeOther)
		return
	}

	user, err := s.store.AuthenticateUser(r.Context(), login, password)
	if err != nil {
		s.logAdminAction(r, "USER_LOGIN_FAILED", fmt.Sprintf("login=%s", login))
		http.Redirect(w, r, "/login?error="+url.QueryEscape(translate(lang, "login.error_invalid")), http.StatusSeeOther)
		return
	}

	// For web_auth=both, also verify IP match
	if strings.ToLower(user.WebAuth) == "both" {
		clientIP := s.clientIP(r)
		ipUser, err := s.store.UserByIP(r.Context(), clientIP)
		if err != nil || ipUser.ID != user.ID {
			s.logAdminAction(r, "USER_LOGIN_IP_MISMATCH", fmt.Sprintf("user=%d login=%s ip=%s", user.ID, login, clientIP))
			http.Redirect(w, r, "/login?error="+url.QueryEscape(translate(lang, "login.error_ip")), http.StatusSeeOther)
			return
		}
	}

	// Set session cookie
	maxAge := 0 // session cookie
	if s.cfg.SessionMaxAge > 0 {
		maxAge = s.cfg.SessionMaxAge
	}
	setUserSessionCookie(w, user.ID, s.cfg.SessionSecret, maxAge, s.userCookieSecure(r))

	s.logAdminAction(r, "USER_LOGIN_SUCCESS", fmt.Sprintf("user=%d login=%s", user.ID, login))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) userLogout(w http.ResponseWriter, r *http.Request) {
	// Clear the user session cookie
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // cookies are already secure (HttpOnly: true, Secure: s.userCookieSecure(r), SameSite: Strict)
		Name:     userSessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.userCookieSecure(r),
		SameSite: http.SameSiteStrictMode,
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// identifyUser resolves a user from the request using the same logic as userPage:
// IP match first, then session cookie. Returns the user or an error.
func (s *Server) identifyUser(r *http.Request) (store.User, error) {
	clientIP := s.clientIP(r)
	user, err := s.store.UserByIP(r.Context(), clientIP)
	if err != nil {
		// No IP match — try session-based auth
		sessionID := getUserSessionID(r, s.cfg.SessionSecret, time.Duration(s.cfg.SessionMaxAge)*time.Second)
		if sessionID > 0 {
			user, err = s.store.User(r.Context(), sessionID)
			if err == nil && user.Enabled && (user.WebAuth == "login" || user.WebAuth == "any") {
				return user, nil
			}
			return store.User{}, err
		}
		return store.User{}, err
	}
	// IP matched — check web_auth mode
	switch user.WebAuth {
	case "network", "any":
		return user, nil
	case "login", "both":
		if !validUserSession(r, user.ID, s.cfg.SessionSecret, time.Duration(s.cfg.SessionMaxAge)*time.Second) {
			return store.User{}, errors.New("session required")
		}
		return user, nil
	}
	return user, nil
}

func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("wdbgp_admin")
		sessionMaxAge := time.Duration(s.cfg.SessionMaxAge) * time.Second
		if sessionMaxAge <= 0 {
			sessionMaxAge = 8 * time.Hour
		}
		if err != nil || !validSession(s.cfg.SessionSecret, cookie.Value, sessionMaxAge) {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}

		// Renew session if it's more than 1 hour old
		parts := strings.SplitN(cookie.Value, ".", 3)
		if len(parts) == 3 {
			timestamp, err := strconv.ParseInt(parts[0], 16, 64)
			if err == nil {
				sessionTime := time.Unix(timestamp, 0)
				if time.Since(sessionTime) > time.Hour && time.Since(sessionTime) < sessionMaxAge-time.Hour {
					maxAge := 0 // session cookie
					if s.cfg.SessionMaxAge > 0 {
						maxAge = s.cfg.SessionMaxAge
					}

					http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure determined at runtime via adminCookieSecure
						Name:     "wdbgp_admin",
						Value:    sessionToken(s.cfg.SessionSecret),
						Path:     "/",
						HttpOnly: true,
						Secure:   s.adminCookieSecure(r),
						SameSite: http.SameSiteStrictMode,
						MaxAge:   maxAge,
						Expires:  time.Now().Add(time.Duration(s.cfg.SessionMaxAge) * time.Second),
					})
				}
			}
		}

		next(w, r)
	}
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	// Clear admin session cookie
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure determined at runtime via adminCookieSecure
		Name:     "wdbgp_admin",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.adminCookieSecure(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
		Expires:  time.Now().Add(-time.Hour),
	})

	s.logAdminAction(r, "LOGOUT", "Admin logged out")
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

func (s *Server) statusAuthorized(r *http.Request) bool {
	// Check IP
	s.mu.RLock()
	cidrs := s.statusCIDRs
	token := s.statusToken
	s.mu.RUnlock()

	if len(cidrs) > 0 {
		clientIP := s.clientIP(r)
		ip, err := netip.ParseAddr(clientIP)
		if err == nil {
			for _, prefix := range cidrs {
				if prefix.Contains(ip) {
					return true
				}
			}
		}
	}
	// Check token
	if token != "" {
		auth := r.Header.Get("Authorization")
		if strings.TrimPrefix(auth, "Bearer ") == token {
			return true
		}
	}
	return false
}
