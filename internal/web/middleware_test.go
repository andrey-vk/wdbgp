package web

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/andrey-vk/wdbgp/internal/logging"
)

// =============================================================================
// TestClientIP — X-Forwarded-For may contain a client-supplied prefix; only
// the last hop (appended by our own reverse proxy) can be trusted. Taking the
// first entry instead lets any direct caller spoof its identity for IP-based
// auth (web_auth=network/any), rate limiting, and the /status IP allowlist.
// =============================================================================

func TestClientIP(t *testing.T) {
	cases := []struct {
		name         string
		trustProxy   bool
		remoteAddr   string
		forwardedFor string
		want         string
	}{
		{
			name:         "no proxy trust, header ignored",
			trustProxy:   false,
			remoteAddr:   "203.0.113.9:1234",
			forwardedFor: "10.0.0.1",
			want:         "203.0.113.9",
		},
		{
			name:         "single hop, last entry is the real client",
			trustProxy:   true,
			remoteAddr:   "127.0.0.1:5678",
			forwardedFor: "198.51.100.7",
			want:         "198.51.100.7",
		},
		{
			name:         "spoofed prefix must not override the appended hop",
			trustProxy:   true,
			remoteAddr:   "127.0.0.1:5678",
			forwardedFor: "10.0.0.1, 198.51.100.7",
			want:         "198.51.100.7",
		},
		{
			name:         "trailing blank/malformed entries are skipped",
			trustProxy:   true,
			remoteAddr:   "127.0.0.1:5678",
			forwardedFor: "10.0.0.1, 198.51.100.7, , not-an-ip",
			want:         "198.51.100.7",
		},
		{
			name:         "empty header falls back to remote addr",
			trustProxy:   true,
			remoteAddr:   "203.0.113.9:1234",
			forwardedFor: "",
			want:         "203.0.113.9",
		},
		{
			name:         "entirely malformed header falls back to remote addr",
			trustProxy:   true,
			remoteAddr:   "203.0.113.9:1234",
			forwardedFor: "not-an-ip, also-not-one",
			want:         "203.0.113.9",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := testSettings()
			if err := s.TrustProxyHeaders.Set(context.Background(), tc.trustProxy); err != nil {
				t.Fatalf("failed to set TrustProxyHeaders: %v", err)
			}

			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = tc.remoteAddr
			if tc.forwardedFor != "" {
				req.Header.Set("X-Forwarded-For", tc.forwardedFor)
			}

			server := &Server{settings: s}
			if got := server.clientIP(req); got != tc.want {
				t.Fatalf("clientIP() = %q, want %q", got, tc.want)
			}
		})
	}
}

// =============================================================================
// TestLimitRequestBody — every JSON handler decodes r.Body without its own
// cap, so the middleware must bound it: an unauthenticated client could
// otherwise stream an arbitrarily large body into json.Decode.
// =============================================================================

func TestLimitRequestBody(t *testing.T) {
	s := testSettings()
	server := &Server{settings: s}

	var readErr error
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, readErr = io.Copy(io.Discard, r.Body)
	})
	handler := server.limitRequestBody(inner)

	adapterLimit := int64(s.JSMaxSourceBytes.Get())*6 + 64<<10
	cases := []struct {
		name  string
		path  string
		limit int64
	}{
		{"default routes get 1MiB", "/api/user/login", 1 << 20},
		{"adapter routes scale with js_max_source", "/api/admin/adapters/7", adapterLimit},
		{"user selections get the large cap", "/api/user/selections", selectionBodyLimit},
		{"user count gets the large cap", "/api/user/count-prefixes", selectionBodyLimit},
		{"admin selections get the large cap", "/api/admin/users/12/selections", selectionBodyLimit},
		{"admin count gets the large cap", "/api/admin/users/12/count-selections", selectionBodyLimit},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A body exactly at the limit reads fully.
			readErr = nil
			req := httptest.NewRequest("POST", tc.path, io.LimitReader(neverEnding('a'), tc.limit))
			handler.ServeHTTP(httptest.NewRecorder(), req)
			if readErr != nil {
				t.Fatalf("body at limit: read error = %v, want nil", readErr)
			}

			// One byte over must fail with MaxBytesError.
			readErr = nil
			req = httptest.NewRequest("POST", tc.path, io.LimitReader(neverEnding('a'), tc.limit+1))
			handler.ServeHTTP(httptest.NewRecorder(), req)
			var maxErr *http.MaxBytesError
			if !errors.As(readErr, &maxErr) {
				t.Fatalf("body over limit: read error = %v, want *http.MaxBytesError", readErr)
			}
		})
	}
}

// The long-running handlers (feed sync, BGP reload, reconcile-calling
// mutations) lift the server-wide Read/WriteTimeout via
// http.ResponseController, which only reaches the real connection if every
// wrapper in the middleware chain implements Unwrap —
// logging.HTTPMiddleware's status-capturing writer didn't, silently turning
// the deadline extension into a no-op. Exercise it over a real connection.
func TestDeadlineControlsWorkThroughLoggingMiddleware(t *testing.T) {
	type result struct{ readErr, writeErr error }
	resCh := make(chan result, 1)
	h := logging.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc := http.NewResponseController(w)
		resCh <- result{
			readErr:  rc.SetReadDeadline(time.Time{}),
			writeErr: rc.SetWriteDeadline(time.Time{}),
		}
	}))
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close() //nolint:errcheck // test cleanup
	res := <-resCh
	if res.readErr != nil {
		t.Fatalf("SetReadDeadline through logging middleware = %v, want nil", res.readErr)
	}
	if res.writeErr != nil {
		t.Fatalf("SetWriteDeadline through logging middleware = %v, want nil", res.writeErr)
	}
}

