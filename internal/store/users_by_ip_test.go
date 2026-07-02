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
