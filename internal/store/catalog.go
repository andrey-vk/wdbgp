package store

import (
	"context"
	"fmt"
	"log"
	"net/netip"
	"strings"
)

// CatalogPrefix represents a single CIDR prefix in the catalog.
type CatalogPrefix struct {
	ServiceKey
	CIDR string
}

func (s *Store) Catalog(ctx context.Context) (map[string][]string, error) {
	return s.CatalogForMode(ctx, DefaultCatalogModeID, false)
}

func (s *Store) CatalogForMode(ctx context.Context, modeID int64, includeDisabled bool) (map[string][]string, error) {
	includeDisabledInt := 0
	if includeDisabled {
		includeDisabledInt = 1
	}
	rows, err := s.DB.QueryContext(ctx, `
SELECT DISTINCT ce.category, ce.service
FROM catalog_entries ce
JOIN feeds f ON f.id = ce.feed_id
JOIN catalog_mode_feeds cmf ON cmf.feed_id = f.id
WHERE (f.enabled = 1 OR ? = 1) AND cmf.mode_id = ?
ORDER BY ce.category, ce.service`, includeDisabledInt, modeID)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("WARNING: rows close: %v", err)
		}
	}()
	catalog := map[string][]string{}
	for rows.Next() {
		var category, service string
		if err := rows.Scan(&category, &service); err != nil {
			return nil, err
		}
		catalog[category] = append(catalog[category], service)
	}
	return catalog, rows.Err()
}

func (s *Store) EnabledCatalogPrefixes(ctx context.Context, modeID int64) ([]CatalogPrefix, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT DISTINCT ce.category, ce.service, ce.cidr
FROM catalog_entries ce
JOIN feeds f ON f.id = ce.feed_id
JOIN catalog_mode_feeds cmf ON cmf.feed_id = f.id
JOIN catalog_modes m ON m.id = cmf.mode_id
WHERE f.enabled = 1 AND m.enabled = 1 AND cmf.mode_id = ?
ORDER BY ce.category, ce.service, ce.cidr`, modeID)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("WARNING: rows close: %v", err)
		}
	}()
	var prefixes []CatalogPrefix
	for rows.Next() {
		var prefix CatalogPrefix
		if err := rows.Scan(&prefix.Category, &prefix.Service, &prefix.CIDR); err != nil {
			return nil, err
		}
		prefixes = append(prefixes, prefix)
	}
	return prefixes, rows.Err()
}

// CategoryPrefixCounts returns the number of distinct IPv4 and IPv6 CIDRs per category.
func (s *Store) CategoryPrefixCounts(ctx context.Context, modeID int64) (v4 map[string]int, v6 map[string]int, err error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT ce.category, ce.cidr
FROM catalog_entries ce JOIN feeds f ON f.id = ce.feed_id
JOIN catalog_mode_feeds cmf ON cmf.feed_id = f.id
WHERE cmf.mode_id = ? AND f.enabled = 1
GROUP BY ce.category, ce.cidr
ORDER BY ce.category`, modeID)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("WARNING: rows close: %v", err)
		}
	}()
	v4 = map[string]int{}
	v6 = map[string]int{}
	seen := map[string]map[netip.Prefix]struct{}{}
	for rows.Next() {
		var category, rawCIDR string
		if err := rows.Scan(&category, &rawCIDR); err != nil {
			return nil, nil, err
		}
		prefix, err := netip.ParsePrefix(rawCIDR)
		if err != nil {
			continue
		}
		if seen[category] == nil {
			seen[category] = map[netip.Prefix]struct{}{}
		}
		if _, ok := seen[category][prefix]; ok {
			continue
		}
		seen[category][prefix] = struct{}{}
		if prefix.Addr().Is6() {
			v6[category]++
		} else {
			v4[category]++
		}
	}
	return v4, v6, rows.Err()
}

