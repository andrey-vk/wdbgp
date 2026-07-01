package store

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/netip"
	"sort"
	"strconv"
	"strings"

	"github.com/andrey-vk/wdbgp/internal/prefixfilter"
)

// PrefixRouteInfo carries category, service, and mode for a prefix
// that made it through the DesiredPrefixes pipeline.
type PrefixRouteInfo struct {
	ModeID   int64
	Category string
	Service  string
}

// userRoute is a single route from the DesiredPrefixes query, carrying
// its original mode/category/service metadata before any filters split it.
type userRoute struct {
	prefix   netip.Prefix
	modeID   int64
	category string
	service  string
}

func (s *Store) DesiredPrefixes(ctx context.Context) (map[string][]int64, map[string]PrefixRouteInfo, error) {
	rows, err := s.DB.QueryContext(ctx, `
-- Simpler UNION-based approach without CTEs
SELECT DISTINCT ce.cidr, u.id, COALESCE(u.filter_mode, ''), u.filter_override_enabled,
       ce.category, ce.service, cmf.mode_id
FROM users u
JOIN catalog_mode_feeds cmf ON cmf.mode_id = u.catalog_mode_id
JOIN feeds f ON f.id = cmf.feed_id
JOIN catalog_modes m ON m.id = cmf.mode_id  
JOIN catalog_entries ce ON ce.feed_id = f.id
WHERE u.enabled = 1
  AND f.enabled = 1
  AND m.enabled = 1
  AND EXISTS (
      SELECT 1 FROM selected_categories sc
      WHERE sc.user_id = u.id
        AND sc.mode_id = u.catalog_mode_id
        AND sc.category = ce.category
  )
UNION
SELECT DISTINCT ce.cidr, u.id, COALESCE(u.filter_mode, ''), u.filter_override_enabled,
       ce.category, ce.service, cmf.mode_id
FROM users u
JOIN catalog_mode_feeds cmf ON cmf.mode_id = u.catalog_mode_id
JOIN feeds f ON f.id = cmf.feed_id
JOIN catalog_modes m ON m.id = cmf.mode_id
JOIN catalog_entries ce ON ce.feed_id = f.id
WHERE u.enabled = 1
  AND f.enabled = 1
  AND m.enabled = 1
  AND EXISTS (
      SELECT 1 FROM selected_services ss
      WHERE ss.user_id = u.id
        AND ss.mode_id = u.catalog_mode_id
        AND ss.category = ce.category
        AND ss.service = ce.service
  )
ORDER BY 1, 2`)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("WARNING: rows close: %v", err)
		}
	}()
	type selectedUser struct {
		filterMode string
		routes     []userRoute
	}
	selected := map[int64]*selectedUser{}
	for rows.Next() {
		var rawPrefix string
		var userID int64
		var filterMode string
		var override bool
		var category, service string
		var modeID int64
		if err := rows.Scan(&rawPrefix, &userID, &filterMode, &override, &category, &service, &modeID); err != nil {
			return nil, nil, err
		}
		prefix, err := netip.ParsePrefix(rawPrefix)
		if err != nil {
			return nil, nil, fmt.Errorf("parse selected prefix %q: %w", rawPrefix, err)
		}
		// A feed-provided default route is never a useful service route.
		if prefix.Bits() == 0 {
			continue
		}
		user := selected[userID]
		if user == nil {
			user = &selectedUser{filterMode: normalizeFilterMode(filterMode, override)}
			selected[userID] = user
		}
		user.routes = append(user.routes, userRoute{
			prefix:   prefix.Masked(),
			modeID:   modeID,
			category: category,
			service:  service,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, nil, err
	}

	// Fetch all user route filters at once to avoid N+1 queries
	userFiltersMap, err := s.allUserRouteFilters(ctx)
	if err != nil {
		return nil, nil, err
	}

	globalFilters, err := s.GlobalRouteFilters(ctx)
	if err != nil {
		return nil, nil, err
	}

	result := map[string][]int64{}
	prefixMeta := map[string]PrefixRouteInfo{}
	for userID, selUser := range selected {
		// Get user-specific filters if any
		var userFilters RouteFilters
		if filters, ok := userFiltersMap[userID]; ok {
			userFilters = filters
		}

		var effectiveFilters RouteFilters
		switch selUser.filterMode {
		case FilterModeOverride:
			effectiveFilters = userFilters
		case FilterModeExtend:
			effectiveFilters = mergeRouteFilters(globalFilters, userFilters)
		default: // FilterModeDefault or empty
			effectiveFilters = globalFilters
		}

		// Build a flat list of original prefixes for filtering
		origPrefixes := make([]netip.Prefix, len(selUser.routes))
		for i, r := range selUser.routes {
			origPrefixes[i] = r.prefix
		}

		filtered, err := applyRouteFiltersToPrefixes(origPrefixes, effectiveFilters)
		if err != nil {
			return nil, nil, fmt.Errorf("filter routes for user %d: %w", userID, err)
		}

		for _, prefix := range filtered {
			key := prefix.String()
			result[key] = append(result[key], userID)
			if len(result) > prefixfilter.DefaultMaxPrefixes {
				return nil, nil, fmt.Errorf("route filters produced more than %d unique routes",
					prefixfilter.DefaultMaxPrefixes)
			}
			// Carry mode/category/service through the filter — find the best matching
			// original route so that communities can be loaded from the correct mode.
			// Key includes userID so that two users in different catalog modes
			// selecting the same CIDR each get their own mode/category/service metadata.
			metaKey := prefix.String() + ":" + strconv.FormatInt(userID, 10)
			if _, exists := prefixMeta[metaKey]; !exists {
				if modeID, cat, svc, ok := findBestMatch(prefix, selUser.routes); ok {
					prefixMeta[metaKey] = PrefixRouteInfo{ModeID: modeID, Category: cat, Service: svc}
				}
			}
		}
	}
	return result, prefixMeta, nil
}

// findBestMatch returns the (modeID, category, service) for the original route
// that best contains the filtered prefix.  This handles the case where route
// filters split a prefix (e.g. /8 → /16) — the longer match wins.
func findBestMatch(needle netip.Prefix, routes []userRoute) (int64, string, string, bool) {
	var best *struct {
		modeID   int64
		category string
		service  string
		bits     int
	}
	for i := range routes {
		r := &routes[i]
		if !r.prefix.Contains(needle.Addr()) || needle.Bits() < r.prefix.Bits() {
			continue
		}
		if best == nil || r.prefix.Bits() > best.bits {
			best = &struct {
				modeID   int64
				category string
				service  string
				bits     int
			}{r.modeID, r.category, r.service, r.prefix.Bits()}
		}
	}
	if best == nil {
		return 0, "", "", false
	}
	return best.modeID, best.category, best.service, true
}

func (s *Store) ApplyUserRouteFilters(
	ctx context.Context,
	user User,
	prefixes []netip.Prefix,
) ([]netip.Prefix, error) {
	globalFilters, err := s.GlobalRouteFilters(ctx)
	if err != nil {
		return nil, err
	}
	return s.applyUserRouteFilters(ctx, user.ID,
		normalizeFilterMode(user.FilterMode, user.FilterOverride), prefixes, globalFilters)
}

func (s *Store) applyUserRouteFilters(
	ctx context.Context,
	userID int64,
	filterMode string,
	prefixes []netip.Prefix,
	globalFilters RouteFilters,
) ([]netip.Prefix, error) {
	filters := globalFilters
	var err error
	switch filterMode {
	case FilterModeOverride:
		filters, err = s.UserRouteFilters(ctx, userID)
		if err != nil {
			return nil, err
		}
	case FilterModeExtend:
		userFilters, err := s.UserRouteFilters(ctx, userID)
		if err != nil {
			return nil, err
		}
		filters = mergeRouteFilters(globalFilters, userFilters)
	}
	lists, err := parseRouteFilters(filters)
	if err != nil {
		return nil, err
	}
	return prefixfilter.Apply(prefixes, lists, prefixfilter.DefaultMaxPrefixes)
}

func (s *Store) GlobalRouteFilters(ctx context.Context) (RouteFilters, error) {
	settings, err := s.GetAllSettings(ctx)
	if err != nil {
		return RouteFilters{}, err
	}
	return RouteFilters{
		Allow: splitNewlines(settings["filter_allow"]),
		Deny:  splitNewlines(settings["filter_deny"]),
	}, nil
}

func splitNewlines(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	var result []string
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		result = append(result, line)
	}
	return result
}

