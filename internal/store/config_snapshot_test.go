package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
)

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
	result, err := s.ApplyConfigSnapshot(ctx, snap, AuditMeta{Actor: "admin:203.0.113.7", Action: "config.imported"})
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
	result2, err := s.ApplyConfigSnapshot(ctx, snap, AuditMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result2.ModesCreated) != 0 || len(result2.ModesUpdated) != 1 {
		t.Fatalf("second apply: modes created=%v updated=%v, want none created and Imported updated", result2.ModesCreated, result2.ModesUpdated)
	}
}
