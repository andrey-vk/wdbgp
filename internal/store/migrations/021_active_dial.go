package migrations

import (
	"context"
	"database/sql"
)

func V021(ctx context.Context, tx *sql.Tx) error {
	var hasActiveDial int
	if err := tx.QueryRow("SELECT COUNT(*) FROM pragma_table_info('users') WHERE name='active_dial'").Scan(&hasActiveDial); err != nil {
		return err
	}
	if hasActiveDial == 0 {
		if _, err := tx.Exec("ALTER TABLE users ADD COLUMN active_dial INTEGER NOT NULL DEFAULT 1"); err != nil {
			return err
		}
	}
	return nil
}
