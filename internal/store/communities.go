package store

import (
	"context"
	"database/sql"
	"fmt"
	"log"
)

// Community represents a catalog community assignment.
type Community struct {
	ModeID    int64
	Category  string
	Service   string // empty = group-level
	Community uint32
}

// findFirstFree returns the first integer >= start that is not in used.
func findFirstFree(start uint32, used map[uint32]bool) uint32 {
	for used[start] {
		start++
	}
	return start
}

// AutoCommunity returns the auto-generated community number for a given
// service position within a group. This is a positional estimate used for
// UI display; actual assignment uses findFirstFree. Not valid for a group's
// own (category-level) entry — use AutoGroupCommunity for that, since a
// group's base value has no "+1 for the first service" offset applied to it.
func AutoCommunity(groupIndex int, serviceIndex int) uint32 {
	for serviceIndex >= 9999 {
		groupIndex++
		serviceIndex -= 9999
	}
	groupCommunity := (groupIndex + 1) * 10000
	return uint32(groupCommunity + serviceIndex + 1) //nolint:gosec // community values fit in uint32
}

// AutoGroupCommunity returns the auto-generated community number for a
// category's own group-level entry — the group base itself (e.g. 10000 for
// the first category), same positional-estimate caveat as AutoCommunity.
func AutoGroupCommunity(groupIndex int) uint32 {
	return uint32((groupIndex + 1) * 10000) //nolint:gosec // community values fit in uint32
}

// GetCommunities returns all communities for a mode.
// Map key: category for groups, "category|service" for services.
func (s *Store) GetCommunities(ctx context.Context, modeID int64) (map[string]uint32, error) {
	rows, err := s.DB.QueryContext(ctx,
		"SELECT category, service, community FROM catalog_communities WHERE mode_id = ? ORDER BY category, service",
		modeID)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("WARNING: rows close: %v", err)
		}
	}()
	result := make(map[string]uint32)
	for rows.Next() {
		var category, service string
		var community uint32
		if err := rows.Scan(&category, &service, &community); err != nil {
			return nil, err
		}
		if service == "" {
			result[category] = community
		} else {
			result[category+"|"+service] = community
		}
	}
	return result, rows.Err()
}

// SetCommunity upserts a community. service="" means group-level.
func (s *Store) SetCommunity(ctx context.Context, modeID int64, category, service string, community uint32) error {
	// Check for duplicate community value
	var existing int
	err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM catalog_communities
		WHERE mode_id = ? AND community = ? AND NOT (category = ? AND service = ?)`,
		modeID, community, category, service).Scan(&existing)
	if err != nil {
		return err
	}
	if existing > 0 {
		return fmt.Errorf("community %d is already used by another category or service in this mode", community)
	}
	// Upsert
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO catalog_communities(mode_id, category, service, community) VALUES (?, ?, ?, ?)
ON CONFLICT(mode_id, category, service) DO UPDATE SET community = excluded.community`,
		modeID, category, service, community)
	return err
}

// DeleteCommunity removes a manual community override.
// After deletion, GenerateCommunities fills the auto value.
func (s *Store) DeleteCommunity(ctx context.Context, modeID int64, category, service string) error {
	_, err := s.DB.ExecContext(ctx,
		`DELETE FROM catalog_communities WHERE mode_id = ? AND category = ? AND service = ?`,
		modeID, category, service)
	return err
}

// GenerateCommunities fills missing communities for all categories/services in a mode.
// Uses the 10000*gap scheme. Skips categories/services that already have a community.
// Returns count of newly generated communities.
func (s *Store) GenerateCommunities(ctx context.Context, modeID int64) (int, error) {
	var count int
	err := s.Transaction(ctx, func(tx *sql.Tx) error {
		var err2 error
		count, err2 = genCommunitiesRuntime(ctx, tx, modeID)
		return err2
	})
	return count, err
}

