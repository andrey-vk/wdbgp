package migrations

import (
	"context"
	"database/sql"
	"fmt"
	"log"
)

func V028(ctx context.Context, tx *sql.Tx) error {
	// Check if the column exists (idempotent — migration may re-run in tests).
	rows, err := tx.QueryContext(ctx, "PRAGMA table_info(feed_adapters)")
	if err != nil {
		return fmt.Errorf("read feed_adapters schema: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("WARNING: rows close: %v", err)
		}
	}()

	hasAllowedHosts := false
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return fmt.Errorf("scan column info: %w", err)
		}
		if name == "allowed_hosts" {
			hasAllowedHosts = true
			break
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read column info: %w", err)
	}
	if rows.Close() != nil {
		log.Printf("WARNING: rows close: %v", rows.Close())
	}

	if hasAllowedHosts {
		if _, err := tx.ExecContext(ctx,
			`ALTER TABLE feed_adapters DROP COLUMN allowed_hosts`); err != nil {
			return err
		}
	}
	return nil
}
