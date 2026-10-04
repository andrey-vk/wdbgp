package store

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strconv"
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

// SaveModeWithFeeds renames/enables a mode and replaces its feed membership in
// one transaction, so a failure part-way can't leave a renamed or re-fed mode
// behind a save that reported failure.
func (s *Store) SaveModeWithFeeds(ctx context.Context, mode CatalogMode, links []ModeFeedLink) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx,
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
		return replaceModeFeedsTx(ctx, tx, mode.ID, links)
	})
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
func (s *Store) DeleteCatalogMode(ctx context.Context, id int64, meta AuditMeta) (reassignedUserIDs []int64, err error) {
	if id <= 3 {
		return nil, fmt.Errorf("built-in catalog modes cannot be deleted")
	}
	err = s.Transaction(ctx, func(tx *sql.Tx) error {
		// Store.Transaction may invoke this closure more than once (it
		// retries on a transient SQLite lock error), so the IDs collected
		// here must stay attempt-local — appending straight to the named
		// return (reassignedUserIDs) would duplicate a prior failed
		// attempt's rows once a retry commits. Only published to the
		// named return just before this closure returns nil, i.e. only
		// for the attempt that actually commits.
		var attemptIDs []int64
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
				attemptIDs = append(attemptIDs, userID)
			}
			return rows.Err()
		}()
		if scanErr != nil {
			return scanErr
		}
		if deleteCatalogModeAttemptHook != nil {
			if err := deleteCatalogModeAttemptHook(attemptIDs); err != nil {
				return err
			}
		}

		// Read the name before the row is deleted, so the audit entries record
		// what the mode was called rather than a later reuse of its ID.
		deletedName, err := modeNameTx(ctx, tx, id)
		if err != nil {
			return err
		}
		// Deleting the mode cascades away every user's selection in it. Audit
		// each one first, so the change log can still say what the user had
		// selected in this mode when a sync reached it.
		selectionMeta := AuditMeta{Actor: meta.Actor, UserAgent: meta.UserAgent, Action: "user.selections_changed"}
		if err := auditDeletedModeSelectionsTx(ctx, tx, id, deletedName, selectionMeta); err != nil {
			return err
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
		// Every reassigned user gets its own audit row, inside this same
		// transaction — without this, those moves would have no trace in
		// the audit log despite user.mode_changed covering every other way
		// a user's mode can change.
		before := modeChangeAuditPayload{CatalogModeID: id, CatalogModeName: deletedName}
		fallbackName, err := modeNameTx(ctx, tx, 1)
		if err != nil {
			return err
		}
		after := modeChangeAuditPayload{CatalogModeID: 1, CatalogModeName: fallbackName}
		for _, userID := range attemptIDs {
			if err := AuditEntryTx(ctx, tx, meta, "user", strconv.FormatInt(userID, 10), before, after, false); err != nil {
				return err
			}
		}
		reassignedUserIDs = attemptIDs
		return nil
	})
	if err != nil {
		return nil, err
	}
	return reassignedUserIDs, nil
}

// deleteCatalogModeAttemptHook, if set, runs once per DeleteCatalogMode
// transaction attempt, right after that attempt's local reassignedUserIDs
// are collected and before the reassignment/delete writes. A test uses it
// to force a retry (returning an error retry.TransientError accepts) and
// confirm a later successful attempt doesn't duplicate an earlier failed
// attempt's collected IDs onto the published result.
var deleteCatalogModeAttemptHook func(attemptIDs []int64) error

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

// auditDeletedModeSelectionsTx writes a selection audit row, emptying the
// selection, for every user with selections in modeID. Called before the mode
// is deleted, since the deletion cascades those selections away.
func auditDeletedModeSelectionsTx(ctx context.Context, tx *sql.Tx, modeID int64, modeName string, meta AuditMeta) error {
	rows, err := tx.QueryContext(ctx, `
SELECT user_id FROM selected_categories WHERE mode_id = ?
UNION
SELECT user_id FROM selected_services WHERE mode_id = ?`, modeID, modeID)
	if err != nil {
		return err
	}
	var userIDs []int64
	for rows.Next() {
		var userID int64
		if err := rows.Scan(&userID); err != nil {
			_ = rows.Close() //nolint:errcheck
			return err
		}
		userIDs = append(userIDs, userID)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	empty := selectionAuditState(modeID, modeName, nil, nil)
	for _, userID := range userIDs {
		cats, svcs, err := userModeSelection(ctx, tx, userID, modeID)
		if err != nil {
			return err
		}
		before := selectionAuditState(modeID, modeName, cats, svcs)
		if err := AuditEntryTx(ctx, tx, meta, "user", strconv.FormatInt(userID, 10), before, empty, false); err != nil {
			return err
		}
	}
	return nil
}
