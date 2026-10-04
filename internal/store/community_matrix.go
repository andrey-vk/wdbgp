package store

import (
	"context"
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

// CommunityMatrix builds the matrix from every mode's communities, read in one
// transaction so the numbers describe one moment across all modes.
func (s *Store) CommunityMatrix(ctx context.Context) (CommunityMatrix, error) {
	modes, snaps, err := s.AllModeCommunitySnapshots(ctx, false)
	if err != nil {
		return CommunityMatrix{}, err
	}
	return buildCommunityMatrix(modes, snaps), nil
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
