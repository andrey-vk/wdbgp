package migrations

import (
	"context"
	"database/sql"
)

// V038 records what each feed sync changed — services and prefixes added or
// removed, with the added services broken down by category — and each
// user's feed_changes_seen_at, which bounds the user-facing note about
// feed-driven growth.
func V038(ctx context.Context, tx *sql.Tx) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS feed_sync_changes (
			id               INTEGER PRIMARY KEY AUTOINCREMENT,
			feed_id          INTEGER NOT NULL REFERENCES feeds(id) ON DELETE CASCADE,
			synced_at        INTEGER NOT NULL,
			added_services   INTEGER NOT NULL,
			removed_services INTEGER NOT NULL,
			added_prefixes   INTEGER NOT NULL,
			removed_prefixes INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_feed_sync_changes_feed ON feed_sync_changes(feed_id, synced_at)`,
		`CREATE TABLE IF NOT EXISTS feed_sync_change_categories (
			change_id      INTEGER NOT NULL REFERENCES feed_sync_changes(id) ON DELETE CASCADE,
			category       TEXT NOT NULL,
			added_services INTEGER NOT NULL,
			PRIMARY KEY (change_id, category)
		)`,
	}
	for _, stmt := range stmts {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	var hasSeen int
	if err := tx.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM pragma_table_info('users') WHERE name = 'feed_changes_seen_at'").Scan(&hasSeen); err != nil {
		return err
	}
	if hasSeen == 0 {
		_, err := tx.ExecContext(ctx, "ALTER TABLE users ADD COLUMN feed_changes_seen_at INTEGER NOT NULL DEFAULT 0")
		return err
	}
	return nil
}
