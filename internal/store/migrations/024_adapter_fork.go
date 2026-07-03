package migrations

import (
	"context"
	"database/sql"
)

func V024(ctx context.Context, tx *sql.Tx) error {
	// Check which columns already exist for idempotency
	cols, err := existingColumns(tx, "feed_adapters")
	if err != nil {
		return err
	}

	// Add forked_from column (NULL for built-ins, references built-in adapter ID for forks)
	if !cols["forked_from"] {
		if _, err := tx.Exec(`ALTER TABLE feed_adapters ADD COLUMN forked_from INTEGER DEFAULT NULL`); err != nil {
			return err
		}
	}
	// Add forked_version column (tracks version of built-in at fork time)
	if !cols["forked_version"] {
		if _, err := tx.Exec(`ALTER TABLE feed_adapters ADD COLUMN forked_version INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	// Update existing built-in adapters to set forked_version = builtin_version
	if _, err := tx.Exec(`UPDATE feed_adapters SET forked_version = builtin_version WHERE key != ''`); err != nil {
		return err
	}
	return nil
}
