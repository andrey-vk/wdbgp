package settings

import (
	"context"
	"sync"
	"testing"
)

// verifySetting checks a Setting[T] was created (not nil).
func verifySetting[T any](t *testing.T, s *Setting[T], name string) {
	t.Helper()
	if s == nil {
		t.Errorf("%s is nil", name)
	}
}

func TestNewSettings_AllFieldsExist(t *testing.T) {
	store := newMockStore()
	s, err := New(store)
	if err != nil {
		t.Fatal(err)
	}

	// Verify all 40 fields exist (non-nil)
	verifySetting(t, s.DBPath, "DBPath")
	verifySetting(t, s.Host, "Host")
	verifySetting(t, s.Port, "Port")
	verifySetting(t, s.BGPPort, "BGPPort")
	verifySetting(t, s.LocalASN, "LocalASN")
	verifySetting(t, s.RouterID, "RouterID")
	verifySetting(t, s.LocalAddressV4, "LocalAddressV4")
	verifySetting(t, s.LocalAddressV6, "LocalAddressV6")
	verifySetting(t, s.AdminPassword, "AdminPassword")
	verifySetting(t, s.SessionSecret, "SessionSecret")
	verifySetting(t, s.AdminCookieSecure, "AdminCookieSecure")
	verifySetting(t, s.DefaultLanguage, "DefaultLanguage")
	verifySetting(t, s.TrustProxyHeaders, "TrustProxyHeaders")
	verifySetting(t, s.SyncInterval, "SyncInterval")
	verifySetting(t, s.SecurityHeaders, "SecurityHeaders")
	verifySetting(t, s.RateLimitLogin, "RateLimitLogin")
	verifySetting(t, s.RateLimitAdmin, "RateLimitAdmin")
	verifySetting(t, s.SessionMaxAge, "SessionMaxAge")
	verifySetting(t, s.LogLevel, "LogLevel")
	verifySetting(t, s.LogFormat, "LogFormat")
	verifySetting(t, s.JSTimeout, "JSTimeout")
	verifySetting(t, s.JSMaxSourceBytes, "JSMaxSourceBytes")
	verifySetting(t, s.JSMaxResponseBytes, "JSMaxResponseBytes")
	verifySetting(t, s.JSMaxTotalBytes, "JSMaxTotalBytes")
	verifySetting(t, s.JSMaxEntries, "JSMaxEntries")
	verifySetting(t, s.JSMaxRequests, "JSMaxRequests")
	verifySetting(t, s.JSMaxCallStack, "JSMaxCallStack")
	verifySetting(t, s.DefaultWebAuth, "DefaultWebAuth")
	verifySetting(t, s.StatusAllowed, "StatusAllowed")
	verifySetting(t, s.StatusToken, "StatusToken")
	verifySetting(t, s.AdapterBackupDir, "AdapterBackupDir")
	verifySetting(t, s.AdapterBackupMax, "AdapterBackupMax")
	verifySetting(t, s.RequirePasswordForNonUniqueIP, "RequirePasswordForNonUniqueIP")
	verifySetting(t, s.AllowDynamicPeers, "AllowDynamicPeers")
	verifySetting(t, s.ActiveDial, "ActiveDial")
	verifySetting(t, s.BackupEnabled, "BackupEnabled")
	verifySetting(t, s.BackupDir, "BackupDir")
	verifySetting(t, s.AutoRestoreEnabled, "AutoRestoreEnabled")
	verifySetting(t, s.MetricsEnabled, "MetricsEnabled")
	verifySetting(t, s.MetricsHistoryDays, "MetricsHistoryDays")
}

func TestNewSettings_Defaults(t *testing.T) {
	store := newMockStore()
	s, err := New(store)
	if err != nil {
		t.Fatal(err)
	}

	if s.Port.Get() != 8080 {
		t.Errorf("Port = %d, want 8080", s.Port.Get())
	}
	if s.BGPPort.Get() != 179 {
		t.Errorf("BGPPort = %d, want 179", s.BGPPort.Get())
	}
	if s.SyncInterval.Get() != 3600 {
		t.Errorf("SyncInterval = %d, want 3600", s.SyncInterval.Get())
	}
	if s.DefaultWebAuth.Get() != "network" {
		t.Errorf("DefaultWebAuth = %q, want network", s.DefaultWebAuth.Get())
	}
	if s.MetricsEnabled.Get() != false {
		t.Errorf("MetricsEnabled = %v, want false", s.MetricsEnabled.Get())
	}
	if s.Host.Get() != "0.0.0.0" {
		t.Errorf("Host = %q, want 0.0.0.0", s.Host.Get())
	}
}

func TestNewSettings_EnvOverrides(t *testing.T) {
	t.Setenv("WDBGP_PORT", "9090")
	t.Setenv("WDBGP_METRICS_ENABLED", "") // not set
	store := newMockStore()
	s, err := New(store)
	if err != nil {
		t.Fatal(err)
	}

	if s.Port.Get() != 9090 {
		t.Errorf("Port = %d, want 9090 (env)", s.Port.Get())
	}
	if !s.Port.IsEnvSet() {
		t.Error("Port should have IsEnvSet=true")
	}
}

