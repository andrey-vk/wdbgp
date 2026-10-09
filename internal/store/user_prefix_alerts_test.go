package store

import (
	"context"
	"testing"
)

func prefixAlertTestUser(t *testing.T, s *Store) int64 {
	t.Helper()
	id, err := s.AddUser(context.Background(), User{
		Name: "alice", PeerIP: "20.0.0.1", PeerASN: 65001, Enabled: true,
		FilterMode: FilterModeGlobal, CatalogModeID: DefaultCatalogModeID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestUserPrefixHistoryRoundTripsAndPurges(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	userID := prefixAlertTestUser(t, s)

	if err := s.RecordUserPrefixSnapshot(ctx, userID, 10, 2); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordUserPrefixSnapshot(ctx, userID, 5, 1); err != nil {
		t.Fatal(err)
	}

	history, err := s.UserPrefixHistory(ctx, userID, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 {
		t.Fatalf("history = %+v, want 2 rows", history)
	}
	if history[0].V4Count != 10 || history[1].V4Count != 5 {
		t.Fatalf("history v4 counts = [%d, %d], want [10, 5] oldest first", history[0].V4Count, history[1].V4Count)
	}

	// Back-date both rows well past any retention window, since daysCutoff
	// has second-level granularity and these were just inserted this same
	// second — a real purge only ever runs long after, never this close.
	if _, err := s.DB.ExecContext(ctx, "UPDATE user_prefix_history SET recorded_at = recorded_at - 100*86400"); err != nil {
		t.Fatal(err)
	}
	if err := s.PurgeUserPrefixHistory(ctx, 30); err != nil {
		t.Fatal(err)
	}
	history, err = s.UserPrefixHistory(ctx, userID, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 0 {
		t.Fatalf("history after purge = %+v, want none", history)
	}
}

func TestEvaluateUserPrefixAlertFirstObservationEstablishesBaseline(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	userID := prefixAlertTestUser(t, s)

	transition, err := s.EvaluateUserPrefixAlert(ctx, userID, 100, 0, 50, 10)
	if err != nil {
		t.Fatal(err)
	}
	if transition != nil {
		t.Fatalf("transition = %+v, want nil on the very first observation", transition)
	}

	// A second call with the same count: still no transition, baseline
	// tracks it.
	transition, err = s.EvaluateUserPrefixAlert(ctx, userID, 100, 0, 50, 10)
	if err != nil {
		t.Fatal(err)
	}
	if transition != nil {
		t.Fatalf("transition = %+v, want nil when nothing changed", transition)
	}
}

func TestEvaluateUserPrefixAlertDetectsDropAndRecovery(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	userID := prefixAlertTestUser(t, s)

	// Establish a normal baseline of 100.
	if _, err := s.EvaluateUserPrefixAlert(ctx, userID, 100, 0, 50, 10); err != nil {
		t.Fatal(err)
	}

	// Drops to 10 (90% drop, past the 50% threshold).
	transition, err := s.EvaluateUserPrefixAlert(ctx, userID, 10, 0, 50, 10)
	if err != nil {
		t.Fatal(err)
	}
	if transition == nil || transition.Event != PrefixAlertDrop {
		t.Fatalf("transition = %+v, want a drop event", transition)
	}
	if transition.BaselineV4 != 100 || transition.CurrentV4 != 10 {
		t.Fatalf("transition = %+v, want baseline 100, current 10", transition)
	}
	// Simulates Checker successfully delivering the drop webhook — without
	// this, the eventual recovery below is silently cancelled instead of
	// reported (see TestEvaluateUserPrefixAlertCancelsRecoveryForAnUndeliveredDrop).
	if err := s.MarkPrefixAlertDropDelivered(ctx, userID); err != nil {
		t.Fatal(err)
	}

	// Still down on the next check: must not re-fire.
	transition, err = s.EvaluateUserPrefixAlert(ctx, userID, 10, 0, 50, 10)
	if err != nil {
		t.Fatal(err)
	}
	if transition != nil {
		t.Fatalf("transition = %+v, want nil: already alerting, must not repeat", transition)
	}

	// A partial recovery (60, still below the 50 dropLine relative to the
	// original 100 baseline) must not yet report recovered.
	transition, err = s.EvaluateUserPrefixAlert(ctx, userID, 49, 0, 50, 10)
	if err != nil {
		t.Fatal(err)
	}
	if transition != nil {
		t.Fatalf("transition = %+v, want nil: still below the recovery line", transition)
	}

	// Recovers back to (at least) the drop line.
	transition, err = s.EvaluateUserPrefixAlert(ctx, userID, 95, 0, 50, 10)
	if err != nil {
		t.Fatal(err)
	}
	if transition == nil || transition.Event != PrefixAlertRecovered {
		t.Fatalf("transition = %+v, want a recovered event", transition)
	}
	if transition.BaselineV4 != 100 {
		t.Fatalf("recovered transition baseline = %d, want the original pre-drop 100", transition.BaselineV4)
	}
	if transition.DurationSeconds < 0 {
		t.Fatalf("duration = %d, want >= 0", transition.DurationSeconds)
	}
}

func TestEvaluateUserPrefixAlertIgnoresDropsBelowBaselineMinimum(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	userID := prefixAlertTestUser(t, s)

	// Baseline of 2 prefixes.
	if _, err := s.EvaluateUserPrefixAlert(ctx, userID, 2, 0, 50, 10); err != nil {
		t.Fatal(err)
	}
	// Drops to 1 — a 50% drop, but the baseline (2) is below
	// baselineMinimum (10), so this must not be meaningful noise.
	transition, err := s.EvaluateUserPrefixAlert(ctx, userID, 1, 0, 50, 10)
	if err != nil {
		t.Fatal(err)
	}
	if transition != nil {
		t.Fatalf("transition = %+v, want nil: baseline is below the minimum floor", transition)
	}
}

func TestEvaluateUserPrefixAlertDoesNotFlapAtExactDropLine(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	userID := prefixAlertTestUser(t, s)

	// Baseline of 100, 50% threshold -> dropLine is exactly 50.
	if _, err := s.EvaluateUserPrefixAlert(ctx, userID, 100, 0, 50, 10); err != nil {
		t.Fatal(err)
	}
	transition, err := s.EvaluateUserPrefixAlert(ctx, userID, 50, 0, 50, 10)
	if err != nil {
		t.Fatal(err)
	}
	if transition == nil || transition.Event != PrefixAlertDrop {
		t.Fatalf("transition = %+v, want a drop event landing exactly on the line", transition)
	}
	if err := s.MarkPrefixAlertDropDelivered(ctx, userID); err != nil {
		t.Fatal(err)
	}

	// Parked exactly on the line, unchanged: must still be "alerting", not
	// bounce to "recovered" and back on every tick despite nothing changing.
	for i := 0; i < 3; i++ {
		transition, err = s.EvaluateUserPrefixAlert(ctx, userID, 50, 0, 50, 10)
		if err != nil {
			t.Fatal(err)
		}
		if transition != nil {
			t.Fatalf("tick %d: transition = %+v, want nil: a count parked exactly on the drop line is still an active drop, not a recovery", i, transition)
		}
	}

	// Only strictly above the line counts as recovered.
	transition, err = s.EvaluateUserPrefixAlert(ctx, userID, 51, 0, 50, 10)
	if err != nil {
		t.Fatal(err)
	}
	if transition == nil || transition.Event != PrefixAlertRecovered {
		t.Fatalf("transition = %+v, want a recovered event once strictly above the drop line", transition)
	}
}

func TestEvaluateUserPrefixAlertCancelsRecoveryForAnUndeliveredDrop(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	userID := prefixAlertTestUser(t, s)

	if _, err := s.EvaluateUserPrefixAlert(ctx, userID, 100, 0, 50, 10); err != nil {
		t.Fatal(err)
	}
	transition, err := s.EvaluateUserPrefixAlert(ctx, userID, 10, 0, 50, 10)
	if err != nil {
		t.Fatal(err)
	}
	if transition == nil || transition.Event != PrefixAlertDrop {
		t.Fatalf("transition = %+v, want a drop event", transition)
	}
	// Deliberately NOT calling MarkPrefixAlertDropDelivered: simulates
	// alerting being disabled, or every delivery attempt having failed,
	// for this entire episode.

	transition, err = s.EvaluateUserPrefixAlert(ctx, userID, 95, 0, 50, 10)
	if err != nil {
		t.Fatal(err)
	}
	if transition != nil {
		t.Fatalf("transition = %+v, want nil: reporting a recovery for an incident nobody was ever told about is more confusing than useful", transition)
	}

	// The episode is over and must not still be offered for retry.
	pending, err := s.PendingPrefixAlertDrop(ctx, userID, 95, 0)
	if err != nil {
		t.Fatal(err)
	}
	if pending != nil {
		t.Fatalf("pending = %+v, want nil: the episode already ended", pending)
	}

	// A fresh drop afterward must behave normally again.
	transition, err = s.EvaluateUserPrefixAlert(ctx, userID, 5, 0, 50, 10)
	if err != nil {
		t.Fatal(err)
	}
	if transition == nil || transition.Event != PrefixAlertDrop {
		t.Fatalf("transition = %+v, want a fresh drop event", transition)
	}
}

func TestEvaluateUserPrefixAlertNeverFiresOnAZeroBaseline(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	userID := prefixAlertTestUser(t, s)

	// Baseline of 0, and baselineMinimum of 0 too (an admin-accepted value —
	// validateNonNegative allows 0): without an explicit baselineTotal > 0
	// guard, 0 >= 0 would satisfy the floor check, and 0 <= dropLine(0)
	// would satisfy the drop check, firing a "drop" for a user who has
	// always had nothing and still has nothing.
	if _, err := s.EvaluateUserPrefixAlert(ctx, userID, 0, 0, 50, 0); err != nil {
		t.Fatal(err)
	}
	transition, err := s.EvaluateUserPrefixAlert(ctx, userID, 0, 0, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if transition != nil {
		t.Fatalf("transition = %+v, want nil: a zero baseline can never meaningfully drop", transition)
	}
}

func TestPrefixAlertDropStillPendingTracksTheEpisode(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	userID := prefixAlertTestUser(t, s)

	pending, err := s.PrefixAlertDropStillPending(ctx, userID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if pending {
		t.Fatal("pending = true, want false: no alert-state row exists yet")
	}

	if _, err := s.EvaluateUserPrefixAlert(ctx, userID, 100, 0, 50, 10); err != nil {
		t.Fatal(err)
	}
	drop, err := s.EvaluateUserPrefixAlert(ctx, userID, 10, 0, 50, 10)
	if err != nil {
		t.Fatal(err)
	}
	if drop == nil {
		t.Fatalf("drop = %+v, want a drop event", drop)
	}
	pending, err = s.PrefixAlertDropStillPending(ctx, userID, drop.DetectedAt)
	if err != nil {
		t.Fatal(err)
	}
	if !pending {
		t.Fatal("pending = false, want true: the drop was just detected and not yet delivered")
	}

	if err := s.MarkPrefixAlertDropDelivered(ctx, userID); err != nil {
		t.Fatal(err)
	}
	pending, err = s.PrefixAlertDropStillPending(ctx, userID, drop.DetectedAt)
	if err != nil {
		t.Fatal(err)
	}
	if pending {
		t.Fatal("pending = true, want false: it was just marked delivered")
	}

	// Recovers: the episode is over, so it's no longer "pending" either.
	if _, err := s.EvaluateUserPrefixAlert(ctx, userID, 95, 0, 50, 10); err != nil {
		t.Fatal(err)
	}
	pending, err = s.PrefixAlertDropStillPending(ctx, userID, drop.DetectedAt)
	if err != nil {
		t.Fatal(err)
	}
	if pending {
		t.Fatal("pending = true, want false: the user has recovered")
	}
}

func TestPrefixAlertDropStillPendingRejectsAStaleEpisode(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	userID := prefixAlertTestUser(t, s)

	if _, err := s.EvaluateUserPrefixAlert(ctx, userID, 100, 0, 50, 10); err != nil {
		t.Fatal(err)
	}
	staleDrop, err := s.EvaluateUserPrefixAlert(ctx, userID, 10, 0, 50, 10)
	if err != nil {
		t.Fatal(err)
	}
	if staleDrop == nil {
		t.Fatalf("staleDrop = %+v, want a drop event", staleDrop)
	}
	// Deliberately not marked delivered: simulates it sitting queued behind
	// a busy delivery batch.

	// Simulates the user having recovered from, and dropped again into, a
	// SECOND distinct episode before the stale first one ever got
	// revalidated and sent — applied directly via SQL (rather than two more
	// real EvaluateUserPrefixAlert calls) to force a DetectedAt
	// deterministically different from staleDrop's own, rather than relying
	// on two time.Now() calls landing in different wall-clock seconds; the
	// recover-then-redrop mechanics themselves are already covered by
	// TestEvaluateUserPrefixAlertCancelsRecoveryForAnUndeliveredDrop — this
	// test's job is specifically the revalidation logic's own episode
	// matching, independent of how the row came to describe a new episode.
	freshDetectedAt := staleDrop.DetectedAt + 1000
	if _, err := s.DB.ExecContext(ctx,
		"UPDATE user_prefix_alert_state SET alerting_since = ?, drop_delivered = 0 WHERE user_id = ?",
		freshDetectedAt, userID); err != nil {
		t.Fatal(err)
	}

	// Revalidating the STALE (first) episode must report false — the row
	// now describes the fresh, second episode, not this one — even though
	// "some" drop is genuinely undelivered right now. Checking only that
	// would wrongly confirm the stale payload as still postable and, worse,
	// let MarkPrefixAlertDropDelivered wrongly mark the fresh episode
	// delivered without it ever actually being sent.
	stalePending, err := s.PrefixAlertDropStillPending(ctx, userID, staleDrop.DetectedAt)
	if err != nil {
		t.Fatal(err)
	}
	if stalePending {
		t.Fatal("stalePending = true, want false: this specific (stale) episode is no longer what the row describes")
	}

	// The fresh episode itself must still correctly report pending.
	freshPending, err := s.PrefixAlertDropStillPending(ctx, userID, freshDetectedAt)
	if err != nil {
		t.Fatal(err)
	}
	if !freshPending {
		t.Fatal("freshPending = false, want true: the fresh episode is genuinely still undelivered")
	}
}

func TestPendingPrefixAlertDropOffersRetryUntilMarkedDelivered(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	userID := prefixAlertTestUser(t, s)

	if _, err := s.EvaluateUserPrefixAlert(ctx, userID, 100, 0, 50, 10); err != nil {
		t.Fatal(err)
	}
	drop, err := s.EvaluateUserPrefixAlert(ctx, userID, 10, 0, 50, 10)
	if err != nil {
		t.Fatal(err)
	}
	if drop == nil || drop.DetectedAt == 0 {
		t.Fatalf("drop = %+v, want a non-zero DetectedAt", drop)
	}

	pending, err := s.PendingPrefixAlertDrop(ctx, userID, 8, 0)
	if err != nil {
		t.Fatal(err)
	}
	if pending == nil || pending.Event != PrefixAlertDrop {
		t.Fatalf("pending = %+v, want an undelivered drop", pending)
	}
	if pending.BaselineV4 != 100 || pending.CurrentV4 != 8 {
		t.Fatalf("pending = %+v, want baseline 100 (stored) and current 8 (this call's freshly measured value, not the original 10)", pending)
	}
	if pending.DetectedAt != drop.DetectedAt {
		t.Fatalf("pending.DetectedAt = %d, want the original detection time %d (not a fresh timestamp from this retry)", pending.DetectedAt, drop.DetectedAt)
	}

	if err := s.MarkPrefixAlertDropDelivered(ctx, userID); err != nil {
		t.Fatal(err)
	}
	pending, err = s.PendingPrefixAlertDrop(ctx, userID, 8, 0)
	if err != nil {
		t.Fatal(err)
	}
	if pending != nil {
		t.Fatalf("pending = %+v, want nil once marked delivered", pending)
	}
}

func TestPrefixAlertRecoveryStillPendingTracksTheEpisode(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	userID := prefixAlertTestUser(t, s)

	pending, err := s.PrefixAlertRecoveryStillPending(ctx, userID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if pending {
		t.Fatal("pending = true, want false: no alert-state row exists yet")
	}

	if _, err := s.EvaluateUserPrefixAlert(ctx, userID, 100, 0, 50, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EvaluateUserPrefixAlert(ctx, userID, 10, 0, 50, 10); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkPrefixAlertDropDelivered(ctx, userID); err != nil {
		t.Fatal(err)
	}
	pending, err = s.PrefixAlertRecoveryStillPending(ctx, userID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if pending {
		t.Fatal("pending = true, want false: still mid-drop, no recovery yet")
	}

	transition, err := s.EvaluateUserPrefixAlert(ctx, userID, 95, 0, 50, 10)
	if err != nil {
		t.Fatal(err)
	}
	if transition == nil || transition.Event != PrefixAlertRecovered {
		t.Fatalf("transition = %+v, want a recovered event", transition)
	}
	pending, err = s.PrefixAlertRecoveryStillPending(ctx, userID, transition.DetectedAt)
	if err != nil {
		t.Fatal(err)
	}
	if !pending {
		t.Fatal("pending = false, want true: the recovery was just detected and not yet delivered")
	}

	if err := s.MarkPrefixAlertRecoveryDelivered(ctx, userID); err != nil {
		t.Fatal(err)
	}
	pending, err = s.PrefixAlertRecoveryStillPending(ctx, userID, transition.DetectedAt)
	if err != nil {
		t.Fatal(err)
	}
	if pending {
		t.Fatal("pending = true, want false: it was just marked delivered")
	}
}

func TestPrefixAlertRecoveryStillPendingRejectsAStaleEpisode(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	userID := prefixAlertTestUser(t, s)

	if _, err := s.EvaluateUserPrefixAlert(ctx, userID, 100, 0, 50, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EvaluateUserPrefixAlert(ctx, userID, 10, 0, 50, 10); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkPrefixAlertDropDelivered(ctx, userID); err != nil {
		t.Fatal(err)
	}
	staleRecovery, err := s.EvaluateUserPrefixAlert(ctx, userID, 95, 0, 50, 10)
	if err != nil {
		t.Fatal(err)
	}
	if staleRecovery == nil || staleRecovery.Event != PrefixAlertRecovered {
		t.Fatalf("staleRecovery = %+v, want a recovered event", staleRecovery)
	}
	// Deliberately not marked delivered: simulates it sitting queued behind
	// a busy delivery batch.

	// Simulates a SECOND recovery firing (a drop then another recovery)
	// before the stale first one ever got revalidated and sent — applied
	// directly via SQL for the same determinism reason
	// TestPrefixAlertDropStillPendingRejectsAStaleEpisode's identical
	// comment explains: this test's job is the revalidation logic's own
	// episode matching, not reproducing two time.Now() calls landing in
	// different wall-clock seconds.
	freshDetectedAt := staleRecovery.DetectedAt + 1000
	if _, err := s.DB.ExecContext(ctx,
		"UPDATE user_prefix_alert_state SET recovery_pending = 1, recovery_detected_at = ? WHERE user_id = ?",
		freshDetectedAt, userID); err != nil {
		t.Fatal(err)
	}

	// Revalidating the STALE (first) recovery must report false — the row
	// now describes the fresh, second recovery, not this one — even though
	// "some" recovery is genuinely undelivered right now. Checking only the
	// boolean would wrongly confirm the stale payload as still postable
	// and, worse, let MarkPrefixAlertRecoveryDelivered wrongly mark the
	// fresh recovery delivered without it ever actually being sent.
	stalePending, err := s.PrefixAlertRecoveryStillPending(ctx, userID, staleRecovery.DetectedAt)
	if err != nil {
		t.Fatal(err)
	}
	if stalePending {
		t.Fatal("stalePending = true, want false: this specific (stale) recovery is no longer what the row describes")
	}

	// The fresh recovery itself must still correctly report pending.
	freshPending, err := s.PrefixAlertRecoveryStillPending(ctx, userID, freshDetectedAt)
	if err != nil {
		t.Fatal(err)
	}
	if !freshPending {
		t.Fatal("freshPending = false, want true: the fresh recovery is genuinely still undelivered")
	}
}

func TestPendingPrefixAlertRecoveryOffersRetryUntilMarkedDelivered(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	userID := prefixAlertTestUser(t, s)

	if _, err := s.EvaluateUserPrefixAlert(ctx, userID, 100, 0, 50, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EvaluateUserPrefixAlert(ctx, userID, 10, 0, 50, 10); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkPrefixAlertDropDelivered(ctx, userID); err != nil {
		t.Fatal(err)
	}
	recovered, err := s.EvaluateUserPrefixAlert(ctx, userID, 95, 0, 50, 10)
	if err != nil {
		t.Fatal(err)
	}
	if recovered == nil || recovered.Event != PrefixAlertRecovered {
		t.Fatalf("recovered = %+v, want a recovered event", recovered)
	}

	// currentV4/V6 passed here (99, the latest measurement) deliberately
	// differs from the 95 the recovery actually fired at, confirming the
	// retried payload reports the freshest count rather than a stale one —
	// mirroring PendingPrefixAlertDrop's own contract.
	pending, err := s.PendingPrefixAlertRecovery(ctx, userID, 99, 0)
	if err != nil {
		t.Fatal(err)
	}
	if pending == nil || pending.Event != PrefixAlertRecovered {
		t.Fatalf("pending = %+v, want an undelivered recovery", pending)
	}
	if pending.BaselineV4 != 100 || pending.CurrentV4 != 99 {
		t.Fatalf("pending = %+v, want the pre-incident baseline 100 (frozen, not overwritten) and current 99 (this call's value)", pending)
	}
	if pending.DetectedAt != recovered.DetectedAt {
		t.Fatalf("pending.DetectedAt = %d, want the original recovery time %d", pending.DetectedAt, recovered.DetectedAt)
	}
	if pending.DurationSeconds != recovered.DurationSeconds {
		t.Fatalf("pending.DurationSeconds = %d, want the original %d (frozen, since alerting_since is cleared)", pending.DurationSeconds, recovered.DurationSeconds)
	}

	if err := s.MarkPrefixAlertRecoveryDelivered(ctx, userID); err != nil {
		t.Fatal(err)
	}
	pending, err = s.PendingPrefixAlertRecovery(ctx, userID, 99, 0)
	if err != nil {
		t.Fatal(err)
	}
	if pending != nil {
		t.Fatalf("pending = %+v, want nil once marked delivered", pending)
	}
}

func TestEvaluateUserPrefixAlertCancelsPendingRecoveryOnFreshDrop(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	userID := prefixAlertTestUser(t, s)

	if _, err := s.EvaluateUserPrefixAlert(ctx, userID, 100, 0, 50, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EvaluateUserPrefixAlert(ctx, userID, 10, 0, 50, 10); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkPrefixAlertDropDelivered(ctx, userID); err != nil {
		t.Fatal(err)
	}
	recovered, err := s.EvaluateUserPrefixAlert(ctx, userID, 95, 0, 50, 10)
	if err != nil {
		t.Fatal(err)
	}
	if recovered == nil || recovered.Event != PrefixAlertRecovered {
		t.Fatalf("recovered = %+v, want a recovered event", recovered)
	}
	// Deliberately NOT calling MarkPrefixAlertRecoveryDelivered: simulates
	// alerting being unreachable when the recovery just fired.
	pending, err := s.PrefixAlertRecoveryStillPending(ctx, userID, recovered.DetectedAt)
	if err != nil {
		t.Fatal(err)
	}
	if !pending {
		t.Fatal("pending = false, want true: the recovery was just detected, undelivered")
	}

	// A fresh drop now supersedes it: telling the operator "they recovered"
	// right after "they dropped again" already fired is stale and confusing.
	transition, err := s.EvaluateUserPrefixAlert(ctx, userID, 8, 0, 50, 10)
	if err != nil {
		t.Fatal(err)
	}
	if transition == nil || transition.Event != PrefixAlertDrop {
		t.Fatalf("transition = %+v, want a fresh drop event", transition)
	}
	pending, err = s.PrefixAlertRecoveryStillPending(ctx, userID, recovered.DetectedAt)
	if err != nil {
		t.Fatal(err)
	}
	if pending {
		t.Fatal("pending = true, want false: the stale recovery must be cancelled by the fresh drop")
	}
}
