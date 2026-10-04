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
	err := s.Transaction(ctx, func(tx *sql.Tx) error {
		modes, err := catalogModes(ctx, tx, false)
		if err != nil {
			return err
		}
		snaps := make(map[int64]ModeCommunitySnapshot, len(modes))
		for _, mode := range modes {
			rows, err := communityRows(ctx, tx, mode.ID)
			if err != nil {
				return err
			}
			snaps[mode.ID] = ModeCommunitySnapshot{Communities: rows}
		}
		matrix = buildCommunityMatrix(modes, snaps)
		return nil
	})
	return matrix, err
}

// buildCommunityMatrix is CommunityMatrix's pure part. Categories are sorted by
// name, and a category appears if any mode gives it a group-level community.
func buildCommunityMatrix(modes []CatalogMode, snaps map[int64]ModeCommunitySnapshot) CommunityMatrix {
	matrix := CommunityMatrix{Modes: modes, Categories: []CommunityMatrixRow{}}
	byCategory := map[string]map[int64]uint32{}
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
