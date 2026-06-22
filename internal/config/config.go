package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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
	AllowDynamicPeers             bool     // allow dynamic peers (0.0.0.0/0) via WDBGP_ALLOW_DYNAMIC_PEERS
	ActiveDial                    bool     // WDBGP_ACTIVE_DIAL — system-wide default for active BGP dialing (default true)
	BackupEnabled      bool   // WDBGP_BACKUP_ENABLED (default true)
	BackupDir          string // WDBGP_BACKUP_DIR (default: same dir as DB)
	AutoRestoreEnabled bool   // WDBGP_AUTO_RESTORE_ENABLED (default false)
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
		AllowDynamicPeers:             envBool("WDBGP_ALLOW_DYNAMIC_PEERS", false),
		ActiveDial:                    envBool("WDBGP_ACTIVE_DIAL", true),
		BackupEnabled:      envBool("WDBGP_BACKUP_ENABLED", true),
		BackupDir:          env("WDBGP_BACKUP_DIR", filepath.Dir(dbPath)),
		AutoRestoreEnabled: envBool("WDBGP_AUTO_RESTORE_ENABLED", false),
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

// ApplyDBOverrides sets config fields from database values for keys not overridden by ENV.
func (c *Config) ApplyDBOverrides(settings map[string]string) {
	if os.Getenv("WDBGP_DEFAULT_LANGUAGE") == "" {
		if v, ok := settings["default_language"]; ok && v != "" {
			c.DefaultLanguage = v
		}
	}
	if os.Getenv("WDBGP_SESSION_MAX_AGE") == "" {
		if v, ok := settings["session_max_age"]; ok && v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				c.SessionMaxAge = n
			}
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
			if n, err := strconv.Atoi(v); err == nil {
				c.RateLimitLogin = n
			}
		}
	}
	if os.Getenv("WDBGP_RATE_LIMIT_ADMIN") == "" {
		if v, ok := settings["rate_limit_admin"]; ok && v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				c.RateLimitAdmin = n
			}
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
			if n, err := strconv.Atoi(v); err == nil {
				c.JSMaxSourceBytes = n
			}
		}
	}
	if os.Getenv("WDBGP_JS_MAX_RESPONSE") == "" {
		if v, ok := settings["js_max_response"]; ok && v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				c.JSMaxResponseBytes = n
			}
		}
	}
	if os.Getenv("WDBGP_JS_MAX_TOTAL") == "" {
		if v, ok := settings["js_max_total"]; ok && v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				c.JSMaxTotalBytes = n
			}
		}
	}
	if os.Getenv("WDBGP_JS_MAX_ENTRIES") == "" {
		if v, ok := settings["js_max_entries"]; ok && v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				c.JSMaxEntries = n
			}
		}
	}
	if os.Getenv("WDBGP_JS_MAX_REQUESTS") == "" {
		if v, ok := settings["js_max_requests"]; ok && v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				c.JSMaxRequests = n
			}
		}
	}
	if os.Getenv("WDBGP_JS_MAX_CALL_STACK") == "" {
		if v, ok := settings["js_max_call_stack"]; ok && v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				c.JSMaxCallStack = n
			}
		}
	}
	if os.Getenv("WDBGP_ADAPTER_BACKUP_DIR") == "" {
		if v, ok := settings["adapter_backup_dir"]; ok && v != "" {
			c.AdapterBackupDir = v
		}
	}
	if os.Getenv("WDBGP_ADAPTER_BACKUP_MAX") == "" {
		if v, ok := settings["adapter_backup_max"]; ok && v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				c.AdapterBackupMax = n
			}
		}
	}
}
