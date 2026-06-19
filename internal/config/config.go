package config

import (
	"fmt"
	"net"
	"net/netip"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	DBPath            string
	Host              string
	Port              int
	BGPListenPort     int32
	LocalASN          uint32
	RouterID          string
	LocalAddressV4    string
	LocalAddressV6    string
	AdminPassword     string
	SessionSecret     string
	AdminCookieSecure string
	DefaultLanguage   string
	TrustProxyHeader  bool
	SyncInterval      time.Duration
	SecurityHeaders   bool
	RateLimitLogin    int
	RateLimitAdmin    int
	SessionMaxAge     int
	LogLevel           string
	LogFormat          string
	JSTimeout          time.Duration
	JSMaxSourceBytes   int
	JSMaxResponseBytes int
	JSMaxTotalBytes    int
	JSMaxEntries       int
	JSMaxRequests      int
	JSMaxCallStack     int
	DefaultWebAuth     string
	StatusAllowed      []string            // comma-separated CIDRs for /status access
	StatusToken        string              // Bearer token for /status access
	AdapterBackupDir   string              // backup directory for adapter sources (empty = disabled)
	AdapterBackupMax   int                 // max backup copies per adapter
	RequirePasswordForNonUniqueIP bool     // require BGP password when sharing IP with different ASN
}

