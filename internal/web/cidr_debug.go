package web

import (
	"context"
	"fmt"
	"math/big"
	"net/netip"
	"sort"
	"strings"

	"github.com/andrey-vk/wdbgp/internal/store"
)

type coverageItem struct {
	Name             string   `json:"name,omitempty"`
	Category         string   `json:"category,omitempty"`
	Service          string   `json:"service,omitempty"`
	Percentage       float64  `json:"percentage,omitempty"`
	BeforePercentage float64  `json:"before_percentage"`
	AfterPercentage  float64  `json:"after_percentage"`
	Matches          []string `json:"matches,omitempty"`
}

type cidrDebugResult struct {
	Query              string         `json:"query"`
	FullServices       []coverageItem `json:"full_services"`
	PartialServices    []coverageItem `json:"partial_services"`
	CombinedServices   []coverageItem `json:"combined_services"`
	CombinedPercentage float64        `json:"combined_percentage"`
	Users              []coverageItem `json:"users"`
}

type addressRange struct {
	start *big.Int
	end   *big.Int
}

func (s *Server) debugCIDR(ctx context.Context, raw string) (cidrDebugResult, error) {
	target, err := parseDebugPrefix(raw)
	if err != nil {
		return cidrDebugResult{}, err
	}
	catalog, err := s.store.EnabledCatalogPrefixes(ctx)
	if err != nil {
		return cidrDebugResult{}, err
	}
	serviceRanges := map[store.ServiceKey][]addressRange{}
	servicePrefixes := map[store.ServiceKey][]netip.Prefix{}
	for _, entry := range catalog {
		prefix, err := netip.ParsePrefix(entry.CIDR)
		if err != nil {
			return cidrDebugResult{}, fmt.Errorf("parse catalog prefix %q: %w", entry.CIDR, err)
		}
		if prefix.Addr().BitLen() != target.Addr().BitLen() {
			continue
		}
		// Feed-provided default routes are discarded before route filtering.
		if prefix.Bits() == 0 {
			continue
		}
		servicePrefixes[entry.ServiceKey] = append(servicePrefixes[entry.ServiceKey], prefix.Masked())
		if overlap, ok := intersectRanges(prefixRange(target), prefixRange(prefix)); ok {
			serviceRanges[entry.ServiceKey] = append(serviceRanges[entry.ServiceKey], overlap)
		}
	}

	result := cidrDebugResult{Query: target.String()}
	for key, ranges := range serviceRanges {
		percentage := coveragePercentage(target, ranges)
		if percentage == 0 {
			continue
		}
		item := coverageItem{
			Category: key.Category, Service: key.Service, Percentage: percentage,
		}
		if percentage == 100 {
			result.FullServices = append(result.FullServices, item)
		} else {
			result.PartialServices = append(result.PartialServices, item)
		}
	}
	if len(result.FullServices) == 0 && len(result.PartialServices) > 1 {
		var combinedRanges []addressRange
		result.CombinedServices = append(result.CombinedServices, result.PartialServices...)
		for key, ranges := range serviceRanges {
			for _, item := range result.CombinedServices {
				if item.Category == key.Category && item.Service == key.Service {
					combinedRanges = append(combinedRanges, ranges...)
					break
				}
			}
		}
		result.CombinedPercentage = coveragePercentage(target, combinedRanges)
	}

	users, err := s.store.Users(ctx, true)
	if err != nil {
		return cidrDebugResult{}, err
	}
	for _, user := range users {
		categories, services, err := s.store.UserSelection(ctx, user.ID)
		if err != nil {
			return cidrDebugResult{}, err
		}
		var beforeRanges []addressRange
		var selectedPrefixes []netip.Prefix
		var matches []string
		for key, serviceCoverage := range serviceRanges {
			if categories[key.Category] || services[key] {
				beforeRanges = append(beforeRanges, serviceCoverage...)
				selectedPrefixes = append(selectedPrefixes, servicePrefixes[key]...)
				matches = append(matches, key.Category+" / "+key.Service)
			}
		}
		beforePercentage := coveragePercentage(target, beforeRanges)
		if beforePercentage > 0 {
			filteredPrefixes, err := s.store.ApplyUserRouteFilters(ctx, user, selectedPrefixes)
			if err != nil {
				return cidrDebugResult{}, fmt.Errorf("filter routes for user %d: %w", user.ID, err)
			}
			var afterRanges []addressRange
			for _, prefix := range filteredPrefixes {
				if prefix.Addr().BitLen() != target.Addr().BitLen() {
					continue
				}
				if overlap, ok := intersectRanges(prefixRange(target), prefixRange(prefix)); ok {
					afterRanges = append(afterRanges, overlap)
				}
			}
			sort.Strings(matches)
			result.Users = append(result.Users, coverageItem{
				Name:             user.Name,
				BeforePercentage: beforePercentage,
				AfterPercentage:  coveragePercentage(target, afterRanges),
				Matches:          matches,
			})
		}
	}

	sortCoverage(result.FullServices)
	sortCoverage(result.PartialServices)
	sortCoverage(result.CombinedServices)
	sort.Slice(result.Users, func(i, j int) bool {
		if result.Users[i].AfterPercentage != result.Users[j].AfterPercentage {
			return result.Users[i].AfterPercentage > result.Users[j].AfterPercentage
		}
		if result.Users[i].BeforePercentage != result.Users[j].BeforePercentage {
			return result.Users[i].BeforePercentage > result.Users[j].BeforePercentage
		}
		return result.Users[i].Name < result.Users[j].Name
	})
	return result, nil
}

