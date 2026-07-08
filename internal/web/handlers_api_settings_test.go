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
	addCSRF(req)
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
	addCSRF(req)
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
	addCSRF(req)
	w := httptest.NewRecorder()
	server.handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if st.RateLimitLogin.Get() != 42 {
		t.Errorf("rate_limit_login = %d, want 42", st.RateLimitLogin.Get())
	}
}

// TestAPISettingsPut_DynamicPeerMD5Match guards the setSetting/validateSettingKey/
// resetSetting wiring for the two dynamic-peer MD5 matching settings — a typo'd
// case string would silently fall through to "unknown setting" and this is the
// only place that dispatch is exercised end to end via the actual HTTP handler.
func TestAPISettingsPut_DynamicPeerMD5Match(t *testing.T) {
	store := settings.NewTestStore()
	st, err := settings.New(store)
	if err != nil {
		t.Fatal(err)
	}
	mustSetSetting(t, st.SessionSecret, "test-secret")
	server := New(st, nil, nil, nil)

	body := `{"dynamic_peer_md5_match": true, "dynamic_peer_md5_queue_num": 7}`
	req := httptest.NewRequest("PUT", "/api/admin/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(adminCookie(st))
	addCSRF(req)
	w := httptest.NewRecorder()
	server.handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if !st.DynamicPeerMD5Match.Get() {
		t.Error("dynamic_peer_md5_match should be true after PUT")
	}
	if st.DynamicPeerMD5QueueNum.Get() != 7 {
		t.Errorf("dynamic_peer_md5_queue_num = %d, want 7", st.DynamicPeerMD5QueueNum.Get())
	}

	// Reset both back to defaults.
	resetBody := `{"dynamic_peer_md5_match": null, "dynamic_peer_md5_queue_num": null}`
	req = httptest.NewRequest("PUT", "/api/admin/settings", strings.NewReader(resetBody))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(adminCookie(st))
	addCSRF(req)
	w = httptest.NewRecorder()
	server.handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("reset status = %d, body = %s", w.Code, w.Body.String())
	}
	if st.DynamicPeerMD5Match.Get() {
		t.Error("dynamic_peer_md5_match should be false after reset")
	}
	if st.DynamicPeerMD5QueueNum.Get() != 0 {
		t.Errorf("dynamic_peer_md5_queue_num = %d, want 0 after reset", st.DynamicPeerMD5QueueNum.Get())
	}
}

// TestAPISettingsPut_BGPHoldTime guards the dispatch wiring for
// bgp_hold_time: the setting shipped with metadata, locales and an OnChange
// hook but without cases in setSetting/validateSettingKey/resetSetting, so
// every UI edit answered "unknown setting" (only the env var worked).
func TestAPISettingsPut_BGPHoldTime(t *testing.T) {
	store := settings.NewTestStore()
	st, err := settings.New(store)
	if err != nil {
		t.Fatal(err)
	}
	mustSetSetting(t, st.SessionSecret, "test-secret")
	server := New(st, nil, nil, nil)

	put := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("PUT", "/api/admin/settings", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(adminCookie(st))
		addCSRF(req)
		w := httptest.NewRecorder()
		server.handler.ServeHTTP(w, req)
		return w
	}

	if w := put(`{"bgp_hold_time": 180}`); w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if st.BGPHoldTime.Get() != 180 {
		t.Errorf("bgp_hold_time = %d, want 180", st.BGPHoldTime.Get())
	}

	// Below the RFC 4271 minimum of 3 — validation must reject it.
	if w := put(`{"bgp_hold_time": 2}`); w.Code != http.StatusBadRequest {
		t.Fatalf("invalid value: status = %d, want 400, body = %s", w.Code, w.Body.String())
	}
	if st.BGPHoldTime.Get() != 180 {
		t.Errorf("bgp_hold_time = %d after rejected PUT, want 180", st.BGPHoldTime.Get())
	}

	if w := put(`{"bgp_hold_time": null}`); w.Code != http.StatusOK {
		t.Fatalf("reset status = %d, body = %s", w.Code, w.Body.String())
	}
	if st.BGPHoldTime.Get() != 90 {
		t.Errorf("bgp_hold_time = %d after reset, want default 90", st.BGPHoldTime.Get())
	}
}

