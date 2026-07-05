package migrations

import (
	"context"
	"database/sql"
)

func V027(ctx context.Context, tx *sql.Tx) error {
	// Check which columns already exist for idempotency
	cols, err := existingColumns(tx, "feed_adapters")
	if err != nil {
		return err
	}

	// 1. Add is_builtin column (default 0 for custom/forked adapters)
	if !cols["is_builtin"] {
		if _, err := tx.ExecContext(ctx,
			`ALTER TABLE feed_adapters ADD COLUMN is_builtin INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}

	// 2. Populate from existing key values
	if _, err := tx.ExecContext(ctx,
		`UPDATE feed_adapters SET is_builtin = 1 WHERE key IN ('canonical-json', 'opencck', 'ipranges', 'singbox-srs')`); err != nil {
		return err
	}

	// 3. Index for efficient built-in lookups
	if _, err := tx.ExecContext(ctx,
		`CREATE INDEX IF NOT EXISTS idx_feed_adapters_builtin ON feed_adapters(is_builtin)`); err != nil {
		return err
	}

	return nil
}
