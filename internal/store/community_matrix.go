package store

import (
	"context"
	"database/sql"
	"sort"
)

// CommunityMatrixRow is one category's group-level community in each mode.
// Values is keyed by mode ID; a nil value means the category has no group-level
// community in that mode. Divergent is set when the modes don't all agree: a
// different number in two modes, or a mode without one.
type CommunityMatrixRow struct {
	Category  string            `json:"category"`
	Values    map[int64]*uint32 `json:"values"`
	Divergent bool              `json:"divergent"`
}

// CommunityMatrix shows, per category, the community number every mode gives
// it, so per-mode numbering differences are visible before a user moves between
// modes and their router configuration would change.
type CommunityMatrix struct {
	Modes      []CatalogMode        `json:"modes"`
	Categories []CommunityMatrixRow `json:"categories"`
}

// CommunityMatrix builds the matrix from the stored communities of every mode,
// read in one transaction so the numbers describe one moment across all modes.
// It reads only what the grid shows: no prefix counts, and no generation of
// missing assignments, so a page view never writes or scans whole catalogs.
// A category with no stored group-level number shows as missing.
func (s *Store) CommunityMatrix(ctx context.Context) (CommunityMatrix, error) {
	var matrix CommunityMatrix
	var served map[int64]map[string]bool
	err := s.Transaction(ctx, func(tx *sql.Tx) error {
		modes, err := catalogModes(ctx, tx, false)
		if err != nil {
			return err
		}
		snaps := make(map[int64]ModeCommunitySnapshot, len(modes))
		served = make(map[int64]map[string]bool, len(modes))
		for _, mode := range modes {
			rows, err := communityRows(ctx, tx, mode.ID)
			if err != nil {
				return err
			}
			// A feed removed from a mode leaves its community rows behind, so only
			// categories the mode currently serves count as its numbering.
			present, err := categoriesServedTx(ctx, tx, mode.ID)
			if err != nil {
				return err
			}
			served[mode.ID] = present
			kept := rows[:0]
			for _, row := range rows {
				if present[row.Category] {
					kept = append(kept, row)
				}
			}
			snaps[mode.ID] = ModeCommunitySnapshot{Communities: kept}
		}
		matrix = buildCommunityMatrix(modes, snaps, served)
		return nil
	})
	return matrix, err
}

// buildCommunityMatrix is CommunityMatrix's pure part. Categories are sorted by
// name. A category appears if any mode serves it, even one with no stored
// group-level community yet, which then shows as missing in that mode. served
// may be nil, in which case only categories with stored numbers appear.
func buildCommunityMatrix(modes []CatalogMode, snaps map[int64]ModeCommunitySnapshot, served map[int64]map[string]bool) CommunityMatrix {
	matrix := CommunityMatrix{Modes: modes, Categories: []CommunityMatrixRow{}}
	byCategory := map[string]map[int64]uint32{}
	for _, mode := range modes {
		for name := range served[mode.ID] {
			if byCategory[name] == nil {
				byCategory[name] = map[int64]uint32{}
			}
		}
	}
	for _, mode := range modes {
		for _, c := range snaps[mode.ID].Communities {
			if c.Service != "" {
				continue
			}
			if byCategory[c.Category] == nil {
				byCategory[c.Category] = map[int64]uint32{}
			}
			byCategory[c.Category][mode.ID] = c.Community
		}
	}
	names := make([]string, 0, len(byCategory))
	for name := range byCategory {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		row := CommunityMatrixRow{Category: name, Values: map[int64]*uint32{}}
		distinct := map[uint32]bool{}
		for _, mode := range modes {
			if v, ok := byCategory[name][mode.ID]; ok {
				value := v
				row.Values[mode.ID] = &value
				distinct[v] = true
			} else {
				row.Values[mode.ID] = nil
				row.Divergent = true
			}
		}
		if len(distinct) > 1 {
			row.Divergent = true
		}
		matrix.Categories = append(matrix.Categories, row)
	}
	return matrix
}

// categoriesServedTx returns the categories a mode currently serves, from its
// materialized entries.
func categoriesServedTx(ctx context.Context, tx *sql.Tx, modeID int64) (map[string]bool, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT DISTINCT c.name FROM catalog_mode_entries cme
JOIN services sv ON sv.id = cme.service_id
JOIN categories c ON c.id = sv.category_id
WHERE cme.mode_id = ?`, modeID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck
	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}
