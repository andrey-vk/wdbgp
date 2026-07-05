package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestUserByIPIgnoresLoginModeNetworks(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "byip-login.sqlite3"), false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck,gosec // test cleanup

	// A login-mode user's network is stale — even though it's the more
	// specific (longer) match for this address, it must never win, since
	// login mode never uses IP for identification.
	loginUser, err := s.AddUser(ctx, User{Name: "login-user", PeerIP: "10.1.0.1", PeerASN: 65001, Enabled: true, WebAuth: "login"})
	if err != nil {
		t.Fatal(err)
	}
	networkUser, err := s.AddUser(ctx, User{
		Name: "network-user", PeerIP: "10.1.0.2", PeerASN: 65002, Enabled: true, WebAuth: "network",
		Networks: []string{"10.0.0.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	insertRawNetwork(t, s.DB, loginUser, "10.0.0.192/26") // more specific, but stale — covers .192-.255

	got, err := s.UserByIP(ctx, "10.0.0.200")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != networkUser {
		t.Errorf("UserByIP resolved to user %d (%s), want %d (network-user)", got.ID, got.Name, networkUser)
	}
}

func TestUserByIPStillPrefersMoreSpecificActiveNetwork(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "byip-specific.sqlite3"), false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck,gosec // test cleanup

	broadUser, err := s.AddUser(ctx, User{
		Name: "broad", PeerIP: "10.1.0.1", PeerASN: 65001, Enabled: true, WebAuth: "network",
		Networks: []string{"10.0.0.0/16"},
	})
	if err != nil {
		t.Fatal(err)
	}
	specificUser, err := s.AddUser(ctx, User{
		Name: "specific", PeerIP: "10.1.0.2", PeerASN: 65002, Enabled: true, WebAuth: "any",
		Networks: []string{"10.0.5.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := s.UserByIP(ctx, "10.0.5.1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != specificUser {
		t.Errorf("UserByIP resolved to user %d, want %d (specific, both active)", got.ID, specificUser)
	}

	got, err = s.UserByIP(ctx, "10.0.9.1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != broadUser {
		t.Errorf("UserByIP resolved to user %d, want %d (only the broad range covers this address)", got.ID, broadUser)
	}
}

// TestUserByIPFallsBackWhenBestMatchIsDisabled — a disabled user's leftover
// network must never shadow an enabled user's overlapping-but-broader
// network. ActiveNetworksOverlap deliberately excludes disabled users from
// its cross-user overlap check (by design — see its doc comment), so a
// disabled user's more-specific network and an enabled user's broader one
// can legally coexist. Before the fix, UserByIP picked the single
// longest-prefix match across ALL users regardless of enabled state, then
// hard-failed with sql.ErrNoRows if that winner turned out disabled —
// instead of falling back to the enabled user whose broader network also
// covers the address.
func TestUserByIPFallsBackWhenBestMatchIsDisabled(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "byip-disabled-shadow.sqlite3"), false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck,gosec // test cleanup

	// Disabled user with a more-specific (narrower) network.
	_, err = s.AddUser(ctx, User{
		Name: "disabled-narrow", PeerIP: "10.2.0.1", PeerASN: 65010, Enabled: false, WebAuth: "network",
		Networks: []string{"10.0.0.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Enabled user with a broader network that also covers the address.
	enabledUser, err := s.AddUser(ctx, User{
		Name: "enabled-broad", PeerIP: "10.2.0.2", PeerASN: 65011, Enabled: true, WebAuth: "network",
		Networks: []string{"10.0.0.0/16"},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := s.UserByIP(ctx, "10.0.0.5")
	if err != nil {
		t.Fatalf("UserByIP returned error %v, want resolution to the enabled user", err)
	}
	if got.ID != enabledUser {
		t.Errorf("UserByIP resolved to user %d, want %d (enabled, broader network) — the disabled user's more-specific network must not shadow it", got.ID, enabledUser)
	}
}
