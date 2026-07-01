package settings

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
)

// Settings holds all configuration fields as typed Setting interfaces.
type Settings struct {
	ActiveDial                    Setting[bool, bool]
	AdapterBackupDir              Setting[string, string]
	AdapterBackupMax              Setting[int, int]
	AdminCookieSecure             Setting[string, string]
	AdminPassword                 Setting[string, string]
	AllowDynamicPeers             Setting[bool, bool]
	AutoRestoreEnabled            Setting[bool, bool]
	BGPPort                       Setting[int, int]
	BackupDir                     Setting[string, string]
	BackupEnabled                 Setting[bool, bool]
	DBPath                        Setting[string, string]
	DefaultLanguage               Setting[string, string]
	DefaultWebAuth                Setting[string, string]
	FilterAllow                   Setting[string, string]  // global route allow filters
	FilterDeny                    Setting[string, string]  // global route deny filters
	Host                          Setting[string, string]
	JSMaxCallStack                Setting[int, int]
	JSMaxEntries                  Setting[int, int]
	JSMaxRequests                 Setting[int, int]
	JSMaxResponseBytes            Setting[int, int]
	JSMaxSourceBytes              Setting[int, int]
	JSMaxTotalBytes               Setting[int, int]
	JSTimeout                     Setting[int, int]
	LocalASN                      Setting[int, int]
	LocalAddressV4                Setting[string, string]
	LocalAddressV6                Setting[string, string]
	LogFormat                     Setting[string, string]
	LogLevel                      Setting[string, string]
	MetricsEnabled                Setting[bool, bool]
	MetricsHistoryDays            Setting[int, int]
	Port                          Setting[int, int]
	RateLimitAdmin                Setting[int, int]
	RateLimitLogin                Setting[int, int]
	RequirePasswordForNonUniqueIP Setting[bool, bool]
	RouterID                      Setting[string, string]
	SecurityHeaders               Setting[bool, bool]
	SessionMaxAge                 Setting[int, int]
	SessionSecret                 Setting[string, string]
	StatusAllowed                 Setting[string, string]
	StatusToken                   Setting[string, string]
	SyncInterval                  Setting[int, int]
	TrustProxyHeaders             Setting[bool, bool]
}

