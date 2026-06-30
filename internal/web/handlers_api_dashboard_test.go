package web

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/andrey-vk/wdbgp/internal/store"
)

// =============================================================================
// TestDashboardEmpty — GET /dashboard with empty DB → 200, verify defaults
// =============================================================================

func TestDashboardEmpty(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)

	req := httptest.NewRequest("GET", "/api/admin/dashboard", nil)
	w := httptest.NewRecorder()
	srv.apiDashboard(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}

	var resp dashboardJSON
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp.Prefixes != 0 {
		t.Fatalf("prefixes = %d, want 0", resp.Prefixes)
	}
	if resp.Users["total"] != 0 {
		t.Fatalf("users.total = %d, want 0", resp.Users["total"])
	}
	if resp.BGP.TotalPeers < 1 {
		t.Fatalf("bgp.total_peers = %d, want >=1", resp.BGP.TotalPeers)
	}
	if resp.MetricsEnabled {
		t.Fatal("metrics_enabled should be false")
	}
	if len(resp.UserHistory) != 0 {
		t.Fatalf("user_history = %d, want 0", len(resp.UserHistory))
	}
	if len(resp.FeedHistory) != 0 {
		t.Fatalf("feed_history = %d, want 0", len(resp.FeedHistory))
	}
	if resp.Uptime <= 0 {
		t.Fatal("uptime_seconds should be > 0")
	}
}

// =============================================================================
// TestDashboardWithData — insert a user + a feed, then GET dashboard → verify counts
// =============================================================================

func TestDashboardWithData(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)
	ctx := context.Background()

	// Create a user
	_, err := st.AddUser(ctx, store.User{
		Name: "test-user", PeerIP: "192.168.1.1", PeerASN: 65001, Enabled: true,
		Networks: []string{"10.0.0.0/8"},
	})
	if err != nil {
		t.Fatalf("AddUser: %v", err)
	}

	// Insert a feed adapter (needed before adding a feed)
	if _, err := st.DB.ExecContext(ctx, "INSERT OR IGNORE INTO feed_adapters(id, key, name, language, api_version, source, revision) VALUES (1, 'test-adapter', 'Test', 'javascript', 1, 'function sync(feed, api) { return []; }', 1)"); err != nil {
		t.Fatalf("setup adapter: %v", err)
	}

	// Count initial feeds
	var initialFeeds int
	if err = st.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM feeds").Scan(&initialFeeds); err != nil {
		t.Fatalf("count feeds: %v", err)
	}

	// Create a feed
	_, err = st.AddFeed(ctx, "test-feed", "http://example.com/feed.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatalf("AddFeed: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/admin/dashboard", nil)
	w := httptest.NewRecorder()
	srv.apiDashboard(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}

	var resp dashboardJSON
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp.Users["total"] != 1 {
		t.Fatalf("users.total = %d, want 1", resp.Users["total"])
	}
	if feedsTotal, ok := resp.Feeds["total"].(float64); !ok || int(feedsTotal) != initialFeeds+1 {
		t.Fatalf("feeds.total = %v, want %d", resp.Feeds["total"], initialFeeds+1)
	}
}

// =============================================================================
// TestDashboardMetricsEnabled — set metricsEnabled=true, create user snapshot,
// GET dashboard → user_history non-empty
// =============================================================================

func TestDashboardMetricsEnabled(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)
	srv.settings.MetricsEnabled.Set(context.Background(), true)
	ctx := context.Background()

	if err := st.SaveUserSnapshot(ctx, 1, 2, 10); err != nil {
		t.Fatalf("SaveUserSnapshot: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/admin/dashboard", nil)
	w := httptest.NewRecorder()
	srv.apiDashboard(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp dashboardJSON
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if !resp.MetricsEnabled {
		t.Fatal("metrics_enabled should be true")
	}
	if len(resp.UserHistory) == 0 {
		t.Fatal("user_history should be non-empty")
	}
}
