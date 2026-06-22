package migrations

import (
	"context"
	"database/sql"
)

func V013(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `
CREATE TABLE catalog_communities (
    mode_id   INTEGER NOT NULL REFERENCES catalog_modes(id) ON DELETE CASCADE,
    category  TEXT NOT NULL,
    service   TEXT NOT NULL DEFAULT '',
    community INTEGER NOT NULL CHECK(community > 0 AND community <= 4294967295),
    PRIMARY KEY (mode_id, category, service)
);
CREATE INDEX idx_catalog_communities_mode ON catalog_communities(mode_id);
CREATE INDEX idx_catalog_communities_value ON catalog_communities(community);
`); err != nil {
		return err
	}
	return autoGenerateCommunities(tx)
}

// autoGenerateCommunities fills communities for all existing catalog data.
func autoGenerateCommunities(tx *sql.Tx) error {
	_, err := genCommunities(tx, nil, 0)
	return err
}

// findFirstFree returns the first integer >= start that is not in used.
func findFirstFree(start uint32, used map[uint32]bool) uint32 {
	for used[start] {
		start++
	}
	return start
}

// genCommunities generates communities for categories and services that don't have one yet.
// If existing is non-nil, it's used as the set of already-assigned keys to skip.
// If modeID is 0, generates for all modes; otherwise only for the specified mode.
func genCommunities(tx *sql.Tx, existing map[string]bool, modeID int64) (int, error) {
	var modeIDs []int64
	if modeID > 0 {
		modeIDs = []int64{modeID}
	} else {
		modes, err := tx.Query("SELECT DISTINCT id FROM catalog_modes ORDER BY id")
		if err != nil {
			return 0, err
		}
		defer func() { _ = modes.Close() }()

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
		// Load all currently-assigned communities for this mode.
		commRows, err := tx.Query(
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
				_ = commRows.Close()
				return 0, err
			}
			used[community] = true
			if service == "" {
				keyComm["grp:"+category] = community
			} else {
				keyComm["svc:"+category+"|"+service] = community
			}
		}
		_ = commRows.Close()

		// Get categories in alphabetical order.
		catRows, err := tx.Query(`
SELECT DISTINCT ce.category
FROM catalog_entries ce
JOIN feeds f ON f.id = ce.feed_id
WHERE f.mode_id = ?
ORDER BY ce.category`, mid)
		if err != nil {
			return 0, err
		}

		var categories []string
		for catRows.Next() {
			var cat string
			if err := catRows.Scan(&cat); err != nil {
				_ = catRows.Close()
				return 0, err
			}
			categories = append(categories, cat)
		}
		_ = catRows.Close()

		groupIndex := 0
		for _, category := range categories {
			groupKey := "grp:" + category

			// Determine group community: use existing assignment or find a free one.
			var groupCommunity uint32
			if existing != nil && existing[groupKey] {
				var ok bool
				groupCommunity, ok = keyComm[groupKey]
				if !ok {
					// Existing group expected to have a community; skip if missing.
					groupIndex++
					continue
				}
			} else {
				groupCommunity = findFirstFree(uint32((groupIndex+1)*10000), used)
				if _, err := tx.Exec(
					"INSERT OR IGNORE INTO catalog_communities(mode_id, category, service, community) VALUES (?, ?, '', ?)",
					mid, category, groupCommunity); err != nil {
					return generated, err
				}
				used[groupCommunity] = true
				generated++
			}

			// Get services in alphabetical order.
			svcRows, err := tx.Query(`
SELECT DISTINCT ce.service
FROM catalog_entries ce
JOIN feeds f ON f.id = ce.feed_id
WHERE f.mode_id = ? AND ce.category = ?
ORDER BY ce.service`, mid, category)
			if err != nil {
				return generated, err
			}

			var services []string
			for svcRows.Next() {
				var svc string
				if err := svcRows.Scan(&svc); err != nil {
					_ = svcRows.Close()
					return generated, err
				}
				services = append(services, svc)
			}
			_ = svcRows.Close()

			for _, service := range services {
				svcKey := "svc:" + category + "|" + service
				if existing != nil && existing[svcKey] {
					continue
				}
				// Find first free community starting from group_community+1.
				// Each insertion adds to used, so subsequent services naturally
				// get the next free number.  Overflow past 9999 services spills
				// into the next block — findFirstFree skips any used numbers.
				svcCommunity := findFirstFree(groupCommunity+1, used)

				if _, err := tx.Exec(
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
