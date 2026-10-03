package store

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestDiffCatalogEntriesCountsServicesAndPrefixes(t *testing.T) {
	prev := []CatalogEntry{
		{Category: "ai", Service: "a", CIDR: "10.0.0.0/24"},
		{Category: "ai", Service: "b", CIDR: "10.0.1.0/24"},
		{Category: "tv", Service: "c", CIDR: "10.9.0.0/16"},
	}
	next := []CatalogEntry{
		{Category: "ai", Service: "a", CIDR: "10.0.0.0/24"},
		{Category: "ai", Service: "d", CIDR: "10.0.2.0/24"},
		{Category: "ai", Service: "e", CIDR: "10.0.3.5/24"},
		{Category: "tv", Service: "c", CIDR: "10.9.0.0/16"},
	}
	d := DiffCatalogEntries(prev, next)
	if d.AddedServices != 2 || d.RemovedServices != 1 {
		t.Fatalf("services added/removed = %d/%d, want 2/1", d.AddedServices, d.RemovedServices)
	}
	if d.AddedByCategory["ai"] != 2 || len(d.AddedByCategory) != 1 {
		t.Fatalf("AddedByCategory = %v, want only ai: 2", d.AddedByCategory)
	}
	if d.AddedPrefixes != 2 || d.RemovedPrefixes != 1 {
		t.Fatalf("prefixes added/removed = %d/%d, want 2/1 (10.0.2.0/24 and 10.0.3.5/24 masked to 10.0.3.0/24 are new; 10.0.1.0/24 is gone)", d.AddedPrefixes, d.RemovedPrefixes)
	}
	if !d.HasChanges() {
		t.Fatal("HasChanges = false for a diff with changes")
	}
	if DiffCatalogEntries(prev, prev).HasChanges() {
		t.Fatal("HasChanges = true for identical entries")
	}
}

func TestCatalogEntriesForFeedRoundTrip(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	feedID, err := s.AddFeed(ctx, "rt-feed", "https://example.test/rt.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	want := []CatalogEntry{
		{Category: "ai", Service: "a", CIDR: "10.0.0.0/24"},
		{Category: "ai", Service: "a", CIDR: "10.0.1.0/24"},
		{Category: "tv", Service: "c", CIDR: "2001:db8::/32"},
	}
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		return ReplaceCatalogEntries(ctx, tx, feedID, want)
	}); err != nil {
		t.Fatal(err)
	}
	var got []CatalogEntry
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		var err error
		got, err = CatalogEntriesForFeedTx(ctx, tx, feedID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if d := DiffCatalogEntries(want, got); d.HasChanges() {
		t.Fatalf("read-back differs from written entries: %+v (got %+v)", d, got)
	}
}

func TestFeedSyncChangeHistoryIsBounded(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	feedID, err := s.AddFeed(ctx, "hist-feed", "https://example.test/hist.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < feedSyncChangeRetention+5; i++ {
		if err := s.Transaction(ctx, func(tx *sql.Tx) error {
			return RecordFeedSyncChangeTx(ctx, tx, feedID, FeedSyncDiff{
				AddedServices: 1, AddedByCategory: map[string]int{"ai": 1},
			}, nil, int64(1000+i))
		}); err != nil {
			t.Fatal(err)
		}
	}
	changes, err := s.RecentFeedSyncChanges(ctx, feedID, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != feedSyncChangeRetention {
		t.Fatalf("kept %d changes, want the retention bound %d", len(changes), feedSyncChangeRetention)
	}
	if changes[0].SyncedAt != int64(1000+feedSyncChangeRetention+4) {
		t.Fatalf("newest kept = %d, want the most recent sync", changes[0].SyncedAt)
	}
	if len(changes[0].Categories) != 1 || changes[0].Categories[0].Category != "ai" {
		t.Fatalf("categories = %+v, want ai breakdown", changes[0].Categories)
	}
}

// userSelectingFeedChange sets up a user on mode 1 selecting category "ai",
// with a feed linked to that mode that just added services to "ai".
func userSelectingFeedChange(t *testing.T, s *Store, selected string, exclude bool) (userID, feedID int64) {
	t.Helper()
	ctx := context.Background()
	feedID, err := s.AddFeed(ctx, "ai-feed", "https://example.test/ai.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	excl := 0
	if exclude {
		excl = 1
	}
	if _, err := s.DB.ExecContext(ctx,
		"INSERT INTO catalog_mode_feeds(mode_id, feed_id, exclude) VALUES (?, ?, ?)", DefaultCatalogModeID, feedID, excl); err != nil {
		t.Fatal(err)
	}
	addAIEntries(t, s, feedID, "21.0.0.0/8", "22.0.0.0/8", "23.0.0.0/8", "24.0.0.0/8", "25.0.0.0/8", "26.0.0.0/8")
	userID = addBlastRadiusTestUser(t, s, DefaultCatalogModeID, FilterModeGlobal, "cat-seed", 1)
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		return SetUserModeSelection(ctx, tx, userID, DefaultCatalogModeID, []string{selected}, nil)
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		return RecordFeedSyncChangeTx(ctx, tx, feedID, FeedSyncDiff{
			AddedServices: 2, AddedByCategory: map[string]int{"ai": 2},
		}, []ModeCategoryGrowth{{ModeID: DefaultCatalogModeID, Category: "ai", Prefixes: mustPrefixes(
			"21.0.0.0/8", "22.0.0.0/8", "23.0.0.0/8", "24.0.0.0/8", "25.0.0.0/8",
		)}}, time.Now().Unix())
	}); err != nil {
		t.Fatal(err)
	}
	return userID, feedID
}

