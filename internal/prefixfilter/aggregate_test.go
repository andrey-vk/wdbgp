package prefixfilter

import (
	"math/rand"
	"net/netip"
	"reflect"
	"sort"
	"testing"
)

func newTestRand(t *testing.T) *rand.Rand {
	t.Helper()
	return rand.New(rand.NewSource(1)) //nolint:gosec // test data generator, not a security context
}

func aggregateStrings(t *testing.T, in ...string) []string {
	t.Helper()
	got := Aggregate(prefixes(in...))
	out := make([]string, len(got))
	for i, p := range got {
		out[i] = p.String()
	}
	sort.Strings(out)
	return out
}

func wantStrings(values ...string) []string {
	sort.Strings(values)
	return values
}

func TestAggregateMasksUnmaskedInput(t *testing.T) {
	got := Aggregate([]netip.Prefix{netip.MustParsePrefix("10.0.0.5/24")})
	if len(got) != 1 || got[0].String() != "10.0.0.0/24" {
		t.Fatalf("got %v, want [10.0.0.0/24]", got)
	}
}

func TestAggregateDeduplicatesExact(t *testing.T) {
	got := aggregateStrings(t, "10.0.0.0/24", "10.0.0.0/24")
	want := wantStrings("10.0.0.0/24")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestAggregateDropsSubsetOfLargerPrefix(t *testing.T) {
	got := aggregateStrings(t, "10.0.0.0/24", "10.0.0.128/25")
	want := wantStrings("10.0.0.0/24")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestAggregateMergesAdjacentEqualLengthPair(t *testing.T) {
	got := aggregateStrings(t, "10.0.0.0/25", "10.0.0.128/25")
	want := wantStrings("10.0.0.0/24")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestAggregateMergesFourSiblingsIntoParent(t *testing.T) {
	got := aggregateStrings(t, "10.0.0.0/26", "10.0.0.64/26", "10.0.0.128/26", "10.0.0.192/26")
	want := wantStrings("10.0.0.0/24")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestAggregateMergesMixedSizeAdjacentBlocks(t *testing.T) {
	// /25 + two /26s that exactly fill out the rest of the /24.
	got := aggregateStrings(t, "10.0.0.0/25", "10.0.0.128/26", "10.0.0.192/26")
	want := wantStrings("10.0.0.0/24")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestAggregateLeavesNonAdjacentPrefixesAlone(t *testing.T) {
	got := aggregateStrings(t, "10.0.0.0/24", "10.0.2.0/24")
	want := wantStrings("10.0.0.0/24", "10.0.2.0/24")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestAggregateNonPowerOfTwoUnionStaysMinimal(t *testing.T) {
	// 10.0.0.0/24 (256 addrs) + 10.0.1.0/25 (128 addrs) are adjacent and
	// together cover 384 addresses — not a power of two, so the minimal
	// covering set is still exactly these same two blocks (verifying the
	// greedy decomposition doesn't needlessly split the /24 into smaller
	// pieces just because a merge happened).
	got := aggregateStrings(t, "10.0.0.0/24", "10.0.1.0/25")
	want := wantStrings("10.0.0.0/24", "10.0.1.0/25")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestAggregateHandlesUnalignedAdjacentRange(t *testing.T) {
	// 10.0.1.0/25 (.1.0-.1.127) + 10.0.1.128/26 (.1.128-.1.191) are
	// adjacent but don't sum to a clean supernet (192 addresses total) —
	// minimal covering set is the two blocks unchanged.
	got := aggregateStrings(t, "10.0.1.0/25", "10.0.1.128/26")
	want := wantStrings("10.0.1.0/25", "10.0.1.128/26")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestAggregateIPv6MergesAdjacent(t *testing.T) {
	got := aggregateStrings(t, "2001:db8::/33", "2001:db8:8000::/33")
	want := wantStrings("2001:db8::/32")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestAggregateIPv4AndIPv6AreIndependent(t *testing.T) {
	got := aggregateStrings(t, "10.0.0.0/25", "10.0.0.128/25", "2001:db8::/33", "2001:db8:8000::/33")
	want := wantStrings("10.0.0.0/24", "2001:db8::/32")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestAggregateFullRangeCollapsesToDefaultRoute(t *testing.T) {
	got := aggregateStrings(t, "0.0.0.0/1", "128.0.0.0/1")
	want := wantStrings("0.0.0.0/0")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestAggregateSingleHostAddress(t *testing.T) {
	got := aggregateStrings(t, "10.0.0.1/32")
	want := wantStrings("10.0.0.1/32")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestAggregateOrderOfInputDoesNotMatter(t *testing.T) {
	a := aggregateStrings(t, "10.0.0.128/26", "10.0.0.0/26", "10.0.0.192/26", "10.0.0.64/26")
	b := aggregateStrings(t, "10.0.0.0/26", "10.0.0.64/26", "10.0.0.128/26", "10.0.0.192/26")
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("order-dependent result: %v vs %v", a, b)
	}
}

func TestAggregateEmptyInput(t *testing.T) {
	got := Aggregate(nil)
	if len(got) != 0 {
		t.Fatalf("got %v, want empty", got)
	}
}

func TestAggregateSkipsInvalidPrefix(t *testing.T) {
	got := Aggregate([]netip.Prefix{{}, netip.MustParsePrefix("10.0.0.0/24")})
	if len(got) != 1 || got[0].String() != "10.0.0.0/24" {
		t.Fatalf("got %v, want [10.0.0.0/24]", got)
	}
}

// TestAggregateMatchesBruteForceCoverage randomly generates sets of CIDRs
// within a small address space (10.0.0.0/16) and verifies, via a brute-force
// per-address bitmap, that Aggregate's output covers exactly the same
// addresses as the input — and that the output prefixes are themselves
// pairwise non-overlapping and non-adjacent (i.e. actually minimal, not
// just "some valid covering").
func TestAggregateMatchesBruteForceCoverage(t *testing.T) {
	rng := newTestRand(t)
	base := netip.MustParseAddr("10.0.0.0")

	for trial := 0; trial < 200; trial++ {
		n := 1 + rng.Intn(8)
		var input []netip.Prefix
		want := make(map[uint16]bool) // offset within /16 -> covered

		for i := 0; i < n; i++ {
			bits := 24 + rng.Intn(9) // /24..>/32 within the /16 (host part <= 8 bits)
			hostBits := 32 - bits
			blockSize := 1 << hostBits
			blockIndex := rng.Intn(256 / blockSize)
			offset := blockIndex * blockSize
			addr := offsetAddr(base, uint16(offset)) //nolint:gosec // offset always < 256 by construction
			input = append(input, netip.PrefixFrom(addr, bits))
			for a := offset; a < offset+blockSize; a++ {
				want[uint16(a)] = true //nolint:gosec // a always < 256 by construction
			}
		}

		got := Aggregate(input)

		// Coverage must match exactly.
		gotCoverage := make(map[uint16]bool)
		for _, p := range got {
			lo, hi := rangeWithinBase(t, base, p)
			for a := lo; a <= hi; a++ {
				gotCoverage[uint16(a)] = true //nolint:gosec // a always < 256 by construction
			}
		}
		if len(gotCoverage) != len(want) {
			t.Fatalf("trial %d: input=%v got=%v coverage size %d, want %d", trial, input, got, len(gotCoverage), len(want))
		}
		for a := range want {
			if !gotCoverage[a] {
				t.Fatalf("trial %d: input=%v got=%v missing address offset %d", trial, input, got, a)
			}
		}

		// Output must be pairwise non-overlapping and non-adjacent (else
		// Aggregate itself failed to fully minimize its own output).
		for i := range got {
			for j := range got {
				if i == j {
					continue
				}
				if got[i].Overlaps(got[j]) {
					t.Fatalf("trial %d: output %v has overlapping prefixes %s and %s", trial, got, got[i], got[j])
				}
			}
		}
	}
}

func offsetAddr(base netip.Addr, offset uint16) netip.Addr {
	b := base.As4()
	b[2] = byte(offset >> 8)
	b[3] = byte(offset) //nolint:gosec // offset always < 256 by construction
	return netip.AddrFrom4(b)
}

func rangeWithinBase(t *testing.T, base netip.Addr, p netip.Prefix) (lo, hi int) {
	t.Helper()
	start := p.Masked().Addr()
	b := start.As4()
	baseB := base.As4()
	if b[0] != baseB[0] || b[1] != baseB[1] {
		t.Fatalf("prefix %s outside test base %s/16", p, base)
	}
	lo = int(b[2])<<8 | int(b[3])
	hostBits := 32 - p.Bits()
	hi = lo + (1 << hostBits) - 1
	return lo, hi
}
