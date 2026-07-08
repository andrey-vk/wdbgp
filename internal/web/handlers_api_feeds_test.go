package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/andrey-vk/wdbgp/internal/feeds"
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
	if _, err := st.DB.ExecContext(ctx, "INSERT OR IGNORE INTO feed_adapters(id, name, language, api_version, source, revision) VALUES (1, 'OpenCCK', 'javascript', 1, 'function sync(feed, api) { return []; }', 1)"); err != nil {
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
	if _, err := st.DB.ExecContext(ctx, "INSERT OR IGNORE INTO feed_adapters(id, name, language, api_version, source, revision) VALUES (1, 'OpenCCK', 'javascript', 1, 'function sync(feed, api) { return []; }', 1)"); err != nil {
		t.Fatalf("setup adapter: %v", err)
	}
	_, err := st.AddFeed(ctx, "sync-feed", "http://example.com/sync.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatalf("AddFeed: %v", err)
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

// TestFeedsUpdateNotFound / TestFeedsDeleteNotFound guard against
// UpdateFeed/DeleteFeed's sql.ErrNoRows for a nonexistent feed ID
// surfacing as a raw 500 instead of the 404 apiFeedsGet already returns.
func TestFeedsUpdateNotFound(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)
	ctx := context.Background()
	if _, err := st.DB.ExecContext(ctx, "INSERT OR IGNORE INTO feed_adapters(id, name, language, api_version, source, revision) VALUES (1, 'OpenCCK', 'javascript', 1, 'function sync(feed, api) { return []; }', 1)"); err != nil {
		t.Fatalf("setup adapter: %v", err)
	}

	updateBody := strings.NewReader(`{"name":"ghost","url":"http://example.com/feed.json","enabled":true,"sync_interval":3600,"adapter_id":1}`)
	req := httptest.NewRequest("PUT", "/api/admin/feeds/99999", updateBody)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "99999")
	w := httptest.NewRecorder()
	srv.apiFeedsUpdate(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("PUT 404: status = %d, want 404, body=%s", w.Code, w.Body.String())
	}
}

func TestFeedsDeleteNotFound(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)

	req := httptest.NewRequest("DELETE", "/api/admin/feeds/99999", nil)
	req.SetPathValue("id", "99999")
	w := httptest.NewRecorder()
	srv.apiFeedsDelete(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("DELETE 404: status = %d, want 404, body=%s", w.Code, w.Body.String())
	}
}

// TestFeedsUpdateRejectsInvalidAdapterID mirrors
// TestFeedsCreateRejectsInvalidAdapterID for the update path — a stale/
// invalid adapter_id must be rejected before UpdateFeed hits the
// feeds.adapter_id foreign-key constraint (raw 500 instead of clean 400).
func TestFeedsUpdateRejectsInvalidAdapterID(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)
	ctx := context.Background()
	if _, err := st.DB.ExecContext(ctx, "INSERT OR IGNORE INTO feed_adapters(id, name, language, api_version, source, revision) VALUES (1, 'OpenCCK', 'javascript', 1, 'function sync(feed, api) { return []; }', 1)"); err != nil {
		t.Fatalf("setup adapter: %v", err)
	}
	feedID, err := st.AddFeed(ctx, "existing-feed", "http://example.com/feed.json", 1, true, 3600, "", "", false)
	if err != nil {
		t.Fatalf("add feed: %v", err)
	}

	updateBody := strings.NewReader(`{"name":"existing-feed","url":"http://example.com/feed.json","enabled":true,"sync_interval":3600,"adapter_id":99999}`)
	req := httptest.NewRequest("PUT", "/api/admin/feeds/"+strconv.FormatInt(feedID, 10), updateBody)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", strconv.FormatInt(feedID, 10))
	w := httptest.NewRecorder()
	srv.apiFeedsUpdate(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("update with invalid adapter_id: status = %d, want 400, body=%s", w.Code, w.Body.String())
	}
}

