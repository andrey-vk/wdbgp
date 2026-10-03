package store

import (
	"context"
	"errors"
	"testing"
)

// TestSetUserCatalogModePrevModeReflectsImmediatelyPriorState guards
// against a caller using a session/pre-fetch snapshot of the user's mode
// (captured once, before this call) instead of this call's own returned
// prevModeID. Simulates two overlapping switch requests without needing
// real goroutines: a stale read taken before either request's own write
// ran, then two real transitions in sequence (1→2, then 2→3) — the second
// transition's prevModeID must be 2 (what the first transition actually
// committed), not the stale pre-everything value of 1.
func TestSetUserCatalogModePrevModeReflectsImmediatelyPriorState(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	userID, err := s.AddUser(ctx, User{
		Name: "mode-race", PeerIP: "172.16.0.7", PeerASN: 65006, Enabled: true,
		Networks: []string{"192.168.25.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	modeB, err := s.AddCatalogMode(ctx, "mode-b", true)
	if err != nil {
		t.Fatal(err)
	}
	modeC, err := s.AddCatalogMode(ctx, "mode-c", true)
	if err != nil {
		t.Fatal(err)
	}

	// A read taken before anything below runs — if a caller used a value
	// like this instead of this call's own return, it would be stale by
	// the time the second transition below runs.
	staleUser, err := s.User(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if staleUser.CatalogModeID != 1 {
		t.Fatalf("staleUser.CatalogModeID = %d, want 1", staleUser.CatalogModeID)
	}

	// "Request A": switches 1 -> B.
	prevA, err := s.SetUserCatalogMode(ctx, userID, modeB, false)
	if err != nil {
		t.Fatal(err)
	}
	if prevA != 1 {
		t.Fatalf("request A's prevModeID = %d, want 1", prevA)
	}

	// "Request B": switches B -> C. The real immediately-prior state is B
	// (what request A just committed) — a caller using staleUser's
	// CatalogModeID (1) here would log the wrong transition (1→C instead
	// of B→C), and two such overlapping requests would both claim "from
	// 1" even though the real sequence moved through B.
	prevB, err := s.SetUserCatalogMode(ctx, userID, modeC, false)
	if err != nil {
		t.Fatal(err)
	}
	if prevB != modeB {
		t.Fatalf("request B's prevModeID = %d, want %d (request A's committed state), not the stale 1", prevB, modeB)
	}
}

// TestDeleteCatalogModeReassignsUsers confirms the basic contract: every
// user pointing at the deleted mode moves to mode 1, and their IDs come
// back from the call.
func TestDeleteCatalogModeReassignsUsers(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	modeID, err := s.AddCatalogMode(ctx, "to-delete", true)
	if err != nil {
		t.Fatal(err)
	}
	userID, err := s.AddUser(ctx, User{
		Name: "moved", PeerIP: "172.16.0.5", PeerASN: 65004, Enabled: true,
		Networks: []string{"192.168.23.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, "UPDATE users SET catalog_mode_id = ? WHERE id = ?", modeID, userID); err != nil {
		t.Fatal(err)
	}

	ids, err := s.DeleteCatalogMode(ctx, modeID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != userID {
		t.Fatalf("reassigned IDs = %v, want [%d]", ids, userID)
	}
	var newModeID int64
	if err := s.DB.QueryRow("SELECT catalog_mode_id FROM users WHERE id = ?", userID).Scan(&newModeID); err != nil {
		t.Fatal(err)
	}
	if newModeID != 1 {
		t.Fatalf("user's catalog_mode_id = %d, want 1", newModeID)
	}
}

// TestDeleteCatalogModeRetryDoesNotDuplicateReassignedUsers guards against
// Store.Transaction retrying the closure (up to 5 times, for a transient
// SQLite error) and each attempt appending to a shared reassignedUserIDs
// across retries — a later successful attempt must not duplicate an
// earlier failed attempt's already-collected IDs onto the published
// result.
func TestDeleteCatalogModeRetryDoesNotDuplicateReassignedUsers(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	modeID, err := s.AddCatalogMode(ctx, "to-delete-retry", true)
	if err != nil {
		t.Fatal(err)
	}
	userID, err := s.AddUser(ctx, User{
		Name: "moved-retry", PeerIP: "172.16.0.6", PeerASN: 65005, Enabled: true,
		Networks: []string{"192.168.24.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, "UPDATE users SET catalog_mode_id = ? WHERE id = ?", modeID, userID); err != nil {
		t.Fatal(err)
	}

	attempts := 0
	deleteCatalogModeAttemptHook = func(attemptIDs []int64) error {
		attempts++
		if len(attemptIDs) != 1 {
			t.Fatalf("attempt %d collected %v, want exactly [%d] every time", attempts, attemptIDs, userID)
		}
		if attempts == 1 {
			// retry.TransientError matches this substring — forces Store.Transaction to retry.
			return errors.New("simulated transient error")
		}
		return nil
	}
	defer func() { deleteCatalogModeAttemptHook = nil }()

	ids, err := s.DeleteCatalogMode(ctx, modeID)
	if err != nil {
		t.Fatalf("DeleteCatalogMode: %v", err)
	}
	if attempts < 2 {
		t.Fatalf("attempts = %d, want at least 2 (the forced transient error must have triggered a retry)", attempts)
	}
	if len(ids) != 1 || ids[0] != userID {
		t.Fatalf("reassigned IDs = %v, want exactly [%d] — the retry must not have duplicated the first attempt's collected ID", ids, userID)
	}
}
