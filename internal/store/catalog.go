package store

import (
	"context"
	"database/sql"
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
	return catalogForMode(ctx, s.DB, modeID, includeDisabled)
}

// CatalogScopesForMode reads the enabled-only catalog (what the admin
// Communities list actually shows) and the reset-scope catalog (every
// include-linked feed, regardless of whether it's enabled — the same scope
// genCommunitiesRuntime counts when it decides each category's real
// position) in one transaction. Two separate CatalogForMode calls here
// would each see their own, independently-committed snapshot, so a feed
// sync landing between them could leave the two maps describing different
// catalog versions — e.g. a service the first read saw already removed
// from the second, silently defaulting a position lookup against it to
// zero instead of erroring. One transaction closes that window the same
// way AllModeCommunitySnapshots does for the community export.
func (s *Store) CatalogScopesForMode(ctx context.Context, modeID int64) (visible, reset map[string][]string, err error) {
	err = s.Transaction(ctx, func(tx *sql.Tx) error {
		var txErr error
		visible, txErr = catalogForMode(ctx, tx, modeID, false)
		if txErr != nil {
			return txErr
		}
		if catalogScopesForModeHook != nil {
			catalogScopesForModeHook()
		}
		reset, txErr = catalogForMode(ctx, tx, modeID, true)
		return txErr
	})
	return visible, reset, err
}

// catalogScopesForModeHook, when set, runs once CatalogScopesForMode's
// first read has returned and before its second read starts — a seam for
// tests to inject a concurrent write at exactly that point and confirm
// it's invisible to the second read, proving the two reads are genuinely
// isolated within one transaction rather than two independent ones. A nil
// hook (the default outside tests) is a no-op.
var catalogScopesForModeHook func()

