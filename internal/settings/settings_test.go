package settings

import (
	"context"
	"os"
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

// TestEnvOnlySettingCannotBeSetOrReset guards the dbKey="" convention: an
// empty dbKey means "env-only, not persisted to the database" (see the
// comments next to Port/Host/BackupDir/etc. in settings.go), and several
// distinct settings intentionally share dbKey="" for exactly that reason.
// Before this test, Set/Validate/Reset only ever checked whether the
// setting's own env var was currently set — never whether dbKey was empty
// — so calling Set on any dbKey="" setting would write to the database
// under key "", and every other dbKey="" setting would silently read that
// same row back on next load. Empirically confirmed (via a throwaway
// reproduction, not committed) that Port.Set(9999) with no env vars set
// corrupted DBPath/Host/BackupDir to "9999" after a simulated restart.
func TestEnvOnlySettingCannotBeSetOrReset(t *testing.T) {
	store := newMockStore()
	s, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	if err := s.Port.Validate(9999); err == nil {
		t.Error("Port.Validate should reject a value for an env-only (dbKey=\"\") setting")
	}
	if err := s.Port.Set(ctx, 9999); err == nil {
		t.Error("Port.Set should reject a value for an env-only (dbKey=\"\") setting")
	}
	if err := s.Port.Reset(ctx); err == nil {
		t.Error("Port.Reset should reject an env-only (dbKey=\"\") setting")
	}

	// Confirm no cross-contamination: nothing was actually persisted to the
	// shared "" key, so sibling env-only settings must be untouched.
	if len(store.saved) != 0 {
		t.Errorf("store.saved = %v, want empty — Set must not have persisted anything", store.saved)
	}
	if s.DBPath.Get() != "/data/wdbgp.sqlite3" {
		t.Errorf("DBPath = %q, want unchanged default (no cross-contamination via the shared \"\" key)", s.DBPath.Get())
	}
	if s.Host.Get() != "0.0.0.0" {
		t.Errorf("Host = %q, want unchanged default (no cross-contamination via the shared \"\" key)", s.Host.Get())
	}
}

// TestNoTwoSettingsShareANonEmptyDBKey guards against a copy-paste mistake
// giving two DB-backed settings the same real dbKey (which would cause the
// same read/write collision dbKey="" causes for env-only settings, but for
// settings that are actually meant to be independently persisted). dbKey=""
// itself is exempt — it's the documented "env-only, no DB slot" marker and
// is intentionally shared by several settings.
func TestNoTwoSettingsShareANonEmptyDBKey(t *testing.T) {
	store := newMockStore()
	s, err := New(store)
	if err != nil {
		t.Fatal(err)
	}

	seen := make(map[string]string) // dbKey -> field name
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
		dbKey, _ := keyer.dbEnvKeys()
		if dbKey == "" {
			continue
		}
		fieldName := v.Type().Field(i).Name
		if other, exists := seen[dbKey]; exists {
			t.Errorf("dbKey %q is shared by both %s and %s", dbKey, other, fieldName)
		}
		seen[dbKey] = fieldName
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

// TestNewSettings_LegacyBirdLocalAddressAlias guards against a regression
// from the internal/config -> internal/settings rewrite: existing releases
// accepted WDBGP_BIRD_LOCAL_ADDRESS(_V6) as a fallback when the newer
// WDBGP_BGP_LOCAL_ADDRESS(_V6) was unset. An install upgrading without
// renaming its env vars must not silently fall back to the hardcoded
// default (192.0.2.2 / empty), which would change the BGP local bind/
// next-hop address and could drop IPv6 announcements.
func TestNewSettings_LegacyBirdLocalAddressAlias(t *testing.T) {
	t.Setenv("WDBGP_BIRD_LOCAL_ADDRESS", "203.0.113.5")
	t.Setenv("WDBGP_BIRD_LOCAL_ADDRESS_V6", "2001:db8::5")
	store := newMockStore()
	s, err := New(store)
	if err != nil {
		t.Fatal(err)
	}

	if s.LocalAddressV4.Get() != "203.0.113.5" {
		t.Errorf("LocalAddressV4 = %q, want 203.0.113.5 (legacy alias)", s.LocalAddressV4.Get())
	}
	if s.LocalAddressV6.Get() != "2001:db8::5" {
		t.Errorf("LocalAddressV6 = %q, want 2001:db8::5 (legacy alias)", s.LocalAddressV6.Get())
	}
}

// TestNewSettings_NewLocalAddressVarTakesPriorityOverLegacy guards the
// precedence direction: the new env var name must win when both are set.
func TestNewSettings_NewLocalAddressVarTakesPriorityOverLegacy(t *testing.T) {
	t.Setenv("WDBGP_BIRD_LOCAL_ADDRESS", "203.0.113.5")
	t.Setenv("WDBGP_BGP_LOCAL_ADDRESS", "198.51.100.9")
	store := newMockStore()
	s, err := New(store)
	if err != nil {
		t.Fatal(err)
	}

	if s.LocalAddressV4.Get() != "198.51.100.9" {
		t.Errorf("LocalAddressV4 = %q, want 198.51.100.9 (new env var wins over legacy alias)", s.LocalAddressV4.Get())
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

// TestPortValidation — unlike BGPPort (a DB-backed setting), Port is
// intentionally env-only (dbKey=""), so Set must reject every value,
// valid or not, rather than silently persisting to the dbKey="" slot
// shared with DBPath/Host/BackupDir/BackupEnabled/AutoRestoreEnabled. The
// domain validation on the port range itself is exercised via the actual
// supported path in TestPortValidationRejectsInvalidEnvValue and
// TestPortValidationRejectsNegativeEnvValue.
func TestPortValidation(t *testing.T) {
	store := newMockStore()
	s, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Port.Set(context.Background(), 0); err == nil {
		t.Error("expected error for port 0 (env-only setting)")
	}
	if err := s.Port.Set(context.Background(), 8080); err == nil {
		t.Error("expected error even for an otherwise-valid port — Port is env-only and cannot be Set")
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
// <= 0 as "disabled". It also guards the upper bound: a value above 1000
// effectively disables rate limiting in practice even though it isn't
// literally <= 0.
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
	if err := s.RateLimitLogin.Set(context.Background(), 1001); err == nil {
		t.Error("expected error for rate_limit_login above 1000")
	}
	if err := s.RateLimitLogin.Set(context.Background(), 1000); err != nil {
		t.Errorf("unexpected error for rate_limit_login at the 1000 boundary: %v", err)
	}
	if err := s.RateLimitLogin.Set(context.Background(), 5); err != nil {
		t.Errorf("unexpected error for valid rate_limit_login: %v", err)
	}
	if err := s.RateLimitAdmin.Set(context.Background(), 0); err == nil {
		t.Error("expected error for rate_limit_admin 0")
	}
	if err := s.RateLimitAdmin.Set(context.Background(), 1001); err == nil {
		t.Error("expected error for rate_limit_admin above 1000")
	}
	if err := s.RateLimitAdmin.Set(context.Background(), 30); err != nil {
		t.Errorf("unexpected error for valid rate_limit_admin: %v", err)
	}
}

// TestNewSettings_StaleDBValueFallsBackToDefault guards against a real
// startup crash: a DB row for rate_limit_login=0 (e.g. from before
// validateRateLimit required a positive value, or written some other way
// that predates the current validator) parses fine as an int but fails
// validation. Before this test existed, that failure aborted New() itself —
// every future restart, including the one an admin would need to fix it
// through the running app — leaving no way to recover short of editing the
// DB file directly. A stale/invalid DB value must fall back to the default
// instead, the same way a DB value that fails to even parse already does.
func TestNewSettings_StaleDBValueFallsBackToDefault(t *testing.T) {
	store := newMockStore()
	store.settings["rate_limit_login"] = "0"
	s, err := New(store)
	if err != nil {
		t.Fatalf("New() should not fail on a stale invalid DB value, got: %v", err)
	}
	if got, want := s.RateLimitLogin.Get(), 5; got != want {
		t.Errorf("RateLimitLogin = %d, want %d (default, falling back from invalid DB value)", got, want)
	}
}

// TestNewSettings_InvalidEnvRateLimitStillFails guards the other half of the
// same fallback logic: only a DB-sourced invalid value degrades gracefully.
// An operator-supplied env var that's out of range must still fail startup
// loudly — unlike a stale DB row, there's no silent, safe substitute for a
// value someone explicitly configured just now.
func TestNewSettings_InvalidEnvRateLimitStillFails(t *testing.T) {
	t.Setenv("WDBGP_RATE_LIMIT_LOGIN", "0")
	store := newMockStore()
	if _, err := New(store); err == nil {
		t.Fatal("expected New() to fail for an out-of-range env var value")
	}
}

// TestSessionMaxAgeValidation guards against session_max_age being settable
// to an absurdly large value (sessions that never meaningfully expire) or a
// tiny nonzero one (sessions expiring almost immediately, which looks like
// "login is broken" with no clear diagnostic). 0 is a special sentinel
// (browser-session-scoped cookie, see validateSessionMaxAge) and must stay
// valid — it was the cause of a prior, since-fixed bug where SessionMaxAge=0
// deleted the cookie immediately (CHANGELOG.md).
func TestSessionMaxAgeValidation(t *testing.T) {
	store := newMockStore()
	s, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SessionMaxAge.Set(context.Background(), 0); err != nil {
		t.Errorf("unexpected error for the 0 (session cookie) sentinel: %v", err)
	}
	if err := s.SessionMaxAge.Set(context.Background(), 59); err == nil {
		t.Error("expected error for session_max_age below 60 seconds")
	}
	if err := s.SessionMaxAge.Set(context.Background(), -1); err == nil {
		t.Error("expected error for negative session_max_age")
	}
	if err := s.SessionMaxAge.Set(context.Background(), 60); err != nil {
		t.Errorf("unexpected error for session_max_age at the 60s boundary: %v", err)
	}
	if err := s.SessionMaxAge.Set(context.Background(), 31536000); err != nil {
		t.Errorf("unexpected error for session_max_age at the 1-year boundary: %v", err)
	}
	if err := s.SessionMaxAge.Set(context.Background(), 31536001); err == nil {
		t.Error("expected error for session_max_age above 1 year")
	}
	if err := s.SessionMaxAge.Set(context.Background(), 28800); err != nil {
		t.Errorf("unexpected error for valid session_max_age: %v", err)
	}
}

func TestSessionMaxAgeValidationRejectsInvalidEnvValue(t *testing.T) {
	t.Setenv("WDBGP_SESSION_MAX_AGE", "999999999999")
	store := newMockStore()
	if _, err := New(store); err == nil {
		t.Error("expected error for WDBGP_SESSION_MAX_AGE above 1 year")
	}
}

// TestLogLevelValidation guards against log_level being settable to a value
// internal/logging.parseLogLevel doesn't recognize — it would silently fall
// back to INFO there, so the admin UI could show e.g. "DEBUG" while nothing
// was actually logged at debug level. The set of accepted values must match
// parseLogLevel exactly (case-insensitively): FATAL/PANIC are deliberately
// excluded even though the old pre-rewrite validator accepted them, since
// slog itself only has Debug/Info/Warn/Error and parseLogLevel silently
// maps anything else to INFO too.
func TestLogLevelValidation(t *testing.T) {
	store := newMockStore()
	s, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"DEBUG", "INFO", "WARN", "WARNING", "ERROR", "debug", "warn"} {
		if err := s.LogLevel.Set(context.Background(), v); err != nil {
			t.Errorf("unexpected error for valid log_level %q: %v", v, err)
		}
	}
	for _, v := range []string{"banana", "TRACE", "FATAL", "PANIC", ""} {
		if err := s.LogLevel.Set(context.Background(), v); err == nil {
			t.Errorf("expected error for invalid log_level %q", v)
		}
	}
}

func TestLogLevelValidationRejectsInvalidEnvValue(t *testing.T) {
	t.Setenv("WDBGP_LOG_LEVEL", "banana")
	store := newMockStore()
	if _, err := New(store); err == nil {
		t.Error("expected error for WDBGP_LOG_LEVEL=banana")
	}
}

// TestLogFormatValidation mirrors TestLogLevelValidation for log_format —
// internal/logging.parseLogFormat only recognizes "json"/"text"
// (case-insensitively) and silently falls back to "text" for anything else.
func TestLogFormatValidation(t *testing.T) {
	store := newMockStore()
	s, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"json", "text", "JSON", "Text"} {
		if err := s.LogFormat.Set(context.Background(), v); err != nil {
			t.Errorf("unexpected error for valid log_format %q: %v", v, err)
		}
	}
	for _, v := range []string{"xml", "yaml", ""} {
		if err := s.LogFormat.Set(context.Background(), v); err == nil {
			t.Errorf("expected error for invalid log_format %q", v)
		}
	}
}

func TestLogFormatValidationRejectsInvalidEnvValue(t *testing.T) {
	t.Setenv("WDBGP_LOG_FORMAT", "xml")
	store := newMockStore()
	if _, err := New(store); err == nil {
		t.Error("expected error for WDBGP_LOG_FORMAT=xml")
	}
}

// TestValidateHost guards against WDBGP_HOST=<host>:<port> — a natural typo
// since the port is actually configured separately via WDBGP_PORT — being
// silently accepted and passed straight into the listen address.
func TestValidateHost(t *testing.T) {
	cases := []struct {
		name    string
		host    string
		wantErr bool
	}{
		{name: "plain IPv4", host: "0.0.0.0", wantErr: false},
		{name: "plain hostname", host: "localhost", wantErr: false},
		{name: "IPv4 with embedded port", host: "0.0.0.0:8080", wantErr: true},
		{name: "hostname with embedded port", host: "example.com:8080", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateHost(tc.host)
			if tc.wantErr && err == nil {
				t.Errorf("ValidateHost(%q) = nil, want error", tc.host)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("ValidateHost(%q) = %v, want nil", tc.host, err)
			}
		})
	}
}

// TestValidateHoldTime pins the RFC 4271 constraint (0 or ≥3) plus this
// app's deliberate rejection of 0 — infinite hold time turns silent peer
// death into a wedged session on the flaky links this app targets.
func TestValidateHoldTime(t *testing.T) {
	for _, v := range []uint16{0, 1, 2} {
		if err := validateHoldTime(v); err == nil {
			t.Errorf("validateHoldTime(%d) = nil, want error", v)
		}
	}
	for _, v := range []uint16{3, 90, 65535} {
		if err := validateHoldTime(v); err != nil {
			t.Errorf("validateHoldTime(%d) = %v, want nil", v, err)
		}
	}
}

func TestHostValidationRejectsEmbeddedPortInEnvValue(t *testing.T) {
	t.Setenv("WDBGP_HOST", "0.0.0.0:8080")
	store := newMockStore()
	if _, err := New(store); err == nil {
		t.Error("expected error for WDBGP_HOST with an embedded port")
	}
}

// TestValidateDBPath guards against a WDBGP_DB whose parent directory
// exists but isn't usable (not a directory, or not writable) — a
// nonexistent parent is not an error since store.Open creates it via
// os.MkdirAll.
func TestValidateDBPath(t *testing.T) {
	dir := t.TempDir()

	if err := ValidateDBPath(dir + "/sub/wdbgp.sqlite3"); err != nil {
		t.Errorf("ValidateDBPath with a not-yet-created parent = %v, want nil (store.Open creates it)", err)
	}
	if err := ValidateDBPath(dir + "/wdbgp.sqlite3"); err != nil {
		t.Errorf("ValidateDBPath with an existing writable parent = %v, want nil", err)
	}

	// Parent path exists but is a file, not a directory.
	notADir := dir + "/not-a-dir"
	if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateDBPath(notADir + "/wdbgp.sqlite3"); err == nil {
		t.Error("expected error when the parent path is a file, not a directory")
	}

	// Parent directory exists but isn't writable. Skipped as root, which
	// ignores the write permission bit entirely.
	if os.Getuid() == 0 {
		t.Skip("running as root, which bypasses the writable-directory check")
	}
	readOnlyDir := dir + "/readonly"
	if err := os.Mkdir(readOnlyDir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(readOnlyDir, 0o700) //nolint:errcheck,gosec // test cleanup so t.TempDir() can remove it
	if err := ValidateDBPath(readOnlyDir + "/wdbgp.sqlite3"); err == nil {
		t.Error("expected error when the parent directory is not writable")
	}
}

// TestDefaultWebAuthValidation guards against a typo like "password" being
// silently accepted for default_web_auth. apiUsersCreate copies this value
// into any user that omits web_auth, but requireUser only recognizes
// network/login/both/any — an unrecognized mode would lock those users out
// until manually corrected.
func TestDefaultWebAuthValidation(t *testing.T) {
	store := newMockStore()
	s, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DefaultWebAuth.Set(context.Background(), "password"); err == nil {
		t.Error("expected error for unrecognized web_auth mode")
	}
	for _, mode := range []string{"network", "login", "both", "any"} {
		if err := s.DefaultWebAuth.Set(context.Background(), mode); err != nil {
			t.Errorf("unexpected error for valid mode %q: %v", mode, err)
		}
	}
}
