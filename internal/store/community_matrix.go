package store

import (
	"context"
	"database/sql"
	"sort"
	"strconv"
)

// CommunityMatrixRow is one category's group-level community in each mode.
// Values is keyed by mode ID; a nil value means the category has no group-level
// community in that mode. Divergent is set when the modes don't all agree: a
// different number in two modes, or a mode without one.
type CommunityMatrixRow struct {
	Category string            `json:"category"`
	Values   map[int64]*uint32 `json:"values"`
	// ServiceDivergence counts the category's services whose number differs
	// between modes, or that are served by some modes but not others. The group
	// number alone can match while a service inside the category doesn't.
	ServiceDivergence int  `json:"service_divergence"`
	Divergent         bool `json:"divergent"`
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
	var servedServices map[int64]map[ServiceKey]bool
	err := s.Transaction(ctx, func(tx *sql.Tx) error {
		modes, err := catalogModes(ctx, tx, false)
		if err != nil {
			return err
		}
		snaps := make(map[int64]ModeCommunitySnapshot, len(modes))
		served = make(map[int64]map[string]bool, len(modes))
		servedServices = make(map[int64]map[ServiceKey]bool, len(modes))
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
			svcs, err := servicesServedTx(ctx, tx, mode.ID)
			if err != nil {
				return err
			}
			servedServices[mode.ID] = svcs
			kept := rows[:0]
			for _, row := range rows {
				if row.Service == "" && present[row.Category] {
					kept = append(kept, row)
				} else if row.Service != "" && svcs[ServiceKey{Category: row.Category, Service: row.Service}] {
					kept = append(kept, row)
				}
			}
			snaps[mode.ID] = ModeCommunitySnapshot{Communities: kept}
		}
		matrix = buildCommunityMatrix(modes, snaps, served, servedServices)
		return nil
	})
	return matrix, err
}

// buildCommunityMatrix is CommunityMatrix's pure part. Categories are sorted by
// name. A category appears if any mode serves it, even one with no stored
// group-level community yet, which then shows as missing in that mode. served
// may be nil, in which case only categories with stored numbers appear.
func buildCommunityMatrix(modes []CatalogMode, snaps map[int64]ModeCommunitySnapshot, served map[int64]map[string]bool, servedServices map[int64]map[ServiceKey]bool) CommunityMatrix {
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
	serviceDivergence := serviceDivergenceByCategory(modes, snaps, servedServices)
	names := make([]string, 0, len(byCategory))
	for name := range byCategory {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		row := CommunityMatrixRow{Category: name, Values: map[int64]*uint32{}, ServiceDivergence: serviceDivergence[name]}
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
		if len(distinct) > 1 || row.ServiceDivergence > 0 {
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

// serviceDivergenceByCategory counts, per category, the services that don't
// agree across modes: a service served by some modes but not others, or served
// by all but with different numbers, or without a number in a mode serving it.
func serviceDivergenceByCategory(modes []CatalogMode, snaps map[int64]ModeCommunitySnapshot, servedServices map[int64]map[ServiceKey]bool) map[string]int {
	universe := map[ServiceKey]bool{}
	for _, mode := range modes {
		for svc := range servedServices[mode.ID] {
			universe[svc] = true
		}
	}
	numbers := map[ServiceKey]map[int64]uint32{}
	for _, mode := range modes {
		for _, c := range snaps[mode.ID].Communities {
			if c.Service == "" {
				continue
			}
			key := ServiceKey{Category: c.Category, Service: c.Service}
			if numbers[key] == nil {
				numbers[key] = map[int64]uint32{}
			}
			numbers[key][mode.ID] = c.Community
		}
	}
	out := map[string]int{}
	for svc := range universe {
		distinct := map[uint32]bool{}
		diverges := false
		for _, mode := range modes {
			if !servedServices[mode.ID][svc] {
				diverges = true
				continue
			}
			v, ok := numbers[svc][mode.ID]
			if !ok {
				diverges = true
				continue
			}
			distinct[v] = true
		}
		if diverges || len(distinct) > 1 {
			out[svc.Category]++
		}
	}
	return out
}

// servicesServedTx returns the services a mode currently serves, from its
// materialized entries.
func servicesServedTx(ctx context.Context, tx *sql.Tx, modeID int64) (map[ServiceKey]bool, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT DISTINCT c.name, sv.name FROM catalog_mode_entries cme
JOIN services sv ON sv.id = cme.service_id
JOIN categories c ON c.id = sv.category_id
WHERE cme.mode_id = ?`, modeID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck
	out := map[ServiceKey]bool{}
	for rows.Next() {
		var k ServiceKey
		if err := rows.Scan(&k.Category, &k.Service); err != nil {
			return nil, err
		}
		out[k] = true
	}
	return out, rows.Err()
}

// GenerateAllMissingCommunities fills in every mode's missing group- and
// service-level community numbers in one transaction, auditing each mode
// that actually changed. Opening the matrix itself never does this (it only
// reads stored numbers, to keep opening it cheap on a large catalog), so a
// mode nobody has opened recently can show categories as "missing" there
// even though nothing is wrong — this is the explicit action that clears
// that, the same generation the single-mode "Regenerate missing" uses.
func (s *Store) GenerateAllMissingCommunities(ctx context.Context, meta AuditMeta) (count int, err error) {
	err = s.Transaction(ctx, func(tx *sql.Tx) error {
		modes, err := catalogModes(ctx, tx, false)
		if err != nil {
			return err
		}
		before := make(map[int64][]Community, len(modes))
		for _, mode := range modes {
			rows, err := communityRows(ctx, tx, mode.ID)
			if err != nil {
				return err
			}
			before[mode.ID] = rows
		}
		count, err = genCommunitiesRuntime(ctx, tx, 0)
		if err != nil {
			return err
		}
		for _, mode := range modes {
			after, err := communityRows(ctx, tx, mode.ID)
			if err != nil {
				return err
			}
			changedBefore, changedAfter := diffCommunityRows(before[mode.ID], after)
			if err := AuditEntryTx(ctx, tx, meta, "mode", strconv.FormatInt(mode.ID, 10),
				boundCommunitiesForAudit(changedBefore), boundCommunitiesForAudit(changedAfter), false); err != nil {
				return err
			}
		}
		return nil
	})
	return count, err
}
