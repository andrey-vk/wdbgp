package migrations

import (
	"context"
	"database/sql"
)

func V017(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
ALTER TABLE feeds ADD COLUMN sync_interval INTEGER NOT NULL DEFAULT 0;
`)
	return err
}
