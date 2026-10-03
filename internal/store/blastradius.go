package store

import (
	"context"
	"database/sql"
	"errors"
)

// AffectedUser is one user's prefix-count impact from a previewed change.
type AffectedUser struct {
	UserID     int64  `json:"user_id"`
	Name       string `json:"name"`
	BeforeV4   int    `json:"before_v4"`
	BeforeV6   int    `json:"before_v6"`
	AfterV4    int    `json:"after_v4"`
	AfterV6    int    `json:"after_v6"`
	LostRoutes bool   `json:"lost_routes"` // after < before on either family
}

// BlastRadiusPreview reports a pending change's impact without applying
// it: which users are affected, how their prefix counts would move, and
// the aggregate delta across all of them.
type BlastRadiusPreview struct {
	AffectedUsers []AffectedUser `json:"affected_users"`
	TotalDeltaV4  int            `json:"total_delta_v4"`
	TotalDeltaV6  int            `json:"total_delta_v6"`
}

// errBlastRadiusDiscard aborts the preview transaction so its trial
// mutation is rolled back — the shared primitive issue #49 item #3 asks
// for, generalizing communities.go's PreviewCommunityReset/
// errResetPreviewDiscard beyond just community resets. Deliberately
// worded to avoid the substrings retry.TransientError matches on, so the
// aborted transaction is never retried.
var errBlastRadiusDiscard = errors.New("blast radius preview: discard trial")

// previewBlastRadius runs mutate for real inside a transaction, measuring
// each of users' CountSelectionPrefixes-style prefix counts before and
// after via countSelectionPrefixesTx, then unconditionally rolls the
// transaction back. The before/after set of users is read once, up front
// (not recomputed after mutate) — none of this package's preview callers
// change who is affected, only what those already-identified users would
// see, so recomputing membership after the trial mutation would only risk
// the two reads disagreeing on counts for a user whose membership itself
// briefly changed and changed back.
//
// This guarantees the preview always reflects exactly what mutate would
// really do — never a parallel calculation that could drift from the
// mutation's own logic.
func (s *Store) previewBlastRadius(ctx context.Context, users []User, mutate func(ctx context.Context, tx *sql.Tx) error) (BlastRadiusPreview, error) {
	var preview BlastRadiusPreview
	err := s.Transaction(ctx, func(tx *sql.Tx) error {
		// Attempt-local: Store.Transaction may retry this closure on a
		// transient error, and a retry must start the preview from
		// scratch, not accumulate onto a prior attempt's partial result.
		preview = BlastRadiusPreview{}

		before := make(map[int64][2]int, len(users))
		for _, u := range users {
			v4, v6, err := countSelectionPrefixesTx(ctx, tx, u.ID)
			if err != nil {
				return err
			}
			before[u.ID] = [2]int{v4, v6}
		}

		if err := mutate(ctx, tx); err != nil {
			return err
		}

		for _, u := range users {
			v4, v6, err := countSelectionPrefixesTx(ctx, tx, u.ID)
			if err != nil {
				return err
			}
			b := before[u.ID]
			preview.AffectedUsers = append(preview.AffectedUsers, AffectedUser{
				UserID: u.ID, Name: u.Name,
				BeforeV4: b[0], BeforeV6: b[1], AfterV4: v4, AfterV6: v6,
				LostRoutes: v4 < b[0] || v6 < b[1],
			})
			preview.TotalDeltaV4 += v4 - b[0]
			preview.TotalDeltaV6 += v6 - b[1]
		}
		return errBlastRadiusDiscard
	})
	if errors.Is(err, errBlastRadiusDiscard) {
		return preview, nil
	}
	if err != nil {
		return BlastRadiusPreview{}, err
	}
	// mutate succeeded and the transaction returned nil instead of the
	// discard sentinel — the trial would have committed for real. Treat
	// as a bug, the same way PreviewCommunityReset treats its own
	// unexpectedly-committed trial.
	return BlastRadiusPreview{}, errors.New("blast radius preview committed unexpectedly")
}

// usersByCatalogMode returns every user currently on modeID, for a mode
// feed-membership change preview.
func usersByCatalogMode(ctx context.Context, q queryer, modeID int64) ([]User, error) {
	return queryUsersWhere(ctx, q, "WHERE catalog_mode_id = ?", modeID)
}

