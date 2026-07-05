//nolint:errcheck // test file, errors in cleanup intentionally ignored
package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	m "github.com/andrey-vk/wdbgp/internal/store/migrations"
	_ "modernc.org/sqlite"
)

func preMigration31Schema(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE feed_adapters (
    id INTEGER PRIMARY KEY,
    key TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL UNIQUE,
    language TEXT NOT NULL DEFAULT 'javascript'
        CHECK (language = 'javascript'),
    api_version INTEGER NOT NULL DEFAULT 1,
    source TEXT NOT NULL DEFAULT '',
    revision INTEGER NOT NULL DEFAULT 1,
    builtin_version INTEGER NOT NULL DEFAULT 0,
    is_customized INTEGER NOT NULL DEFAULT 0,
    forked_from INTEGER DEFAULT NULL,
    forked_version INTEGER NOT NULL DEFAULT 0,
    is_builtin INTEGER NOT NULL DEFAULT 0
);
INSERT INTO feed_adapters(id, key, name, source, is_builtin) VALUES
    (1, 'canonical-json', 'Canonical JSON', 'function sync() {}', 1),
    (2, 'my-custom', 'My Custom', 'function sync() { return []; }', 0);
`)
	return err
}

// TestMigration31DropsKeyColumn verifies the normal, uninterrupted rebuild:
// key is gone, both rows survive with their data intact.
func TestMigration31DropsKeyColumn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m31.sqlite3")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := preMigration31Schema(db); err != nil {
		t.Fatal(err)
	}

	if err := m.V031NoTxSQL(context.Background(), db); err != nil {
		t.Fatalf("V031NoTxSQL failed: %v", err)
	}

	var hasKey int
	if err := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('feed_adapters') WHERE name='key'").Scan(&hasKey); err != nil {
		t.Fatal(err)
	}
	if hasKey != 0 {
		t.Fatal("key column still present after V031NoTxSQL")
	}

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM feed_adapters").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("feed_adapters row count = %d, want 2", count)
	}

	var name string
	if err := db.QueryRow("SELECT name FROM feed_adapters WHERE id = 2").Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "My Custom" {
		t.Fatalf("row id=2 name = %q, want %q", name, "My Custom")
	}
}

// TestMigration31IdempotentAfterCrash simulates a process kill between the
// CREATE TABLE feed_adapters_new and DROP TABLE feed_adapters steps: on
// retry, feed_adapters (with key) still exists AND feed_adapters_new (from
// the killed run) already exists too. V031NoTxSQL must not fail with
// "table feed_adapters_new already exists", and must not duplicate rows
// into it.
func TestMigration31IdempotentAfterCrash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m31-crash.sqlite3")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := preMigration31Schema(db); err != nil {
		t.Fatal(err)
	}

	// Simulate the killed run's partial progress: feed_adapters_new already
	// created and already populated, feed_adapters (with key) untouched.
	if _, err := db.Exec(`
CREATE TABLE feed_adapters_new (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    language TEXT NOT NULL DEFAULT 'javascript'
        CHECK (language = 'javascript'),
    api_version INTEGER NOT NULL DEFAULT 1,
    source TEXT NOT NULL DEFAULT '',
    revision INTEGER NOT NULL DEFAULT 1,
    builtin_version INTEGER NOT NULL DEFAULT 0,
    is_customized INTEGER NOT NULL DEFAULT 0,
    forked_from INTEGER DEFAULT NULL,
    forked_version INTEGER NOT NULL DEFAULT 0,
    is_builtin INTEGER NOT NULL DEFAULT 0
);
INSERT INTO feed_adapters_new (id, name, language, api_version, source, revision, builtin_version, is_customized, forked_from, forked_version, is_builtin)
SELECT id, name, language, api_version, source, revision, builtin_version, is_customized, forked_from, forked_version, is_builtin
FROM feed_adapters;
`); err != nil {
		t.Fatal(err)
	}

	if err := m.V031NoTxSQL(context.Background(), db); err != nil {
		t.Fatalf("V031NoTxSQL should tolerate a retry after a simulated crash, got: %v", err)
	}

	var newTableCount int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='feed_adapters_new'").Scan(&newTableCount); err != nil {
		t.Fatal(err)
	}
	if newTableCount != 0 {
		t.Fatal("feed_adapters_new should have been renamed away, not left behind")
	}

	var hasKey int
	if err := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('feed_adapters') WHERE name='key'").Scan(&hasKey); err != nil {
		t.Fatal(err)
	}
	if hasKey != 0 {
		t.Fatal("key column still present after retried V031NoTxSQL")
	}

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM feed_adapters").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("feed_adapters row count = %d, want 2 (retry must not duplicate rows)", count)
	}
}

// TestMigration31IdempotentAfterCrashBetweenDropAndRename simulates a process
// kill after DROP TABLE feed_adapters succeeded but before the RENAME
// committed: feed_adapters doesn't exist at all, only the fully-populated
// feed_adapters_new does. V031NoTxSQL must finish the rename rather than
// treating the missing key column as "already migrated" and returning nil
// without ever restoring the feed_adapters table.
func TestMigration31IdempotentAfterCrashBetweenDropAndRename(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m31-crash-post-drop.sqlite3")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := preMigration31Schema(db); err != nil {
		t.Fatal(err)
	}

	// Simulate the killed run's state: feed_adapters_new fully populated,
	// feed_adapters already dropped (not just about to be).
	if _, err := db.Exec(`
CREATE TABLE feed_adapters_new (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    language TEXT NOT NULL DEFAULT 'javascript'
        CHECK (language = 'javascript'),
    api_version INTEGER NOT NULL DEFAULT 1,
    source TEXT NOT NULL DEFAULT '',
    revision INTEGER NOT NULL DEFAULT 1,
    builtin_version INTEGER NOT NULL DEFAULT 0,
    is_customized INTEGER NOT NULL DEFAULT 0,
    forked_from INTEGER DEFAULT NULL,
    forked_version INTEGER NOT NULL DEFAULT 0,
    is_builtin INTEGER NOT NULL DEFAULT 0
);
INSERT INTO feed_adapters_new (id, name, language, api_version, source, revision, builtin_version, is_customized, forked_from, forked_version, is_builtin)
SELECT id, name, language, api_version, source, revision, builtin_version, is_customized, forked_from, forked_version, is_builtin
FROM feed_adapters;
DROP TABLE feed_adapters;
`); err != nil {
		t.Fatal(err)
	}

	if err := m.V031NoTxSQL(context.Background(), db); err != nil {
		t.Fatalf("V031NoTxSQL should finish the rename on retry, got: %v", err)
	}

	var tableExists int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='feed_adapters'").Scan(&tableExists); err != nil {
		t.Fatal(err)
	}
	if tableExists == 0 {
		t.Fatal("feed_adapters must exist after retry — it must not be left permanently missing")
	}

	var newTableCount int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='feed_adapters_new'").Scan(&newTableCount); err != nil {
		t.Fatal(err)
	}
	if newTableCount != 0 {
		t.Fatal("feed_adapters_new should have been renamed away, not left behind")
	}

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM feed_adapters").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("feed_adapters row count = %d, want 2", count)
	}

	var indexCount int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_feed_adapters_builtin'").Scan(&indexCount); err != nil {
		t.Fatal(err)
	}
	if indexCount == 0 {
		t.Fatal("idx_feed_adapters_builtin should have been recreated on the renamed table")
	}
}
