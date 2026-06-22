package migrations

import (
	"context"
	"database/sql"
)

func V006(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
ALTER TABLE users ADD COLUMN filter_mode TEXT NOT NULL DEFAULT 'global'
    CHECK (filter_mode IN ('global', 'extend', 'override'));

UPDATE users
SET filter_mode = CASE WHEN filter_override_enabled THEN 'override' ELSE 'global' END;
`)
	return err
}