// TestFeedsCreateRejectsEmptyName / TestFeedsUpdateRejectsEmptyName restore
// a validation the old (pre-SPA) handlers had — the JSON handlers dropped
// it, and the feeds table only enforces NOT NULL UNIQUE, not non-blank.
func TestFeedsCreateRejectsEmptyName(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)
	ctx := context.Background()
	if _, err := st.DB.ExecContext(ctx, "INSERT OR IGNORE INTO feed_adapters(id, name, language, api_version, source, revision) VALUES (1, 'OpenCCK', 'javascript', 1, 'function sync(feed, api) { return []; }', 1)"); err != nil {
		t.Fatalf("setup adapter: %v", err)
	}

	createBody := strings.NewReader(`{"name":"","url":"http://example.com/feed.json","enabled":true,"sync_interval":3600,"adapter_id":1}`)
	req := httptest.NewRequest("POST", "/api/admin/feeds", createBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiFeedsCreate(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("create with empty name: status = %d, want 400, body=%s", w.Code, w.Body.String())
	}
}

func TestFeedsUpdateRejectsEmptyName(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)
	ctx := context.Background()
	if _, err := st.DB.ExecContext(ctx, "INSERT OR IGNORE INTO feed_adapters(id, name, language, api_version, source, revision) VALUES (1, 'OpenCCK', 'javascript', 1, 'function sync(feed, api) { return []; }', 1)"); err != nil {
		t.Fatalf("setup adapter: %v", err)
	}
	feedID, err := st.AddFeed(ctx, "existing-feed2", "http://example.com/feed.json", 1, true, 3600, "", "", false)
	if err != nil {
		t.Fatalf("add feed: %v", err)
	}

	updateBody := strings.NewReader(`{"name":"  ","url":"http://example.com/feed.json","enabled":true,"sync_interval":3600,"adapter_id":1}`)
	req := httptest.NewRequest("PUT", "/api/admin/feeds/"+strconv.FormatInt(feedID, 10), updateBody)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", strconv.FormatInt(feedID, 10))
	w := httptest.NewRecorder()
	srv.apiFeedsUpdate(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("update with blank name: status = %d, want 400, body=%s", w.Code, w.Body.String())
	}
}

// TestFeedsCreateRejectsInvalidModeID guards against a bug where a stale/
// invalid mode_id let AddFeed commit the feed row before the
// catalog_mode_feeds insert failed — the failure was only logged at Debug
// level, so the API still returned 201 with the feed silently assigned to no
// mode (and therefore excluded from Feeds(ctx, true) and every future
// automatic sync). mode_id must now be validated before the feed is created.
func TestFeedsCreateRejectsInvalidModeID(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)
	ctx := context.Background()

	if _, err := st.DB.ExecContext(ctx, "INSERT OR IGNORE INTO feed_adapters(id, name, language, api_version, source, revision) VALUES (1, 'OpenCCK', 'javascript', 1, 'function sync(feed, api) { return []; }', 1)"); err != nil {
		t.Fatalf("setup adapter: %v", err)
	}

	req0 := httptest.NewRequest("GET", "/api/admin/feeds", nil)
	w0 := httptest.NewRecorder()
	srv.apiFeedsList(w0, req0)
	var before struct {
		Feeds []feedJSON `json:"feeds"`
	}
	if err := json.NewDecoder(w0.Body).Decode(&before); err != nil {
		t.Fatal(err)
	}

	createBody := strings.NewReader(`{"name":"bad-mode-feed","url":"http://example.com/feed.json","enabled":true,"sync_interval":3600,"mode_id":999999,"adapter_id":1}`)
	req := httptest.NewRequest("POST", "/api/admin/feeds", createBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiFeedsCreate(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", w.Code, w.Body.String())
	}

	req1 := httptest.NewRequest("GET", "/api/admin/feeds", nil)
	w1 := httptest.NewRecorder()
	srv.apiFeedsList(w1, req1)
	var after struct {
		Feeds []feedJSON `json:"feeds"`
	}
	if err := json.NewDecoder(w1.Body).Decode(&after); err != nil {
		t.Fatal(err)
	}
	if len(after.Feeds) != len(before.Feeds) {
		t.Fatalf("feed count = %d after rejected create, want unchanged %d — a feed with an invalid mode_id must not have been created", len(after.Feeds), len(before.Feeds))
	}
	for _, f := range after.Feeds {
		if f.Name == "bad-mode-feed" {
			t.Fatal("bad-mode-feed must not have been created after rejected mode_id")
		}
	}
}

