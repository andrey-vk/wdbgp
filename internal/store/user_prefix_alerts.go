package store

import (
	"context"
	"database/sql"
	"time"
)

// UserPrefixSnapshot is one periodic measurement of a user's effective
// announced prefix count (after selection and route filters — see
// CountSelectionPrefixes), for the admin "prefix history" chart and for
// EvaluateUserPrefixAlert's own drop/recovery detection.
type UserPrefixSnapshot struct {
	RecordedAt time.Time `json:"recorded_at"`
	V4Count    int       `json:"v4_count"`
	V6Count    int       `json:"v6_count"`
}

// RecordUserPrefixSnapshot inserts one measurement. Unlike SaveUserSnapshot/
// SaveFeedSnapshot, this isn't rate-limited in-place — the periodic checker
// that calls this already controls its own tick interval, so every call here
// is already meant to become its own row.
func (s *Store) RecordUserPrefixSnapshot(ctx context.Context, userID int64, v4, v6 int) error {
	_, err := s.DB.ExecContext(ctx,
		"INSERT INTO user_prefix_history(user_id, recorded_at, v4_count, v6_count) VALUES (?, ?, ?, ?)",
		userID, time.Now().UTC().Unix(), v4, v6)
	return err
}

// UserPrefixHistory returns a user's prefix-count measurements from the last
// `days` days, oldest first.
func (s *Store) UserPrefixHistory(ctx context.Context, userID int64, days int) ([]UserPrefixSnapshot, error) {
	rows, err := s.DB.QueryContext(ctx,
		"SELECT recorded_at, v4_count, v6_count FROM user_prefix_history WHERE user_id = ? AND recorded_at >= ? ORDER BY recorded_at",
		userID, daysCutoff(days))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck
	var out []UserPrefixSnapshot
	for rows.Next() {
		var recordedAtUnix int64
		var snap UserPrefixSnapshot
		if err := rows.Scan(&recordedAtUnix, &snap.V4Count, &snap.V6Count); err != nil {
			return nil, err
		}
		snap.RecordedAt = time.Unix(recordedAtUnix, 0).UTC()
		out = append(out, snap)
	}
	return out, rows.Err()
}

// PurgeUserPrefixHistory deletes prefix-history rows older than N days.
func (s *Store) PurgeUserPrefixHistory(ctx context.Context, days int) error {
	_, err := s.DB.ExecContext(ctx, "DELETE FROM user_prefix_history WHERE recorded_at < ?", daysCutoff(days))
	return err
}

// PrefixAlertEvent names what EvaluateUserPrefixAlert detected.
type PrefixAlertEvent string

const (
	PrefixAlertDrop      PrefixAlertEvent = "prefix_drop"
	PrefixAlertRecovered PrefixAlertEvent = "prefix_recovered"
)

// PrefixAlertTransition describes a state change EvaluateUserPrefixAlert
// detected — the caller (internal/alerts) is expected to deliver this as a
// webhook. Baseline is the last known-normal count: the count just before a
// drop for a PrefixAlertDrop event, or what it was before the incident
// started for a PrefixAlertRecovered one. DetectedAt is when this episode's
// drop actually started (unix seconds) — not necessarily when this
// particular PrefixAlertTransition value was produced: a drop retried via
// PendingPrefixAlertDrop carries the original detection time forward, so a
// webhook enabled hours into an outage still reports when it actually began.
type PrefixAlertTransition struct {
	Event           PrefixAlertEvent
	DetectedAt      int64
	BaselineV4      int
	BaselineV6      int
	CurrentV4       int
	CurrentV6       int
	DurationSeconds int64 // only set for PrefixAlertRecovered
}

