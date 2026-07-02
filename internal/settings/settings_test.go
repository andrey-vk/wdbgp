package settings

import (
	"context"
	"reflect"
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
	if s.FilterAllow == nil {
		t.Error("FilterAllow is nil")
	}
	if s.FilterDeny == nil {
		t.Error("FilterDeny is nil")
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

// TestNoSettingIsBothDBAndEnvInaccessible guards against a setting being
// defined with both dbKey and envVar empty. Such a setting would be a
// disguised constant: unreachable via Set/Reset (the backend's setSetting/
// resetSetting switches would hit their "unknown setting" default case,
// since nothing would ever wire a case for a key with no dbKey), yet its
// JSON() output would be indistinguishable from a normal env-only setting
// whose env var simply isn't set right now — value: null, env_override:
// false. A frontend readonly flag has to be added by hand for every such
// field (see settingsMeta.ts); nothing catches a forgotten one except this.
func TestNoSettingIsBothDBAndEnvInaccessible(t *testing.T) {
	store := newMockStore()
	s, err := New(store)
	if err != nil {
		t.Fatal(err)
	}

	v := reflect.ValueOf(s).Elem()
	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		if !field.CanInterface() {
			continue
		}
		keyer, ok := field.Interface().(interface {
			dbEnvKeys() (string, string)
		})
		if !ok {
			continue
		}
		dbKey, envVar := keyer.dbEnvKeys()
		if dbKey == "" && envVar == "" {
			t.Errorf("field %s has both dbKey and envVar empty — permanently unsettable via any path", v.Type().Field(i).Name)
		}
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
	if err := s.RateLimitLogin.Set(context.Background(), 42); err != nil {
		t.Fatal(err)
	}

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

func TestSettingsPersistAfterSet(t *testing.T) {
	store := newMockStore()
	s, err := New(store)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		set     func(context.Context) error
		get     func() string
		wantKey string
		wantVal string
	}{
		{
			name:    "active_dial",
			set:     func(ctx context.Context) error { return s.ActiveDial.Set(ctx, false) },
			get:     func() string { return formatBool(s.ActiveDial.Get()) },
			wantKey: "active_dial",
			wantVal: "false",
		},
		{
			name:    "allow_dynamic_peers",
			set:     func(ctx context.Context) error { return s.AllowDynamicPeers.Set(ctx, true) },
			get:     func() string { return formatBool(s.AllowDynamicPeers.Get()) },
			wantKey: "allow_dynamic_peers",
			wantVal: "true",
		},
		{
			name:    "require_password_for_non_unique_ip",
			set:     func(ctx context.Context) error { return s.RequirePasswordForNonUniqueIP.Set(ctx, false) },
			get:     func() string { return formatBool(s.RequirePasswordForNonUniqueIP.Get()) },
			wantKey: "require_password_for_non_unique_ip",
			wantVal: "false",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if err := tt.set(ctx); err != nil {
				t.Errorf("Set failed: %v", err)
				return
			}
			if got := tt.get(); got != tt.wantVal {
				t.Errorf("Get = %q, want %q", got, tt.wantVal)
			}
			if store.saved[tt.wantKey] != tt.wantVal {
				t.Errorf("saved[%q] = %q, want %q", tt.wantKey, store.saved[tt.wantKey], tt.wantVal)
			}
		})
	}
}

func formatBool(b bool) string {
	if b {
		return "true"
	}
	return "false"
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

func TestSecretSettingsDBKeys(t *testing.T) {
	store := newMockStore()
	s, err := New(store)
	if err != nil {
		t.Fatal(err)
	}

	// Type-assert to access the unexported dbKey field (same package).
	adminPW, ok := s.AdminPassword.(*simpleSetting[string])
	if !ok {
		t.Fatal("AdminPassword is not *simpleSetting[string]")
	}
	sessionSec, ok := s.SessionSecret.(*simpleSetting[string])
	if !ok {
		t.Fatal("SessionSecret is not *simpleSetting[string]")
	}

	// Both must have non-empty dbKeys.
	if adminPW.dbKey == "" {
		t.Error("AdminPassword dbKey is empty")
	}
	if sessionSec.dbKey == "" {
		t.Error("SessionSecret dbKey is empty")
	}

	// The dbKeys must be distinct (no collision).
	if adminPW.dbKey == sessionSec.dbKey {
		t.Errorf("AdminPassword and SessionSecret share the same dbKey %q", adminPW.dbKey)
	}

	// Verify exact expected keys.
	if adminPW.dbKey != "admin_password" {
		t.Errorf("AdminPassword dbKey = %q, want admin_password", adminPW.dbKey)
	}
	if sessionSec.dbKey != "session_secret" {
		t.Errorf("SessionSecret dbKey = %q, want session_secret", sessionSec.dbKey)
	}
}

func TestSecretSettingsJSONReturnsNil(t *testing.T) {
	// Set env vars so the settings have live values.
	t.Setenv("WDBGP_ADMIN_PASSWORD", "secret123")
	t.Setenv("WDBGP_SESSION_SECRET", "s3cr3t")
	store := newMockStore()
	s, err := New(store)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the settings actually have values from env.
	if s.AdminPassword.Get() != "secret123" {
		t.Errorf("AdminPassword = %q, want secret123", s.AdminPassword.Get())
	}
	if s.SessionSecret.Get() != "s3cr3t" {
		t.Errorf("SessionSecret = %q, want s3cr3t", s.SessionSecret.Get())
	}

	j := s.JSON(context.Background())

	// JSON must NEVER expose secret values — Value must be nil.
	if j.AdminPassword.Value != nil {
		t.Errorf("AdminPassword.Value = %v, want nil (secret must not leak)", j.AdminPassword.Value)
	}
	if j.SessionSecret.Value != nil {
		t.Errorf("SessionSecret.Value = %v, want nil (secret must not leak)", j.SessionSecret.Value)
	}

	// DefaultValue is the empty string (the "default" in New).
	if j.AdminPassword.DefaultValue != "" {
		t.Errorf("AdminPassword.DefaultValue = %q, want \"\"", j.AdminPassword.DefaultValue)
	}
	if j.SessionSecret.DefaultValue != "" {
		t.Errorf("SessionSecret.DefaultValue = %q, want \"\"", j.SessionSecret.DefaultValue)
	}

	// Env override must still be reported (frontend uses this for tags).
	if !j.AdminPassword.EnvOverride {
		t.Error("AdminPassword.EnvOverride should be true")
	}
	if !j.SessionSecret.EnvOverride {
		t.Error("SessionSecret.EnvOverride should be true")
	}
}

// Test that even DB-stored secrets don't leak via JSON.
func TestSecretSettingsJSONReturnsNil_DB(t *testing.T) {
	store := newMockStore()
	store.settings["admin_password"] = "db-secret"
	store.settings["session_secret"] = "db-s3cr3t"
	s, err := New(store)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the settings have DB values.
	if s.AdminPassword.Get() != "db-secret" {
		t.Errorf("AdminPassword = %q, want db-secret", s.AdminPassword.Get())
	}

	j := s.JSON(context.Background())

	if j.AdminPassword.Value != nil {
		t.Errorf("AdminPassword.Value = %v, want nil (secret must not leak)", j.AdminPassword.Value)
	}
	if j.SessionSecret.Value != nil {
		t.Errorf("SessionSecret.Value = %v, want nil (secret must not leak)", j.SessionSecret.Value)
	}
}

func TestBGPPortValidation(t *testing.T) {
	store := newMockStore()
	s, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	// 65536 and negative values are no longer expressible as a Go uint16
	// literal at all — see TestPortValidationRejectsInvalidEnvValue and
	// TestBGPPortValidationRejectsInvalidEnvValue for the string-input path
	// (env var / DB value) where out-of-range values can still arrive.
	if err := s.BGPPort.Set(context.Background(), 0); err == nil {
		t.Error("expected error for port 0")
	}
	if err := s.BGPPort.Set(context.Background(), 179); err != nil {
		t.Errorf("unexpected error for valid port: %v", err)
	}
}

func TestPortValidation(t *testing.T) {
	store := newMockStore()
	s, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	// 65536 and negative values are no longer expressible as a Go uint16
	// literal at all — see TestPortValidationRejectsInvalidEnvValue for the
	// string-input path (env var / DB value) where out-of-range values can
	// still arrive.
	if err := s.Port.Set(context.Background(), 0); err == nil {
		t.Error("expected error for port 0")
	}
	if err := s.Port.Set(context.Background(), 8080); err != nil {
		t.Errorf("unexpected error for valid port: %v", err)
	}
}

func TestPortValidationRejectsInvalidEnvValue(t *testing.T) {
	t.Setenv("WDBGP_PORT", "70000")
	store := newMockStore()
	if _, err := New(store); err == nil {
		t.Error("expected error for out-of-range WDBGP_PORT")
	}
}

func TestPortValidationRejectsNegativeEnvValue(t *testing.T) {
	t.Setenv("WDBGP_PORT", "-1")
	store := newMockStore()
	if _, err := New(store); err == nil {
		t.Error("expected error for negative WDBGP_PORT")
	}
}

func TestBGPPortValidationRejectsInvalidEnvValue(t *testing.T) {
	t.Setenv("WDBGP_BGP_PORT", "70000")
	store := newMockStore()
	if _, err := New(store); err == nil {
		t.Error("expected error for out-of-range WDBGP_BGP_PORT")
	}
}

func TestASNValidation(t *testing.T) {
	store := newMockStore()
	s, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.LocalASN.Set(context.Background(), 0); err == nil {
		t.Error("expected error for ASN 0")
	}
	if err := s.LocalASN.Set(context.Background(), 64512); err != nil {
		t.Errorf("unexpected error for valid ASN: %v", err)
	}
	// The full 4-byte ASN range (up to 4294967295) must be settable — this
	// literal wouldn't even compile if LocalASN were still a platform-width
	// int on a 32-bit target.
	if err := s.LocalASN.Set(context.Background(), 4294967295); err != nil {
		t.Errorf("unexpected error for max valid ASN: %v", err)
	}
}

func TestASNValidationRejectsInvalidEnvValue(t *testing.T) {
	t.Setenv("WDBGP_LOCAL_ASN", "4294967296")
	store := newMockStore()
	if _, err := New(store); err == nil {
		t.Error("expected error for out-of-range WDBGP_LOCAL_ASN")
	}
}

func TestASNValidationRejectsNegativeEnvValue(t *testing.T) {
	t.Setenv("WDBGP_LOCAL_ASN", "-1")
	store := newMockStore()
	if _, err := New(store); err == nil {
		t.Error("expected error for negative WDBGP_LOCAL_ASN")
	}
}

func TestIPv4Validation(t *testing.T) {
	store := newMockStore()
	s, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RouterID.Set(context.Background(), "not-an-ip"); err == nil {
		t.Error("expected error for invalid IPv4")
	}
	if err := s.RouterID.Set(context.Background(), "::1"); err == nil {
		t.Error("expected error for IPv6 as router ID")
	}
}

func TestSyncIntervalValidation(t *testing.T) {
	store := newMockStore()
	s, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SyncInterval.Set(context.Background(), 0); err == nil {
		t.Error("expected error for sync_interval 0")
	}
	if err := s.SyncInterval.Set(context.Background(), -1); err == nil {
		t.Error("expected error for sync_interval -1")
	}
	if err := s.SyncInterval.Set(context.Background(), 3600); err != nil {
		t.Errorf("unexpected error for valid sync_interval: %v", err)
	}
}

func TestFilterListValidation(t *testing.T) {
	store := newMockStore()
	s, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FilterAllow.Set(context.Background(), "10.0.0.0/33"); err == nil {
		t.Error("expected error for CIDR with out-of-range prefix length")
	}
	if err := s.FilterAllow.Set(context.Background(), "not-a-cidr"); err == nil {
		t.Error("expected error for malformed CIDR")
	}
	if err := s.FilterAllow.Set(context.Background(), "10.0.0.0/8\n# a comment\n\n192.168.0.0/16"); err != nil {
		t.Errorf("unexpected error for valid multi-line list with comments/blank lines: %v", err)
	}
	if err := s.FilterAllow.Set(context.Background(), ""); err != nil {
		t.Errorf("unexpected error for empty list: %v", err)
	}
	if err := s.FilterAllow.Set(context.Background(), "10.0.0.0/8\n10.0.0.0/33"); err == nil {
		t.Error("expected error when one line of a multi-line list is invalid")
	}
}

// TestRateLimitValidation guards against WDBGP_RATE_LIMIT_LOGIN=0 (or the
// admin equivalent) silently disabling brute-force protection — the
// documented range starts at 1, and rateLimiter.allow treats maxRequests
// <= 0 as "disabled".
func TestRateLimitValidation(t *testing.T) {
	store := newMockStore()
	s, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RateLimitLogin.Set(context.Background(), 0); err == nil {
		t.Error("expected error for rate_limit_login 0")
	}
	if err := s.RateLimitLogin.Set(context.Background(), -1); err == nil {
		t.Error("expected error for negative rate_limit_login")
	}
	if err := s.RateLimitLogin.Set(context.Background(), 5); err != nil {
		t.Errorf("unexpected error for valid rate_limit_login: %v", err)
	}
	if err := s.RateLimitAdmin.Set(context.Background(), 0); err == nil {
		t.Error("expected error for rate_limit_admin 0")
	}
	if err := s.RateLimitAdmin.Set(context.Background(), 30); err != nil {
		t.Errorf("unexpected error for valid rate_limit_admin: %v", err)
	}
}
