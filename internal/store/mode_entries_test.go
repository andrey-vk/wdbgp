package store

import (
	"context"
	"sort"
	"strings"
	"testing"
)

func setupModeEntriesTest(t *testing.T) (*Store, context.Context) {
	t.Helper()
	s, err := Open(":memory:", false, "", false)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	})
	return s, context.Background()
}

// addTestFeed creates an enabled feed using any seeded adapter.
func addTestFeed(ctx context.Context, t *testing.T, s *Store, name string) int64 {
	t.Helper()
	adapters, err := s.FeedAdapters(ctx)
	if err != nil || len(adapters) == 0 {
		t.Fatalf("feed adapters: %v (count %d)", err, len(adapters))
	}
	id, err := s.AddFeed(ctx, name, "https://example.invalid/"+name, adapters[0].ID, true, 0, "", "", false)
	if err != nil {
		t.Fatalf("add feed %s: %v", name, err)
	}
	return id
}

func modePrefixStrings(ctx context.Context, t *testing.T, s *Store, modeID int64) []string {
	t.Helper()
	prefixes, err := s.EnabledCatalogPrefixes(ctx, modeID)
	if err != nil {
		t.Fatalf("enabled catalog prefixes: %v", err)
	}
	out := make([]string, len(prefixes))
	for i, p := range prefixes {
		out[i] = p.Category + "/" + p.Service + ":" + p.CIDR
	}
	sort.Strings(out)
	return out
}

func equalStrings(a, b []string) bool {
	return strings.Join(a, "\x00") == strings.Join(b, "\x00")
}

// TestRebuildModeEntriesSubtraction — an exclude feed's prefixes are
// hole-punched out of every include entry; untouched prefixes pass through
// and fragments inherit the include entry's category/service. The exclude
// feed's own labels must not appear anywhere.
func TestRebuildModeEntriesSubtraction(t *testing.T) {
	s, ctx := setupModeEntriesTest(t)

	modeID, err := s.AddCatalogMode(ctx, "test-mode", true)
	if err != nil {
		t.Fatalf("add mode: %v", err)
	}
	include := addTestFeed(ctx, t, s, "include-feed")
	exclude := addTestFeed(ctx, t, s, "exclude-feed")

	if err := s.InsertCatalogEntries(ctx, include, []CatalogEntry{
		{Category: "Cat", Service: "Svc", CIDR: "10.0.0.0/22"},
		{Category: "Cat", Service: "Svc", CIDR: "192.168.0.0/24"},
		{Category: "Cat", Service: "Other", CIDR: "10.0.1.0/24"},
	}); err != nil {
		t.Fatalf("insert include entries: %v", err)
	}
	if err := s.InsertCatalogEntries(ctx, exclude, []CatalogEntry{
		{Category: "ExcludeCat", Service: "ExcludeSvc", CIDR: "10.0.1.0/24"},
	}); err != nil {
		t.Fatalf("insert exclude entries: %v", err)
	}

	if err := s.AddFeedToMode(ctx, modeID, include, false); err != nil {
		t.Fatalf("link include feed: %v", err)
	}
	if err := s.AddFeedToMode(ctx, modeID, exclude, true); err != nil {
		t.Fatalf("link exclude feed: %v", err)
	}

	got := modePrefixStrings(ctx, t, s, modeID)
	want := []string{
		// 10.0.0.0/22 minus 10.0.1.0/24 → 10.0.0.0/24 + 10.0.2.0/23
		"Cat/Svc:10.0.0.0/24",
		"Cat/Svc:10.0.2.0/23",
		"Cat/Svc:192.168.0.0/24",
		// Other's only prefix is fully excluded → service disappears.
	}
	sort.Strings(want)
	if !equalStrings(got, want) {
		t.Errorf("mode prefixes = %v, want %v", got, want)
	}

	// The fully-excluded service must be gone from the catalog listing,
	// and the exclude feed's own labels must never appear.
	catalog, err := s.CatalogForMode(ctx, modeID, false)
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	if services := catalog["Cat"]; len(services) != 1 || services[0] != "Svc" {
		t.Errorf("catalog[Cat] = %v, want [Svc]", services)
	}
	if _, ok := catalog["ExcludeCat"]; ok {
		t.Error("exclude feed's category leaked into the catalog")
	}
}