// genCommunitiesRuntime generates communities using catalog_mode_feeds (post-migration-20).
// Used by GenerateCommunities during normal runtime operation. Which
// categories/services already have a community is read once per mode from
// catalog_communities (into keyComm below) — there is no separate
// "existing" pre-check query, since that would just be reading the same
// table under the same mode_id filter a second time in the same transaction.
func genCommunitiesRuntime(ctx context.Context, tx *sql.Tx, modeID int64) (int, error) {
	var modeIDs []int64
	if modeID > 0 {
		modeIDs = []int64{modeID}
	} else {
		modes, err := tx.QueryContext(ctx, "SELECT DISTINCT id FROM catalog_modes ORDER BY id")
		if err != nil {
			return 0, err
		}
		defer func() {
			if err := modes.Close(); err != nil {
				log.Printf("WARNING: modes close: %v", err)
			}
		}()

		for modes.Next() {
			var id int64
			if err := modes.Scan(&id); err != nil {
				return 0, err
			}
			modeIDs = append(modeIDs, id)
		}
		if err := modes.Err(); err != nil {
			return 0, err
		}
	}

	generated := 0
	for _, mid := range modeIDs {
		commRows, err := tx.QueryContext(ctx,
			"SELECT category, service, community FROM catalog_communities WHERE mode_id = ? ORDER BY community",
			mid)
		if err != nil {
			return 0, err
		}
		used := make(map[uint32]bool)
		keyComm := make(map[string]uint32)
		for commRows.Next() {
			var category, service string
			var community uint32
			if err := commRows.Scan(&category, &service, &community); err != nil {
				if err := commRows.Close(); err != nil {
					log.Printf("WARNING: commRows close: %v", err)
				}
				return 0, err
			}
			used[community] = true
			if service == "" {
				keyComm["grp:"+category] = community
			} else {
				keyComm["svc:"+category+"|"+service] = community
			}
		}
		if err := commRows.Err(); err != nil {
			if cerr := commRows.Close(); cerr != nil {
				log.Printf("WARNING: commRows close: %v", cerr)
			}
			return 0, err
		}
		if err := commRows.Close(); err != nil {
			log.Printf("WARNING: commRows close: %v", err)
		}

		// One query for every (category, service) pair in the mode, instead
		// of a DISTINCT category query followed by a per-category DISTINCT
		// service query — the per-category query was an N+1: one extra
		// round trip for every category in the mode.
		entryRows, err := tx.QueryContext(ctx, `
SELECT DISTINCT ce.category, ce.service
FROM catalog_entries ce
JOIN feeds f ON f.id = ce.feed_id
JOIN catalog_mode_feeds cmf ON cmf.feed_id = f.id
WHERE cmf.mode_id = ?
ORDER BY ce.category, ce.service`, mid)
		if err != nil {
			return 0, err
		}

		var categories []string
		servicesByCategory := make(map[string][]string)
		for entryRows.Next() {
			var category, service string
			if err := entryRows.Scan(&category, &service); err != nil {
				if err := entryRows.Close(); err != nil {
					log.Printf("WARNING: entryRows close: %v", err)
				}
				return generated, err
			}
			// Rows are ordered by category, so all of a category's rows are
			// contiguous — a new category starts whenever it differs from
			// the last one appended.
			if len(categories) == 0 || categories[len(categories)-1] != category {
				categories = append(categories, category)
			}
			servicesByCategory[category] = append(servicesByCategory[category], service)
		}
		if err := entryRows.Err(); err != nil {
			if cerr := entryRows.Close(); cerr != nil {
				log.Printf("WARNING: entryRows close: %v", cerr)
			}
			return generated, err
		}
		if err := entryRows.Close(); err != nil {
			log.Printf("WARNING: entryRows close: %v", err)
		}

		groupIndex := 0
		for _, category := range categories {
			groupKey := "grp:" + category

			groupCommunity, ok := keyComm[groupKey]
			if !ok {
				groupCommunity = findFirstFree(uint32((groupIndex+1)*10000), used)
				if _, err := tx.ExecContext(ctx,
					"INSERT OR IGNORE INTO catalog_communities(mode_id, category, service, community) VALUES (?, ?, '', ?)",
					mid, category, groupCommunity); err != nil {
					return generated, err
				}
				used[groupCommunity] = true
				generated++
			}

			for _, service := range servicesByCategory[category] {
				svcKey := "svc:" + category + "|" + service
				if _, ok := keyComm[svcKey]; ok {
					continue
				}
				svcCommunity := findFirstFree(groupCommunity+1, used)

				if _, err := tx.ExecContext(ctx,
					"INSERT OR IGNORE INTO catalog_communities(mode_id, category, service, community) VALUES (?, ?, ?, ?)",
					mid, category, service, svcCommunity); err != nil {
					return generated, err
				}
				used[svcCommunity] = true
				generated++
			}
			groupIndex++
		}
	}
	return generated, nil
}
