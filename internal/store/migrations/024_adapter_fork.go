package migrations

import (
	"context"
	"database/sql"
	"log"
)

func V024(ctx context.Context, tx *sql.Tx) error {
	// Check which columns already exist for idempotency
	rows, err := tx.Query("SELECT name FROM pragma_table_info('feed_adapters')")
	if err != nil {
		return err
	}
	hasForkedFrom := false
	hasForkedVersion := false
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			if err := rows.Close(); err != nil {
				log.Printf("WARNING: rows close: %v", err)
			}
			return err
		}
		switch name {
		case "forked_from":
			hasForkedFrom = true
		case "forked_version":
			hasForkedVersion = true
		}
	}
	if err := rows.Close(); err != nil {
		log.Printf("WARNING: rows close: %v", err)
	}

	// Add forked_from column (empty for built-ins, references built-in key for forks)
	if !hasForkedFrom {
		if _, err := tx.Exec(`ALTER TABLE feed_adapters ADD COLUMN forked_from TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	// Add forked_version column (tracks version of built-in at fork time)
	if !hasForkedVersion {
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
