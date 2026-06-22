package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadRejectsOutOfRangeASN(t *testing.T) {
	t.Setenv("WDBGP_LOCAL_ASN", "-1")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "between 1") {
		t.Fatalf("Load() error = %v, want ASN range error", err)
	}

	t.Setenv("WDBGP_LOCAL_ASN", "4294967296")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "integer") {
		t.Fatalf("Load() error = %v, want ASN integer range error", err)
	}

	t.Setenv("WDBGP_LOCAL_ASN", "0")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "greater than zero") {
		t.Fatalf("Load() error = %v, want ASN zero error", err)
	}
}

func TestLoadAdminCookieSecureDefaultAndOverride(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AdminCookieSecure != "auto" {
		t.Fatalf("AdminCookieSecure = %q, want auto", cfg.AdminCookieSecure)
	}

	t.Setenv("WDBGP_ADMIN_COOKIE_SECURE", "false")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AdminCookieSecure != "false" {
		t.Fatalf("AdminCookieSecure = %q, want false", cfg.AdminCookieSecure)
	}
}

func TestLoadDefaultLanguage(t *testing.T) {
	t.Setenv("WDBGP_DEFAULT_LANGUAGE", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultLanguage != "en" {
		t.Fatalf("DefaultLanguage = %q, want en", cfg.DefaultLanguage)
	}

	t.Setenv("WDBGP_DEFAULT_LANGUAGE", "RU")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultLanguage != "ru" {
		t.Fatalf("DefaultLanguage = %q, want ru", cfg.DefaultLanguage)
	}
}

func TestValidateServeRejectsInvalidNetworkSettings(t *testing.T) {
	valid := Config{
		AdminPassword: "admin", SessionSecret: "secret",
		Port: 8080, BGPListenPort: 179, LocalASN: 64512,
		RouterID: "192.0.2.1", LocalAddressV4: "192.0.2.2",
		SyncInterval:   time.Hour,
		RateLimitLogin: 5,
		RateLimitAdmin: 30,
		SessionMaxAge:  28800,
	}
	tests := []struct {
		name      string
		change    func(*Config)
		wantError bool
	}{
		// These validations now happen in Load(), not ValidateServe()
		{"HTTP port", func(cfg *Config) { cfg.Port = 70000 }, false},
		{"BGP port", func(cfg *Config) { cfg.BGPListenPort = 0 }, false},
		{"router ID", func(cfg *Config) { cfg.RouterID = "2001:db8::1" }, false},
		{"local IPv4", func(cfg *Config) { cfg.LocalAddressV4 = "invalid" }, false},
		{"local IPv6", func(cfg *Config) { cfg.LocalAddressV6 = "192.0.2.3" }, false},
		{"admin cookie secure", func(cfg *Config) { cfg.AdminCookieSecure = "sometimes" }, false},
		{"default language", func(cfg *Config) { cfg.DefaultLanguage = "de" }, false},
		// These still validated in ValidateServe()
		{"rate limit login too low", func(cfg *Config) { cfg.RateLimitLogin = 0 }, true},
		{"rate limit admin too low", func(cfg *Config) { cfg.RateLimitAdmin = 0 }, true},
		{"session max age too low", func(cfg *Config) { cfg.SessionMaxAge = 30 }, true},
		{"sync interval zero", func(cfg *Config) { cfg.SyncInterval = 0 }, true},
		{"missing admin password", func(cfg *Config) { cfg.AdminPassword = "" }, true},
		{"missing session secret", func(cfg *Config) { cfg.SessionSecret = "" }, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := valid
			test.change(&cfg)
			err := cfg.ValidateServe()
			if test.wantError && err == nil {
				t.Fatal("ValidateServe() should have returned error")
			}
			if !test.wantError && err != nil {
				t.Fatalf("ValidateServe() unexpected error: %v", err)
			}
		})
	}
}

func TestLoadValidatesRateLimits(t *testing.T) {
	tests := []struct {
		name      string
		envVar    string
		value     string
		wantError bool
		errorMsg  string
	}{
		{"valid login rate limit", "WDBGP_RATE_LIMIT_LOGIN", "10", false, ""},
		{"login rate limit too low", "WDBGP_RATE_LIMIT_LOGIN", "0", true, "at least 1"},
		{"login rate limit negative", "WDBGP_RATE_LIMIT_LOGIN", "-5", true, "at least 1"},
		{"login rate limit too high", "WDBGP_RATE_LIMIT_LOGIN", "2000", true, "not exceed 1000"},
		{"valid admin rate limit", "WDBGP_RATE_LIMIT_ADMIN", "50", false, ""},
		{"admin rate limit invalid", "WDBGP_RATE_LIMIT_ADMIN", "abc", true, "integer"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(test.envVar, test.value)
			_, err := Load()
			if test.wantError && err == nil {
				t.Fatal("Load() should have returned error")
			}
			if !test.wantError && err != nil {
				t.Fatalf("Load() unexpected error: %v", err)
			}
			if test.wantError && test.errorMsg != "" && !strings.Contains(err.Error(), test.errorMsg) {
				t.Fatalf("Load() error = %v, want containing %q", err, test.errorMsg)
			}
		})
	}
}

