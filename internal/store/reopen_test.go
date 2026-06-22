package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/andrey-vk/wdbgp/internal/config"

	_ "modernc.org/sqlite"
)

// TestMigrationReopenPreservesData verifies that re-opening a fully migrated
// database preserves all data.
func TestMigrationReopenPreservesData(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "reopen.sqlite3")
	s, err := Open(dbPath, config.Config{})
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
	if _, err := s.AddFeedForModeAdapter(
		ctx, "persist-feed", "https://example.test/persist", 1, 1, true, 0, ""); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// Reopen the same database
	s2, err := Open(dbPath, config.Config{})
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
