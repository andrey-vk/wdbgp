package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// TestGenerateCommunitiesHandlesMultiServiceCategoriesAndIsIdempotent covers
// the refactor that replaced genCommunitiesRuntime's per-category N+1
// service query with a single batched (category, service) query, and
// removed GenerateCommunities' separate "existing" pre-check query in favor
// of reusing the keyComm map already built from catalog_communities. Two
// categories with multiple services each exercise the batching/grouping
// logic; a second GenerateCommunities call verifies the existing-community
// check still correctly skips everything instead of regenerating or
// duplicating.
func TestGenerateCommunitiesHandlesMultiServiceCategoriesAndIsIdempotent(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	feedID, err := s.AddFeed(ctx, "multi-svc-feed", "https://example.test/feed.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatalf("add feed: %v", err)
	}
	if _, err := s.DB.ExecContext(ctx,
		"INSERT INTO catalog_mode_feeds(mode_id, feed_id) VALUES (1, ?)", feedID); err != nil {
		t.Fatalf("assign feed to mode: %v", err)
	}
	if err := s.InsertCatalogEntries(ctx, feedID, []CatalogEntry{
		{Category: "cat-a", Service: "svc-1", CIDR: "10.0.0.0/24"},
		{Category: "cat-a", Service: "svc-2", CIDR: "10.0.1.0/24"},
		{Category: "cat-a", Service: "svc-3", CIDR: "10.0.2.0/24"},
		{Category: "cat-b", Service: "svc-1", CIDR: "10.1.0.0/24"},
		{Category: "cat-b", Service: "svc-2", CIDR: "10.1.1.0/24"},
	}); err != nil {
		t.Fatalf("insert catalog entries: %v", err)
	}

	generated, err := s.GenerateCommunities(ctx, 1)
	if err != nil {
		t.Fatalf("GenerateCommunities: %v", err)
	}
	const wantGenerated = 7 // 2 group + 5 service communities
	if generated != wantGenerated {
		t.Fatalf("generated = %d, want %d", generated, wantGenerated)
	}

	comms, err := s.GetCommunities(ctx, 1)
	if err != nil {
		t.Fatalf("GetCommunities: %v", err)
	}
	wantKeys := []string{
		"cat-a", "cat-a|svc-1", "cat-a|svc-2", "cat-a|svc-3",
		"cat-b", "cat-b|svc-1", "cat-b|svc-2",
	}
	for _, k := range wantKeys {
		if _, ok := comms[k]; !ok {
			t.Errorf("missing community for %q", k)
		}
	}
	if len(comms) != len(wantKeys) {
		t.Errorf("got %d communities, want %d", len(comms), len(wantKeys))
	}

	generatedAgain, err := s.GenerateCommunities(ctx, 1)
	if err != nil {
		t.Fatalf("second GenerateCommunities: %v", err)
	}
	if generatedAgain != 0 {
		t.Fatalf("second GenerateCommunities generated %d, want 0 (idempotent)", generatedAgain)
	}

	commsAgain, err := s.GetCommunities(ctx, 1)
	if err != nil {
		t.Fatalf("GetCommunities after second generate: %v", err)
	}
	for k, v := range comms {
		if commsAgain[k] != v {
			t.Errorf("community for %q changed from %d to %d after second generate", k, v, commsAgain[k])
		}
	}
}

