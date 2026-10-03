package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/netip"
	"testing"
	"time"
)

// syncGrowthTo runs a sync's growth sequence for a feed: plan from the diff,
// replace the feed's entries, rebuild the modes, and finish the check.
func syncGrowthTo(t *testing.T, s *Store, feedID int64, entries []CatalogEntry) []ModeCategoryGrowth {
	t.Helper()
	ctx := context.Background()
	var growth []ModeCategoryGrowth
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		prev, err := CatalogEntriesForFeedTx(ctx, tx, feedID)
		if err != nil {
			return err
		}
		diff := DiffCatalogEntries(prev, entries)
		check, err := BeginGrowthCheckTx(ctx, tx, feedID, diff)
		if err != nil {
			return err
		}
		if err := ReplaceCatalogEntries(ctx, tx, feedID, entries); err != nil {
			return err
		}
		if err := RebuildModeEntriesForFeedTx(ctx, tx, feedID); err != nil {
			return err
		}
		growth, err = check.FinishTx(ctx, tx)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return growth
}

func newLinkedFeed(t *testing.T, s *Store, name string) int64 {
	t.Helper()
	ctx := context.Background()
	feedID, err := s.AddFeed(ctx, name, fmt.Sprintf("https://example.test/%s.json", name), 1, true, 0, "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, "INSERT INTO catalog_mode_feeds(mode_id, feed_id, exclude) VALUES (?, ?, 0)", DefaultCatalogModeID, feedID); err != nil {
		t.Fatal(err)
	}
	return feedID
}

func growthPrefixes(g []ModeCategoryGrowth, category string) []string {
	var out []string
	for _, x := range g {
		if x.Category == category {
			for _, p := range x.Prefixes {
				out = append(out, p.String())
			}
		}
	}
	return out
}

func entry(category, service, cidr string) CatalogEntry {
	return CatalogEntry{Category: category, Service: service, CIDR: cidr}
}

func TestGrowthIgnoresPrefixCoveredByBroaderOne(t *testing.T) {
	s := openTestStore(t)
	broad := newLinkedFeed(t, s, "broad")
	narrow := newLinkedFeed(t, s, "narrow")
	syncGrowthTo(t, s, broad, []CatalogEntry{entry("cat-a", "x", "10.0.0.0/8")})
	if g := syncGrowthTo(t, s, narrow, []CatalogEntry{entry("cat-a", "y", "10.0.0.0/9")}); len(g) != 0 {
		t.Fatalf("growth = %+v, want none: 10.0.0.0/9 is covered by 10.0.0.0/8", g)
	}
}

func TestGrowthBroaderArrivalIsGrowth(t *testing.T) {
	s := openTestStore(t)
	narrow := newLinkedFeed(t, s, "narrow")
	broad := newLinkedFeed(t, s, "broad")
	syncGrowthTo(t, s, narrow, []CatalogEntry{entry("cat-a", "y", "10.0.0.0/9")})
	g := syncGrowthTo(t, s, broad, []CatalogEntry{entry("cat-a", "x", "10.0.0.0/8")})
	if got := growthPrefixes(g, "cat-a"); len(got) != 1 || got[0] != "10.0.0.0/8" {
		t.Fatalf("growth cat-a = %v, want [10.0.0.0/8]", got)
	}
}

// Removing a covering prefix uncovers the narrower ones another feed supplies.
func TestGrowthRemovalUncoversDescendants(t *testing.T) {
	s := openTestStore(t)
	cover := newLinkedFeed(t, s, "cover")
	narrow := newLinkedFeed(t, s, "narrow")
	syncGrowthTo(t, s, cover, []CatalogEntry{entry("cat-a", "x", "10.0.0.0/8")})
	syncGrowthTo(t, s, narrow, []CatalogEntry{entry("cat-a", "y", "10.0.0.0/9")})
	g := syncGrowthTo(t, s, cover, nil)
	if got := growthPrefixes(g, "cat-a"); len(got) != 1 || got[0] != "10.0.0.0/9" {
		t.Fatalf("growth cat-a = %v, want [10.0.0.0/9] uncovered by the removal", got)
	}
}

