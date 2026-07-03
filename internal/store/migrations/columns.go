package migrations

import (
	"database/sql"
	"log"
)

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