// TestFeedsCreateRejectsInvalidAdapterID guards against a stale/invalid
// adapter_id only being caught by the feeds.adapter_id foreign-key
// constraint on insert, which surfaced as a raw driver error under a 500
// instead of a clean 400 — unlike mode_id, which was already validated
// up front (see TestFeedsCreateRejectsInvalidModeID).
func TestFeedsCreateRejectsInvalidAdapterID(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)

	createBody := strings.NewReader(`{"name":"bad-adapter-feed","url":"http://example.com/feed.json","enabled":true,"sync_interval":3600,"adapter_id":999999}`)
	req := httptest.NewRequest("POST", "/api/admin/feeds", createBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiFeedsCreate(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", w.Code, w.Body.String())
	}

	req1 := httptest.NewRequest("GET", "/api/admin/feeds", nil)
	w1 := httptest.NewRecorder()
	srv.apiFeedsList(w1, req1)
	var after struct {
		Feeds []feedJSON `json:"feeds"`
	}
	if err := json.NewDecoder(w1.Body).Decode(&after); err != nil {
		t.Fatal(err)
	}
	for _, f := range after.Feeds {
		if f.Name == "bad-adapter-feed" {
			t.Fatal("bad-adapter-feed must not have been created after rejected adapter_id")
		}
	}
}

// =============================================================================
// TestFeedsSyncOneAsync — manual sync answers 202 immediately, the feeds
// list reports live syncing/sync_started_at while the sync runs, a second
// trigger gets 409, and the status clears once the sync finishes. Uses a
// real Syncer whose HTTP transport blocks until the test releases it.
// =============================================================================

type blockingTransport struct {
	release chan struct{}
	started chan struct{} // closed on first request, signals the sync is mid-flight
}

func (bt *blockingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	select {
	case <-bt.started:
	default:
		close(bt.started)
	}
	<-bt.release
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`[]`)),
		Header:     make(http.Header),
	}, nil
}

func TestFeedsSyncOneAsync(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)
	ctx := context.Background()

	if _, err := st.DB.ExecContext(ctx, `INSERT OR IGNORE INTO feed_adapters(id, name, language, api_version, source, revision)
		VALUES (7, 'blocking', 'javascript', 1, 'function sync(feed, api) { api.httpGet(feed.url); return []; }', 1)`); err != nil {
		t.Fatalf("setup adapter: %v", err)
	}
	feedID, err := st.AddFeed(ctx, "async-feed", "http://feed.test/data.json", 7, true, 0, "", "", false)
	if err != nil {
		t.Fatalf("AddFeed: %v", err)
	}

	bt := &blockingTransport{release: make(chan struct{}), started: make(chan struct{})}
	syncer := feeds.NewSyncer(st, srv.settings)
	syncer.Client = &http.Client{Transport: bt}
	srv.syncer = syncer

	syncPath := "/api/admin/feeds/" + strconv.FormatInt(feedID, 10) + "/sync"
	trigger := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", syncPath, nil)
		req.SetPathValue("id", strconv.FormatInt(feedID, 10))
		w := httptest.NewRecorder()
		srv.apiFeedsSyncOne(w, req)
		return w
	}

	if w := trigger(); w.Code != http.StatusAccepted {
		t.Fatalf("first trigger: status = %d, want 202, body=%s", w.Code, w.Body.String())
	}

	// The goroutine is mid-sync once the transport has been reached.
	select {
	case <-bt.started:
	case <-time.After(5 * time.Second):
		t.Fatal("sync goroutine never reached the HTTP transport")
	}

	if w := trigger(); w.Code != http.StatusConflict {
		t.Fatalf("second trigger during sync: status = %d, want 409, body=%s", w.Code, w.Body.String())
	}

	listFeeds := func() []feedJSON {
		req := httptest.NewRequest("GET", "/api/admin/feeds", nil)
		w := httptest.NewRecorder()
		srv.apiFeedsList(w, req)
		var resp struct {
			Feeds []feedJSON `json:"feeds"`
		}
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode list: %v", err)
		}
		return resp.Feeds
	}

	var during *feedJSON
	for _, f := range listFeeds() {
		if f.ID == feedID {
			f := f
			during = &f
		}
	}
	if during == nil || !during.Syncing || during.SyncStartedAt == "" {
		t.Fatalf("feed during sync = %+v, want syncing=true with sync_started_at", during)
	}

	close(bt.release)

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, running := srv.syncer.SyncingSince(feedID); !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("sync never finished after transport release")
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, f := range listFeeds() {
		if f.ID == feedID && (f.Syncing || f.SyncStartedAt != "") {
			t.Fatalf("feed after sync = %+v, want syncing=false", f)
		}
	}
}
