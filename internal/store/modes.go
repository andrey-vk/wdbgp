package store

import (
	"context"
	"database/sql"
	"fmt"
	"log"
)

// CatalogMode represents a catalog mode (OpenCCK, IPRanges, sing-box SRS, etc.).
type CatalogMode struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

func (s *Store) CatalogModes(ctx context.Context, enabledOnly bool) ([]CatalogMode, error) {
	return catalogModes(ctx, s.DB, enabledOnly)
}

// catalogModes is CatalogModes' implementation, parameterized on queryer —
// see catalogForMode's doc comment for why.
func catalogModes(ctx context.Context, q queryer, enabledOnly bool) ([]CatalogMode, error) {
	query := "SELECT id, name, enabled FROM catalog_modes"
	if enabledOnly {
		query += " WHERE enabled = 1"
	}
	query += " ORDER BY id"
	rows, err := q.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("WARNING: rows close: %v", err)
		}
	}()
	var modes []CatalogMode
	for rows.Next() {
		var mode CatalogMode
		if err := rows.Scan(&mode.ID, &mode.Name, &mode.Enabled); err != nil {
			return nil, err
		}
		modes = append(modes, mode)
	}
	return modes, rows.Err()
}

func (s *Store) CatalogMode(ctx context.Context, id int64) (CatalogMode, error) {
	var mode CatalogMode
	err := s.DB.QueryRowContext(ctx,
		"SELECT id, name, enabled FROM catalog_modes WHERE id = ?", id).
		Scan(&mode.ID, &mode.Name, &mode.Enabled)
	return mode, err
}

func (s *Store) UpdateCatalogMode(ctx context.Context, mode CatalogMode) error {
	result, err := s.DB.ExecContext(ctx,
		"UPDATE catalog_modes SET name = ?, enabled = ? WHERE id = ?",
		mode.Name, mode.Enabled, mode.ID)
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

func (s *Store) AddCatalogMode(ctx context.Context, name string, enabled bool) (int64, error) {
	result, err := s.DB.ExecContext(ctx,
		"INSERT INTO catalog_modes(name, enabled) VALUES (?, ?)",
		name, enabled)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// DeleteCatalogMode deletes mode id, reassigning any user still pointing at
// it to the default mode (id=1) first to avoid a foreign-key violation.
// Returns the IDs of users actually reassigned, read inside the same
// transaction as the reassignment and the delete — so a caller logging an
// audit entry per reassigned user sees exactly the set this call itself
// moved, not a separately-queried snapshot a concurrent request could
// change out from under it.
func (s *Store) DeleteCatalogMode(ctx context.Context, id int64) (reassignedUserIDs []int64, err error) {
	if id <= 3 {
		return nil, fmt.Errorf("built-in catalog modes cannot be deleted")
	}
	err = s.Transaction(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, "SELECT id FROM users WHERE catalog_mode_id = ?", id)
		if err != nil {
			return err
		}
		scanErr := func() error {
			defer func() {
				if err := rows.Close(); err != nil {
					log.Printf("WARNING: rows close: %v", err)
				}
			}()
			for rows.Next() {
				var userID int64
				if err := rows.Scan(&userID); err != nil {
					return err
				}
				reassignedUserIDs = append(reassignedUserIDs, userID)
			}
			return rows.Err()
		}()
		if scanErr != nil {
			return scanErr
		}

		if _, err := tx.ExecContext(ctx,
			"UPDATE users SET catalog_mode_id = 1 WHERE catalog_mode_id = ?", id); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, "DELETE FROM catalog_modes WHERE id = ?", id)
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
	if err != nil {
		return nil, err
	}
	return reassignedUserIDs, nil
}

// ModeFeedCounts returns a map of mode_id→feed count.
func (s *Store) ModeFeedCounts(ctx context.Context) (map[int64]int, error) {
	rows, err := s.DB.QueryContext(ctx,
		"SELECT mode_id, COUNT(*) FROM catalog_mode_feeds GROUP BY mode_id")
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("WARNING: rows close: %v", err)
		}
	}()
	counts := make(map[int64]int)
	for rows.Next() {
		var modeID int64
		var count int
		if err := rows.Scan(&modeID, &count); err != nil {
			return nil, err
		}
		counts[modeID] = count
	}
	return counts, rows.Err()
}

// ModeFeeds returns all feeds associated with a catalog mode.
// ModeFeed is a feed as linked to a mode, carrying the link's role:
// Exclude=false contributes the feed's entries to the mode's catalog,
// Exclude=true subtracts its prefixes from the mode's built list.
type ModeFeed struct {
	Feed
	Exclude bool
}

func (s *Store) ModeFeeds(ctx context.Context, modeID int64) ([]ModeFeed, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT f.id, f.name, f.url, f.adapter_id, f.enabled,
       COALESCE(f.sync_interval, 0),
       COALESCE(f.data, ''),
       f.allowed_hosts, f.restrict_hosts,
       COALESCE(f.last_success, 0), COALESCE(f.last_error, ''),
       cmf.exclude
FROM feeds f
JOIN catalog_mode_feeds cmf ON cmf.feed_id = f.id
WHERE cmf.mode_id = ?
ORDER BY f.id`, modeID)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("WARNING: rows close: %v", err)
		}
	}()
	var feeds []ModeFeed
	for rows.Next() {
		var feed ModeFeed
		if err := rows.Scan(
			&feed.ID, &feed.Name, &feed.URL, &feed.AdapterID,
			&feed.Enabled, &feed.SyncInterval, &feed.Data,
			&feed.AllowedHosts, &feed.RestrictHosts,
			&feed.LastSuccess, &feed.LastError,
			&feed.Exclude,
		); err != nil {
			return nil, err
		}
		feeds = append(feeds, feed)
	}
	return feeds, rows.Err()
}

// AddFeedToMode links a feed to a mode with the given role, updating the
// role if the link already exists, and rebuilds the mode's materialized
// entries — one transaction, so the link and the materialization never
// disagree.
func (s *Store) AddFeedToMode(ctx context.Context, modeID, feedID int64, exclude bool) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO catalog_mode_feeds(mode_id, feed_id, exclude) VALUES (?, ?, ?)
ON CONFLICT(mode_id, feed_id) DO UPDATE SET exclude = excluded.exclude`,
			modeID, feedID, exclude); err != nil {
			return err
		}
		return rebuildModeEntriesTx(ctx, tx, modeID)
	})
}

func (s *Store) RemoveFeedFromMode(ctx context.Context, modeID, feedID int64) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx,
			"DELETE FROM catalog_mode_feeds WHERE mode_id = ? AND feed_id = ?",
			modeID, feedID)
		if err != nil {
			return err
		}
		if count, err := result.RowsAffected(); err != nil {
			return fmt.Errorf("rows affected: %w", err)
		} else if count == 0 {
			return sql.ErrNoRows
		}
		return rebuildModeEntriesTx(ctx, tx, modeID)
	})
}
