package store

import (
	"context"
	"database/sql"
	"net/netip"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestMigrateFreshDatabase(t *testing.T) {
	s := openTestStore(t)
	var version int
	if err := s.DB.QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != len(migrations) {
		t.Fatalf("schema version = %d, want %d", version, len(migrations))
	}
	feeds, err := s.Feeds(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(feeds) != 5 {
		t.Fatalf("feed count = %d, want 5", len(feeds))
	}
}

func TestMigrateLegacyPythonDatabasePreservesData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.sqlite3")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
CREATE TABLE feeds (
 id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE, url TEXT NOT NULL,
 enabled INTEGER NOT NULL DEFAULT 1, last_success TEXT, last_error TEXT
);
CREATE TABLE users (
 id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE, peer_ip TEXT NOT NULL UNIQUE,
 peer_asn INTEGER NOT NULL, next_hop TEXT, bgp_password TEXT,
 selection_locked INTEGER NOT NULL DEFAULT 0, enabled INTEGER NOT NULL DEFAULT 1
);
CREATE TABLE user_networks (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 cidr TEXT NOT NULL UNIQUE, PRIMARY KEY (user_id, cidr)
);
INSERT INTO users(id, name, peer_ip, peer_asn) VALUES (7, 'legacy', '172.16.0.2', 65007);
INSERT INTO user_networks(user_id, cidr) VALUES (7, '192.168.20.0/24');
INSERT INTO feeds(name, url) VALUES
 ('opencck-main-v4', 'https://iplist.opencck.org/?format=json&data=cidr4'),
 ('opencck-beta-v4', 'https://beta.iplist.opencck.org/?format=json&data=cidr4'),
 ('opencck-main-v6', 'https://iplist.opencck.org/?format=json&data=cidr6'),
 ('opencck-beta-v6', 'https://beta.iplist.opencck.org/?format=json&data=cidr6');
`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	user, err := s.User(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if user.Name != "legacy" || len(user.Networks) != 1 || user.Networks[0] != "192.168.20.0/24" {
		t.Fatalf("legacy user changed during migration: %#v", user)
	}
	feeds, err := s.Feeds(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(feeds) != 5 {
		t.Fatalf("legacy feed count = %d, want 5", len(feeds))
	}
}

func TestMigrateLegacyPythonDatabaseDropsOpenCCKFeedCategorySelection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-opencck-selection.sqlite3")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
CREATE TABLE feeds (
 id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE, url TEXT NOT NULL,
 enabled INTEGER NOT NULL DEFAULT 1, last_success TEXT, last_error TEXT
);
CREATE TABLE catalog_entries (
 feed_id INTEGER NOT NULL REFERENCES feeds(id) ON DELETE CASCADE,
 category TEXT NOT NULL, service TEXT NOT NULL, cidr TEXT NOT NULL,
 PRIMARY KEY (feed_id, category, service, cidr)
);
CREATE TABLE users (
 id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE, peer_ip TEXT NOT NULL UNIQUE,
 peer_asn INTEGER NOT NULL, next_hop TEXT, bgp_password TEXT,
 selection_locked INTEGER NOT NULL DEFAULT 0, enabled INTEGER NOT NULL DEFAULT 1
);
CREATE TABLE user_networks (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 cidr TEXT NOT NULL UNIQUE, PRIMARY KEY (user_id, cidr)
);
CREATE TABLE selected_categories (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 category TEXT NOT NULL, PRIMARY KEY (user_id, category)
);
CREATE TABLE selected_services (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 category TEXT NOT NULL, service TEXT NOT NULL,
 PRIMARY KEY (user_id, category, service)
);
INSERT INTO feeds(id, name, url) VALUES
 (1, 'opencck-main', 'https://iplist.opencck.org/?format=json&data=cidr4');
INSERT INTO users(id, name, peer_ip, peer_asn) VALUES (7, 'legacy', '172.16.0.2', 65007);
INSERT INTO user_networks(user_id, cidr) VALUES (7, '192.168.20.0/24');
INSERT INTO selected_categories(user_id, category) VALUES (7, 'opencck-main');
INSERT INTO catalog_entries(feed_id, category, service, cidr) VALUES
 (1, 'opencck-main', 'legacy-service', '8.8.8.0/24');
`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var selectedCount int
	if err := s.DB.QueryRow("SELECT COUNT(*) FROM selected_categories").Scan(&selectedCount); err != nil {
		t.Fatal(err)
	}
	if selectedCount != 0 {
		t.Fatalf("legacy OpenCCK feed category selections = %d, want 0", selectedCount)
	}
	prefixes, err := s.DesiredPrefixes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(prefixes) != 0 {
		t.Fatalf("prefixes = %#v, want empty after dropping legacy feed category selection", prefixes)
	}
}

func TestMigrateLegacyPythonDatabaseDropsOrphanSelections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-orphan-selection.sqlite3")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
CREATE TABLE feeds (
 id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE, url TEXT NOT NULL,
 enabled INTEGER NOT NULL DEFAULT 1, last_success TEXT, last_error TEXT
);
CREATE TABLE catalog_entries (
 feed_id INTEGER NOT NULL REFERENCES feeds(id) ON DELETE CASCADE,
 category TEXT NOT NULL, service TEXT NOT NULL, cidr TEXT NOT NULL,
 PRIMARY KEY (feed_id, category, service, cidr)
);
CREATE TABLE users (
 id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE, peer_ip TEXT NOT NULL UNIQUE,
 peer_asn INTEGER NOT NULL, next_hop TEXT, bgp_password TEXT,
 selection_locked INTEGER NOT NULL DEFAULT 0, enabled INTEGER NOT NULL DEFAULT 1
);
CREATE TABLE user_networks (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 cidr TEXT NOT NULL UNIQUE, PRIMARY KEY (user_id, cidr)
);
CREATE TABLE selected_categories (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 category TEXT NOT NULL, PRIMARY KEY (user_id, category)
);
CREATE TABLE selected_services (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 category TEXT NOT NULL, service TEXT NOT NULL,
 PRIMARY KEY (user_id, category, service)
);
INSERT INTO feeds(id, name, url) VALUES
 (1, 'custom', 'https://example.test/feed.json');
INSERT INTO users(id, name, peer_ip, peer_asn) VALUES (7, 'legacy', '172.16.0.2', 65007);
INSERT INTO user_networks(user_id, cidr) VALUES (7, '192.168.20.0/24');
INSERT INTO selected_categories(user_id, category) VALUES (7, 'old-category');
INSERT INTO selected_services(user_id, category, service) VALUES (7, 'old-category', 'old-service');
INSERT INTO catalog_entries(feed_id, category, service, cidr) VALUES
 (1, 'new-category', 'new-service', '8.8.8.0/24');
`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, table := range []string{"selected_categories", "selected_services"} {
		var count int
		if err := s.DB.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s count = %d, want 0", table, count)
		}
	}
	prefixes, err := s.DesiredPrefixes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(prefixes) != 0 {
		t.Fatalf("prefixes = %#v, want empty after dropping orphan selections", prefixes)
	}
}

func TestRejectNewerDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "newer.sqlite3")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE schema_migrations (
		version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL);
		INSERT INTO schema_migrations VALUES (99, 'future', 'now')`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := Open(path); err == nil {
		t.Fatal("Open accepted a newer database schema")
	}
}

