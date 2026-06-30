package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/andrey-vk/wdbgp/internal/feeds"
	"github.com/andrey-vk/wdbgp/internal/store"
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
	s.SessionMaxAge.Set(context.Background(), 0) // unconfigured — should produce a session cookie

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
	s.SessionMaxAge.Set(context.Background(), 3600) // 1 hour

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