// TestSettingsDispatchCoversAllKeys sweeps every key the settings API
// exposes (the JSON field names of GET /api/admin/settings) through the
// three dispatch switches. A setting added to SettingsJSON but missed in
// setSetting/validateSettingKey/resetSetting shows up in the UI yet always
// answers "unknown setting" on save — exactly what happened with
// bgp_hold_time. Read-only keys are fine: they return their own
// "cannot be changed here" error, not the unknown-setting fallthrough.
func TestSettingsDispatchCoversAllKeys(t *testing.T) {
	st, err := settings.New(settings.NewTestStore())
	if err != nil {
		t.Fatal(err)
	}
	server := New(st, nil, nil, nil)
	ctx := context.Background()

	blob, err := json.Marshal(st.JSON(ctx))
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(blob, &keys); err != nil {
		t.Fatal(err)
	}
	if len(keys) < 30 {
		t.Fatalf("only %d settings keys found — JSON shape changed?", len(keys))
	}

	unknown := func(err error) bool {
		return err != nil && strings.Contains(err.Error(), "unknown setting")
	}
	for key := range keys {
		if err := server.validateSettingKey(key, json.RawMessage("null")); unknown(err) {
			t.Errorf("validateSettingKey misses %q", key)
		}
		// "{}" parses as valid JSON but unmarshals into no scalar type, so
		// no setting is actually mutated — editable keys fail with a type
		// error, only a missing case falls through to "unknown setting".
		if err := server.setSetting(ctx, key, json.RawMessage("{}")); unknown(err) {
			t.Errorf("setSetting misses %q", key)
		}
		if err := server.resetSetting(ctx, key); unknown(err) {
			t.Errorf("resetSetting misses %q", key)
		}
	}
}

// TestAPISettingsPut_HostPortDBPathAreReadOnly verifies host/port/db_path
// and backup_enabled/backup_dir/auto_restore_enabled can no longer be
// changed via the API — they're all env-only. host/port/db_path always
// required a restart to take effect and could lock an admin out with no
// local recovery path; the three backup settings are read by main.go
// straight from the environment to open the store, before settings.New()
// (and therefore any DB-stored value) exists — a DB write for them was
// always silently ineffective.
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
		"host":                 `{"host": "127.0.0.1"}`,
		"port":                 `{"port": 9090}`,
		"db_path":              `{"db_path": "/other.sqlite3"}`,
		"backup_enabled":       `{"backup_enabled": false}`,
		"backup_dir":           `{"backup_dir": "/other/backup"}`,
		"auto_restore_enabled": `{"auto_restore_enabled": true}`,
	} {
		req := httptest.NewRequest("PUT", "/api/admin/settings", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(cookie)
		addCSRF(req)
		w := httptest.NewRecorder()
		server.handler.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("PUT %s: status = %d, want 400", key, w.Code)
		}

		resetReq := httptest.NewRequest("PUT", "/api/admin/settings", strings.NewReader(`{"`+key+`": null}`))
		resetReq.Header.Set("Content-Type", "application/json")
		resetReq.AddCookie(cookie)
		addCSRF(resetReq)
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
	if !st.BackupEnabled.Get() {
		t.Error("backup_enabled = false, want unchanged default true")
	}
	if st.BackupDir.Get() != "/data" {
		t.Errorf("backup_dir = %q, want unchanged default /data", st.BackupDir.Get())
	}
	if st.AutoRestoreEnabled.Get() {
		t.Error("auto_restore_enabled = true, want unchanged default false")
	}
}

