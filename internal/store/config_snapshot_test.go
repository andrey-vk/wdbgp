package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// applyConfigSnapshot computes the live-state-bound digest ApplyConfigSnapshot
// now requires (ConfigImportDigest) and applies snap, for tests that don't
// care about the digest mechanism itself, only about what applying does.
func applyConfigSnapshot(t *testing.T, s *Store, snap ConfigSnapshot, meta AuditMeta) (ConfigApplyResult, error) {
	t.Helper()
	current, err := s.ConfigSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	digest, err := ConfigImportDigest(current, snap)
	if err != nil {
		t.Fatal(err)
	}
	return s.ApplyConfigSnapshot(context.Background(), snap, digest, meta)
}

func configSnapshotTestUser(t *testing.T, s *Store, name, peerIP string, asn uint32, modeID int64) int64 {
	t.Helper()
	id, err := s.AddUser(context.Background(), User{
		Name: name, PeerIP: peerIP, PeerASN: asn, Enabled: true,
		FilterMode: FilterModeGlobal, CatalogModeID: modeID, Networks: []string{"192.168.100.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestConfigSnapshotRoundTripsUsersModesAndFilters(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	if err := s.SaveSettings(ctx, map[string]string{"filter_allow": "10.0.0.0/8", "filter_deny": "192.168.0.0/16"}); err != nil {
		t.Fatal(err)
	}
	modeID, err := s.AddCatalogMode(ctx, "Lab", true)
	if err != nil {
		t.Fatal(err)
	}
	feedID, err := s.AddFeed(ctx, "snapshot-feed", "https://example.test/snapshot.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, "INSERT INTO catalog_mode_feeds(mode_id, feed_id, exclude) VALUES (?, ?, 1)", modeID, feedID); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCommunity(ctx, modeID, "ai", "", 10000); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCommunity(ctx, modeID, "ai", "openai", 10001); err != nil {
		t.Fatal(err)
	}

	userID := configSnapshotTestUser(t, s, "alice", "20.0.0.1", 65001, modeID)
	if _, _, err := s.SetUserRouteFilters(ctx, userID, RouteFilters{Allow: []string{"30.0.0.0/8"}}, AuditMeta{}); err != nil {
		t.Fatal(err)
	}
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		return SetUserModeSelection(ctx, tx, userID, modeID, []string{"ai"}, nil)
	}); err != nil {
		t.Fatal(err)
	}

	snap, err := s.ConfigSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snap.SchemaVersion != ConfigSnapshotSchemaVersion {
		t.Fatalf("schema version = %d, want %d", snap.SchemaVersion, ConfigSnapshotSchemaVersion)
	}
	if len(snap.GlobalFilters.Allow) != 1 || snap.GlobalFilters.Allow[0] != "10.0.0.0/8" {
		t.Fatalf("global filters = %+v, want allow 10.0.0.0/8", snap.GlobalFilters)
	}

	var lab *ConfigMode
	for i := range snap.Modes {
		if snap.Modes[i].Name == "Lab" {
			lab = &snap.Modes[i]
		}
	}
	if lab == nil {
		t.Fatal("Lab mode not in snapshot")
	}
	if len(lab.Feeds) != 1 || lab.Feeds[0].Feed != "snapshot-feed" || !lab.Feeds[0].Exclude {
		t.Fatalf("Lab feeds = %+v, want snapshot-feed excluded", lab.Feeds)
	}
	if len(lab.Communities) != 2 {
		t.Fatalf("Lab communities = %+v, want 2 entries", lab.Communities)
	}

	var alice *ConfigUser
	for i := range snap.Users {
		if snap.Users[i].Name == "alice" {
			alice = &snap.Users[i]
		}
	}
	if alice == nil {
		t.Fatal("alice not in snapshot")
	}
	if alice.CatalogMode != "Lab" {
		t.Fatalf("alice.CatalogMode = %q, want Lab", alice.CatalogMode)
	}
	if alice.HasBGPPassword {
		t.Fatal("alice has no password set, but HasBGPPassword is true")
	}
	if len(alice.RouteFilters.Allow) != 1 || alice.RouteFilters.Allow[0] != "30.0.0.0/8" {
		t.Fatalf("alice route filters = %+v, want allow 30.0.0.0/8", alice.RouteFilters)
	}
	if len(alice.SelectedCategories) != 1 || alice.SelectedCategories[0] != "ai" {
		t.Fatalf("alice selected categories = %+v, want [ai]", alice.SelectedCategories)
	}

	// A password is never exported, but its presence is: HasBGPPassword should
	// flip once one is set.
	if _, err := s.DB.ExecContext(ctx, "UPDATE users SET bgp_password = 'secret' WHERE id = ?", userID); err != nil {
		t.Fatal(err)
	}
	snap2, err := s.ConfigSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, u := range snap2.Users {
		if u.Name != "alice" {
			continue
		}
		found = true
		if !u.HasBGPPassword {
			t.Fatal("alice now has a password, but HasBGPPassword is false")
		}
		raw, err := json.Marshal(u)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "secret") {
			t.Fatalf("the password leaked into the exported user: %s", raw)
		}
	}
	if !found {
		t.Fatal("alice missing from the second snapshot")
	}
}