func Load() (Config, error) {
	port, err := validatePort("WDBGP_PORT", 8080)
	if err != nil {
		return Config{}, err
	}
	asn, err := validateASN("WDBGP_LOCAL_ASN", 64512)
	if err != nil {
		return Config{}, err
	}
	syncSeconds, err := validateSyncInterval("WDBGP_SYNC_INTERVAL", 3600)
	if err != nil {
		return Config{}, err
	}
	bgpPort, err := validatePort32("WDBGP_BGP_PORT", 179)
	if err != nil {
		return Config{}, err
	}
	rateLimitLogin, err := validateRateLimit("WDBGP_RATE_LIMIT_LOGIN", 5)
	if err != nil {
		return Config{}, err
	}
	rateLimitAdmin, err := validateRateLimit("WDBGP_RATE_LIMIT_ADMIN", 30)
	if err != nil {
		return Config{}, err
	}
	sessionMaxAge, err := validateSessionMaxAge("WDBGP_SESSION_MAX_AGE", 28800) // 8 hours in seconds
	if err != nil {
		return Config{}, err
	}
	logLevel, err := validateLogLevel("WDBGP_LOG_LEVEL", "INFO")
	if err != nil {
		return Config{}, err
	}
	logFormat, err := validateLogFormat("WDBGP_LOG_FORMAT", "text")
	if err != nil {
		return Config{}, err
	}
	host, err := validateHost("WDBGP_HOST", "0.0.0.0")
	if err != nil {
		return Config{}, err
	}
	dbPath, err := validateDBPath("WDBGP_DB", "/data/wdbgp.sqlite3")
	if err != nil {
		return Config{}, err
	}
	routerID, err := validateIPv4Address("WDBGP_ROUTER_ID", "192.0.2.1")
	if err != nil {
		return Config{}, err
	}
	localAddressV4, err := validateIPv4Address("WDBGP_BGP_LOCAL_ADDRESS", env("WDBGP_BIRD_LOCAL_ADDRESS", "192.0.2.2"))
	if err != nil {
		return Config{}, err
	}
	localAddressV6, err := validateIPv6Address("WDBGP_BGP_LOCAL_ADDRESS_V6", env("WDBGP_BIRD_LOCAL_ADDRESS_V6", ""))
	if err != nil {
		return Config{}, err
	}
	adminCookieSecure, err := validateAdminCookieSecure("WDBGP_ADMIN_COOKIE_SECURE", "auto")
	if err != nil {
		return Config{}, err
	}
	defaultLanguage, err := validateDefaultLanguage("WDBGP_DEFAULT_LANGUAGE", "en")
	if err != nil {
		return Config{}, err
	}
	jsTimeout, err := integer("WDBGP_JS_TIMEOUT", 120)
	if err != nil {
		return Config{}, err
	}
	jsMaxSource, err := integer("WDBGP_JS_MAX_SOURCE", 1048576)
	if err != nil {
		return Config{}, err
	}
	jsMaxResponse, err := integer("WDBGP_JS_MAX_RESPONSE", 16777216)
	if err != nil {
		return Config{}, err
	}
	jsMaxTotal, err := integer("WDBGP_JS_MAX_TOTAL", 67108864)
	if err != nil {
		return Config{}, err
	}
	jsMaxEntries, err := integer("WDBGP_JS_MAX_ENTRIES", 1000000)
	if err != nil {
		return Config{}, err
	}
	jsMaxRequests, err := integer("WDBGP_JS_MAX_REQUESTS", 200)
	if err != nil {
		return Config{}, err
	}
	jsMaxCallStack, err := integer("WDBGP_JS_MAX_CALL_STACK", 1000)
	if err != nil {
		return Config{}, err
	}
	defaultWebAuth, err := validateWebAuthMode("WDBGP_DEFAULT_WEB_AUTH", "network")
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		DBPath:            dbPath,
		Host:              host,
		Port:              port,
		BGPListenPort:     bgpPort,
		LocalASN:          asn,
		RouterID:          routerID,
		LocalAddressV4:    localAddressV4,
		LocalAddressV6:    localAddressV6,
		AdminPassword:     os.Getenv("WDBGP_ADMIN_PASSWORD"),
		SessionSecret:     os.Getenv("WDBGP_SESSION_SECRET"),
		AdminCookieSecure: adminCookieSecure,
		DefaultLanguage:   defaultLanguage,
		TrustProxyHeader:  boolean("WDBGP_TRUST_PROXY_HEADERS"),
		SyncInterval:      time.Duration(syncSeconds) * time.Second,
		SecurityHeaders:   boolean("WDBGP_SECURITY_HEADERS"),
		RateLimitLogin:    rateLimitLogin,
		RateLimitAdmin:    rateLimitAdmin,
		SessionMaxAge:     sessionMaxAge,
		LogLevel:           logLevel,
		LogFormat:          logFormat,
		JSTimeout:          time.Duration(jsTimeout) * time.Second,
		JSMaxSourceBytes:   jsMaxSource,
		JSMaxResponseBytes: jsMaxResponse,
		JSMaxTotalBytes:    jsMaxTotal,
		JSMaxEntries:       jsMaxEntries,
		JSMaxRequests:      jsMaxRequests,
		JSMaxCallStack:     jsMaxCallStack,
		DefaultWebAuth:     defaultWebAuth,
		StatusAllowed:      splitCIDRsEnv("WDBGP_STATUS_ALLOWED"),
		StatusToken:        os.Getenv("WDBGP_STATUS_TOKEN"),
		AdapterBackupDir:   env("WDBGP_ADAPTER_BACKUP_DIR", filepath.Dir(dbPath)+"/backup/adapters"),
		AdapterBackupMax:   validateBackupMax("WDBGP_ADAPTER_BACKUP_MAX", 10),
		RequirePasswordForNonUniqueIP: envBool("WDBGP_REQUIRE_PASSWORD_FOR_NON_UNIQUE_IP", true),
	}
	return cfg, nil
}

func (c Config) ListenAddress() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