func TestMigrationDeduplicatesFeedsWithoutLosingCatalog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "duplicate-feeds.sqlite3")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
CREATE TABLE feeds (
 id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE, url TEXT NOT NULL,
 enabled INTEGER NOT NULL DEFAULT 1, last_success TEXT, last_error TEXT
);
CREATE TABLE catalog_entries (
 feed_id INTEGER NOT NULL REFERENCES feeds(id) ON DELETE CASCADE,
 category TEXT NOT NULL, service TEXT NOT NULL, cidr TEXT NOT NULL,
 PRIMARY KEY (feed_id, category, service, cidr)
);
INSERT INTO feeds(id, name, url) VALUES
 (1, 'old-name', 'https://example.test/feed'),
 (2, 'new-name', 'https://example.test/feed');
INSERT INTO catalog_entries VALUES
 (1, 'one', 'a', '192.0.2.0/24'),
 (2, 'two', 'b', '198.51.100.0/24');
`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var feedCount, entryCount int
	if err := s.DB.QueryRow("SELECT COUNT(*) FROM feeds WHERE url = 'https://example.test/feed'").
		Scan(&feedCount); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow("SELECT COUNT(*) FROM catalog_entries WHERE feed_id = 1").
		Scan(&entryCount); err != nil {
		t.Fatal(err)
	}
	if feedCount != 1 || entryCount != 2 {
		t.Fatalf("feeds=%d entries=%d, want 1 and 2", feedCount, entryCount)
	}
}

func TestMigrationPreservesLegacyFilterOverrideMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-filter-mode.sqlite3")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
CREATE TABLE schema_migrations (
    version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL
);
INSERT INTO schema_migrations(version, name, applied_at) VALUES
    (1, 'initial schema', 'now'),
    (2, 'add OpenCCK IPv6 feeds', 'now'),
    (3, 'add lookup indexes', 'now'),
    (4, 'deduplicate feeds by URL', 'now'),
    (5, 'add route filters', 'now');
CREATE TABLE users (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    peer_ip TEXT NOT NULL UNIQUE,
    peer_asn INTEGER NOT NULL,
    next_hop TEXT,
    bgp_password TEXT,
    selection_locked INTEGER NOT NULL DEFAULT 0,
    enabled INTEGER NOT NULL DEFAULT 1,
    filter_override_enabled INTEGER NOT NULL DEFAULT 0,
    filter_editable INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE feeds (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    url TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    last_success TEXT,
    last_error TEXT
);
CREATE TABLE catalog_entries (
    feed_id INTEGER NOT NULL REFERENCES feeds(id) ON DELETE CASCADE,
    category TEXT NOT NULL,
    service TEXT NOT NULL,
    cidr TEXT NOT NULL,
    PRIMARY KEY (feed_id, category, service, cidr)
);
CREATE TABLE user_networks (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    cidr TEXT NOT NULL UNIQUE,
    PRIMARY KEY (user_id, cidr)
);
CREATE TABLE selected_categories (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    category TEXT NOT NULL,
    PRIMARY KEY (user_id, category)
);
CREATE TABLE selected_services (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    category TEXT NOT NULL,
    service TEXT NOT NULL,
    PRIMARY KEY (user_id, category, service)
);
INSERT INTO users(id, name, peer_ip, peer_asn, filter_override_enabled)
VALUES (7, 'legacy', '172.16.0.2', 65007, 1);
INSERT INTO user_networks(user_id, cidr) VALUES (7, '192.168.20.0/24');
`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	user, err := s.User(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if user.FilterMode != FilterModeOverride || !user.FilterOverride {
		t.Fatalf("legacy filter mode not preserved: %#v", user)
	}
}

