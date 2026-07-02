package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestActiveNetworksOverlapDetectsConflict(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "overlap.sqlite3"), false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck,gosec // test cleanup

	_, err = s.AddUser(ctx, User{
		Name: "existing", PeerIP: "10.1.0.1", PeerASN: 65001, Enabled: true, WebAuth: "network",
		Networks: []string{"10.0.0.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}

	err = s.ActiveNetworksOverlap(ctx, []string{"10.0.0.128/25"}, 0)
	if err == nil {
		t.Fatal("expected overlap error")
	}
	if !strings.Contains(err.Error(), "existing") {
		t.Errorf("error = %q, want it to name the conflicting user", err.Error())
	}
}

func TestActiveNetworksOverlapIgnoresLoginModeUsers(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "overlap-login.sqlite3"), false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck,gosec // test cleanup

	loginUser, err := s.AddUser(ctx, User{Name: "login-user", PeerIP: "10.1.0.1", PeerASN: 65001, Enabled: true, WebAuth: "login"})
	if err != nil {
		t.Fatal(err)
	}
	insertRawNetwork(t, s.DB, loginUser, "10.0.0.0/24")

	if err := s.ActiveNetworksOverlap(ctx, []string{"10.0.0.128/25"}, 0); err != nil {
		t.Errorf("unexpected error, login-mode network should be ignored: %v", err)
	}
}

func TestActiveNetworksOverlapExcludesSelf(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "overlap-self.sqlite3"), false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck,gosec // test cleanup

	userID, err := s.AddUser(ctx, User{
		Name: "self", PeerIP: "10.1.0.1", PeerASN: 65001, Enabled: true, WebAuth: "network",
		Networks: []string{"10.0.0.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Re-checking the same user's own (unchanged) network against itself
	// must not be treated as a conflict.
	if err := s.ActiveNetworksOverlap(ctx, []string{"10.0.0.0/24"}, userID); err != nil {
		t.Errorf("unexpected error checking a user's own network against itself: %v", err)
	}
}

func TestActiveNetworksOverlapAllowsNonOverlapping(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "overlap-none.sqlite3"), false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck,gosec // test cleanup

	_, err = s.AddUser(ctx, User{
		Name: "existing", PeerIP: "10.1.0.1", PeerASN: 65001, Enabled: true, WebAuth: "network",
		Networks: []string{"10.0.0.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := s.ActiveNetworksOverlap(ctx, []string{"10.0.2.0/24"}, 0); err != nil {
		t.Errorf("unexpected error for non-overlapping network: %v", err)
	}
}