func TestNewSettings_DBValues(t *testing.T) {
	store := newMockStore()
	store.settings["rate_limit_login"] = "99"
	store.settings["default_language"] = "ru"
	s, err := New(store)
	if err != nil {
		t.Fatal(err)
	}

	if s.RateLimitLogin.Get() != 99 {
		t.Errorf("RateLimitLogin = %d, want 99 (DB)", s.RateLimitLogin.Get())
	}
	if s.DefaultLanguage.Get() != "ru" {
		t.Errorf("DefaultLanguage = %q, want ru (DB)", s.DefaultLanguage.Get())
	}
}

func TestNewSettings_InvalidEnv(t *testing.T) {
	t.Setenv("WDBGP_PORT", "not-a-number")
	store := newMockStore()
	_, err := New(store)
	if err == nil {
		t.Fatal("expected error for invalid env var")
	}
}

func TestSettings_SetAndReset(t *testing.T) {
	store := newMockStore()
	s, err := New(store)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.RateLimitLogin.Set(context.Background(), 42); err != nil {
		t.Fatal(err)
	}
	if s.RateLimitLogin.Get() != 42 {
		t.Error("RateLimitLogin should be 42 after Set")
	}
	if store.saved["rate_limit_login"] != "42" {
		t.Errorf("store saved %q, want 42", store.saved["rate_limit_login"])
	}

	if err := s.RateLimitLogin.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.RateLimitLogin.Get() != 5 {
		t.Error("RateLimitLogin should be back to default 5 after Reset")
	}
}

func TestSettings_EnvOnlyField_Set(t *testing.T) {
	t.Setenv("WDBGP_DB", "/tmp/test.db")
	store := newMockStore()
	s, err := New(store)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.DBPath.Set(context.Background(), "/other.db"); err == nil {
		t.Error("expected error when setting env-only DBPath")
	}
}

func TestSettings_OnChange(t *testing.T) {
	store := newMockStore()
	s, err := New(store)
	if err != nil {
		t.Fatal(err)
	}

	var got int
	var mu sync.Mutex
	unreg := s.RateLimitLogin.OnChange(func(v int) {
		mu.Lock()
		defer mu.Unlock()
		got = v
	})

	if err := s.RateLimitLogin.Set(context.Background(), 77); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	if got != 77 {
		t.Errorf("callback received %d, want 77", got)
	}
	mu.Unlock()

	// Unregister and set again — callback should not fire
	got = 0
	unreg()
	if err := s.RateLimitLogin.Set(context.Background(), 88); err != nil {
		t.Fatal(err)
	}
	if got != 0 {
		t.Errorf("callback fired after unregister: got %d", got)
	}
}

func TestSettings_JSON(t *testing.T) {
	store := newMockStore()
	s, err := New(store)
	if err != nil {
		t.Fatal(err)
	}

	// Set a field to verify DB value appears in JSON
	s.RateLimitLogin.Set(context.Background(), 42)

	j := s.JSON()

	// Check a default field
	if j.Port.DefaultValue != 8080 {
		t.Errorf("Port DefaultValue = %d, want 8080", j.Port.DefaultValue)
	}
	if j.Port.Value != nil {
		t.Error("Port Value should be nil (default)")
	}
	if j.Port.EnvOverride {
		t.Error("Port EnvOverride should be false")
	}

	// Check a DB-set field
	if j.RateLimitLogin.Value == nil || *j.RateLimitLogin.Value != 42 {
		t.Errorf("RateLimitLogin Value = %v, want 42", j.RateLimitLogin.Value)
	}
	if j.RateLimitLogin.DefaultValue != 5 {
		t.Errorf("RateLimitLogin DefaultValue = %d, want 5", j.RateLimitLogin.DefaultValue)
	}

	// Check an env-only field
	if j.DBPath.DefaultValue != "/data/wdbgp.sqlite3" {
		t.Errorf("DBPath DefaultValue = %q, want /data/wdbgp.sqlite3", j.DBPath.DefaultValue)
	}
}

func TestSettings_ComputedDefaults(t *testing.T) {
	t.Setenv("WDBGP_DB", "/custom/path/db.sqlite3")
	store := newMockStore()
	s, err := New(store)
	if err != nil {
		t.Fatal(err)
	}

	if s.DBPath.Get() != "/custom/path/db.sqlite3" {
		t.Errorf("DBPath = %q, want /custom/path/db.sqlite3", s.DBPath.Get())
	}
	if s.AdapterBackupDir.Get() != "/custom/path/backup/adapters" {
		t.Errorf("AdapterBackupDir = %q, want /custom/path/backup/adapters", s.AdapterBackupDir.Get())
	}
	if s.BackupDir.Get() != "/custom/path" {
		t.Errorf("BackupDir = %q, want /custom/path", s.BackupDir.Get())
	}
}
