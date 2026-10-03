package store

import (
	"context"
	"database/sql"
	"testing"
)

// TestSaveUserSelectionCountsBeforeAfterBracketingWouldDoubleCount shows
// the bug the old handler pattern had and SaveUserSelectionCounts' atomic
// before/after fixes: two overlapping requests both checking the same
// previously-unchecked category. If both read "before" via an independent
// UserModeSelection call taken before either request's own write ran (the
// old bracketing pattern), both see before=0; after either one's write,
// both see after=1 (checking an already-checked category is a no-op) —
// so both would report a real change, double-logging one actual change as
// two. SaveUserSelectionCounts (the fix) can't make this mistake: its
// before/after are read inside the same transaction as its own write, so a
// second call naturally sees the first call's already-committed state as
// its "before", not a value cached before anything happened.
func TestSaveUserSelectionCountsBeforeAfterBracketingWouldDoubleCount(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	userID, err := s.AddUser(ctx, User{
		Name: "bracket-bug", PeerIP: "172.16.0.4", PeerASN: 65003, Enabled: true,
		Networks: []string{"192.168.22.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.InsertCatalogEntries(ctx, 1, []CatalogEntry{
		{Category: "cat-b", Service: "svc-b", CIDR: "10.71.0.0/16"},
	}); err != nil {
		t.Fatal(err)
	}

	// The old pattern: both "requests" read "before" via an independent
	// call taken before either one's write has happened.
	staleBefore, _, err := s.UserModeSelection(ctx, userID, DefaultCatalogModeID)
	if err != nil {
		t.Fatal(err)
	}
	if len(staleBefore) != 0 {
		t.Fatalf("staleBefore categories = %d, want 0", len(staleBefore))
	}

	// "Request A" commits its toggle.
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		return ToggleSelectedCategory(ctx, tx, userID, DefaultCatalogModeID, "cat-b", true)
	}); err != nil {
		t.Fatal(err)
	}

	// Both "requests" read "after" via an independent call, post-commit.
	staleAfter, _, err := s.UserModeSelection(ctx, userID, DefaultCatalogModeID)
	if err != nil {
		t.Fatal(err)
	}
	if len(staleAfter) != 1 {
		t.Fatalf("staleAfter categories = %d, want 1", len(staleAfter))
	}
	// Under the old pattern, BOTH requests would now compare
	// len(staleBefore)=0 to len(staleAfter)=1 and conclude a real change —
	// the bug: one actual change double-logged as two.
	if len(staleBefore) == len(staleAfter) {
		t.Fatal("expected the old bracketing pattern to (wrongly) show a change for both requests")
	}

	// The fix: "request B" (toggling the same category, now already
	// checked) using SaveUserSelectionCounts sees the TRUE current state
	// as "before" — 1, not the stale 0 — and correctly reports a no-op.
	beforeB, _, afterB, _, _, err := s.SaveUserSelectionCounts(ctx, userID, DefaultCatalogModeID, false,
		[]CategoryToggle{{Category: "cat-b", Checked: true}}, nil, AuditMeta{}, AuditMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if beforeB != 1 || afterB != 1 {
		t.Fatalf("request B via SaveUserSelectionCounts: before=%d after=%d, want 1 and 1 (correctly a no-op, not a second \"change\")", beforeB, afterB)
	}
}

// TestSaveUserSelectionCountsReflectsImmediatelyPriorState guards against
// a caller bracketing the selection-toggle transaction with two independent
// UserModeSelection reads instead of using this call's own atomic
// before/after. Simulates two overlapping "check this same category"
// requests without needing real goroutines: both toggle the same
// previously-unchecked category on. If a caller used a once-cached
// "before" (read before either request's own transaction ran) for both,
// both would report before=0/after=1 (two real changes) even though only
// the first one actually was — the second is a no-op, since the category
// was already checked by the first.
func TestSaveUserSelectionCountsReflectsImmediatelyPriorState(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	userID, err := s.AddUser(ctx, User{
		Name: "dup-toggle", PeerIP: "172.16.0.3", PeerASN: 65002, Enabled: true,
		Networks: []string{"192.168.21.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.InsertCatalogEntries(ctx, 1, []CatalogEntry{
		{Category: "cat-a", Service: "svc-a", CIDR: "10.70.0.0/16"},
	}); err != nil {
		t.Fatal(err)
	}

	// "Request A": checks cat-a. Real change: 0 -> 1.
	beforeA, _, afterA, _, _, err := s.SaveUserSelectionCounts(ctx, userID, DefaultCatalogModeID, false,
		[]CategoryToggle{{Category: "cat-a", Checked: true}}, nil, AuditMeta{}, AuditMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if beforeA != 0 || afterA != 1 {
		t.Fatalf("request A: before=%d after=%d, want 0 and 1", beforeA, afterA)
	}

	// "Request B": checks the SAME cat-a again (e.g. a duplicate/overlapping
	// submission). The real immediately-prior state is already 1 (what
	// request A just committed) — a caller using a stale pre-everything
	// "before" of 0 here would wrongly report this as a second real
	// change instead of the no-op it actually is.
	beforeB, _, afterB, _, _, err := s.SaveUserSelectionCounts(ctx, userID, DefaultCatalogModeID, false,
		[]CategoryToggle{{Category: "cat-a", Checked: true}}, nil, AuditMeta{}, AuditMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if beforeB != 1 {
		t.Fatalf("request B: before=%d, want 1 (request A's committed state, not a stale 0)", beforeB)
	}
	if afterB != 1 {
		t.Fatalf("request B: after=%d, want 1 (checking an already-checked category is a no-op)", afterB)
	}
}
