package store

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

func firstFeedID(t *testing.T, s *Store) int64 {
	t.Helper()
	feeds, err := s.Feeds(context.Background(), false)
	if err != nil || len(feeds) == 0 {
		t.Fatalf("expected at least one seeded feed, feeds=%v err=%v", feeds, err)
	}
	return feeds[0].ID
}

func replaceEntries(t *testing.T, s *Store, feedID int64, entries []CatalogEntry) {
	t.Helper()
	if err := s.Transaction(context.Background(), func(tx *sql.Tx) error {
		return ReplaceCatalogEntries(context.Background(), tx, feedID, entries)
	}); err != nil {
		t.Fatalf("ReplaceCatalogEntries: %v", err)
	}
}

// TestReplaceCatalogEntriesBatchBoundaries covers counts at, just under, and
// just over entryInsertBatchSize, since an off-by-one in the chunk loop
// would only surface right at those boundaries — a small feed's test
// wouldn't catch it.
func TestReplaceCatalogEntriesBatchBoundaries(t *testing.T) {
	for _, count := range []int{0, 1, entryInsertBatchSize - 1, entryInsertBatchSize, entryInsertBatchSize + 1, entryInsertBatchSize*2 + 7} {
		t.Run(fmt.Sprintf("count=%d", count), func(t *testing.T) {
			s := openTestStore(t)
			feedID := firstFeedID(t, s)

			entries := make([]CatalogEntry, count)
			for i := range entries {
				entries[i] = CatalogEntry{
					Category: "cat",
					Service:  "svc",
					CIDR:     fmt.Sprintf("10.%d.%d.0/24", i/256, i%256),
				}
			}
			replaceEntries(t, s, feedID, entries)

			var got int
			if err := s.DB.QueryRow("SELECT COUNT(*) FROM catalog_entries WHERE feed_id = ?", feedID).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != count {
				t.Fatalf("catalog_entries count = %d, want %d", got, count)
			}
			var prefixCount int
			if err := s.DB.QueryRow("SELECT COUNT(*) FROM prefixes").Scan(&prefixCount); err != nil {
				t.Fatal(err)
			}
			if prefixCount != count {
				t.Fatalf("prefixes count = %d, want %d", prefixCount, count)
			}
		})
	}
}

func TestReplaceCatalogEntriesReplacesAndNormalizes(t *testing.T) {
	s := openTestStore(t)
	feedID := firstFeedID(t, s)

	replaceEntries(t, s, feedID, []CatalogEntry{
		{Category: "cat", Service: "svc", CIDR: "192.168.1.0/24"},
		// Same prefix once masked — must collapse, not error.
		{Category: "cat", Service: "svc", CIDR: "192.168.1.99/24"},
		{Category: "cat", Service: "other", CIDR: "192.168.1.0/24"},
	})

	var entryCount, prefixCount int
	if err := s.DB.QueryRow("SELECT COUNT(*) FROM catalog_entries WHERE feed_id = ?", feedID).Scan(&entryCount); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow("SELECT COUNT(*) FROM prefixes").Scan(&prefixCount); err != nil {
		t.Fatal(err)
	}
	if entryCount != 2 {
		t.Errorf("entries = %d, want 2 (masked duplicate must collapse)", entryCount)
	}
	if prefixCount != 1 {
		t.Errorf("prefixes = %d, want 1 (same prefix shared between services)", prefixCount)
	}

	// Resync with different content replaces the old rows.
	replaceEntries(t, s, feedID, []CatalogEntry{
		{Category: "cat", Service: "svc", CIDR: "10.0.0.0/8"},
	})
	if err := s.DB.QueryRow("SELECT COUNT(*) FROM catalog_entries WHERE feed_id = ?", feedID).Scan(&entryCount); err != nil {
		t.Fatal(err)
	}
	if entryCount != 1 {
		t.Errorf("entries after resync = %d, want 1", entryCount)
	}

	// The old prefix is orphaned now; prune removes exactly it.
	pruned, err := s.PruneOrphanPrefixes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if pruned != 1 {
		t.Errorf("pruned = %d, want 1", pruned)
	}
	if err := s.DB.QueryRow("SELECT COUNT(*) FROM prefixes").Scan(&prefixCount); err != nil {
		t.Fatal(err)
	}
	if prefixCount != 1 {
		t.Errorf("prefixes after prune = %d, want 1", prefixCount)
	}
}

func TestReplaceCatalogEntriesRejectsInvalidCIDR(t *testing.T) {
	s := openTestStore(t)
	feedID := firstFeedID(t, s)
	err := s.Transaction(context.Background(), func(tx *sql.Tx) error {
		return ReplaceCatalogEntries(context.Background(), tx, feedID, []CatalogEntry{
			{Category: "cat", Service: "svc", CIDR: "not-a-cidr"},
		})
	})
	if err == nil {
		t.Fatal("expected error for invalid CIDR")
	}
}

func TestEnsureDictionaryIDsAreStable(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	var first, second int64
	err := s.Transaction(ctx, func(tx *sql.Tx) error {
		var err error
		first, err = EnsureServiceID(ctx, tx, "cat", "svc")
		if err != nil {
			return err
		}
		second, err = EnsureServiceID(ctx, tx, "cat", "svc")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if first != second || first == 0 {
		t.Errorf("EnsureServiceID not stable: first=%d second=%d", first, second)
	}
}

func TestToggleSelections(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	userID, err := s.AddUser(ctx, User{Name: "toggler", PeerIP: "192.0.2.10", PeerASN: 65001})
	if err != nil {
		t.Fatal(err)
	}
	user, err := s.User(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	modeID := user.CatalogModeID

	check := func(wantCats int, wantSvcs int) {
		t.Helper()
		categories, services, err := s.UserModeSelection(ctx, userID, modeID)
		if err != nil {
			t.Fatal(err)
		}
		if len(categories) != wantCats || len(services) != wantSvcs {
			t.Fatalf("selection = %d cats / %d svcs, want %d / %d", len(categories), len(services), wantCats, wantSvcs)
		}
	}

	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		if err := ToggleSelectedCategory(ctx, tx, userID, modeID, "cat-a", true); err != nil {
			return err
		}
		return ToggleSelectedService(ctx, tx, userID, modeID, "cat-a", "svc-1", true)
	}); err != nil {
		t.Fatal(err)
	}
	check(1, 1)

	// Toggling on twice is a no-op; toggling off removes.
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		if err := ToggleSelectedCategory(ctx, tx, userID, modeID, "cat-a", true); err != nil {
			return err
		}
		if err := ToggleSelectedService(ctx, tx, userID, modeID, "cat-a", "svc-1", false); err != nil {
			return err
		}
		// Removing a never-selected name (not even in the dictionary)
		// must not error.
		return ToggleSelectedCategory(ctx, tx, userID, modeID, "no-such-cat", false)
	}); err != nil {
		t.Fatal(err)
	}
	check(1, 0)
}