// catalogForMode is CatalogForMode's implementation, parameterized on
// queryer so a caller that needs it inside a larger transaction (e.g. a
// consistent snapshot alongside communities and prefix counts) can pass a
// *sql.Tx instead of going through the Store's own connection.
func catalogForMode(ctx context.Context, q queryer, modeID int64, includeDisabled bool) (map[string][]string, error) {
	// The materialized catalog_mode_entries only carries enabled include
	// feeds (minus excludes). The includeDisabled admin view wants disabled
	// feeds' services listed too, so it reads the raw entries — still
	// restricted to include links: exclude feeds never contribute catalog
	// services, they only subtract prefixes.
	query := `
SELECT DISTINCT c.name, sv.name
FROM catalog_mode_entries cme
JOIN services sv ON sv.id = cme.service_id
JOIN categories c ON c.id = sv.category_id
WHERE cme.mode_id = ?
ORDER BY c.name, sv.name`
	if includeDisabled {
		query = `
SELECT DISTINCT c.name, sv.name
FROM catalog_entries ce
JOIN services sv ON sv.id = ce.service_id
JOIN categories c ON c.id = sv.category_id
JOIN catalog_mode_feeds cmf ON cmf.feed_id = ce.feed_id
WHERE cmf.mode_id = ? AND cmf.exclude = 0
ORDER BY c.name, sv.name`
	}
	rows, err := q.QueryContext(ctx, query, modeID)
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
SELECT DISTINCT c.name, sv.name, p.ip, p.bits
FROM catalog_mode_entries cme
JOIN services sv ON sv.id = cme.service_id
JOIN categories c ON c.id = sv.category_id
JOIN prefixes p ON p.id = cme.prefix_id
JOIN catalog_modes m ON m.id = cme.mode_id
WHERE m.enabled = 1 AND cme.mode_id = ?
ORDER BY c.name, sv.name, p.ip, p.bits`, modeID)
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
		var ip []byte
		var bits int
		if err := rows.Scan(&prefix.Category, &prefix.Service, &ip, &bits); err != nil {
			return nil, err
		}
		decoded, err := DecodePrefix(ip, bits)
		if err != nil {
			return nil, err
		}
		prefix.CIDR = decoded.String()
		prefixes = append(prefixes, prefix)
	}
	return prefixes, rows.Err()
}

// CategoryPrefixCounts returns the number of distinct IPv4 and IPv6 CIDRs per category.
func (s *Store) CategoryPrefixCounts(ctx context.Context, modeID int64) (v4 map[string]int, v6 map[string]int, err error) {
	return categoryPrefixCounts(ctx, s.DB, modeID)
}

// categoryPrefixCounts is CategoryPrefixCounts' implementation, parameterized
// on queryer — see catalogForMode's doc comment for why.
func categoryPrefixCounts(ctx context.Context, q queryer, modeID int64) (v4 map[string]int, v6 map[string]int, err error) {
	rows, err := q.QueryContext(ctx, `
SELECT c.name, length(p.ip), COUNT(DISTINCT cme.prefix_id)
FROM catalog_mode_entries cme
JOIN services sv ON sv.id = cme.service_id
JOIN categories c ON c.id = sv.category_id
JOIN prefixes p ON p.id = cme.prefix_id
WHERE cme.mode_id = ?
GROUP BY c.name, length(p.ip)`, modeID)
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
	for rows.Next() {
		var category string
		var ipLen, count int
		if err := rows.Scan(&category, &ipLen, &count); err != nil {
			return nil, nil, err
		}
		if ipLen == 16 {
			v6[category] += count
		} else {
			v4[category] += count
		}
	}
	return v4, v6, rows.Err()
}

// PrefixCounts returns the number of distinct IPv4 and IPv6 CIDR prefixes for each service in each category.
func (s *Store) PrefixCounts(ctx context.Context, modeID int64) (v4 map[string]map[string]int, v6 map[string]map[string]int, err error) {
	return prefixCounts(ctx, s.DB, modeID)
}

// prefixCounts is PrefixCounts' implementation, parameterized on queryer —
// see catalogForMode's doc comment for why.
func prefixCounts(ctx context.Context, q queryer, modeID int64) (v4 map[string]map[string]int, v6 map[string]map[string]int, err error) {
	rows, err := q.QueryContext(ctx, `
SELECT c.name, sv.name, length(p.ip), COUNT(DISTINCT cme.prefix_id)
FROM catalog_mode_entries cme
JOIN services sv ON sv.id = cme.service_id
JOIN categories c ON c.id = sv.category_id
JOIN prefixes p ON p.id = cme.prefix_id
WHERE cme.mode_id = ?
GROUP BY c.name, sv.name, length(p.ip)`, modeID)
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
	for rows.Next() {
		var category, service string
		var ipLen, count int
		if err := rows.Scan(&category, &service, &ipLen, &count); err != nil {
			return nil, nil, err
		}
		target := &v4
		if ipLen == 16 {
			target = &v6
		}
		cat, ok := (*target)[category]
		if !ok {
			cat = map[string]int{}
			(*target)[category] = cat
		}
		cat[service] += count
	}
	return v4, v6, rows.Err()
}

// CountPrefixes returns the number of unique IPv4 and IPv6 prefixes that would be
// announced for a given explicit selection (categories + services lists) after
// applying the user's route filters. It does NOT read selected_categories or
// selected_services from the DB — use the passed-in slices instead.
func (s *Store) CountPrefixes(ctx context.Context, modeID int64, categories []string, services []ServiceKey, userID int64) (v4, v6 int, err error) {
	var filterModeInt int
	err = s.DB.QueryRowContext(ctx,
		"SELECT filter_mode FROM users WHERE id = ?", userID).
		Scan(&filterModeInt)
	if err != nil {
		return 0, 0, err
	}
	filterMode := filterModeFromInt(filterModeInt)

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
SELECT DISTINCT p.ip, p.bits
FROM catalog_mode_entries cme
JOIN catalog_modes m ON m.id = cme.mode_id
JOIN services sv ON sv.id = cme.service_id
JOIN categories c ON c.id = sv.category_id
JOIN prefixes p ON p.id = cme.prefix_id
WHERE cme.mode_id = ?1
  AND m.enabled = 1
  AND c.name IN (%s)`, strings.Join(placeholders, ", ")))
	}

	if len(services) > 0 {
		pairs := make([]string, len(services))
		for i, svc := range services {
			pairs[i] = "(?, ?)"
			args = append(args, svc.Category, svc.Service)
		}
		queryParts = append(queryParts, fmt.Sprintf(`
SELECT DISTINCT p.ip, p.bits
FROM catalog_mode_entries cme
JOIN catalog_modes m ON m.id = cme.mode_id
JOIN services sv ON sv.id = cme.service_id
JOIN categories c ON c.id = sv.category_id
JOIN prefixes p ON p.id = cme.prefix_id
WHERE cme.mode_id = ?1
  AND m.enabled = 1
  AND (c.name, sv.name) IN (%s)`, strings.Join(pairs, ", ")))
	}

	query := strings.Join(queryParts, " UNION ")

	prefixes, err := s.queryPrefixes(ctx, query, args...)
	if err != nil {
		return 0, 0, err
	}
	if len(prefixes) == 0 {
		return 0, 0, nil
	}

	return s.countFilteredPrefixes(ctx, userID, filterMode, prefixes)
}