func TestDiffConfigSnapshotsDetectsAddedChangedRemoved(t *testing.T) {
	before := ConfigSnapshot{
		Modes: []ConfigMode{{Name: "Default", Enabled: true}},
		Users: []ConfigUser{
			{Name: "alice", CatalogMode: "OpenCCK", Enabled: true},
			{Name: "gone", CatalogMode: "OpenCCK", Enabled: true},
		},
	}
	after := ConfigSnapshot{
		Modes: []ConfigMode{{Name: "Default", Enabled: false}, {Name: "New", Enabled: true}},
		Users: []ConfigUser{
			{Name: "alice", CatalogMode: "OpenCCK", Enabled: false},
			{Name: "fresh", CatalogMode: "OpenCCK", Enabled: true},
		},
	}
	diff := DiffConfigSnapshots(before, after)
	if len(diff.Modes.Added) != 1 || diff.Modes.Added[0].Name != "New" {
		t.Fatalf("added modes = %+v, want [New]", diff.Modes.Added)
	}
	if len(diff.Modes.Changed) != 1 || diff.Modes.Changed[0].Name != "Default" {
		t.Fatalf("changed modes = %+v, want [Default]", diff.Modes.Changed)
	}
	if len(diff.Users.Added) != 1 || diff.Users.Added[0].Name != "fresh" {
		t.Fatalf("added users = %+v, want [fresh]", diff.Users.Added)
	}
	if len(diff.Users.Changed) != 1 || diff.Users.Changed[0].Name != "alice" {
		t.Fatalf("changed users = %+v, want [alice]", diff.Users.Changed)
	}
	if len(diff.Users.Removed) != 1 || diff.Users.Removed[0].Name != "gone" {
		t.Fatalf("removed users = %+v, want [gone] (informational only, never deleted)", diff.Users.Removed)
	}
}

func TestDiffConfigSnapshotsIgnoresNilVersusEmptySlices(t *testing.T) {
	before := ConfigSnapshot{Users: []ConfigUser{{Name: "alice", CatalogMode: "OpenCCK", Networks: nil}}}
	after := ConfigSnapshot{Users: []ConfigUser{{Name: "alice", CatalogMode: "OpenCCK", Networks: []string{}}}}
	diff := DiffConfigSnapshots(before, after)
	if len(diff.Users.Changed) != 0 {
		t.Fatalf("changed = %+v, want none: nil and [] networks are the same configuration", diff.Users.Changed)
	}
}

func TestConfigSnapshotDigestIgnoresOrderAndNilVersusEmpty(t *testing.T) {
	a := ConfigSnapshot{Users: []ConfigUser{
		{Name: "bob", CatalogMode: "OpenCCK", Networks: []string{"10.0.0.0/8", "20.0.0.0/8"}},
		{Name: "alice", CatalogMode: "OpenCCK", Networks: nil},
	}}
	b := ConfigSnapshot{Users: []ConfigUser{
		{Name: "alice", CatalogMode: "OpenCCK", Networks: []string{}},
		{Name: "bob", CatalogMode: "OpenCCK", Networks: []string{"20.0.0.0/8", "10.0.0.0/8"}},
	}}
	da, err := a.Digest()
	if err != nil {
		t.Fatal(err)
	}
	db, err := b.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if da != db {
		t.Fatalf("digests differ (%s vs %s) for snapshots that normalize identically", da, db)
	}

	c := b
	c.Users = append([]ConfigUser{}, b.Users...)
	c.Users[0].Enabled = true
	dc, err := c.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if dc == da {
		t.Fatal("digest did not change for a genuinely different snapshot")
	}
}