func (s *Store) UserRouteFilters(ctx context.Context, userID int64) (RouteFilters, error) {
	return readRouteFilters(ctx, s.DB,
		"SELECT action, cidr FROM user_route_filters WHERE user_id = ? ORDER BY action, cidr", userID)
}

// allUserRouteFilters fetches all user route filters in a single query
func (s *Store) allUserRouteFilters(ctx context.Context) (map[int64]RouteFilters, error) {
	rows, err := s.DB.QueryContext(ctx,
		"SELECT user_id, action, cidr FROM user_route_filters ORDER BY user_id, action, cidr")
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("WARNING: rows close: %v", err)
		}
	}()

	filtersMap := make(map[int64]RouteFilters)
	for rows.Next() {
		var userID int64
		var action, cidr string
		if err := rows.Scan(&userID, &action, &cidr); err != nil {
			return nil, err
		}
		filters := filtersMap[userID]
		if action == "allow" {
			filters.Allow = append(filters.Allow, cidr)
		} else {
			filters.Deny = append(filters.Deny, cidr)
		}
		filtersMap[userID] = filters
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return filtersMap, nil
}

// applyRouteFiltersToPrefixes applies route filters to prefixes without database queries
func applyRouteFiltersToPrefixes(prefixes []netip.Prefix, filters RouteFilters) ([]netip.Prefix, error) {
	lists, err := parseRouteFilters(filters)
	if err != nil {
		return nil, err
	}
	return prefixfilter.Apply(prefixes, lists, prefixfilter.DefaultMaxPrefixes)
}

