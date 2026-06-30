package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/andrey-vk/wdbgp/internal/settings"
	"github.com/andrey-vk/wdbgp/internal/store"
)

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
	st.SessionSecret.Set(context.Background(), "test-secret")
	st.AdminCookieSecure.Set(context.Background(), "true")

	server := New(st, db, nil, nil)
	cookie := adminCookie(st)
	req := httptest.NewRequest("GET", "/api/admin/settings", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	server.handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp struct {
		Settings     settings.SettingsJSON `json:"settings"`
		RouteFilters struct {
			FilterAllow string `json:"filter_allow"`
			FilterDeny  string `json:"filter_deny"`
		} `json:"route_filters"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}

	// Verify typed fields are correct
	if resp.Settings.Port.DefaultValue != 8080 {
		t.Errorf("port default = %d, want 8080", resp.Settings.Port.DefaultValue)
	}
	if resp.Settings.Port.Value != nil {
		t.Errorf("port value = %v, want nil (not overridden)", resp.Settings.Port.Value)
	}
	if resp.Settings.Port.EnvOverride {
		t.Error("port env_override should be false")
	}

	if resp.Settings.MetricsEnabled.DefaultValue != false {
		t.Error("metrics_enabled default should be false")
	}

	if resp.Settings.DefaultWebAuth.DefaultValue != "network" {
		t.Errorf("default_web_auth default = %q, want network", resp.Settings.DefaultWebAuth.DefaultValue)
	}

	// Verify route filters are present
	if resp.RouteFilters.FilterAllow != "" {
		t.Errorf("filter_allow = %q, want empty", resp.RouteFilters.FilterAllow)
	}
}
