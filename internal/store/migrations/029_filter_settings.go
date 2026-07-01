package migrations

import (
	"context"
	"database/sql"
	"log"
)

func V029(ctx context.Context, tx *sql.Tx) error {
	// Check if both tables exist (idempotent — may not exist in test DBs)
	hasFilters := tableExists(ctx, tx, "global_route_filters")
	hasSettings := tableExists(ctx, tx, "app_settings")

	if hasFilters && hasSettings {
		// Move allow filters to app_settings
		if _, err := tx.ExecContext(ctx, `
			INSERT OR REPLACE INTO app_settings(key, value, updated_at)
			VALUES ('filter_allow', (
				SELECT COALESCE(GROUP_CONCAT(cidr, CHAR(10)), '')
				FROM global_route_filters WHERE action = 'allow' ORDER BY cidr
			), datetime('now'))
		`); err != nil {
			return err
		}

		// Move deny filters to app_settings
		if _, err := tx.ExecContext(ctx, `
			INSERT OR REPLACE INTO app_settings(key, value, updated_at)
			VALUES ('filter_deny', (
				SELECT COALESCE(GROUP_CONCAT(cidr, CHAR(10)), '')
				FROM global_route_filters WHERE action = 'deny' ORDER BY cidr
			), datetime('now'))
		`); err != nil {
			return err
		}
	}

	// Drop the old table if it exists
	if hasFilters {
		if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS global_route_filters`); err != nil {
			return err
		}
	}

	return nil
}

func tableExists(ctx context.Context, tx *sql.Tx, name string) bool {
	rows, err := tx.QueryContext(ctx,
		"SELECT 1 FROM sqlite_master WHERE type='table' AND name=?", name)
	if err != nil {
		return false
	}
	found := rows.Next()
	if err := rows.Err(); err != nil {
		return false
	}
	if err := rows.Close(); err != nil {
		log.Printf("WARNING: rows close: %v", err)
	}
	return found
}