func TestConfigSnapshotDigestIgnoresGeneratedAt(t *testing.T) {
	a := ConfigSnapshot{GeneratedAt: 1000, Users: []ConfigUser{{Name: "alice", CatalogMode: "OpenCCK"}}}
	b := ConfigSnapshot{GeneratedAt: 2000, Users: []ConfigUser{{Name: "alice", CatalogMode: "OpenCCK"}}}
	da, err := a.Digest()
	if err != nil {
		t.Fatal(err)
	}
	db, err := b.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if da != db {
		t.Fatal("digest changed with GeneratedAt alone: two reads of the same unchanged configuration a moment apart would spuriously reject a real, unmodified apply")
	}
}

func TestApplyConfigSnapshotIsAdditiveOnly(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	liveOnlyID, err := s.AddUser(ctx, User{
		Name: "live-only", PeerIP: "20.0.0.9", PeerASN: 65099, Enabled: true,
		FilterMode: FilterModeGlobal, CatalogModeID: DefaultCatalogModeID,
	})
	if err != nil {
		t.Fatal(err)
	}
	bobID := configSnapshotTestUser(t, s, "bob", "20.0.0.2", 65002, DefaultCatalogModeID)
	if _, err := s.DB.ExecContext(ctx, "UPDATE users SET bgp_password = 'keep-me' WHERE id = ?", bobID); err != nil {
		t.Fatal(err)
	}

	snap := ConfigSnapshot{
		Modes: []ConfigMode{
			{Name: "Imported", Enabled: true, Feeds: []ConfigModeFeedLink{{Feed: "no-such-feed"}}},
		},
		Users: []ConfigUser{
			{Name: "bob", PeerIP: "20.0.0.2", PeerASN: 65002, CatalogMode: "OpenCCK", Enabled: true, SelectedCategories: []string{"ai"}},
			{Name: "carol", CatalogMode: "no-such-mode", Enabled: true},
		},
	}
	result, err := applyConfigSnapshot(t, s, snap, AuditMeta{Actor: "admin:203.0.113.7", Action: "config.imported"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ModesCreated) != 1 || result.ModesCreated[0] != "Imported" {
		t.Fatalf("modes created = %+v, want [Imported]", result.ModesCreated)
	}
	if len(result.UnknownFeeds) != 1 {
		t.Fatalf("unknown feeds = %+v, want one entry", result.UnknownFeeds)
	}
	if len(result.UsersUpdated) != 1 || result.UsersUpdated[0] != "bob" {
		t.Fatalf("users updated = %+v, want [bob]", result.UsersUpdated)
	}
	if len(result.UnknownModes) != 1 {
		t.Fatalf("unknown modes = %+v, want one entry for carol", result.UnknownModes)
	}
	if len(result.AffectedUserIDs) != 1 || result.AffectedUserIDs[0] != bobID {
		t.Fatalf("affected user ids = %+v, want [%d] (bob; carol was skipped)", result.AffectedUserIDs, bobID)
	}

	// carol was skipped (unknown mode), not created half-finished.
	if _, err := s.DB.QueryContext(ctx, "SELECT 1 FROM users WHERE name = 'carol'"); err != nil {
		t.Fatal(err)
	}
	var carolCount int
	if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE name = 'carol'").Scan(&carolCount); err != nil {
		t.Fatal(err)
	}
	if carolCount != 0 {
		t.Fatal("carol was created despite referencing an unknown mode")
	}

	// live-only, which was never in the snapshot, must still exist: additive,
	// never deletes an entity the snapshot doesn't name.
	if _, err := s.User(ctx, liveOnlyID); err != nil {
		t.Fatalf("live-only user was removed by an additive-only apply: %v", err)
	}

	// bob's password predates the import and isn't in any snapshot: it must
	// survive the update untouched.
	bob, err := s.User(ctx, bobID)
	if err != nil {
		t.Fatal(err)
	}
	if bob.BGPPassword != "keep-me" {
		t.Fatalf("bob.BGPPassword = %q, want it preserved across the import", bob.BGPPassword)
	}

	// Applying again with the same data is idempotent: bob becomes "updated"
	// again, not newly created, and Imported becomes "updated" too.
	result2, err := applyConfigSnapshot(t, s, snap, AuditMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result2.ModesCreated) != 0 || len(result2.ModesUpdated) != 1 {
		t.Fatalf("second apply: modes created=%v updated=%v, want none created and Imported updated", result2.ModesCreated, result2.ModesUpdated)
	}
}

