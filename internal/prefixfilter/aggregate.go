package prefixfilter

import (
	"math/big"
	"net/netip"
	"sort"
)

// Aggregate returns the minimal set of CIDR prefixes that covers exactly
// the same address space as the input — masking host bits, dropping exact
// and subset duplicates, and merging overlapping or adjacent prefixes into
// their shortest equivalent covering blocks (e.g. 10.0.0.0/25 + 10.0.0.128/25
// become 10.0.0.0/24). IPv4 and IPv6 inputs are aggregated independently,
// since they occupy disjoint address spaces. The result is sorted by
// address family, then by starting address, then by prefix length.
func Aggregate(prefixes []netip.Prefix) []netip.Prefix {
	var v4, v6 []netip.Prefix
	for _, p := range prefixes {
		if !p.IsValid() {
			continue
		}
		p = p.Masked()
		if p.Addr().Is4() {
			v4 = append(v4, p)
		} else {
			v6 = append(v6, p)
		}
	}

	result := make([]netip.Prefix, 0, len(v4)+len(v6))
	result = append(result, aggregateFamily(v4)...)
	result = append(result, aggregateFamily(v6)...)
	return result
}

// addrRange is an inclusive [start, end] address range, represented as a
// big.Int so the same logic handles both 32-bit (IPv4) and 128-bit (IPv6)
// address spaces without duplicating arithmetic per family.
type addrRange struct {
	start, end *big.Int
	is4        bool
}

func aggregateFamily(prefixes []netip.Prefix) []netip.Prefix {
	if len(prefixes) == 0 {
		return nil
	}

	ranges := make([]addrRange, len(prefixes))
	for i, p := range prefixes {
		start, end := prefixToRange(p)
		ranges[i] = addrRange{start: start, end: end, is4: p.Addr().Is4()}
	}

	sort.Slice(ranges, func(i, j int) bool {
		return ranges[i].start.Cmp(ranges[j].start) < 0
	})

	// Merge overlapping or adjacent ranges (end of one immediately precedes
	// start of the next) into the widest covering range.
	merged := ranges[:1]
	one := big.NewInt(1)
	for _, r := range ranges[1:] {
		last := &merged[len(merged)-1]
		gap := new(big.Int).Sub(r.start, last.end)
		if gap.Cmp(one) <= 0 { // r.start <= last.end+1: overlapping or adjacent
			if r.end.Cmp(last.end) > 0 {
				last.end = r.end
			}
			continue
		}
		merged = append(merged, r)
	}

	var result []netip.Prefix
	for _, r := range merged {
		result = append(result, rangeToPrefixes(r)...)
	}
	return result
}

// prefixToRange returns the inclusive [network address, broadcast address]
// range covered by a masked prefix, as big.Int for uniform 32/128-bit math.
func prefixToRange(p netip.Prefix) (start, end *big.Int) {
	start = new(big.Int).SetBytes(p.Addr().AsSlice())
	hostBits := p.Addr().BitLen() - p.Bits()
	span := new(big.Int).Lsh(big.NewInt(1), uint(hostBits))
	end = new(big.Int).Add(start, span)
	end.Sub(end, big.NewInt(1))
	return start, end
}

// rangeToPrefixes decomposes an inclusive [start, end] address range into
// the minimal list of properly-aligned CIDR blocks that exactly cover it.
func rangeToPrefixes(r addrRange) []netip.Prefix {
	bitLen := 32
	if !r.is4 {
		bitLen = 128
	}

	var result []netip.Prefix
	cur := new(big.Int).Set(r.start)
	one := big.NewInt(1)
	for cur.Cmp(r.end) <= 0 {
		// Largest block size allowed by cur's alignment (trailing zero bits).
		alignBits := bitLen
		if cur.Sign() != 0 {
			alignBits = trailingZeroBits(cur, bitLen)
		}

		// Largest power-of-two block that still fits within [cur, r.end].
		// remaining.BitLen()-1 == floor(log2(remaining)) for any remaining
		// >= 1, which is exactly the host-bit count of that largest block.
		remaining := new(big.Int).Sub(r.end, cur)
		remaining.Add(remaining, one) // count of addresses left, inclusive
		fitBits := remaining.BitLen() - 1

		blockHostBits := alignBits
		if fitBits < blockHostBits {
			blockHostBits = fitBits
		}
		prefixLen := bitLen - blockHostBits

		addr := bigIntToAddr(cur, r.is4)
		result = append(result, netip.PrefixFrom(addr, prefixLen))

		blockSize := new(big.Int).Lsh(one, uint(blockHostBits))
		cur.Add(cur, blockSize)
	}
	return result
}

// trailingZeroBits returns the number of trailing zero bits in v, treated
// as a fixed-width bitLen-bit unsigned integer (v == 0 is handled by the
// caller, since that has bitLen trailing zeros — the whole address space).
func trailingZeroBits(v *big.Int, bitLen int) int {
	for i := 0; i < bitLen; i++ {
		if v.Bit(i) != 0 {
			return i
		}
	}
	return bitLen
}

func bigIntToAddr(v *big.Int, is4 bool) netip.Addr {
	buf := v.Bytes()
	size := 16
	if is4 {
		size = 4
	}
	padded := make([]byte, size)
	copy(padded[size-len(buf):], buf)
	addr, _ := netip.AddrFromSlice(padded) //nolint:errcheck // fixed-size buffer always parses
	return addr
}
