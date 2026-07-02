package main

import "testing"

// TestEnvBoolIsCaseInsensitive guards against a regression from the
// internal/config -> internal/settings rewrite: the previous parser
// lowercased the env value before matching, so deployments setting
// WDBGP_BACKUP_ENABLED=False or WDBGP_AUTO_RESTORE_ENABLED=True (common
// boolean spellings) would silently fall back to the default instead of
// being parsed.
func TestEnvBoolIsCaseInsensitive(t *testing.T) {
	cases := []struct {
		value    string
		fallback bool
		want     bool
	}{
		{"True", false, true},
		{"FALSE", true, false},
		{"YES", false, true},
		{"No", true, false},
		{"On", false, true},
		{"OFF", true, false},
		{"garbage", true, true},   // unrecognized -> fallback
		{"garbage", false, false}, // unrecognized -> fallback
	}
	for _, c := range cases {
		t.Setenv("TEST_ENV_BOOL", c.value)
		if got := envBool("TEST_ENV_BOOL", c.fallback); got != c.want {
			t.Errorf("envBool(%q, %v) = %v, want %v", c.value, c.fallback, got, c.want)
		}
	}
}