// EvaluateUserPrefixAlert compares (currentV4, currentV6) against the user's
// stored alert-state row and updates it, returning a transition if this
// measurement just crossed into, or back out of, a drop — or nil if nothing
// changed (steady normal, or still mid-drop; this never re-fires between the
// two transitions, to avoid notifying on every single periodic check while a
// user stays down).
//
// A "drop" fires when the new total (v4+v6) falls to dropThresholdPercent%
// or more below the baseline, the baseline total is positive, and it's at
// least baselineMinimum — below that floor, a percentage swing isn't
// meaningful (a user with 2 prefixes going to 1 is a 50% "drop" that is, in
// practice, just noise). The baseline must be positive regardless of how
// low baselineMinimum itself is set (0 is a valid, accepted setting value):
// a user who has always had zero prefixes and still has zero cannot
// meaningfully "drop" — without this, a zero baseline and a zero
// baselineMinimum both being accepted as satisfying "baseline >= minimum"
// would fire a drop for a user who never had anything to lose. While a drop is active, the baseline is held at its pre-drop
// value rather than tracking the still-low current count, so "recovered"
// is measured against what was normal before the incident, not against
// whatever the user happens to be at right now. Outside of an active drop,
// the baseline continuously tracks the latest observation — this models a
// *sudden* drop between two consecutive checks, which is what "lost 90% of
// its prefixes at 03:14" describes, not a slow decline compared to some
// distant high-water mark.
//
// EvaluateUserPrefixAlert always runs this state machine, regardless of
// whether a webhook is even configured — it has to, so the baseline stays
// current and a drop that happens while alerting is disabled (or briefly
// unreachable) is still recorded as having happened. What it does NOT do on
// its own is guarantee delivery: a drop this call returns may still go
// un-POSTed (no webhook configured yet, or the request failed), which is
// why it separately tracks drop_delivered — see PendingPrefixAlertDrop for
// the retry half of that contract.
func (s *Store) EvaluateUserPrefixAlert(ctx context.Context, userID int64, currentV4, currentV6, dropThresholdPercent, baselineMinimum int) (*PrefixAlertTransition, error) {
	var baselineV4, baselineV6 int
	var alertingSince sql.NullInt64
	var dropDelivered bool
	err := s.DB.QueryRowContext(ctx,
		"SELECT baseline_v4, baseline_v6, alerting_since, drop_delivered FROM user_prefix_alert_state WHERE user_id = ?", userID).
		Scan(&baselineV4, &baselineV6, &alertingSince, &dropDelivered)
	if err == sql.ErrNoRows {
		// First-ever observation for this user: nothing to compare against
		// yet, so just establish the baseline.
		_, err := s.DB.ExecContext(ctx,
			"INSERT INTO user_prefix_alert_state(user_id, baseline_v4, baseline_v6, alerting_since, drop_delivered) VALUES (?, ?, ?, NULL, 1)",
			userID, currentV4, currentV6)
		return nil, err
	}
	if err != nil {
		return nil, err
	}

	baselineTotal := baselineV4 + baselineV6
	currentTotal := currentV4 + currentV6
	dropLine := baselineTotal * (100 - dropThresholdPercent) / 100

	if !alertingSince.Valid {
		// Currently normal.
		if baselineTotal > 0 && baselineTotal >= baselineMinimum && currentTotal <= dropLine {
			now := time.Now().UTC().Unix()
			if _, err := s.DB.ExecContext(ctx,
				"UPDATE user_prefix_alert_state SET alerting_since = ?, drop_delivered = 0 WHERE user_id = ?", now, userID); err != nil {
				return nil, err
			}
			return &PrefixAlertTransition{
				Event: PrefixAlertDrop, DetectedAt: now,
				BaselineV4: baselineV4, BaselineV6: baselineV6,
				CurrentV4: currentV4, CurrentV6: currentV6,
			}, nil
		}
		// Still normal: keep the baseline current.
		_, err := s.DB.ExecContext(ctx,
			"UPDATE user_prefix_alert_state SET baseline_v4 = ?, baseline_v6 = ? WHERE user_id = ?",
			currentV4, currentV6, userID)
		return nil, err
	}

	// Currently in a drop episode. Strictly above dropLine, not >=: the drop
	// condition above fires at currentTotal <= dropLine, so a count parked
	// exactly on the line would otherwise satisfy both predicates — flipping
	// alerting_since to NULL and back to set on every single tick, for a
	// persistent outage that never actually recovered.
	if currentTotal > dropLine {
		if _, err := s.DB.ExecContext(ctx,
			"UPDATE user_prefix_alert_state SET baseline_v4 = ?, baseline_v6 = ?, alerting_since = NULL, drop_delivered = 1 WHERE user_id = ?",
			currentV4, currentV6, userID); err != nil {
			return nil, err
		}
		if !dropDelivered {
			// This episode's drop was never actually announced (alerting
			// was disabled, or every delivery attempt failed, for its
			// entire duration) — reporting "recovered" for an incident
			// nobody was ever told about is more confusing than useful, so
			// this one is silently dropped rather than delivered.
			return nil, nil
		}
		now := time.Now().UTC().Unix()
		return &PrefixAlertTransition{
			Event: PrefixAlertRecovered, DetectedAt: now,
			BaselineV4: baselineV4, BaselineV6: baselineV6,
			CurrentV4: currentV4, CurrentV6: currentV6,
			DurationSeconds: now - alertingSince.Int64,
		}, nil
	}
	return nil, nil
}

