package store

import (
	"context"
	"database/sql"
)

// Feed represents a data feed source.
type Feed struct {
	ID           int64
	Name         string
	URL          string
	ModeID       int64
	AdapterID    int64
	Enabled      bool
	SyncInterval int
	Data         string // JSON parameterization for adapters
	LastSuccess  string
	LastError    string
}

func (s *Store) Feeds(ctx context.Context, enabledOnly bool) ([]Feed, error) {
	query := `SELECT f.id, f.name, f.url,
	                 COALESCE((SELECT cmf.mode_id FROM catalog_mode_feeds cmf WHERE cmf.feed_id = f.id LIMIT 1), 0) as mode_id,
	                 f.adapter_id, f.enabled,
	                 COALESCE(f.sync_interval, 0),
	                 COALESCE(f.data, ''),
	                 COALESCE(f.last_success, ''), COALESCE(f.last_error, '')
	          FROM feeds f
	          JOIN feed_adapters a ON a.id = f.adapter_id`
	if enabledOnly {
		query += ` WHERE f.enabled = 1
		           AND EXISTS (SELECT 1 FROM catalog_mode_feeds cmf
		                       JOIN catalog_modes m ON m.id = cmf.mode_id
		                       WHERE cmf.feed_id = f.id AND m.enabled = 1)`
	}
	query += " ORDER BY f.id"
	rows, err := s.DB.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var feeds []Feed
	for rows.Next() {
		var feed Feed
		if err := rows.Scan(
			&feed.ID, &feed.Name, &feed.URL, &feed.ModeID, &feed.AdapterID,
			&feed.Enabled, &feed.SyncInterval, &feed.Data, &feed.LastSuccess, &feed.LastError,
		); err != nil {
			return nil, err
		}
		feeds = append(feeds, feed)
	}
	return feeds, rows.Err()
}

func (s *Store) Feed(ctx context.Context, id int64) (Feed, error) {
	var feed Feed
	err := s.DB.QueryRowContext(ctx, `
SELECT id, name, url,
       COALESCE((SELECT cmf.mode_id FROM catalog_mode_feeds cmf WHERE cmf.feed_id = feeds.id LIMIT 1), 0) as mode_id,
       adapter_id, enabled,
       COALESCE(sync_interval, 0),
       COALESCE(data, ''),
       COALESCE(last_success, ''), COALESCE(last_error, '')
FROM feeds
WHERE id = ?`, id).Scan(
		&feed.ID, &feed.Name, &feed.URL, &feed.ModeID, &feed.AdapterID,
		&feed.Enabled, &feed.SyncInterval, &feed.Data, &feed.LastSuccess, &feed.LastError,
	)
	return feed, err
}

func (s *Store) AddFeed(ctx context.Context, name, url string, enabled bool, syncInterval int) error {
	return s.AddFeedForMode(ctx, name, url, DefaultCatalogModeID, enabled, syncInterval)
}

func (s *Store) AddFeedForMode(
	ctx context.Context,
	name string,
	url string,
	modeID int64,
	enabled bool,
	syncInterval int,
) error {
	_, err := s.AddFeedForModeAdapter(ctx, name, url, modeID, 1, enabled, syncInterval, "")
	return err
}

func (s *Store) AddFeedForModeAdapter(
	ctx context.Context,
	name string,
	url string,
	modeID int64,
	adapterID int64,
	enabled bool,
	syncInterval int,
	data string,
) (int64, error) {
	var feedID int64
	err := s.Transaction(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx,
			"INSERT INTO feeds(name, url, adapter_id, enabled, sync_interval, data) VALUES (?, ?, ?, ?, ?, ?)",
			name, url, adapterID, enabled, syncInterval, data)
		if err != nil {
			return err
		}
		feedID, err = result.LastInsertId()
		if err != nil {
			return err
		}
		if modeID > 0 {
			_, err = tx.ExecContext(ctx,
				"INSERT INTO catalog_mode_feeds(mode_id, feed_id) VALUES (?, ?)",
				modeID, feedID)
		}
		return err
	})
	return feedID, err
}

func (s *Store) UpdateFeed(ctx context.Context, feed Feed) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		var oldURL string
		var oldAdapterID int64
		var oldData string
		var oldName string
		if err := tx.QueryRowContext(ctx,
			"SELECT url, adapter_id, data, name FROM feeds WHERE id = ?", feed.ID).
			Scan(&oldURL, &oldAdapterID, &oldData, &oldName); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE feeds
			 SET name = ?, url = ?, adapter_id = ?, enabled = ?, sync_interval = ?, data = ?
			 WHERE id = ?`,
			feed.Name, feed.URL, feed.AdapterID, feed.Enabled, feed.SyncInterval, feed.Data, feed.ID); err != nil {
			return err
		}
		if oldURL == feed.URL && oldAdapterID == feed.AdapterID && oldData == feed.Data && oldName == feed.Name {
			return nil
		}
		if _, err := tx.ExecContext(ctx,
			"DELETE FROM catalog_entries WHERE feed_id = ?", feed.ID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			"UPDATE feeds SET last_success = NULL, last_error = NULL WHERE id = ?", feed.ID)
		return err
	})
}

// SetFeedModes replaces all mode assignments for a feed with the given mode IDs.
func (s *Store) SetFeedModes(ctx context.Context, feedID int64, modeIDs []int64) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			"DELETE FROM catalog_mode_feeds WHERE feed_id = ?", feedID); err != nil {
			return err
		}
		for _, modeID := range modeIDs {
			if modeID <= 0 {
				continue
			}
			if _, err := tx.ExecContext(ctx,
				"INSERT INTO catalog_mode_feeds(mode_id, feed_id) VALUES (?, ?)",
				modeID, feedID); err != nil {
				return err
			}
		}
		return nil
	})
}

// FeedModes returns the mode IDs associated with a feed.
func (s *Store) FeedModes(ctx context.Context, feedID int64) ([]int64, error) {
	rows, err := s.DB.QueryContext(ctx,
		"SELECT mode_id FROM catalog_mode_feeds WHERE feed_id = ? ORDER BY mode_id", feedID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var modeIDs []int64
	for rows.Next() {
		var modeID int64
		if err := rows.Scan(&modeID); err != nil {
			return nil, err
		}
		modeIDs = append(modeIDs, modeID)
	}
	return modeIDs, rows.Err()
}

func (s *Store) DeleteFeed(ctx context.Context, id int64) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, "DELETE FROM feeds WHERE id = ?", id)
		if err != nil {
			return err
		}
		deleted, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if deleted == 0 {
			return sql.ErrNoRows
		}
		if _, err := tx.ExecContext(ctx, `
DELETE FROM selected_categories
WHERE NOT EXISTS (
    SELECT 1 FROM catalog_entries ce JOIN feeds f ON f.id = ce.feed_id
    JOIN catalog_mode_feeds cmf ON cmf.feed_id = f.id
    WHERE ce.category = selected_categories.category
      AND cmf.mode_id = selected_categories.mode_id
)`); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
DELETE FROM selected_services
WHERE NOT EXISTS (
    SELECT 1 FROM catalog_entries ce JOIN feeds f ON f.id = ce.feed_id
    JOIN catalog_mode_feeds cmf ON cmf.feed_id = f.id
    WHERE ce.category = selected_services.category
      AND ce.service = selected_services.service
      AND cmf.mode_id = selected_services.mode_id
)`)
		return err
	})
}