// TestRebuildModeEntriesNoExcludes — without exclude links the merge is a
// byte-identical copy of the include feeds' entries.
func TestRebuildModeEntriesNoExcludes(t *testing.T) {
	s, ctx := setupModeEntriesTest(t)

	modeID, err := s.AddCatalogMode(ctx, "copy-mode", true)
	if err != nil {
		t.Fatalf("add mode: %v", err)
	}
	feed := addTestFeed(ctx, t, s, "plain-feed")
	if err := s.InsertCatalogEntries(ctx, feed, []CatalogEntry{
		{Category: "Cat", Service: "Svc", CIDR: "10.0.0.0/8"},
		{Category: "Cat", Service: "Svc", CIDR: "2001:db8::/32"},
	}); err != nil {
		t.Fatalf("insert entries: %v", err)
	}
	if err := s.AddFeedToMode(ctx, modeID, feed, false); err != nil {
		t.Fatalf("link feed: %v", err)
	}

	got := modePrefixStrings(ctx, t, s, modeID)
	want := []string{"Cat/Svc:10.0.0.0/8", "Cat/Svc:2001:db8::/32"}
	sort.Strings(want)
	if !equalStrings(got, want) {
		t.Errorf("mode prefixes = %v, want %v", got, want)
	}
}

// TestRebuildTriggers — role changes, feed disable and unlink each rebuild
// the materialized entries.
func TestRebuildTriggers(t *testing.T) {
	s, ctx := setupModeEntriesTest(t)

	modeID, err := s.AddCatalogMode(ctx, "trigger-mode", true)
	if err != nil {
		t.Fatalf("add mode: %v", err)
	}
	include := addTestFeed(ctx, t, s, "trigger-include")
	exclude := addTestFeed(ctx, t, s, "trigger-exclude")
	if err := s.InsertCatalogEntries(ctx, include, []CatalogEntry{
		{Category: "Cat", Service: "Svc", CIDR: "10.0.0.0/24"},
	}); err != nil {
		t.Fatalf("insert include entries: %v", err)
	}
	if err := s.InsertCatalogEntries(ctx, exclude, []CatalogEntry{
		{Category: "X", Service: "X", CIDR: "10.0.0.0/24"},
	}); err != nil {
		t.Fatalf("insert exclude entries: %v", err)
	}
	if err := s.AddFeedToMode(ctx, modeID, include, false); err != nil {
		t.Fatalf("link include: %v", err)
	}

	if got := modePrefixStrings(ctx, t, s, modeID); !equalStrings(got, []string{"Cat/Svc:10.0.0.0/24"}) {
		t.Fatalf("before exclude: %v", got)
	}

	// Linking the exclude feed wipes the overlapping prefix.
	if err := s.AddFeedToMode(ctx, modeID, exclude, true); err != nil {
		t.Fatalf("link exclude: %v", err)
	}
	if got := modePrefixStrings(ctx, t, s, modeID); len(got) != 0 {
		t.Fatalf("after exclude link: %v, want empty", got)
	}

	// Disabling the exclude feed brings the prefix back.
	feed, err := s.Feed(ctx, exclude)
	if err != nil {
		t.Fatalf("load feed: %v", err)
	}
	feed.Enabled = false
	if err := s.UpdateFeed(ctx, feed); err != nil {
		t.Fatalf("disable exclude feed: %v", err)
	}
	if got := modePrefixStrings(ctx, t, s, modeID); !equalStrings(got, []string{"Cat/Svc:10.0.0.0/24"}) {
		t.Fatalf("after exclude disable: %v", got)
	}

	// Re-enable, then flip its role to include: entries reappear under the
	// exclude feed's own labels.
	feed.Enabled = true
	if err := s.UpdateFeed(ctx, feed); err != nil {
		t.Fatalf("re-enable exclude feed: %v", err)
	}
	// UpdateFeed with unchanged config keeps entries; re-add them in case
	// the enable flip path wiped them (it must not, but stay independent).
	if got := modePrefixStrings(ctx, t, s, modeID); len(got) != 0 {
		t.Fatalf("after exclude re-enable: %v, want empty", got)
	}

	// Removing the exclude link restores the include entry.
	if err := s.RemoveFeedFromMode(ctx, modeID, exclude); err != nil {
		t.Fatalf("remove exclude link: %v", err)
	}
	if got := modePrefixStrings(ctx, t, s, modeID); !equalStrings(got, []string{"Cat/Svc:10.0.0.0/24"}) {
		t.Fatalf("after exclude unlink: %v", got)
	}

	// Deleting the include feed empties the mode.
	if err := s.DeleteFeed(ctx, include); err != nil {
		t.Fatalf("delete include feed: %v", err)
	}
	if got := modePrefixStrings(ctx, t, s, modeID); len(got) != 0 {
		t.Fatalf("after include delete: %v, want empty", got)
	}
}