// usersAffectedByGlobalFilter returns every user whose effective route
// filtering actually uses the global filter_allow/filter_deny value —
// filter_mode "global" (global only) or "extend" (global merged with
// their own). A user in "override" mode ignores the global filter
// entirely and is structurally unaffected by a change to it.
func usersAffectedByGlobalFilter(ctx context.Context, q queryer) ([]User, error) {
	return queryUsersWhere(ctx, q, "WHERE filter_mode != ?", filterModeToInt(FilterModeOverride))
}

// queryUsersWhere is Users' queryer-parameterized, WHERE-clause-scoped
// twin — it skips the UserNetworks follow-up query Users() does, since
// none of this package's preview callers need a user's networks.
func queryUsersWhere(ctx context.Context, q queryer, whereClause string, args ...any) ([]User, error) {
	rows, err := q.QueryContext(ctx, "SELECT "+userSelectColumns+" FROM users "+whereClause+" ORDER BY id", args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck
	var users []User
	for rows.Next() {
		user, err := scanUserRow(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

// PreviewGlobalRouteFilterChange reports the blast radius of setting the
// global filter_allow/filter_deny to newAllow/newDeny, without persisting
// anything. Writes app_settings directly via tx rather than going through
// settings.Setting.SetTx — the Store package doesn't hold a reference to
// settings.Settings (that dependency runs the other way), and a preview
// that always rolls back has no use for SetTx's commit func (which exists
// to update the in-memory cached value once a real apply durably
// commits — this trial never does).
func (s *Store) PreviewGlobalRouteFilterChange(ctx context.Context, newAllow, newDeny string) (BlastRadiusPreview, error) {
	users, err := usersAffectedByGlobalFilter(ctx, s.DB)
	if err != nil {
		return BlastRadiusPreview{}, err
	}
	return s.previewBlastRadius(ctx, users, func(ctx context.Context, tx *sql.Tx) error {
		if err := s.SaveSettingTx(ctx, tx, "filter_allow", newAllow); err != nil {
			return err
		}
		return s.SaveSettingTx(ctx, tx, "filter_deny", newDeny)
	})
}

// PreviewUserRouteFilterChange reports the blast radius of replacing
// userID's own route filters with filters, without persisting anything.
func (s *Store) PreviewUserRouteFilterChange(ctx context.Context, userID int64, filters RouteFilters) (BlastRadiusPreview, error) {
	user, err := s.User(ctx, userID)
	if err != nil {
		return BlastRadiusPreview{}, err
	}
	return s.previewBlastRadius(ctx, []User{user}, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM user_route_filters WHERE user_id = ?", userID); err != nil {
			return err
		}
		return insertRouteFilters(ctx, tx, userID, filters)
	})
}

// PreviewModeFeedChange reports the blast radius of replacing modeID's
// feed membership with links, without persisting anything.
func (s *Store) PreviewModeFeedChange(ctx context.Context, modeID int64, links []ModeFeedLink) (BlastRadiusPreview, error) {
	users, err := usersByCatalogMode(ctx, s.DB, modeID)
	if err != nil {
		return BlastRadiusPreview{}, err
	}
	return s.previewBlastRadius(ctx, users, func(ctx context.Context, tx *sql.Tx) error {
		return replaceModeFeedsTx(ctx, tx, modeID, links)
	})
}

// PreviewUserModeMove reports the blast radius of moving userID to
// newModeID, without persisting anything. Before/after naturally differ
// because selections are already mode-scoped (selected_categories/
// selected_services keyed by (user_id, mode_id)) — "after" reflects
// whatever userID already has saved for newModeID (zero if they've never
// used it), with no extra logic needed to carry anything over.
//
// mutate is a plain UPDATE, deliberately not SetUserCatalogModeTx — that
// helper's enabled-mode gate (and sql.ErrNoRows on a miss) exists for the
// end-user self-service switch-mode flow, and the real admin save this
// previews (apiUsersUpdate -> Store.UpdateUser) applies no such gate at
// the database layer at all, validating the target mode in the handler
// instead. Gating here too would make an admin's already-validated,
// disabled-or-nonexistent-mode case surface as a misleading
// store.IsNotFound "user not found" instead of the handler's real 400.
func (s *Store) PreviewUserModeMove(ctx context.Context, userID, newModeID int64) (BlastRadiusPreview, error) {
	user, err := s.User(ctx, userID)
	if err != nil {
		return BlastRadiusPreview{}, err
	}
	return s.previewBlastRadius(ctx, []User{user}, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE users SET catalog_mode_id = ? WHERE id = ?", newModeID, userID)
		return err
	})
}
