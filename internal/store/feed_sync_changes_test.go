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
	userID = addBlastRadiusTestUser(t, s, DefaultCatalogModeID, FilterModeGlobal, "cat-seed", 1)
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		return SetUserModeSelection(ctx, tx, userID, DefaultCatalogModeID, []string{selected}, nil)
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		return RecordFeedSyncChangeTx(ctx, tx, feedID, FeedSyncDiff{
			AddedServices: 2, AddedByCategory: map[string]int{"ai": 2},
		}, []ModeCategoryGrowth{{ModeID: DefaultCatalogModeID, Category: "ai", Prefixes: []string{
			"21.0.0.0/8", "22.0.0.0/8", "23.0.0.0/8", "24.0.0.0/8", "25.0.0.0/8",
		}}}, time.Now().Unix())
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

// Growth is measured on the mode's effective prefix set, so a sync from an
// exclude feed that re-announces prefixes is real growth for the user too.
func TestUserFeedChangesShowsEffectiveGrowthFromExcludeFeed(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	userID, _ := userSelectingFeedChange(t, s, "ai", true)
	changes, err := s.UserFeedChanges(ctx, userID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 {
		t.Fatalf("effective growth from an exclude feed not shown: %+v", changes)
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
	if err := s.AckUserFeedChanges(ctx, userID, changes[0].ChangeID); err != nil {
		t.Fatal(err)
	}
	after, err := s.UserFeedChanges(ctx, userID, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 0 {
		t.Fatalf("acknowledged change still shown: %+v", after)
	}
	if err := s.AckUserFeedChanges(ctx, userID, changes[0].ChangeID-1); err != nil {
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
		}, []ModeCategoryGrowth{{ModeID: DefaultCatalogModeID, Category: "ai", Prefixes: []string{"26.0.0.0/8"}}}, same)
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
	if err := s.AckUserFeedChanges(ctx, userID, changes[0].ChangeID); err != nil {
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

// A prefix a feed adds is growth for the mode only if the mode didn't already
// announce it — another include feed supplying it, or an exclude feed cutting
// it, changes the outcome. This runs the same sequence as a sync.
func TestModeGrowthCountsOnlyNewlyAnnouncedPrefixes(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	feedA, err := s.AddFeed(ctx, "growth-a", "https://example.test/a.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	feedB, err := s.AddFeed(ctx, "growth-b", "https://example.test/b.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []int64{feedA, feedB} {
		if _, err := s.DB.ExecContext(ctx, "INSERT INTO catalog_mode_feeds(mode_id, feed_id, exclude) VALUES (?, ?, 0)", DefaultCatalogModeID, f); err != nil {
			t.Fatal(err)
		}
	}
	seed := func(feedID int64, entries []CatalogEntry) {
		t.Helper()
		if err := s.Transaction(ctx, func(tx *sql.Tx) error {
			if err := ReplaceCatalogEntries(ctx, tx, feedID, entries); err != nil {
				return err
			}
			return RebuildModeEntriesForFeedTx(ctx, tx, feedID)
		}); err != nil {
			t.Fatal(err)
		}
	}
	seed(feedA, []CatalogEntry{{Category: "cat-a", Service: "svc1", CIDR: "21.0.0.0/8"}})
	seed(feedB, []CatalogEntry{{Category: "cat-a", Service: "svc2", CIDR: "21.0.0.0/8"}})

	// Sync of feed A: adds svc3 with a new prefix (22/8) and keeps 21/8.
	// 22/8 is growth in cat-a; 21/8 was already announced via feed B.
	syncTo := func(feedID int64, entries []CatalogEntry) []ModeCategoryGrowth {
		t.Helper()
		var growth []ModeCategoryGrowth
		if err := s.Transaction(ctx, func(tx *sql.Tx) error {
			before, err := SnapshotFeedModePrefixesTx(ctx, tx, feedID)
			if err != nil {
				return err
			}
			if err := ReplaceCatalogEntries(ctx, tx, feedID, entries); err != nil {
				return err
			}
			if err := RebuildModeEntriesForFeedTx(ctx, tx, feedID); err != nil {
				return err
			}
			after, err := SnapshotFeedModePrefixesTx(ctx, tx, feedID)
			if err != nil {
				return err
			}
			growth = ModeGrowth(before, after)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return growth
	}
	g := syncTo(feedA, []CatalogEntry{
		{Category: "cat-a", Service: "svc1", CIDR: "21.0.0.0/8"},
		{Category: "cat-a", Service: "svc3", CIDR: "22.0.0.0/8"},
	})
	if len(g) != 1 || g[0].Category != "cat-a" || len(g[0].Prefixes) != 1 || g[0].Prefixes[0] != "22.0.0.0/8" {
		t.Fatalf("growth = %+v, want exactly cat-a: 22.0.0.0/8 (21/8 was already announced)", g)
	}

	// Feed B adds cat-b/svc4 with 21/8, which the mode already announces
	// through cat-a. For a user who selects only cat-b it is new.
	g = syncTo(feedB, []CatalogEntry{
		{Category: "cat-a", Service: "svc2", CIDR: "21.0.0.0/8"},
		{Category: "cat-b", Service: "svc4", CIDR: "21.0.0.0/8"},
	})
	if len(g) != 1 || g[0].Category != "cat-b" || len(g[0].Prefixes) != 1 || g[0].Prefixes[0] != "21.0.0.0/8" {
		t.Fatalf("growth = %+v, want exactly cat-b: 21.0.0.0/8 (new through cat-b even though cat-a already had it)", g)
	}
}

// A route filter that denies a growth prefix means it was never announced to
// the user, so it must not be reported.
func TestUserFeedChangesAppliesRouteFilters(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	userID, feedID := userSelectingFeedChange(t, s, "ai", false)
	if _, err := s.DB.ExecContext(ctx, "DELETE FROM feed_sync_mode_growth"); err != nil {
		t.Fatal(err)
	}
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		return RecordFeedSyncChangeTx(ctx, tx, feedID, FeedSyncDiff{AddedServices: 1, AddedByCategory: map[string]int{"ai": 1}},
			[]ModeCategoryGrowth{{ModeID: DefaultCatalogModeID, Category: "ai", Prefixes: []string{"31.0.0.0/8", "32.0.0.0/8"}}},
			time.Now().Unix())
	}); err != nil {
		t.Fatal(err)
	}
	// Override mode, so the user's own deny list is the one that applies.
	if _, err := s.DB.ExecContext(ctx, "UPDATE users SET filter_mode = ? WHERE id = ?", filterModeToInt(FilterModeOverride), userID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SetUserRouteFilters(ctx, userID, RouteFilters{Deny: []string{"31.0.0.0/8"}}, AuditMeta{}); err != nil {
		t.Fatal(err)
	}
	changes, err := s.UserFeedChanges(ctx, userID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || len(changes[0].Categories) != 1 || changes[0].Categories[0].AddedPrefixes != 1 {
		t.Fatalf("UserFeedChanges = %+v, want one prefix (32/8) after the deny on 31/8", changes)
	}
}

func TestDiffCatalogEntriesDetectsMovedAssociations(t *testing.T) {
	prev := []CatalogEntry{
		{Category: "a", Service: "s1", CIDR: "10.0.0.0/24"},
		{Category: "b", Service: "s2", CIDR: "10.0.1.0/24"},
	}
	next := []CatalogEntry{
		{Category: "a", Service: "s1", CIDR: "10.0.1.0/24"},
		{Category: "b", Service: "s2", CIDR: "10.0.0.0/24"},
	}
	if !DiffCatalogEntries(prev, next).HasChanges() {
		t.Fatal("swapping prefixes between services in different categories reported no change")
	}
}

// A deny on a more specific prefix splits a growth prefix into fragments, and
// the fragments are still announced, so the growth must still be reported.
func TestUserFeedChangesKeepsPartiallyFilteredGrowth(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	userID, feedID := userSelectingFeedChange(t, s, "ai", false)
	if _, err := s.DB.ExecContext(ctx, "DELETE FROM feed_sync_mode_growth"); err != nil {
		t.Fatal(err)
	}
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		return RecordFeedSyncChangeTx(ctx, tx, feedID, FeedSyncDiff{AddedServices: 1, AddedByCategory: map[string]int{"ai": 1}},
			[]ModeCategoryGrowth{{ModeID: DefaultCatalogModeID, Category: "ai", Prefixes: []string{"31.0.0.0/8"}}}, time.Now().Unix())
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, "UPDATE users SET filter_mode = ? WHERE id = ?", filterModeToInt(FilterModeOverride), userID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SetUserRouteFilters(ctx, userID, RouteFilters{Deny: []string{"31.1.0.0/16"}}, AuditMeta{}); err != nil {
		t.Fatal(err)
	}
	changes, err := s.UserFeedChanges(ctx, userID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Categories[0].AddedPrefixes != 1 {
		t.Fatalf("UserFeedChanges = %+v, want the partially filtered 31/8 reported", changes)
	}
}

// A disabled mode announces nothing, so its growth isn't news to the user.
func TestUserFeedChangesSkipsDisabledMode(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	userID, _ := userSelectingFeedChange(t, s, "ai", false)
	if _, err := s.DB.ExecContext(ctx, "UPDATE catalog_modes SET enabled = 0 WHERE id = ?", DefaultCatalogModeID); err != nil {
		t.Fatal(err)
	}
	changes, err := s.UserFeedChanges(ctx, userID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 0 {
		t.Fatalf("growth shown for a disabled mode: %+v", changes)
	}
}
