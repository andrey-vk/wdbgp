package migrations

import (
	"context"
	"database/sql"
	"log"
)

// dbTableExists reports whether a table exists, for NoTxSQL migrations
// that need to detect which step of an interrupted rebuild to resume from.
func dbTableExists(ctx context.Context, db *sql.DB, table string) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&n)
	return n > 0, err
}

// dbTableHasColumn reports whether a column exists on a table. A missing
// table yields false, not an error (pragma_table_info returns no rows).
func dbTableHasColumn(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?", table, column).Scan(&n)
	return n > 0, err
}

// existingColumns returns the set of column names currently present on
// table, via the pragma_table_info table-valued function. Migrations that
// conditionally ALTER TABLE ADD COLUMN use this to stay idempotent across
// partial/retried runs.
func existingColumns(tx *sql.Tx, table string) (map[string]bool, error) {
	rows, err := tx.Query("SELECT name FROM pragma_table_info(?)", table)
	if err != nil {
		return nil, err
	}
	cols := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			if cerr := rows.Close(); cerr != nil {
				log.Printf("WARNING: rows close: %v", cerr)
			}
			return nil, err
		}
		cols[name] = true
	}
	if err := rows.Err(); err != nil {
		if cerr := rows.Close(); cerr != nil {
			log.Printf("WARNING: rows close: %v", cerr)
		}
		return nil, err
	}
	if err := rows.Close(); err != nil {
		log.Printf("WARNING: rows close: %v", err)
	}
	return cols, nil
}