func TestUserFeedChangesShowsOnlySelectedCategories(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	userID, _ := userSelectingFeedChange(t, s, "ai", false)
	changes, err := s.UserFeedChanges(ctx, userID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].FeedName != "ai-feed" || changes[0].Categories[0].AddedPrefixes != 5 {
		t.Fatalf("UserFeedChanges = %+v, want one ai-feed change with ai +5 prefixes", changes)
	}

	other, err := s.AddUser(ctx, User{Name: "other", PeerIP: "172.16.9.1", PeerASN: 65099, Enabled: true,
		FilterMode: FilterModeGlobal, CatalogModeID: DefaultCatalogModeID, Networks: []string{"192.168.9.0/24"}})
	if err != nil {
		t.Fatal(err)
	}
	none, err := s.UserFeedChanges(ctx, other, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("user with no ai selection got %+v, want none", none)
	}
}

// Growth is measured on the mode's effective prefix set. An exclude feed's
// prefixes are subtracted from the mode, so growth from one that isn't
// announced to the user is not reported.
func TestUserFeedChangesHidesExcludeFeedGrowthNotAnnounced(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	userID, _ := userSelectingFeedChange(t, s, "ai", true)
	changes, err := s.UserFeedChanges(ctx, userID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 0 {
		t.Fatalf("growth from an exclude feed shown as announced: %+v", changes)
	}
}

func TestAckUserFeedChangesHidesSeenAndNeverMovesBack(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	userID, _ := userSelectingFeedChange(t, s, "ai", false)
	now := time.Now()
	changes, err := s.UserFeedChanges(ctx, userID, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 {
		t.Fatalf("setup: want 1 change, got %+v", changes)
	}
	if err := s.AckUserFeedChanges(ctx, userID, DefaultCatalogModeID, changes[0].ChangeID); err != nil {
		t.Fatal(err)
	}
	after, err := s.UserFeedChanges(ctx, userID, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 0 {
		t.Fatalf("acknowledged change still shown: %+v", after)
	}
	if err := s.AckUserFeedChanges(ctx, userID, DefaultCatalogModeID, changes[0].ChangeID-1); err != nil {
		t.Fatal(err)
	}
	var seen int64
	if err := s.DB.QueryRowContext(ctx, "SELECT change_id FROM user_feed_changes_seen WHERE user_id = ?", userID).Scan(&seen); err != nil {
		t.Fatal(err)
	}
	if seen != changes[0].ChangeID {
		t.Fatalf("seen_id moved backwards to %d, want still %d", seen, changes[0].ChangeID)
	}
}

// Two changes can commit in the same second. Acknowledging the first must not
// hide the second, which the timestamp cursor used to do.
func TestAckUserFeedChangesDoesNotHideSameSecondChange(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	userID, feedID := userSelectingFeedChange(t, s, "ai", false)
	same := time.Now().Unix()
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		return RecordFeedSyncChangeTx(ctx, tx, feedID, FeedSyncDiff{
			AddedServices: 1, AddedByCategory: map[string]int{"ai": 1},
		}, []ModeCategoryGrowth{{ModeID: DefaultCatalogModeID, Category: "ai", Prefixes: mustPrefixes("26.0.0.0/8")}}, same)
	}); err != nil {
		t.Fatal(err)
	}
	changes, err := s.UserFeedChanges(ctx, userID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 2 {
		t.Fatalf("setup: want 2 changes, got %+v", changes)
	}
	if err := s.AckUserFeedChanges(ctx, userID, DefaultCatalogModeID, changes[0].ChangeID); err != nil {
		t.Fatal(err)
	}
	left, err := s.UserFeedChanges(ctx, userID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 || left[0].ChangeID != changes[1].ChangeID {
		t.Fatalf("after acknowledging the first, left = %+v, want only the second", left)
	}
}

// An acknowledgement advances the cursor of the mode it was shown for, and
// no other, so unseen changes in another mode stay visible.
func TestAckUserFeedChangesIsScopedToMode(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	userID, _ := userSelectingFeedChange(t, s, "ai", false)
	changes, err := s.UserFeedChanges(ctx, userID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 {
		t.Fatalf("setup: want 1 change, got %+v", changes)
	}
	otherMode, err := s.AddCatalogMode(ctx, "Other", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AckUserFeedChanges(ctx, userID, otherMode, changes[0].ChangeID); err != nil {
		t.Fatal(err)
	}
	still, err := s.UserFeedChanges(ctx, userID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(still) != 1 {
		t.Fatalf("acknowledging another mode hid this mode's change: %+v", still)
	}
}