func TestApplyConfigSnapshotRejectsOverlappingActiveNetworks(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	if _, err := s.AddUser(ctx, User{
		Name: "existing", PeerIP: "20.0.0.9", PeerASN: 65099, Enabled: true, WebAuth: "network",
		FilterMode: FilterModeGlobal, CatalogModeID: DefaultCatalogModeID, Networks: []string{"203.0.113.0/24"},
	}); err != nil {
		t.Fatal(err)
	}

	snap := ConfigSnapshot{
		Users: []ConfigUser{
			{
				Name: "newcomer", PeerIP: "20.0.0.10", PeerASN: 65100, CatalogMode: "OpenCCK", Enabled: true,
				WebAuth: "network", Networks: []string{"203.0.113.128/25"}, // overlaps "existing"'s /24
			},
		},
	}
	if _, err := applyConfigSnapshot(t, s, snap, AuditMeta{}); err == nil {
		t.Fatal("expected an error for a network overlapping an existing active user, got nil")
	}

	// Rejected entirely, not partially applied: newcomer must not exist.
	var count int
	if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE name = 'newcomer'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("newcomer was created despite an active-network overlap with an existing user")
	}
}

func TestApplyConfigSnapshotAllowsOverlapForNonActiveWebAuth(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	if _, err := s.AddUser(ctx, User{
		Name: "existing", PeerIP: "20.0.0.9", PeerASN: 65099, Enabled: true, WebAuth: "network",
		FilterMode: FilterModeGlobal, CatalogModeID: DefaultCatalogModeID, Networks: []string{"203.0.113.0/24"},
	}); err != nil {
		t.Fatal(err)
	}

	snap := ConfigSnapshot{
		Users: []ConfigUser{
			{
				Name: "loginuser", PeerIP: "20.0.0.11", PeerASN: 65101, CatalogMode: "OpenCCK", Enabled: true,
				WebAuth: "login", Networks: []string{"203.0.113.128/25"},
			},
		},
	}
	if _, err := applyConfigSnapshot(t, s, snap, AuditMeta{}); err != nil {
		t.Fatalf("a login-mode user's networks aren't IP-resolved, so an overlap must not block the import: %v", err)
	}
}

func TestConfigImportDigestBindsCurrentState(t *testing.T) {
	uploaded := ConfigSnapshot{Users: []ConfigUser{{Name: "alice", CatalogMode: "OpenCCK"}}}
	currentA := ConfigSnapshot{GlobalFilters: RouteFilters{Allow: []string{"10.0.0.0/8"}}}
	currentB := ConfigSnapshot{GlobalFilters: RouteFilters{Allow: []string{"10.1.0.0/16"}}}

	digestA, err := ConfigImportDigest(currentA, uploaded)
	if err != nil {
		t.Fatal(err)
	}
	digestB, err := ConfigImportDigest(currentB, uploaded)
	if err != nil {
		t.Fatal(err)
	}
	if digestA == digestB {
		t.Fatal("digest did not change when the current (live) state differed, with the same uploaded snapshot")
	}

	digestARepeat, err := ConfigImportDigest(currentA, uploaded)
	if err != nil {
		t.Fatal(err)
	}
	if digestA != digestARepeat {
		t.Fatal("digest is not stable for the same (current, uploaded) pair")
	}
}

