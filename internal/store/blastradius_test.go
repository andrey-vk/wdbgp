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

// TestPreviewUserRouteFilterChange checks the per-user filter preview
// reports that one user's own before/after prefix counts and nothing
// else, and doesn't persist the trial filters.
func TestPreviewUserRouteFilterChange(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	userID := addBlastRadiusTestUser(t, s, DefaultCatalogModeID, FilterModeOverride, "cat-a", 1)

	preview, err := s.PreviewUserRouteFilterChange(ctx, userID, RouteFilters{Deny: []string{"21.0.0.0/8"}})
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

	preview, err := s.PreviewModeFeedChange(ctx, modeAID, nil) // remove all feeds from mode A
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

	// Mode A's real feed membership must be untouched.
	v4, _, err := s.CountSelectionPrefixes(ctx, userOnA)
	if err != nil {
		t.Fatal(err)
	}
	if v4 != 1 {
		t.Fatalf("user's real prefix count = %d after a preview, want unchanged 1", v4)
	}
}

// TestPreviewUserModeMove checks that moving a user to a mode they've
// never used reports a drop to zero (selections are mode-scoped, so the
// destination mode's selection starts empty), without persisting the move.
func TestPreviewUserModeMove(t *testing.T) {
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

	preview, err := s.PreviewUserModeMove(ctx, userID, modeBID)
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