// CountSelectionPrefixes returns the number of unique IPv4 and IPv6 prefixes that
// would be announced for a single user after applying their route filters (global
// and per-user). It replicates the same filter logic as DesiredPrefixes: collect
// prefixes matching the user's selection, then apply allow/deny lists according
// to the filter mode.
func (s *Store) CountSelectionPrefixes(ctx context.Context, userID int64) (v4, v6 int, err error) {
	return countSelectionPrefixes(ctx, s.DB, userID)
}

// countSelectionPrefixesTx is CountSelectionPrefixes' tx-scoped twin, for a
// caller measuring prefix counts inside a transaction that hasn't
// committed yet (see blastradius.go) — s.DB-backed queries run on a
// different connection and can't see uncommitted writes from tx, or would
// block on the write lock tx is still holding.
func countSelectionPrefixesTx(ctx context.Context, tx *sql.Tx, userID int64) (v4, v6 int, err error) {
	return countSelectionPrefixes(ctx, tx, userID)
}

func countSelectionPrefixes(ctx context.Context, q queryer, userID int64) (v4, v6 int, err error) {
	var catalogModeID int64
	var filterModeInt int
	var enabled bool
	err = q.QueryRowContext(ctx, "SELECT catalog_mode_id, filter_mode, enabled FROM users WHERE id = ?", userID).
		Scan(&catalogModeID, &filterModeInt, &enabled)
	if err != nil {
		return 0, 0, err
	}
	// A disabled user has nothing announced (DesiredPrefixes requires
	// u.enabled = 1), so their announced-route count is zero.
	if !enabled {
		return 0, 0, nil
	}
	filterMode := filterModeFromInt(filterModeInt)

	prefixes, err := queryPrefixes(ctx, q, `
SELECT DISTINCT p.ip, p.bits
FROM catalog_mode_entries cme
JOIN catalog_modes m ON m.id = cme.mode_id
JOIN services sv ON sv.id = cme.service_id
JOIN prefixes p ON p.id = cme.prefix_id
WHERE cme.mode_id = ?1
  AND m.enabled = 1
  AND (
      EXISTS (
          SELECT 1 FROM selected_categories sc
          WHERE sc.user_id = ?2
            AND sc.mode_id = ?1
            AND sc.category_id = sv.category_id
      )
      OR EXISTS (
          SELECT 1 FROM selected_services ss
          WHERE ss.user_id = ?2
            AND ss.mode_id = ?1
            AND ss.service_id = cme.service_id
      )
  )`, catalogModeID, userID)
	if err != nil {
		return 0, 0, err
	}
	if len(prefixes) == 0 {
		return 0, 0, nil
	}

	return countFilteredPrefixes(ctx, q, userID, filterMode, prefixes)
}

// queryPrefixes runs a query returning (ip BLOB, bits) rows and decodes
// them, skipping default routes (a feed-provided default route is never a
// useful service route).
func (s *Store) queryPrefixes(ctx context.Context, query string, args ...any) ([]netip.Prefix, error) {
	return queryPrefixes(ctx, s.DB, query, args...)
}

// queryPrefixes is queryPrefixes' queryer-parameterized implementation —
// see countSelectionPrefixesTx for why a tx-scoped caller needs this.
func queryPrefixes(ctx context.Context, q queryer, query string, args ...any) ([]netip.Prefix, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("WARNING: rows close: %v", err)
		}
	}()

	var prefixes []netip.Prefix
	for rows.Next() {
		var ip []byte
		var bits int
		if err := rows.Scan(&ip, &bits); err != nil {
			return nil, err
		}
		prefix, err := DecodePrefix(ip, bits)
		if err != nil {
			return nil, err
		}
		if prefix.Bits() == 0 {
			continue
		}
		prefixes = append(prefixes, prefix)
	}
	return prefixes, rows.Err()
}

// countFilteredPrefixes applies the user's effective route filters to the
// prefixes and counts the IPv4/IPv6 survivors.
func (s *Store) countFilteredPrefixes(ctx context.Context, userID int64, filterMode string, prefixes []netip.Prefix) (v4, v6 int, err error) {
	return countFilteredPrefixes(ctx, s.DB, userID, filterMode, prefixes)
}

// countFilteredPrefixes is countFilteredPrefixes' queryer-parameterized
// implementation — see countSelectionPrefixesTx.
func countFilteredPrefixes(ctx context.Context, q queryer, userID int64, filterMode string, prefixes []netip.Prefix) (v4, v6 int, err error) {
	userFilters, err := readRouteFilters(ctx, q,
		"SELECT action, ip, bits FROM user_route_filters WHERE user_id = ? ORDER BY action, ip, bits", userID)
	if err != nil {
		return 0, 0, err
	}

	globalFilters, err := globalRouteFilters(ctx, q)
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
