package web

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// =============================================================================
// TestDebugCIDRRequiresParam — GET /api/admin/debug?mode=1 (no cidr) → 400
// =============================================================================

func TestDebugCIDRRequiresParam(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)

	req := httptest.NewRequest("GET", "/api/admin/debug?mode=1", nil)
	w := httptest.NewRecorder()
	srv.apiDebugCIDR(w, req)

	if w.Code != 400 {
		t.Fatalf("status = %d, want 400, body=%s", w.Code, w.Body.String())
	}
}

// =============================================================================
// TestDebugCIDRInvalid — GET /api/admin/debug?cidr=invalid&mode=1 → 400
// =============================================================================

func TestDebugCIDRInvalid(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)

	req := httptest.NewRequest("GET", "/api/admin/debug?cidr=invalid&mode=1", nil)
	w := httptest.NewRecorder()
	srv.apiDebugCIDR(w, req)

	if w.Code != 400 {
		t.Fatalf("status = %d, want 400, body=%s", w.Code, w.Body.String())
	}
}

// =============================================================================
// TestDebugCIDRValid — insert a catalog entry, GET /api/admin/debug?cidr=10.0.0.0/8&mode=1 → 200,
// verify response has query field
// =============================================================================

func TestDebugCIDRValid(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)
	ctx := context.Background()

	// Create a feed with adapter
	if _, err := st.DB.ExecContext(ctx, "INSERT OR IGNORE INTO feed_adapters(id, name, language, api_version, source, revision) VALUES (1, 'Test', 'javascript', 1, 'function sync(feed, api) { return []; }', 1)"); err != nil {
		t.Fatalf("setup adapter: %v", err)
	}

	feedID, err := st.AddFeed(ctx, "debug-feed", "http://example.com/debug.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatalf("AddFeed: %v", err)
	}

	// Insert a catalog entry matching 10.0.0.0/8
	if _, err := st.DB.ExecContext(ctx, "INSERT INTO catalog_entries(feed_id, category, service, cidr) VALUES (?, 'Test', 'Debug', '10.0.0.0/8')", feedID); err != nil {
		t.Fatalf("setup catalog entry: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/admin/debug?cidr=10.0.0.0/8&mode=1", nil)
	w := httptest.NewRecorder()
	srv.apiDebugCIDR(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Query == "" {
		t.Fatal("response should have query field")
	}
}