func TestApplyConfigSnapshotRejectsStaleLiveTarget(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	snap := ConfigSnapshot{Users: []ConfigUser{
		{Name: "carol", PeerIP: "20.0.0.3", PeerASN: 65003, CatalogMode: "OpenCCK", Enabled: true},
	}}
	current, err := s.ConfigSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := ConfigImportDigest(current, snap)
	if err != nil {
		t.Fatal(err)
	}

	// The live configuration changes after the digest was computed — another
	// admin's edit, landing before this apply actually runs.
	if _, err := s.AddUser(ctx, User{
		Name: "dave", PeerIP: "20.0.0.4", PeerASN: 65004, Enabled: true,
		FilterMode: FilterModeGlobal, CatalogModeID: DefaultCatalogModeID,
	}); err != nil {
		t.Fatal(err)
	}

	_, err = s.ApplyConfigSnapshot(ctx, snap, digest, AuditMeta{})
	if !errors.Is(err, ErrConfigImportStale) {
		t.Fatalf("err = %v, want ErrConfigImportStale", err)
	}

	var carolCount int
	if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE name = 'carol'").Scan(&carolCount); err != nil {
		t.Fatal(err)
	}
	if carolCount != 0 {
		t.Fatal("carol was created despite the rejected, drifted apply")
	}
}

func TestApplyConfigSnapshotSwapsCommunityNumbersWithoutTransientCollision(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	modeID, err := s.AddCatalogMode(ctx, "Lab", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetCommunity(ctx, modeID, "ai", "", 100); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCommunity(ctx, modeID, "tv", "", 200); err != nil {
		t.Fatal(err)
	}

	// Swapped relative to the current assignment above: applying these in
	// snapshot order (ai first) would transiently try to set ai=200 while tv
	// still holds 200, which setCommunityTx would reject by write order alone
	// if each entry weren't pre-cleared first.
	snap := ConfigSnapshot{Modes: []ConfigMode{
		{Name: "Lab", Enabled: true, Communities: []ConfigCommunity{
			{Category: "ai", Community: 200},
			{Category: "tv", Community: 100},
		}},
	}}
	if _, err := applyConfigSnapshot(t, s, snap, AuditMeta{}); err != nil {
		t.Fatalf("a valid community swap was rejected: %v", err)
	}

	rows, err := s.CommunityRows(ctx, modeID)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]uint32{}
	for _, r := range rows {
		got[r.Category] = r.Community
	}
	if got["ai"] != 200 || got["tv"] != 100 {
		t.Fatalf("communities after swap = %+v, want ai=200 tv=100", got)
	}
}