func TestLoadValidatesSessionMaxAge(t *testing.T) {
	tests := []struct {
		name      string
		value     string
		wantError bool
		errorMsg  string
	}{
		{"valid session age", "3600", false, ""},
		{"session age too low", "30", true, "at least 60 seconds"},
		{"session age zero", "0", true, "at least 60 seconds"},
		{"session age negative", "-100", true, "at least 60 seconds"}, // Changed from "integer"
		{"session age too high", "40000000", true, "not exceed 31536000"},
		{"session age invalid", "abc", true, "integer"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("WDBGP_SESSION_MAX_AGE", test.value)
			_, err := Load()
			if test.wantError && err == nil {
				t.Fatal("Load() should have returned error")
			}
			if !test.wantError && err != nil {
				t.Fatalf("Load() unexpected error: %v", err)
			}
			if test.wantError && test.errorMsg != "" && !strings.Contains(err.Error(), test.errorMsg) {
				t.Fatalf("Load() error = %v, want containing %q", err, test.errorMsg)
			}
		})
	}
}

func TestLoadValidatesLogLevelAndFormat(t *testing.T) {
	tests := []struct {
		name      string
		envVar    string
		value     string
		wantError bool
		errorMsg  string
	}{
		{"valid log level INFO", "WDBGP_LOG_LEVEL", "INFO", false, ""},
		{"valid log level debug lowercase", "WDBGP_LOG_LEVEL", "debug", false, ""},
		{"valid log level ERROR", "WDBGP_LOG_LEVEL", "ERROR", false, ""},
		{"invalid log level", "WDBGP_LOG_LEVEL", "VERBOSE", true, "one of: DEBUG, INFO, WARN, ERROR, FATAL, PANIC"},
		{"valid log format text", "WDBGP_LOG_FORMAT", "text", false, ""},
		{"valid log format json", "WDBGP_LOG_FORMAT", "json", false, ""},
		{"valid log format JSON uppercase", "WDBGP_LOG_FORMAT", "JSON", false, ""},
		{"invalid log format", "WDBGP_LOG_FORMAT", "yaml", true, "either 'text' or 'json'"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(test.envVar, test.value)
			_, err := Load()
			if test.wantError && err == nil {
				t.Fatal("Load() should have returned error")
			}
			if !test.wantError && err != nil {
				t.Fatalf("Load() unexpected error: %v", err)
			}
			if test.wantError && test.errorMsg != "" && !strings.Contains(err.Error(), test.errorMsg) {
				t.Fatalf("Load() error = %v, want containing %q", err, test.errorMsg)
			}
		})
	}
}

func TestLoadValidatesSyncInterval(t *testing.T) {
	tests := []struct {
		name      string
		value     string
		wantError bool
		errorMsg  string
	}{
		{"valid sync interval", "3600", false, ""},
		{"sync interval zero", "0", true, "greater than zero"},
		{"sync interval negative", "-100", true, "greater than zero"},
		{"sync interval invalid", "abc", true, "integer"},
		{"very short sync interval (warning only)", "10", false, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("WDBGP_SYNC_INTERVAL", test.value)
			_, err := Load()
			if test.wantError && err == nil {
				t.Fatal("Load() should have returned error")
			}
			if !test.wantError && err != nil {
				t.Fatalf("Load() unexpected error: %v", err)
			}
			if test.wantError && test.errorMsg != "" && !strings.Contains(err.Error(), test.errorMsg) {
				t.Fatalf("Load() error = %v, want containing %q", err, test.errorMsg)
			}
		})
	}
}

func TestLoadValidatesHost(t *testing.T) {
	tests := []struct {
		name      string
		value     string
		wantError bool
		errorMsg  string
	}{
		{"valid IPv4 address", "0.0.0.0", false, ""},
		{"valid IPv6 address", "::1", false, ""},
		{"valid localhost", "localhost", false, ""},
		{"host with port", "localhost:8080", true, "should not include port number"},
		{"invalid host hyphen start", "-invalid", true, "cannot start or end with hyphen"},
		{"invalid host hyphen end", "invalid-", true, "cannot start or end with hyphen"},
		{"invalid host too long", strings.Repeat("a", 256), true, "too long"},
		{"valid domain", "example.com", false, ""},
		{"valid subdomain", "sub.example.com", false, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("WDBGP_HOST", test.value)
			_, err := Load()
			if test.wantError && err == nil {
				t.Fatal("Load() should have returned error")
			}
			if !test.wantError && err != nil {
				t.Fatalf("Load() unexpected error: %v", err)
			}
			if test.wantError && test.errorMsg != "" && !strings.Contains(err.Error(), test.errorMsg) {
				t.Fatalf("Load() error = %v, want containing %q", err, test.errorMsg)
			}
		})
	}
}

