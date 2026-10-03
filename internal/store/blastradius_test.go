package store

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

// addBlastRadiusTestUser creates a user on modeID with the given filter
// mode, selecting categoryName (via a freshly created, linked feed
// contributing one /8), and returns the user ID. n keeps each test user's
// peer_ip/networks/ASN unique within one test.
func addBlastRadiusTestUser(t *testing.T, s *Store, modeID int64, filterMode, categoryName string, n uint32) int64 {
	t.Helper()
	ctx := context.Background()

	feedID, err := s.AddFeed(ctx, categoryName+"-feed", fmt.Sprintf("https://example.test/%s.json", categoryName), 1, true, 0, "", "", true)
	if err != nil {
		t.Fatalf("add feed: %v", err)
	}
	if _, err := s.DB.ExecContext(ctx, "INSERT INTO catalog_mode_feeds(mode_id, feed_id) VALUES (?, ?)", modeID, feedID); err != nil {
		t.Fatalf("link feed to mode: %v", err)
	}
	// 20.0.0.0/8 and up: deliberately NOT one of the bogon/private ranges
	// the product's default global filter_deny blocks (10/8, 127/8,
	// 172.16/12, 192.168/16, etc.) — using one of those here would make
	// every global-mode user's prefix silently disappear behind the
	// DEFAULT deny list before this test's own preview even runs.
	if err := s.InsertCatalogEntries(ctx, feedID, []CatalogEntry{
		{Category: categoryName, Service: "svc", CIDR: fmt.Sprintf("%d.0.0.0/8", 20+n)},
	}); err != nil {
		t.Fatalf("insert catalog: %v", err)
	}

	userID, err := s.AddUser(ctx, User{
		Name:          fmt.Sprintf("%s-user-%d", categoryName, n),
		PeerIP:        fmt.Sprintf("172.16.%d.1", n),
		PeerASN:       65000 + n,
		Enabled:       true,
		FilterMode:    filterMode,
		CatalogModeID: modeID,
		Networks:      []string{fmt.Sprintf("192.168.%d.0/24", n)},
	})
	if err != nil {
		t.Fatalf("add user: %v", err)
	}
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		return SetUserModeSelection(ctx, tx, userID, modeID, []string{categoryName}, nil)
	}); err != nil {
		t.Fatalf("select category: %v", err)
	}
	return userID
}