func (c Config) ValidateServe() error {
	if c.AdminPassword == "" || c.SessionSecret == "" {
		return fmt.Errorf("WDBGP_ADMIN_PASSWORD and WDBGP_SESSION_SECRET are required")
	}
	// Additional runtime validations that can't be done at load time
	if c.RateLimitLogin < 1 {
		return fmt.Errorf("WDBGP_RATE_LIMIT_LOGIN must be at least 1")
	}
	if c.RateLimitAdmin < 1 {
		return fmt.Errorf("WDBGP_RATE_LIMIT_ADMIN must be at least 1")
	}
	if c.SessionMaxAge < 60 {
		return fmt.Errorf("WDBGP_SESSION_MAX_AGE must be at least 60 seconds")
	}
	if c.SyncInterval <= 0 {
		return fmt.Errorf("WDBGP_SYNC_INTERVAL must be greater than zero")
	}
	return nil
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func integer(name string, fallback int) (int, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	number, err := strconv.ParseInt(value, 10, 0)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	return int(number), nil
}

func integer32(name string, fallback int32) (int32, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	number, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	return int32(number), nil
}

func unsignedInteger32(name string, fallback uint32) (uint32, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	if strings.HasPrefix(strings.TrimSpace(value), "-") {
		return 0, fmt.Errorf("%s must be between 1 and %d", name, uint64(^uint32(0)))
	}
	number, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	return uint32(number), nil
}

func boolean(name string) bool {
	switch strings.ToLower(os.Getenv(name)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func envBool(name string, fallback bool) bool {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	switch strings.ToLower(value) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func validatePort(name string, fallback int) (int, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	number, err := strconv.ParseInt(value, 10, 0)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	if number < 1 || number > 65535 {
		return 0, fmt.Errorf("%s must be between 1 and 65535", name)
	}
	return int(number), nil
}

func validatePort32(name string, fallback int32) (int32, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	number, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	if number < 1 || number > 65535 {
		return 0, fmt.Errorf("%s must be between 1 and 65535", name)
	}
	return int32(number), nil
}

func validateASN(name string, fallback uint32) (uint32, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	if strings.HasPrefix(strings.TrimSpace(value), "-") {
		return 0, fmt.Errorf("%s must be between 1 and %d", name, uint64(^uint32(0)))
	}
	number, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	if number == 0 {
		return 0, fmt.Errorf("%s must be greater than zero", name)
	}
	if number > 65535 {
		return 0, fmt.Errorf("%s must not exceed 65535 (2-byte BGP ASN)", name)
	}
	return uint32(number), nil
}

func validateSyncInterval(name string, fallback int) (int, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	number, err := strconv.ParseInt(value, 10, 0)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	if number <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero seconds", name)
	}
	// Warn about extremely short intervals but don't reject them
	if number < 60 {
		// This is just a warning, not an error
	}
	return int(number), nil
}

func validateRateLimit(name string, fallback int) (int, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	number, err := strconv.ParseInt(value, 10, 0)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	if number < 1 {
		return 0, fmt.Errorf("%s must be at least 1 request per minute", name)
	}
	if number > 1000 {
		return 0, fmt.Errorf("%s must not exceed 1000 requests per minute", name)
	}
	return int(number), nil
}

func validateSessionMaxAge(name string, fallback int) (int, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	number, err := strconv.ParseInt(value, 10, 0)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	if number < 60 {
		return 0, fmt.Errorf("%s must be at least 60 seconds (1 minute)", name)
	}
	if number > 31536000 {
		return 0, fmt.Errorf("%s must not exceed 31536000 seconds (1 year)", name)
	}
	return int(number), nil
}

func validateLogLevel(name string, fallback string) (string, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	validLevels := map[string]bool{
		"DEBUG":   true,
		"INFO":    true,
		"WARN":    true,
		"WARNING": true,
		"ERROR":   true,
		"FATAL":   true,
		"PANIC":   true,
	}
	upperValue := strings.ToUpper(value)
	if !validLevels[upperValue] {
		return "", fmt.Errorf("%s must be one of: DEBUG, INFO, WARN, ERROR, FATAL, PANIC", name)
	}
	return upperValue, nil
}

func validateLogFormat(name string, fallback string) (string, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	lowerValue := strings.ToLower(value)
	if lowerValue != "text" && lowerValue != "json" {
		return "", fmt.Errorf("%s must be either 'text' or 'json'", name)
	}
	return lowerValue, nil
}

func validateHost(name string, fallback string) (string, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	// Check if it's a valid IP address
	if ip := net.ParseIP(value); ip != nil {
		return value, nil
	}
	// Check if it's "localhost"
	if strings.ToLower(value) == "localhost" {
		return value, nil
	}
	// Check for common invalid patterns
	if strings.Contains(value, ":") {
		// Might contain port
		if _, _, err := net.SplitHostPort(value); err == nil {
			return "", fmt.Errorf("%s should not include port number (port is configured separately)", name)
		}
	}
	// Basic hostname validation (without DNS lookup to avoid blocking)
	// Check length and characters
	if len(value) > 255 {
		return "", fmt.Errorf("%s hostname too long (max 255 characters)", name)
	}
	// Check if it looks like a valid hostname
	// Allow underscore which is common in internal networks
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 {
			return "", fmt.Errorf("%s invalid hostname label %q (length must be 1-63)", name, label)
		}
		// Allow letters, digits, hyphen, and underscore
		for _, r := range label {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_') {
				return "", fmt.Errorf("%s invalid character %q in hostname label %q", name, r, label)
			}
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("%s hostname label cannot start or end with hyphen: %q", name, label)
		}
	}
	return value, nil
}

func validateDBPath(name string, fallback string) (string, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	// Check for absolute path
	if !strings.HasPrefix(value, "/") {
		// Relative paths are allowed but warn
		// In production, absolute paths are recommended
	}
	
	// Get parent directory
	dir := path.Dir(value)
	if dir == "." {
		dir = "."
	} else if dir == "/" {
		// Root directory
	} else if dir == "" {
		dir = "."
	}
	
	// Check if parent directory exists and is writable
	if stat, err := os.Stat(dir); err == nil {
		// Directory exists
		if !stat.IsDir() {
			return "", fmt.Errorf("%s: parent path %s is not a directory", name, dir)
		}
		// Check write permission (simplified check)
		if stat.Mode().Perm()&0200 == 0 {
			return "", fmt.Errorf("%s: directory %s is not writable", name, dir)
		}
	} else if os.IsNotExist(err) {
		// Parent directory doesn't exist
		// Try to check grandparent directory
		grandDir := path.Dir(dir)
		if grandDir != dir { // Not at root
			if stat, err := os.Stat(grandDir); err == nil && stat.IsDir() {
				if stat.Mode().Perm()&0200 == 0 {
					return "", fmt.Errorf("%s: cannot create directory %s - parent %s is not writable", name, dir, grandDir)
				}
			} else if os.IsNotExist(err) {
				// Keep going up until we find an existing directory or hit root
				// For now, just return a warning
			}
		}
	} else {
		// Other error (permission denied, etc.)
		return "", fmt.Errorf("%s: cannot access directory %s: %w", name, dir, err)
	}
	
	return value, nil
}

func validateIPv4Address(name string, fallback string) (string, error) {
	value := os.Getenv(name)
	if value == "" {
		// Check fallback value
		if fallback == "" {
			return "", nil
		}
		value = fallback
	}
	ip, err := netip.ParseAddr(value)
	if err != nil || !ip.Is4() {
		return "", fmt.Errorf("%s must be a valid IPv4 address", name)
	}
	return value, nil
}

func validateIPv6Address(name string, fallback string) (string, error) {
	value := os.Getenv(name)
	if value == "" {
		// Check fallback value
		if fallback == "" {
			return "", nil
		}
		value = fallback
	}
	if value == "" {
		return "", nil
	}
	ip, err := netip.ParseAddr(value)
	if err != nil || !ip.Is6() {
		return "", fmt.Errorf("%s must be a valid IPv6 address", name)
	}
	return value, nil
}

func validateAdminCookieSecure(name string, fallback string) (string, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	lowerValue := strings.ToLower(value)
	if lowerValue != "auto" && lowerValue != "true" && lowerValue != "false" {
		return "", fmt.Errorf("%s must be one of: auto, true, false", name)
	}
	return lowerValue, nil
}

func validateDefaultLanguage(name string, fallback string) (string, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	lowerValue := strings.ToLower(value)
	if lowerValue != "en" && lowerValue != "ru" {
		return "", fmt.Errorf("%s must be one of: en, ru", name)
	}
	return lowerValue, nil
}

func splitCIDRsEnv(name string) []string {
	value := os.Getenv(name)
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

func validateWebAuthMode(name string, fallback string) (string, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	switch value {
	case "network", "login", "both", "any":
		return value, nil
	default:
		return "", fmt.Errorf("%s must be network, login, or both", name)
	}
}

func validateBackupMax(name string, fallback int) int {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		return fallback
	}
	return n
}

// ApplyDBOverrides sets config fields from database values for keys not overridden by ENV.
func (c *Config) ApplyDBOverrides(settings map[string]string) {
	if os.Getenv("WDBGP_DEFAULT_LANGUAGE") == "" {
		if v, ok := settings["default_language"]; ok && v != "" {
			c.DefaultLanguage = v
		}
	}
	if os.Getenv("WDBGP_SESSION_MAX_AGE") == "" {
		if v, ok := settings["session_max_age"]; ok && v != "" {
			c.SessionMaxAge, _ = strconv.Atoi(v)
		}
	}
	if os.Getenv("WDBGP_ADMIN_COOKIE_SECURE") == "" {
		if v, ok := settings["admin_cookie_secure"]; ok && v != "" {
			c.AdminCookieSecure = v
		}
	}
	if os.Getenv("WDBGP_TRUST_PROXY_HEADERS") == "" {
		if v, ok := settings["trust_proxy_headers"]; ok && v == "true" {
			c.TrustProxyHeader = true
		}
	}
	if os.Getenv("WDBGP_SECURITY_HEADERS") == "" {
		if v, ok := settings["security_headers"]; ok && v == "true" {
			c.SecurityHeaders = true
		}
	}
	if os.Getenv("WDBGP_DEFAULT_WEB_AUTH") == "" {
		if v, ok := settings["default_web_auth"]; ok && v != "" {
			c.DefaultWebAuth = v
		}
	}
	if os.Getenv("WDBGP_RATE_LIMIT_LOGIN") == "" {
		if v, ok := settings["rate_limit_login"]; ok && v != "" {
			c.RateLimitLogin, _ = strconv.Atoi(v)
		}
	}
	if os.Getenv("WDBGP_RATE_LIMIT_ADMIN") == "" {
		if v, ok := settings["rate_limit_admin"]; ok && v != "" {
			c.RateLimitAdmin, _ = strconv.Atoi(v)
		}
	}
	if os.Getenv("WDBGP_SYNC_INTERVAL") == "" {
		if v, ok := settings["sync_interval"]; ok && v != "" {
			if sec, err := strconv.Atoi(v); err == nil {
				c.SyncInterval = time.Duration(sec) * time.Second
			}
		}
	}
	if os.Getenv("WDBGP_LOG_LEVEL") == "" {
		if v, ok := settings["log_level"]; ok && v != "" {
			c.LogLevel = v
		}
	}
	if os.Getenv("WDBGP_LOG_FORMAT") == "" {
		if v, ok := settings["log_format"]; ok && v != "" {
			c.LogFormat = v
		}
	}
	if os.Getenv("WDBGP_JS_TIMEOUT") == "" {
		if v, ok := settings["js_timeout"]; ok && v != "" {
			if sec, err := strconv.Atoi(v); err == nil {
				c.JSTimeout = time.Duration(sec) * time.Second
			}
		}
	}
	if os.Getenv("WDBGP_JS_MAX_SOURCE") == "" {
		if v, ok := settings["js_max_source"]; ok && v != "" {
			c.JSMaxSourceBytes, _ = strconv.Atoi(v)
		}
	}
	if os.Getenv("WDBGP_JS_MAX_RESPONSE") == "" {
		if v, ok := settings["js_max_response"]; ok && v != "" {
			c.JSMaxResponseBytes, _ = strconv.Atoi(v)
		}
	}
	if os.Getenv("WDBGP_JS_MAX_TOTAL") == "" {
		if v, ok := settings["js_max_total"]; ok && v != "" {
			c.JSMaxTotalBytes, _ = strconv.Atoi(v)
		}
	}
	if os.Getenv("WDBGP_JS_MAX_ENTRIES") == "" {
		if v, ok := settings["js_max_entries"]; ok && v != "" {
			c.JSMaxEntries, _ = strconv.Atoi(v)
		}
	}
	if os.Getenv("WDBGP_JS_MAX_REQUESTS") == "" {
		if v, ok := settings["js_max_requests"]; ok && v != "" {
			c.JSMaxRequests, _ = strconv.Atoi(v)
		}
	}
	if os.Getenv("WDBGP_JS_MAX_CALL_STACK") == "" {
		if v, ok := settings["js_max_call_stack"]; ok && v != "" {
			c.JSMaxCallStack, _ = strconv.Atoi(v)
		}
	}
	if os.Getenv("WDBGP_ADAPTER_BACKUP_DIR") == "" {
		if v, ok := settings["adapter_backup_dir"]; ok && v != "" {
			c.AdapterBackupDir = v
		}
	}
	if os.Getenv("WDBGP_ADAPTER_BACKUP_MAX") == "" {
		if v, ok := settings["adapter_backup_max"]; ok && v != "" {
			c.AdapterBackupMax, _ = strconv.Atoi(v)
		}
	}
}