func TestLoadValidatesPorts(t *testing.T) {
	tests := []struct {
		name      string
		envVar    string
		value     string
		wantError bool
		errorMsg  string
	}{
		{"valid port", "WDBGP_PORT", "8080", false, ""},
		{"port zero", "WDBGP_PORT", "0", true, "between 1 and 65535"},
		{"port negative", "WDBGP_PORT", "-1", true, "between 1 and 65535"},
		{"port too high", "WDBGP_PORT", "70000", true, "between 1 and 65535"},
		{"valid BGP port", "WDBGP_BGP_PORT", "179", false, ""},
		{"BGP port invalid", "WDBGP_BGP_PORT", "abc", true, "integer"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(test.envVar, test.value)
			_, err := Load()
			if test.wantError && err == nil {
				t.Fatal("Load() should have returned error")
			}
			if !test.wantError && err != nil {
				t.Fatalf("Load() unexpected error: %v", err)
			}
			if test.wantError && test.errorMsg != "" && !strings.Contains(err.Error(), test.errorMsg) {
				t.Fatalf("Load() error = %v, want containing %q", err, test.errorMsg)
			}
		})
	}
}

func TestLoadValidatesIPAddresses(t *testing.T) {
	tests := []struct {
		name      string
		envVar    string
		value     string
		wantError bool
		errorMsg  string
	}{
		{"valid IPv4 router ID", "WDBGP_ROUTER_ID", "192.168.1.1", false, ""},
		{"invalid router ID", "WDBGP_ROUTER_ID", "not-an-ip", true, "IPv4 address"},
		{"IPv6 router ID", "WDBGP_ROUTER_ID", "2001:db8::1", true, "IPv4 address"},
		{"valid IPv4 local address", "WDBGP_BGP_LOCAL_ADDRESS", "10.0.0.1", false, ""},
		{"valid IPv6 local address", "WDBGP_BGP_LOCAL_ADDRESS_V6", "2001:db8::2", false, ""},
		{"invalid IPv6 local address", "WDBGP_BGP_LOCAL_ADDRESS_V6", "192.168.1.1", true, "IPv6 address"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(test.envVar, test.value)
			_, err := Load()
			if test.wantError && err == nil {
				t.Fatal("Load() should have returned error")
			}
			if !test.wantError && err != nil {
				t.Fatalf("Load() unexpected error: %v", err)
			}
			if test.wantError && test.errorMsg != "" && !strings.Contains(err.Error(), test.errorMsg) {
				t.Fatalf("Load() error = %v, want containing %q", err, test.errorMsg)
			}
		})
	}
}

func TestLoadValidatesAdminCookieSecureAndDefaultLanguage(t *testing.T) {
	tests := []struct {
		name      string
		envVar    string
		value     string
		wantError bool
		errorMsg  string
	}{
		{"valid admin cookie secure auto", "WDBGP_ADMIN_COOKIE_SECURE", "auto", false, ""},
		{"valid admin cookie secure true", "WDBGP_ADMIN_COOKIE_SECURE", "true", false, ""},
		{"valid admin cookie secure false", "WDBGP_ADMIN_COOKIE_SECURE", "false", false, ""},
		{"invalid admin cookie secure", "WDBGP_ADMIN_COOKIE_SECURE", "maybe", true, "one of: auto, true, false"},
		{"valid default language en", "WDBGP_DEFAULT_LANGUAGE", "en", false, ""},
		{"valid default language ru", "WDBGP_DEFAULT_LANGUAGE", "ru", false, ""},
		{"invalid default language", "WDBGP_DEFAULT_LANGUAGE", "de", true, "one of: en, ru"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(test.envVar, test.value)
			_, err := Load()
			if test.wantError && err == nil {
				t.Fatal("Load() should have returned error")
			}
			if !test.wantError && err != nil {
				t.Fatalf("Load() unexpected error: %v", err)
			}
			if test.wantError && test.errorMsg != "" && !strings.Contains(err.Error(), test.errorMsg) {
				t.Fatalf("Load() error = %v, want containing %q", err, test.errorMsg)
			}
		})
	}
}

func TestAllowDynamicPeersEnvDefaultFalse(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AllowDynamicPeers {
		t.Fatalf("AllowDynamicPeers = true, want false (default)")
	}
}

func TestAllowDynamicPeersEnvTrue(t *testing.T) {
	t.Setenv("WDBGP_ALLOW_DYNAMIC_PEERS", "true")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.AllowDynamicPeers {
		t.Fatalf("AllowDynamicPeers = false, want true (env set to \"true\")")
	}
}