func TestGrowthSkipsDefaultRoutes(t *testing.T) {
	s := openTestStore(t)
	feed := newLinkedFeed(t, s, "defaults")
	g := syncGrowthTo(t, s, feed, []CatalogEntry{entry("cat-a", "x", "0.0.0.0/0"), entry("cat-a", "y", "21.0.0.0/8")})
	if got := growthPrefixes(g, "cat-a"); len(got) != 1 || got[0] != "21.0.0.0/8" {
		t.Fatalf("growth cat-a = %v, want only 21.0.0.0/8", got)
	}
}

// A prefix already announced through category A is new through category B.
func TestGrowthNewCategoryForSamePrefix(t *testing.T) {
	s := openTestStore(t)
	a := newLinkedFeed(t, s, "feed-a")
	b := newLinkedFeed(t, s, "feed-b")
	syncGrowthTo(t, s, a, []CatalogEntry{entry("cat-a", "x", "21.0.0.0/8")})
	g := syncGrowthTo(t, s, b, []CatalogEntry{entry("cat-b", "y", "21.0.0.0/8")})
	if got := growthPrefixes(g, "cat-b"); len(got) != 1 || got[0] != "21.0.0.0/8" {
		t.Fatalf("growth = %+v, want cat-b: 21.0.0.0/8", g)
	}
	if got := growthPrefixes(g, "cat-a"); len(got) != 0 {
		t.Fatalf("growth cat-a = %v, want none", got)
	}
}

// addAIEntries gives the feed real ai catalog entries for the prefixes and
// rebuilds the modes it's linked to, so growth rows point at announced prefixes.
func addAIEntries(t *testing.T, s *Store, feedID int64, cidrs ...string) {
	t.Helper()
	entries := make([]CatalogEntry, 0, len(cidrs))
	for i, c := range cidrs {
		entries = append(entries, CatalogEntry{Category: "ai", Service: fmt.Sprintf("svc-%d", i), CIDR: c})
	}
	ctx := context.Background()
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		if err := ReplaceCatalogEntries(ctx, tx, feedID, entries); err != nil {
			return err
		}
		return RebuildModeEntriesForFeedTx(ctx, tx, feedID)
	}); err != nil {
		t.Fatal(err)
	}
}

func mustPrefixes(cidrs ...string) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(cidrs))
	for _, c := range cidrs {
		out = append(out, netip.MustParsePrefix(c))
	}
	return out
}

// A /9 a sync adds through category B adds no route for a user who already
// announces the /8 through category A. A user who selects only B still gets it.
func TestUserFeedChangesSkipsGrowthCoveredByAnnouncedBroaderPrefix(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	both := addBlastRadiusTestUser(t, s, DefaultCatalogModeID, FilterModeGlobal, "cat-a", 1)
	onlyB := addBlastRadiusTestUser(t, s, DefaultCatalogModeID, FilterModeGlobal, "cat-b", 2)
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		return SetUserModeSelection(ctx, tx, both, DefaultCatalogModeID, []string{"cat-a", "cat-b"}, nil)
	}); err != nil {
		t.Fatal(err)
	}

	feed := newLinkedFeed(t, s, "b-feed")
	growth := syncGrowthTo(t, s, feed, []CatalogEntry{entry("cat-b", "y", "21.0.0.0/9")})
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		return RecordFeedSyncChangeTx(ctx, tx, feed, FeedSyncDiff{AddedServices: 1, AddedByCategory: map[string]int{"cat-b": 1}},
			growth, time.Now().Unix())
	}); err != nil {
		t.Fatal(err)
	}

	got, err := s.UserFeedChanges(ctx, both, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("user announcing 21/8 through cat-a was told about covered 21/9: %+v", got)
	}
	control, err := s.UserFeedChanges(ctx, onlyB, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(control) != 1 || control[0].Categories[0].AddedPrefixes != 1 {
		t.Fatalf("user selecting only cat-b should see 21/9: %+v", control)
	}
}