// TestPreviewCommunityResetRollsBack covers the preview contract: it must
// report exactly the renumbering a reset would perform, and must not persist
// the trial regeneration it runs to find that out.
func TestPreviewCommunityResetRollsBack(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	feedID, err := s.AddFeed(ctx, "preview-feed", "https://example.test/f.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatalf("add feed: %v", err)
	}
	if _, err := s.DB.ExecContext(ctx,
		"INSERT INTO catalog_mode_feeds(mode_id, feed_id) VALUES (1, ?)", feedID); err != nil {
		t.Fatalf("assign feed to mode: %v", err)
	}
	if err := s.InsertCatalogEntries(ctx, feedID, []CatalogEntry{
		{Category: "cat-a", Service: "svc-1", CIDR: "10.0.0.0/24"},
	}); err != nil {
		t.Fatalf("insert entries: %v", err)
	}
	if _, err := s.GenerateCommunities(ctx, 1); err != nil {
		t.Fatalf("generate: %v", err)
	}

	// Override one value so a reset has something to undo.
	const custom = 54321
	if err := s.SetCommunity(ctx, 1, "cat-a", "svc-1", custom); err != nil {
		t.Fatalf("set community: %v", err)
	}

	changes, digest, err := s.PreviewCommunityReset(ctx, 1)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if digest == "" {
		t.Fatal("preview returned an empty digest")
	}
	var found *CommunityChange
	for i := range changes {
		if changes[i].Category == "cat-a" && changes[i].Service == "svc-1" {
			found = &changes[i]
		}
	}
	if found == nil {
		t.Fatalf("preview did not report the overridden pair: %+v", changes)
	}
	if found.Old != custom {
		t.Fatalf("preview old = %d, want %d", found.Old, custom)
	}
	if found.New == custom {
		t.Fatal("preview new equals the override; a reset would change nothing")
	}
	for _, change := range changes {
		if change.Old == change.New {
			t.Fatalf("preview listed an unchanged pair: %+v", change)
		}
	}

	// The override must still be in place — the preview wrote nothing.
	after, err := s.GetCommunities(ctx, 1)
	if err != nil {
		t.Fatalf("read communities: %v", err)
	}
	if after["cat-a|svc-1"] != custom {
		t.Fatalf("stored community = %d after preview, want %d", after["cat-a|svc-1"], custom)
	}

	// A stale digest must be rejected without writing anything.
	if _, err := s.ResetCommunities(ctx, 1, "wrong-digest"); !errors.Is(err, ErrCommunityResetStale) {
		t.Fatalf("reset with wrong digest: err = %v, want ErrCommunityResetStale", err)
	}
	stillCustom, err := s.GetCommunities(ctx, 1)
	if err != nil {
		t.Fatalf("read communities after rejected reset: %v", err)
	}
	if stillCustom["cat-a|svc-1"] != custom {
		t.Fatalf("stored community = %d after rejected reset, want %d", stillCustom["cat-a|svc-1"], custom)
	}

	// An actual reset, given the digest the preview issued, must land on
	// exactly what the preview predicted.
	if _, err := s.ResetCommunities(ctx, 1, digest); err != nil {
		t.Fatalf("reset: %v", err)
	}
	applied, err := s.GetCommunities(ctx, 1)
	if err != nil {
		t.Fatalf("read communities after reset: %v", err)
	}
	if applied["cat-a|svc-1"] != found.New {
		t.Fatalf("reset produced %d, preview predicted %d",
			applied["cat-a|svc-1"], found.New)
	}
}

// TestResetCommunitiesRejectsStaleDigestAfterConcurrentChange covers the race
// a preview alone cannot close: an operator opens the preview, then (a feed
// sync, or another admin) changes the mode's catalog before Apply is clicked.
// The confirm must be rejected — applying it would renumber a state the
// operator never actually reviewed — rather than silently reset whatever the
// mode happens to look like by the time confirm arrives.
func TestResetCommunitiesRejectsStaleDigestAfterConcurrentChange(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	feedID, err := s.AddFeed(ctx, "race-feed", "https://example.test/r.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatalf("add feed: %v", err)
	}
	if _, err := s.DB.ExecContext(ctx,
		"INSERT INTO catalog_mode_feeds(mode_id, feed_id) VALUES (1, ?)", feedID); err != nil {
		t.Fatalf("assign feed to mode: %v", err)
	}
	if err := s.InsertCatalogEntries(ctx, feedID, []CatalogEntry{
		{Category: "cat-a", Service: "svc-1", CIDR: "10.0.0.0/24"},
	}); err != nil {
		t.Fatalf("insert entries: %v", err)
	}
	if _, err := s.GenerateCommunities(ctx, 1); err != nil {
		t.Fatalf("generate: %v", err)
	}

	_, digest, err := s.PreviewCommunityReset(ctx, 1)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}

	// Simulate a feed sync landing a new service (and its community, as the
	// real sync path does via GenerateCommunities) after the preview.
	if err := s.InsertCatalogEntries(ctx, feedID, []CatalogEntry{
		{Category: "cat-a", Service: "svc-1", CIDR: "10.0.0.0/24"},
		{Category: "cat-a", Service: "svc-2", CIDR: "10.0.1.0/24"},
	}); err != nil {
		t.Fatalf("insert additional entry: %v", err)
	}
	if _, err := s.GenerateCommunities(ctx, 1); err != nil {
		t.Fatalf("generate after change: %v", err)
	}

	before, err := s.GetCommunities(ctx, 1)
	if err != nil {
		t.Fatalf("read communities before stale reset attempt: %v", err)
	}

	if _, err := s.ResetCommunities(ctx, 1, digest); !errors.Is(err, ErrCommunityResetStale) {
		t.Fatalf("reset with pre-change digest: err = %v, want ErrCommunityResetStale", err)
	}

	after, err := s.GetCommunities(ctx, 1)
	if err != nil {
		t.Fatalf("read communities after rejected stale reset: %v", err)
	}
	for key, value := range before {
		if after[key] != value {
			t.Fatalf("rejected stale reset still changed %q: %d -> %d", key, value, after[key])
		}
	}
}

