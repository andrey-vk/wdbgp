package store

import (
	"context"
	"database/sql"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

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
	if _, err := s.UpdateFeed(ctx, feed, AuditMeta{}); err != nil {
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
	if err := s.DeleteFeed(ctx, feed.ID, AuditMeta{}); err != nil {
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

// TestUpdateFeedPrevEnabledReflectsImmediatelyPriorState guards against a
// caller (the audit log hook in apiFeedsUpdate) bracketing UpdateFeed with
// its own separate "before" read instead of using the value this call
// returns. Simulates two overlapping requests without needing real
// goroutines: a "stale" read taken before either request's own UpdateFeed
// call runs, then two UpdateFeed calls in sequence (mirroring two
// overlapping PUTs committing in some order) — each call's own returned
// prevEnabled must reflect the state its own write actually overwrote, not
// whatever a once-cached read from earlier in the sequence still says.
func TestUpdateFeedPrevEnabledReflectsImmediatelyPriorState(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	feedID, err := s.AddFeed(ctx, "race-feed", "https://example.test/race.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	feed, err := s.Feed(ctx, feedID)
	if err != nil {
		t.Fatal(err)
	}

	// A read taken before anything below runs — if UpdateFeed's caller used
	// a value like this instead of UpdateFeed's own return, it would be
	// stale by the time the second call below runs.
	staleBefore := feed.Enabled // true

	// "Request B": disables the feed. Its own prevEnabled must be the
	// actual current value (true), same as staleBefore here — not yet
	// distinguishing, but establishes the baseline.
	feed.Enabled = false
	prevB, err := s.UpdateFeed(ctx, feed, AuditMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if prevB != true {
		t.Fatalf("request B's prevEnabled = %v, want true", prevB)
	}

	// "Request A": re-enables the feed. The real immediately-prior state is
	// false (what request B above just committed) — not staleBefore
	// (true). A caller using staleBefore here would compare true-to-true
	// and conclude nothing changed, even though the feed just flipped
	// false→true.
	feed.Enabled = true
	prevA, err := s.UpdateFeed(ctx, feed, AuditMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if prevA != false {
		t.Fatalf("request A's prevEnabled = %v, want false (request B's write), not staleBefore (%v)", prevA, staleBefore)
	}
}

// TestUpdateFeedConcurrentCallsPreserveAuditCommitOrder forces two
// UpdateFeed calls on the same feed to genuinely overlap (A pauses just
// before its commit, via updateFeedPreCommitHook, while still holding
// SQLite's write lock from its earlier UPDATE statement; B is launched
// while A is paused) and checks the resulting audit rows land in true
// commit order. Before UpdateFeed recorded its own audit entry inside its
// own transaction, the audit write was a separate statement run after the
// mutation's transaction committed, which gave two overlapping callers'
// mutation-commit and audit-insert steps no guaranteed relative order
// against each other.
func TestUpdateFeedConcurrentCallsPreserveAuditCommitOrder(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	feedID, err := s.AddFeed(ctx, "concurrent-audit-feed", "https://example.test/concurrent.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	feed, err := s.Feed(ctx, feedID)
	if err != nil {
		t.Fatal(err)
	}

	var hookCalls int32
	entered := make(chan struct{})
	release := make(chan struct{})
	updateFeedPreCommitHook = func() {
		if atomic.AddInt32(&hookCalls, 1) == 1 {
			close(entered)
			<-release
		}
	}
	t.Cleanup(func() { updateFeedPreCommitHook = nil })

	update := func(enabled bool, action string) error {
		f := feed
		f.Enabled = enabled
		_, err := s.UpdateFeed(ctx, f, AuditMeta{Actor: "admin:test", Action: action})
		return err
	}

	aDone := make(chan error, 1)
	go func() { aDone <- update(false, "a") }() // true -> false, pauses just before commit
	<-entered

	bDone := make(chan error, 1)
	go func() { bDone <- update(true, "b") }() // false -> true, should block behind A's open write transaction

	select {
	case err := <-bDone:
		t.Fatalf("B completed (err=%v) before A committed — expected SQLite's write lock to block it", err)
	case <-time.After(150 * time.Millisecond):
	}

	close(release)

	if err := <-aDone; err != nil {
		t.Fatalf("A: %v", err)
	}
	select {
	case err := <-bDone:
		if err != nil {
			t.Fatalf("B: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("B never completed after A committed")
	}

	entries, total, err := s.ListAuditLog(ctx, AuditLogFilter{ObjectType: "feed", ObjectID: strconv.FormatInt(feedID, 10)}, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Fatalf("total = %d, want 2", total)
	}
	// Newest first: entries[0] is B (committed second), entries[1] is A.
	bEntry, aEntry := entries[0], entries[1]
	if aEntry.Action != "a" || bEntry.Action != "b" {
		t.Fatalf("entries out of expected order: entries[0].Action=%q (want b, committed second), entries[1].Action=%q (want a, committed first)",
			bEntry.Action, aEntry.Action)
	}
	if aEntry.After != bEntry.Before {
		t.Fatalf("chain broken: A.after=%q != B.before=%q — audit insert order didn't match true commit order", aEntry.After, bEntry.Before)
	}
}
