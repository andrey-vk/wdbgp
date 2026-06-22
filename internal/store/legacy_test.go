//nolint:errcheck // test file, errors in cleanup intentionally ignored
package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/andrey-vk/wdbgp/internal/config"

	_ "modernc.org/sqlite"
)

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

	s, err := Open(path, config.Config{})
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
