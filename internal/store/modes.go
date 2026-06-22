package store

import (
	"context"
	"database/sql"
	"fmt"
)

// CatalogMode represents a catalog mode (OpenCCK, IPRanges, sing-box SRS, etc.).
type CatalogMode struct {
	ID      int64
	Key     string
	Name    string
	Enabled bool
}

func (s *Store) CatalogModes(ctx context.Context, enabledOnly bool) ([]CatalogMode, error) {
	query := "SELECT id, key, name, enabled FROM catalog_modes"
	if enabledOnly {
		query += " WHERE enabled = 1"
	}
	query += " ORDER BY id"
	rows, err := s.DB.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var modes []CatalogMode
	for rows.Next() {
		var mode CatalogMode
		if err := rows.Scan(&mode.ID, &mode.Key, &mode.Name, &mode.Enabled); err != nil {
			return nil, err
		}
		modes = append(modes, mode)
	}
	return modes, rows.Err()
}

func (s *Store) CatalogMode(ctx context.Context, id int64) (CatalogMode, error) {
	var mode CatalogMode
	err := s.DB.QueryRowContext(ctx,
		"SELECT id, key, name, enabled FROM catalog_modes WHERE id = ?", id).
		Scan(&mode.ID, &mode.Key, &mode.Name, &mode.Enabled)
	return mode, err
}

func (s *Store) UpdateCatalogMode(ctx context.Context, mode CatalogMode) error {
	result, err := s.DB.ExecContext(ctx,
		"UPDATE catalog_modes SET name = ?, enabled = ? WHERE id = ?",
		mode.Name, mode.Enabled, mode.ID)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) AddCatalogMode(ctx context.Context, key, name string, enabled bool) (int64, error) {
	result, err := s.DB.ExecContext(ctx,
		"INSERT INTO catalog_modes(key, name, enabled) VALUES (?, ?, ?)",
		key, name, enabled)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (s *Store) DeleteCatalogMode(ctx context.Context, id int64) error {
	if id <= 3 {
		return fmt.Errorf("built-in catalog modes cannot be deleted")
	}
	// Reassign users referencing this mode to the default mode (id=1)
	// before deleting, to avoid foreign key violations.
	if _, err := s.DB.ExecContext(ctx,
		"UPDATE users SET catalog_mode_id = 1 WHERE catalog_mode_id = ?", id); err != nil {
		return err
	}
	result, err := s.DB.ExecContext(ctx, "DELETE FROM catalog_modes WHERE id = ?", id)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// ModeFeedCounts returns a map of mode_id→feed count.
func (s *Store) ModeFeedCounts(ctx context.Context) (map[int64]int, error) {
	rows, err := s.DB.QueryContext(ctx,
		"SELECT mode_id, COUNT(*) FROM catalog_mode_feeds GROUP BY mode_id")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
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
func (s *Store) ModeFeeds(ctx context.Context, modeID int64) ([]Feed, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT f.id, f.name, f.url, f.adapter_id, f.enabled,
       COALESCE(f.sync_interval, 0),
       COALESCE(f.data, ''),
       COALESCE(f.last_success, ''), COALESCE(f.last_error, '')
FROM feeds f
JOIN catalog_mode_feeds cmf ON cmf.feed_id = f.id
WHERE cmf.mode_id = ?
ORDER BY f.id`, modeID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var feeds []Feed
	for rows.Next() {
		var feed Feed
		feed.ModeID = modeID
		if err := rows.Scan(
			&feed.ID, &feed.Name, &feed.URL, &feed.AdapterID,
			&feed.Enabled, &feed.SyncInterval, &feed.Data, &feed.LastSuccess, &feed.LastError,
		); err != nil {
			return nil, err
		}
		feeds = append(feeds, feed)
	}
	return feeds, rows.Err()
}

func (s *Store) AddFeedToMode(ctx context.Context, modeID, feedID int64) error {
	_, err := s.DB.ExecContext(ctx,
		"INSERT OR IGNORE INTO catalog_mode_feeds(mode_id, feed_id) VALUES (?, ?)",
		modeID, feedID)
	return err
}

func (s *Store) RemoveFeedFromMode(ctx context.Context, modeID, feedID int64) error {
	result, err := s.DB.ExecContext(ctx,
		"DELETE FROM catalog_mode_feeds WHERE mode_id = ? AND feed_id = ?",
		modeID, feedID)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return sql.ErrNoRows
	}
	return nil
}
