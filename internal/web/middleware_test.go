package web

import (
	"context"
	"net/http/httptest"
	"testing"
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
