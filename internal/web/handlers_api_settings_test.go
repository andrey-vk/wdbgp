package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrey-vk/wdbgp/internal/settings"
	"github.com/andrey-vk/wdbgp/internal/store"
)

// mustSetSetting sets a setting and fails the test immediately if it errors —
// used in test setup, where an unexpected Set() failure means the test's
// preconditions are already broken.
func mustSetSetting[JSON, Runtime any](t *testing.T, s settings.Setting[JSON, Runtime], v JSON) {
	t.Helper()
	if err := s.Set(context.Background(), v); err != nil {
		t.Fatal(err)
	}
}

func TestAPISettingsGet_TypedResponse(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "typed-settings.sqlite3"), false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	}()

	st, err := settings.New(settings.NewTestStore())
	if err != nil {
		t.Fatal(err)
	}
	mustSetSetting(t, st.SessionSecret, "test-secret")
	mustSetSetting(t, st.AdminCookieSecure, "true")

	server := New(st, db, nil, nil)
	cookie := adminCookie(st)
	req := httptest.NewRequest("GET", "/api/admin/settings", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	server.handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp settings.SettingsJSON
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}

	// Verify typed fields are correct
	if resp.Port.DefaultValue != 8080 {
		t.Errorf("port default = %d, want 8080", resp.Port.DefaultValue)
	}
	if resp.Port.Value != nil {
		t.Errorf("port value = %v, want nil (not overridden)", resp.Port.Value)
	}
	if resp.Port.EnvOverride {
		t.Error("port env_override should be false")
	}

	if resp.MetricsEnabled.DefaultValue != false {
		t.Error("metrics_enabled default should be false")
	}

	if resp.DefaultWebAuth.DefaultValue != "network" {
		t.Errorf("default_web_auth default = %q, want network", resp.DefaultWebAuth.DefaultValue)
	}

	// Verify filter fields exist in settings
	if resp.FilterAllow.DefaultValue != "" {
		t.Errorf("filter_allow default = %q, want empty", resp.FilterAllow.DefaultValue)
	}
}

func TestAPISettingsPut_TypedBool(t *testing.T) {
	store := settings.NewTestStore()
	st, err := settings.New(store)
	if err != nil {
		t.Fatal(err)
	}
	mustSetSetting(t, st.SessionSecret, "test-secret")
	server := New(st, nil, nil, nil)

	body := `{"metrics_enabled": true}`
	req := httptest.NewRequest("PUT", "/api/admin/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	cookie := adminCookie(st)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	server.handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if st.MetricsEnabled.Get() != true {
		t.Error("metrics_enabled should be true after PUT")
	}
}

func TestAPISettingsPut_TypedInt(t *testing.T) {
	store := settings.NewTestStore()
	st, err := settings.New(store)
	if err != nil {
		t.Fatal(err)
	}
	mustSetSetting(t, st.SessionSecret, "test-secret")
	server := New(st, nil, nil, nil)

	body := `{"rate_limit_login": 42}`
	req := httptest.NewRequest("PUT", "/api/admin/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	cookie := adminCookie(st)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	server.handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if st.RateLimitLogin.Get() != 42 {
		t.Errorf("rate_limit_login = %d, want 42", st.RateLimitLogin.Get())
	}
}

// TestAPISettingsPut_HostPortDBPathAreReadOnly verifies host/port/db_path
// can no longer be changed via the API — they're env-only (WDBGP_HOST,
// WDBGP_PORT, WDBGP_DB), same as they always required a restart to take
// effect, but now genuinely can't be edited from a running instance
// instead of silently accepting a value that could lock an admin out of
// the UI or point at an unbindable port with no local recovery path.
func TestAPISettingsPut_HostPortDBPathAreReadOnly(t *testing.T) {
	store := settings.NewTestStore()
	st, err := settings.New(store)
	if err != nil {
		t.Fatal(err)
	}
	mustSetSetting(t, st.SessionSecret, "test-secret")
	server := New(st, nil, nil, nil)
	cookie := adminCookie(st)

	for key, body := range map[string]string{
		"host":    `{"host": "127.0.0.1"}`,
		"port":    `{"port": 9090}`,
		"db_path": `{"db_path": "/other.sqlite3"}`,
	} {
		req := httptest.NewRequest("PUT", "/api/admin/settings", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(cookie)
		w := httptest.NewRecorder()
		server.handler.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("PUT %s: status = %d, want 400", key, w.Code)
		}

		resetReq := httptest.NewRequest("PUT", "/api/admin/settings", strings.NewReader(`{"`+key+`": null}`))
		resetReq.Header.Set("Content-Type", "application/json")
		resetReq.AddCookie(cookie)
		resetW := httptest.NewRecorder()
		server.handler.ServeHTTP(resetW, resetReq)
		if resetW.Code != http.StatusBadRequest {
			t.Errorf("RESET %s: status = %d, want 400", key, resetW.Code)
		}
	}

	if st.Host.Get() != "0.0.0.0" {
		t.Errorf("host = %q, want unchanged default 0.0.0.0", st.Host.Get())
	}
	if st.Port.Get() != 8080 {
		t.Errorf("port = %d, want unchanged default 8080", st.Port.Get())
	}
	if st.DBPath.Get() != "/data/wdbgp.sqlite3" {
		t.Errorf("db_path = %q, want unchanged default", st.DBPath.Get())
	}
}

