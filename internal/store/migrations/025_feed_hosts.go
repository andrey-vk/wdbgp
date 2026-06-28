package migrations

import (
	"context"
	"database/sql"
	"fmt"
	"log"
)

func V025(ctx context.Context, tx *sql.Tx) error {
	// Check existing columns to make the migration idempotent.
	rows, err := tx.QueryContext(ctx, "PRAGMA table_info(feeds)")
	if err != nil {
		return fmt.Errorf("read feeds schema: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("WARNING: rows close: %v", err)
		}
	}()

	existing := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return fmt.Errorf("scan column info: %w", err)
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read column info: %w", err)
	}

	// Add allowed_hosts to feeds if not present
	if !existing["allowed_hosts"] {
		if _, err := tx.Exec(`ALTER TABLE feeds ADD COLUMN allowed_hosts TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	// Add restrict_hosts to feeds if not present (1 = restrict, 0 = allow all)
	if !existing["restrict_hosts"] {
		if _, err := tx.Exec(`ALTER TABLE feeds ADD COLUMN restrict_hosts INTEGER NOT NULL DEFAULT 1`); err != nil {
			return err
		}
	}
	return nil
}
