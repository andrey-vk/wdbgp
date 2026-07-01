package web

import (
	"context"
	"encoding/json"
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

	body := `{"port": 9090}`
	req := httptest.NewRequest("PUT", "/api/admin/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	cookie := adminCookie(st)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	server.handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if st.Port.Get() != 9090 {
		t.Errorf("port = %d, want 9090", st.Port.Get())
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
	mustSetSetting(t, st.Port, 9090)
	if st.Port.Get() != 9090 {
		t.Fatal("precondition failed")
	}

	server := New(st, nil, nil, nil)
	body := `{"port": null}`
	req := httptest.NewRequest("PUT", "/api/admin/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	cookie := adminCookie(st)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	server.handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if st.Port.Get() != 8080 {
		t.Errorf("port = %d, want 8080 (default after reset)", st.Port.Get())
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
