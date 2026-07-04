//nolint:errcheck // test file, errors in cleanup intentionally ignored
package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strconv"
	"testing"

	_ "modernc.org/sqlite"
)

// TestMigration20Idempotency verifies that migration 20 can be re-run after a
// crash that left the DB in an intermediate state. The NoTxSQL in migration 20
// drops the original tables and renames *_new copies. If the crash happened
// after the transactional part (which creates *_new tables) but before the
// NoTxSQL completed, the Go func rebuilds the missing *_new tables and the
// NoTxSQL safely retries the DROP+RENAME.
func TestMigration20Idempotency(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crash.sqlite3")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// Create a database with all migrations 1-19 already applied so that
	// only migration 20 runs when Open() is called.
	_, err = db.Exec(`
		CREATE TABLE schema_migrations (
			version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL
		);
	`)
	if err != nil {
		t.Fatal(err)
	}
	// Insert all migrations 1-19
	for v := 1; v <= 19; v++ {
		if _, err := db.Exec(
			"INSERT INTO schema_migrations(version, name, applied_at) VALUES (?, ?, ?)",
			v, "migration-"+strconv.Itoa(v), "2024-01-01T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	// Create all tables as they would exist after migration 19 (with mode_id on feeds)
	_, err = db.Exec(`
		CREATE TABLE feeds (
			id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE, url TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1, last_success TEXT, last_error TEXT,
			mode_id INTEGER,
			adapter_id INTEGER, sync_interval INTEGER NOT NULL DEFAULT 0,
			data TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE catalog_entries (
			feed_id INTEGER NOT NULL REFERENCES feeds(id) ON DELETE CASCADE,
			category TEXT NOT NULL, service TEXT NOT NULL, cidr TEXT NOT NULL,
			PRIMARY KEY (feed_id, category, service, cidr)
		);
		CREATE TABLE users (
			id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE,
			peer_ip TEXT NOT NULL UNIQUE, peer_asn INTEGER NOT NULL,
			next_hop TEXT, bgp_password TEXT,
			selection_locked INTEGER NOT NULL DEFAULT 0,
			enabled INTEGER NOT NULL DEFAULT 1,
			filter_override_enabled INTEGER NOT NULL DEFAULT 0,
			filter_editable INTEGER NOT NULL DEFAULT 0,
			filter_mode TEXT NOT NULL DEFAULT 'global',
			catalog_mode_id INTEGER, catalog_mode_editable INTEGER NOT NULL DEFAULT 0,
			web_auth TEXT NOT NULL DEFAULT 'network'
		);
		CREATE TABLE user_networks (
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			cidr TEXT NOT NULL UNIQUE,
			PRIMARY KEY (user_id, cidr)
		);
		CREATE TABLE selected_categories (
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			mode_id INTEGER NOT NULL, category TEXT NOT NULL,
			PRIMARY KEY (user_id, mode_id, category)
		);
		CREATE TABLE selected_services (
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			mode_id INTEGER NOT NULL, category TEXT NOT NULL, service TEXT NOT NULL,
			PRIMARY KEY (user_id, mode_id, category, service)
		);
		CREATE TABLE catalog_modes (
			id INTEGER PRIMARY KEY, key TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL UNIQUE, enabled INTEGER NOT NULL DEFAULT 1
		);
		CREATE TABLE feed_adapters (
			id INTEGER PRIMARY KEY, key TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL UNIQUE, language TEXT NOT NULL DEFAULT 'javascript',
			api_version INTEGER NOT NULL DEFAULT 1, source TEXT NOT NULL DEFAULT '',
			allowed_hosts TEXT NOT NULL DEFAULT '', revision INTEGER NOT NULL DEFAULT 1
		);
		CREATE TABLE catalog_communities (
			mode_id INTEGER NOT NULL, category TEXT NOT NULL,
			service TEXT NOT NULL DEFAULT '', community INTEGER NOT NULL,
			PRIMARY KEY (mode_id, category, service)
		);
		CREATE TABLE user_credentials (
			user_id INTEGER NOT NULL, login TEXT NOT NULL,
			password_hash TEXT NOT NULL, PRIMARY KEY (user_id, login)
		);
		CREATE TABLE app_settings (key TEXT PRIMARY KEY, value TEXT NOT NULL,
			updated_at TEXT NOT NULL DEFAULT (datetime('now')));
		CREATE TABLE global_route_filters (
			action TEXT NOT NULL, cidr TEXT NOT NULL,
			PRIMARY KEY (action, cidr)
		);
		CREATE TABLE user_route_filters (
			user_id INTEGER NOT NULL, action TEXT NOT NULL, cidr TEXT NOT NULL,
			PRIMARY KEY (user_id, action, cidr)
		);
		INSERT INTO catalog_modes(id, key, name, enabled) VALUES
			(1, 'opencck', 'OpenCCK', 1);
		INSERT INTO feed_adapters(id, key, name) VALUES
			(1, 'canonical-json', 'Canonical JSON');
		INSERT INTO feeds(id, name, url, enabled, adapter_id) VALUES
			(1, 'test-feed', 'https://example.test/feed', 1, 1);
		INSERT INTO users(id, name, peer_ip, peer_asn, enabled) VALUES
			(1, 'testuser', '10.0.0.1', 65001, 1);
		INSERT INTO user_networks(user_id, cidr) VALUES (1, '1.1.1.0/24');
	`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close() //nolint:errcheck,gosec // test cleanup

	// Now open — this triggers migration 20. It should succeed.
	s, err := Open(path, false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Verify migration 20 was applied
	var version int
	if err := s.DB.QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version < 20 {
		t.Fatalf("expected at least migration 20 applied, got %d", version)
	}

	// Verify data survived: migration 20 should not lose users or feeds
	var userCount, feedCount, networkCount int
	if err := s.DB.QueryRow("SELECT COUNT(*) FROM users").Scan(&userCount); err != nil {
		t.Fatal(err)
	}
	if userCount != 1 {
		t.Fatalf("user count = %d, want 1", userCount)
	}
	if err := s.DB.QueryRow("SELECT COUNT(*) FROM feeds").Scan(&feedCount); err != nil {
		t.Fatal(err)
	}
	if feedCount != 1 {
		t.Fatalf("feed count = %d, want 1", feedCount)
	}
	if err := s.DB.QueryRow("SELECT COUNT(*) FROM user_networks").Scan(&networkCount); err != nil {
		t.Fatal(err)
	}
	if networkCount != 1 {
		t.Fatalf("user_networks count = %d, want 1", networkCount)
	}

	// Verify we can insert two users with same peer_ip but different ASN
	// (proving peer_ip UNIQUE constraint is gone)
	if _, err := s.DB.Exec(
		"INSERT INTO users(name, peer_ip, peer_asn, enabled) VALUES ('dup-test', '10.0.0.1', 65002, 1)"); err != nil {
		t.Fatalf("should be able to add second user with same IP: %v", err)
	}

	// Verify catalog_mode_feeds table exists
	var tableCount int
	if err := s.DB.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='catalog_mode_feeds'").Scan(&tableCount); err != nil {
		t.Fatal(err)
	}
	if tableCount != 1 {
		t.Fatal("catalog_mode_feeds table not created")
	}
}

// TestMigration20NoTxSQLFailureRollsBack verifies that if migration 20's
// NoTxSQL fails, version 20 is NOT recorded in schema_migrations.
// This prevents the database from being left in a partial state where
// the version is committed but the user-table rebuild never happened,
// with no retry on next startup.
func TestMigration20NoTxSQLFailureRollsBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notxsql-fail.sqlite3")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// Create schema_migrations with all migrations 1-19 applied, so only
	// migration 20 runs on Open().
	_, err = db.Exec(`CREATE TABLE schema_migrations (
		version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL
	)`)
	if err != nil {
		t.Fatal(err)
	}
	for v := 1; v <= 19; v++ {
		if _, err := db.Exec(
			"INSERT INTO schema_migrations(version, name, applied_at) VALUES (?, ?, ?)",
			v, "migration-"+strconv.Itoa(v), "2024-01-01T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	// Build a pre-migration-20 database with a users table intentionally
	// missing the catalog_mode_editable column.  The SQL part of migration 20
	// does not touch users, but NoTxSQL expects that column in the source
	// table.  This simulates any NoTxSQL parse/constraint failure.
	_, err = db.Exec(`
		CREATE TABLE feeds (
			id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE, url TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1, last_success TEXT, last_error TEXT,
			mode_id INTEGER,
			adapter_id INTEGER, sync_interval INTEGER NOT NULL DEFAULT 0,
			data TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE feed_adapters (
			id INTEGER PRIMARY KEY, key TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL UNIQUE, language TEXT NOT NULL DEFAULT 'javascript',
			api_version INTEGER NOT NULL DEFAULT 1, source TEXT NOT NULL DEFAULT '',
			allowed_hosts TEXT NOT NULL DEFAULT '', revision INTEGER NOT NULL DEFAULT 1
		);
		CREATE TABLE catalog_modes (
			id INTEGER PRIMARY KEY, key TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL UNIQUE, enabled INTEGER NOT NULL DEFAULT 1
		);
		CREATE TABLE users (
			id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE,
			peer_ip TEXT NOT NULL UNIQUE, peer_asn INTEGER NOT NULL,
			next_hop TEXT, bgp_password TEXT,
			selection_locked INTEGER NOT NULL DEFAULT 0,
			enabled INTEGER NOT NULL DEFAULT 1,
			filter_override_enabled INTEGER NOT NULL DEFAULT 0,
			filter_editable INTEGER NOT NULL DEFAULT 0,
			filter_mode TEXT NOT NULL DEFAULT 'global',
			catalog_mode_id INTEGER,
			-- catalog_mode_editable intentionally omitted to make NoTxSQL fail
			web_auth TEXT NOT NULL DEFAULT 'network'
		);
		CREATE TABLE user_networks (
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			cidr TEXT NOT NULL UNIQUE, PRIMARY KEY (user_id, cidr)
		);
		CREATE TABLE selected_categories (
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			mode_id INTEGER NOT NULL, category TEXT NOT NULL,
			PRIMARY KEY (user_id, mode_id, category)
		);
		CREATE TABLE selected_services (
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			mode_id INTEGER NOT NULL, category TEXT NOT NULL, service TEXT NOT NULL,
			PRIMARY KEY (user_id, mode_id, category, service)
		);
		CREATE TABLE catalog_communities (
			mode_id INTEGER NOT NULL, category TEXT NOT NULL,
			service TEXT NOT NULL DEFAULT '', community INTEGER NOT NULL,
			PRIMARY KEY (mode_id, category, service)
		);
		CREATE TABLE user_credentials (
			user_id INTEGER NOT NULL, login TEXT NOT NULL,
			password_hash TEXT NOT NULL, PRIMARY KEY (user_id, login)
		);
		CREATE TABLE app_settings (key TEXT PRIMARY KEY, value TEXT NOT NULL,
			updated_at TEXT NOT NULL DEFAULT (datetime('now')));
		CREATE TABLE global_route_filters (
			action TEXT NOT NULL, cidr TEXT NOT NULL,
			PRIMARY KEY (action, cidr)
		);
		CREATE TABLE user_route_filters (
			user_id INTEGER NOT NULL, action TEXT NOT NULL, cidr TEXT NOT NULL,
			PRIMARY KEY (user_id, action, cidr)
		);
		CREATE TABLE catalog_entries (
			feed_id INTEGER NOT NULL REFERENCES feeds(id) ON DELETE CASCADE,
			category TEXT NOT NULL, service TEXT NOT NULL, cidr TEXT NOT NULL,
			PRIMARY KEY (feed_id, category, service, cidr)
		);
		INSERT INTO catalog_modes(id, key, name, enabled) VALUES
			(1, 'opencck', 'OpenCCK', 1);
		INSERT INTO feed_adapters(id, key, name) VALUES
			(1, 'canonical-json', 'Canonical JSON');
		INSERT INTO feeds(id, name, url, enabled, adapter_id, mode_id) VALUES
			(1, 'test-feed', 'https://example.test/feed', 1, 1, 1);
		INSERT INTO users(id, name, peer_ip, peer_asn, enabled) VALUES
			(1, 'testuser', '10.0.0.1', 65001, 1);
	`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close() //nolint:errcheck,gosec // test cleanup

	// Open the DB — this triggers migration 20.  The transactional SQL
	// part (feed_adapters columns, catalog_mode_feeds table) will succeed,
	// but the NoTxSQL (users table rebuild) MUST fail because the source
	// users table is missing the catalog_mode_editable column.
	s, err := Open(path, false, "", false)
	if err == nil {
		defer s.Close()
		t.Fatal("expected Open to fail because NoTxSQL should fail, but got no error")
	}
	t.Logf("Open correctly returned error: %v", err)

	// Re-query the DB manually to check whether version 20 was recorded
	// despite the failure.
	db2, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()

	var version int
	err = db2.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&version)
	if err != nil {
		t.Fatal(err)
	}
	if version >= 20 {
		t.Errorf("BUG: migration version %d recorded despite NoTxSQL failure; "+
			"DB is left in partial state with no retry path", version)
	}
}

// TestMigration20FeedAdapterUpgrade verifies that migration 20 adds
// the builtin_version and is_customized columns to feed_adapters.
func TestMigration20FeedAdapterUpgrade(t *testing.T) {
	path := filepath.Join(t.TempDir(), "adapter-upgrade.sqlite3")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// Create schema_migrations with all migrations 1-19 applied, so only
	// migration 20 runs on Open().
	_, err = db.Exec(`CREATE TABLE schema_migrations (
		version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL
	)`)
	if err != nil {
		t.Fatal(err)
	}
	for v := 1; v <= 19; v++ {
		if _, err := db.Exec(
			"INSERT INTO schema_migrations(version, name, applied_at) VALUES (?, ?, ?)",
			v, "migration-"+strconv.Itoa(v), "2024-01-01T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	// Build a pre-migration-20 database with feed_adapters (without builtin_version/is_customized)
	_, err = db.Exec(`
		CREATE TABLE feed_adapters (
			id INTEGER PRIMARY KEY, key TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL UNIQUE, language TEXT NOT NULL DEFAULT 'javascript',
			api_version INTEGER NOT NULL DEFAULT 1, source TEXT NOT NULL DEFAULT '',
			allowed_hosts TEXT NOT NULL DEFAULT '', revision INTEGER NOT NULL DEFAULT 1
		);
		INSERT INTO feed_adapters(id, key, name) VALUES
			(1, 'canonical-json', 'Canonical JSON'),
			(2, 'custom-adapter', 'Custom Adapter');
		CREATE TABLE feeds (
			id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE, url TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1, last_success TEXT, last_error TEXT,
			adapter_id INTEGER, sync_interval INTEGER NOT NULL DEFAULT 0,
			data TEXT NOT NULL DEFAULT '',
			mode_id INTEGER
		);
		CREATE TABLE catalog_modes (id INTEGER PRIMARY KEY, key TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL UNIQUE, enabled INTEGER NOT NULL DEFAULT 1);
		INSERT INTO catalog_modes(id, key, name, enabled) VALUES (1, 'test', 'Test', 1);
		INSERT INTO feeds(id, name, url, enabled, adapter_id, mode_id) VALUES
			(1, 'feed1', 'https://example.test/1', 1, 1, 1);
		CREATE TABLE users (
			id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE,
			peer_ip TEXT NOT NULL UNIQUE, peer_asn INTEGER NOT NULL,
			next_hop TEXT, bgp_password TEXT,
			selection_locked INTEGER NOT NULL DEFAULT 0,
			enabled INTEGER NOT NULL DEFAULT 1,
			filter_override_enabled INTEGER NOT NULL DEFAULT 0,
			filter_editable INTEGER NOT NULL DEFAULT 0,
			filter_mode TEXT NOT NULL DEFAULT 'global',
			catalog_mode_id INTEGER, catalog_mode_editable INTEGER NOT NULL DEFAULT 0,
			web_auth TEXT NOT NULL DEFAULT 'network'
		);
		CREATE TABLE user_networks (
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			cidr TEXT NOT NULL UNIQUE, PRIMARY KEY (user_id, cidr)
		);
		CREATE TABLE selected_categories (
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			mode_id INTEGER NOT NULL, category TEXT NOT NULL,
			PRIMARY KEY (user_id, mode_id, category)
		);
		CREATE TABLE selected_services (
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			mode_id INTEGER NOT NULL, category TEXT NOT NULL, service TEXT NOT NULL,
			PRIMARY KEY (user_id, mode_id, category, service)
		);
		CREATE TABLE catalog_communities (
			mode_id INTEGER NOT NULL, category TEXT NOT NULL,
			service TEXT NOT NULL DEFAULT '', community INTEGER NOT NULL,
			PRIMARY KEY (mode_id, category, service)
		);
		CREATE TABLE user_credentials (
			user_id INTEGER NOT NULL, login TEXT NOT NULL,
			password_hash TEXT NOT NULL, PRIMARY KEY (user_id, login)
		);
		CREATE TABLE app_settings (key TEXT PRIMARY KEY, value TEXT NOT NULL,
			updated_at TEXT NOT NULL DEFAULT (datetime('now')));
		CREATE TABLE global_route_filters (
			action TEXT NOT NULL, cidr TEXT NOT NULL,
			PRIMARY KEY (action, cidr)
		);
		CREATE TABLE user_route_filters (
			user_id INTEGER NOT NULL, action TEXT NOT NULL, cidr TEXT NOT NULL,
			PRIMARY KEY (user_id, action, cidr)
		);
		CREATE TABLE catalog_entries (
			feed_id INTEGER NOT NULL REFERENCES feeds(id) ON DELETE CASCADE,
			category TEXT NOT NULL, service TEXT NOT NULL, cidr TEXT NOT NULL,
			PRIMARY KEY (feed_id, category, service, cidr)
		);
	`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close() //nolint:gosec // test cleanup

	s, err := Open(path, false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Verify builtin_version and is_customized columns exist
	var cnt int
	if err := s.DB.QueryRow(
		"SELECT COUNT(*) FROM pragma_table_info('feed_adapters') WHERE name = 'builtin_version'").Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt != 1 {
		t.Fatal("builtin_version column not added to feed_adapters")
	}
	if err := s.DB.QueryRow(
		"SELECT COUNT(*) FROM pragma_table_info('feed_adapters') WHERE name = 'is_customized'").Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt != 1 {
		t.Fatal("is_customized column not added to feed_adapters")
	}

	// Verify existing adapter data survived
	adapters, err := s.FeedAdapters(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(adapters) < 2 {
		t.Fatalf("adapter count = %d, want at least 2", len(adapters))
	}

	// Verify catalog_mode_feeds migration happened — feeds.mode_id removed
	if err := s.DB.QueryRow(
		"SELECT COUNT(*) FROM pragma_table_info('feeds') WHERE name = 'mode_id'").Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt != 0 {
		t.Fatal("mode_id column should have been dropped from feeds")
	}

	// Verify feed still exists with data
	feed, err := s.Feed(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if feed.Name != "feed1" {
		t.Fatalf("feed name = %q, want 'feed1'", feed.Name)
	}

	// Verify the feed's mode assignment migrated to junction table
	var modeFeedCount int
	if err := s.DB.QueryRow(
		"SELECT COUNT(*) FROM catalog_mode_feeds WHERE feed_id = 1 AND mode_id = 1").Scan(&modeFeedCount); err != nil {
		t.Fatal(err)
	}
	if modeFeedCount != 1 {
		t.Fatalf("catalog_mode_feeds count = %d, want 1", modeFeedCount)
	}
}

// TestMigration20PreservesAdapterCustomizations verifies that migration 20 and
// seedBuiltInAdapters do NOT overwrite admin-customized built-in adapter sources.
func TestMigration20PreservesAdapterCustomizations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "adapter-custom.sqlite3")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// Create schema_migrations with all migrations 1-19 applied, so only
	// migration 20 runs on Open().
	_, err = db.Exec(`CREATE TABLE schema_migrations (
		version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL
	)`)
	if err != nil {
		t.Fatal(err)
	}
	for v := 1; v <= 19; v++ {
		if _, err := db.Exec(
			"INSERT INTO schema_migrations(version, name, applied_at) VALUES (?, ?, ?)",
			v, "migration-"+strconv.Itoa(v), "2024-01-01T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}

	customSource := "// CUSTOMIZED BY ADMIN\nfunction sync(feed, api) { return []; }\n"

	// Build a pre-migration-20 database with a customized built-in adapter and
	// minimal other tables required by migration 20's DDL.
	_, err = db.Exec(`
		CREATE TABLE feed_adapters (
			id INTEGER PRIMARY KEY, key TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL UNIQUE, language TEXT NOT NULL DEFAULT 'javascript',
			api_version INTEGER NOT NULL DEFAULT 1, source TEXT NOT NULL DEFAULT '',
			allowed_hosts TEXT NOT NULL DEFAULT '', revision INTEGER NOT NULL DEFAULT 1
		);
		INSERT INTO feed_adapters(id, key, name, source) VALUES
			(1, 'canonical-json', 'Canonical JSON', ?);
		CREATE TABLE catalog_modes (
			id INTEGER PRIMARY KEY, key TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL UNIQUE, enabled INTEGER NOT NULL DEFAULT 1
		);
		INSERT INTO catalog_modes(id, key, name, enabled) VALUES (1, 'test', 'Test', 1);
		CREATE TABLE feeds (
			id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE, url TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1, last_success TEXT, last_error TEXT,
			adapter_id INTEGER, sync_interval INTEGER NOT NULL DEFAULT 0,
			data TEXT NOT NULL DEFAULT '', mode_id INTEGER
		);
		CREATE TABLE users (
			id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE,
			peer_ip TEXT NOT NULL UNIQUE, peer_asn INTEGER NOT NULL,
			next_hop TEXT, bgp_password TEXT,
			selection_locked INTEGER NOT NULL DEFAULT 0,
			enabled INTEGER NOT NULL DEFAULT 1,
			filter_override_enabled INTEGER NOT NULL DEFAULT 0,
			filter_editable INTEGER NOT NULL DEFAULT 0,
			filter_mode TEXT NOT NULL DEFAULT 'global',
			catalog_mode_id INTEGER, catalog_mode_editable INTEGER NOT NULL DEFAULT 0,
			web_auth TEXT NOT NULL DEFAULT 'network'
		);
	`, customSource)
	if err != nil {
		t.Fatal(err)
	}
	db.Close() //nolint:gosec // test cleanup

	// Open triggers migration 20 + seedBuiltInAdapters.
	s, err := Open(path, false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Verify the custom source was NOT overwritten by seedBuiltInAdapters.
	var storedSource string
	err = s.DB.QueryRow("SELECT source FROM feed_adapters WHERE name = 'Canonical JSON'").Scan(&storedSource)
	if err != nil {
		t.Fatal(err)
	}
	if storedSource != customSource {
		t.Errorf("custom source was overwritten by seedBuiltInAdapters!\ngot:  %q\nwant: %q", storedSource, customSource)
	}

	// Verify is_customized was set to 1 to protect from future overwrites.
	var isCustomized int
	err = s.DB.QueryRow("SELECT is_customized FROM feed_adapters WHERE name = 'Canonical JSON'").Scan(&isCustomized)
	if err != nil {
		t.Fatal(err)
	}
	if isCustomized != 1 {
		t.Errorf("is_customized = %d, want 1 (custom adapter not protected from overwrites)", isCustomized)
	}
}