func TestAPISettingsPut_Reset(t *testing.T) {
	store := settings.NewTestStore()
	st, err := settings.New(store)
	if err != nil {
		t.Fatal(err)
	}
	mustSetSetting(t, st.SessionSecret, "test-secret")
	// Set first
	mustSetSetting(t, st.RateLimitLogin, 42)
	if st.RateLimitLogin.Get() != 42 {
		t.Fatal("precondition failed")
	}

	server := New(st, nil, nil, nil)
	body := `{"rate_limit_login": null}`
	req := httptest.NewRequest("PUT", "/api/admin/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	cookie := adminCookie(st)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	server.handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if st.RateLimitLogin.Get() != 5 {
		t.Errorf("rate_limit_login = %d, want 5 (default after reset)", st.RateLimitLogin.Get())
	}
}

func TestAPISettingsPut_InvalidType(t *testing.T) {
	store := settings.NewTestStore()
	st, err := settings.New(store)
	if err != nil {
		t.Fatal(err)
	}
	mustSetSetting(t, st.SessionSecret, "test-secret")
	server := New(st, nil, nil, nil)

	body := `{"port": "not-a-number"}`
	req := httptest.NewRequest("PUT", "/api/admin/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	cookie := adminCookie(st)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	server.handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestAPISettingsPut_EnvOverridden(t *testing.T) {
	t.Setenv("WDBGP_PORT", "1234")
	store := settings.NewTestStore()
	st, err := settings.New(store)
	if err != nil {
		t.Fatal(err)
	}
	mustSetSetting(t, st.SessionSecret, "test-secret")
	server := New(st, nil, nil, nil)

	body := `{"port": 9999}`
	req := httptest.NewRequest("PUT", "/api/admin/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	cookie := adminCookie(st)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	server.handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for env-overridden field", w.Code)
	}
}

func TestAPISettingsPut_RouteFilters(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "settings-filters.sqlite3"), false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	}()

	st, err := settings.New(db)
	if err != nil {
		t.Fatal(err)
	}
	mustSetSetting(t, st.SessionSecret, "test-secret")
	mustSetSetting(t, st.AdminPassword, "admin")
	mustSetSetting(t, st.AdminCookieSecure, "true")
	server := New(st, db, nil, nil)

	body := `{"filter_allow": "10.0.0.0/8\n192.168.0.0/16", "filter_deny": "10.1.0.0/16"}`
	req := httptest.NewRequest("PUT", "/api/admin/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	cookie := adminCookie(st)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	server.handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	// Verify filters were saved in app_settings via the DB-backed settings
	filters, err := db.GlobalRouteFilters(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(filters.Allow) != 2 {
		t.Errorf("expected 2 allow filters, got %d", len(filters.Allow))
	}
	if len(filters.Deny) != 1 {
		t.Errorf("expected 1 deny filter, got %d: %v", len(filters.Deny), filters.Deny)
	}
}

// TestAPISettingsPut_RouteFiltersTriggerReconcile guards against a bug where
// changing global filter_allow/filter_deny persisted the new value but never
// told the running BGP manager to recompute routes — already-announced
// prefixes would keep using the old filter set until the next scheduled
// sync (up to sync_interval seconds later), which for a deny filter means
// routes the admin just tried to block stay live in the meantime.
func TestAPISettingsPut_RouteFiltersTriggerReconcile(t *testing.T) {
	store := settings.NewTestStore()
	st, err := settings.New(store)
	if err != nil {
		t.Fatal(err)
	}
	mustSetSetting(t, st.SessionSecret, "test-secret")
	fake := &fakeBGP{}
	server := New(st, nil, nil, fake)
	cookie := adminCookie(st)

	body := `{"filter_deny": "10.1.0.0/16"}`
	req := httptest.NewRequest("PUT", "/api/admin/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	server.handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if fake.reconciles != 1 {
		t.Errorf("Reconcile called %d times, want 1", fake.reconciles)
	}
}

// TestAPISettingsPut_UnrelatedSettingDoesNotTriggerReconcile confirms the
// reconcile-on-filter-change logic doesn't fire for settings that have
// nothing to do with route filters.
func TestAPISettingsPut_UnrelatedSettingDoesNotTriggerReconcile(t *testing.T) {
	store := settings.NewTestStore()
	st, err := settings.New(store)
	if err != nil {
		t.Fatal(err)
	}
	mustSetSetting(t, st.SessionSecret, "test-secret")
	fake := &fakeBGP{}
	server := New(st, nil, nil, fake)
	cookie := adminCookie(st)

	body := `{"rate_limit_login": 42}`
	req := httptest.NewRequest("PUT", "/api/admin/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	server.handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if fake.reconciles != 0 {
		t.Errorf("Reconcile called %d times, want 0", fake.reconciles)
	}
}

// TestAPISettingsPut_RouteFiltersReconcileFailureSurfaces confirms a reconcile
// failure after a successful settings save produces a clear error rather
// than a bare 200, so the admin knows the filter change may not have
// actually applied to the live BGP session.
func TestAPISettingsPut_RouteFiltersReconcileFailureSurfaces(t *testing.T) {
	store := settings.NewTestStore()
	st, err := settings.New(store)
	if err != nil {
		t.Fatal(err)
	}
	mustSetSetting(t, st.SessionSecret, "test-secret")
	fake := &fakeBGP{down: true, downErr: fmt.Errorf("bgp speaker is not running")}
	server := New(st, nil, nil, fake)
	cookie := adminCookie(st)

	body := `{"filter_allow": "10.0.0.0/8"}`
	req := httptest.NewRequest("PUT", "/api/admin/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	server.handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Settings saved but BGP reconciliation failed") {
		t.Errorf("body = %s, want it to mention settings were saved despite reconcile failure", w.Body.String())
	}
}
