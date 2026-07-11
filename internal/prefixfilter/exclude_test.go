package prefixfilter

import (
	"net/netip"
	"reflect"
	"sort"
	"testing"
)

func mustPrefixes(cidrs ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(cidrs))
	for i, c := range cidrs {
		out[i] = netip.MustParsePrefix(c)
	}
	return out
}

func sortedStrings(prefixes []netip.Prefix) []string {
	out := make([]string, len(prefixes))
	for i, p := range prefixes {
		out[i] = p.String()
	}
	sort.Strings(out)
	return out
}

func TestExcludeSetSubtract(t *testing.T) {
	cases := []struct {
		name     string
		excludes []string
		prefix   string
		want     []string
		changed  bool
	}{
		{
			name:     "no overlap untouched",
			excludes: []string{"10.0.0.0/8"},
			prefix:   "192.168.0.0/16",
			want:     []string{"192.168.0.0/16"},
			changed:  false,
		},
		{
			name:     "exclude covers prefix entirely",
			excludes: []string{"10.0.0.0/8"},
			prefix:   "10.1.0.0/16",
			want:     nil,
			changed:  true,
		},
		{
			name:     "hole punched in the middle",
			excludes: []string{"10.0.1.0/24"},
			prefix:   "10.0.0.0/22",
			want:     []string{"10.0.0.0/24", "10.0.2.0/23"},
			changed:  true,
		},
		{
			name:     "two holes from separate excludes",
			excludes: []string{"10.0.0.0/24", "10.0.3.0/24"},
			prefix:   "10.0.0.0/22",
			want:     []string{"10.0.1.0/24", "10.0.2.0/24"},
			changed:  true,
		},
		{
			name:     "exact match removed",
			excludes: []string{"172.16.4.0/24"},
			prefix:   "172.16.4.0/24",
			want:     nil,
			changed:  true,
		},
		{
			name:     "wrong family untouched",
			excludes: []string{"10.0.0.0/8"},
			prefix:   "2001:db8::/32",
			want:     []string{"2001:db8::/32"},
			changed:  false,
		},
		{
			name:     "ipv6 hole punch",
			excludes: []string{"2001:db8:0:1::/64"},
			prefix:   "2001:db8::/62",
			want:     []string{"2001:db8::/64", "2001:db8:0:2::/63"},
			changed:  true,
		},
		{
			name:     "nested excludes normalized to broadest",
			excludes: []string{"10.0.0.0/16", "10.0.1.0/24"},
			prefix:   "10.0.0.0/15",
			want:     []string{"10.1.0.0/16"},
			changed:  true,
		},
		{
			name: "exclude starting before prefix still found",
			// The /15 exclude starts below the probed /24 — the backward
			// scan from the binary-search cut must still see it.
			excludes: []string{"10.0.0.0/15", "172.16.0.0/16"},
			prefix:   "10.1.2.0/24",
			want:     nil,
			changed:  true,
		},
		{
			name:     "empty exclude set",
			excludes: nil,
			prefix:   "10.0.0.0/8",
			want:     []string{"10.0.0.0/8"},
			changed:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			set := NewExcludeSet(mustPrefixes(tc.excludes...))
			got, changed := set.Subtract(netip.MustParsePrefix(tc.prefix))
			if changed != tc.changed {
				t.Errorf("changed = %v, want %v", changed, tc.changed)
			}
			var want []string
			if tc.want != nil {
				want = append([]string{}, tc.want...)
				sort.Strings(want)
			}
			gotStr := sortedStrings(got)
			if len(gotStr) == 0 {
				gotStr = nil
			}
			if !reflect.DeepEqual(gotStr, want) {
				t.Errorf("fragments = %v, want %v", gotStr, want)
			}
		})
	}
}

// TestExcludeSetSubtractCoversFullSpan — fragments plus the excluded space
// must exactly re-tile the original prefix: no address lost, none invented.
func TestExcludeSetSubtractCoversFullSpan(t *testing.T) {
	set := NewExcludeSet(mustPrefixes("10.0.64.0/18", "10.0.192.0/20", "10.1.0.0/16"))
	fragments, changed := set.Subtract(netip.MustParsePrefix("10.0.0.0/15"))
	if !changed {
		t.Fatal("expected overlap")
	}
	// Count addresses: /15 = 131072; minus /18 (16384) + /20 (4096) + /16 (65536) = 45056 remaining.
	var total uint64
	for _, f := range fragments {
		total += 1 << (32 - f.Bits())
	}
	if total != 131072-16384-4096-65536 {
		t.Errorf("fragment address count = %d, want %d", total, 131072-16384-4096-65536)
	}
	// No fragment may intersect any exclude.
	for _, f := range fragments {
		if got, ch := set.Subtract(f); ch || len(got) != 1 {
			t.Errorf("fragment %v still overlaps the exclude set", f)
		}
	}
}
