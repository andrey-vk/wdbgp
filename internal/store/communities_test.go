package store

import (
	"context"
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

	changes, err := s.PreviewCommunityReset(ctx, 1)
	if err != nil {
		t.Fatalf("preview: %v", err)
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

	// An actual reset must land on exactly what the preview predicted.
	if _, err := s.ResetCommunities(ctx, 1); err != nil {
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
