package web

import (
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

func (rl *rateLimiter) SetMax(n int) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.maxRequests = n
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

// securityHeaders adds security headers to HTTP responses
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Content Security Policy - restrict resource loading
		// Allow inline styles/scripts for simplicity, plus unpkg CDN for htmx/alpine
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline' 'unsafe-eval' https://unpkg.com; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; form-action 'self'")

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