// PrefixCounts returns the number of distinct IPv4 and IPv6 CIDR prefixes for each service in each category.
func (s *Store) PrefixCounts(ctx context.Context, modeID int64) (v4 map[string]map[string]int, v6 map[string]map[string]int, err error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT ce.category, ce.service, ce.cidr
FROM catalog_entries ce
JOIN feeds f ON f.id = ce.feed_id
JOIN catalog_mode_feeds cmf ON cmf.feed_id = f.id
WHERE cmf.mode_id = ? AND f.enabled = 1
GROUP BY ce.category, ce.service, ce.cidr
ORDER BY ce.category, ce.service`, modeID)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("WARNING: rows close: %v", err)
		}
	}()
	v4 = map[string]map[string]int{}
	v6 = map[string]map[string]int{}
	ens := ensureCount
	for rows.Next() {
		var category, service, rawCIDR string
		if err := rows.Scan(&category, &service, &rawCIDR); err != nil {
			return nil, nil, err
		}
		prefix, err := netip.ParsePrefix(rawCIDR)
		if err != nil {
			continue
		}
		if prefix.Addr().Is6() {
			ens(&v6, category, service)
		} else {
			ens(&v4, category, service)
		}
	}
	return v4, v6, rows.Err()
}

func ensureCount(m *map[string]map[string]int, category, service string) {
	cat, ok := (*m)[category]
	if !ok {
		cat = map[string]int{}
		(*m)[category] = cat
	}
	cat[service]++
}

// CountPrefixes returns the number of unique IPv4 and IPv6 prefixes that would be
// announced for a given explicit selection (categories + services lists) after
// applying the user's route filters. It does NOT read selected_categories or
// selected_services from the DB — use the passed-in slices instead.
func (s *Store) CountPrefixes(ctx context.Context, modeID int64, categories []string, services []ServiceKey, userID int64) (v4, v6 int, err error) {
	var filterMode string
	var filterOverride bool
	err = s.DB.QueryRowContext(ctx,
		`SELECT COALESCE(filter_mode, ''), filter_override_enabled
		 FROM users WHERE id = ?`, userID).
		Scan(&filterMode, &filterOverride)
	if err != nil {
		return 0, 0, err
	}
	filterMode = normalizeFilterMode(filterMode, filterOverride)

	if len(categories) == 0 && len(services) == 0 {
		return 0, 0, nil
	}

	// Build the same UNION pattern as CountSelectionPrefixes but with
	// explicit category/service lists instead of DB lookups.
	args := []any{modeID}

	var queryParts []string

	if len(categories) > 0 {
		placeholders := make([]string, len(categories))
		for i, cat := range categories {
			placeholders[i] = "?"
			args = append(args, cat)
		}
		queryParts = append(queryParts, fmt.Sprintf(`
SELECT DISTINCT ce.cidr
FROM feeds f
JOIN catalog_mode_feeds cmf ON cmf.feed_id = f.id
JOIN catalog_modes m ON m.id = cmf.mode_id
JOIN catalog_entries ce ON ce.feed_id = f.id
WHERE cmf.mode_id = ?1
  AND f.enabled = 1
  AND m.enabled = 1
  AND ce.category IN (%s)`, strings.Join(placeholders, ", ")))
	}

	if len(services) > 0 {
		pairs := make([]string, len(services))
		for i, svc := range services {
			pairs[i] = "(?, ?)"
			args = append(args, svc.Category, svc.Service)
		}
		queryParts = append(queryParts, fmt.Sprintf(`
SELECT DISTINCT ce.cidr
FROM feeds f
JOIN catalog_mode_feeds cmf ON cmf.feed_id = f.id
JOIN catalog_modes m ON m.id = cmf.mode_id
JOIN catalog_entries ce ON ce.feed_id = f.id
WHERE cmf.mode_id = ?1
  AND f.enabled = 1
  AND m.enabled = 1
  AND (ce.category, ce.service) IN (%s)`, strings.Join(pairs, ", ")))
	}

	query := strings.Join(queryParts, " UNION ")

	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, 0, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("WARNING: rows close: %v", err)
		}
	}()

	seen := make(map[netip.Prefix]struct{})
	for rows.Next() {
		var rawPrefix string
		if err := rows.Scan(&rawPrefix); err != nil {
			return 0, 0, err
		}
		prefix, err := netip.ParsePrefix(rawPrefix)
		if err != nil {
			return 0, 0, fmt.Errorf("parse prefix %q: %w", rawPrefix, err)
		}
		if prefix.Bits() == 0 {
			continue
		}
		seen[prefix.Masked()] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}

	if len(seen) == 0 {
		return 0, 0, nil
	}

	prefixes := make([]netip.Prefix, 0, len(seen))
	for p := range seen {
		prefixes = append(prefixes, p)
	}

	// Apply the same filter logic as CountSelectionPrefixes
	userFilters, err := s.UserRouteFilters(ctx, userID)
	if err != nil {
		return 0, 0, err
	}

	globalFilters, err := s.GlobalRouteFilters(ctx)
	if err != nil {
		return 0, 0, err
	}

	var effectiveFilters RouteFilters
	switch filterMode {
	case FilterModeOverride:
		effectiveFilters = userFilters
	case FilterModeExtend:
		effectiveFilters = mergeRouteFilters(globalFilters, userFilters)
	default:
		effectiveFilters = globalFilters
	}

	filtered, err := applyRouteFiltersToPrefixes(prefixes, effectiveFilters)
	if err != nil {
		return 0, 0, fmt.Errorf("filter routes for user %d: %w", userID, err)
	}

	for _, pfx := range filtered {
		if pfx.Addr().Is6() {
			v6++
		} else {
			v4++
		}
	}
	return v4, v6, nil
}

// CountSelectionPrefixes returns the number of unique IPv4 and IPv6 prefixes that
// would be announced for a single user after applying their route filters (global
// and per-user). It replicates the same filter logic as DesiredPrefixes: collect
// prefixes matching the user's selection, then apply allow/deny lists according
// to the filter mode.
func (s *Store) CountSelectionPrefixes(ctx context.Context, userID int64) (v4, v6 int, err error) {
	var catalogModeID int64
	var filterMode string
	var filterOverride bool
	err = s.DB.QueryRowContext(ctx,
		`SELECT catalog_mode_id, COALESCE(filter_mode, ''), filter_override_enabled
		 FROM users WHERE id = ?`, userID).
		Scan(&catalogModeID, &filterMode, &filterOverride)
	if err != nil {
		return 0, 0, err
	}
	filterMode = normalizeFilterMode(filterMode, filterOverride)

	rows, err := s.DB.QueryContext(ctx, `
SELECT DISTINCT ce.cidr
FROM feeds f
JOIN catalog_mode_feeds cmf ON cmf.feed_id = f.id
JOIN catalog_modes m ON m.id = cmf.mode_id
JOIN catalog_entries ce ON ce.feed_id = f.id
WHERE cmf.mode_id = ?1
  AND f.enabled = 1
  AND m.enabled = 1
  AND EXISTS (
      SELECT 1 FROM selected_categories sc
      WHERE sc.user_id = ?2
        AND sc.mode_id = ?1
        AND sc.category = ce.category
  )
UNION
SELECT DISTINCT ce.cidr
FROM feeds f
JOIN catalog_mode_feeds cmf ON cmf.feed_id = f.id
JOIN catalog_modes m ON m.id = cmf.mode_id
JOIN catalog_entries ce ON ce.feed_id = f.id
WHERE cmf.mode_id = ?1
  AND f.enabled = 1
  AND m.enabled = 1
  AND EXISTS (
      SELECT 1 FROM selected_services ss
      WHERE ss.user_id = ?2
        AND ss.mode_id = ?1
        AND ss.category = ce.category
        AND ss.service = ce.service
  )`, catalogModeID, userID)
	if err != nil {
		return 0, 0, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("WARNING: rows close: %v", err)
		}
	}()

	seen := make(map[netip.Prefix]struct{})
	for rows.Next() {
		var rawPrefix string
		if err := rows.Scan(&rawPrefix); err != nil {
			return 0, 0, err
		}
		prefix, err := netip.ParsePrefix(rawPrefix)
		if err != nil {
			return 0, 0, fmt.Errorf("parse prefix %q: %w", rawPrefix, err)
		}
		if prefix.Bits() == 0 {
			continue
		}
		seen[prefix.Masked()] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}

	if len(seen) == 0 {
		return 0, 0, nil
	}

	prefixes := make([]netip.Prefix, 0, len(seen))
	for p := range seen {
		prefixes = append(prefixes, p)
	}

	userFilters, err := s.UserRouteFilters(ctx, userID)
	if err != nil {
		return 0, 0, err
	}

	globalFilters, err := s.GlobalRouteFilters(ctx)
	if err != nil {
		return 0, 0, err
	}

	var effectiveFilters RouteFilters
	switch filterMode {
	case FilterModeOverride:
		effectiveFilters = userFilters
	case FilterModeExtend:
		effectiveFilters = mergeRouteFilters(globalFilters, userFilters)
	default:
		effectiveFilters = globalFilters
	}

	filtered, err := applyRouteFiltersToPrefixes(prefixes, effectiveFilters)
	if err != nil {
		return 0, 0, fmt.Errorf("filter routes for user %d: %w", userID, err)
	}

	for _, pfx := range filtered {
		if pfx.Addr().Is6() {
			v6++
		} else {
			v4++
		}
	}
	return v4, v6, nil
}