func TestApplyConfigSnapshotValidatesNetworkOverlapAgainstFinalState(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	aliceID, err := s.AddUser(ctx, User{
		Name: "alice", PeerIP: "20.0.0.1", PeerASN: 65001, Enabled: true, WebAuth: "network",
		FilterMode: FilterModeGlobal, CatalogModeID: DefaultCatalogModeID, Networks: []string{"203.0.113.0/25"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = aliceID

	// The import moves alice off 203.0.113.0/25 and onto a new CIDR, while
	// handing 203.0.113.0/25 to a brand-new user in the very same import. A
	// per-user, in-order check (alice processed first, still holding her old
	// network when newbob's check ran) would reject this; the final combined
	// state has no overlap at all.
	snap := ConfigSnapshot{Users: []ConfigUser{
		{Name: "alice", PeerIP: "20.0.0.1", PeerASN: 65001, CatalogMode: "OpenCCK", Enabled: true,
			WebAuth: "network", Networks: []string{"203.0.113.128/25"}},
		{Name: "newbob", PeerIP: "20.0.0.5", PeerASN: 65005, CatalogMode: "OpenCCK", Enabled: true,
			WebAuth: "network", Networks: []string{"203.0.113.0/25"}},
	}}
	if _, err := applyConfigSnapshot(t, s, snap, AuditMeta{}); err != nil {
		t.Fatalf("a valid final-state network reassignment was rejected: %v", err)
	}
}

func TestApplyConfigSnapshotChecksOverlapAgainstStoredWebAuthNotRawInput(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	if _, err := s.AddUser(ctx, User{
		Name: "existing", PeerIP: "20.0.0.9", PeerASN: 65099, Enabled: true, WebAuth: "network",
		FilterMode: FilterModeGlobal, CatalogModeID: DefaultCatalogModeID, Networks: []string{"203.0.113.0/24"},
	}); err != nil {
		t.Fatal(err)
	}

	// An unrecognized web_auth string isn't "login" or any other inactive
	// mode — it's garbage. webAuthToInt's own fallback stores it as
	// "network" (the active, IP-resolved mode) regardless, so the overlap
	// check must follow what actually gets stored, not the input string
	// (which isActiveWebAuth("garbled-value") alone would read as inactive).
	snap := ConfigSnapshot{Users: []ConfigUser{
		{Name: "newcomer", PeerIP: "20.0.0.10", PeerASN: 65100, CatalogMode: "OpenCCK", Enabled: true,
			WebAuth: "garbled-value", Networks: []string{"203.0.113.128/25"}},
	}}
	if _, err := applyConfigSnapshot(t, s, snap, AuditMeta{}); err == nil {
		t.Fatal("expected an overlap error for a garbled web_auth that still stores as the active 'network' mode")
	}
}

func TestApplyConfigSnapshotSkipsOverlapValidationForDisabledUsers(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	if _, err := s.AddUser(ctx, User{
		Name: "existing", PeerIP: "20.0.0.9", PeerASN: 65099, Enabled: true, WebAuth: "network",
		FilterMode: FilterModeGlobal, CatalogModeID: DefaultCatalogModeID, Networks: []string{"203.0.113.0/24"},
	}); err != nil {
		t.Fatal(err)
	}

	// Disabled: this user's networks are already excluded from UserByIP and
	// from the OTHER-user side of activeNetworksOverlapTx's own query, so
	// they cannot create any real ambiguity regardless of what they overlap.
	snap := ConfigSnapshot{Users: []ConfigUser{
		{Name: "newcomer", PeerIP: "20.0.0.10", PeerASN: 65100, CatalogMode: "OpenCCK", Enabled: false,
			WebAuth: "network", Networks: []string{"203.0.113.128/25"}},
	}}
	if _, err := applyConfigSnapshot(t, s, snap, AuditMeta{}); err != nil {
		t.Fatalf("a disabled user's overlapping networks must not block the import: %v", err)
	}
}

func TestApplyConfigSnapshotSwapsPeerIdentitiesWithoutTransientCollision(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	aliceID, err := s.AddUser(ctx, User{
		Name: "alice", PeerIP: "20.0.0.1", PeerASN: 65001, Enabled: true,
		FilterMode: FilterModeGlobal, CatalogModeID: DefaultCatalogModeID,
	})
	if err != nil {
		t.Fatal(err)
	}
	bobID, err := s.AddUser(ctx, User{
		Name: "bob", PeerIP: "20.0.0.2", PeerASN: 65002, Enabled: true,
		FilterMode: FilterModeGlobal, CatalogModeID: DefaultCatalogModeID,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Swapped relative to their current rows: applying in snapshot order
	// (alice first) would transiently try to give alice bob's current tuple
	// while bob still holds it, violating users(peer_ip, peer_asn)'s UNIQUE
	// constraint purely because of write order, unless each changed
	// identity is staged out of the way first.
	snap := ConfigSnapshot{Users: []ConfigUser{
		{Name: "alice", PeerIP: "20.0.0.2", PeerASN: 65002, CatalogMode: "OpenCCK", Enabled: true},
		{Name: "bob", PeerIP: "20.0.0.1", PeerASN: 65001, CatalogMode: "OpenCCK", Enabled: true},
	}}
	if _, err := applyConfigSnapshot(t, s, snap, AuditMeta{}); err != nil {
		t.Fatalf("a valid peer identity swap was rejected: %v", err)
	}

	alice, err := s.User(ctx, aliceID)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := s.User(ctx, bobID)
	if err != nil {
		t.Fatal(err)
	}
	if alice.PeerIP != "20.0.0.2" || alice.PeerASN != 65002 {
		t.Fatalf("alice = %+v, want bob's old identity", alice)
	}
	if bob.PeerIP != "20.0.0.1" || bob.PeerASN != 65001 {
		t.Fatalf("bob = %+v, want alice's old identity", bob)
	}
}

func TestApplyConfigSnapshotNeverStagesAwayAnUnresolvedModeUsersIdentity(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	daveID := configSnapshotTestUser(t, s, "dave", "20.0.0.3", 65003, DefaultCatalogModeID)

	// dave's peer identity is changing, but his catalog mode doesn't resolve
	// on this instance — the second loop in ApplyConfigSnapshot skips him
	// entirely (UnknownModes), without ever writing a real identity back. If
	// the identity-staging pass ran for him anyway, he'd be left stuck with
	// the staging placeholder as his permanent identity.
	snap := ConfigSnapshot{Users: []ConfigUser{
		{Name: "dave", PeerIP: "20.0.0.4", PeerASN: 65004, CatalogMode: "no-such-mode", Enabled: true},
	}}
	result, err := applyConfigSnapshot(t, s, snap, AuditMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.UnknownModes) != 1 {
		t.Fatalf("unknown modes = %+v, want one entry for dave", result.UnknownModes)
	}
	if len(result.AffectedUserIDs) != 0 {
		t.Fatalf("affected user ids = %+v, want none: dave was skipped", result.AffectedUserIDs)
	}

	dave, err := s.User(ctx, daveID)
	if err != nil {
		t.Fatal(err)
	}
	if dave.PeerIP != "20.0.0.3" || dave.PeerASN != 65003 {
		t.Fatalf("dave = %+v, want his original identity untouched, not the staging placeholder", dave)
	}
}

func TestApplyConfigSnapshotReservesStagingIdentityAgainstARealCollision(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	aliceID, err := s.AddUser(ctx, User{
		Name: "alice", PeerIP: "20.0.0.1", PeerASN: 65001, Enabled: true,
		FilterMode: FilterModeGlobal, CatalogModeID: DefaultCatalogModeID,
	})
	if err != nil {
		t.Fatal(err)
	}
	// A real, already-configured user who happens to sit exactly on the
	// naive first guess stagingPeerIdentity would make for alice (userID
	// offset from 4200000000) if it didn't check for collisions first —
	// a lab/test deployment using documentation addresses and private ASNs
	// is exactly the scenario the finding this test covers describes.
	collisionASN := uint32(4200000000) + uint32(aliceID) //nolint:gosec
	if _, err := s.AddUser(ctx, User{
		Name: "placeholder-squatter", PeerIP: stagingPeerIPText, PeerASN: collisionASN, Enabled: true,
		FilterMode: FilterModeGlobal, CatalogModeID: DefaultCatalogModeID,
	}); err != nil {
		t.Fatal(err)
	}

	// alice's identity changes, which would stage her through
	// (stagingPeerIPText, collisionASN) — exactly what the squatter already
	// holds — if the reservation didn't skip already-taken tuples.
	snap := ConfigSnapshot{Users: []ConfigUser{
		{Name: "alice", PeerIP: "20.0.0.9", PeerASN: 65009, CatalogMode: "OpenCCK", Enabled: true},
		{Name: "placeholder-squatter", PeerIP: stagingPeerIPText, PeerASN: collisionASN, CatalogMode: "OpenCCK", Enabled: true},
	}}
	if _, err := applyConfigSnapshot(t, s, snap, AuditMeta{}); err != nil {
		t.Fatalf("a valid import was rejected by a staging-identity collision: %v", err)
	}

	alice, err := s.User(ctx, aliceID)
	if err != nil {
		t.Fatal(err)
	}
	if alice.PeerIP != "20.0.0.9" || alice.PeerASN != 65009 {
		t.Fatalf("alice = %+v, want her new imported identity", alice)
	}
}

func TestApplyConfigSnapshotReservesSnapshotTargetIdentitiesBeforeStaging(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	// "amy" sorts before "carol" (ApplyConfigSnapshot processes snap.Users
	// in name order), which matters here: amy's own real identity update
	// must run BEFORE carol's staging placeholder is ever vacated, or the
	// collision below never actually manifests — carol moving off the
	// contested tuple first would quietly resolve it regardless of whether
	// it was ever reserved.
	amyID, err := s.AddUser(ctx, User{
		Name: "amy", PeerIP: "20.0.0.1", PeerASN: 65001, Enabled: true,
		FilterMode: FilterModeGlobal, CatalogModeID: DefaultCatalogModeID,
	})
	if err != nil {
		t.Fatal(err)
	}
	carolID, err := s.AddUser(ctx, User{
		Name: "carol", PeerIP: "20.0.0.3", PeerASN: 65003, Enabled: true,
		FilterMode: FilterModeGlobal, CatalogModeID: DefaultCatalogModeID,
	})
	if err != nil {
		t.Fatal(err)
	}

	// amy's imported identity exactly matches carol's own naive first-guess
	// staging placeholder (4200000000+carolID) — nothing in the live table
	// occupies it, so without reserving every snapshot target at the
	// staging address up front, carol would be staged straight onto exactly
	// the tuple amy's own update claims moments later (amy sorts first, so
	// her real write runs while carol is still parked there), rejecting
	// this entirely valid import on a UNIQUE(peer_ip, peer_asn) violation
	// purely because of staging order.
	targetASN := uint32(4200000000) + uint32(carolID) //nolint:gosec
	snap := ConfigSnapshot{Users: []ConfigUser{
		{Name: "amy", PeerIP: stagingPeerIPText, PeerASN: targetASN, CatalogMode: "OpenCCK", Enabled: true},
		{Name: "carol", PeerIP: "20.0.0.9", PeerASN: 65009, CatalogMode: "OpenCCK", Enabled: true},
	}}
	if _, err := applyConfigSnapshot(t, s, snap, AuditMeta{}); err != nil {
		t.Fatalf("a valid import was rejected by an un-reserved snapshot-target collision: %v", err)
	}

	amy, err := s.User(ctx, amyID)
	if err != nil {
		t.Fatal(err)
	}
	if amy.PeerIP != stagingPeerIPText || amy.PeerASN != targetASN {
		t.Fatalf("amy = %+v, want her imported identity at the staging address", amy)
	}
	carol, err := s.User(ctx, carolID)
	if err != nil {
		t.Fatal(err)
	}
	if carol.PeerIP != "20.0.0.9" || carol.PeerASN != 65009 {
		t.Fatalf("carol = %+v, want her new imported identity", carol)
	}
}

func TestApplyConfigSnapshotNormalizesAddressesBeforeReservingStagingTargets(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	// Same scenario and ordering as the test above, but amy's PeerIP is
	// written with surrounding whitespace — a raw string comparison against
	// stagingPeerIPText would miss it, even though encodeAddrArg (what the
	// real write actually stores) trims it and ends up with the exact same
	// BLOB as the unpadded form.
	amyID, err := s.AddUser(ctx, User{
		Name: "amy", PeerIP: "20.0.0.1", PeerASN: 65001, Enabled: true,
		FilterMode: FilterModeGlobal, CatalogModeID: DefaultCatalogModeID,
	})
	if err != nil {
		t.Fatal(err)
	}
	carolID, err := s.AddUser(ctx, User{
		Name: "carol", PeerIP: "20.0.0.3", PeerASN: 65003, Enabled: true,
		FilterMode: FilterModeGlobal, CatalogModeID: DefaultCatalogModeID,
	})
	if err != nil {
		t.Fatal(err)
	}

	targetASN := uint32(4200000000) + uint32(carolID) //nolint:gosec
	snap := ConfigSnapshot{Users: []ConfigUser{
		{Name: "amy", PeerIP: " " + stagingPeerIPText + " ", PeerASN: targetASN, CatalogMode: "OpenCCK", Enabled: true},
		{Name: "carol", PeerIP: "20.0.0.9", PeerASN: 65009, CatalogMode: "OpenCCK", Enabled: true},
	}}
	if _, err := applyConfigSnapshot(t, s, snap, AuditMeta{}); err != nil {
		t.Fatalf("a valid import was rejected by an un-normalized staging-target collision: %v", err)
	}

	amy, err := s.User(ctx, amyID)
	if err != nil {
		t.Fatal(err)
	}
	if amy.PeerIP != stagingPeerIPText || amy.PeerASN != targetASN {
		t.Fatalf("amy = %+v, want her imported identity (normalized) at the staging address", amy)
	}
	carol, err := s.User(ctx, carolID)
	if err != nil {
		t.Fatal(err)
	}
	if carol.PeerIP != "20.0.0.9" || carol.PeerASN != 65009 {
		t.Fatalf("carol = %+v, want her new imported identity", carol)
	}
}