// TestModeCommunitySnapshotIsSelfConsistent covers the atomicity guarantee
// the export relies on: catalog, communities, and prefix counts must all
// reflect the same point in time, with no entry whose community is missing
// because it was added after generation ran but before the read did. This
// reproduces the intermediate state a feed sync leaves between publishing
// its catalog and generating communities for it (two separate transactions
// in the real sync path) and asserts the snapshot closes that gap by
// generating inside its own transaction rather than relying on a prior
// caller having done so.
func TestModeCommunitySnapshotIsSelfConsistent(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	feedID, err := s.AddFeed(ctx, "snapshot-feed", "https://example.test/s.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatalf("add feed: %v", err)
	}
	if _, err := s.DB.ExecContext(ctx,
		"INSERT INTO catalog_mode_feeds(mode_id, feed_id) VALUES (1, ?)", feedID); err != nil {
		t.Fatalf("assign feed to mode: %v", err)
	}
	if err := s.InsertCatalogEntries(ctx, feedID, []CatalogEntry{
		{Category: "cat-a", Service: "svc-1", CIDR: "10.1.0.0/24"},
	}); err != nil {
		t.Fatalf("insert entries: %v", err)
	}
	// Publish the catalog without generating communities for it — exactly
	// the state a request can observe mid-sync.
	if err := s.RebuildModeEntries(ctx, 1); err != nil {
		t.Fatalf("rebuild mode entries: %v", err)
	}

	snap, err := s.ModeCommunitySnapshot(ctx, 1)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	services, ok := snap.Catalog["cat-a"]
	if !ok || len(services) != 1 || services[0] != "svc-1" {
		t.Fatalf("snapshot catalog missing cat-a/svc-1: %+v", snap.Catalog)
	}
	var groupComm, svcComm uint32
	for _, row := range snap.Communities {
		switch {
		case row.Category == "cat-a" && row.Service == "":
			groupComm = row.Community
		case row.Category == "cat-a" && row.Service == "svc-1":
			svcComm = row.Community
		}
	}
	if groupComm == 0 || svcComm == 0 {
		t.Fatalf("snapshot returned catalog without matching assignments: group=%d service=%d",
			groupComm, svcComm)
	}
	if snap.CategoryPrefixV4["cat-a"] != 1 {
		t.Fatalf("category prefix count = %d, want 1", snap.CategoryPrefixV4["cat-a"])
	}
	if snap.ServicePrefixV4["cat-a"]["svc-1"] != 1 {
		t.Fatalf("service prefix count = %d, want 1", snap.ServicePrefixV4["cat-a"]["svc-1"])
	}

	// A second call must be a no-op on top of what the first one generated.
	snap2, err := s.ModeCommunitySnapshot(ctx, 1)
	if err != nil {
		t.Fatalf("second snapshot: %v", err)
	}
	var groupComm2 uint32
	for _, row := range snap2.Communities {
		if row.Category == "cat-a" && row.Service == "" {
			groupComm2 = row.Community
		}
	}
	if groupComm2 != groupComm {
		t.Fatalf("second snapshot renumbered cat-a: got %d, want %d (idempotent)", groupComm2, groupComm)
	}
}

