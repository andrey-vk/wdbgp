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
	
	// For typical use cases (allow list is small), the simple algorithm is fine
	// But we can optimize by sorting and using binary search for larger lists
	// However, since allow lists in wdbgp are typically small (a few entries),
	// the simple O(n*m) algorithm is acceptable.
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
	// Step 1: Deduplicate and mask prefixes
	unique := make(map[netip.Prefix]struct{}, len(prefixes))
	for _, prefix := range prefixes {
		if prefix.IsValid() {
			unique[prefix.Masked()] = struct{}{}
		}
	}
	
	// Step 2: Convert to slice
	result := make([]netip.Prefix, 0, len(unique))
	for prefix := range unique {
		result = append(result, prefix)
	}
	if len(result) <= 1 {
		return result
	}
	
	// Step 3: Sort by address family, then prefix length, then address
	// This puts parent prefixes before child prefixes
	sort.Slice(result, func(i, j int) bool {
		if result[i].Addr().BitLen() != result[j].Addr().BitLen() {
			return result[i].Addr().BitLen() < result[j].Addr().BitLen()
		}
		if result[i].Bits() != result[j].Bits() {
			return result[i].Bits() < result[j].Bits()
		}
		return result[i].Addr().Less(result[j].Addr())
	})
	
	// Step 4: Use threshold to choose algorithm
	// For small number of prefixes, simple algorithm is faster
	// For large number or many overlaps, trie is better
	const threshold = 100
	if len(result) <= threshold {
		return normalizeSimple(result)
	}
	
	// Separate IPv4 and IPv6 since they can't overlap
	var v4Prefixes, v6Prefixes []netip.Prefix
	for _, p := range result {
		if p.Addr().Is4() {
			v4Prefixes = append(v4Prefixes, p)
		} else {
			v6Prefixes = append(v6Prefixes, p)
		}
	}
	
	normalized := make([]netip.Prefix, 0, len(result))
	normalized = append(normalized, normalizeWithTrie(v4Prefixes)...)
	normalized = append(normalized, normalizeWithTrie(v6Prefixes)...)
	
	return normalized
}

func normalizeSimple(prefixes []netip.Prefix) []netip.Prefix {
	// Simple O(n*m) algorithm where m is prefix length
	// Works well for small n
	accepted := make(map[netip.Prefix]struct{}, len(prefixes))
	collapsed := make([]netip.Prefix, 0, len(prefixes))
	
	for _, prefix := range prefixes {
		covered := false
		
		// Check all possible parent prefixes
		addr := prefix.Addr()
		for bits := prefix.Bits() - 1; bits >= 0; bits-- {
			parent := netip.PrefixFrom(addr, bits).Masked()
			if _, ok := accepted[parent]; ok {
				covered = true
				break
			}
		}
		
		if !covered {
			accepted[prefix] = struct{}{}
			collapsed = append(collapsed, prefix)
		}
	}
	
	return collapsed
}

// normalizeWithTrie uses a binary trie for O(m) containment checks
func normalizeWithTrie(prefixes []netip.Prefix) []netip.Prefix {
	if len(prefixes) <= 1 {
		return prefixes
	}
	
	// Build a simple binary trie
	type trieNode struct {
		prefix   *netip.Prefix // nil for internal nodes
		children [2]*trieNode  // 0: left, 1: right
	}
	
	var root *trieNode
	normalized := make([]netip.Prefix, 0, len(prefixes))
	
	for _, p := range prefixes {
		// Check if prefix is covered by any prefix in the trie
		covered := false
		
		// Walk down the trie following the prefix bits
		node := root
		depth := 0
		addr := p.Addr()
		
		for node != nil {
			// If we encounter a node with a prefix, check if it covers p
			if node.prefix != nil {
				if node.prefix.Bits() <= p.Bits() && (*node.prefix).Contains(addr) {
					covered = true
					break
				}
			}
			
			// Stop if we've reached the prefix length
			if depth >= p.Bits() {
				break
			}
			
			// Get the bit at current depth
			var bit byte
			if addr.Is4() {
				bytes := addr.As4()
				if depth/8 < 4 {
					bit = (bytes[depth/8] >> (7 - (depth % 8))) & 1
				}
			} else {
				bytes := addr.As16()
				if depth/8 < 16 {
					bit = (bytes[depth/8] >> (7 - (depth % 8))) & 1
				}
			}
			
			// Move to child
			node = node.children[bit]
			depth++
		}
		
		if !covered {
			// Add prefix to result and insert into trie
			normalized = append(normalized, p)
			
			// Insert into trie
			if root == nil {
				root = &trieNode{}
			}
			
			node := root
			for depth := 0; depth < p.Bits(); depth++ {
				// Get the bit at current depth
				var bit byte
				if addr.Is4() {
					bytes := addr.As4()
					if depth/8 < 4 {
						bit = (bytes[depth/8] >> (7 - (depth % 8))) & 1
					}
				} else {
					bytes := addr.As16()
					if depth/8 < 16 {
						bit = (bytes[depth/8] >> (7 - (depth % 8))) & 1
					}
				}
				
				if node.children[bit] == nil {
					node.children[bit] = &trieNode{}
				}
				node = node.children[bit]
			}
			
			// At the leaf node, store the prefix
			node.prefix = &p
		}
	}
	
	return normalized
}
