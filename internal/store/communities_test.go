package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
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

	// Bounded, constant-size writes (toggling between one and two services)
	// paced with a small sleep: enough churn to create real race windows
	// without the write volume or contention growing with the iteration
	// count, which produced enough SQLITE_BUSY pressure on slower/shared CI
	// runners to exhaust the store's own retry budget on an unrelated,
	// infra-induced delay rather than the correctness property under test.
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
			if i%2 == 1 {
				entries = append(entries, CatalogEntry{Category: "cat", Service: "svc-2", CIDR: "10.1.0.0/24"})
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
			time.Sleep(time.Millisecond)
		}
	}()

	// Retries a transient SQLITE_BUSY the same way production callers of
	// Transaction already do (retry.DatabaseConfig) — this hedges against
	// CI-specific contention delays, not against the invariant below, which
	// is checked identically regardless of how many attempts it took.
	readSnapshot := func() (map[int64]ModeCommunitySnapshot, error) {
		var snapshots map[int64]ModeCommunitySnapshot
		var err error
		for attempt := 0; attempt < 5; attempt++ {
			_, snapshots, err = s.AllModeCommunitySnapshots(ctx, false)
			if err == nil || !strings.Contains(err.Error(), "database is locked") {
				return snapshots, err
			}
			time.Sleep(10 * time.Millisecond)
		}
		return snapshots, err
	}

	const iterations = 60
	for i := 0; i < iterations; i++ {
		snapshots, err := readSnapshot()
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

// TestGenerateCommunitiesHandlesPipeInCategoryName covers a real collision
// in genCommunitiesRuntime's in-memory bookkeeping: a category literally
// containing "|" and an unrelated (category, service) pair can join to the
// same "category|service" string — e.g. (category "a", service "b|c") and
// (category "a|b", service "c") both joined to "a|b|c" under the old
// string-keyed map. generateCommunitiesRuntime would then mistake the new
// pair for the existing one already having a community, silently skip
// allocating one for it, and leave it unassigned (community 0) forever.
func TestGenerateCommunitiesHandlesPipeInCategoryName(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	feedID, err := s.AddFeed(ctx, "pipe-gen-feed", "https://example.test/pipe-gen.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatalf("add feed: %v", err)
	}
	if _, err := s.DB.ExecContext(ctx,
		"INSERT INTO catalog_mode_feeds(mode_id, feed_id) VALUES (1, ?)", feedID); err != nil {
		t.Fatalf("assign feed to mode: %v", err)
	}

	// First sync: only the pre-existing pair.
	if err := s.InsertCatalogEntries(ctx, feedID, []CatalogEntry{
		{Category: "a", Service: "b|c", CIDR: "10.0.0.0/24"},
	}); err != nil {
		t.Fatalf("insert initial entries: %v", err)
	}
	if _, err := s.GenerateCommunities(ctx, 1); err != nil {
		t.Fatalf("initial generate: %v", err)
	}
	before, err := s.CommunityRows(ctx, 1)
	if err != nil {
		t.Fatalf("read communities: %v", err)
	}
	var existingSvcComm uint32
	for _, row := range before {
		if row.Category == "a" && row.Service == "b|c" {
			existingSvcComm = row.Community
		}
	}
	if existingSvcComm == 0 {
		t.Fatal("fixture did not generate a community for (a, b|c)")
	}

	// Second sync: a feed resync replaces the whole catalog, so the new
	// entry is added alongside the pre-existing one, exactly like a real
	// feed update.
	if err := s.InsertCatalogEntries(ctx, feedID, []CatalogEntry{
		{Category: "a", Service: "b|c", CIDR: "10.0.0.0/24"},
		{Category: "a|b", Service: "c", CIDR: "10.0.1.0/24"},
	}); err != nil {
		t.Fatalf("insert second entries: %v", err)
	}
	if _, err := s.GenerateCommunities(ctx, 1); err != nil {
		t.Fatalf("second generate: %v", err)
	}

	after, err := s.CommunityRows(ctx, 1)
	if err != nil {
		t.Fatalf("read communities after second generate: %v", err)
	}
	var newGroupComm, newSvcComm, stillExistingSvcComm uint32
	var foundNewGroup, foundNewSvc bool
	for _, row := range after {
		switch {
		case row.Category == "a|b" && row.Service == "":
			newGroupComm = row.Community
			foundNewGroup = true
		case row.Category == "a|b" && row.Service == "c":
			newSvcComm = row.Community
			foundNewSvc = true
		case row.Category == "a" && row.Service == "b|c":
			stillExistingSvcComm = row.Community
		}
	}
	if !foundNewGroup || newGroupComm == 0 {
		t.Fatalf("new category a|b got no group community: found=%v value=%d", foundNewGroup, newGroupComm)
	}
	if !foundNewSvc || newSvcComm == 0 {
		t.Fatalf("new pair (a|b, c) got no community — mistaken for the existing (a, b|c) pair: found=%v value=%d",
			foundNewSvc, newSvcComm)
	}
	if newSvcComm == existingSvcComm {
		t.Fatalf("new pair (a|b, c) collided with existing (a, b|c): both got community %d", existingSvcComm)
	}
	if stillExistingSvcComm != existingSvcComm {
		t.Fatalf("existing (a, b|c) community changed from %d to %d", existingSvcComm, stillExistingSvcComm)
	}
}

// TestResetDigestDoesNotCollideAcrossModes covers a real cross-mode
// confusion: two modes backed by identical feeds can legitimately converge
// to the exact same category/service/community rows. If the digest only
// covered those rows (not which mode they belong to), a preview computed
// for mode A would also validate a confirm request against mode B, applying
// a reset the operator reviewed for a different mode than the one it
// actually got applied to.
func TestResetDigestDoesNotCollideAcrossModes(t *testing.T) {
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
		{Category: "cat-a", Service: "svc-1", CIDR: "10.0.0.0/24"},
		{Category: "cat-a", Service: "svc-2", CIDR: "10.0.1.0/24"},
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

	// Both modes share the same feed and were generated from a clean slate,
	// so their allocator output is identical — confirming the premise this
	// bug depends on, not just assuming it.
	commsA, err := s.GetCommunities(ctx, 1)
	if err != nil {
		t.Fatalf("read mode 1 communities: %v", err)
	}
	commsB, err := s.GetCommunities(ctx, modeB)
	if err != nil {
		t.Fatalf("read mode %d communities: %v", modeB, err)
	}
	if len(commsA) == 0 || len(commsA) != len(commsB) {
		t.Fatalf("fixture did not converge: mode 1 = %v, mode %d = %v", commsA, modeB, commsB)
	}
	for k, v := range commsA {
		if commsB[k] != v {
			t.Fatalf("fixture did not converge on %q: mode 1 = %d, mode %d = %d", k, v, modeB, commsB[k])
		}
	}

	_, digestA, err := s.PreviewCommunityReset(ctx, 1)
	if err != nil {
		t.Fatalf("preview mode 1: %v", err)
	}
	_, digestB, err := s.PreviewCommunityReset(ctx, modeB)
	if err != nil {
		t.Fatalf("preview mode %d: %v", modeB, err)
	}
	if digestA == digestB {
		t.Fatalf("modes with identical community state produced the same digest: %q", digestA)
	}

	// Mode 1's own preview must never authorize applying to mode B.
	if _, err := s.ResetCommunities(ctx, modeB, digestA); !errors.Is(err, ErrCommunityResetStale) {
		t.Fatalf("reset mode %d with mode 1's digest: err = %v, want ErrCommunityResetStale", modeB, err)
	}

	// Its own digest must still work correctly.
	if _, err := s.ResetCommunities(ctx, modeB, digestB); err != nil {
		t.Fatalf("reset mode %d with its own digest: %v", modeB, err)
	}
}

// TestResetDigestHandlesNulByteInNames covers a real collision in a naive
// NUL-joined digest encoding: feed parsing only trims whitespace, so a
// category or service name containing a NUL byte is valid input, and two
// distinct (category, service) pairs can join to the same delimited bytes
// (category "a" service "b\x00c" vs. category "a\x00b" service "c"). If a
// feed switches between these pairs after a preview but before confirmation,
// a digest that cannot tell them apart would accept a reset the operator
// never actually reviewed. communityResetDigest must produce different
// digests for the two.
func TestResetDigestHandlesNulByteInNames(t *testing.T) {
	rowsA := []Community{{Category: "a", Service: "b\x00c", Community: 100}}
	rowsB := []Community{{Category: "a\x00b", Service: "c", Community: 100}}

	digestA := communityResetDigest(1, nil, rowsA)
	digestB := communityResetDigest(1, nil, rowsB)
	if digestA == digestB {
		t.Fatalf("distinct (category, service) pairs joining to the same NUL-delimited bytes "+
			"produced the same digest: %q", digestA)
	}
}