// TestAPISettingsPut_UnknownKeyRejected guards the fail-safe default case in
// setSetting/resetSetting: a key with no explicit case must error out rather
// than silently succeeding or panicking. This is what would actually catch a
// future settings field that's exposed via SettingsJSON but never wired into
// these switches — including a hypothetical dbKey=""+envVar="" field, which
// would need a case here just like host/port/db_path do today.
func TestAPISettingsPut_UnknownKeyRejected(t *testing.T) {
	store := settings.NewTestStore()
	st, err := settings.New(store)
	if err != nil {
		t.Fatal(err)
	}
	mustSetSetting(t, st.SessionSecret, "test-secret")
	server := New(st, nil, nil, nil)
	cookie := adminCookie(st)

	for _, body := range []string{
		`{"totally_unknown_setting": "x"}`,
		`{"totally_unknown_setting": null}`,
	} {
		req := httptest.NewRequest("PUT", "/api/admin/settings", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(cookie)
		addCSRF(req)
		w := httptest.NewRecorder()
		server.handler.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("PUT %s: status = %d, want 400", body, w.Code)
		}
		if !strings.Contains(w.Body.String(), "unknown setting") {
			t.Errorf("PUT %s: body = %q, want it to mention unknown setting", body, w.Body.String())
		}
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
	addCSRF(req)
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
	addCSRF(req)
	w := httptest.NewRecorder()
	server.handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

// TestAPISettingsPut_RejectsPartiallyInvalidBatch guards against a request
// containing one valid and one invalid field persisting the valid change
// while still returning 400 — the whole batch must be validated before any
// of it is applied, so a request that fails validation leaves every
// setting in the batch untouched.
func TestAPISettingsPut_RejectsPartiallyInvalidBatch(t *testing.T) {
	store := settings.NewTestStore()
	st, err := settings.New(store)
	if err != nil {
		t.Fatal(err)
	}
	mustSetSetting(t, st.SessionSecret, "test-secret")
	server := New(st, nil, nil, nil)
	cookie := adminCookie(st)

	body := `{"rate_limit_admin": 99, "filter_deny": "10.0.0.0/33"}`
	req := httptest.NewRequest("PUT", "/api/admin/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	addCSRF(req)
	w := httptest.NewRecorder()
	server.handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", w.Code, w.Body.String())
	}
	if st.RateLimitAdmin.Get() != 30 {
		t.Errorf("rate_limit_admin = %d, want unchanged default 30 — valid field must not persist when another field in the same request is invalid", st.RateLimitAdmin.Get())
	}
	if st.FilterDeny.Get() != "" {
		t.Errorf("filter_deny = %q, want unchanged empty default", st.FilterDeny.Get())
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
	addCSRF(req)
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
	addCSRF(req)
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

// TestAPISettingsPut_RouteFiltersRejectInvalidCIDR guards against a typo'd
// CIDR (e.g. a /33 on IPv4) being persisted into filter_allow/filter_deny —
// prior to validateFilterList, this saved successfully and only surfaced
// as a reconcile failure later, requiring a manual DB fix.
func TestAPISettingsPut_RouteFiltersRejectInvalidCIDR(t *testing.T) {
	store := settings.NewTestStore()
	st, err := settings.New(store)
	if err != nil {
		t.Fatal(err)
	}
	mustSetSetting(t, st.SessionSecret, "test-secret")
	server := New(st, nil, nil, nil)
	cookie := adminCookie(st)

	for key, body := range map[string]string{
		"filter_allow": `{"filter_allow": "10.0.0.0/33"}`,
		"filter_deny":  `{"filter_deny": "not-a-cidr"}`,
	} {
		req := httptest.NewRequest("PUT", "/api/admin/settings", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(cookie)
		addCSRF(req)
		w := httptest.NewRecorder()
		server.handler.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("PUT %s: status = %d, want 400, body=%s", key, w.Code, w.Body.String())
		}
	}

	if st.FilterAllow.Get() != "" {
		t.Errorf("filter_allow = %q, want unchanged empty default", st.FilterAllow.Get())
	}
	if st.FilterDeny.Get() != "" {
		t.Errorf("filter_deny = %q, want unchanged empty default", st.FilterDeny.Get())
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
	addCSRF(req)
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
	addCSRF(req)
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
// failure after a successful settings save reports 200 with a warning field
// (not a 500, which previously left the client unable to tell that the
// setting had, in fact, already been persisted) and that the setting really
// was saved despite the reconcile failure.
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
	addCSRF(req)
	w := httptest.NewRecorder()
	server.handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Settings saved, but BGP reconciliation failed") {
		t.Errorf("body = %s, want it to mention settings were saved despite reconcile failure", w.Body.String())
	}
	if got := st.FilterAllow.Get(); got != "10.0.0.0/8" {
		t.Errorf("filter_allow = %q, want it persisted despite the reconcile failure", got)
	}
}

// TestAPISettingsPut_RejectsEmptyAdminSecrets guards against an authenticated
// caller setting admin_password or session_secret to "" via the settings
// API — hmac.Equal(password, "") would then accept an empty login password,
// and an empty session_secret signs every session with an empty key. Neither
// is enforced by the setting's own Validate: it has no validate hook at all,
// since that same hook would also run against the zero-value default at
// construction time and break commands that don't need admin auth.
func TestAPISettingsPut_RejectsEmptyAdminSecrets(t *testing.T) {
	store := settings.NewTestStore()
	st, err := settings.New(store)
	if err != nil {
		t.Fatal(err)
	}
	mustSetSetting(t, st.SessionSecret, "test-secret")
	mustSetSetting(t, st.AdminPassword, "test-password")
	server := New(st, nil, nil, nil)
	cookie := adminCookie(st)

	for _, body := range []string{`{"admin_password": ""}`, `{"session_secret": ""}`} {
		req := httptest.NewRequest("PUT", "/api/admin/settings", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(cookie)
		addCSRF(req)
		w := httptest.NewRecorder()
		server.handler.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("PUT %s: status = %d, want 400, body=%s", body, w.Code, w.Body.String())
		}
	}

	if st.AdminPassword.Get() != "test-password" {
		t.Errorf("admin_password = %q after rejected PUT, want unchanged", st.AdminPassword.Get())
	}
	if st.SessionSecret.Get() != "test-secret" {
		t.Errorf("session_secret = %q after rejected PUT, want unchanged", st.SessionSecret.Get())
	}
}

// TestAPISettingsPut_RejectsAdminSecretReset guards against {"admin_password":
// null} / {"session_secret": null} reaching Reset() — Reset reverts to the
// zero-value default with no validation at all, so it's just as capable of
// producing an empty secret as a direct empty-string Set.
func TestAPISettingsPut_RejectsAdminSecretReset(t *testing.T) {
	store := settings.NewTestStore()
	st, err := settings.New(store)
	if err != nil {
		t.Fatal(err)
	}
	mustSetSetting(t, st.SessionSecret, "test-secret")
	mustSetSetting(t, st.AdminPassword, "test-password")
	server := New(st, nil, nil, nil)
	cookie := adminCookie(st)

	for _, body := range []string{`{"admin_password": null}`, `{"session_secret": null}`} {
		req := httptest.NewRequest("PUT", "/api/admin/settings", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(cookie)
		addCSRF(req)
		w := httptest.NewRecorder()
		server.handler.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("PUT %s: status = %d, want 400, body=%s", body, w.Code, w.Body.String())
		}
	}

	if st.AdminPassword.Get() != "test-password" {
		t.Errorf("admin_password = %q after rejected reset, want unchanged", st.AdminPassword.Get())
	}
	if st.SessionSecret.Get() != "test-secret" {
		t.Errorf("session_secret = %q after rejected reset, want unchanged", st.SessionSecret.Get())
	}
}

// TestAPISettingsPut_AcceptsNonEmptyAdminSecrets is the accompanying
// happy-path check: a real, non-empty value for either secret must still go
// through.
func TestAPISettingsPut_AcceptsNonEmptyAdminSecrets(t *testing.T) {
	store := settings.NewTestStore()
	st, err := settings.New(store)
	if err != nil {
		t.Fatal(err)
	}
	mustSetSetting(t, st.SessionSecret, "test-secret")
	server := New(st, nil, nil, nil)
	cookie := adminCookie(st)

	body := `{"admin_password": "new-strong-password"}`
	req := httptest.NewRequest("PUT", "/api/admin/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	addCSRF(req)
	w := httptest.NewRecorder()
	server.handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	if st.AdminPassword.Get() != "new-strong-password" {
		t.Errorf("admin_password = %q, want \"new-strong-password\"", st.AdminPassword.Get())
	}
}