// SettingsJSON is the JSON-serializable representation of all settings.
type SettingsJSON struct {
	ActiveDial                    SettingJSON[bool]   `json:"active_dial"`
	AdapterBackupDir              SettingJSON[string] `json:"adapter_backup_dir"`
	AdapterBackupMax              SettingJSON[int]    `json:"adapter_backup_max"`
	AdminCookieSecure             SettingJSON[string] `json:"admin_cookie_secure"`
	AdminPassword                 SettingJSON[string] `json:"admin_password"`
	AllowDynamicPeers             SettingJSON[bool]   `json:"allow_dynamic_peers"`
	AutoRestoreEnabled            SettingJSON[bool]   `json:"auto_restore_enabled"`
	BGPPort                       SettingJSON[int]    `json:"bgp_port"`
	BackupDir                     SettingJSON[string] `json:"backup_dir"`
	BackupEnabled                 SettingJSON[bool]   `json:"backup_enabled"`
	DBPath                        SettingJSON[string] `json:"db_path"`
	DefaultLanguage               SettingJSON[string] `json:"default_language"`
	DefaultWebAuth                SettingJSON[string] `json:"default_web_auth"`
	FilterAllow                   SettingJSON[string] `json:"filter_allow"`
	FilterDeny                    SettingJSON[string] `json:"filter_deny"`
	Host                          SettingJSON[string] `json:"host"`
	JSMaxCallStack                SettingJSON[int]    `json:"js_max_call_stack"`
	JSMaxEntries                  SettingJSON[int]    `json:"js_max_entries"`
	JSMaxRequests                 SettingJSON[int]    `json:"js_max_requests"`
	JSMaxResponseBytes            SettingJSON[int]    `json:"js_max_response"`
	JSMaxSourceBytes              SettingJSON[int]    `json:"js_max_source"`
	JSMaxTotalBytes               SettingJSON[int]    `json:"js_max_total"`
	JSTimeout                     SettingJSON[int]    `json:"js_timeout"`
	LocalASN                      SettingJSON[int]    `json:"local_asn"`
	LocalAddressV4                SettingJSON[string] `json:"local_address_v4"`
	LocalAddressV6                SettingJSON[string] `json:"local_address_v6"`
	LogFormat                     SettingJSON[string] `json:"log_format"`
	LogLevel                      SettingJSON[string] `json:"log_level"`
	MetricsEnabled                SettingJSON[bool]   `json:"metrics_enabled"`
	MetricsHistoryDays            SettingJSON[int]    `json:"metrics_history_days"`
	Port                          SettingJSON[int]    `json:"port"`
	RateLimitAdmin                SettingJSON[int]    `json:"rate_limit_admin"`
	RateLimitLogin                SettingJSON[int]    `json:"rate_limit_login"`
	RequirePasswordForNonUniqueIP SettingJSON[bool]   `json:"require_password_for_non_unique_ip"`
	RouterID                      SettingJSON[string] `json:"router_id"`
	SecurityHeaders               SettingJSON[bool]   `json:"security_headers"`
	SessionMaxAge                 SettingJSON[int]    `json:"session_max_age"`
	SessionSecret                 SettingJSON[string] `json:"session_secret"`
	StatusAllowed                 SettingJSON[string] `json:"status_allowed"`
	StatusToken                   SettingJSON[string] `json:"status_token"`
	SyncInterval                  SettingJSON[int]    `json:"sync_interval"`
	TrustProxyHeaders             SettingJSON[bool]   `json:"trust_proxy_headers"`
}

