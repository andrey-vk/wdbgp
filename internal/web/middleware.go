package web

import (
	"crypto/hmac"
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/andrey-vk/wdbgp/internal/logging"
)

// clientIP returns the address the request should be attributed to.
//
// When TrustProxyHeaders is on, the reverse proxy is trusted to append the
// real client address as the last hop in X-Forwarded-For — every entry to
// its left was supplied by the client (or an untrusted intermediary) and
// must not be trusted for auth or rate-limit decisions. We scan from the
// right and return the first well-formed IP we find.
func (s *Server) clientIP(r *http.Request) string {
	if s.settings.TrustProxyHeaders.Get() {
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			parts := strings.Split(forwarded, ",")
			for i := len(parts) - 1; i >= 0; i-- {
				candidate := strings.TrimSpace(parts[i])
				if candidate == "" {
					continue
				}
				if _, err := netip.ParseAddr(candidate); err != nil {
					continue
				}
				return candidate
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

// limitRequestBody caps every request body with http.MaxBytesReader so a
// handler's json.NewDecoder(r.Body) can never be fed an unbounded stream.
// The cap follows the largest legitimate payload — an adapter save, whose
// JavaScript source may be up to JSMaxSourceBytes before JSON string
// escaping (worst case 6× for exotic characters, plus envelope slack) —
// with a floor of 1 MiB for everything else.
func (s *Server) limitRequestBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit := int64(s.settings.JSMaxSourceBytes.Get())*6 + 64<<10
		if limit < 1<<20 {
			limit = 1 << 20
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		next.ServeHTTP(w, r)
	})
}

// csrfCookieName/csrfHeaderName implement the double-submit-cookie pattern:
// the cookie is deliberately NOT HttpOnly so the SPA's HTTP client can read
// it and echo it back as a header. A cross-site attacker can trigger a
// request with the browser's cookies attached (defeated separately by
// SameSite=Strict) but cannot read the cookie's value to also set the
// header, so the two must originate from the same site.
const (
	csrfCookieName = "wdbgp_csrf"
	csrfHeaderName = "X-CSRF-Token"
)

// newCSRFToken returns a fresh random token for the CSRF cookie.
func newCSRFToken() string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// setCSRFCookie issues a new CSRF cookie, mirroring the lifetime of the
// session cookie it accompanies.
func setCSRFCookie(w http.ResponseWriter, secure bool, maxAge int, expires time.Time) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure determined at runtime; deliberately not HttpOnly, see csrfCookieName doc
		Name:     csrfCookieName,
		Value:    newCSRFToken(),
		Path:     "/",
		HttpOnly: false,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   maxAge,
		Expires:  expires,
	})
}

// clearCSRFCookie removes the CSRF cookie, mirroring session logout.
func clearCSRFCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure determined at runtime
		Name:     csrfCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: false,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}

// validCSRFRequest reports whether a mutating request carries a CSRF header
// that matches its CSRF cookie. Safe methods never mutate state and are
// exempt.
func validCSRFRequest(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	cookie, err := r.Cookie(csrfCookieName)
	if err != nil || cookie.Value == "" {
		return false
	}
	header := r.Header.Get(csrfHeaderName)
	if header == "" {
		return false
	}
	return hmac.Equal([]byte(cookie.Value), []byte(header))
}

// rateLimiter implements per-IP rate limiting.
//
// limits only ever grows on its own — entries for IPs that stop making
// requests must be reclaimed explicitly or the map leaks for the life of the
// process. allow() amortizes that cleanup: at most once per window, it walks
// the whole map and drops any IP with no timestamps left inside the window,
// piggybacking on regular traffic instead of needing a background goroutine.
type rateLimiter struct {
	mu          sync.RWMutex
	limits      map[string][]time.Time
	window      time.Duration
	maxRequests int
	lastSweep   time.Time
}

func newRateLimiter(window time.Duration, maxRequests int) *rateLimiter {
	return &rateLimiter{
		limits:      make(map[string][]time.Time),
		window:      window,
		maxRequests: maxRequests,
	}
}

func (rl *rateLimiter) SetMax(n int) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.maxRequests = n
}

// sweepLocked removes IPs with no timestamps left inside the window. Callers
// must hold rl.mu. No-op if less than a full window has passed since the
// last sweep, so the cost is amortized rather than paid on every request.
func (rl *rateLimiter) sweepLocked(now time.Time) {
	if now.Sub(rl.lastSweep) < rl.window {
		return
	}
	rl.lastSweep = now

	cutoff := now.Add(-rl.window)
	for ip, requests := range rl.limits {
		stale := true
		for _, t := range requests {
			if t.After(cutoff) {
				stale = false
				break
			}
		}
		if stale {
			delete(rl.limits, ip)
		}
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

	rl.sweepLocked(now)

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
		rl.limits[ip] = valid
		return false
	}

	// Add current request
	valid = append(valid, now)
	rl.limits[ip] = valid
	return true
}

// securityHeaders adds security headers to HTTP responses
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Content Security Policy - restrict resource loading.
		// script-src is locked to same-origin build assets only — the SPA has no
		// inline scripts, no runtime eval, and no CDN dependency.
		// style-src needs 'unsafe-inline' because PrimeVue's theming
		// (@primeuix/styled) injects <style> elements at runtime; there's no
		// static build step that can precompute this away.
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; form-action 'self'")

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

// adminRateLimitMiddleware applies rate limiting to admin endpoints
func (s *Server) adminRateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only apply to admin API paths
		if strings.HasPrefix(r.URL.Path, "/api/admin") && r.URL.Path != "/api/admin/login" && r.URL.Path != "/api/admin/me" && r.URL.Path != "/api/admin/users/statuses" {
			clientIP := s.clientIP(r)

			if !s.adminLimiter.allow(clientIP) {
				http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
