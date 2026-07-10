package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/andrey-vk/wdbgp/internal/feeds"
	"github.com/andrey-vk/wdbgp/internal/store"
	"github.com/andrey-vk/wdbgp/internal/version"
)

// =============================================================================
// TestLoginCookieExpiresWithZeroMaxAge — when SessionMaxAge is 0, the login
// cookie must NOT have an Expires field set (session cookie behavior). If
// Expires=time.Now(), the browser treats it as already expired.
// =============================================================================

func TestLoginCookieExpiresWithZeroMaxAge(t *testing.T) {
	db, err := store.Open(":memory:", false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if errClose := db.Close(); errClose != nil {
			t.Logf("close: %v", errClose)
		}
	}()

	s := testSettings()
	// unconfigured — should produce a session cookie
	if err := s.SessionMaxAge.Set(context.Background(), 0); err != nil {
		t.Fatal(err)
	}

	handler := New(s, db, feeds.NewSyncer(db, testSettings()), &fakeBGP{}).Handler()

	body := `{"password": "admin"}`
	req := httptest.NewRequest(http.MethodPost, "/api/admin/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("login: status=%d, want 200", rec.Code)
	}

	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("no cookies in login response")
	}

	c := cookies[0]
	if c.Name != "wdbgp_admin" {
		t.Fatalf("cookie name = %q, want wdbgp_admin", c.Name)
	}

	// When SessionMaxAge is 0, Expires must be zero (session cookie — no Expires header).
	if !c.Expires.IsZero() {
		t.Fatalf("Expires = %v, want zero value (session cookie, no Expires header). This bug causes immediate cookie expiry.", c.Expires)
	}

	// MaxAge should also be 0 (not set).
	if c.MaxAge != 0 {
		t.Fatalf("MaxAge = %d, want 0", c.MaxAge)
	}
}

// =============================================================================
// TestLoginCookieExpiresWithMaxAge — when SessionMaxAge > 0, the login cookie
// should have both MaxAge and Expires set.
// =============================================================================

func TestLoginCookieExpiresWithMaxAge(t *testing.T) {
	db, err := store.Open(":memory:", false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if errClose := db.Close(); errClose != nil {
			t.Logf("close: %v", errClose)
		}
	}()

	s := testSettings()
	if err := s.SessionMaxAge.Set(context.Background(), 3600); err != nil { // 1 hour
		t.Fatal(err)
	}

	handler := New(s, db, feeds.NewSyncer(db, testSettings()), &fakeBGP{}).Handler()

	body := `{"password": "admin"}`
	req := httptest.NewRequest(http.MethodPost, "/api/admin/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("login: status=%d, want 200", rec.Code)
	}

	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("no cookies in login response")
	}

	c := cookies[0]
	if c.Name != "wdbgp_admin" {
		t.Fatalf("cookie name = %q, want wdbgp_admin", c.Name)
	}

	// When SessionMaxAge > 0, Expires should be set in the future.
	if c.Expires.IsZero() {
		t.Fatal("Expires is zero, want a future time")
	}
	if !c.Expires.After(time.Now()) {
		t.Fatalf("Expires = %v, want a time in the future", c.Expires)
	}

	if c.MaxAge != 3600 {
		t.Fatalf("MaxAge = %d, want 3600", c.MaxAge)
	}
}

// =============================================================================
// TestAdminMeReturnsServerVersion — /api/admin/me is the SPA's bootstrap
// call and the only place the running build version is exposed; it must be
// present for authenticated admins (the footer shows it) and the endpoint
// itself must stay behind auth so anonymous visitors can't fingerprint the
// exact build.
// =============================================================================

func TestAdminMeReturnsServerVersion(t *testing.T) {
	db, err := store.Open(":memory:", false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if errClose := db.Close(); errClose != nil {
			t.Logf("close: %v", errClose)
		}
	}()

	handler := New(testSettings(), db, feeds.NewSyncer(db, testSettings()), &fakeBGP{}).Handler()

	// Unauthenticated: 401, no version leak.
	req := httptest.NewRequest(http.MethodGet, "/api/admin/me", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated /api/admin/me: status=%d, want 401", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "server_version") {
		t.Fatal("unauthenticated /api/admin/me response leaks server_version")
	}

	// Log in, replay the session cookie.
	loginReq := httptest.NewRequest(http.MethodPost, "/api/admin/login", strings.NewReader(`{"password": "admin"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	loginRec := httptest.NewRecorder()
	handler.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login: status=%d, want 200", loginRec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/admin/me", nil)
	for _, c := range loginRec.Result().Cookies() {
		req.AddCookie(c)
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("authenticated /api/admin/me: status=%d, want 200", rec.Code)
	}

	var resp struct {
		Authenticated bool   `json:"authenticated"`
		ServerVersion string `json:"server_version"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Authenticated {
		t.Fatal("authenticated = false, want true")
	}
	if resp.ServerVersion != version.Version {
		t.Fatalf("server_version = %q, want %q", resp.ServerVersion, version.Version)
	}
}