// TestPreviewBlastRadiusNeverPersists checks the core guarantee of the
// shared primitive: a preview call never changes persisted state, however
// large an impact it reports.
func TestPreviewBlastRadiusNeverPersists(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	userID := addBlastRadiusTestUser(t, s, DefaultCatalogModeID, FilterModeGlobal, "cat-a", 1)

	beforeFilters, err := s.GlobalRouteFilters(ctx)
	if err != nil {
		t.Fatal(err)
	}

	preview, err := s.PreviewGlobalRouteFilterChange(ctx, "", "21.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.AffectedUsers) != 1 || !preview.AffectedUsers[0].LostRoutes {
		t.Fatalf("preview = %+v, want 1 affected user that loses routes", preview)
	}

	afterFilters, err := s.GlobalRouteFilters(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(afterFilters.Deny) != len(beforeFilters.Deny) {
		t.Fatalf("GlobalRouteFilters changed after a preview: before=%+v after=%+v", beforeFilters, afterFilters)
	}
	v4, _, err := s.CountSelectionPrefixes(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if v4 != 1 {
		t.Fatalf("user's real prefix count = %d after a preview, want unchanged 1", v4)
	}
}

// TestPreviewGlobalRouteFilterChangeExcludesOverrideUsers checks that a
// user whose filter_mode is "override" — ignoring the global filter
// entirely — is excluded from a global filter preview's affected-user
// set, while "global" and "extend" users are included.
func TestPreviewGlobalRouteFilterChangeExcludesOverrideUsers(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	globalUser := addBlastRadiusTestUser(t, s, DefaultCatalogModeID, FilterModeGlobal, "cat-global", 1)
	extendUser := addBlastRadiusTestUser(t, s, DefaultCatalogModeID, FilterModeExtend, "cat-extend", 2)
	overrideUser := addBlastRadiusTestUser(t, s, DefaultCatalogModeID, FilterModeOverride, "cat-override", 3)

	preview, err := s.PreviewGlobalRouteFilterChange(ctx, "10.0.0.0/8", "")
	if err != nil {
		t.Fatal(err)
	}

	affected := make(map[int64]bool, len(preview.AffectedUsers))
	for _, a := range preview.AffectedUsers {
		affected[a.UserID] = true
	}
	if !affected[globalUser] {
		t.Fatalf("global-mode user %d missing from AffectedUsers: %+v", globalUser, preview.AffectedUsers)
	}
	if !affected[extendUser] {
		t.Fatalf("extend-mode user %d missing from AffectedUsers: %+v", extendUser, preview.AffectedUsers)
	}
	if affected[overrideUser] {
		t.Fatalf("override-mode user %d unexpectedly in AffectedUsers: %+v", overrideUser, preview.AffectedUsers)
	}
}

// TestPreviewUserEditFilterChange checks the combined user-edit preview
// reports one user's own before/after prefix counts and nothing else when
// only the route filters change, and doesn't persist the trial filters.
func TestPreviewUserEditFilterChange(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	userID := addBlastRadiusTestUser(t, s, DefaultCatalogModeID, FilterModeOverride, "cat-a", 1)

	preview, err := s.PreviewUserEdit(ctx, userID, true, FilterModeOverride, false, RouteFilters{Deny: []string{"21.0.0.0/8"}}, DefaultCatalogModeID)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.AffectedUsers) != 1 {
		t.Fatalf("AffectedUsers = %+v, want exactly 1", preview.AffectedUsers)
	}
	a := preview.AffectedUsers[0]
	if a.UserID != userID || a.BeforeV4 != 1 || a.AfterV4 != 0 || !a.LostRoutes {
		t.Fatalf("entry = %+v, want user %d: 1 -> 0, LostRoutes true", a, userID)
	}

	persisted, err := s.UserRouteFilters(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.Deny) != 0 {
		t.Fatalf("UserRouteFilters changed after a preview: %+v", persisted)
	}
}

// TestPreviewModeFeedChange checks that removing a mode's only feed
// reports every user on that mode losing their routes, and a user on a
// different mode is excluded.
func TestPreviewModeFeedChange(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	modeAID, err := s.AddCatalogMode(ctx, "Mode A", true)
	if err != nil {
		t.Fatal(err)
	}
	modeBID, err := s.AddCatalogMode(ctx, "Mode B", true)
	if err != nil {
		t.Fatal(err)
	}
	userOnA := addBlastRadiusTestUser(t, s, modeAID, FilterModeGlobal, "cat-a", 1)
	_ = addBlastRadiusTestUser(t, s, modeBID, FilterModeGlobal, "cat-b", 2)

	preview, err := s.PreviewModeFeedChange(ctx, modeAID, nil, true) // remove all feeds from mode A, stays enabled
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.AffectedUsers) != 1 {
		t.Fatalf("AffectedUsers = %+v, want exactly 1 (only mode A's user)", preview.AffectedUsers)
	}
	a := preview.AffectedUsers[0]
	if a.UserID != userOnA || a.BeforeV4 != 1 || a.AfterV4 != 0 || !a.LostRoutes {
		t.Fatalf("entry = %+v, want user %d: 1 -> 0, LostRoutes true", a, userOnA)
	}

	// Mode A's real feed membership and enabled flag must be untouched.
	v4, _, err := s.CountSelectionPrefixes(ctx, userOnA)
	if err != nil {
		t.Fatal(err)
	}
	if v4 != 1 {
		t.Fatalf("user's real prefix count = %d after a preview, want unchanged 1", v4)
	}
	modeA, err := s.CatalogMode(ctx, modeAID)
	if err != nil {
		t.Fatal(err)
	}
	if !modeA.Enabled {
		t.Fatalf("mode A enabled = false after a preview, want unchanged true")
	}
}

// TestPreviewModeFeedChangeIncludesEnabledTransition reproduces the gap
// Codex's review flagged: ModesPage.vue's Save button persists a mode's own
// enabled flag and its feed membership as two separate requests from one
// click, so previewing the feed side alone — leaving the mode's current
// enabled value untouched in the trial — would always measure 0 -> 0 for a
// disabled mode regardless of its feed membership, since
// countSelectionPrefixesTx's query requires catalog_modes.enabled = 1. The
// fix threads the target enabled value into the same trial transaction.
func TestPreviewModeFeedChangeIncludesEnabledTransition(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	modeID, err := s.AddCatalogMode(ctx, "Disabled Mode", false)
	if err != nil {
		t.Fatal(err)
	}
	userID := addBlastRadiusTestUser(t, s, modeID, FilterModeGlobal, "cat-a", 1)
	feeds, err := s.ModeFeeds(ctx, modeID)
	if err != nil {
		t.Fatal(err)
	}
	if len(feeds) != 1 {
		t.Fatalf("ModeFeeds = %+v, want exactly 1", feeds)
	}
	links := []ModeFeedLink{{FeedID: feeds[0].ID}}

	// Sanity: while the mode stays disabled, the preview must show no
	// impact regardless of feed membership — this is the old (buggy)
	// behavior, still correct for a mode that is genuinely staying disabled.
	staysDisabled, err := s.PreviewModeFeedChange(ctx, modeID, links, false)
	if err != nil {
		t.Fatal(err)
	}
	if staysDisabled.AffectedUsers[0].BeforeV4 != 0 || staysDisabled.AffectedUsers[0].AfterV4 != 0 {
		t.Fatalf("staysDisabled preview = %+v, want 0 -> 0", staysDisabled.AffectedUsers[0])
	}

	// The real bug: enabling the mode in the same save the feeds are kept
	// (unchanged) must show the user actually gaining routes, not 0 -> 0.
	becomesEnabled, err := s.PreviewModeFeedChange(ctx, modeID, links, true)
	if err != nil {
		t.Fatal(err)
	}
	a := becomesEnabled.AffectedUsers[0]
	if a.UserID != userID || a.BeforeV4 != 0 || a.AfterV4 != 1 {
		t.Fatalf("becomesEnabled preview = %+v, want user %d: 0 -> 1", a, userID)
	}

	// Nothing persisted by either preview.
	mode, err := s.CatalogMode(ctx, modeID)
	if err != nil {
		t.Fatal(err)
	}
	if mode.Enabled {
		t.Fatalf("mode enabled = true after previews, want unchanged false")
	}
}