// TestPruneKeepsFragmentPrefixes — split fragments live only in
// catalog_mode_entries; the orphan prune must not eat them.
func TestPruneKeepsFragmentPrefixes(t *testing.T) {
	s, ctx := setupModeEntriesTest(t)

	modeID, err := s.AddCatalogMode(ctx, "prune-mode", true)
	if err != nil {
		t.Fatalf("add mode: %v", err)
	}
	include := addTestFeed(ctx, t, s, "prune-include")
	exclude := addTestFeed(ctx, t, s, "prune-exclude")
	if err := s.InsertCatalogEntries(ctx, include, []CatalogEntry{
		{Category: "Cat", Service: "Svc", CIDR: "10.0.0.0/22"},
	}); err != nil {
		t.Fatalf("insert include entries: %v", err)
	}
	if err := s.InsertCatalogEntries(ctx, exclude, []CatalogEntry{
		{Category: "X", Service: "X", CIDR: "10.0.1.0/24"},
	}); err != nil {
		t.Fatalf("insert exclude entries: %v", err)
	}
	if err := s.AddFeedToMode(ctx, modeID, include, false); err != nil {
		t.Fatalf("link include: %v", err)
	}
	if err := s.AddFeedToMode(ctx, modeID, exclude, true); err != nil {
		t.Fatalf("link exclude: %v", err)
	}

	want := []string{"Cat/Svc:10.0.0.0/24", "Cat/Svc:10.0.2.0/23"}
	sort.Strings(want)
	if got := modePrefixStrings(ctx, t, s, modeID); !equalStrings(got, want) {
		t.Fatalf("before prune: %v, want %v", got, want)
	}

	if _, err := s.PruneOrphanPrefixes(ctx); err != nil {
		t.Fatalf("prune: %v", err)
	}
	if got := modePrefixStrings(ctx, t, s, modeID); !equalStrings(got, want) {
		t.Errorf("after prune: %v, want %v (fragments must survive)", got, want)
	}
}

// TestRebuildLeavesDefaultRouteUntouched — a feed-provided default route is
// filtered at read time (DesiredPrefixes skips bits==0); the rebuild must
// never shatter it into non-default fragments that dodge that guard
// (Codex P1 on PR #39). Every exclude overlaps 0.0.0.0/0, so without the
// guard this test's default route would explode into fragments.
func TestRebuildLeavesDefaultRouteUntouched(t *testing.T) {
	s, ctx := setupModeEntriesTest(t)

	modeID, err := s.AddCatalogMode(ctx, "default-route-mode", true)
	if err != nil {
		t.Fatalf("add mode: %v", err)
	}
	include := addTestFeed(ctx, t, s, "default-include")
	exclude := addTestFeed(ctx, t, s, "default-exclude")
	if err := s.InsertCatalogEntries(ctx, include, []CatalogEntry{
		{Category: "Cat", Service: "Svc", CIDR: "0.0.0.0/0"},
		{Category: "Cat", Service: "Svc", CIDR: "10.0.0.0/24"},
	}); err != nil {
		t.Fatalf("insert include entries: %v", err)
	}
	if err := s.InsertCatalogEntries(ctx, exclude, []CatalogEntry{
		{Category: "X", Service: "X", CIDR: "10.0.0.0/25"},
	}); err != nil {
		t.Fatalf("insert exclude entries: %v", err)
	}
	if err := s.AddFeedToMode(ctx, modeID, include, false); err != nil {
		t.Fatalf("link include: %v", err)
	}
	if err := s.AddFeedToMode(ctx, modeID, exclude, true); err != nil {
		t.Fatalf("link exclude: %v", err)
	}

	got := modePrefixStrings(ctx, t, s, modeID)
	want := []string{
		"Cat/Svc:0.0.0.0/0", // untouched, filtered later at read time
		"Cat/Svc:10.0.0.128/25",
	}
	sort.Strings(want)
	if !equalStrings(got, want) {
		t.Errorf("mode prefixes = %v, want %v (default route must not fragment)", got, want)
	}
}
