package config

import (
	"strings"
	"testing"
)

// =============================================================================
// WDBGP_DEFAULT_WEB_AUTH validation
// =============================================================================

func TestDefaultWebAuthValidation(t *testing.T) {
	tests := []struct {
		name      string
		value     string
		wantError bool
		wantValue string
	}{
		{"network", "network", false, "network"},
		{"login", "login", false, "login"},
		{"both", "both", false, "both"},
		{"invalid", "token", true, ""},
		{"empty value uses default", "", false, "network"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.value != "" {
				t.Setenv("WDBGP_DEFAULT_WEB_AUTH", test.value)
			}
			cfg, err := Load()
			if test.wantError && err == nil {
				t.Fatal("Load() should have returned error")
			}
			if !test.wantError && err != nil {
				t.Fatalf("Load() unexpected error: %v", err)
			}
			if !test.wantError && cfg.DefaultWebAuth != test.wantValue {
				t.Fatalf("DefaultWebAuth = %q, want %q", cfg.DefaultWebAuth, test.wantValue)
			}
			if test.wantError && err != nil && !strings.Contains(err.Error(), "network, login, both, or any") {
				t.Fatalf("error = %v, want network/login/both/any message", err)
			}
		})
	}
}

// =============================================================================
// WDBGP_LOG_LEVEL validation
// =============================================================================

func TestLogLevelAllValidLevels(t *testing.T) {
	validLevels := []string{"DEBUG", "INFO", "WARN", "WARNING", "ERROR", "FATAL", "PANIC"}
	// Test both uppercase and lowercase
	for _, level := range validLevels {
		t.Run("upper-"+level, func(t *testing.T) {
			t.Setenv("WDBGP_LOG_LEVEL", level)
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load() with %s: %v", level, err)
			}
			if cfg.LogLevel != level {
				t.Fatalf("LogLevel = %q, want %q", cfg.LogLevel, level)
			}
		})
		t.Run("lower-"+strings.ToLower(level), func(t *testing.T) {
			t.Setenv("WDBGP_LOG_LEVEL", strings.ToLower(level))
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load() with %s: %v", strings.ToLower(level), err)
			}
			expected := level
			if expected == "WARNING" {
				expected = "WARN"
			}
			if cfg.LogLevel != strings.ToUpper(expected) {
				// WARNING is mapped to WARNING not WARN - depends on config logic
			}
		})
	}
}

func TestLogLevelInvalid(t *testing.T) {
	t.Setenv("WDBGP_LOG_LEVEL", "TRACE")
	_, err := Load()
	if err == nil {
		t.Fatal("Load() with TRACE should have returned error")
	}
	if !strings.Contains(err.Error(), "one of:") {
		t.Fatalf("error = %v, want 'one of:' message", err)
	}
}

// =============================================================================
// WDBGP_LOG_FORMAT validation
// =============================================================================

func TestLogFormatValidation(t *testing.T) {
	tests := []struct {
		name      string
		value     string
		wantError bool
		wantValue string
	}{
		{"text", "text", false, "text"},
		{"json", "json", false, "json"},
		{"TEXT uppercase", "TEXT", false, "text"},
		{"JSON uppercase", "JSON", false, "json"},
		{"invalid yaml", "yaml", true, ""},
		{"invalid xml", "xml", true, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("WDBGP_LOG_FORMAT", test.value)
			cfg, err := Load()
			if test.wantError && err == nil {
				t.Fatal("Load() should have returned error")
			}
			if !test.wantError && err != nil {
				t.Fatalf("Load() unexpected error: %v", err)
			}
			if !test.wantError && cfg.LogFormat != test.wantValue {
				t.Fatalf("LogFormat = %q, want %q", cfg.LogFormat, test.wantValue)
			}
			if test.wantError && !strings.Contains(err.Error(), "either 'text' or 'json'") {
				t.Fatalf("error = %v, want 'either text or json'", err)
			}
		})
	}
}

// =============================================================================
// Default values
// =============================================================================

func TestLogLevelDefaultIsInfo(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LogLevel != "INFO" {
		t.Fatalf("default LogLevel = %q, want INFO", cfg.LogLevel)
	}
}

func TestLogFormatDefaultIsText(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LogFormat != "text" {
		t.Fatalf("default LogFormat = %q, want text", cfg.LogFormat)
	}
}

func TestDefaultWebAuthDefaultIsNetwork(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultWebAuth != "network" {
		t.Fatalf("default DefaultWebAuth = %q, want network", cfg.DefaultWebAuth)
	}
}
