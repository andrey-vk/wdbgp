package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

// TestRunRejectsBadDBPathBeforeOpeningStore guards against WDBGP_DB pointing
// at an unusable parent path (here: a path component that's a regular file,
// not a directory) reaching store.Open at all. Without the early check,
// run() still eventually fails — but from inside store.Open's os.MkdirAll,
// with a raw "mkdir ...: not a directory" error instead of a clear one
// naming WDBGP_DB. Asserting on the message (not just err != nil) is what
// actually distinguishes "caught early with a clear message" from
// "store.Open was attempted and failed on its own".
func TestRunRejectsBadDBPathBeforeOpeningStore(t *testing.T) {
	dir := t.TempDir()
	notADir := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WDBGP_DB", filepath.Join(notADir, "wdbgp.sqlite3"))

	origArgs := os.Args
	os.Args = []string{"wdbgp", "migrate"}
	defer func() { os.Args = origArgs }()

	err := run()
	if err == nil {
		t.Fatal("expected an error for a WDBGP_DB whose parent path is a file, not a directory")
	}
	if !strings.Contains(err.Error(), "WDBGP_DB") {
		t.Errorf("error = %q, want it to name WDBGP_DB (i.e. caught by the early check, not store.Open's own mkdir failure)", err.Error())
	}
}