func TestCatalogModeMigrationPreservesExistingSelections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "version-8.sqlite3")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA foreign_keys = ON;
CREATE TABLE schema_migrations (
    version INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    applied_at TEXT NOT NULL
)`); err != nil {
		t.Fatal(err)
	}
	for _, migration := range migrations[:8] {
		if _, err := db.Exec(migration.SQL); err != nil {
			t.Fatalf("apply migration %d: %v", migration.Version, err)
		}
		if _, err := db.Exec(
			"INSERT INTO schema_migrations(version, name, applied_at) VALUES (?, ?, 'now')",
			migration.Version, migration.Name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`
INSERT INTO catalog_entries(feed_id, category, service, cidr)
VALUES (1, 'Messengers', 'Telegram', '149.154.160.0/20');
INSERT INTO users(id, name, peer_ip, peer_asn)
VALUES (7, 'existing', '172.16.0.2', 65007);
INSERT INTO user_networks(user_id, cidr) VALUES (7, '192.168.20.0/24');
INSERT INTO selected_categories(user_id, category) VALUES (7, 'Messengers');
INSERT INTO selected_services(user_id, category, service)
VALUES (7, 'Messengers', 'Telegram');
`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	user, err := s.User(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	categories, services, err := s.UserModeSelection(context.Background(), 7, 1)
	if err != nil {
		t.Fatal(err)
	}
	if user.CatalogModeID != 1 || user.CatalogEditable ||
		!categories["Messengers"] ||
		!services[ServiceKey{Category: "Messengers", Service: "Telegram"}] {
		t.Fatalf("migrated user=%#v categories=%#v services=%#v", user, categories, services)
	}
}

func TestCatalogModeMigrationReusesExistingIPRangesFeeds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "version-8-ipranges.sqlite3")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA foreign_keys = ON;
CREATE TABLE schema_migrations (
    version INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    applied_at TEXT NOT NULL
)`); err != nil {
		t.Fatal(err)
	}
	for _, migration := range migrations[:8] {
		if _, err := db.Exec(migration.SQL); err != nil {
			t.Fatalf("apply migration %d: %v", migration.Version, err)
		}
		if _, err := db.Exec(
			"INSERT INTO schema_migrations(version, name, applied_at) VALUES (?, ?, 'now')",
			migration.Version, migration.Name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`
INSERT INTO feeds(id, name, url) VALUES
    (20, 'ipranges', 'https://example.test/old-ipranges'),
    (21, 'lord-alfred', 'https://github.com/lord-alfred/ipranges'),
    (22, 'antonme', 'https://github.com/antonme/ipranges');
INSERT INTO catalog_entries(feed_id, category, service, cidr) VALUES
    (20, 'Platforms', 'Telegram', '149.154.160.0/20'),
    (21, 'Platforms', 'Discord', '162.159.128.0/17'),
    (22, 'Platforms', 'YouTube', '142.250.0.0/15');
`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var feedID, modeID, feedCount, entryCount int64
	var name, feedURL string
	if err := s.DB.QueryRow(`
SELECT id, name, url, mode_id FROM feeds
WHERE name = 'ipranges' OR url = 'https://github.com/antonme/ipranges'
`).Scan(&feedID, &name, &feedURL, &modeID); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(`
SELECT COUNT(*) FROM feeds
WHERE name = 'ipranges'
   OR url = 'https://github.com/lord-alfred/ipranges'
   OR url = 'https://github.com/antonme/ipranges'
`).Scan(&feedCount); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(
		"SELECT COUNT(*) FROM catalog_entries WHERE feed_id = ?", feedID).
		Scan(&entryCount); err != nil {
		t.Fatal(err)
	}
	if feedID != 20 || name != "ipranges" ||
		feedURL != "https://github.com/antonme/ipranges" ||
		modeID != IPRangesCatalogModeID ||
		feedCount != 1 || entryCount != 0 {
		t.Fatalf("feed=%d name=%q url=%q mode=%d feeds=%d entries=%d",
			feedID, name, feedURL, modeID, feedCount, entryCount)
	}
}

func TestDesiredPrefixesForCategoryAndService(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	userID, err := s.AddUser(ctx, User{
		Name: "client", PeerIP: "172.16.0.2", PeerASN: 65001, Enabled: true,
		Networks: []string{"192.168.20.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.DB.Exec(`INSERT INTO catalog_entries(feed_id, category, service, cidr) VALUES
		(1, 'Messengers', 'Telegram', '149.154.160.0/20'),
		(1, 'Messengers', 'Signal', '76.223.92.0/24'),
		(1, 'AI', 'Copilot', '140.82.112.0/20')`)
	if err != nil {
		t.Fatal(err)
	}
	err = s.Transaction(ctx, func(tx *sql.Tx) error {
		return SetUserSelection(ctx, tx, userID, []string{"Messengers"},
			[]ServiceKey{{Category: "AI", Service: "Copilot"}})
	})
	if err != nil {
		t.Fatal(err)
	}
	prefixes, err := s.DesiredPrefixes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(prefixes) != 3 {
		t.Fatalf("prefix count = %d, want 3: %#v", len(prefixes), prefixes)
	}
	for prefix, users := range prefixes {
		if len(users) != 1 || users[0] != userID {
			t.Fatalf("%s has unexpected users: %#v", prefix, users)
		}
	}
}

func TestDesiredPrefixesEmptyWithoutSelection(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.AddUser(ctx, User{
		Name: "client", PeerIP: "172.16.0.2", PeerASN: 65001, Enabled: true,
		Networks: []string{"192.168.20.0/24"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO catalog_entries(feed_id, category, service, cidr) VALUES
		(1, 'Messengers', 'Telegram', '149.154.160.0/20'),
		(1, 'AI', 'Copilot', '140.82.112.0/20')`); err != nil {
		t.Fatal(err)
	}

	prefixes, err := s.DesiredPrefixes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(prefixes) != 0 {
		t.Fatalf("prefixes = %#v, want empty without user selection", prefixes)
	}
}

func TestCatalogModesKeepSelectionsAndRoutesIsolated(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	modes, err := s.CatalogModes(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(modes) != 2 || modes[0].Key != "opencck" || modes[1].Key != "ipranges" {
		t.Fatalf("catalog modes = %#v", modes)
	}
	ipranges := modes[1]
	ipranges.Enabled = true
	if err := s.UpdateCatalogMode(ctx, ipranges); err != nil {
		t.Fatal(err)
	}

	var openCCKFeedID, ipRangesFeedID int64
	if err := s.DB.QueryRow("SELECT id FROM feeds WHERE mode_id = 1 ORDER BY id LIMIT 1").
		Scan(&openCCKFeedID); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow("SELECT id FROM feeds WHERE mode_id = ?", ipranges.ID).
		Scan(&ipRangesFeedID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO catalog_entries(feed_id, category, service, cidr) VALUES
		(?, 'Open', 'Wide', '8.8.0.0/16'),
		(?, 'Precise', 'Narrow', '8.8.8.0/24')`,
		openCCKFeedID, ipRangesFeedID); err != nil {
		t.Fatal(err)
	}
	userID, err := s.AddUser(ctx, User{
		Name: "client", PeerIP: "172.16.0.2", PeerASN: 65001, Enabled: true,
		CatalogModeID: 1, CatalogEditable: true,
		Networks: []string{"192.168.20.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		if err := SetUserModeSelection(ctx, tx, userID, 1, []string{"Open"}, nil); err != nil {
			return err
		}
		return SetUserModeSelection(ctx, tx, userID, ipranges.ID, []string{"Precise"}, nil)
	}); err != nil {
		t.Fatal(err)
	}

	prefixes, err := s.DesiredPrefixes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(prefixes) != 1 || len(prefixes["8.8.0.0/16"]) != 1 {
		t.Fatalf("OpenCCK prefixes = %#v", prefixes)
	}
	if err := s.SetUserCatalogMode(ctx, userID, ipranges.ID, true); err != nil {
		t.Fatal(err)
	}
	prefixes, err = s.DesiredPrefixes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(prefixes) != 1 || len(prefixes["8.8.8.0/24"]) != 1 {
		t.Fatalf("IPRanges prefixes = %#v", prefixes)
	}

	ipranges.Enabled = false
	if err := s.UpdateCatalogMode(ctx, ipranges); err != nil {
		t.Fatal(err)
	}
	prefixes, err = s.DesiredPrefixes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(prefixes) != 0 {
		t.Fatalf("disabled mode prefixes = %#v", prefixes)
	}
	categories, _, err := s.UserModeSelection(ctx, userID, ipranges.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !categories["Precise"] {
		t.Fatalf("disabled mode selection was lost: %#v", categories)
	}
}

func TestUserCannotChangeCatalogModeWithoutPermission(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	modes, err := s.CatalogModes(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	ipranges := modes[1]
	ipranges.Enabled = true
	if err := s.UpdateCatalogMode(ctx, ipranges); err != nil {
		t.Fatal(err)
	}
	userID, err := s.AddUser(ctx, User{
		Name: "managed", PeerIP: "172.16.0.2", PeerASN: 65001, Enabled: true,
		CatalogModeID: 1, Networks: []string{"192.168.20.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetUserCatalogMode(ctx, userID, ipranges.ID, true); !IsNotFound(err) {
		t.Fatalf("mode change error = %v, want not found", err)
	}
	if err := s.SetUserCatalogMode(ctx, userID, 1, true); err != nil {
		t.Fatalf("saving current managed mode: %v", err)
	}
}

func TestDisabledFeedIsExcludedWithoutDeletingSnapshot(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.AddFeed(ctx, "custom", "https://example.test/feed.json", true); err != nil {
		t.Fatal(err)
	}
	feeds, err := s.Feeds(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	feed := feeds[len(feeds)-1]
	if _, err := s.DB.Exec(`INSERT INTO catalog_entries(feed_id, category, service, cidr)
		VALUES (?, 'Custom', 'Example', '8.8.8.0/24')`, feed.ID); err != nil {
		t.Fatal(err)
	}
	userID, err := s.AddUser(ctx, User{
		Name: "client", PeerIP: "172.16.0.2", PeerASN: 65001, Enabled: true,
		Networks: []string{"192.168.20.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		return SetUserSelection(ctx, tx, userID, []string{"Custom"}, nil)
	}); err != nil {
		t.Fatal(err)
	}

	catalog, err := s.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog["Custom"]) != 1 {
		t.Fatalf("enabled feed missing from catalog: %#v", catalog)
	}
	prefixes, err := s.DesiredPrefixes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(prefixes["8.8.8.0/24"]) != 1 {
		t.Fatalf("enabled feed prefix missing: %#v", prefixes)
	}

	feed.Enabled = false
	if err := s.UpdateFeed(ctx, feed); err != nil {
		t.Fatal(err)
	}
	catalog, err = s.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := catalog["Custom"]; ok {
		t.Fatalf("disabled feed remains in catalog: %#v", catalog)
	}
	prefixes, err = s.DesiredPrefixes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(prefixes) != 0 {
		t.Fatalf("disabled feed remains announced: %#v", prefixes)
	}
	var entries int
	if err := s.DB.QueryRow("SELECT COUNT(*) FROM catalog_entries WHERE feed_id = ?", feed.ID).
		Scan(&entries); err != nil {
		t.Fatal(err)
	}
	if entries != 1 {
		t.Fatalf("disabled feed snapshot entries = %d, want 1", entries)
	}

	feed.Enabled = true
	if err := s.UpdateFeed(ctx, feed); err != nil {
		t.Fatal(err)
	}
	prefixes, err = s.DesiredPrefixes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(prefixes["8.8.8.0/24"]) != 1 {
		t.Fatalf("re-enabled feed prefix missing: %#v", prefixes)
	}
}

func TestSetVisibleUserSelectionPreservesDisabledOnlySelections(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.DB.Exec("UPDATE feeds SET enabled = 0"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddFeed(ctx, "enabled", "https://example.test/enabled", true); err != nil {
		t.Fatal(err)
	}
	if err := s.AddFeed(ctx, "disabled", "https://example.test/disabled", false); err != nil {
		t.Fatal(err)
	}
	feeds, err := s.Feeds(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	var enabledID, disabledID int64
	for _, feed := range feeds {
		switch feed.Name {
		case "enabled":
			enabledID = feed.ID
		case "disabled":
			disabledID = feed.ID
		}
	}
	if _, err := s.DB.Exec(`INSERT INTO catalog_entries(feed_id, category, service, cidr) VALUES
		(?, 'Visible', 'Keep', '8.8.8.0/24'),
		(?, 'Visible', 'Remove', '8.8.4.0/24'),
		(?, 'Shared', 'Service', '9.9.9.0/24'),
		(?, 'HiddenCategory', 'Any', '1.1.1.0/24'),
		(?, 'HiddenServices', 'Hidden', '1.0.0.0/24'),
		(?, 'Shared', 'Service', '2.2.2.0/24')`,
		enabledID, enabledID, enabledID, disabledID, disabledID, disabledID); err != nil {
		t.Fatal(err)
	}
	userID, err := s.AddUser(ctx, User{
		Name: "client", PeerIP: "172.16.0.2", PeerASN: 65001, Enabled: true,
		Networks: []string{"192.168.20.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		return SetUserSelection(ctx, tx, userID,
			[]string{"HiddenCategory", "Shared"},
			[]ServiceKey{
				{Category: "HiddenServices", Service: "Hidden"},
				{Category: "Visible", Service: "Remove"},
			})
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		return SetVisibleUserSelection(ctx, tx, userID, nil,
			[]ServiceKey{{Category: "Visible", Service: "Keep"}})
	}); err != nil {
		t.Fatal(err)
	}

	categories, services, err := s.UserSelection(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(categories) != 1 || !categories["HiddenCategory"] {
		t.Fatalf("categories = %#v, want disabled-only category", categories)
	}
	wantServices := map[ServiceKey]bool{
		{Category: "HiddenServices", Service: "Hidden"}: true,
		{Category: "Visible", Service: "Keep"}:          true,
	}
	if len(services) != len(wantServices) {
		t.Fatalf("services = %#v, want %#v", services, wantServices)
	}
	for service := range wantServices {
		if !services[service] {
			t.Fatalf("service %v was not preserved/saved: %#v", service, services)
		}
	}
}

func TestUpdateFeedURLClearsSnapshotAndDeleteCascades(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.AddFeed(ctx, "custom", "https://example.test/old.json", true); err != nil {
		t.Fatal(err)
	}
	feeds, err := s.Feeds(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	feed := feeds[len(feeds)-1]
	if _, err := s.DB.Exec(`
UPDATE feeds SET last_success = 'now', last_error = 'old error' WHERE id = ?;
INSERT INTO catalog_entries(feed_id, category, service, cidr)
VALUES (?, 'Custom', 'Example', '8.8.8.0/24')`, feed.ID, feed.ID); err != nil {
		t.Fatal(err)
	}

	feed.Name = "renamed"
	feed.URL = "https://example.test/new.json"
	if err := s.UpdateFeed(ctx, feed); err != nil {
		t.Fatal(err)
	}
	feeds, err = s.Feeds(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	updated := feeds[len(feeds)-1]
	if updated.Name != feed.Name || updated.URL != feed.URL ||
		updated.LastSuccess != "" || updated.LastError != "" {
		t.Fatalf("updated feed = %#v", updated)
	}
	var entries int
	if err := s.DB.QueryRow("SELECT COUNT(*) FROM catalog_entries WHERE feed_id = ?", feed.ID).
		Scan(&entries); err != nil {
		t.Fatal(err)
	}
	if entries != 0 {
		t.Fatalf("entries after URL change = %d, want 0", entries)
	}

	if _, err := s.DB.Exec(`INSERT INTO catalog_entries(feed_id, category, service, cidr)
		VALUES (?, 'Custom', 'Example', '8.8.8.0/24')`, feed.ID); err != nil {
		t.Fatal(err)
	}
	userID, err := s.AddUser(ctx, User{
		Name: "selected", PeerIP: "172.16.0.2", PeerASN: 65001, Enabled: true,
		Networks: []string{"192.168.20.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		return SetUserSelection(ctx, tx, userID, []string{"Custom"},
			[]ServiceKey{{Category: "Custom", Service: "Example"}})
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteFeed(ctx, feed.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow("SELECT COUNT(*) FROM catalog_entries WHERE feed_id = ?", feed.ID).
		Scan(&entries); err != nil {
		t.Fatal(err)
	}
	if entries != 0 {
		t.Fatalf("entries after feed deletion = %d, want 0", entries)
	}
	for _, table := range []string{"selected_categories", "selected_services"} {
		var selections int
		if err := s.DB.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE user_id = ?", userID).
			Scan(&selections); err != nil {
			t.Fatal(err)
		}
		if selections != 0 {
			t.Fatalf("%s after feed deletion = %d, want 0", table, selections)
		}
	}
}

func TestDesiredPrefixesSubtractsGlobalDeny(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	userID := addFilteredTestUser(t, s, false)
	if err := s.SetGlobalRouteFilters(ctx, RouteFilters{Deny: []string{"1.1.1.1/32"}}); err != nil {
		t.Fatal(err)
	}

	prefixes, err := s.DesiredPrefixes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(prefixes) != 24 {
		t.Fatalf("prefix count = %d, want 24", len(prefixes))
	}
	for rawPrefix, users := range prefixes {
		prefix := netip.MustParsePrefix(rawPrefix)
		if prefix.Contains(netip.MustParseAddr("1.1.1.1")) {
			t.Fatalf("denied address remains covered by %s", prefix)
		}
		if len(users) != 1 || users[0] != userID {
			t.Fatalf("%s has unexpected users: %v", rawPrefix, users)
		}
	}
}

func TestDesiredPrefixesUsesUserOverride(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	userID := addFilteredTestUser(t, s, true)
	if err := s.SetGlobalRouteFilters(ctx, RouteFilters{Deny: []string{"1.1.1.1/32"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetUserRouteFilters(ctx, userID, RouteFilters{Allow: []string{"1.1.0.0/16"}}); err != nil {
		t.Fatal(err)
	}

	prefixes, err := s.DesiredPrefixes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(prefixes) != 1 {
		t.Fatalf("prefixes = %v, want one user override prefix", prefixes)
	}
	if users := prefixes["1.1.0.0/16"]; len(users) != 1 || users[0] != userID {
		t.Fatalf("override prefix users = %v", users)
	}
}

func TestDesiredPrefixesExtendsGlobalFilters(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	userID := addFilteredTestUser(t, s, false)
	if err := s.SetUserFilterOverride(ctx, userID, false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetUserRouteFilterConfig(ctx, userID, FilterModeExtend,
		RouteFilters{Allow: []string{"1.1.0.0/16"}, Deny: []string{"1.1.1.1/32"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetGlobalRouteFilters(ctx, RouteFilters{Deny: []string{"1.1.2.0/24"}}); err != nil {
		t.Fatal(err)
	}

	prefixes, err := s.DesiredPrefixes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for rawPrefix, users := range prefixes {
		prefix := netip.MustParsePrefix(rawPrefix)
		if !prefixContains(netip.MustParsePrefix("1.1.0.0/16"), prefix) {
			t.Fatalf("extended allow leaked prefix %s", rawPrefix)
		}
		if prefix.Contains(netip.MustParseAddr("1.1.1.1")) || prefix.Contains(netip.MustParseAddr("1.1.2.1")) {
			t.Fatalf("extended deny remains covered by %s", rawPrefix)
		}
		if len(users) != 1 || users[0] != userID {
			t.Fatalf("%s has unexpected users: %v", rawPrefix, users)
		}
	}
	if len(prefixes) == 0 {
		t.Fatal("extended filters produced no prefixes")
	}
}

func TestDesiredPrefixesDropsFeedDefaultRoute(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	userID, err := s.AddUser(ctx, User{
		Name: "default-route", PeerIP: "172.16.0.2", PeerASN: 65001, Enabled: true,
		Networks: []string{"192.168.20.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO catalog_entries(feed_id, category, service, cidr) VALUES
		(1, 'test', 'default', '0.0.0.0/0'),
		(1, 'test', 'public', '8.8.8.0/24')`); err != nil {
		t.Fatal(err)
	}
	if err := s.SetGlobalRouteFilters(ctx, RouteFilters{}); err != nil {
		t.Fatal(err)
	}
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		return SetUserSelection(ctx, tx, userID, []string{"test"}, nil)
	}); err != nil {
		t.Fatal(err)
	}

	prefixes, err := s.DesiredPrefixes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(prefixes) != 1 {
		t.Fatalf("prefixes = %v, want only the non-default route", prefixes)
	}
	if users := prefixes["8.8.8.0/24"]; len(users) != 1 || users[0] != userID {
		t.Fatalf("public prefix users = %v", users)
	}
}

func addFilteredTestUser(t *testing.T, s *Store, override bool) int64 {
	t.Helper()
	ctx := context.Background()
	userID, err := s.AddUser(ctx, User{
		Name: "filtered", PeerIP: "172.16.0.2", PeerASN: 65001, Enabled: true,
		FilterOverride: override, Networks: []string{"192.168.20.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO catalog_entries(feed_id, category, service, cidr)
		VALUES (1, 'test', 'wide', '1.0.0.0/8')`); err != nil {
		t.Fatal(err)
	}
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		return SetUserSelection(ctx, tx, userID, []string{"test"}, nil)
	}); err != nil {
		t.Fatal(err)
	}
	return userID
}

func prefixContains(parent, child netip.Prefix) bool {
	return parent.Contains(child.Addr()) && child.Bits() >= parent.Bits()
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