func (s *Store) SetUserRouteFilters(ctx context.Context, userID int64, filters RouteFilters) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM user_route_filters WHERE user_id = ?", userID); err != nil {
			return err
		}
		return insertRouteFilters(ctx, tx, userID, filters)
	})
}

func (s *Store) SetUserRouteFilterConfig(ctx context.Context, userID int64, mode string, filters RouteFilters) error {
	mode, err := NormalizeFilterMode(mode)
	if err != nil {
		return err
	}
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM user_route_filters WHERE user_id = ?", userID); err != nil {
			return err
		}
		if err := insertRouteFilters(ctx, tx, userID, filters); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx,
			"UPDATE users SET filter_override_enabled = ?, filter_mode = ? WHERE id = ?",
			mode != FilterModeGlobal, mode, userID)
		if err != nil {
			return err
		}
		if count, err := result.RowsAffected(); err != nil {
			return fmt.Errorf("rows affected: %w", err)
		} else if count == 0 {
			return sql.ErrNoRows
		}
		return nil
	})
}

func (s *Store) SetUserFilterOverride(ctx context.Context, userID int64, enabled bool) error {
	mode := FilterModeGlobal
	if enabled {
		mode = FilterModeOverride
	}
	result, err := s.DB.ExecContext(ctx,
		"UPDATE users SET filter_override_enabled = ?, filter_mode = ? WHERE id = ?", enabled, mode, userID)
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("rows affected: %w", err)
	} else if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

