package settings

import (
	"os"
	"path/filepath"
)

// Settings holds all configuration fields as typed Setting[T] pointers.
type Settings struct {
	ActiveDial                    *Setting[bool]
	AdapterBackupDir              *Setting[string]
	AdapterBackupMax              *Setting[int]
	AdminCookieSecure             *Setting[string]
	AdminPassword                 *Setting[string]
	AllowDynamicPeers             *Setting[bool]
	AutoRestoreEnabled            *Setting[bool]
	BGPPort                       *Setting[int]
	BackupDir                     *Setting[string]
	BackupEnabled                 *Setting[bool]
	DBPath                        *Setting[string]
	DefaultLanguage               *Setting[string]
	DefaultWebAuth                *Setting[string]
	Host                          *Setting[string]
	JSMaxCallStack                *Setting[int]
	JSMaxEntries                  *Setting[int]
	JSMaxRequests                 *Setting[int]
	JSMaxResponseBytes            *Setting[int]
	JSMaxSourceBytes              *Setting[int]
	JSMaxTotalBytes               *Setting[int]
	JSTimeout                     *Setting[int]
	LocalASN                      *Setting[int]
	LocalAddressV4                *Setting[string]
	LocalAddressV6                *Setting[string]
	LogFormat                     *Setting[string]
	LogLevel                      *Setting[string]
	MetricsEnabled                *Setting[bool]
	MetricsHistoryDays            *Setting[int]
	Port                          *Setting[int]
	RateLimitAdmin                *Setting[int]
	RateLimitLogin                *Setting[int]
	RequirePasswordForNonUniqueIP *Setting[bool]
	RouterID                      *Setting[string]
	SecurityHeaders               *Setting[bool]
	SessionMaxAge                 *Setting[int]
	SessionSecret                 *Setting[string]
	StatusAllowed                 *Setting[string]
	StatusToken                   *Setting[string]
	SyncInterval                  *Setting[int]
	TrustProxyHeaders             *Setting[bool]
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

// New creates a Settings instance with all 40 fields initialized from env vars,
// DB values, and computed defaults.
func New(store Store) (*Settings, error) {
	// Compute the effective DB path for derived defaults.
	dbPath := "/data/wdbgp.sqlite3"
	if envDB := os.Getenv("WDBGP_DB"); envDB != "" {
		dbPath = envDB
	}
	dbDir := filepath.Dir(dbPath)

	s := &Settings{}
	var err error

	// DBPath: env-only, no dbKey.
	s.DBPath, err = newSetting("/data/wdbgp.sqlite3", "", "WDBGP_DB", parseString, store)
	if err != nil {
		return nil, err
	}

	// Host.
	s.Host, err = newSetting("0.0.0.0", "host", "WDBGP_HOST", parseString, store)
	if err != nil {
		return nil, err
	}

	// Port.
	s.Port, err = newSetting(8080, "port", "WDBGP_PORT", parseInt, store)
	if err != nil {
		return nil, err
	}

	// BGPPort.
	s.BGPPort, err = newSetting(179, "bgp_port", "WDBGP_BGP_PORT", parseInt, store)
	if err != nil {
		return nil, err
	}

	// LocalASN.
	s.LocalASN, err = newSetting(64512, "local_asn", "WDBGP_LOCAL_ASN", parseInt, store)
	if err != nil {
		return nil, err
	}

	// RouterID.
	s.RouterID, err = newSetting("192.0.2.1", "router_id", "WDBGP_ROUTER_ID", parseString, store)
	if err != nil {
		return nil, err
	}

	// LocalAddressV4.
	s.LocalAddressV4, err = newSetting("192.0.2.2", "local_address_v4", "WDBGP_BGP_LOCAL_ADDRESS", parseString, store)
	if err != nil {
		return nil, err
	}

	// LocalAddressV6.
	s.LocalAddressV6, err = newSetting("", "local_address_v6", "WDBGP_BGP_LOCAL_ADDRESS_V6", parseString, store)
	if err != nil {
		return nil, err
	}

	// AdminPassword: env-only.
	s.AdminPassword, err = newSetting("", "", "WDBGP_ADMIN_PASSWORD", parseString, store)
	if err != nil {
		return nil, err
	}

	// SessionSecret: env-only.
	s.SessionSecret, err = newSetting("", "", "WDBGP_SESSION_SECRET", parseString, store)
	if err != nil {
		return nil, err
	}

	// AdminCookieSecure.
	s.AdminCookieSecure, err = newSetting("auto", "admin_cookie_secure", "WDBGP_ADMIN_COOKIE_SECURE", parseString, store)
	if err != nil {
		return nil, err
	}

	// DefaultLanguage.
	s.DefaultLanguage, err = newSetting("en", "default_language", "WDBGP_DEFAULT_LANGUAGE", parseString, store)
	if err != nil {
		return nil, err
	}

	// TrustProxyHeaders.
	s.TrustProxyHeaders, err = newSetting(false, "trust_proxy_headers", "WDBGP_TRUST_PROXY_HEADERS", parseBool, store)
	if err != nil {
		return nil, err
	}

	// SyncInterval.
	s.SyncInterval, err = newSetting(3600, "sync_interval", "WDBGP_SYNC_INTERVAL", parseInt, store)
	if err != nil {
		return nil, err
	}

	// SecurityHeaders.
	s.SecurityHeaders, err = newSetting(false, "security_headers", "WDBGP_SECURITY_HEADERS", parseBool, store)
	if err != nil {
		return nil, err
	}

	// RateLimitLogin.
	s.RateLimitLogin, err = newSetting(5, "rate_limit_login", "WDBGP_RATE_LIMIT_LOGIN", parseInt, store)
	if err != nil {
		return nil, err
	}

	// RateLimitAdmin.
	s.RateLimitAdmin, err = newSetting(30, "rate_limit_admin", "WDBGP_RATE_LIMIT_ADMIN", parseInt, store)
	if err != nil {
		return nil, err
	}

	// SessionMaxAge.
	s.SessionMaxAge, err = newSetting(28800, "session_max_age", "WDBGP_SESSION_MAX_AGE", parseInt, store)
	if err != nil {
		return nil, err
	}

	// LogLevel.
	s.LogLevel, err = newSetting("INFO", "log_level", "WDBGP_LOG_LEVEL", parseString, store)
	if err != nil {
		return nil, err
	}

	// LogFormat.
	s.LogFormat, err = newSetting("text", "log_format", "WDBGP_LOG_FORMAT", parseString, store)
	if err != nil {
		return nil, err
	}

	// JSTimeout.
	s.JSTimeout, err = newSetting(120, "js_timeout", "WDBGP_JS_TIMEOUT", parseInt, store)
	if err != nil {
		return nil, err
	}

	// JSMaxSourceBytes.
	s.JSMaxSourceBytes, err = newSetting(1048576, "js_max_source", "WDBGP_JS_MAX_SOURCE", parseInt, store)
	if err != nil {
		return nil, err
	}

	// JSMaxResponseBytes.
	s.JSMaxResponseBytes, err = newSetting(16777216, "js_max_response", "WDBGP_JS_MAX_RESPONSE", parseInt, store)
	if err != nil {
		return nil, err
	}

	// JSMaxTotalBytes.
	s.JSMaxTotalBytes, err = newSetting(67108864, "js_max_total", "WDBGP_JS_MAX_TOTAL", parseInt, store)
	if err != nil {
		return nil, err
	}

	// JSMaxEntries.
	s.JSMaxEntries, err = newSetting(1000000, "js_max_entries", "WDBGP_JS_MAX_ENTRIES", parseInt, store)
	if err != nil {
		return nil, err
	}

	// JSMaxRequests.
	s.JSMaxRequests, err = newSetting(200, "js_max_requests", "WDBGP_JS_MAX_REQUESTS", parseInt, store)
	if err != nil {
		return nil, err
	}

	// JSMaxCallStack.
	s.JSMaxCallStack, err = newSetting(1000, "js_max_call_stack", "WDBGP_JS_MAX_CALL_STACK", parseInt, store)
	if err != nil {
		return nil, err
	}

	// DefaultWebAuth.
	s.DefaultWebAuth, err = newSetting("network", "default_web_auth", "WDBGP_DEFAULT_WEB_AUTH", parseString, store)
	if err != nil {
		return nil, err
	}

	// StatusAllowed.
	s.StatusAllowed, err = newSetting("", "status_allowed", "WDBGP_STATUS_ALLOWED", parseString, store)
	if err != nil {
		return nil, err
	}

	// StatusToken.
	s.StatusToken, err = newSetting("", "status_token", "WDBGP_STATUS_TOKEN", parseString, store)
	if err != nil {
		return nil, err
	}

	// AdapterBackupDir: computed default depends on DBPath.
	s.AdapterBackupDir, err = newSetting(dbDir+"/backup/adapters", "adapter_backup_dir", "WDBGP_ADAPTER_BACKUP_DIR", parseString, store)
	if err != nil {
		return nil, err
	}

	// AdapterBackupMax.
	s.AdapterBackupMax, err = newSetting(10, "adapter_backup_max", "WDBGP_ADAPTER_BACKUP_MAX", parseInt, store)
	if err != nil {
		return nil, err
	}

	// RequirePasswordForNonUniqueIP: env-only.
	s.RequirePasswordForNonUniqueIP, err = newSetting(true, "", "WDBGP_REQUIRE_PASSWORD_FOR_NON_UNIQUE_IP", parseBool, store)
	if err != nil {
		return nil, err
	}

	// AllowDynamicPeers: env-only.
	s.AllowDynamicPeers, err = newSetting(false, "", "WDBGP_ALLOW_DYNAMIC_PEERS", parseBool, store)
	if err != nil {
		return nil, err
	}

	// ActiveDial: env-only.
	s.ActiveDial, err = newSetting(true, "", "WDBGP_ACTIVE_DIAL", parseBool, store)
	if err != nil {
		return nil, err
	}

	// BackupEnabled: env-only.
	s.BackupEnabled, err = newSetting(true, "", "WDBGP_BACKUP_ENABLED", parseBool, store)
	if err != nil {
		return nil, err
	}

	// BackupDir: env-only, computed default depends on DBPath.
	s.BackupDir, err = newSetting(dbDir, "", "WDBGP_BACKUP_DIR", parseString, store)
	if err != nil {
		return nil, err
	}

	// AutoRestoreEnabled: env-only.
	s.AutoRestoreEnabled, err = newSetting(false, "", "WDBGP_AUTO_RESTORE_ENABLED", parseBool, store)
	if err != nil {
		return nil, err
	}

	// MetricsEnabled: DB-only (no env var).
	s.MetricsEnabled, err = newSetting(false, "metrics_enabled", "", parseBool, store)
	if err != nil {
		return nil, err
	}

	// MetricsHistoryDays: DB-only (no env var).
	s.MetricsHistoryDays, err = newSetting(14, "metrics_history_days", "", parseInt, store)
	if err != nil {
		return nil, err
	}

	return s, nil
}

// JSON returns a serializable representation of all settings.
func (s *Settings) JSON() SettingsJSON {
	return SettingsJSON{
		ActiveDial:                    s.ActiveDial.JSON(),
		AdapterBackupDir:              s.AdapterBackupDir.JSON(),
		AdapterBackupMax:              s.AdapterBackupMax.JSON(),
		AdminCookieSecure:             s.AdminCookieSecure.JSON(),
		AdminPassword:                 s.AdminPassword.JSON(),
		AllowDynamicPeers:             s.AllowDynamicPeers.JSON(),
		AutoRestoreEnabled:            s.AutoRestoreEnabled.JSON(),
		BGPPort:                       s.BGPPort.JSON(),
		BackupDir:                     s.BackupDir.JSON(),
		BackupEnabled:                 s.BackupEnabled.JSON(),
		DBPath:                        s.DBPath.JSON(),
		DefaultLanguage:               s.DefaultLanguage.JSON(),
		DefaultWebAuth:                s.DefaultWebAuth.JSON(),
		Host:                          s.Host.JSON(),
		JSMaxCallStack:                s.JSMaxCallStack.JSON(),
		JSMaxEntries:                  s.JSMaxEntries.JSON(),
		JSMaxRequests:                 s.JSMaxRequests.JSON(),
		JSMaxResponseBytes:            s.JSMaxResponseBytes.JSON(),
		JSMaxSourceBytes:              s.JSMaxSourceBytes.JSON(),
		JSMaxTotalBytes:               s.JSMaxTotalBytes.JSON(),
		JSTimeout:                     s.JSTimeout.JSON(),
		LocalASN:                      s.LocalASN.JSON(),
		LocalAddressV4:                s.LocalAddressV4.JSON(),
		LocalAddressV6:                s.LocalAddressV6.JSON(),
		LogFormat:                     s.LogFormat.JSON(),
		LogLevel:                      s.LogLevel.JSON(),
		MetricsEnabled:                s.MetricsEnabled.JSON(),
		MetricsHistoryDays:            s.MetricsHistoryDays.JSON(),
		Port:                          s.Port.JSON(),
		RateLimitAdmin:                s.RateLimitAdmin.JSON(),
		RateLimitLogin:                s.RateLimitLogin.JSON(),
		RequirePasswordForNonUniqueIP: s.RequirePasswordForNonUniqueIP.JSON(),
		RouterID:                      s.RouterID.JSON(),
		SecurityHeaders:               s.SecurityHeaders.JSON(),
		SessionMaxAge:                 s.SessionMaxAge.JSON(),
		SessionSecret:                 s.SessionSecret.JSON(),
		StatusAllowed:                 s.StatusAllowed.JSON(),
		StatusToken:                   s.StatusToken.JSON(),
		SyncInterval:                  s.SyncInterval.JSON(),
		TrustProxyHeaders:             s.TrustProxyHeaders.JSON(),
	}
}
