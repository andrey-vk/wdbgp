package migrations

import (
	"context"
	"database/sql"
)

func V026(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS user_snapshots (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			recorded_at     INTEGER NOT NULL,
			users_disabled  INTEGER NOT NULL DEFAULT 0,
			users_connected INTEGER NOT NULL DEFAULT 0,
			users_total     INTEGER NOT NULL DEFAULT 0
		)`)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_user_snapshots_time ON user_snapshots(recorded_at)`)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS feed_snapshots (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			recorded_at INTEGER NOT NULL,
			prefixes    TEXT NOT NULL
		)`)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_feed_snapshots_time ON feed_snapshots(recorded_at)`)
	return err
}