// TestPreviewUserEditModeMove checks that moving a user to a mode they've
// never used (filters unchanged) reports a drop to zero (selections are
// mode-scoped, so the destination mode's selection starts empty), without
// persisting the move.
func TestPreviewUserEditModeMove(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	modeAID, err := s.AddCatalogMode(ctx, "Mode A", true)
	if err != nil {
		t.Fatal(err)
	}
	modeBID, err := s.AddCatalogMode(ctx, "Mode B", true)
	if err != nil {
		t.Fatal(err)
	}
	userID := addBlastRadiusTestUser(t, s, modeAID, FilterModeGlobal, "cat-a", 1)
	// Mode B needs its own catalog to exist, otherwise "after" is trivially
	// zero regardless of this test's point.
	feedID, err := s.AddFeed(ctx, "mode-b-feed", "https://example.test/modeb.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, "INSERT INTO catalog_mode_feeds(mode_id, feed_id) VALUES (?, ?)", modeBID, feedID); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertCatalogEntries(ctx, feedID, []CatalogEntry{
		{Category: "cat-a", Service: "svc", CIDR: "30.0.0.0/8"},
	}); err != nil {
		t.Fatal(err)
	}

	preview, err := s.PreviewUserEdit(ctx, userID, true, FilterModeGlobal, false, RouteFilters{}, modeBID)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.AffectedUsers) != 1 {
		t.Fatalf("AffectedUsers = %+v, want exactly 1", preview.AffectedUsers)
	}
	a := preview.AffectedUsers[0]
	if a.UserID != userID || a.BeforeV4 != 1 || a.AfterV4 != 0 || !a.LostRoutes {
		t.Fatalf("entry = %+v, want user %d: 1 -> 0 (no selection saved for mode B yet)", a, userID)
	}

	user, err := s.User(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if user.CatalogModeID != modeAID {
		t.Fatalf("CatalogModeID = %d after a preview, want unchanged %d", user.CatalogModeID, modeAID)
	}
}

