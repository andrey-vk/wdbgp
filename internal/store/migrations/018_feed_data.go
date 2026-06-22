package migrations

import (
	"context"
	"database/sql"
)

func V018(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
ALTER TABLE feeds ADD COLUMN data TEXT NOT NULL DEFAULT '';
`)
	return err
}