// TestAllModeCommunitySnapshotsIsConsistentAcrossModes covers the cross-mode
// atomicity guarantee AllModeCommunitySnapshots adds over calling
// ModeCommunitySnapshot once per mode: a feed sync whose catalog update
// spans multiple modes must never be observed as committed for one mode but
// not yet for another, even though each mode's own read is itself already
// transactionally consistent (ModeCommunitySnapshot's own guarantee). Two
// modes share one feed; a concurrent writer repeatedly republishes that
// feed with one more service and rebuilds both modes, while reads run in a
// tight loop against the real file-backed, WAL-mode database the other
// store tests use — every read must see the same service count in both
// modes, never a split.
func TestAllModeCommunitySnapshotsIsConsistentAcrossModes(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	feedID, err := s.AddFeed(ctx, "shared-feed", "https://example.test/shared.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatalf("add feed: %v", err)
	}
	modeB, err := s.AddCatalogMode(ctx, "mode-b", true)
	if err != nil {
		t.Fatalf("add mode: %v", err)
	}
	modeIDs := []int64{1, modeB}
	for _, modeID := range modeIDs {
		if _, err := s.DB.ExecContext(ctx,
			"INSERT INTO catalog_mode_feeds(mode_id, feed_id) VALUES (?, ?)", modeID, feedID); err != nil {
			t.Fatalf("assign feed to mode %d: %v", modeID, err)
		}
	}
	if err := s.InsertCatalogEntries(ctx, feedID, []CatalogEntry{
		{Category: "cat", Service: "svc-1", CIDR: "10.0.0.0/24"},
	}); err != nil {
		t.Fatalf("insert entries: %v", err)
	}
	for _, modeID := range modeIDs {
		if err := s.RebuildModeEntries(ctx, modeID); err != nil {
			t.Fatalf("rebuild mode entries for %d: %v", modeID, err)
		}
		if _, err := s.GenerateCommunities(ctx, modeID); err != nil {
			t.Fatalf("generate for %d: %v", modeID, err)
		}
	}

	stop := make(chan struct{})
	writerErr := make(chan error, 1)
	go func() {
		defer close(writerErr)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			entries := []CatalogEntry{{Category: "cat", Service: "svc-1", CIDR: "10.0.0.0/24"}}
			for j := 0; j <= i; j++ {
				entries = append(entries, CatalogEntry{
					Category: "cat",
					Service:  fmt.Sprintf("svc-new-%d", j),
					CIDR:     fmt.Sprintf("10.1.%d.0/24", j%256),
				})
			}
			if err := s.InsertCatalogEntries(ctx, feedID, entries); err != nil {
				writerErr <- fmt.Errorf("insert entries: %w", err)
				return
			}
			// Republishing both modes, one after the other, is exactly what
			// leaves the gap AllModeCommunitySnapshots closes: under the old
			// one-transaction-per-mode code, a reader could land between
			// these two calls and see the update for mode 1 but not yet for
			// modeB.
			for _, modeID := range modeIDs {
				if err := s.RebuildModeEntries(ctx, modeID); err != nil {
					writerErr <- fmt.Errorf("rebuild mode %d: %w", modeID, err)
					return
				}
				if _, err := s.GenerateCommunities(ctx, modeID); err != nil {
					writerErr <- fmt.Errorf("generate mode %d: %w", modeID, err)
					return
				}
			}
		}
	}()

	const iterations = 150
	for i := 0; i < iterations; i++ {
		_, snapshots, err := s.AllModeCommunitySnapshots(ctx, false)
		if err != nil {
			close(stop)
			<-writerErr
			t.Fatalf("snapshot: %v", err)
		}
		count1 := len(snapshots[1].Catalog["cat"])
		count2 := len(snapshots[modeB].Catalog["cat"])
		if count1 != count2 {
			close(stop)
			<-writerErr
			t.Fatalf("iteration %d: mode 1 saw %d services, mode %d saw %d — snapshot split across modes",
				i, count1, modeB, count2)
		}
	}
	close(stop)
	if err := <-writerErr; err != nil {
		t.Fatalf("writer: %v", err)
	}
}
