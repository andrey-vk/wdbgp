package migrations

import (
	"context"
	"database/sql"
)

// V038 records what each feed sync changed — services and prefixes added or
// removed, with the added services broken down by category, and the prefixes
// each mode newly announces through each category — plus, per user and mode,
// the newest change acknowledged. The cursor is a change ID, not a timestamp,
// so changes committed in the same second can't hide one another.
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
		`CREATE TABLE IF NOT EXISTS feed_sync_mode_growth (
			change_id INTEGER NOT NULL REFERENCES feed_sync_changes(id) ON DELETE CASCADE,
			mode_id   INTEGER NOT NULL,
			category  TEXT NOT NULL,
			prefix    TEXT NOT NULL,
			PRIMARY KEY (change_id, mode_id, category, prefix)
		)`,
		`CREATE TABLE IF NOT EXISTS user_feed_changes_seen (
			user_id   INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			mode_id   INTEGER NOT NULL,
			change_id INTEGER NOT NULL,
			PRIMARY KEY (user_id, mode_id)
		)`,
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
	return nil
}
