package store

import (
	"context"
	"database/sql"
)

// ModePrefixCategories maps mode → prefix → the categories whose services
// provide that prefix in the mode's materialized catalog.
type ModePrefixCategories map[int64]map[int64]map[string]bool

// ModeCategoryGrowth is how many distinct prefixes became announced in a mode
// through one category during a sync.
type ModeCategoryGrowth struct {
	ModeID   int64
	Category string
	Added    int
}

// SnapshotFeedModePrefixesTx captures, for every mode the feed is linked to,
// which prefixes the mode's materialized catalog announces and through which
// categories. Taken before and after a sync, it shows what the sync actually
// made newly announced, after deduplication and exclude feeds.
func SnapshotFeedModePrefixesTx(ctx context.Context, tx *sql.Tx, feedID int64) (ModePrefixCategories, error) {
	modeIDs, err := feedModeIDsTx(ctx, tx, feedID)
	if err != nil {
		return nil, err
	}
	snap := ModePrefixCategories{}
	for _, modeID := range modeIDs {
		rows, err := tx.QueryContext(ctx, `
SELECT DISTINCT cme.prefix_id, c.name
FROM catalog_mode_entries cme
JOIN services sv ON sv.id = cme.service_id
JOIN categories c ON c.id = sv.category_id
WHERE cme.mode_id = ?`, modeID)
		if err != nil {
			return nil, err
		}
		prefixes := map[int64]map[string]bool{}
		for rows.Next() {
			var prefixID int64
			var category string
			if err := rows.Scan(&prefixID, &category); err != nil {
				_ = rows.Close() //nolint:errcheck
				return nil, err
			}
			if prefixes[prefixID] == nil {
				prefixes[prefixID] = map[string]bool{}
			}
			prefixes[prefixID][category] = true
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

// ModeGrowth lists, per mode and category, the prefixes that are announced
// after a sync but weren't before it.
func ModeGrowth(before, after ModePrefixCategories) []ModeCategoryGrowth {
	var out []ModeCategoryGrowth
	for modeID, afterPrefixes := range after {
		beforePrefixes := before[modeID]
		added := map[string]int{}
		for prefixID, categories := range afterPrefixes {
			if _, existed := beforePrefixes[prefixID]; existed {
				continue
			}
			for category := range categories {
				added[category]++
			}
		}
		for category, n := range added {
			out = append(out, ModeCategoryGrowth{ModeID: modeID, Category: category, Added: n})
		}
	}
	return out
}
