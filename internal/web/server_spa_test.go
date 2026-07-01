package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrey-vk/wdbgp/internal/store"
)

// =============================================================================
// Test helpers
// =============================================================================

// newTestServer creates an httptest.Server with a fully initialized Server
// suitable for SPA serving tests. Returns the server and the underlying store.
func newTestServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	s := testSettings()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"), false, dir, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	server := New(s, db, nil, nil)
	return httptest.NewServer(server.Handler()), db
}

func mustGet(t *testing.T, url string) *http.Response {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// =============================================================================
// SPA serving tests
// =============================================================================

func TestSPAAdminServesHTML(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()

	resp := mustGet(t, ts.URL+"/admin")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "<!DOCTYPE html>") && !strings.Contains(string(body), "<!doctype html>") {
		t.Error("admin.html should be HTML")
	}
}

func TestSPARootServesUserHTML(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()

	resp := mustGet(t, ts.URL+"/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "<!DOCTYPE html>") && !strings.Contains(string(body), "<!doctype html>") {
		t.Error("user.html should be HTML")
	}
}

func TestSPAAssetsServed(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()

	// Get admin.html and extract an asset path from it
	resp := mustGet(t, ts.URL+"/admin")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	// Find an asset src in the HTML: src="/assets/..." or href="/assets/..."
	content := string(body)
	idx := strings.Index(content, "/assets/")
	if idx == -1 {
		t.Skip("no /assets/ reference found in admin.html (build may differ)")
		return
	}
	// Extract the asset path (everything after /assets/ up to the next quote)
	start := idx + len("/assets/")
	end := start
	for end < len(content) && content[end] != '"' && content[end] != '\'' && content[end] != ' ' && content[end] != '>' {
		end++
	}
	assetName := content[start:end]

	// Fetch the asset
	assetURL := ts.URL + "/assets/" + assetName
	resp2 := mustGet(t, assetURL)
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("GET %s = %d, want 200", assetURL, resp2.StatusCode)
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if len(body2) == 0 {
		t.Error("asset body is empty")
	}
}

func TestSPANotFoundFallback(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()

	// Any unknown path should serve user.html (SPA fallback)
	resp := mustGet(t, ts.URL+"/some/nonexistent/path")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "<!DOCTYPE html>") && !strings.Contains(string(body), "<!doctype html>") {
		t.Error("SPA fallback should serve user.html")
	}
}
