package settings

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
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
	BGPPort                       Setting[uint16, uint16]
	BackupDir                     Setting[string, string]
	BackupEnabled                 Setting[bool, bool]
	DBPath                        Setting[string, string]
	DefaultLanguage               Setting[string, string]
	DefaultWebAuth                Setting[string, string]
	FilterAllow                   Setting[string, string] // global route allow filters
	FilterDeny                    Setting[string, string] // global route deny filters
	Host                          Setting[string, string]
	JSMaxCallStack                Setting[int, int]
	JSMaxEntries                  Setting[int, int]
	JSMaxRequests                 Setting[int, int]
	JSMaxResponseBytes            Setting[int, int]
	JSMaxSourceBytes              Setting[int, int]
	JSMaxTotalBytes               Setting[int, int]
	JSTimeout                     Setting[int, int]
	LocalASN                      Setting[uint32, uint32]
	LocalAddressV4                Setting[string, string]
	LocalAddressV6                Setting[string, string]
	LogFormat                     Setting[string, string]
	LogLevel                      Setting[string, string]
	MetricsEnabled                Setting[bool, bool]
	MetricsHistoryDays            Setting[int, int]
	Port                          Setting[uint16, uint16]
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
	BGPPort                       SettingJSON[uint16] `json:"bgp_port"`
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
	LocalASN                      SettingJSON[uint32] `json:"local_asn"`
	LocalAddressV4                SettingJSON[string] `json:"local_address_v4"`
	LocalAddressV6                SettingJSON[string] `json:"local_address_v6"`
	LogFormat                     SettingJSON[string] `json:"log_format"`
	LogLevel                      SettingJSON[string] `json:"log_level"`
	MetricsEnabled                SettingJSON[bool]   `json:"metrics_enabled"`
	MetricsHistoryDays            SettingJSON[int]    `json:"metrics_history_days"`
	Port                          SettingJSON[uint16] `json:"port"`
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

	// DBPath: env-only, no dbKey. Also validated directly by cmd/wdbgp
	// before store.Open is called — by the time this constructor runs, the
	// store has already been opened once from the same env var, so this
	// validation here is a backstop, not the first line of defense.
	s.DBPath, err = newSimple("/data/wdbgp.sqlite3", "", "WDBGP_DB", parseString, ValidateDBPath, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// Host: env-only, no dbKey — the HTTP listen address is infrastructure
	// identity (like DBPath), not something safe to edit from a form in
	// the app that's already listening on it. A bad value here can lock
	// an admin out of the UI with no way to fix it short of shell/redeploy
	// access, and it already required a restart to take effect anyway.
	s.Host, err = newSimple("0.0.0.0", "", "WDBGP_HOST", parseString, ValidateHost, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// Port: env-only, no dbKey — same reasoning as Host above.
	s.Port, err = newSimple[uint16](8080, "", "WDBGP_PORT", parseUint16, validatePort, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// BGPPort.
	s.BGPPort, err = newSimple[uint16](179, "bgp_port", "WDBGP_BGP_PORT", parseUint16, validatePort, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// LocalASN.
	s.LocalASN, err = newSimple[uint32](64512, "local_asn", "WDBGP_LOCAL_ASN", parseUint32, validateASN, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// RouterID.
	s.RouterID, err = newSimple("192.0.2.1", "router_id", "WDBGP_ROUTER_ID", parseString, validateIPv4, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// LocalAddressV4. WDBGP_BIRD_LOCAL_ADDRESS is a legacy alias from before
	// the GoBGP rewrite — used as the default (so WDBGP_BGP_LOCAL_ADDRESS and
	// any DB-stored value still take priority) so an install upgrading
	// without renaming its env vars doesn't silently fall back to
	// 192.0.2.2, changing its BGP local bind/next-hop address.
	localAddrV4Default := "192.0.2.2"
	if legacy := os.Getenv("WDBGP_BIRD_LOCAL_ADDRESS"); legacy != "" {
		localAddrV4Default = legacy
	}
	s.LocalAddressV4, err = newSimple(localAddrV4Default, "local_address_v4", "WDBGP_BGP_LOCAL_ADDRESS", parseString, validateIPv4, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// LocalAddressV6. Same legacy-alias handling as LocalAddressV4.
	localAddrV6Default := ""
	if legacy := os.Getenv("WDBGP_BIRD_LOCAL_ADDRESS_V6"); legacy != "" {
		localAddrV6Default = legacy
	}
	s.LocalAddressV6, err = newSimple(localAddrV6Default, "local_address_v6", "WDBGP_BGP_LOCAL_ADDRESS_V6", parseString, validateIPv6, store, dbSettings)
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
	s.SyncInterval, err = newSimple(3600, "sync_interval", "WDBGP_SYNC_INTERVAL", parseInt, validatePositive, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// SecurityHeaders.
	s.SecurityHeaders, err = newSimple(false, "security_headers", "WDBGP_SECURITY_HEADERS", parseBool, nil, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// RateLimitLogin.
	s.RateLimitLogin, err = newSimple(5, "rate_limit_login", "WDBGP_RATE_LIMIT_LOGIN", parseInt, validateRateLimit, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// RateLimitAdmin.
	s.RateLimitAdmin, err = newSimple(30, "rate_limit_admin", "WDBGP_RATE_LIMIT_ADMIN", parseInt, validateRateLimit, store, dbSettings)
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
	s.DefaultWebAuth, err = newSimple("network", "default_web_auth", "WDBGP_DEFAULT_WEB_AUTH", parseString, validateWebAuthMode, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// FilterAllow.
	s.FilterAllow, err = newSimple("", "filter_allow", "", parseString, validateFilterList, store, dbSettings)
	if err != nil {
		return nil, err
	}
	// FilterDeny.
	s.FilterDeny, err = newSimple("", "filter_deny", "", parseString, validateFilterList, store, dbSettings)
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

	// ActiveDial: defaults to false. Was true early on; that turned out to be
	// the wrong default for an alpha product, so it changed — deliberately
	// not backfilled the way the per-user active_dial column is (migration
	// 023), since this is the "we got it wrong, don't perpetuate it even
	// implicitly" case, not a "preserve existing behavior" one. Any install
	// that never explicitly touched this setting will flip from
	// effectively-on to effectively-off on next restart after upgrading.
	s.ActiveDial, err = newSimple(false, "active_dial", "WDBGP_ACTIVE_DIAL", parseBool, nil, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// BackupEnabled, BackupDir, AutoRestoreEnabled: env-only, no dbKey.
	// main.go reads these three straight from the environment to call
	// store.Open() — before settings.New() (this function) ever runs, since
	// there's no DB to read DB-backed values from until Open() has decided
	// whether to back up/restore it. A DB-stored value here would never be
	// read by anything: editing them via the UI would silently do nothing
	// until the value is also set as an env var, same reasoning as Host
	// above.
	s.BackupEnabled, err = newSimple(true, "", "WDBGP_BACKUP_ENABLED", parseBool, nil, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// BackupDir: computed default depends on DBPath.
	s.BackupDir, err = newSimple(dbDir, "", "WDBGP_BACKUP_DIR", parseString, nil, store, dbSettings)
	if err != nil {
		return nil, err
	}

	// AutoRestoreEnabled.
	s.AutoRestoreEnabled, err = newSimple(false, "", "WDBGP_AUTO_RESTORE_ENABLED", parseBool, nil, store, dbSettings)
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
	dbSettings, err := s.store().GetAllSettings(ctx)
	if err != nil {
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
	if ss, ok := s.Port.(*simpleSetting[uint16]); ok {
		return ss.store
	}
	// Fallback: shouldn't happen, but use Host.
	if ss, ok := s.Host.(*simpleSetting[string]); ok {
		return ss.store
	}
	return nil
}

// validatePositive checks that a value is greater than zero.
func validatePositive(v int) error {
	if v <= 0 {
		return fmt.Errorf("must be positive, got %d", v)
	}
	return nil
}

// validateRateLimit checks that a per-minute rate limit is positive and
// capped at 1000 — well above any legitimate login/admin request rate, but
// low enough that a misconfigured value can't silently disable brute-force
// protection.
func validateRateLimit(v int) error {
	if v <= 0 {
		return fmt.Errorf("must be positive, got %d", v)
	}
	if v > 1000 {
		return fmt.Errorf("must not exceed 1000 requests per minute, got %d", v)
	}
	return nil
}

// validatePort checks that a port number is in the valid range 1-65535.
// The upper bound is enforced by uint16's width itself; only 0 needs
// rejecting here.
func validatePort(v uint16) error {
	if v == 0 {
		return fmt.Errorf("port must be 1-65535, got %d", v)
	}
	return nil
}

// validateASN checks that an ASN is in the valid range 1-4294967295.
// The upper bound is enforced by uint32's width itself; only 0 needs
// rejecting here.
func validateASN(v uint32) error {
	if v == 0 {
		return fmt.Errorf("ASN must be 1-4294967295, got %d", v)
	}
	return nil
}

// ValidateHost checks that a listen-address host doesn't have a port
// accidentally embedded in it (a common typo: WDBGP_HOST=0.0.0.0:8080
// instead of setting WDBGP_PORT separately). Exported so cmd/wdbgp can
// validate WDBGP_HOST before it's used to construct the listen address.
func ValidateHost(v string) error {
	if _, _, err := net.SplitHostPort(v); err == nil {
		return fmt.Errorf("must not include a port number (port is configured separately via WDBGP_PORT), got %q", v)
	}
	return nil
}

// ValidateDBPath checks that a database file's parent directory, if it
// already exists, is actually a directory and is writable. A parent
// directory that doesn't exist yet is not an error here — store.Open
// creates it via os.MkdirAll. Exported so cmd/wdbgp can validate WDBGP_DB
// before calling store.Open: by the time settings.New() runs, the store
// (and thus a file at this path) has already been opened once, so this
// check only has fail-fast value if run before that call.
func ValidateDBPath(v string) error {
	dir := filepath.Dir(v)
	stat, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("cannot stat parent directory %s: %w", dir, err)
	}
	if !stat.IsDir() {
		return fmt.Errorf("parent path %s is not a directory", dir)
	}
	if stat.Mode().Perm()&0o200 == 0 {
		return fmt.Errorf("directory %s is not writable", dir)
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

// validateFilterList checks that a newline-separated list of CIDRs (the
// storage format for filter_allow/filter_deny) contains only well-formed
// prefixes. Blank lines and #-prefixed comments are skipped, matching how
// store.splitNewlines interprets the same value when building the filter
// at reconcile time — an entry this validator accepts is guaranteed to
// still parse there.
func validateFilterList(v string) error {
	for _, line := range strings.Split(v, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if _, err := netip.ParsePrefix(line); err != nil {
			return fmt.Errorf("invalid CIDR %q: %w", line, err)
		}
	}
	return nil
}

// validateWebAuthMode checks that a value is one of the recognized
// per-user web_auth modes. apiUsersCreate copies default_web_auth into any
// user that omits web_auth, so an unrecognized value here would silently
// lock those users out — requireUser (internal/web) only recognizes
// network/login/both/any. Kept in sync by hand with internal/web's
// isValidWebAuth (handlers_api_users.go); settings can't import web, which
// already imports settings.
func validateWebAuthMode(v string) error {
	switch v {
	case "network", "login", "both", "any":
		return nil
	default:
		return fmt.Errorf("web_auth must be one of network, login, both, any, got %q", v)
	}
}
