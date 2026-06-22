package migrations

import (
	"context"
	"database/sql"
)

func V007(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
DELETE FROM selected_categories
WHERE category IN (
    'opencck-main',
    'opencck-beta',
    'opencck-main-v4',
    'opencck-beta-v4',
    'opencck-main-v6',
    'opencck-beta-v6'
);
`)
	return err
}