// TestPreviewUserEditCombinedChangeCatchesWhatNeitherIsolatedChangeWould
// reproduces the exact gap Codex's review flagged: previewing a filter
// change and a mode move independently, each against the ORIGINAL state,
// can both report "no impact" even though applying them together — which
// is what the real admin save actually does in one PUT — does have one.
// The user here already has a saved selection on mode B from before this
// edit (selections are mode-scoped, so switching to a mode doesn't need a
// fresh selection); the new deny only targets mode B's route, so neither
// the filter-only nor the mode-only trial alone shows a count change, but
// the combination does.
func TestPreviewUserEditCombinedChangeCatchesWhatNeitherIsolatedChangeWould(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	modeAID, err := s.AddCatalogMode(ctx, "Mode A", true)
	if err != nil {
		t.Fatal(err)
	}
	modeBID, err := s.AddCatalogMode(ctx, "Mode B", true)
	if err != nil {
		t.Fatal(err)
	}
	// addBlastRadiusTestUser sets the user up on mode A, override filter
	// mode, with "cat-a" selected at 21.0.0.0/8 (n=1 -> 20+1).
	userID := addBlastRadiusTestUser(t, s, modeAID, FilterModeOverride, "cat-a", 1)

	feedID, err := s.AddFeed(ctx, "mode-b-feed", "https://example.test/modeb.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, "INSERT INTO catalog_mode_feeds(mode_id, feed_id) VALUES (?, ?)", modeBID, feedID); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertCatalogEntries(ctx, feedID, []CatalogEntry{
		{Category: "cat-b", Service: "svc", CIDR: "22.0.0.0/8"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		return SetUserModeSelection(ctx, tx, userID, modeBID, []string{"cat-b"}, nil)
	}); err != nil {
		t.Fatalf("pre-save mode B selection: %v", err)
	}

	filterOnly, err := s.PreviewUserEdit(ctx, userID, true, FilterModeOverride, false, RouteFilters{Deny: []string{"22.0.0.0/8"}}, modeAID)
	if err != nil {
		t.Fatal(err)
	}
	if filterOnly.AffectedUsers[0].BeforeV4 != 1 || filterOnly.AffectedUsers[0].AfterV4 != 1 {
		t.Fatalf("filter-only preview = %+v, want 1 -> 1 (deny targets mode B's route, user stays on mode A)", filterOnly.AffectedUsers[0])
	}

	modeOnly, err := s.PreviewUserEdit(ctx, userID, true, FilterModeOverride, false, RouteFilters{}, modeBID)
	if err != nil {
		t.Fatal(err)
	}
	if modeOnly.AffectedUsers[0].BeforeV4 != 1 || modeOnly.AffectedUsers[0].AfterV4 != 1 {
		t.Fatalf("mode-only preview = %+v, want 1 -> 1 (same count, different route, filters stay empty)", modeOnly.AffectedUsers[0])
	}

	combined, err := s.PreviewUserEdit(ctx, userID, true, FilterModeOverride, false, RouteFilters{Deny: []string{"22.0.0.0/8"}}, modeBID)
	if err != nil {
		t.Fatal(err)
	}
	c := combined.AffectedUsers[0]
	if c.BeforeV4 != 1 || c.AfterV4 != 0 || !c.LostRoutes {
		t.Fatalf("combined preview = %+v, want 1 -> 0, LostRoutes true — the impact neither isolated preview showed", c)
	}

	user, err := s.User(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if user.CatalogModeID != modeAID {
		t.Fatalf("CatalogModeID = %d after previews, want unchanged %d", user.CatalogModeID, modeAID)
	}
	filters, err := s.UserRouteFilters(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(filters.Deny) != 0 {
		t.Fatalf("UserRouteFilters changed after previews: %+v", filters)
	}
}

// TestPreviewUserEditDisabledUserAnnouncesNothing checks that a disabled
// user's routes are not counted (DesiredPrefixes announces nothing for
// u.enabled = 0), so disabling a user previews as losing their routes and
// enabling one previews as gaining them, not the other way around.
func TestPreviewUserEditDisabledUserAnnouncesNothing(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	userID := addBlastRadiusTestUser(t, s, DefaultCatalogModeID, FilterModeGlobal, "cat-a", 1)
	if _, err := s.DB.ExecContext(ctx, "UPDATE users SET enabled = 0 WHERE id = ?", userID); err != nil {
		t.Fatal(err)
	}

	enabling, err := s.PreviewUserEdit(ctx, userID, true, FilterModeGlobal, false, RouteFilters{}, DefaultCatalogModeID)
	if err != nil {
		t.Fatal(err)
	}
	a := enabling.AffectedUsers[0]
	if a.BeforeV4 != 0 || a.AfterV4 != 1 {
		t.Fatalf("enabling preview = %+v, want 0 -> 1", a)
	}

	disabling, err := s.PreviewUserEdit(ctx, userID, false, FilterModeGlobal, false, RouteFilters{}, DefaultCatalogModeID)
	if err != nil {
		t.Fatal(err)
	}
	if disabling.AffectedUsers[0].BeforeV4 != 0 || disabling.AffectedUsers[0].AfterV4 != 0 {
		t.Fatalf("disabled-to-disabled preview = %+v, want 0 -> 0", disabling.AffectedUsers[0])
	}
}

// TestUpdateUserWithRouteFiltersRollsBackRowOnFilterFailure checks that the
// admin user save commits the user row and its route filters together: a
// filter write that fails must not leave the mode change committed.
func TestUpdateUserWithRouteFiltersRollsBackRowOnFilterFailure(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	modeAID, err := s.AddCatalogMode(ctx, "Mode A", true)
	if err != nil {
		t.Fatal(err)
	}
	modeBID, err := s.AddCatalogMode(ctx, "Mode B", true)
	if err != nil {
		t.Fatal(err)
	}
	userID := addBlastRadiusTestUser(t, s, modeAID, FilterModeOverride, "cat-a", 1)
	user, err := s.User(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	user.CatalogModeID = modeBID
	user.FilterMode = FilterModeOverride

	_, err = s.UpdateUserWithRouteFilters(ctx, user, AuditMeta{}, AuditMeta{},
		&RouteFilters{Deny: []string{"not-a-cidr"}}, AuditMeta{})
	if err == nil {
		t.Fatal("UpdateUserWithRouteFilters with a malformed filter = nil error, want failure")
	}

	after, err := s.User(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if after.CatalogModeID != modeAID {
		t.Fatalf("CatalogModeID = %d after a failed combined save, want rolled back to %d", after.CatalogModeID, modeAID)
	}
}
