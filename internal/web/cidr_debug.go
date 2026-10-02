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

func (s *Server) debugCIDR(
	ctx context.Context,
	raw string,
	modeIDs ...int64,
) (cidrDebugResult, error) {
	modeID := store.DefaultCatalogModeID
	if len(modeIDs) > 0 {
		modeID = modeIDs[0]
	}
	target, err := parseDebugPrefix(raw)
	if err != nil {
		return cidrDebugResult{}, err
	}
	coverage, err := s.coverageForTarget(ctx, target, modeID)
	if err != nil {
		return cidrDebugResult{}, err
	}

	result := cidrDebugResult{
		Query:              coverage.target.String(),
		FullServices:       coverage.fullServices,
		PartialServices:    coverage.partialServices,
		CombinedServices:   coverage.combinedServices,
		CombinedPercentage: coverage.combinedPercentage,
		Users:              []coverageItem{},
	}

	users, err := s.store.Users(ctx, true)
	if err != nil {
		return cidrDebugResult{}, err
	}
	for _, user := range users {
		if user.CatalogModeID != modeID {
			continue
		}
		matches, beforePercentage, afterPercentage, err := s.userCoverage(ctx, user, coverage)
		if err != nil {
			return cidrDebugResult{}, err
		}
		if beforePercentage == 0 {
			continue
		}
		selected := make([]string, 0, len(matches))
		for _, m := range matches {
			if m.Selected {
				selected = append(selected, m.Category+" / "+m.Service)
			}
		}
		sort.Strings(selected)
		result.Users = append(result.Users, coverageItem{
			Name:             user.Name,
			BeforePercentage: beforePercentage,
			AfterPercentage:  afterPercentage,
			Matches:          selected,
		})
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

// catalogCoverage is the catalog-wide intersection of a target
// address/CIDR against every enabled catalog prefix for one mode,
// independent of any user's selections or filters.
type catalogCoverage struct {
	target             netip.Prefix
	serviceRanges      map[store.ServiceKey][]addressRange
	servicePrefixes    map[store.ServiceKey][]netip.Prefix
	fullServices       []coverageItem
	partialServices    []coverageItem
	combinedServices   []coverageItem
	combinedPercentage float64
}

func (s *Server) coverageForTarget(ctx context.Context, target netip.Prefix, modeID int64) (catalogCoverage, error) {
	catalog, err := s.store.EnabledCatalogPrefixes(ctx, modeID)
	if err != nil {
		return catalogCoverage{}, err
	}
	coverage := catalogCoverage{
		target:           target,
		serviceRanges:    map[store.ServiceKey][]addressRange{},
		servicePrefixes:  map[store.ServiceKey][]netip.Prefix{},
		fullServices:     []coverageItem{},
		partialServices:  []coverageItem{},
		combinedServices: []coverageItem{},
	}
	for _, entry := range catalog {
		prefix, err := netip.ParsePrefix(entry.CIDR)
		if err != nil {
			return catalogCoverage{}, fmt.Errorf("parse catalog prefix %q: %w", entry.CIDR, err)
		}
		if prefix.Addr().BitLen() != target.Addr().BitLen() {
			continue
		}
		// Feed-provided default routes are discarded before route filtering.
		if prefix.Bits() == 0 {
			continue
		}
		coverage.servicePrefixes[entry.ServiceKey] = append(coverage.servicePrefixes[entry.ServiceKey], prefix.Masked())
		if overlap, ok := intersectRanges(prefixRange(target), prefixRange(prefix)); ok {
			coverage.serviceRanges[entry.ServiceKey] = append(coverage.serviceRanges[entry.ServiceKey], overlap)
		}
	}

	for key, ranges := range coverage.serviceRanges {
		percentage := coveragePercentage(target, ranges)
		if percentage == 0 {
			continue
		}
		item := coverageItem{Category: key.Category, Service: key.Service, Percentage: percentage}
		if percentage == 100 {
			coverage.fullServices = append(coverage.fullServices, item)
		} else {
			coverage.partialServices = append(coverage.partialServices, item)
		}
	}
	if len(coverage.fullServices) == 0 && len(coverage.partialServices) > 1 {
		var combinedRanges []addressRange
		coverage.combinedServices = append(coverage.combinedServices, coverage.partialServices...)
		for key, ranges := range coverage.serviceRanges {
			for _, item := range coverage.combinedServices {
				if item.Category == key.Category && item.Service == key.Service {
					combinedRanges = append(combinedRanges, ranges...)
					break
				}
			}
		}
		coverage.combinedPercentage = coveragePercentage(target, combinedRanges)
	}
	return coverage, nil
}

// userCIDRMatch is one catalog category/service covering the queried
// address, independent of whether the caller has selected it.
type userCIDRMatch struct {
	Category   string  `json:"category"`
	Service    string  `json:"service"`
	Percentage float64 `json:"percentage"`
	Selected   bool    `json:"selected"`
}

// userCoverage reports every catalog category/service that intersects
// coverage.target (selected or not, each flagged), plus the before/after
// -route-filter percentage summed over only the ones user has selected.
// Touches only the one user passed in — shared by debugCIDR (loops all
// users) and userDebugCIDR (single caller), so the range math and the
// ApplyUserRouteFilters call exist in one place.
func (s *Server) userCoverage(
	ctx context.Context, user store.User, coverage catalogCoverage,
) (matches []userCIDRMatch, beforePercentage, afterPercentage float64, err error) {
	categories, services, err := s.store.UserModeSelection(ctx, user.ID, user.CatalogModeID)
	if err != nil {
		return nil, 0, 0, err
	}
	var beforeRanges []addressRange
	var selectedPrefixes []netip.Prefix
	for key, ranges := range coverage.serviceRanges {
		percentage := coveragePercentage(coverage.target, ranges)
		selected := categories[key.Category] || services[key]
		matches = append(matches, userCIDRMatch{
			Category: key.Category, Service: key.Service, Percentage: percentage, Selected: selected,
		})
		if selected {
			beforeRanges = append(beforeRanges, ranges...)
			selectedPrefixes = append(selectedPrefixes, coverage.servicePrefixes[key]...)
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Category != matches[j].Category {
			return matches[i].Category < matches[j].Category
		}
		return matches[i].Service < matches[j].Service
	})

	beforePercentage = coveragePercentage(coverage.target, beforeRanges)
	if beforePercentage == 0 {
		return matches, 0, 0, nil
	}
	filteredPrefixes, err := s.store.ApplyUserRouteFilters(ctx, user, selectedPrefixes)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("filter routes for user %d: %w", user.ID, err)
	}
	var afterRanges []addressRange
	for _, prefix := range filteredPrefixes {
		if prefix.Addr().BitLen() != coverage.target.Addr().BitLen() {
			continue
		}
		if overlap, ok := intersectRanges(prefixRange(coverage.target), prefixRange(prefix)); ok {
			afterRanges = append(afterRanges, overlap)
		}
	}
	afterPercentage = coveragePercentage(coverage.target, afterRanges)
	return matches, beforePercentage, afterPercentage, nil
}

// userCIDRLookupResult is scoped to exactly the authenticated caller who
// requested it — no Name field and no cross-user list, unlike
// cidrDebugResult.Users, so there is nothing here that could leak another
// user's data.
type userCIDRLookupResult struct {
	Query            string          `json:"query"`
	Matches          []userCIDRMatch `json:"matches"`
	BeforePercentage float64         `json:"before_percentage"`
	AfterPercentage  float64         `json:"after_percentage"`
	InTunnel         bool            `json:"in_tunnel"`
}

// userDebugCIDR answers "why is this address in/not in my tunnel" for
// exactly user. Mode is always user.CatalogModeID; this never reads
// s.store.Users(...) — there is no code path here that can see another
// user's row.
func (s *Server) userDebugCIDR(ctx context.Context, user store.User, raw string) (userCIDRLookupResult, error) {
	target, err := parseDebugPrefix(raw)
	if err != nil {
		return userCIDRLookupResult{}, err
	}
	coverage, err := s.coverageForTarget(ctx, target, user.CatalogModeID)
	if err != nil {
		return userCIDRLookupResult{}, err
	}
	matches, beforePercentage, afterPercentage, err := s.userCoverage(ctx, user, coverage)
	if err != nil {
		return userCIDRLookupResult{}, err
	}
	if matches == nil {
		matches = []userCIDRMatch{}
	}
	return userCIDRLookupResult{
		Query:            coverage.target.String(),
		Matches:          matches,
		BeforePercentage: beforePercentage,
		AfterPercentage:  afterPercentage,
		InTunnel:         afterPercentage > 0,
	}, nil
}

func parseDebugPrefix(raw string) (netip.Prefix, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return netip.Prefix{}, fmt.Errorf("CIDR or IP address is required")
	}
	prefix, err := store.ParsePrefixOrAddr(raw)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("invalid CIDR or IP address")
	}
	return prefix.Masked(), nil
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
