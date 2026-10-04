package store

import (
	"context"
	"testing"
)

func TestBuildCommunityMatrixFlagsDivergence(t *testing.T) {
	modes := []CatalogMode{{ID: 1, Name: "Default", Enabled: true}, {ID: 2, Name: "Lab", Enabled: true}, {ID: 3, Name: "Old", Enabled: false}}
	snaps := map[int64]ModeCommunitySnapshot{
		1: {Communities: []Community{
			{Category: "ai", Service: "", Community: 10000},
			{Category: "ai", Service: "openai", Community: 10001},
			{Category: "tv", Service: "", Community: 20000},
		}},
		2: {Communities: []Community{
			{Category: "ai", Service: "", Community: 10000},
			{Category: "tv", Service: "", Community: 30000},
		}},
		3: {Communities: []Community{
			{Category: "ai", Service: "", Community: 10000},
		}},
	}
	m := buildCommunityMatrix(modes, snaps)
	if len(m.Categories) != 2 || m.Categories[0].Category != "ai" || m.Categories[1].Category != "tv" {
		t.Fatalf("categories = %+v, want ai then tv", m.Categories)
	}
	ai, tv := m.Categories[0], m.Categories[1]
	if ai.Divergent {
		t.Fatalf("ai agrees in every mode it appears in but was flagged: %+v", ai)
	}
	if ai.Values[1] == nil || *ai.Values[1] != 10000 || ai.Values[3] == nil {
		t.Fatalf("ai values = %+v, want 10000 in each mode", ai.Values)
	}
	if !tv.Divergent || *tv.Values[1] != 20000 || *tv.Values[2] != 30000 {
		t.Fatalf("tv = %+v, want divergent 20000 vs 30000", tv)
	}
}

func TestBuildCommunityMatrixFlagsMissingCategory(t *testing.T) {
	modes := []CatalogMode{{ID: 1, Name: "Default", Enabled: true}, {ID: 2, Name: "Lab", Enabled: true}}
	snaps := map[int64]ModeCommunitySnapshot{
		1: {Communities: []Community{{Category: "ai", Service: "", Community: 10000}}},
		2: {Communities: []Community{{Category: "ai", Service: "x", Community: 10001}}},
	}
	m := buildCommunityMatrix(modes, snaps)
	if len(m.Categories) != 1 {
		t.Fatalf("categories = %+v, want one", m.Categories)
	}
	row := m.Categories[0]
	if !row.Divergent || row.Values[2] != nil {
		t.Fatalf("ai in Lab has no group-level community, want nil and divergent: %+v", row)
	}
}

func TestBuildCommunityMatrixEmpty(t *testing.T) {
	m := buildCommunityMatrix(nil, nil)
	if m.Categories == nil || len(m.Categories) != 0 {
		t.Fatalf("categories = %#v, want an empty array", m.Categories)
	}
}

// TestCommunityMatrixReadsModesWithoutGenerating checks the store path: the
// seeded modes come back, and reading the matrix builds the grid without
// generating assignments, so the stored communities are unchanged by it.
func TestCommunityMatrixReadsModesWithoutGenerating(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	before, err := s.CommunityRows(ctx, DefaultCatalogModeID)
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.CommunityMatrix(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Modes) == 0 {
		t.Fatalf("modes = %+v, want the seeded catalog modes", m.Modes)
	}
	after, err := s.CommunityRows(ctx, DefaultCatalogModeID)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatalf("stored communities changed from %d to %d by reading the matrix", len(before), len(after))
	}
}
