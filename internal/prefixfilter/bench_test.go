package prefixfilter

import (
	"math/rand"
	"net/netip"
	"testing"
)

func BenchmarkNormalizeRandom(b *testing.B) {
	// Generate random prefixes for benchmarking
	rng := rand.New(rand.NewSource(42))
	prefixes := make([]netip.Prefix, 10000)
	for i := range prefixes {
		// Random IPv4 prefixes
		addr := netip.AddrFrom4([4]byte{
			byte(rng.Intn(256)),
			byte(rng.Intn(256)),
			byte(rng.Intn(256)),
			byte(rng.Intn(256)),
		})
		prefixes[i] = netip.PrefixFrom(addr, rng.Intn(33))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		normalize(prefixes)
	}
}

func BenchmarkNormalizeOverlapping(b *testing.B) {
	// Generate prefixes with many overlaps (worst case for original algorithm)
	prefixes := make([]netip.Prefix, 1000)
	for i := range prefixes {
		// All prefixes within 10.0.0.0/8
		addr := netip.AddrFrom4([4]byte{10, byte(i >> 8), byte(i & 0xFF), 0})
		prefixes[i] = netip.PrefixFrom(addr, 24)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		normalize(prefixes)
	}
}

func BenchmarkSubtractDeep(b *testing.B) {
	// Test subtracting a deep /32 from a /8 (creates many fragments)
	prefix := netip.MustParsePrefix("10.0.0.0/8")
	denied := netip.MustParsePrefix("10.1.2.3/32")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		subtract(prefix, denied)
	}
}

func BenchmarkApplyManyDeny(b *testing.B) {
	// Test applying many deny rules
	input := []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}
	lists := Lists{
		Deny: make([]netip.Prefix, 100),
	}
	
	rng := rand.New(rand.NewSource(42))
	for i := range lists.Deny {
		addr := netip.AddrFrom4([4]byte{
			byte(rng.Intn(256)),
			byte(rng.Intn(256)),
			byte(rng.Intn(256)),
			byte(rng.Intn(256)),
		})
		lists.Deny[i] = netip.PrefixFrom(addr, 32)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Apply(input, lists, 1000000)
	}
}

func BenchmarkIntersectLarge(b *testing.B) {
	// Generate large input and allow lists
	input := make([]netip.Prefix, 1000)
	allow := make([]netip.Prefix, 1000)
	
	rng := rand.New(rand.NewSource(42))
	for i := range input {
		addr := netip.AddrFrom4([4]byte{
			byte(rng.Intn(256)),
			byte(rng.Intn(256)),
			byte(rng.Intn(256)),
			byte(rng.Intn(256)),
		})
		input[i] = netip.PrefixFrom(addr, rng.Intn(33))
		allow[i] = netip.PrefixFrom(addr, rng.Intn(33))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		intersect(input, allow)
	}
}