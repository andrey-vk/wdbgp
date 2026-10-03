package store

import (
	"context"
	"database/sql"
	"sort"
)

// prefixAssociation is one prefix as a mode announces it, with the categories
// whose services provide it. Selections are per category, so the same prefix
// under a new category is growth for a user who selects only that category.
type prefixAssociation struct {
	cidr       string
	categories map[string]bool
}

// ModePrefixCategories maps mode → prefix id → how the mode announces it.
type ModePrefixCategories map[int64]map[int64]*prefixAssociation

// ModeCategoryGrowth lists the prefixes a sync newly announced in a mode
// through one category.
type ModeCategoryGrowth struct {
	ModeID   int64
	Category string
	Prefixes []string
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
			a := prefixes[prefixID]
			if a == nil {
				a = &prefixAssociation{cidr: prefix.String(), categories: map[string]bool{}}
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
// that weren't announced through that category before it.
func ModeGrowth(before, after ModePrefixCategories) []ModeCategoryGrowth {
	type key struct {
		mode     int64
		category string
	}
	added := map[key][]string{}
	for modeID, afterPrefixes := range after {
		for prefixID, a := range afterPrefixes {
			prior := before[modeID][prefixID]
			for category := range a.categories {
				if prior != nil && prior.categories[category] {
					continue
				}
				k := key{modeID, category}
				added[k] = append(added[k], a.cidr)
			}
		}
	}
	out := make([]ModeCategoryGrowth, 0, len(added))
	for k, prefixes := range added {
		sort.Strings(prefixes)
		out = append(out, ModeCategoryGrowth{ModeID: k.mode, Category: k.category, Prefixes: prefixes})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ModeID != out[j].ModeID {
			return out[i].ModeID < out[j].ModeID
		}
		return out[i].Category < out[j].Category
	})
	return out
}
