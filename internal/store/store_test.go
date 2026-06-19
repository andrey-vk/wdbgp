package store

import (
	"context"
	"database/sql"
	"net/netip"
	"path/filepath"
	"strconv"
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
	if len(feeds) != 6 {
		t.Fatalf("feed count = %d, want 6", len(feeds))
	}
	adapters, err := s.FeedAdapters(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(adapters) != 4 {
		t.Fatalf("adapter count = %d, want 4", len(adapters))
	}
	for _, adapter := range adapters {
		if adapter.Source == "" || adapter.Revision != 1 {
			t.Fatalf("built-in adapter was not seeded: %#v", adapter)
		}
	}
	for _, feed := range feeds {
		wantAdapterID := int64(2)
		if feed.Name == "ipranges" {
			wantAdapterID = 3
		}
		if feed.Name == "Russia GeoIP (SRS)" {
			wantAdapterID = 4
		}
		if feed.AdapterID != wantAdapterID {
			t.Fatalf("feed %q adapter = %d, want %d",
				feed.Name, feed.AdapterID, wantAdapterID)
		}
	}
}

func TestResetBuiltInFeedAdapter(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	original, err := s.FeedAdapter(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	changed := original
	changed.Name = "Changed"
	changed.Source = "function sync() { return []; }\n"
	changed.AllowedHosts = "example.test"
	if err := s.UpdateFeedAdapter(ctx, changed); err != nil {
		t.Fatal(err)
	}
	if err := s.ResetFeedAdapter(ctx, changed.ID); err != nil {
		t.Fatal(err)
	}
	reset, err := s.FeedAdapter(ctx, changed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reset.Name != original.Name ||
		reset.Source != original.Source ||
		reset.AllowedHosts != original.AllowedHosts ||
		reset.Revision != original.Revision+2 ||
		!reset.BuiltIn {
		t.Fatalf("reset adapter = %#v, original = %#v", reset, original)
	}

	customID, err := s.AddFeedAdapter(ctx, FeedAdapter{
		Key: "custom", Name: "Custom",
		Source: "function sync() { return []; }\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ResetFeedAdapter(ctx, customID); err == nil {
		t.Fatal("custom adapter reset succeeded")
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
	if len(feeds) != 6 {
		t.Fatalf("legacy feed count = %d, want 6", len(feeds))
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
	prefixes, _, err := s.DesiredPrefixes(context.Background())
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
	prefixes, _, err := s.DesiredPrefixes(context.Background())
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
SELECT f.id, f.name, f.url, cmf.mode_id FROM feeds f
JOIN catalog_mode_feeds cmf ON cmf.feed_id = f.id
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
	prefixes, _, err := s.DesiredPrefixes(ctx)
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

	prefixes, _, err := s.DesiredPrefixes(ctx)
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
	if len(modes) != 3 || modes[0].Key != "opencck" || modes[1].Key != "ipranges" || modes[2].Key != "singbox-srs" {
		t.Fatalf("catalog modes = %#v", modes)
	}
	ipranges := modes[1]
	ipranges.Enabled = true
	if err := s.UpdateCatalogMode(ctx, ipranges); err != nil {
		t.Fatal(err)
	}

	var openCCKFeedID, ipRangesFeedID int64
	if err := s.DB.QueryRow(	"SELECT f.id FROM feeds f JOIN catalog_mode_feeds cmf ON cmf.feed_id = f.id WHERE cmf.mode_id = 1 ORDER BY f.id LIMIT 1").
		Scan(&openCCKFeedID); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(		"SELECT f.id FROM feeds f JOIN catalog_mode_feeds cmf ON cmf.feed_id = f.id WHERE cmf.mode_id = ?", ipranges.ID).
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

	prefixes, _, err := s.DesiredPrefixes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(prefixes) != 1 || len(prefixes["8.8.0.0/16"]) != 1 {
		t.Fatalf("OpenCCK prefixes = %#v", prefixes)
	}
	if err := s.SetUserCatalogMode(ctx, userID, ipranges.ID, true); err != nil {
		t.Fatal(err)
	}
	prefixes, _, err = s.DesiredPrefixes(ctx)
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
	prefixes, _, err = s.DesiredPrefixes(ctx)
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
	if err := s.AddFeed(ctx, "custom", "https://example.test/feed.json", true, 0); err != nil {
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
	prefixes, _, err := s.DesiredPrefixes(ctx)
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
	prefixes, _, err = s.DesiredPrefixes(ctx)
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
	prefixes, _, err = s.DesiredPrefixes(ctx)
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
	if err := s.AddFeed(ctx, "enabled", "https://example.test/enabled", true, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.AddFeed(ctx, "disabled", "https://example.test/disabled", false, 0); err != nil {
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
	if err := s.AddFeed(ctx, "custom", "https://example.test/old.json", true, 0); err != nil {
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

	prefixes, _, err := s.DesiredPrefixes(ctx)
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

	prefixes, _, err := s.DesiredPrefixes(ctx)
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

	prefixes, _, err := s.DesiredPrefixes(ctx)
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

	prefixes, _, err := s.DesiredPrefixes(ctx)
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

func TestCountSelectionPrefixes(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	// Get a feed ID for mode 1 (opencck, already enabled)
	var feedID int64
	if err := s.DB.QueryRow(	"SELECT f.id FROM feeds f JOIN catalog_mode_feeds cmf ON cmf.feed_id = f.id WHERE cmf.mode_id = 1 ORDER BY f.id LIMIT 1").Scan(&feedID); err != nil {
		t.Fatal(err)
	}

	// Insert catalog entries with both IPv4 and IPv6 prefixes
	if _, err := s.DB.Exec(`INSERT INTO catalog_entries(feed_id, category, service, cidr) VALUES
		(?, 'CountTest', 'ServiceA', '8.8.8.0/24'),
		(?, 'CountTest', 'ServiceA', '2a01::/32'),
		(?, 'CountTest', 'ServiceB', '37.228.0.0/24')`,
		feedID, feedID, feedID); err != nil {
		t.Fatal(err)
	}

	// Add a user with mode 1
	userID, err := s.AddUser(ctx, User{
		Name: "client", PeerIP: "172.16.0.2", PeerASN: 65001, Enabled: true,
		CatalogModeID: 1,
		Networks:      []string{"192.168.20.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Select the CountTest category
	err = s.Transaction(ctx, func(tx *sql.Tx) error {
		return SetUserSelection(ctx, tx, userID, []string{"CountTest"}, nil)
	})
	if err != nil {
		t.Fatal(err)
	}

	// Count all prefixes (both IPv4 and IPv6)
	v4, v6, err := s.CountSelectionPrefixes(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if v4 != 2 {
		t.Fatalf("CountSelectionPrefixes v4 = %d, want 2", v4)
	}
	if v6 != 1 {
		t.Fatalf("CountSelectionPrefixes v6 = %d, want 1", v6)
	}
}

// =============================================================================
// Migration 20 idempotency tests (Part C)
// =============================================================================

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
	db.Close()

	// Now open — this triggers migration 20. It should succeed.
	s, err := Open(path)
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
	db.Close()

	// Open the DB — this triggers migration 20.  The transactional SQL
	// part (feed_adapters columns, catalog_mode_feeds table) will succeed,
	// but the NoTxSQL (users table rebuild) MUST fail because the source
	// users table is missing the catalog_mode_editable column.
	s, err := Open(path)
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
	db.Close()

	s, err := Open(path)
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

// TestMigrationReopenPreservesData verifies that re-opening a fully migrated
// database preserves all data.
func TestMigrationReopenPreservesData(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "reopen.sqlite3")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// Add some data
	_, err = s.AddUser(ctx, User{
		Name: "persist", PeerIP: "10.0.1.1", PeerASN: 65001, Enabled: true,
		Networks: []string{"192.168.100.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddFeedForModeAdapter(
		ctx, "persist-feed", "https://example.test/persist", 1, 1, true, 0, ""); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// Reopen the same database
	s2, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()

	var userCount, feedCount int
	if err := s2.DB.QueryRow("SELECT COUNT(*) FROM users").Scan(&userCount); err != nil {
		t.Fatal(err)
	}
	if userCount < 1 {
		t.Fatal("user data lost on reopen")
	}
	if err := s2.DB.QueryRow("SELECT COUNT(*) FROM feeds").Scan(&feedCount); err != nil {
		t.Fatal(err)
	}
	if feedCount < 1 {
		t.Fatal("feed data lost on reopen")
	}
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
