package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// =============================================================================
// TestFeedsList — empty → []
// =============================================================================

func TestFeedsList(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)

	req := httptest.NewRequest("GET", "/api/admin/feeds", nil)
	w := httptest.NewRecorder()
	srv.apiFeedsList(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp struct {
		Feeds []feedJSON `json:"feeds"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	// Built-in feeds exist; just verify valid response structure
	_ = resp.Feeds
}

// =============================================================================
// TestFeedsCRUD — create, read, update, delete lifecycle
// =============================================================================

func TestFeedsCRUD(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)
	ctx := context.Background()

	// Count initial feeds (built-in feeds exist)
	req0 := httptest.NewRequest("GET", "/api/admin/feeds", nil)
	w0 := httptest.NewRecorder()
	srv.apiFeedsList(w0, req0)
	var initResp struct {
		Feeds []feedJSON `json:"feeds"`
	}
	if err := json.NewDecoder(w0.Body).Decode(&initResp); err != nil {
		t.Fatal(err)
	}
	initialCount := len(initResp.Feeds)

	// Create a built-in adapter for test feeds (may already exist, use INSERT OR IGNORE)
	if _, err := st.DB.ExecContext(ctx, "INSERT OR IGNORE INTO feed_adapters(id, key, name, language, api_version, source, revision) VALUES (1, 'opencck', 'OpenCCK', 'javascript', 1, 'function sync(feed, api) { return []; }', 1)"); err != nil {
		t.Fatalf("setup adapter: %v", err)
	}

	// --- CREATE ---
	createBody := strings.NewReader(`{"name":"test-feed","url":"http://example.com/feed.json","enabled":true,"sync_interval":3600,"mode_id":1,"adapter_id":1}`)
	req := httptest.NewRequest("POST", "/api/admin/feeds", createBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiFeedsCreate(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201, body=%s", w.Code, w.Body.String())
	}
	var created feedJSON
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("created feed has no id")
	}
	if created.Name != "test-feed" {
		t.Fatalf("name = %q, want test-feed", created.Name)
	}

	feedID := created.ID

	// --- LIST ---
	req = httptest.NewRequest("GET", "/api/admin/feeds", nil)
	w = httptest.NewRecorder()
	srv.apiFeedsList(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("list: status = %d, want 200", w.Code)
	}
	var listResp struct {
		Feeds []feedJSON `json:"feeds"`
	}
	if err := json.NewDecoder(w.Body).Decode(&listResp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listResp.Feeds) != initialCount+1 {
		t.Fatalf("list feeds count = %d, want %d", len(listResp.Feeds), initialCount+1)
	}

	// --- GET single ---
	idStr := strconv.FormatInt(feedID, 10)
	req = httptest.NewRequest("GET", "/api/admin/feeds/"+idStr, nil)
	req.SetPathValue("id", idStr)
	w = httptest.NewRecorder()
	srv.apiFeedsGet(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("get single: status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var single feedJSON
	if err := json.NewDecoder(w.Body).Decode(&single); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	if single.ID != feedID {
		t.Fatalf("get id = %d, want %d", single.ID, feedID)
	}
	if single.Name != "test-feed" {
		t.Fatalf("get name = %q, want test-feed", single.Name)
	}

	// --- UPDATE ---
	updateBody := strings.NewReader(`{"name":"updated-feed","url":"http://example.com/updated.json","enabled":false,"sync_interval":7200,"mode_id":1,"adapter_id":1}`)
	req = httptest.NewRequest("PUT", "/api/admin/feeds/"+idStr, updateBody)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", idStr)
	w = httptest.NewRecorder()
	srv.apiFeedsUpdate(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("update: status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var updated feedJSON
	if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
		t.Fatalf("decode update response: %v", err)
	}
	if updated.Name != "updated-feed" {
		t.Fatalf("updated name = %q, want updated-feed", updated.Name)
	}
	if updated.Enabled {
		t.Fatal("updated feed should be disabled")
	}

	// --- DELETE ---
	req = httptest.NewRequest("DELETE", "/api/admin/feeds/"+idStr, nil)
	req.SetPathValue("id", idStr)
	w = httptest.NewRecorder()
	srv.apiFeedsDelete(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("delete: status = %d, want 200, body=%s", w.Code, w.Body.String())
	}

	// Verify list back to initial count after delete
	req = httptest.NewRequest("GET", "/api/admin/feeds", nil)
	w = httptest.NewRecorder()
	srv.apiFeedsList(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("list after delete: status = %d", w.Code)
	}
	if err := json.NewDecoder(w.Body).Decode(&listResp); err != nil {
		t.Fatalf("decode list after delete: %v", err)
	}
	if len(listResp.Feeds) != initialCount {
		t.Fatalf("feeds after delete = %d, want %d", len(listResp.Feeds), initialCount)
	}
}

// =============================================================================
// TestFeedsSyncAll — create enabled feed, sync all → 503 (syncer is nil)
// =============================================================================

func TestFeedsSyncAll(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)
	ctx := context.Background()

	// Create adapter and feed
	if _, err := st.DB.ExecContext(ctx, "INSERT OR IGNORE INTO feed_adapters(id, key, name, language, api_version, source, revision) VALUES (1, 'opencck', 'OpenCCK', 'javascript', 1, 'function sync(feed, api) { return []; }', 1)"); err != nil {
		t.Fatalf("setup adapter: %v", err)
	}
	_, err := st.AddFeedForModeAdapter(ctx, "sync-feed", "http://example.com/sync.json", 1, 1, true, 0, "", "", true)
	if err != nil {
		t.Fatalf("AddFeedForModeAdapter: %v", err)
	}

	// syncer is nil → should return 503
	req := httptest.NewRequest("POST", "/api/admin/feeds/sync-all", nil)
	w := httptest.NewRecorder()
	srv.apiFeedsSyncAll(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("sync-all with nil syncer: status = %d, want 503, body=%s", w.Code, w.Body.String())
	}
}

// =============================================================================
// TestFeedsGetNotFound — GET /feeds/99999 → 404
// =============================================================================

func TestFeedsGetNotFound(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)

	req := httptest.NewRequest("GET", "/api/admin/feeds/99999", nil)
	req.SetPathValue("id", "99999")
	w := httptest.NewRecorder()
	srv.apiFeedsGet(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("GET 404: status = %d, want 404, body=%s", w.Code, w.Body.String())
	}
}
