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
		SyncInterval: time.Hour,
	}
	tests := []struct {
		name   string
		change func(*Config)
	}{
		{"HTTP port", func(cfg *Config) { cfg.Port = 70000 }},
		{"BGP port", func(cfg *Config) { cfg.BGPListenPort = 0 }},
		{"router ID", func(cfg *Config) { cfg.RouterID = "2001:db8::1" }},
		{"local IPv4", func(cfg *Config) { cfg.LocalAddressV4 = "invalid" }},
		{"local IPv6", func(cfg *Config) { cfg.LocalAddressV6 = "192.0.2.3" }},
		{"admin cookie secure", func(cfg *Config) { cfg.AdminCookieSecure = "sometimes" }},
		{"default language", func(cfg *Config) { cfg.DefaultLanguage = "de" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := valid
			test.change(&cfg)
			if err := cfg.ValidateServe(); err == nil {
				t.Fatal("ValidateServe() accepted invalid configuration")
			}
		})
	}
}