type neverEnding byte

func (b neverEnding) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(b)
	}
	return len(p), nil
}

// =============================================================================
// TestRateLimiterSweepsStaleIPs — limits only ever grew before this fix:
// every distinct IP got a permanent map entry, even long after its requests
// aged out of the window. A long-running server accumulates one entry per
// unique visitor forever. allow() must reclaim IPs with no requests left in
// the window, not just decide allow/deny correctly.
// =============================================================================

func TestRateLimiterSweepsStaleIPs(t *testing.T) {
	window := 50 * time.Millisecond
	rl := newRateLimiter(window, 10)

	if !rl.allow("1.2.3.4") {
		t.Fatal("first request from 1.2.3.4 should be allowed")
	}
	if got := len(rl.limits); got != 1 {
		t.Fatalf("after one IP: len(limits) = %d, want 1", got)
	}

	time.Sleep(window * 2)

	// This request is what should trigger the sweep — for any IP, not just
	// the stale one — and reclaim 1.2.3.4's now-stale entry.
	if !rl.allow("5.6.7.8") {
		t.Fatal("first request from 5.6.7.8 should be allowed")
	}

	rl.mu.RLock()
	_, stale := rl.limits["1.2.3.4"]
	_, fresh := rl.limits["5.6.7.8"]
	total := len(rl.limits)
	rl.mu.RUnlock()

	if stale {
		t.Error("1.2.3.4 should have been swept after its entries aged out of the window")
	}
	if !fresh {
		t.Error("5.6.7.8 should still be present — it was just added")
	}
	if total != 1 {
		t.Errorf("len(limits) = %d, want 1 (only the fresh IP)", total)
	}
}

// TestRateLimiterStillEnforcesLimit is a basic regression check that the
// sweep doesn't change actual allow/deny behavior within a window.
func TestRateLimiterStillEnforcesLimit(t *testing.T) {
	rl := newRateLimiter(time.Minute, 2)

	if !rl.allow("9.9.9.9") {
		t.Fatal("request 1 should be allowed")
	}
	if !rl.allow("9.9.9.9") {
		t.Fatal("request 2 should be allowed")
	}
	if rl.allow("9.9.9.9") {
		t.Fatal("request 3 should be denied (limit is 2)")
	}
	if rl.allow("9.9.9.9") {
		t.Fatal("request 4 should still be denied")
	}
}

// =============================================================================
// TestValidCSRFRequest — double-submit cookie check: safe methods are always
// exempt; mutating methods require a header that matches the cookie.
// =============================================================================

func TestValidCSRFRequest(t *testing.T) {
	cases := []struct {
		name      string
		method    string
		cookie    string
		hasCookie bool
		header    string
		wantValid bool
	}{
		{name: "GET is exempt even with no token at all", method: http.MethodGet, hasCookie: false, wantValid: true},
		{name: "matching cookie and header", method: http.MethodPost, hasCookie: true, cookie: "tok-1", header: "tok-1", wantValid: true},
		{name: "missing cookie", method: http.MethodPost, hasCookie: false, header: "tok-1", wantValid: false},
		{name: "missing header", method: http.MethodPost, hasCookie: true, cookie: "tok-1", header: "", wantValid: false},
		{name: "mismatched cookie and header", method: http.MethodPut, hasCookie: true, cookie: "tok-1", header: "tok-2", wantValid: false},
		{name: "DELETE requires token too", method: http.MethodDelete, hasCookie: false, wantValid: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, "/api/admin/settings", nil)
			if tc.hasCookie {
				req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: tc.cookie}) //nolint:gosec // test cookie
			}
			if tc.header != "" {
				req.Header.Set(csrfHeaderName, tc.header)
			}
			if got := validCSRFRequest(req); got != tc.wantValid {
				t.Errorf("validCSRFRequest() = %v, want %v", got, tc.wantValid)
			}
		})
	}
}

// =============================================================================
// TestAPIRequireAdminRejectsMutatingRequestWithoutCSRFToken — an admin with a
// valid session cookie but no CSRF token cannot perform a mutating request
// through apiRequireAdmin, closing the cross-site request forgery gap left
// once the legacy CSRF middleware was dropped from the SPA rewrite.
// =============================================================================

func TestAPIRequireAdminRejectsMutatingRequestWithoutCSRFToken(t *testing.T) {
	st := testSettings()
	s := &Server{settings: st}
	called := false
	handler := s.apiRequireAdmin(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/api/admin/settings", nil)
	req.AddCookie(&http.Cookie{Name: "wdbgp_admin", Value: sessionToken(st.SessionSecret.Get())}) //nolint:gosec // test cookie
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
	}
	if called {
		t.Fatal("next handler must not run without a valid CSRF token")
	}

	// A matching cookie+header pair must succeed.
	req2 := httptest.NewRequest(http.MethodPost, "/api/admin/settings", nil)
	req2.AddCookie(&http.Cookie{Name: "wdbgp_admin", Value: sessionToken(st.SessionSecret.Get())}) //nolint:gosec // test cookie
	addCSRF(req2)
	w2 := httptest.NewRecorder()
	handler(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with matching CSRF token; body=%s", w2.Code, w2.Body.String())
	}
	if !called {
		t.Fatal("next handler should have run with a valid CSRF token")
	}
}
