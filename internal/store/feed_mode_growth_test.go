package store

import (
	"net/netip"
	"testing"
)

func snapshotOf(mode int64, category string, prefixes ...string) ModePrefixCategories {
	m := map[int64]*prefixAssociation{}
	for i, p := range prefixes {
		m[int64(i+1)] = &prefixAssociation{prefix: netip.MustParsePrefix(p), categories: map[string]bool{category: true}}
	}
	return ModePrefixCategories{mode: m}
}

// A /9 inside an existing /8 of the same category adds no route, so it isn't growth.
func TestModeGrowthIgnoresPrefixCoveredByBroaderOne(t *testing.T) {
	before := snapshotOf(1, "ai", "10.0.0.0/8")
	after := snapshotOf(1, "ai", "10.0.0.0/8", "10.0.0.0/9")
	if g := ModeGrowth(before, after); len(g) != 0 {
		t.Fatalf("ModeGrowth = %+v, want none: 10.0.0.0/9 is covered by 10.0.0.0/8", g)
	}
}

// A broader prefix that arrives over an existing narrower one covers it; the
// broader prefix is the growth.
func TestModeGrowthBroaderArrivalIsGrowth(t *testing.T) {
	before := snapshotOf(1, "ai", "10.0.0.0/9")
	after := snapshotOf(1, "ai", "10.0.0.0/9", "10.0.0.0/8")
	g := ModeGrowth(before, after)
	if len(g) != 1 || len(g[0].Prefixes) != 1 || g[0].Prefixes[0].String() != "10.0.0.0/8" {
		t.Fatalf("ModeGrowth = %+v, want 10.0.0.0/8 as growth", g)
	}
}
