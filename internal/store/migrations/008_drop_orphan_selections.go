package migrations

import (
	"context"
	"database/sql"
)

func V008(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
DELETE FROM selected_categories
WHERE NOT EXISTS (
    SELECT 1 FROM catalog_entries ce
    WHERE ce.category = selected_categories.category
);

DELETE FROM selected_services
WHERE NOT EXISTS (
    SELECT 1 FROM catalog_entries ce
    WHERE ce.category = selected_services.category
      AND ce.service = selected_services.service
);
`)
	return err
}
