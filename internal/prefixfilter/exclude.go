package prefixfilter

import (
	"net/netip"
	"sort"
)

// ExcludeSet is a normalized, per-family, address-sorted set of prefixes
// prepared for repeated subtraction. Build it once per mode rebuild and
// call Subtract for every include entry: entries that don't overlap any
// exclude prefix are detected with a binary search and returned unchanged,
// so the common case costs O(log m) per entry.
type ExcludeSet struct {
	v4 []netip.Prefix
	v6 []netip.Prefix
}

// NewExcludeSet normalizes the prefixes (mask, dedupe, drop nested) and
// splits them by family, sorted by network address. Normalized CIDRs are
// pairwise disjoint — two CIDRs either nest or don't intersect at all —
// which is what makes the sorted-run overlap scan in Subtract correct.
func NewExcludeSet(prefixes []netip.Prefix) *ExcludeSet {
	set := &ExcludeSet{}
	for _, p := range normalize(prefixes) {
		if p.Addr().Is4() {
			set.v4 = append(set.v4, p)
		} else {
			set.v6 = append(set.v6, p)
		}
	}
	sort.Slice(set.v4, func(i, j int) bool { return set.v4[i].Addr().Less(set.v4[j].Addr()) })
	sort.Slice(set.v6, func(i, j int) bool { return set.v6[i].Addr().Less(set.v6[j].Addr()) })
	return set
}

// Empty reports whether the set contains no prefixes at all.
func (e *ExcludeSet) Empty() bool {
	return len(e.v4) == 0 && len(e.v6) == 0
}

// Subtract hole-punches every overlapping exclude prefix out of prefix.
// It returns the surviving fragments and whether anything was removed;
// when changed is false the input is untouched (fragments is exactly
// [prefix.Masked()]).
func (e *ExcludeSet) Subtract(prefix netip.Prefix) (fragments []netip.Prefix, changed bool) {
	prefix = prefix.Masked()
	excludes := e.v4
	if prefix.Addr().Is6() {
		excludes = e.v6
	}

	start := prefix.Addr()
	end := lastAddr(prefix)

	// First exclude starting after the end of prefix; every overlap lies
	// strictly before idx. The excludes are disjoint and sorted by start,
	// so their ends are sorted too and the overlapping ones form a
	// contiguous run ending at idx-1.
	idx := sort.Search(len(excludes), func(i int) bool {
		return end.Less(excludes[i].Addr())
	})

	fragments = []netip.Prefix{prefix}
	for i := idx - 1; i >= 0; i-- {
		if lastAddr(excludes[i]).Less(start) {
			break
		}
		next := fragments[:0:0]
		for _, f := range fragments {
			next = append(next, subtract(f, excludes[i])...)
		}
		fragments = next
		changed = true
		if len(fragments) == 0 {
			return nil, true
		}
	}
	return fragments, changed
}

// lastAddr returns the highest address covered by the prefix.
func lastAddr(p netip.Prefix) netip.Addr {
	raw := p.Masked().Addr().AsSlice()
	for bit := p.Bits(); bit < len(raw)*8; bit++ {
		raw[bit/8] |= byte(1 << (7 - bit%8))
	}
	addr, _ := netip.AddrFromSlice(raw) //nolint:errcheck // raw came from AsSlice, always valid
	return addr
}
