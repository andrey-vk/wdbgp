package prefixfilter

import (
	"fmt"
	"net/netip"
	"sort"
)

const DefaultMaxPrefixes = 65536

type Lists struct {
	Allow []netip.Prefix
	Deny  []netip.Prefix
}

func Apply(input []netip.Prefix, lists Lists, maxPrefixes int) ([]netip.Prefix, error) {
	if maxPrefixes <= 0 {
		maxPrefixes = DefaultMaxPrefixes
	}
	allowed := intersect(input, lists.Allow)
	if len(allowed) > maxPrefixes {
		return nil, fmt.Errorf("prefix filter produced more than %d routes", maxPrefixes)
	}
	for _, denied := range normalize(lists.Deny) {
		next := make([]netip.Prefix, 0, len(allowed))
		for _, prefix := range allowed {
			fragments := subtract(prefix, denied)
			if len(next)+len(fragments) > maxPrefixes {
				return nil, fmt.Errorf("prefix filter produced more than %d routes", maxPrefixes)
			}
			next = append(next, fragments...)
		}
		allowed = next
	}
	return normalize(allowed), nil
}

func intersect(input, allow []netip.Prefix) []netip.Prefix {
	input = normalize(input)
	allow = normalize(allow)
	if len(allow) == 0 {
		return input
	}
	var result []netip.Prefix
	for _, prefix := range input {
		for _, permitted := range allow {
			if prefix.Addr().BitLen() != permitted.Addr().BitLen() {
				continue
			}
			switch {
			case prefix.Contains(permitted.Addr()):
				result = append(result, permitted)
			case permitted.Contains(prefix.Addr()):
				result = append(result, prefix)
			}
		}
	}
	return normalize(result)
}

func subtract(prefix, denied netip.Prefix) []netip.Prefix {
	prefix = prefix.Masked()
	denied = denied.Masked()
	if prefix.Addr().BitLen() != denied.Addr().BitLen() || !prefix.Contains(denied.Addr()) {
		return []netip.Prefix{prefix}
	}
	if denied.Bits() <= prefix.Bits() {
		return nil
	}

	result := make([]netip.Prefix, 0, denied.Bits()-prefix.Bits())
	current := prefix
	for current.Bits() < denied.Bits() {
		left, right := children(current)
		if left.Contains(denied.Addr()) {
			result = append(result, right)
			current = left
		} else {
			result = append(result, left)
			current = right
		}
	}
	return result
}

func children(prefix netip.Prefix) (netip.Prefix, netip.Prefix) {
	prefix = prefix.Masked()
	bits := prefix.Bits()
	left := netip.PrefixFrom(prefix.Addr(), bits+1)
	if prefix.Addr().Is4() {
		address := prefix.Addr().As4()
		address[bits/8] |= byte(1 << (7 - bits%8))
		return left, netip.PrefixFrom(netip.AddrFrom4(address), bits+1)
	}
	address := prefix.Addr().As16()
	address[bits/8] |= byte(1 << (7 - bits%8))
	return left, netip.PrefixFrom(netip.AddrFrom16(address), bits+1)
}

func normalize(prefixes []netip.Prefix) []netip.Prefix {
	unique := make(map[netip.Prefix]struct{}, len(prefixes))
	for _, prefix := range prefixes {
		if prefix.IsValid() {
			unique[prefix.Masked()] = struct{}{}
		}
	}
	result := make([]netip.Prefix, 0, len(unique))
	for prefix := range unique {
		result = append(result, prefix)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Addr().BitLen() != result[j].Addr().BitLen() {
			return result[i].Addr().BitLen() < result[j].Addr().BitLen()
		}
		if result[i].Bits() != result[j].Bits() {
			return result[i].Bits() < result[j].Bits()
		}
		return result[i].Addr().Less(result[j].Addr())
	})
	collapsed := make([]netip.Prefix, 0, len(result))
	accepted := make(map[netip.Prefix]struct{}, len(result))
	for _, prefix := range result {
		covered := false
		for bits := 0; bits < prefix.Bits(); bits++ {
			parent := netip.PrefixFrom(prefix.Addr(), bits).Masked()
			if _, ok := accepted[parent]; ok {
				covered = true
				break
			}
		}
		if !covered {
			collapsed = append(collapsed, prefix)
			accepted[prefix] = struct{}{}
		}
	}
	return collapsed
}