// New creates a Settings instance with all fields initialized from env vars,
// DB values, and computed defaults.
func New(store Store) (*Settings, error) {
	// Load all DB settings once to avoid one DB query per field.
	dbSettings, err := store.GetAllSettings(context.Background())
	if err != nil {
		dbSettings = make(map[string]string) // fallback to empty
	}

	// Compute the effective DB path for derived defaults.
	dbPath := "/data/wdbgp.sqlite3"
	if envDB := os.Getenv("WDBGP_DB"); envDB != "" {
		dbPath = envDB
	}
	dbDir := filepath.Dir(dbPath)

	s := &Settings{}

	// DBPath: env-only, no dbKey.
	s.DBPath, err = newSimple("/data/wdbgp.sqlite3", "", "WDBGP_DB", parseString, nil, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// Host.
	s.Host, err = newSimple("0.0.0.0", "host", "WDBGP_HOST", parseString, nil, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// Port.
	s.Port, err = newSimple(8080, "port", "WDBGP_PORT", parseInt, nil, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// BGPPort.
	s.BGPPort, err = newSimple(179, "bgp_port", "WDBGP_BGP_PORT", parseInt, validatePort, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// LocalASN.
	s.LocalASN, err = newSimple(64512, "local_asn", "WDBGP_LOCAL_ASN", parseInt, validateASN, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// RouterID.
	s.RouterID, err = newSimple("192.0.2.1", "router_id", "WDBGP_ROUTER_ID", parseString, validateIPv4, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// LocalAddressV4.
	s.LocalAddressV4, err = newSimple("192.0.2.2", "local_address_v4", "WDBGP_BGP_LOCAL_ADDRESS", parseString, validateIPv4, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// LocalAddressV6.
	s.LocalAddressV6, err = newSimple("", "local_address_v6", "WDBGP_BGP_LOCAL_ADDRESS_V6", parseString, validateIPv6, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// AdminPassword: env-only.
	s.AdminPassword, err = newSimple("", "admin_password", "WDBGP_ADMIN_PASSWORD", parseString, nil, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// SessionSecret: env-only.
	s.SessionSecret, err = newSimple("", "session_secret", "WDBGP_SESSION_SECRET", parseString, nil, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// AdminCookieSecure.
	s.AdminCookieSecure, err = newSimple("auto", "admin_cookie_secure", "WDBGP_ADMIN_COOKIE_SECURE", parseString, nil, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// DefaultLanguage.
	s.DefaultLanguage, err = newSimple("en", "default_language", "WDBGP_DEFAULT_LANGUAGE", parseString, nil, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// TrustProxyHeaders.
	s.TrustProxyHeaders, err = newSimple(false, "trust_proxy_headers", "WDBGP_TRUST_PROXY_HEADERS", parseBool, nil, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// SyncInterval.
	s.SyncInterval, err = newSimple(3600, "sync_interval", "WDBGP_SYNC_INTERVAL", parseInt, nil, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// SecurityHeaders.
	s.SecurityHeaders, err = newSimple(false, "security_headers", "WDBGP_SECURITY_HEADERS", parseBool, nil, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// RateLimitLogin.
	s.RateLimitLogin, err = newSimple(5, "rate_limit_login", "WDBGP_RATE_LIMIT_LOGIN", parseInt, nil, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// RateLimitAdmin.
	s.RateLimitAdmin, err = newSimple(30, "rate_limit_admin", "WDBGP_RATE_LIMIT_ADMIN", parseInt, nil, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// SessionMaxAge.
	s.SessionMaxAge, err = newSimple(28800, "session_max_age", "WDBGP_SESSION_MAX_AGE", parseInt, nil, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// LogLevel.
	s.LogLevel, err = newSimple("INFO", "log_level", "WDBGP_LOG_LEVEL", parseString, nil, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// LogFormat.
	s.LogFormat, err = newSimple("text", "log_format", "WDBGP_LOG_FORMAT", parseString, nil, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// JSTimeout.
	s.JSTimeout, err = newSimple(120, "js_timeout", "WDBGP_JS_TIMEOUT", parseInt, nil, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// JSMaxSourceBytes.
	s.JSMaxSourceBytes, err = newSimple(1048576, "js_max_source", "WDBGP_JS_MAX_SOURCE", parseInt, nil, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// JSMaxResponseBytes.
	s.JSMaxResponseBytes, err = newSimple(16777216, "js_max_response", "WDBGP_JS_MAX_RESPONSE", parseInt, nil, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// JSMaxTotalBytes.
	s.JSMaxTotalBytes, err = newSimple(67108864, "js_max_total", "WDBGP_JS_MAX_TOTAL", parseInt, nil, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// JSMaxEntries.
	s.JSMaxEntries, err = newSimple(1000000, "js_max_entries", "WDBGP_JS_MAX_ENTRIES", parseInt, nil, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// JSMaxRequests.
	s.JSMaxRequests, err = newSimple(200, "js_max_requests", "WDBGP_JS_MAX_REQUESTS", parseInt, nil, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// JSMaxCallStack.
	s.JSMaxCallStack, err = newSimple(1000, "js_max_call_stack", "WDBGP_JS_MAX_CALL_STACK", parseInt, nil, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// DefaultWebAuth.
	s.DefaultWebAuth, err = newSimple("network", "default_web_auth", "WDBGP_DEFAULT_WEB_AUTH", parseString, nil, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// FilterAllow.
	s.FilterAllow, err = newSimple("", "filter_allow", "", parseString, nil, store, dbSettings)
	if err != nil {
		return nil, err
	}
	// FilterDeny.
	s.FilterDeny, err = newSimple("", "filter_deny", "", parseString, nil, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// StatusAllowed.
	s.StatusAllowed, err = newSimple("", "status_allowed", "WDBGP_STATUS_ALLOWED", parseString, nil, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// StatusToken.
	s.StatusToken, err = newSimple("", "status_token", "WDBGP_STATUS_TOKEN", parseString, nil, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// AdapterBackupDir: computed default depends on DBPath.
	s.AdapterBackupDir, err = newSimple(dbDir+"/backup/adapters", "adapter_backup_dir", "WDBGP_ADAPTER_BACKUP_DIR", parseString, nil, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// AdapterBackupMax.
	s.AdapterBackupMax, err = newSimple(10, "adapter_backup_max", "WDBGP_ADAPTER_BACKUP_MAX", parseInt, nil, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// RequirePasswordForNonUniqueIP.
	s.RequirePasswordForNonUniqueIP, err = newSimple(true, "require_password_for_non_unique_ip", "WDBGP_REQUIRE_PASSWORD_FOR_NON_UNIQUE_IP", parseBool, nil, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// AllowDynamicPeers.
	s.AllowDynamicPeers, err = newSimple(false, "allow_dynamic_peers", "WDBGP_ALLOW_DYNAMIC_PEERS", parseBool, nil, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// ActiveDial.
	s.ActiveDial, err = newSimple(true, "active_dial", "WDBGP_ACTIVE_DIAL", parseBool, nil, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// BackupEnabled.
	s.BackupEnabled, err = newSimple(true, "backup_enabled", "WDBGP_BACKUP_ENABLED", parseBool, nil, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// BackupDir: computed default depends on DBPath.
	s.BackupDir, err = newSimple(dbDir, "backup_dir", "WDBGP_BACKUP_DIR", parseString, nil, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// AutoRestoreEnabled.
	s.AutoRestoreEnabled, err = newSimple(false, "auto_restore_enabled", "WDBGP_AUTO_RESTORE_ENABLED", parseBool, nil, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// MetricsEnabled: DB-only (no env var).
	s.MetricsEnabled, err = newSimple(false, "metrics_enabled", "", parseBool, nil, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// MetricsHistoryDays: DB-only (no env var).
	s.MetricsHistoryDays, err = newSimple(14, "metrics_history_days", "", parseInt, nil, store, dbSettings)
	if err != nil {
		return nil, err
	}

	return s, nil
}

// JSON returns a serializable representation of all settings.
func (s *Settings) JSON(ctx context.Context) SettingsJSON {
	dbSettings, _ := s.store().GetAllSettings(ctx)
	if dbSettings == nil {
		dbSettings = make(map[string]string)
	}
	j := SettingsJSON{
		ActiveDial:                    s.ActiveDial.JSON(dbSettings),
		AdapterBackupDir:              s.AdapterBackupDir.JSON(dbSettings),
		AdapterBackupMax:              s.AdapterBackupMax.JSON(dbSettings),
		AdminCookieSecure:             s.AdminCookieSecure.JSON(dbSettings),
		AdminPassword:                 s.AdminPassword.JSON(dbSettings),
		AllowDynamicPeers:             s.AllowDynamicPeers.JSON(dbSettings),
		AutoRestoreEnabled:            s.AutoRestoreEnabled.JSON(dbSettings),
		BGPPort:                       s.BGPPort.JSON(dbSettings),
		BackupDir:                     s.BackupDir.JSON(dbSettings),
		BackupEnabled:                 s.BackupEnabled.JSON(dbSettings),
		DBPath:                        s.DBPath.JSON(dbSettings),
		DefaultLanguage:               s.DefaultLanguage.JSON(dbSettings),
		DefaultWebAuth:                s.DefaultWebAuth.JSON(dbSettings),
		FilterAllow:                   s.FilterAllow.JSON(dbSettings),
		FilterDeny:                    s.FilterDeny.JSON(dbSettings),
		Host:                          s.Host.JSON(dbSettings),
		JSMaxCallStack:                s.JSMaxCallStack.JSON(dbSettings),
		JSMaxEntries:                  s.JSMaxEntries.JSON(dbSettings),
		JSMaxRequests:                 s.JSMaxRequests.JSON(dbSettings),
		JSMaxResponseBytes:            s.JSMaxResponseBytes.JSON(dbSettings),
		JSMaxSourceBytes:              s.JSMaxSourceBytes.JSON(dbSettings),
		JSMaxTotalBytes:               s.JSMaxTotalBytes.JSON(dbSettings),
		JSTimeout:                     s.JSTimeout.JSON(dbSettings),
		LocalASN:                      s.LocalASN.JSON(dbSettings),
		LocalAddressV4:                s.LocalAddressV4.JSON(dbSettings),
		LocalAddressV6:                s.LocalAddressV6.JSON(dbSettings),
		LogFormat:                     s.LogFormat.JSON(dbSettings),
		LogLevel:                      s.LogLevel.JSON(dbSettings),
		MetricsEnabled:                s.MetricsEnabled.JSON(dbSettings),
		MetricsHistoryDays:            s.MetricsHistoryDays.JSON(dbSettings),
		Port:                          s.Port.JSON(dbSettings),
		RateLimitAdmin:                s.RateLimitAdmin.JSON(dbSettings),
		RateLimitLogin:                s.RateLimitLogin.JSON(dbSettings),
		RequirePasswordForNonUniqueIP: s.RequirePasswordForNonUniqueIP.JSON(dbSettings),
		RouterID:                      s.RouterID.JSON(dbSettings),
		SecurityHeaders:               s.SecurityHeaders.JSON(dbSettings),
		SessionMaxAge:                 s.SessionMaxAge.JSON(dbSettings),
		SessionSecret:                 s.SessionSecret.JSON(dbSettings),
		StatusAllowed:                 s.StatusAllowed.JSON(dbSettings),
		StatusToken:                   s.StatusToken.JSON(dbSettings),
		SyncInterval:                  s.SyncInterval.JSON(dbSettings),
		TrustProxyHeaders:             s.TrustProxyHeaders.JSON(dbSettings),
	}

	// Never expose secret values — always nil
	j.AdminPassword.Value = nil
	j.SessionSecret.Value = nil

	return j
}

// store returns the Store from the first non-nil setting field.
// All fields share the same store, so we just pick the first one.
func (s *Settings) store() Store {
	// Any field will do — they all share the same store.
	// Use a field that always has a dbKey (not env-only with empty dbKey).
	if ss, ok := s.Port.(*simpleSetting[int]); ok {
		return ss.store
	}
	// Fallback: shouldn't happen, but use Host.
	if ss, ok := s.Host.(*simpleSetting[string]); ok {
		return ss.store
	}
	return nil
}

// validatePort checks that a port number is in the valid range 1-65535.
func validatePort(v int) error {
	if v < 1 || v > 65535 {
		return fmt.Errorf("port must be 1-65535, got %d", v)
	}
	return nil
}

// validateASN checks that an ASN is in the valid range 1-4294967295.
func validateASN(v int) error {
	if v < 1 || v > 4294967295 {
		return fmt.Errorf("ASN must be 1-4294967295, got %d", v)
	}
	return nil
}

// validateIPv4 checks that a string is a valid IPv4 address.
func validateIPv4(v string) error {
	ip, err := netip.ParseAddr(v)
	if err != nil {
		return fmt.Errorf("must be a valid IPv4 address, got %q", v)
	}
	if !ip.Is4() {
		return fmt.Errorf("must be a valid IPv4 address, got %q", v)
	}
	return nil
}

// validateIPv6 checks that a string is a valid IPv6 address (empty is allowed).
func validateIPv6(v string) error {
	if v == "" {
		return nil
	}
	ip, err := netip.ParseAddr(v)
	if err != nil {
		return fmt.Errorf("invalid IPv6 address %q: %w", v, err)
	}
	if !ip.Is6() {
		return fmt.Errorf("must be a valid IPv6 address, got %q", v)
	}
	return nil
}
