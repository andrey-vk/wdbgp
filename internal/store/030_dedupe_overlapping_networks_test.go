//nolint:errcheck // test file, errors in cleanup intentionally ignored
package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	m "github.com/andrey-vk/wdbgp/internal/store/migrations"
)

// Migration 30 ran against the text-era schema (user_networks.cidr TEXT,
// users.web_auth TEXT) — migration 33 later converted both to binary/enum
// form, so these tests replay migrations to just before 30 and build their
// fixtures in that era's shape instead of using a live (current-schema)
// store.

// legacyM30DB returns a DB migrated to just before version 30.
func legacyM30DB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "m30-legacy.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	replayMigrationsBefore(t, db, 30)
	return db
}

// legacyAddUser inserts a user directly in the pre-30 schema. It bypasses
// the store on purpose: the store now speaks the post-33 schema.
func legacyAddUser(t *testing.T, db *sql.DB, name, peerIP string, peerASN int, enabled bool, webAuth string) int64 {
	t.Helper()
	result, err := db.Exec(`
INSERT INTO users(name, peer_ip, peer_asn, enabled, web_auth, catalog_mode_id)
VALUES (?, ?, ?, ?, ?, 1)`, name, peerIP, peerASN, enabled, webAuth)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// legacyAddNetwork bypasses replaceNetworks (which already enforces
// normalization/overlap going forward) to simulate pre-existing,
// inconsistent data as it could exist before this migration — exactly what
// V030 is meant to clean up.
func legacyAddNetwork(t *testing.T, db *sql.DB, userID int64, cidr string) {
	t.Helper()
	if _, err := db.Exec("INSERT INTO user_networks(user_id, cidr) VALUES (?, ?)", userID, cidr); err != nil {
		t.Fatal(err)
	}
}

func runMigration30(t *testing.T, db *sql.DB) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := m.V030(context.Background(), tx); err != nil {
		tx.Rollback() //nolint:errcheck,gosec // test cleanup
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func legacyNetworksFor(t *testing.T, db *sql.DB, userID int64) []string {
	t.Helper()
	rows, err := db.Query("SELECT cidr FROM user_networks WHERE user_id = ? ORDER BY cidr", userID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var networks []string
	for rows.Next() {
		var cidr string
		if err := rows.Scan(&cidr); err != nil {
			t.Fatal(err)
		}
		networks = append(networks, cidr)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return networks
}

func TestMigration30KeepsMoreSpecificPrefixOnOverlap(t *testing.T) {
	db := legacyM30DB(t)

	userA := legacyAddUser(t, db, "userA", "10.1.0.1", 65001, true, "network")
	userB := legacyAddUser(t, db, "userB", "10.1.0.2", 65002, true, "network")

	// A has the broader /24, B has a more specific /26 fully inside it.
	legacyAddNetwork(t, db, userA, "10.0.0.0/24")
	legacyAddNetwork(t, db, userB, "10.0.0.128/26")

	runMigration30(t, db)

	if got := legacyNetworksFor(t, db, userA); len(got) != 0 {
		t.Errorf("userA networks = %v, want empty (less specific, should be deleted)", got)
	}
	if got := legacyNetworksFor(t, db, userB); len(got) != 1 || got[0] != "10.0.0.128/26" {
		t.Errorf("userB networks = %v, want [10.0.0.128/26] (more specific, should survive)", got)
	}
}

func TestMigration30KeepsLowerUserIDOnExactDuplicateTie(t *testing.T) {
	db := legacyM30DB(t)

	userA := legacyAddUser(t, db, "userA", "10.1.0.1", 65001, true, "network")
	userB := legacyAddUser(t, db, "userB", "10.1.0.2", 65002, true, "network")

	// Different raw strings, same network once masked — the DB's raw-string
	// UNIQUE constraint on cidr already blocks byte-identical duplicates,
	// so this (pre-normalization-fix-era) shape is the realistic "tie".
	legacyAddNetwork(t, db, userB, "10.0.0.5/24")
	legacyAddNetwork(t, db, userA, "10.0.0.9/24")

	runMigration30(t, db)

	if got := legacyNetworksFor(t, db, userA); len(got) != 1 {
		t.Errorf("userA (lower id) networks = %v, want 1 entry", got)
	}
	if got := legacyNetworksFor(t, db, userB); len(got) != 0 {
		t.Errorf("userB (higher id) networks = %v, want empty", got)
	}
}

func TestMigration30IgnoresLoginModeNetworks(t *testing.T) {
	db := legacyM30DB(t)

	userA := legacyAddUser(t, db, "userA", "10.1.0.1", 65001, true, "network")
	userB := legacyAddUser(t, db, "userB", "10.1.0.2", 65002, true, "login")

	// userB's network would clearly conflict (fully contained within userA's)
	// if it were considered — but userB is login-mode, so it's stale and
	// must be left untouched entirely, and userA's network must survive too
	// since there's no *active* conflict.
	legacyAddNetwork(t, db, userA, "10.0.0.0/24")
	legacyAddNetwork(t, db, userB, "10.0.0.128/25")

	runMigration30(t, db)

	if got := legacyNetworksFor(t, db, userA); len(got) != 1 {
		t.Errorf("userA (active) networks = %v, want [10.0.0.0/24] unchanged", got)
	}
	if got := legacyNetworksFor(t, db, userB); len(got) != 1 {
		t.Errorf("userB (login/stale) networks = %v, want unchanged (not considered)", got)
	}
}

func TestMigration30LeavesNonOverlappingNetworksAlone(t *testing.T) {
	db := legacyM30DB(t)

	userA := legacyAddUser(t, db, "userA", "10.1.0.1", 65001, true, "network")
	userB := legacyAddUser(t, db, "userB", "10.1.0.2", 65002, true, "network")

	legacyAddNetwork(t, db, userA, "10.0.0.0/24")
	legacyAddNetwork(t, db, userB, "10.0.2.0/24")

	runMigration30(t, db)

	if got := legacyNetworksFor(t, db, userA); len(got) != 1 || got[0] != "10.0.0.0/24" {
		t.Errorf("userA networks = %v, want [10.0.0.0/24]", got)
	}
	if got := legacyNetworksFor(t, db, userB); len(got) != 1 || got[0] != "10.0.2.0/24" {
		t.Errorf("userB networks = %v, want [10.0.2.0/24]", got)
	}
}

// TestMigration30ExcludesDisabledUsersFromOverlapResolution guards against a
// bug where a disabled user's narrower network could "win" the overlap
// comparison against an enabled user's broader network, deleting the
// enabled user's entire entry — even though a disabled user's IP match is
// always discarded by requireUser (ipMatch := ipErr == nil &&
// ipUser.Enabled), so their network could never actually have caused real
// auth ambiguity in the first place. The enabled user's broader network
// must survive untouched, and the disabled user's network must be left
// alone too (it's simply excluded from the comparison, not treated as a
// loser).
func TestMigration30ExcludesDisabledUsersFromOverlapResolution(t *testing.T) {
	db := legacyM30DB(t)

	enabledUser := legacyAddUser(t, db, "enabled-user", "10.1.0.1", 65001, true, "network")
	disabledUser := legacyAddUser(t, db, "disabled-user", "10.1.0.2", 65002, false, "network")

	// disabled-user has a more specific /26 fully inside enabled-user's
	// broader /24 — without the fix, the more-specific-wins rule would
	// delete enabled-user's /24 entirely.
	legacyAddNetwork(t, db, enabledUser, "10.0.0.0/24")
	legacyAddNetwork(t, db, disabledUser, "10.0.0.128/26")

	runMigration30(t, db)

	if got := legacyNetworksFor(t, db, enabledUser); len(got) != 1 || got[0] != "10.0.0.0/24" {
		t.Errorf("enabled-user networks = %v, want unchanged [10.0.0.0/24] — a disabled user's network must never cause an enabled user's network to be deleted", got)
	}
	if got := legacyNetworksFor(t, db, disabledUser); len(got) != 1 || got[0] != "10.0.0.128/26" {
		t.Errorf("disabled-user networks = %v, want unchanged [10.0.0.128/26] — disabled users are excluded from the comparison, not treated as losers", got)
	}
}