const (
	FilterModeGlobal   = "global"
	FilterModeExtend   = "extend"
	FilterModeOverride = "override"
)

func NormalizeFilterMode(mode string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", FilterModeGlobal:
		return FilterModeGlobal, nil
	case FilterModeExtend:
		return FilterModeExtend, nil
	case FilterModeOverride:
		return FilterModeOverride, nil
	default:
		return "", fmt.Errorf("invalid route filter mode %q", mode)
	}
}

func normalizeFilterMode(mode string, override bool) string {
	normalized, err := NormalizeFilterMode(mode)
	if err == nil && normalized != FilterModeGlobal {
		return normalized
	}
	if override {
		return FilterModeOverride
	}
	return FilterModeGlobal
}

func mergeRouteFilters(global, user RouteFilters) RouteFilters {
	return RouteFilters{
		Allow: append(append([]string{}, global.Allow...), user.Allow...),
		Deny:  append(append([]string{}, global.Deny...), user.Deny...),
	}
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func readRouteFilters(ctx context.Context, db queryer, query string, args ...any) (RouteFilters, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return RouteFilters{}, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("WARNING: rows close: %v", err)
		}
	}()
	var filters RouteFilters
	for rows.Next() {
		var action, cidr string
		if err := rows.Scan(&action, &cidr); err != nil {
			return RouteFilters{}, err
		}
		if action == "allow" {
			filters.Allow = append(filters.Allow, cidr)
		} else {
			filters.Deny = append(filters.Deny, cidr)
		}
	}
	return filters, rows.Err()
}

func insertRouteFilters(ctx context.Context, tx *sql.Tx, userID int64, filters RouteFilters) error {
	normalized, err := NormalizeRouteFilters(filters)
	if err != nil {
		return err
	}
	for _, item := range []struct {
		action string
		cidrs  []string
	}{
		{"allow", normalized.Allow},
		{"deny", normalized.Deny},
	} {
		for _, cidr := range item.cidrs {
			if _, err := tx.ExecContext(ctx,
				"INSERT INTO user_route_filters(user_id, action, cidr) VALUES (?, ?, ?)",
				userID, item.action, cidr); err != nil {
				return err
			}
		}
	}
	return nil
}

func NormalizeRouteFilters(filters RouteFilters) (RouteFilters, error) {
	normalize := func(values []string) ([]string, error) {
		unique := map[string]struct{}{}
		for _, value := range values {
			if strings.TrimSpace(value) == "" {
				continue
			}
			cidr, err := NormalizePrefix(value)
			if err != nil {
				return nil, err
			}
			unique[cidr] = struct{}{}
		}
		result := make([]string, 0, len(unique))
		for cidr := range unique {
			result = append(result, cidr)
		}
		sort.Strings(result)
		return result, nil
	}
	allow, err := normalize(filters.Allow)
	if err != nil {
		return RouteFilters{}, fmt.Errorf("invalid allow prefix: %w", err)
	}
	deny, err := normalize(filters.Deny)
	if err != nil {
		return RouteFilters{}, fmt.Errorf("invalid deny prefix: %w", err)
	}
	return RouteFilters{Allow: allow, Deny: deny}, nil
}

func parseRouteFilters(filters RouteFilters) (prefixfilter.Lists, error) {
	parse := func(values []string) ([]netip.Prefix, error) {
		result := make([]netip.Prefix, 0, len(values))
		for _, value := range values {
			prefix, err := netip.ParsePrefix(value)
			if err != nil {
				return nil, err
			}
			result = append(result, prefix.Masked())
		}
		return result, nil
	}
	allow, err := parse(filters.Allow)
	if err != nil {
		return prefixfilter.Lists{}, err
	}
	deny, err := parse(filters.Deny)
	if err != nil {
		return prefixfilter.Lists{}, err
	}
	return prefixfilter.Lists{Allow: allow, Deny: deny}, nil
}
