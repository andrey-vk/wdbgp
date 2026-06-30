package settings

import (
	"context"
	"sync"
	"testing"
)

func TestNewSettings_AllFieldsExist(t *testing.T) {
	store := newMockStore()
	s, err := New(store)
	if err != nil {
		t.Fatal(err)
	}

	// Verify all fields exist (non-nil interfaces)
	if s.DBPath == nil {
		t.Error("DBPath is nil")
	}
	if s.Host == nil {
		t.Error("Host is nil")
	}
	if s.Port == nil {
		t.Error("Port is nil")
	}
	if s.BGPPort == nil {
		t.Error("BGPPort is nil")
	}
	if s.LocalASN == nil {
		t.Error("LocalASN is nil")
	}
	if s.RouterID == nil {
		t.Error("RouterID is nil")
	}
	if s.LocalAddressV4 == nil {
		t.Error("LocalAddressV4 is nil")
	}
	if s.LocalAddressV6 == nil {
		t.Error("LocalAddressV6 is nil")
	}
	if s.AdminPassword == nil {
		t.Error("AdminPassword is nil")
	}
	if s.SessionSecret == nil {
		t.Error("SessionSecret is nil")
	}
	if s.AdminCookieSecure == nil {
		t.Error("AdminCookieSecure is nil")
	}
	if s.DefaultLanguage == nil {
		t.Error("DefaultLanguage is nil")
	}
	if s.TrustProxyHeaders == nil {
		t.Error("TrustProxyHeaders is nil")
	}
	if s.SyncInterval == nil {
		t.Error("SyncInterval is nil")
	}
	if s.SecurityHeaders == nil {
		t.Error("SecurityHeaders is nil")
	}
	if s.RateLimitLogin == nil {
		t.Error("RateLimitLogin is nil")
	}
	if s.RateLimitAdmin == nil {
		t.Error("RateLimitAdmin is nil")
	}
	if s.SessionMaxAge == nil {
		t.Error("SessionMaxAge is nil")
	}
	if s.LogLevel == nil {
		t.Error("LogLevel is nil")
	}
	if s.LogFormat == nil {
		t.Error("LogFormat is nil")
	}
	if s.JSTimeout == nil {
		t.Error("JSTimeout is nil")
	}
	if s.JSMaxSourceBytes == nil {
		t.Error("JSMaxSourceBytes is nil")
	}
	if s.JSMaxResponseBytes == nil {
		t.Error("JSMaxResponseBytes is nil")
	}
	if s.JSMaxTotalBytes == nil {
		t.Error("JSMaxTotalBytes is nil")
	}
	if s.JSMaxEntries == nil {
		t.Error("JSMaxEntries is nil")
	}
	if s.JSMaxRequests == nil {
		t.Error("JSMaxRequests is nil")
	}
	if s.JSMaxCallStack == nil {
		t.Error("JSMaxCallStack is nil")
	}
	if s.DefaultWebAuth == nil {
		t.Error("DefaultWebAuth is nil")
	}
	if s.StatusAllowed == nil {
		t.Error("StatusAllowed is nil")
	}
	if s.StatusToken == nil {
		t.Error("StatusToken is nil")
	}
	if s.AdapterBackupDir == nil {
		t.Error("AdapterBackupDir is nil")
	}
	if s.AdapterBackupMax == nil {
		t.Error("AdapterBackupMax is nil")
	}
	if s.RequirePasswordForNonUniqueIP == nil {
		t.Error("RequirePasswordForNonUniqueIP is nil")
	}
	if s.AllowDynamicPeers == nil {
		t.Error("AllowDynamicPeers is nil")
	}
	if s.ActiveDial == nil {
		t.Error("ActiveDial is nil")
	}
	if s.BackupEnabled == nil {
		t.Error("BackupEnabled is nil")
	}
	if s.BackupDir == nil {
		t.Error("BackupDir is nil")
	}
	if s.AutoRestoreEnabled == nil {
		t.Error("AutoRestoreEnabled is nil")
	}
	if s.MetricsEnabled == nil {
		t.Error("MetricsEnabled is nil")
	}
	if s.MetricsHistoryDays == nil {
		t.Error("MetricsHistoryDays is nil")
	}
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

	j := s.JSON(context.Background())

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

func TestSettings_JSON_EnvSet(t *testing.T) {
	t.Setenv("WDBGP_PORT", "9090")
	store := newMockStore()
	s, err := New(store)
	if err != nil {
		t.Fatal(err)
	}

	j := s.JSON(context.Background())

	if j.Port.Value == nil || *j.Port.Value != 9090 {
		t.Errorf("Port Value = %v, want 9090 (env)", j.Port.Value)
	}
	if !j.Port.EnvOverride {
		t.Error("Port EnvOverride should be true")
	}
	if j.Port.DefaultValue != 8080 {
		t.Errorf("Port DefaultValue = %d, want 8080", j.Port.DefaultValue)
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