func parseDebugPrefix(raw string) (netip.Prefix, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return netip.Prefix{}, fmt.Errorf("CIDR or IP address is required")
	}
	if prefix, err := netip.ParsePrefix(raw); err == nil {
		return prefix.Masked(), nil
	}
	addr, err := netip.ParseAddr(raw)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("invalid CIDR or IP address")
	}
	return netip.PrefixFrom(addr, addr.BitLen()), nil
}

func prefixRange(prefix netip.Prefix) addressRange {
	start := addrInteger(prefix.Masked().Addr())
	size := new(big.Int).Lsh(big.NewInt(1), uint(prefix.Addr().BitLen()-prefix.Bits()))
	end := new(big.Int).Sub(new(big.Int).Add(new(big.Int).Set(start), size), big.NewInt(1))
	return addressRange{start: start, end: end}
}

func addrInteger(addr netip.Addr) *big.Int {
	if addr.Is4() {
		value := addr.As4()
		return new(big.Int).SetBytes(value[:])
	}
	value := addr.As16()
	return new(big.Int).SetBytes(value[:])
}

func intersectRanges(left, right addressRange) (addressRange, bool) {
	start := maxInt(left.start, right.start)
	end := minInt(left.end, right.end)
	return addressRange{start: start, end: end}, start.Cmp(end) <= 0
}

func coveragePercentage(target netip.Prefix, ranges []addressRange) float64 {
	if len(ranges) == 0 {
		return 0
	}
	sort.Slice(ranges, func(i, j int) bool {
		if comparison := ranges[i].start.Cmp(ranges[j].start); comparison != 0 {
			return comparison < 0
		}
		return ranges[i].end.Cmp(ranges[j].end) < 0
	})
	covered := new(big.Int)
	current := addressRange{
		start: new(big.Int).Set(ranges[0].start),
		end:   new(big.Int).Set(ranges[0].end),
	}
	for _, next := range ranges[1:] {
		adjacent := new(big.Int).Add(current.end, big.NewInt(1))
		if next.start.Cmp(adjacent) <= 0 {
			if next.end.Cmp(current.end) > 0 {
				current.end.Set(next.end)
			}
			continue
		}
		covered.Add(covered, rangeSize(current))
		current = addressRange{
			start: new(big.Int).Set(next.start),
			end:   new(big.Int).Set(next.end),
		}
	}
	covered.Add(covered, rangeSize(current))
	total := new(big.Int).Lsh(big.NewInt(1), uint(target.Addr().BitLen()-target.Bits()))
	ratio, _ := new(big.Rat).SetFrac(covered, total).Float64()
	percentage := ratio * 100
	if percentage > 100 {
		return 100
	}
	return percentage
}

func rangeSize(value addressRange) *big.Int {
	return new(big.Int).Add(new(big.Int).Sub(value.end, value.start), big.NewInt(1))
}

func minInt(left, right *big.Int) *big.Int {
	if left.Cmp(right) <= 0 {
		return new(big.Int).Set(left)
	}
	return new(big.Int).Set(right)
}

func maxInt(left, right *big.Int) *big.Int {
	if left.Cmp(right) >= 0 {
		return new(big.Int).Set(left)
	}
	return new(big.Int).Set(right)
}

func sortCoverage(items []coverageItem) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Percentage != items[j].Percentage {
			return items[i].Percentage > items[j].Percentage
		}
		if items[i].Category != items[j].Category {
			return items[i].Category < items[j].Category
		}
		return items[i].Service < items[j].Service
	})
}
