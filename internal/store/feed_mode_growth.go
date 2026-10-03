package store

import (
	"context"
	"database/sql"
	"net/netip"
	"sort"
)

// prefixAssociation is one prefix as a mode announces it, with the categories
// whose services provide it. Selections are per category, so the same prefix
// under a new category is growth for a user who selects only that category.
type prefixAssociation struct {
	prefix     netip.Prefix
	categories map[string]bool
}

// ModePrefixCategories maps mode → prefix id → how the mode announces it.
type ModePrefixCategories map[int64]map[int64]*prefixAssociation

// ModeCategoryGrowth lists the prefixes a sync newly announced in a mode
// through one category.
type ModeCategoryGrowth struct {
	ModeID   int64
	Category string
	Prefixes []netip.Prefix
}

// SnapshotFeedModePrefixesTx captures, for every mode the feed is linked to,
// the prefixes its materialized catalog announces and through which
// categories. Taken before and after a sync, it shows what the sync made newly
// announced, after deduplication and exclude feeds.
func SnapshotFeedModePrefixesTx(ctx context.Context, tx *sql.Tx, feedID int64) (ModePrefixCategories, error) {
	modeIDs, err := feedModeIDsTx(ctx, tx, feedID)
	if err != nil {
		return nil, err
	}
	snap := ModePrefixCategories{}
	for _, modeID := range modeIDs {
		rows, err := tx.QueryContext(ctx, `
SELECT DISTINCT cme.prefix_id, c.name, p.ip, p.bits
FROM catalog_mode_entries cme
JOIN services sv ON sv.id = cme.service_id
JOIN categories c ON c.id = sv.category_id
JOIN prefixes p ON p.id = cme.prefix_id
WHERE cme.mode_id = ?`, modeID)
		if err != nil {
			return nil, err
		}
		prefixes := map[int64]*prefixAssociation{}
		for rows.Next() {
			var prefixID int64
			var category string
			var ip []byte
			var bits int
			if err := rows.Scan(&prefixID, &category, &ip, &bits); err != nil {
				_ = rows.Close() //nolint:errcheck
				return nil, err
			}
			prefix, err := DecodePrefix(ip, bits)
			if err != nil {
				_ = rows.Close() //nolint:errcheck
				return nil, err
			}
			// Default routes are never announced (DesiredPrefixes skips them).
			if prefix.Bits() == 0 {
				continue
			}
			a := prefixes[prefixID]
			if a == nil {
				a = &prefixAssociation{prefix: prefix, categories: map[string]bool{}}
				prefixes[prefixID] = a
			}
			a.categories[category] = true
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close() //nolint:errcheck
			return nil, err
		}
		_ = rows.Close() //nolint:errcheck
		snap[modeID] = prefixes
	}
	return snap, nil
}

// ModeGrowth lists, per mode and category, the prefixes announced after a sync
// that weren't announced through that category before it. Each category's set
// is normalized first: a prefix covered by a broader one in the same category
// adds no route, so it isn't growth (prefixfilter.Apply drops such prefixes too).
func ModeGrowth(before, after ModePrefixCategories) []ModeCategoryGrowth {
	type key struct {
		mode     int64
		category string
	}
	beforeSets := map[key]map[netip.Prefix]bool{}
	afterSets := map[key]map[netip.Prefix]bool{}
	collect := func(snap ModePrefixCategories, into map[key]map[netip.Prefix]bool) {
		for modeID, prefixes := range snap {
			for _, a := range prefixes {
				for category := range a.categories {
					k := key{modeID, category}
					if into[k] == nil {
						into[k] = map[netip.Prefix]bool{}
					}
					into[k][a.prefix] = true
				}
			}
		}
	}
	collect(before, beforeSets)
	collect(after, afterSets)

	var out []ModeCategoryGrowth
	for k, set := range afterSets {
		normAfter := normalizeCovered(set)
		normBefore := normalizeCovered(beforeSets[k])
		var added []netip.Prefix
		for p := range normAfter {
			if !normBefore[p] {
				added = append(added, p)
			}
		}
		if len(added) == 0 {
			continue
		}
		sort.Slice(added, func(i, j int) bool { return added[i].String() < added[j].String() })
		out = append(out, ModeCategoryGrowth{ModeID: k.mode, Category: k.category, Prefixes: added})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ModeID != out[j].ModeID {
			return out[i].ModeID < out[j].ModeID
		}
		return out[i].Category < out[j].Category
	})
	return out
}

// normalizeCovered drops every prefix that a broader prefix in the set covers.
func normalizeCovered(set map[netip.Prefix]bool) map[netip.Prefix]bool {
	out := make(map[netip.Prefix]bool, len(set))
	for p := range set {
		if !covered(p, set) {
			out[p] = true
		}
	}
	return out
}

// covered reports whether a strictly broader prefix in the set contains p.
func covered(p netip.Prefix, set map[netip.Prefix]bool) bool {
	for bits := p.Bits() - 1; bits >= 0; bits-- {
		if set[netip.PrefixFrom(p.Addr(), bits).Masked()] {
			return true
		}
	}
	return false
}
