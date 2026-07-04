package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/andrey-vk/wdbgp/internal/store"
)

// TestAdaptersDeleteNotFound guards against apiAdaptersDelete's initial
// s.store.FeedAdapter lookup surfacing sql.ErrNoRows as a raw 500 instead
// of the 404 apiAdaptersGet/apiAdaptersUpdate already return for the same
// nonexistent-ID case.
func TestAdaptersDeleteNotFound(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)

	req := httptest.NewRequest("DELETE", "/api/admin/adapters/99999", nil)
	req.SetPathValue("id", "99999")
	w := httptest.NewRecorder()
	srv.apiAdaptersDelete(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", w.Code, w.Body.String())
	}
}

// TestPruneAdapterBackupsSweepsLegacyKeyPrefixedFiles guards against backups
// written before feed_adapters.key was dropped (named {key}_r{rev}_{ts}.js)
// silently escaping AdapterBackupMax forever once new backups switched to
// {id}_r{rev}_{ts}.js — both naming schemes for the same adapter must be
// swept into the same rotation, oldest-first by actual timestamp (not by
// prefix, which would sort digits before letters and delete newest-first).
func TestPruneAdapterBackupsSweepsLegacyKeyPrefixedFiles(t *testing.T) {
	dir := t.TempDir()
	// Legacy key-prefixed files (oldest two).
	legacy := []string{
		"my-custom_r1_20260101T000000Z.js",
		"my-custom_r2_20260102T000000Z.js",
	}
	// New id-prefixed files (newest two).
	current := []string{
		"5_r3_20260103T000000Z.js",
		"5_r4_20260104T000000Z.js",
	}
	for _, f := range append(append([]string{}, legacy...), current...) {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	pruneAdapterBackups(store.FeedAdapter{ID: 5, Name: "My Custom"}, dir, 2)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Fatalf("remaining files = %v, want exactly the 2 newest", names)
	}
	remaining := map[string]bool{}
	for _, e := range entries {
		remaining[e.Name()] = true
	}
	for _, f := range current {
		if !remaining[f] {
			t.Errorf("expected newest file %q to survive pruning", f)
		}
	}
	for _, f := range legacy {
		if remaining[f] {
			t.Errorf("expected legacy file %q to be pruned as the oldest, not kept", f)
		}
	}
}
