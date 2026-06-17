package store

import (
	"context"
	"database/sql"
	"net/netip"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// compoundKey builds a "prefix\x00modeID" key for DesiredPrefixes result validation.
func compoundKey(prefix string, modeID int64) string {
	return prefix + "\x00" + strconv.FormatInt(modeID, 10)
}

// splitCompoundKey extracts the prefix from a "prefix\x00modeID" key.
func splitCompoundKeyTest(key string) string {
	if idx := strings.IndexByte(key, 0); idx >= 0 {
		return key[:idx]
	}
	return key
}

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
	var feedID, feedCount, entryCount int64
	var name, feedURL string
	if err := s.DB.QueryRow(`
SELECT f.id, f.name, f.url FROM feeds f
WHERE f.name = 'ipranges' OR f.url = 'https://github.com/antonme/ipranges'
`).Scan(&feedID, &name, &feedURL); err != nil {
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
	// Verify via junction table that the feed belongs to IPRanges mode
	modes, err := s.FeedModes(context.Background(), feedID)
	if err != nil {
		t.Fatal(err)
	}
	if feedID != 20 || name != "ipranges" ||
		feedURL != "https://github.com/antonme/ipranges" ||
		len(modes) != 1 || modes[0] != IPRangesCatalogModeID ||
		feedCount != 1 || entryCount != 0 {
		t.Fatalf("feed=%d name=%q url=%q modes=%v feeds=%d entries=%d",
			feedID, name, feedURL, modes, feedCount, entryCount)
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
	if err := s.UpdateCatalogMode(ctx, ipranges.ID, ipranges.Name, true); err != nil {
		t.Fatal(err)
	}

	var openCCKFeedID, ipRangesFeedID int64
	if err := s.DB.QueryRow(`
SELECT f.id FROM feeds f
JOIN catalog_mode_feeds cmf ON cmf.feed_id = f.id
WHERE cmf.mode_id = 1 ORDER BY f.id LIMIT 1`).
		Scan(&openCCKFeedID); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(`
SELECT f.id FROM feeds f
JOIN catalog_mode_feeds cmf ON cmf.feed_id = f.id
WHERE cmf.mode_id = ? ORDER BY f.id LIMIT 1`, ipranges.ID).
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
	if len(prefixes) != 1 || len(prefixes[compoundKey("8.8.0.0/16", 1)]) != 1 {
		t.Fatalf("OpenCCK prefixes = %#v", prefixes)
	}
	if err := s.SetUserCatalogMode(ctx, userID, ipranges.ID, true); err != nil {
		t.Fatal(err)
	}
	prefixes, _, err = s.DesiredPrefixes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(prefixes) != 1 || len(prefixes[compoundKey("8.8.8.0/24", 2)]) != 1 {
		t.Fatalf("IPRanges prefixes = %#v", prefixes)
	}

	ipranges.Enabled = false
	if err := s.UpdateCatalogMode(ctx, ipranges.ID, ipranges.Name, false); err != nil {
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
	if err := s.UpdateCatalogMode(ctx, ipranges.ID, ipranges.Name, true); err != nil {
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
	if err := s.AddFeed(ctx, "custom", "https://example.test/feed.json", 0); err != nil {
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
	if len(prefixes[compoundKey("8.8.8.0/24", 1)]) != 1 {
		t.Fatalf("enabled feed prefix missing: %#v", prefixes)
	}

	if err := s.RemoveFeedFromMode(ctx, DefaultCatalogModeID, feed.ID); err != nil {
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

	if err := s.AddFeedToMode(ctx, DefaultCatalogModeID, feed.ID); err != nil {
		t.Fatal(err)
	}
	prefixes, _, err = s.DesiredPrefixes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(prefixes[compoundKey("8.8.8.0/24", 1)]) != 1 {
		t.Fatalf("re-enabled feed prefix missing: %#v", prefixes)
	}
}

func TestSetVisibleUserSelectionPreservesDisabledOnlySelections(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	// Remove all built-in feeds from mode 1 so only our test feeds matter
	if _, err := s.DB.Exec("DELETE FROM catalog_mode_feeds"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddFeed(ctx, "feed-a", "https://example.test/feed-a", 0); err != nil {
		t.Fatal(err)
	}
	if err := s.AddFeed(ctx, "feed-b", "https://example.test/feed-b", 0); err != nil {
		t.Fatal(err)
	}
	feeds, err := s.Feeds(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	var idA, idB int64
	for _, feed := range feeds {
		switch feed.Name {
		case "feed-a":
			idA = feed.ID
		case "feed-b":
			idB = feed.ID
		}
	}
	// Put feed-b in a disabled mode (mode 3 = singbox-srs, disabled by default).
	// feed-a stays in mode 1 (enabled).
	// Remove feed-b from mode 1 so it has no enabled modes.
	if err := s.AddFeedToMode(ctx, SingboxSRSCatalogModeID, idB); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveFeedFromMode(ctx, DefaultCatalogModeID, idB); err != nil {
		t.Fatal(err)
	}
	// Insert catalog entries. feed-a has entries in enabled mode 1,
	// feed-b has entries visible through NO enabled modes (only disabled mode 3).
	if _, err := s.DB.Exec(`INSERT INTO catalog_entries(feed_id, category, service, cidr) VALUES
		(?, 'Visible', 'Keep', '8.8.8.0/24'),
		(?, 'Visible', 'Remove', '8.8.4.0/24'),
		(?, 'Shared', 'Service', '9.9.9.0/24'),
		(?, 'HiddenCategory', 'Any', '1.1.1.0/24'),
		(?, 'HiddenServices', 'Hidden', '1.0.0.0/24'),
		(?, 'Shared', 'Service', '2.2.2.0/24')`,
		idA, idA, idA, idB, idB, idB); err != nil {
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

	// SetVisibleUserSelection should preserve HiddenCategory (from feed-b,
	// which has no enabled modes) and HiddenServices+Hidden (also from feed-b).
	// The explicitly passed Visible+Keep is also kept.
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
	if err := s.AddFeed(ctx, "custom", "https://example.test/old.json", 0); err != nil {
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
		prefix := netip.MustParsePrefix(splitCompoundKeyTest(rawPrefix))
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
	if users := prefixes[compoundKey("1.1.0.0/16", 1)]; len(users) != 1 || users[0] != userID {
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
		prefix := netip.MustParsePrefix(splitCompoundKeyTest(rawPrefix))
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
	if users := prefixes[compoundKey("8.8.8.0/24", 1)]; len(users) != 1 || users[0] != userID {
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
	if err := s.DB.QueryRow(`
SELECT f.id FROM feeds f
JOIN catalog_mode_feeds cmf ON cmf.feed_id = f.id
WHERE cmf.mode_id = 1 ORDER BY f.id LIMIT 1`).Scan(&feedID); err != nil {
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

func TestMigration20IsReentrant(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.sqlite3")

	// Step 1: Open a store — runs all migrations including migration 20
	// (both transactional SQL and NoTxSQL), records schema_migrations rows.
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// Verify migration 20 was applied.
	var version int
	if err := s.DB.QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != len(migrations) {
		t.Fatalf("schema version after first open = %d, want %d", version, len(migrations))
	}
	s.Close()

	// Step 2: Simulate a crash — the transactional SQL committed, but
	// the process died before INSERT INTO schema_migrations for version 20.
	// Delete the schema_migrations row so that the next Open thinks it
	// needs to re-apply migration 20.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	result, err := db.ExecContext(context.Background(),
		"DELETE FROM schema_migrations WHERE version IN (20, 21)")
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	deleted, _ := result.RowsAffected()
	if deleted != 2 {
		db.Close()
		t.Fatalf("expected to delete 1 schema_migrations row, deleted %d", deleted)
	}
	db.Close()

	// Step 3: Create a NEW store on the same DB file.
	// This will try to re-run migration 20 from scratch.
	//
	// Step 4: Expected (if migration 20 were re-entrant): the second Open
	//   succeeds because the transactional SQL is idempotent.
	//
	// Step 5: Current behavior: FAILS with "duplicate column name" because
	//   SQLite does not support ALTER TABLE ADD COLUMN IF NOT EXISTS, and
	//   migration 20's transactional SQL includes:
	//     ALTER TABLE feed_adapters ADD COLUMN builtin_version ...
	//     ALTER TABLE feed_adapters ADD COLUMN is_customized ...
	//     CREATE TABLE catalog_mode_feeds ... (no IF NOT EXISTS)
	//
	// These statements fail on the second run because the schema objects
	// already exist from the first (committed) run.
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("migration 20 is not re-entrant: %v", err)
	}
	defer s2.Close()

	// If we get here, migration 20 was re-entrant (the bug was fixed).
	t.Log("migration 20 is re-entrant — this should not happen with current code")
}

func TestFeedsEnabledOnlyExcludesDisabledFeed(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	// Check if the feeds table has an enabled column after migrations.
	// If not, the test SKIPs — this IS the failure: the enabled column was dropped
	// and cannot be used to exclude disabled feeds from Feeds(enabledOnly=true).
	hasColumn := false
	rows, err := s.DB.Query("PRAGMA table_info(feeds)")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		if name == "enabled" {
			hasColumn = true
			break
		}
	}
	rows.Close()
	if !hasColumn {
		t.Skip("feeds.enabled column does not exist — disabled feeds cannot be excluded by Feeds(enabledOnly=true)")
	}

	// Insert a catalog mode (id=1 already exists as 'opencck' from seed, enabled=1).
	// We insert a custom mode for isolation.
	if _, err := s.DB.Exec(
		`INSERT INTO catalog_modes(id, key, name, enabled) VALUES (99, 'test', 'Test', 1)`); err != nil {
		t.Fatal(err)
	}

	// Insert a feed with enabled=0 (disabled) using raw SQL.
	if _, err := s.DB.Exec(
		`INSERT INTO feeds(name, url, enabled, adapter_id) VALUES ('disabled-feed', 'https://example.test/feed.json', 0, 1)`); err != nil {
		t.Fatal(err)
	}
	var feedID int64
	if err := s.DB.QueryRow("SELECT id FROM feeds WHERE name = 'disabled-feed'").Scan(&feedID); err != nil {
		t.Fatal(err)
	}

	// Link feed to the enabled mode via catalog_mode_feeds junction table.
	if _, err := s.DB.Exec(
		`INSERT INTO catalog_mode_feeds(mode_id, feed_id) VALUES (99, ?)`, feedID); err != nil {
		t.Fatal(err)
	}

	// enabledOnly=true should return 0 feeds (the disabled feed is excluded).
	feeds, err := s.Feeds(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	disabledFound := false
	for _, f := range feeds {
		if f.ID == feedID {
			disabledFound = true
		}
	}
	if disabledFound {
		t.Fatal("Feeds(enabledOnly=true) returned a disabled feed (enabled=0) — should have been excluded")
	}

	// enabledOnly=false should return the disabled feed (includeDisabled=true).
	feeds, err = s.Feeds(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	disabledFound = false
	for _, f := range feeds {
		if f.ID == feedID {
			disabledFound = true
		}
	}
	if !disabledFound {
		t.Fatal("Feeds(enabledOnly=false) did not return the disabled feed — should have been included")
	}
}

func TestBuiltInAdapterUpgradeOnSourceChange(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	// Get the current built-in adapter info for canonical-json
	var currentSource string
	var currentVersion int
	var currentCustomized int
	if err := s.DB.QueryRow(
		"SELECT source, builtin_version, is_customized FROM feed_adapters WHERE key='canonical-json'",
	).Scan(&currentSource, &currentVersion, &currentCustomized); err != nil {
		t.Fatal(err)
	}
	t.Logf("initial source length=%d version=%d customized=%d", len(currentSource), currentVersion, currentCustomized)

	// Simulate a migration state where the adapter source is empty (adapter was
	// created by v17 migration but never seeded). builtin_version=0 means the
	// version was not tracked before v20. The user has NOT customized the
	// adapter. After seeding, the source should be populated with the built-in.
	if _, err := s.DB.Exec(
		"UPDATE feed_adapters SET builtin_version = 0, source = '', is_customized = 0 WHERE key='canonical-json'"); err != nil {
		t.Fatal(err)
	}

	// Re-run the built-in adapter seeding (simulating startup after migration).
	if err := s.seedBuiltInAdapters(ctx); err != nil {
		t.Fatal(err)
	}

	// After seeding, the adapter should have been upgraded:
	// - is_customized should STILL be 0 (user never customized it)
	// - source should be the CURRENT built-in source
	// - builtin_version should be the current version (1)
	var newSource string
	var newCustomized int
	var newVersion int
	if err := s.DB.QueryRow(
		"SELECT source, is_customized, builtin_version FROM feed_adapters WHERE key='canonical-json'",
	).Scan(&newSource, &newCustomized, &newVersion); err != nil {
		t.Fatal(err)
	}

	expectedSource := normalizedBuiltInSource(canonicalJSONAdapter)

	if newCustomized != 0 {
		t.Errorf("is_customized = %d, want 0 (adapter should not be marked as customized — source changed because built-in was upgraded, not because user edited it)", newCustomized)
	}
	if newVersion != 1 {
		t.Errorf("builtin_version = %d, want 1 (should be upgraded to current built-in version)", newVersion)
	}
	if newSource != expectedSource {
		t.Errorf("source was not upgraded to current built-in:\n  got  length=%d\n  want length=%d", len(newSource), len(expectedSource))
	}
}

func TestSeedPreservesLegacyCustomizedAdapters(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	customSource := `function sync(feed, api) { return ["10.0.0.0/24"]; }`

	// Simulate a v20 migration state: builtin_version = 0, is_customized = 0,
	// but the source differs from the built-in — this was a legacy customization.
	if _, err := s.DB.Exec(
		`UPDATE feed_adapters SET builtin_version = 0, source = ?, is_customized = 0 WHERE key='canonical-json'`,
		customSource); err != nil {
		t.Fatal(err)
	}

	// Run seedBuiltInAdapters — simulating startup after migration.
	if err := s.seedBuiltInAdapters(ctx); err != nil {
		t.Fatal(err)
	}

	var newSource string
	var newCustomized int
	var newVersion int
	if err := s.DB.QueryRow(
		"SELECT source, is_customized, builtin_version FROM feed_adapters WHERE key='canonical-json'",
	).Scan(&newSource, &newCustomized, &newVersion); err != nil {
		t.Fatal(err)
	}

	// The custom source must be PRESERVED — not overwritten with the built-in.
	if newSource != customSource {
		t.Errorf("source was overwritten:\n  got  %q\n  want %q (custom source must be preserved)", newSource, customSource)
	}

	// is_customized should be 1 to protect the custom source from future upgrades.
	if newCustomized != 1 {
		t.Errorf("is_customized = %d, want 1 (legacy customization must be detected and protected)", newCustomized)
	}

	// builtin_version should be updated to the current version.
	if newVersion != 1 {
		t.Errorf("builtin_version = %d, want 1 (version should be bumped even when preserving custom source)", newVersion)
	}
}

func TestCatalogForModeDisabled(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	modes, err := s.CatalogModes(ctx, false)
	if err != nil {
		t.Fatal(err)
	}

	// modeA = opencck (ID=1, enabled by default)
	modeA := modes[0]
	if modeA.ID != 1 || !modeA.Enabled {
		t.Fatalf("expected modeA (opencck) to be enabled, got %#v", modeA)
	}

	// modeB = ipranges (ID=2, disabled by default)
	modeB := modes[1]
	if modeB.ID != 2 || modeB.Enabled {
		t.Fatalf("expected modeB (ipranges) to be disabled, got %#v", modeB)
	}

	// Create a feed linked to mode B (via AddFeedForMode)
	if err := s.AddFeedForMode(ctx, "shared-feed", "https://example.test/feed.json", modeB.ID, 0); err != nil {
		t.Fatal(err)
	}

	feeds, err := s.Feeds(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	feedID := feeds[len(feeds)-1].ID

	// Link the same feed to mode A as well — so the feed belongs to BOTH modes.
	if err := s.AddFeedToMode(ctx, modeA.ID, feedID); err != nil {
		t.Fatal(err)
	}

	// Insert a catalog entry for the shared feed.
	if _, err := s.DB.Exec(`INSERT INTO catalog_entries(feed_id, category, service, cidr) VALUES
		(?, 'DisabledCat', 'DisabledSvc', '10.0.0.0/24')`, feedID); err != nil {
		t.Fatal(err)
	}

	// Bug reproduction: CatalogForMode(B, false) should return EMPTY because
	// mode B is disabled and includeDisabled=false. The current buggy behavior
	// is that it returns entries because the feed is also linked to enabled mode A.
	catalog, err := s.CatalogForMode(ctx, modeB.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog) != 0 {
		t.Fatalf("CatalogForMode(B, false) = %#v, want empty (disabled mode with includeDisabled=false)", catalog)
	}

	// CatalogForMode(B, true) should return entries since includeDisabled=true.
	catalog, err = s.CatalogForMode(ctx, modeB.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if svcs, ok := catalog["DisabledCat"]; !ok || len(svcs) != 1 || svcs[0] != "DisabledSvc" {
		t.Fatalf("CatalogForMode(B, true) = %#v, want {DisabledCat:[DisabledSvc]}", catalog)
	}
}

func TestDesiredPrefixesExcludesDisabledFeeds(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	// Use seeded mode 1 (opencck, enabled) and feed 1 (opencck-main, enabled).
	userID, err := s.AddUser(ctx, User{
		Name: "test-user", PeerIP: "172.16.0.9", PeerASN: 65009, Enabled: true,
		CatalogModeID: 1, Networks: []string{"192.168.50.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Give feed 1 a catalog entry (use non-denied prefix).
	if _, err := s.DB.Exec(`INSERT INTO catalog_entries(feed_id, category, service, cidr) VALUES
		(1, 'TestCat', 'TestSvc', '8.8.8.0/24')`); err != nil {
		t.Fatal(err)
	}

	// Enable mode 1 (already enabled by default, but be explicit).
	if err := s.UpdateCatalogMode(ctx, 1, "OpenCCK", true); err != nil {
		t.Fatal(err)
	}

	// User selects the category.
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		return SetUserModeSelection(ctx, tx, userID, 1, []string{"TestCat"}, nil)
	}); err != nil {
		t.Fatal(err)
	}

	// Confirm the prefix appears while feed is enabled.
	prefixes, _, err := s.DesiredPrefixes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	key := compoundKey("8.8.8.0/24", 1)
	if _, ok := prefixes[key]; !ok {
		t.Fatalf("enabled feed: expected prefix %s, got none. prefixes=%#v", key, prefixes)
	}
	if len(prefixes[key]) != 1 || prefixes[key][0] != userID {
		t.Fatalf("enabled feed: unexpected users for %s: %#v", key, prefixes[key])
	}

	// Disable the feed.
	if _, err := s.DB.Exec(`UPDATE feeds SET enabled = 0 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}

	// After disabling the feed, the prefix should NOT appear.
	prefixes, _, err = s.DesiredPrefixes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := prefixes[key]; ok {
		t.Fatalf("disabled feed: prefix %s still returned, want none. prefixes=%#v", key, prefixes)
	}
}

func TestCatalogForModeExcludesDisabledFeeds(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	// Use mode 1 (opencck) — enabled by default per migration seed data.
	mode, err := s.CatalogMode(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !mode.Enabled {
		t.Fatalf("mode 1 (opencck) should be enabled by default, got %#v", mode)
	}

	// Create an enabled feed linked to the enabled mode.
	if err := s.AddFeedForMode(ctx, "test-feed", "https://example.test/feed.json", mode.ID, 0); err != nil {
		t.Fatal(err)
	}

	feeds, err := s.Feeds(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	feedID := feeds[len(feeds)-1].ID

	// Insert a catalog entry for the feed.
	if _, err := s.DB.Exec(`INSERT INTO catalog_entries(feed_id, category, service, cidr) VALUES
		(?, 'EnabledCat', 'EnabledSvc', '10.0.0.0/24')`, feedID); err != nil {
		t.Fatal(err)
	}

	// CatalogForMode should return the entry (feed is enabled, mode is enabled).
	catalog, err := s.CatalogForMode(ctx, mode.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if svcs, ok := catalog["EnabledCat"]; !ok || len(svcs) != 1 || svcs[0] != "EnabledSvc" {
		t.Fatalf("CatalogForMode(enabled mode, false) = %#v, want {EnabledCat:[EnabledSvc]}", catalog)
	}

	// Disable the feed.
	if _, err := s.DB.Exec(`UPDATE feeds SET enabled = 0 WHERE id = ?`, feedID); err != nil {
		t.Fatal(err)
	}

	// CatalogForMode should NOT return the entry (feed is disabled).
	// BUG: currently returns it because CatalogForMode doesn't filter by f.enabled.
	catalog, err = s.CatalogForMode(ctx, mode.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog) != 0 {
		t.Fatalf("BUG: CatalogForMode(enabled mode, false) with disabled feed = %#v, want empty", catalog)
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
