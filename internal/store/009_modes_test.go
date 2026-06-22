package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/andrey-vk/wdbgp/internal/config"

	_ "modernc.org/sqlite"
)

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
		tx, err := db.Begin()
		if err != nil {
			t.Fatalf("begin tx for migration %d: %v", migration.Version, err)
		}
		if err := migration.Func(context.Background(), tx); err != nil {
			tx.Rollback()
			t.Fatalf("apply migration %d: %v", migration.Version, err)
		}
		if _, err := tx.Exec(
			"INSERT INTO schema_migrations(version, name, applied_at) VALUES (?, ?, 'now')",
			migration.Version, migration.Name); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
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

	s, err := Open(path, config.Config{})
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
		tx, err := db.Begin()
		if err != nil {
			t.Fatalf("begin tx for migration %d: %v", migration.Version, err)
		}
		if err := migration.Func(context.Background(), tx); err != nil {
			tx.Rollback()
			t.Fatalf("apply migration %d: %v", migration.Version, err)
		}
		if _, err := tx.Exec(
			"INSERT INTO schema_migrations(version, name, applied_at) VALUES (?, ?, 'now')",
			migration.Version, migration.Name); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
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

	s, err := Open(path, config.Config{})
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
