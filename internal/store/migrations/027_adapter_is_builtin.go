package migrations

import (
	"context"
	"database/sql"
	"log"
)

func V027(ctx context.Context, tx *sql.Tx) error {
	// Check which columns already exist for idempotency
	rows, err := tx.Query("SELECT name FROM pragma_table_info('feed_adapters')")
	if err != nil {
		return err
	}
	hasIsBuiltin := false
	hasForkedFrom := false
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			if err := rows.Close(); err != nil {
				log.Printf("WARNING: rows close: %v", err)
			}
			return err
		}
		if name == "is_builtin" {
			hasIsBuiltin = true
		}
		if name == "forked_from" {
			hasForkedFrom = true
		}
	}
	if err := rows.Close(); err != nil {
		log.Printf("WARNING: rows close: %v", err)
	}

	// 1. Add is_builtin column (default 0 for custom/forked adapters)
	if !hasIsBuiltin {
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

	// 3. Update forked_from to store adapter NAME instead of key
	//    (so "Forked from opencck" becomes "Forked from OpenCCK" in the UI).
	//    Only update rows where forked_from still references a valid key
	//    (hasn't been converted yet — idempotent).
	if hasForkedFrom {
		if _, err := tx.ExecContext(ctx,
			`UPDATE feed_adapters SET forked_from = (
				SELECT name FROM feed_adapters WHERE key = feed_adapters.forked_from
			) WHERE forked_from != '' AND EXISTS (
				SELECT 1 FROM feed_adapters WHERE key = feed_adapters.forked_from
			)`); err != nil {
			return err
		}
	}

	// 4. Index for efficient built-in lookups
	if _, err := tx.ExecContext(ctx,
		`CREATE INDEX IF NOT EXISTS idx_feed_adapters_builtin ON feed_adapters(is_builtin)`); err != nil {
		return err
	}

	return nil
}
