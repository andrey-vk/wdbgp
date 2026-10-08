package migrations

import (
	"context"
	"database/sql"
)

func V040(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS user_prefix_history (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			recorded_at INTEGER NOT NULL,
			v4_count    INTEGER NOT NULL,
			v6_count    INTEGER NOT NULL
		)`)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_user_prefix_history_user_time ON user_prefix_history(user_id, recorded_at)`)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS user_prefix_alert_state (
			user_id        INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
			baseline_v4    INTEGER NOT NULL,
			baseline_v6    INTEGER NOT NULL,
			alerting_since INTEGER,
			drop_delivered INTEGER NOT NULL DEFAULT 1
		)`)
	return err
}
