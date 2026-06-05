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