// PendingPrefixAlertDrop reports an ongoing drop episode whose webhook was
// never successfully delivered — because alerting was disabled or
// unreachable when EvaluateUserPrefixAlert first detected it, or a prior
// delivery attempt simply failed — so the caller can retry it. currentV4/V6
// should be this same tick's freshly measured count (not whatever was
// current when the drop was first detected), so a retried delivery reflects
// the latest severity rather than a stale snapshot. Returns nil if there is
// no such pending drop (not currently alerting, or already delivered).
func (s *Store) PendingPrefixAlertDrop(ctx context.Context, userID int64, currentV4, currentV6 int) (*PrefixAlertTransition, error) {
	var baselineV4, baselineV6 int
	var alertingSince sql.NullInt64
	var dropDelivered bool
	err := s.DB.QueryRowContext(ctx,
		"SELECT baseline_v4, baseline_v6, alerting_since, drop_delivered FROM user_prefix_alert_state WHERE user_id = ?", userID).
		Scan(&baselineV4, &baselineV6, &alertingSince, &dropDelivered)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !alertingSince.Valid || dropDelivered {
		return nil, nil
	}
	return &PrefixAlertTransition{
		Event: PrefixAlertDrop, DetectedAt: alertingSince.Int64,
		BaselineV4: baselineV4, BaselineV6: baselineV6,
		CurrentV4: currentV4, CurrentV6: currentV6,
	}, nil
}

// PrefixAlertDropStillPending reports whether a user's alert state still
// describes an active, undelivered drop, without needing a fresh
// measurement — just whether EvaluateUserPrefixAlert has moved them out of
// that state since. Used to revalidate a drop immediately before actually
// posting it: internal/alerts' delivery can be queued behind a slow or
// backlogged batch, long enough for the episode it describes to have
// already resolved in the meantime (a later tick's own fresh evaluation),
// and sending it anyway would tell the receiver an outage is still ongoing
// when it no longer is.
func (s *Store) PrefixAlertDropStillPending(ctx context.Context, userID int64) (bool, error) {
	var alertingSince sql.NullInt64
	var dropDelivered bool
	err := s.DB.QueryRowContext(ctx,
		"SELECT alerting_since, drop_delivered FROM user_prefix_alert_state WHERE user_id = ?", userID).
		Scan(&alertingSince, &dropDelivered)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return alertingSince.Valid && !dropDelivered, nil
}

// MarkPrefixAlertDropDelivered records that the current drop episode's
// webhook was successfully delivered, so EvaluateUserPrefixAlert's eventual
// recovery for it is reported normally instead of silently cancelled, and
// PendingPrefixAlertDrop stops offering it for retry.
func (s *Store) MarkPrefixAlertDropDelivered(ctx context.Context, userID int64) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE user_prefix_alert_state SET drop_delivered = 1 WHERE user_id = ?", userID)
	return err
}
