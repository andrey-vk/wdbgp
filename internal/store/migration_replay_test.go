//nolint:errcheck // test file, errors in cleanup intentionally ignored
package store

import (
	"context"
	"database/sql"
	"testing"
)

// replayMigrationsBefore applies every registered migration with Version <
// stop to db, mirroring store.go's runner (NoTxSQL first, then the
// transactional Func), and records each in schema_migrations. Used by
// migration tests that need a database frozen at a historical schema
// version, with fixture data inserted in that era's shape.
func replayMigrationsBefore(t *testing.T, db *sql.DB, stop int) {
	t.Helper()
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    applied_at TEXT NOT NULL
)`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, migration := range migrations {
		if migration.Version >= stop {
			break
		}
		if migration.NoTxSQL != nil {
			if err := migration.NoTxSQL(ctx, db); err != nil {
				t.Fatalf("migration %d NoTxSQL: %v", migration.Version, err)
			}
		}
		tx, err := db.Begin()
		if err != nil {
			t.Fatalf("begin tx for migration %d: %v", migration.Version, err)
		}
		if err := migration.Func(ctx, tx); err != nil {
			tx.Rollback() //nolint:errcheck,gosec // test cleanup
			t.Fatalf("apply migration %d: %v", migration.Version, err)
		}
		if _, err := tx.Exec(
			"INSERT INTO schema_migrations(version, name, applied_at) VALUES (?, ?, 'now')",
			migration.Version, migration.Name); err != nil {
			tx.Rollback() //nolint:errcheck,gosec // test cleanup
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
}
