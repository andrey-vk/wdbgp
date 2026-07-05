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

// buildVersion31DB creates a database migrated through version 31 (the last
// text-based catalog schema) with representative catalog, selection, and
// community data, and returns its path.
func buildVersion31DB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "version-31.sqlite3")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`
CREATE TABLE schema_migrations (
    version INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    applied_at TEXT NOT NULL
)`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, migration := range migrations {
		if migration.Version >= 32 {
			break
		}
		// Mirror store.go's migration runner: NoTxSQL first, then Func.
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
	if _, err := db.Exec(`
INSERT INTO users(id, name, peer_ip, peer_asn, catalog_mode_id)
VALUES (7, 'existing', '172.16.0.2', 65007, 1);
INSERT INTO user_networks(user_id, cidr) VALUES (7, '192.168.20.0/24');
INSERT INTO catalog_entries(feed_id, category, service, cidr) VALUES
    (1, 'Messengers', 'Telegram', '149.154.160.0/20'),
    (1, 'Messengers', 'Signal', '76.223.92.0/24'),
    (1, 'Unselected', 'Extra', '203.0.113.0/24');
INSERT INTO selected_categories(user_id, mode_id, category) VALUES (7, 1, 'Messengers');
INSERT INTO selected_services(user_id, mode_id, category, service)
VALUES (7, 1, 'Messengers', 'Telegram');
INSERT INTO catalog_communities(mode_id, category, service, community) VALUES
    (1, 'Messengers', '', 10000),
    (1, 'Messengers', 'Telegram', 10001);
`); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertMigration32Result(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()

	// catalog_entries: data dropped, new numeric shape.
	var hasCategoryColumn, entryCount int
	if err := s.DB.QueryRow(
		"SELECT COUNT(*) FROM pragma_table_info('catalog_entries') WHERE name='category'").
		Scan(&hasCategoryColumn); err != nil {
		t.Fatal(err)
	}
	if hasCategoryColumn != 0 {
		t.Fatal("catalog_entries still has the old category column")
	}
	if err := s.DB.QueryRow("SELECT COUNT(*) FROM catalog_entries").Scan(&entryCount); err != nil {
		t.Fatal(err)
	}
	if entryCount != 0 {
		t.Fatalf("catalog_entries = %d rows, want 0 (dropped for resync)", entryCount)
	}

	// Dictionaries: seeded from all old text data, even names only present
	// in entries (Unselected/Extra).
	for _, name := range []string{"Messengers", "Unselected"} {
		var n int
		if err := s.DB.QueryRow("SELECT COUNT(*) FROM categories WHERE name = ?", name).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("category %q dictionary rows = %d, want 1", name, n)
		}
	}

	// Selections survive the remap with their names intact.
	categories, services, err := s.UserModeSelection(ctx, 7, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !categories["Messengers"] ||
		!services[ServiceKey{Category: "Messengers", Service: "Telegram"}] {
		t.Fatalf("selections lost in remap: categories=%#v services=%#v", categories, services)
	}

	// Communities survive, including the category-wide ('' service) one.
	comms, err := s.GetCommunities(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if comms["Messengers"] != 10000 || comms["Messengers|Telegram"] != 10001 {
		t.Fatalf("communities lost in remap: %#v", comms)
	}
}

func TestMigration32RemapsCatalogToNumericKeys(t *testing.T) {
	path := buildVersion31DB(t)

	s, err := Open(path, false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	assertMigration32Result(t, s)

	// Re-running the NoTxSQL body on an already-converted database (a crash
	// after conversion but before schema_migrations was updated) must be a
	// clean no-op.
	if err := m.V032NoTxSQL(context.Background(), s.DB); err != nil {
		t.Fatalf("re-run on converted DB: %v", err)
	}
	assertMigration32Result(t, s)
}

func TestMigration32FinishesAfterCrashBetweenDropAndRename(t *testing.T) {
	path := buildVersion31DB(t)

	// Simulate a crash mid-032: dictionaries seeded and selected_categories
	// already copied into selected_categories_new and the old table dropped,
	// but the process died before the RENAME.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
CREATE TABLE categories (id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE);
CREATE TABLE services (
    id INTEGER PRIMARY KEY,
    category_id INTEGER NOT NULL REFERENCES categories(id),
    name TEXT NOT NULL,
    UNIQUE(category_id, name)
);
CREATE TABLE prefixes (id INTEGER PRIMARY KEY, ip BLOB NOT NULL, bits INTEGER NOT NULL, UNIQUE(ip, bits));
INSERT INTO categories(name) SELECT DISTINCT category FROM catalog_entries;
INSERT INTO services(category_id, name)
SELECT c.id, t.service FROM (SELECT DISTINCT category, service FROM catalog_entries) t
JOIN categories c ON c.name = t.category;
CREATE TABLE selected_categories_new (
    user_id     INTEGER NOT NULL,
    mode_id     INTEGER NOT NULL,
    category_id INTEGER NOT NULL,
    PRIMARY KEY (user_id, mode_id, category_id)
) WITHOUT ROWID;
INSERT INTO selected_categories_new (user_id, mode_id, category_id)
SELECT sc.user_id, sc.mode_id, c.id
FROM selected_categories sc JOIN categories c ON c.name = sc.category;
DROP TABLE selected_categories;
`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path, false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	assertMigration32Result(t, s)
}
