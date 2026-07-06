package store

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// TestAddFeedWithModeRollsBackOnModeAssignmentFailure guards against a
// non-atomic create: AddFeed's transaction only ever inserted the feeds
// row, so a caller doing the catalog_mode_feeds assignment as a separate,
// later call could end up with a feed row committed and orphaned (invisible
// to mode-scoped sync, and a source of duplicate feeds on client retry) if
// that second insert failed. AddFeedWithMode must do both in one
// transaction: if the mode assignment fails, the feed row must not exist.
func TestAddFeedWithModeRollsBackOnModeAssignmentFailure(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	// A nonexistent mode ID fails catalog_mode_feeds' own FK constraint —
	// this must roll back the whole operation, not just fail the second
	// half of it.
	const nonexistentModeID = 999999
	if _, err := s.AddFeedWithMode(ctx, "orphan-candidate", "https://example.test/feed.json", 1, true, 0, "", "", true, nonexistentModeID); err == nil {
		t.Fatal("expected an error from AddFeedWithMode with a nonexistent mode_id")
	}

	var count int
	if err := s.DB.QueryRow("SELECT COUNT(*) FROM feeds WHERE name = 'orphan-candidate'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("feed row exists despite failed mode assignment — got %d, want 0 (atomic rollback)", count)
	}
}

// TestAddFeedWithModeAssignsOnSuccess confirms the happy path: a valid mode
// ID gets the feed assigned in the same call, matching what AddFeed +
// AddFeedToMode used to require two separate calls for.
func TestAddFeedWithModeAssignsOnSuccess(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	modeID, err := s.AddCatalogMode(ctx, "Add Feed With Mode Test", true)
	if err != nil {
		t.Fatalf("add mode: %v", err)
	}

	feedID, err := s.AddFeedWithMode(ctx, "assigned-feed", "https://example.test/feed.json", 1, true, 0, "", "", true, modeID)
	if err != nil {
		t.Fatalf("AddFeedWithMode: %v", err)
	}

	var count int
	if err := s.DB.QueryRow(
		"SELECT COUNT(*) FROM catalog_mode_feeds WHERE mode_id = ? AND feed_id = ?", modeID, feedID,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("catalog_mode_feeds row count = %d, want 1", count)
	}
}

func TestUpdateFeedURLClearsSnapshotAndDeleteCascades(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.AddFeed(ctx, "custom", "https://example.test/old.json", 1, true, 0, "", "", true); err != nil {
		t.Fatal(err)
	}
	feeds, err := s.Feeds(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	feed := feeds[len(feeds)-1]
	if _, err := s.DB.Exec(
		"UPDATE feeds SET last_success = 1751000000, last_error = 'old error' WHERE id = ?", feed.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertCatalogEntries(ctx, feed.ID, []CatalogEntry{
		{Category: "Custom", Service: "Example", CIDR: "8.8.8.0/24"},
	}); err != nil {
		t.Fatal(err)
	}

	feed.Name = "renamed"
	feed.URL = "https://example.test/new.json"
	if err := s.UpdateFeed(ctx, feed); err != nil {
		t.Fatal(err)
	}
	feeds, err = s.Feeds(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	updated := feeds[len(feeds)-1]
	if updated.Name != feed.Name || updated.URL != feed.URL ||
		updated.LastSuccess != 0 || updated.LastError != "" {
		t.Fatalf("updated feed = %#v", updated)
	}
	var entries int
	if err := s.DB.QueryRow("SELECT COUNT(*) FROM catalog_entries WHERE feed_id = ?", feed.ID).
		Scan(&entries); err != nil {
		t.Fatal(err)
	}
	if entries != 0 {
		t.Fatalf("entries after URL change = %d, want 0", entries)
	}

	if err := s.InsertCatalogEntries(ctx, feed.ID, []CatalogEntry{
		{Category: "Custom", Service: "Example", CIDR: "8.8.8.0/24"},
	}); err != nil {
		t.Fatal(err)
	}
	userID, err := s.AddUser(ctx, User{
		Name: "selected", PeerIP: "172.16.0.2", PeerASN: 65001, Enabled: true,
		Networks: []string{"192.168.20.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		return SetUserSelection(ctx, tx, userID, []string{"Custom"},
			[]ServiceKey{{Category: "Custom", Service: "Example"}})
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteFeed(ctx, feed.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow("SELECT COUNT(*) FROM catalog_entries WHERE feed_id = ?", feed.ID).
		Scan(&entries); err != nil {
		t.Fatal(err)
	}
	if entries != 0 {
		t.Fatalf("entries after feed deletion = %d, want 0", entries)
	}
	for _, table := range []string{"selected_categories", "selected_services"} {
		var selections int
		if err := s.DB.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE user_id = ?", userID).
			Scan(&selections); err != nil {
			t.Fatal(err)
		}
		if selections != 0 {
			t.Fatalf("%s after feed deletion = %d, want 0", table, selections)
		}
	}
}
