package migrations

import (
	"context"
	"database/sql"
)

func V037(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS audit_log (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			recorded_at INTEGER NOT NULL,
			actor       TEXT NOT NULL,
			user_agent  TEXT NOT NULL DEFAULT '',
			action      TEXT NOT NULL,
			object_type TEXT NOT NULL,
			object_id   TEXT NOT NULL,
			before      TEXT,
			after       TEXT
		)`)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_audit_log_time ON audit_log(recorded_at)`)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_audit_log_object ON audit_log(object_type, object_id)`)
	return err
}
