package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	m "github.com/andrey-vk/wdbgp/internal/store/migrations"
)

// insertRawNetwork bypasses SetUserRouteFilters/replaceNetworks (which
// already enforce normalization/overlap going forward) to simulate
// pre-existing, inconsistent data as it could exist before this migration —
// exactly what V030 is meant to clean up.
func insertRawNetwork(t *testing.T, db *sql.DB, userID int64, cidr string) {
	t.Helper()
	if _, err := db.Exec("INSERT INTO user_networks(user_id, cidr) VALUES (?, ?)", userID, cidr); err != nil {
		t.Fatal(err)
	}
}

func runMigration30(t *testing.T, s *Store) {
	t.Helper()
	if err := s.Transaction(context.Background(), func(tx *sql.Tx) error {
		return m.V030(context.Background(), tx)
	}); err != nil {
		t.Fatal(err)
	}
}

func networksFor(t *testing.T, s *Store, userID int64) []string {
	t.Helper()
	got, err := s.UserNetworks(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestMigration30KeepsMoreSpecificPrefixOnOverlap(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "m30.sqlite3"), false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck,gosec // test cleanup

	userA, err := s.AddUser(ctx, User{Name: "userA", PeerIP: "10.1.0.1", PeerASN: 65001, Enabled: true, WebAuth: "network"})
	if err != nil {
		t.Fatal(err)
	}
	userB, err := s.AddUser(ctx, User{Name: "userB", PeerIP: "10.1.0.2", PeerASN: 65002, Enabled: true, WebAuth: "network"})
	if err != nil {
		t.Fatal(err)
	}

	// A has the broader /24, B has a more specific /26 fully inside it.
	insertRawNetwork(t, s.DB, userA, "10.0.0.0/24")
	insertRawNetwork(t, s.DB, userB, "10.0.0.128/26")

	runMigration30(t, s)

	if got := networksFor(t, s, userA); len(got) != 0 {
		t.Errorf("userA networks = %v, want empty (less specific, should be deleted)", got)
	}
	if got := networksFor(t, s, userB); len(got) != 1 || got[0] != "10.0.0.128/26" {
		t.Errorf("userB networks = %v, want [10.0.0.128/26] (more specific, should survive)", got)
	}
}

func TestMigration30KeepsLowerUserIDOnExactDuplicateTie(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "m30-tie.sqlite3"), false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck,gosec // test cleanup

	userA, err := s.AddUser(ctx, User{Name: "userA", PeerIP: "10.1.0.1", PeerASN: 65001, Enabled: true, WebAuth: "network"})
	if err != nil {
		t.Fatal(err)
	}
	userB, err := s.AddUser(ctx, User{Name: "userB", PeerIP: "10.1.0.2", PeerASN: 65002, Enabled: true, WebAuth: "network"})
	if err != nil {
		t.Fatal(err)
	}

	// Different raw strings, same network once masked — the DB's raw-string
	// UNIQUE constraint on cidr already blocks byte-identical duplicates,
	// so this (pre-normalization-fix-era) shape is the realistic "tie".
	insertRawNetwork(t, s.DB, userB, "10.0.0.5/24")
	insertRawNetwork(t, s.DB, userA, "10.0.0.9/24")

	runMigration30(t, s)

	if got := networksFor(t, s, userA); len(got) != 1 {
		t.Errorf("userA (lower id) networks = %v, want 1 entry", got)
	}
	if got := networksFor(t, s, userB); len(got) != 0 {
		t.Errorf("userB (higher id) networks = %v, want empty", got)
	}
}

func TestMigration30IgnoresLoginModeNetworks(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "m30-login.sqlite3"), false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck,gosec // test cleanup

	userA, err := s.AddUser(ctx, User{Name: "userA", PeerIP: "10.1.0.1", PeerASN: 65001, Enabled: true, WebAuth: "network"})
	if err != nil {
		t.Fatal(err)
	}
	userB, err := s.AddUser(ctx, User{Name: "userB", PeerIP: "10.1.0.2", PeerASN: 65002, Enabled: true, WebAuth: "login"})
	if err != nil {
		t.Fatal(err)
	}

	// userB's network would clearly conflict (fully contained within userA's)
	// if it were considered — but userB is login-mode, so it's stale and
	// must be left untouched entirely, and userA's network must survive too
	// since there's no *active* conflict.
	insertRawNetwork(t, s.DB, userA, "10.0.0.0/24")
	insertRawNetwork(t, s.DB, userB, "10.0.0.128/25")

	runMigration30(t, s)

	if got := networksFor(t, s, userA); len(got) != 1 {
		t.Errorf("userA (active) networks = %v, want [10.0.0.0/24] unchanged", got)
	}
	if got := networksFor(t, s, userB); len(got) != 1 {
		t.Errorf("userB (login/stale) networks = %v, want unchanged (not considered)", got)
	}
}

func TestMigration30LeavesNonOverlappingNetworksAlone(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "m30-noconflict.sqlite3"), false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck,gosec // test cleanup

	userA, err := s.AddUser(ctx, User{Name: "userA", PeerIP: "10.1.0.1", PeerASN: 65001, Enabled: true, WebAuth: "network"})
	if err != nil {
		t.Fatal(err)
	}
	userB, err := s.AddUser(ctx, User{Name: "userB", PeerIP: "10.1.0.2", PeerASN: 65002, Enabled: true, WebAuth: "network"})
	if err != nil {
		t.Fatal(err)
	}

	insertRawNetwork(t, s.DB, userA, "10.0.0.0/24")
	insertRawNetwork(t, s.DB, userB, "10.0.2.0/24")

	runMigration30(t, s)

	if got := networksFor(t, s, userA); len(got) != 1 || got[0] != "10.0.0.0/24" {
		t.Errorf("userA networks = %v, want [10.0.0.0/24]", got)
	}
	if got := networksFor(t, s, userB); len(got) != 1 || got[0] != "10.0.2.0/24" {
		t.Errorf("userB networks = %v, want [10.0.2.0/24]", got)
	}
}
