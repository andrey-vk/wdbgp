package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/andrey-vk/wdbgp/internal/config"

	_ "modernc.org/sqlite"
)

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

	s, err := Open(path, config.Config{})
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
