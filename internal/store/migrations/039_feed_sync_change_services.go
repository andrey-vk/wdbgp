package migrations

import (
	"context"
	"database/sql"
)

// V039 records which services each feed sync added or removed, by name, so a
// user's change log can say which of their services moved rather than only how
// many. Rows follow their feed_sync_changes row and are pruned with it.
func V039(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS feed_sync_change_services (
			change_id INTEGER NOT NULL REFERENCES feed_sync_changes(id) ON DELETE CASCADE,
			kind      TEXT NOT NULL CHECK (kind IN ('added', 'removed')),
			category  TEXT NOT NULL,
			service   TEXT NOT NULL,
			PRIMARY KEY (change_id, kind, category, service)
		)`)
	return err
}
