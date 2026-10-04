package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"
)

// openChangeLogStore opens a test store whose audit completeness boundary is
// at the epoch. The migration stamps the boundary with the time it ran, which
// in a fresh test database is now, while these tests place syncs in the past.
func openChangeLogStore(t *testing.T) *Store {
	t.Helper()
	s := openTestStore(t)
	if _, err := s.DB.ExecContext(context.Background(), "UPDATE audit_log_coverage SET complete_since = 0"); err != nil {
		t.Fatal(err)
	}
	return s
}

// selectionChangeLogUser returns a user in the default mode with no selection,
// so each test starts from a known empty state. Only one per test: the blast
// radius helper creates a feed by URL, so a second call would collide.
func selectionChangeLogUser(t *testing.T, s *Store) int64 {
	t.Helper()
	return addBlastRadiusTestUser(t, s, DefaultCatalogModeID, FilterModeGlobal, "log-seed", 1)
}

// plainChangeLogUser adds a user without creating a feed, for tests that
// already have one.
func plainChangeLogUser(t *testing.T, s *Store) int64 {
	t.Helper()
	id, err := s.AddUser(context.Background(), User{Name: "log-plain", PeerIP: "172.16.9.1", PeerASN: 65099, Enabled: true,
		FilterMode: FilterModeGlobal, CatalogModeID: DefaultCatalogModeID, Networks: []string{"192.168.9.0/24"}})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestUserChangeLogAttributesSelfAndAdminSelectionChanges(t *testing.T) {
	s := openChangeLogStore(t)
	ctx := context.Background()
	userID := selectionChangeLogUser(t, s)
	uid := strconv.FormatInt(userID, 10)

	if _, _, _, _, _, err := s.SaveUserSelectionCounts(ctx, userID, DefaultCatalogModeID, false,
		[]CategoryToggle{{Category: "ai", Checked: true}}, nil,
		AuditMeta{}, AuditMeta{Actor: "user:" + uid, Action: "user.selections_changed"}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, _, err := s.SaveUserSelectionCounts(ctx, userID, DefaultCatalogModeID, false,
		[]CategoryToggle{{Category: "ai", Checked: false}}, []ServiceToggle{{Category: "tv", Service: "c", Checked: true}},
		AuditMeta{}, AuditMeta{Actor: "admin:203.0.113.7", Action: "user.selections_changed"}); err != nil {
		t.Fatal(err)
	}

	entries, err := s.UserChangeLog(ctx, userID, time.Now(), userChangeLogWindow)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %+v, want 2 selection changes", entries)
	}
	admin, self := entries[0], entries[1]
	if admin.Source != "admin" || admin.Kind != "selections" {
		t.Fatalf("newest entry = %+v, want an admin selection change", admin)
	}
	if len(admin.Removed.Categories) != 1 || admin.Removed.Categories[0] != "ai" {
		t.Fatalf("admin removed categories = %v, want [ai]", admin.Removed.Categories)
	}
	if len(admin.Added.Services) != 1 || admin.Added.Services[0] != (ServiceKey{Category: "tv", Service: "c"}) {
		t.Fatalf("admin added services = %+v, want tv/c", admin.Added.Services)
	}
	if self.Source != "self" || len(self.Added.Categories) != 1 || self.Added.Categories[0] != "ai" {
		t.Fatalf("self entry = %+v, want self added ai", self)
	}
	if admin.Mode == "" {
		t.Fatal("selection entry has no mode name")
	}
}

func TestUserChangeLogShowsFeedChangesOnlyForSelectedCategoriesAndServices(t *testing.T) {
	s := openChangeLogStore(t)
	ctx := context.Background()
	// The user selects category ai, plus one specific service tv/unrelated in
	// a category they don't otherwise select. Relevance is judged against this
	// selection, so ai/* and tv/unrelated count and tv/other doesn't.
	userID, feedID := userSelectingFeedChange(t, s, "ai", false)
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		return SetUserModeSelection(ctx, tx, userID, DefaultCatalogModeID, []string{"ai"}, []ServiceKey{{Category: "tv", Service: "unrelated"}})
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		return RecordFeedSyncChangeTx(ctx, tx, feedID, FeedSyncDiff{
			AddedServices:      2,
			RemovedServices:    2,
			AddedByCategory:    map[string]int{"ai": 1},
			AddedServiceKeys:   []ServiceKey{{Category: "ai", Service: "new"}, {Category: "tv", Service: "other"}},
			RemovedServiceKeys: []ServiceKey{{Category: "ai", Service: "gone"}, {Category: "tv", Service: "unrelated"}},
		}, time.Now().Unix())
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		return RecordFeedSyncChangeTx(ctx, tx, feedID, FeedSyncDiff{
			RemovedServices:    1,
			RemovedServiceKeys: []ServiceKey{{Category: "tv", Service: "unrelated"}},
		}, time.Now().Unix()+1)
	}); err != nil {
		t.Fatal(err)
	}

	entries, err := s.UserChangeLog(ctx, userID, time.Now(), userChangeLogWindow)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %+v, want two feed sync entries", entries)
	}
	latest, first := entries[0], entries[1]
	if latest.Source != "feed_sync" || latest.FeedName != "ai-feed" {
		t.Fatalf("latest entry = %+v, want an ai-feed sync", latest)
	}
	if len(latest.Removed.Services) != 1 || latest.Removed.Services[0] != (ServiceKey{Category: "tv", Service: "unrelated"}) {
		t.Fatalf("latest removed services = %+v, want only tv/unrelated", latest.Removed.Services)
	}
	if len(latest.Added.Services) != 0 {
		t.Fatalf("latest added services = %+v, want none", latest.Added.Services)
	}
	if len(first.Added.Services) != 1 || first.Added.Services[0] != (ServiceKey{Category: "ai", Service: "new"}) {
		t.Fatalf("first added services = %+v, want only ai/new (tv/other is not selected)", first.Added.Services)
	}
	wantRemoved := []ServiceKey{{Category: "ai", Service: "gone"}, {Category: "tv", Service: "unrelated"}}
	if len(first.Removed.Services) != 2 || first.Removed.Services[0] != wantRemoved[0] || first.Removed.Services[1] != wantRemoved[1] {
		t.Fatalf("first removed services = %+v, want %+v", first.Removed.Services, wantRemoved)
	}
}

func TestUserChangeLogSkipsFeedChangesForUserWithoutSelection(t *testing.T) {
	s := openChangeLogStore(t)
	ctx := context.Background()
	_, feedID := userSelectingFeedChange(t, s, "ai", false)
	other := plainChangeLogUser(t, s)
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		return RecordFeedSyncChangeTx(ctx, tx, feedID, FeedSyncDiff{
			AddedServiceKeys: []ServiceKey{{Category: "ai", Service: "new"}},
		}, time.Now().Unix())
	}); err != nil {
		t.Fatal(err)
	}
	entries, err := s.UserChangeLog(ctx, other, time.Now(), userChangeLogWindow)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("user with no selection got %+v, want no feed entries", entries)
	}
}

func TestUserChangeLogReadsLegacyCountPayloadWithoutNames(t *testing.T) {
	s := openChangeLogStore(t)
	ctx := context.Background()
	userID := selectionChangeLogUser(t, s)
	if err := s.RecordAuditLog(ctx, AuditLogEntry{
		Actor: "user:" + strconv.FormatInt(userID, 10), Action: "user.selections_changed",
		ObjectType: "user", ObjectID: strconv.FormatInt(userID, 10),
		Before: `{"categories":0,"services":0}`, After: `{"categories":1,"services":2}`,
	}); err != nil {
		t.Fatal(err)
	}
	entries, err := s.UserChangeLog(ctx, userID, time.Now(), userChangeLogWindow)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Kind != "selections" || entries[0].Source != "self" {
		t.Fatalf("entries = %+v, want one self selection entry", entries)
	}
	if len(entries[0].Added.Categories) != 0 || len(entries[0].Added.Services) != 0 {
		t.Fatalf("legacy entry listed names: %+v", entries[0].Added)
	}
	if entries[0].Added.Categories == nil || entries[0].Removed.Routes == nil {
		t.Fatal("empty lists encoded as null instead of []")
	}
}

func TestUserChangeLogRouteAndModeAndFilterModeChanges(t *testing.T) {
	s := openChangeLogStore(t)
	ctx := context.Background()
	userID := selectionChangeLogUser(t, s)
	uid := strconv.FormatInt(userID, 10)
	if _, _, err := s.SetUserRouteFilters(ctx, userID, RouteFilters{Allow: []string{"10.0.0.0/8"}, Deny: []string{"192.168.0.0/16"}},
		AuditMeta{Actor: "user:" + uid, Action: "route_filters.user_updated"}); err != nil {
		t.Fatal(err)
	}
	for _, e := range []AuditLogEntry{
		{Action: "user.mode_changed", Before: `{"catalog_mode_id":1,"catalog_mode_name":"Default"}`, After: `{"catalog_mode_id":99,"catalog_mode_name":"Lab"}`},
		{Action: "user.filter_mode_changed", Before: `{"filter_mode":"global"}`, After: `{"filter_mode":"extend"}`},
	} {
		e.Actor = "admin:203.0.113.7"
		e.ObjectType = "user"
		e.ObjectID = uid
		if err := s.RecordAuditLog(ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	entries, err := s.UserChangeLog(ctx, userID, time.Now(), userChangeLogWindow)
	if err != nil {
		t.Fatal(err)
	}
	byKind := map[string]UserChangeEntry{}
	for _, e := range entries {
		byKind[e.Kind] = e
	}
	route := byKind["route_filters"]
	if route.Source != "self" || len(route.Added.Routes) != 2 || route.Added.Routes[0] != "allow 10.0.0.0/8" {
		t.Fatalf("route entry = %+v, want self added allow 10.0.0.0/8 and deny 192.168.0.0/16", route)
	}
	mode := byKind["mode"]
	if mode.Source != "admin" || mode.From != "Default" || mode.To != "Lab" {
		t.Fatalf("mode entry = %+v, want admin move from Default to Lab, names as recorded", mode)
	}
	fm := byKind["filter_mode"]
	if fm.From != "global" || fm.To != "extend" {
		t.Fatalf("filter_mode entry = %+v, want global -> extend", fm)
	}
}

func TestUserChangeLogExcludesOtherUsersAndOldRows(t *testing.T) {
	s := openChangeLogStore(t)
	ctx := context.Background()
	userID := selectionChangeLogUser(t, s)
	other := plainChangeLogUser(t, s)
	payload := `{"mode_id":1,"categories":["ai"],"services":[]}`
	if err := s.RecordAuditLog(ctx, AuditLogEntry{
		Actor: "admin:203.0.113.7", Action: "user.selections_changed", ObjectType: "user",
		ObjectID: strconv.FormatInt(other, 10), Before: `{}`, After: payload,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordAuditLog(ctx, AuditLogEntry{
		RecordedAt: time.Now().Add(-userChangeLogWindow - time.Hour),
		Actor:      "admin:203.0.113.7", Action: "user.selections_changed", ObjectType: "user",
		ObjectID: strconv.FormatInt(userID, 10), Before: `{}`, After: payload,
	}); err != nil {
		t.Fatal(err)
	}
	entries, err := s.UserChangeLog(ctx, userID, time.Now(), userChangeLogWindow)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries = %+v, want none: other user's row and a row past the window", entries)
	}
}

func TestCapChangeListCountsOmittedItems(t *testing.T) {
	services := make([]ServiceKey, userChangeLogListLimit+7)
	l := UserChangeList{Services: services}
	capChangeList(&l)
	if len(l.Services) != userChangeLogListLimit || l.Omitted != 7 {
		t.Fatalf("services = %d omitted = %d, want %d and 7", len(l.Services), l.Omitted, userChangeLogListLimit)
	}
	if l.Categories == nil || l.Routes == nil {
		t.Fatal("nil lists left nil; they must encode as []")
	}
}

func TestDiffCatalogEntriesNamesServicesSorted(t *testing.T) {
	prev := []CatalogEntry{{Category: "tv", Service: "z", CIDR: "10.1.0.0/24"}, {Category: "ai", Service: "b", CIDR: "10.2.0.0/24"}}
	next := []CatalogEntry{{Category: "ai", Service: "a", CIDR: "10.3.0.0/24"}, {Category: "ai", Service: "c", CIDR: "10.4.0.0/24"}}
	d := DiffCatalogEntries(prev, next)
	wantAdded := []ServiceKey{{Category: "ai", Service: "a"}, {Category: "ai", Service: "c"}}
	wantRemoved := []ServiceKey{{Category: "ai", Service: "b"}, {Category: "tv", Service: "z"}}
	if len(d.AddedServiceKeys) != 2 || d.AddedServiceKeys[0] != wantAdded[0] || d.AddedServiceKeys[1] != wantAdded[1] {
		t.Fatalf("AddedServiceKeys = %+v, want %+v", d.AddedServiceKeys, wantAdded)
	}
	if len(d.RemovedServiceKeys) != 2 || d.RemovedServiceKeys[0] != wantRemoved[0] || d.RemovedServiceKeys[1] != wantRemoved[1] {
		t.Fatalf("RemovedServiceKeys = %+v, want %+v", d.RemovedServiceKeys, wantRemoved)
	}
}

// historyFeed adds an enabled feed to the default mode, for tests that place
// syncs at chosen times.
func historyFeed(t *testing.T, s *Store) int64 {
	t.Helper()
	ctx := context.Background()
	feedID, err := s.AddFeed(ctx, "hist-feed", "https://example.test/hist-log.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx,
		"INSERT INTO catalog_mode_feeds(mode_id, feed_id, exclude) VALUES (?, ?, 0)", DefaultCatalogModeID, feedID); err != nil {
		t.Fatal(err)
	}
	return feedID
}

func recordHistorySync(t *testing.T, s *Store, feedID, at int64, added ...ServiceKey) {
	t.Helper()
	if err := s.Transaction(context.Background(), func(tx *sql.Tx) error {
		return RecordFeedSyncChangeTx(context.Background(), tx, feedID, FeedSyncDiff{
			AddedServices: len(added), AddedServiceKeys: added,
		}, at)
	}); err != nil {
		t.Fatal(err)
	}
}

// recordSelectionAudit writes a selection audit row at a chosen time, the way
// SaveUserSelectionCounts would, with the given before and after category lists.
func recordSelectionAudit(t *testing.T, s *Store, userID, at int64, before, after []string) {
	t.Helper()
	payload := func(cats []string) string {
		b, err := json.Marshal(selectionAuditPayload{ModeID: DefaultCatalogModeID, Categories: cats, Services: []ServiceKey{}})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if err := s.RecordAuditLog(context.Background(), AuditLogEntry{
		RecordedAt: time.Unix(at, 0), Actor: "admin:203.0.113.7", Action: "user.selections_changed",
		ObjectType: "user", ObjectID: strconv.FormatInt(userID, 10), Before: payload(before), After: payload(after),
	}); err != nil {
		t.Fatal(err)
	}
}

// feedSyncEntries keeps only the feed-sync entries, so tests about sync
// placement aren't muddied by the selection and mode rows that also appear.
func feedSyncEntries(all []UserChangeEntry) []UserChangeEntry {
	var out []UserChangeEntry
	for _, e := range all {
		if e.Kind == "feed_sync" {
			out = append(out, e)
		}
	}
	return out
}

func TestUserChangeLogUsesSelectionAtSyncTime(t *testing.T) {
	s := openChangeLogStore(t)
	ctx := context.Background()
	userID := plainChangeLogUser(t, s)
	feedID := historyFeed(t, s)
	t0 := time.Now().Add(-5 * time.Hour).Unix()

	// Selects ai at t0, drops it at t0+2000. The user never selects ai now.
	recordSelectionAudit(t, s, userID, t0, nil, []string{"ai"})
	recordSelectionAudit(t, s, userID, t0+2000, []string{"ai"}, nil)

	recordHistorySync(t, s, feedID, t0-100, ServiceKey{Category: "ai", Service: "before-selection"})
	recordHistorySync(t, s, feedID, t0+1000, ServiceKey{Category: "ai", Service: "while-selected"})
	recordHistorySync(t, s, feedID, t0+3000, ServiceKey{Category: "ai", Service: "after-dropped"})

	all, err := s.UserChangeLog(ctx, userID, time.Now(), userChangeLogWindow)
	if err != nil {
		t.Fatal(err)
	}
	entries := feedSyncEntries(all)
	if len(entries) != 1 {
		t.Fatalf("entries = %+v, want only the sync made while ai was selected", entries)
	}
	got := entries[0].Added.Services
	if len(got) != 1 || got[0] != (ServiceKey{Category: "ai", Service: "while-selected"}) {
		t.Fatalf("added services = %+v, want only ai/while-selected", got)
	}
}

func TestUserChangeLogLegacyRowsHideSyncsItCannotPlace(t *testing.T) {
	s := openChangeLogStore(t)
	ctx := context.Background()
	userID := plainChangeLogUser(t, s)
	feedID := historyFeed(t, s)
	t0 := time.Now().Add(-5 * time.Hour).Unix()

	// A pre-names row at t0 could have changed anything, so a sync before it
	// is unplaceable. The named row at t0+2000 records the state before it,
	// which does include ai, so a sync between the two is placeable.
	if err := s.RecordAuditLog(ctx, AuditLogEntry{
		RecordedAt: time.Unix(t0, 0), Actor: "admin:203.0.113.7", Action: "user.selections_changed",
		ObjectType: "user", ObjectID: strconv.FormatInt(userID, 10),
		Before: `{"categories":0,"services":0}`, After: `{"categories":1,"services":0}`,
	}); err != nil {
		t.Fatal(err)
	}
	recordSelectionAudit(t, s, userID, t0+2000, []string{"ai"}, nil)

	recordHistorySync(t, s, feedID, t0-100, ServiceKey{Category: "ai", Service: "unplaceable"})
	recordHistorySync(t, s, feedID, t0+1000, ServiceKey{Category: "ai", Service: "placeable"})

	all, err := s.UserChangeLog(ctx, userID, time.Now(), userChangeLogWindow)
	if err != nil {
		t.Fatal(err)
	}
	entries := feedSyncEntries(all)
	if len(entries) != 1 || len(entries[0].Added.Services) != 1 || entries[0].Added.Services[0].Service != "placeable" {
		t.Fatalf("entries = %+v, want only the placeable sync", entries)
	}
}

func TestUserChangeLogSkipsSyncsMadeInAnotherMode(t *testing.T) {
	s := openChangeLogStore(t)
	ctx := context.Background()
	userID := plainChangeLogUser(t, s)
	feedID := historyFeed(t, s)
	t0 := time.Now().Add(-5 * time.Hour).Unix()

	// The user has ai in the default mode now, but was in mode 2 until t0, so
	// a sync before t0 never reached them.
	if _, err := s.DB.ExecContext(ctx, "UPDATE users SET catalog_mode_id = ? WHERE id = ?", DefaultCatalogModeID, userID); err != nil {
		t.Fatal(err)
	}
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		return SetUserModeSelection(ctx, tx, userID, DefaultCatalogModeID, []string{"ai"}, nil)
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordAuditLog(ctx, AuditLogEntry{
		RecordedAt: time.Unix(t0, 0), Actor: "admin:203.0.113.7", Action: "user.mode_changed",
		ObjectType: "user", ObjectID: strconv.FormatInt(userID, 10),
		Before: `{"catalog_mode_id":2}`, After: `{"catalog_mode_id":1}`,
	}); err != nil {
		t.Fatal(err)
	}
	recordHistorySync(t, s, feedID, t0-100, ServiceKey{Category: "ai", Service: "old-mode"})
	recordHistorySync(t, s, feedID, t0+100, ServiceKey{Category: "ai", Service: "current-mode"})

	all, err := s.UserChangeLog(ctx, userID, time.Now(), userChangeLogWindow)
	if err != nil {
		t.Fatal(err)
	}
	entries := feedSyncEntries(all)
	if len(entries) != 1 || entries[0].Added.Services[0].Service != "current-mode" {
		t.Fatalf("entries = %+v, want only the sync made after the move", entries)
	}
}

func TestUserChangeLogSameSecondFollowsCommitOrder(t *testing.T) {
	s := openChangeLogStore(t)
	ctx := context.Background()
	t0 := time.Now().Add(-5 * time.Hour).Unix()

	// The selection is committed first, then the sync, both in one second. The
	// sync came after the selection, so it sees the selection and is listed.
	userID := plainChangeLogUser(t, s)
	feedID := historyFeed(t, s)
	recordSelectionAudit(t, s, userID, t0, nil, []string{"ai"})
	recordHistorySync(t, s, feedID, t0, ServiceKey{Category: "ai", Service: "after-selection"})
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		return SetUserModeSelection(ctx, tx, userID, DefaultCatalogModeID, []string{"ai"}, nil)
	}); err != nil {
		t.Fatal(err)
	}
	all, err := s.UserChangeLog(ctx, userID, time.Now(), userChangeLogWindow)
	if err != nil {
		t.Fatal(err)
	}
	if got := feedSyncEntries(all); len(got) != 1 {
		t.Fatalf("selected first, sync second: feed entries = %+v, want the sync listed", got)
	}

	// Reversed: the sync is committed first, then the selection in the same
	// second. The sync saw no selection, so it stays out.
	other, err := s.AddUser(ctx, User{Name: "log-other", PeerIP: "172.16.9.7", PeerASN: 65097, Enabled: true,
		FilterMode: FilterModeGlobal, CatalogModeID: DefaultCatalogModeID, Networks: []string{"192.168.7.0/24"}})
	if err != nil {
		t.Fatal(err)
	}
	recordHistorySync(t, s, feedID, t0+10, ServiceKey{Category: "ai", Service: "before-selection"})
	recordSelectionAudit(t, s, other, t0+10, nil, []string{"ai"})
	all, err = s.UserChangeLog(ctx, other, time.Now(), userChangeLogWindow)
	if err != nil {
		t.Fatal(err)
	}
	if got := feedSyncEntries(all); len(got) != 0 {
		t.Fatalf("sync first, selected second: feed entries = %+v, want none", got)
	}
}

func TestUserChangeLogPlacesSyncsByModesTheyReachedThen(t *testing.T) {
	s := openChangeLogStore(t)
	ctx := context.Background()
	userID := plainChangeLogUser(t, s)
	// The feed syncs while no mode includes it, then is attached to the user's
	// mode afterwards. The sync reached nobody, so it must not appear now.
	feedID, err := s.AddFeed(ctx, "late-feed", "https://example.test/late-log.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	recordHistorySync(t, s, feedID, time.Now().Add(-5*time.Hour).Unix(), ServiceKey{Category: "ai", Service: "unreached"})
	if _, err := s.DB.ExecContext(ctx,
		"INSERT INTO catalog_mode_feeds(mode_id, feed_id, exclude) VALUES (?, ?, 0)", DefaultCatalogModeID, feedID); err != nil {
		t.Fatal(err)
	}
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		return SetUserModeSelection(ctx, tx, userID, DefaultCatalogModeID, []string{"ai"}, nil)
	}); err != nil {
		t.Fatal(err)
	}

	all, err := s.UserChangeLog(ctx, userID, time.Now(), userChangeLogWindow)
	if err != nil {
		t.Fatal(err)
	}
	if got := feedSyncEntries(all); len(got) != 0 {
		t.Fatalf("feed entries = %+v, want none: the sync predates the feed's mode assignment", got)
	}
}

func TestUserChangeLogKeepsSyncsOfFeedDisabledLater(t *testing.T) {
	s := openChangeLogStore(t)
	ctx := context.Background()
	userID, feedID := userSelectingFeedChange(t, s, "ai", false)
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		return RecordFeedSyncChangeTx(ctx, tx, feedID, FeedSyncDiff{
			AddedServices: 1, AddedServiceKeys: []ServiceKey{{Category: "ai", Service: "kept"}},
		}, time.Now().Unix()-600)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, "UPDATE feeds SET enabled = 0 WHERE id = ?", feedID); err != nil {
		t.Fatal(err)
	}

	all, err := s.UserChangeLog(ctx, userID, time.Now(), userChangeLogWindow)
	if err != nil {
		t.Fatal(err)
	}
	got := feedSyncEntries(all)
	if len(got) != 1 || got[0].Added.Services[0].Service != "kept" {
		t.Fatalf("feed entries = %+v, want the earlier sync still listed after the feed was disabled", got)
	}
}

func TestUserChangeLogWindowFollowsAuditRetention(t *testing.T) {
	day := 24 * time.Hour
	if got := UserChangeLogWindow(90); got != userChangeLogWindow {
		t.Fatalf("window with long retention = %v, want the %v cap", got, userChangeLogWindow)
	}
	if got := UserChangeLogWindow(7); got != 7*day {
		t.Fatalf("window with 7-day retention = %v, want 7 days", got)
	}
	// A non-positive retention falls back to the 30-day default, as the purge does.
	if got := UserChangeLogWindow(0); got != userChangeLogWindow {
		t.Fatalf("window with zero retention = %v, want the %v default", got, userChangeLogWindow)
	}
	if got := UserChangeLogWindow(-3); got != userChangeLogWindow {
		t.Fatalf("window with negative retention = %v, want the %v default", got, userChangeLogWindow)
	}
	// A huge setting must not overflow the duration into a negative window.
	if got := UserChangeLogWindow(math.MaxInt); got != userChangeLogWindow {
		t.Fatalf("window with huge retention = %v, want the %v cap", got, userChangeLogWindow)
	}
}

// TestUserChangeLogKeepsModeNameAsRecorded checks the snapshot: a mode renamed
// after a sync is still shown under the name it had when the sync ran.
func TestUserChangeLogKeepsModeNameAsRecorded(t *testing.T) {
	s := openChangeLogStore(t)
	ctx := context.Background()
	userID, feedID := userSelectingFeedChange(t, s, "ai", false)
	var oldName string
	if err := s.DB.QueryRowContext(ctx, "SELECT name FROM catalog_modes WHERE id = ?", DefaultCatalogModeID).Scan(&oldName); err != nil {
		t.Fatal(err)
	}
	recordHistorySync(t, s, feedID, time.Now().Add(-time.Hour).Unix(), ServiceKey{Category: "ai", Service: "named"})
	if _, err := s.DB.ExecContext(ctx, "UPDATE catalog_modes SET name = 'Renamed' WHERE id = ?", DefaultCatalogModeID); err != nil {
		t.Fatal(err)
	}

	all, err := s.UserChangeLog(ctx, userID, time.Now(), userChangeLogWindow)
	if err != nil {
		t.Fatal(err)
	}
	got := feedSyncEntries(all)
	if len(got) != 1 || got[0].Mode != oldName {
		t.Fatalf("feed entries = %+v, want the mode shown as %q", got, oldName)
	}
}

// TestUserChangeLogKeepsFeedNameAsRecorded checks the feed snapshot: a feed
// renamed after a sync is still shown under the name it had then.
func TestUserChangeLogKeepsFeedNameAsRecorded(t *testing.T) {
	s := openChangeLogStore(t)
	ctx := context.Background()
	userID, feedID := userSelectingFeedChange(t, s, "ai", false)
	recordHistorySync(t, s, feedID, time.Now().Add(-time.Hour).Unix(), ServiceKey{Category: "ai", Service: "named"})
	if _, err := s.DB.ExecContext(ctx, "UPDATE feeds SET name = 'renamed-feed' WHERE id = ?", feedID); err != nil {
		t.Fatal(err)
	}

	all, err := s.UserChangeLog(ctx, userID, time.Now(), userChangeLogWindow)
	if err != nil {
		t.Fatal(err)
	}
	got := feedSyncEntries(all)
	if len(got) != 1 || got[0].FeedName != "ai-feed" {
		t.Fatalf("feed entries = %+v, want the feed shown as ai-feed, its name at sync time", got)
	}
}

// TestUserChangeLogKeepsSelectionsOfDeletedMode checks that a sync reaching a
// mode still shows after the mode is deleted, since the deletion audits the
// selections it cascades away.
func TestUserChangeLogKeepsSelectionsOfDeletedMode(t *testing.T) {
	s := openChangeLogStore(t)
	ctx := context.Background()
	modeID, err := s.AddCatalogMode(ctx, "Lab", true)
	if err != nil {
		t.Fatal(err)
	}
	userID := plainChangeLogUser(t, s)
	if _, err := s.DB.ExecContext(ctx, "UPDATE users SET catalog_mode_id = ? WHERE id = ?", modeID, userID); err != nil {
		t.Fatal(err)
	}
	feedID := historyFeed(t, s)
	if _, err := s.DB.ExecContext(ctx,
		"INSERT INTO catalog_mode_feeds(mode_id, feed_id, exclude) VALUES (?, ?, 0)", modeID, feedID); err != nil {
		t.Fatal(err)
	}
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		return SetUserModeSelection(ctx, tx, userID, modeID, []string{"ai"}, nil)
	}); err != nil {
		t.Fatal(err)
	}
	recordHistorySync(t, s, feedID, time.Now().Add(-time.Hour).Unix(), ServiceKey{Category: "ai", Service: "in-lab"})

	if _, err := s.DeleteCatalogMode(ctx, modeID, AuditMeta{Actor: "admin:203.0.113.7", UserAgent: "test", Action: "user.mode_changed"}); err != nil {
		t.Fatal(err)
	}

	all, err := s.UserChangeLog(ctx, userID, time.Now(), userChangeLogWindow)
	if err != nil {
		t.Fatal(err)
	}
	got := feedSyncEntries(all)
	if len(got) != 1 || got[0].Mode != "Lab" || len(got[0].Added.Services) != 1 {
		t.Fatalf("feed entries = %+v, want the sync shown in mode Lab after the mode was deleted", got)
	}
}

// TestUserChangeLogStopsAtPurgeBoundary checks that a purge moves the readable
// window forward for good: raising retention afterwards doesn't bring back
// the days that were already deleted, where a sync can't be placed.
func TestUserChangeLogStopsAtPurgeBoundary(t *testing.T) {
	s := openChangeLogStore(t)
	ctx := context.Background()
	userID, feedID := userSelectingFeedChange(t, s, "ai", false)
	old := time.Now().Add(-20 * 24 * time.Hour).Unix()
	recordHistorySync(t, s, feedID, old, ServiceKey{Category: "ai", Service: "twenty-days-ago"})

	before, err := s.UserChangeLog(ctx, userID, time.Now(), userChangeLogWindow)
	if err != nil {
		t.Fatal(err)
	}
	if len(feedSyncEntries(before)) != 1 {
		t.Fatalf("before purge: feed entries = %+v, want the sync listed", feedSyncEntries(before))
	}

	if err := s.PurgeAuditLog(ctx, 7); err != nil {
		t.Fatal(err)
	}
	// Retention is now 30 days, but the 20-day-old sync predates the purge.
	after, err := s.UserChangeLog(ctx, userID, time.Now(), UserChangeLogWindow(30))
	if err != nil {
		t.Fatal(err)
	}
	if got := feedSyncEntries(after); len(got) != 0 {
		t.Fatalf("after purge: feed entries = %+v, want none before the purge boundary", got)
	}
}

// TestUserChangeLogKeepsHistoryOfDeletedFeed checks that deleting a feed keeps
// its earlier syncs in the log, and records the services it took away.
func TestUserChangeLogKeepsHistoryOfDeletedFeed(t *testing.T) {
	s := openChangeLogStore(t)
	ctx := context.Background()
	userID, feedID := userSelectingFeedChange(t, s, "ai", false)
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		return ReplaceCatalogEntries(ctx, tx, feedID, []CatalogEntry{{Category: "ai", Service: "kept-service", CIDR: "20.0.0.0/24"}})
	}); err != nil {
		t.Fatal(err)
	}
	recordHistorySync(t, s, feedID, time.Now().Add(-2*time.Hour).Unix(), ServiceKey{Category: "ai", Service: "added-earlier"})

	if err := s.DeleteFeed(ctx, feedID, AuditMeta{Actor: "admin:203.0.113.7", UserAgent: "test"}); err != nil {
		t.Fatal(err)
	}

	all, err := s.UserChangeLog(ctx, userID, time.Now(), userChangeLogWindow)
	if err != nil {
		t.Fatal(err)
	}
	got := feedSyncEntries(all)
	if len(got) != 2 {
		t.Fatalf("feed entries = %+v, want the earlier sync and the deletion", got)
	}
	deletion, earlier := got[0], got[1]
	if deletion.FeedName != "ai-feed" || len(deletion.Removed.Services) != 1 || deletion.Removed.Services[0].Service != "kept-service" {
		t.Fatalf("deletion entry = %+v, want ai-feed removing kept-service", deletion)
	}
	if len(earlier.Added.Services) != 1 || earlier.Added.Services[0].Service != "added-earlier" {
		t.Fatalf("earlier entry = %+v, want added-earlier kept after the deletion", earlier)
	}
}

// TestPurgeAuditLogPrunesDeletedFeedHistory checks that history of a deleted
// feed is dropped once it is older than audit retention, and not before.
func TestPurgeAuditLogPrunesDeletedFeedHistory(t *testing.T) {
	s := openChangeLogStore(t)
	ctx := context.Background()
	_, feedID := userSelectingFeedChange(t, s, "ai", false)
	recordHistorySync(t, s, feedID, time.Now().Add(-20*24*time.Hour).Unix(), ServiceKey{Category: "ai", Service: "old"})
	recordHistorySync(t, s, feedID, time.Now().Add(-1*time.Hour).Unix(), ServiceKey{Category: "ai", Service: "recent"})
	if err := s.DeleteFeed(ctx, feedID, AuditMeta{Actor: "admin:203.0.113.7", UserAgent: "test"}); err != nil {
		t.Fatal(err)
	}

	countOrphans := func() int {
		var n int
		if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM feed_sync_changes WHERE feed_id IS NULL").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := countOrphans(); n != 3 {
		t.Fatalf("orphaned changes = %d, want the two syncs and the deletion", n)
	}
	if err := s.PurgeAuditLog(ctx, 7); err != nil {
		t.Fatal(err)
	}
	// The 20-day-old sync and the deletion record of the same feed, which both
	// predate the cutoff... the deletion is now, so only the old sync goes.
	if n := countOrphans(); n != 2 {
		t.Fatalf("orphaned changes after purge = %d, want 2 (the old sync is gone)", n)
	}
}

// TestDeleteFeedAuditsSelectionsInDetachedModes checks that a selection the
// feed's deletion prunes is audited even in a mode the feed was detached from
// earlier, since the pruning reaches every mode.
func TestDeleteFeedAuditsSelectionsInDetachedModes(t *testing.T) {
	s := openChangeLogStore(t)
	ctx := context.Background()
	labID, err := s.AddCatalogMode(ctx, "Detached", true)
	if err != nil {
		t.Fatal(err)
	}
	userID, feedID := userSelectingFeedChange(t, s, "ai", false)
	if _, err := s.DB.ExecContext(ctx,
		"INSERT INTO catalog_mode_feeds(mode_id, feed_id, exclude) VALUES (?, ?, 0)", labID, feedID); err != nil {
		t.Fatal(err)
	}
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		return SetUserModeSelection(ctx, tx, userID, labID, []string{"ai"}, nil)
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveFeedFromMode(ctx, labID, feedID); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteFeed(ctx, feedID, AuditMeta{Actor: "admin:203.0.113.7", UserAgent: "test"}); err != nil {
		t.Fatal(err)
	}

	var rows int
	if err := s.DB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM audit_log WHERE action = 'user.selections_changed' AND object_id = ? AND before LIKE ?",
		strconv.FormatInt(userID, 10), `%"mode_id":`+strconv.FormatInt(labID, 10)+`%`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("selection audit rows for the detached mode = %d, want 1", rows)
	}
}

// TestUserChangeLogDirectEntriesIgnoreCompleteBoundary checks that the
// completeness boundary only limits feed reconstruction: a direct audit entry
// still shows even when the boundary sits after it, as it does right after an
// upgrade.
func TestUserChangeLogDirectEntriesIgnoreCompleteBoundary(t *testing.T) {
	s := openChangeLogStore(t)
	ctx := context.Background()
	userID := plainChangeLogUser(t, s)
	recordSelectionAudit(t, s, userID, time.Now().Add(-time.Hour).Unix(), nil, []string{"ai"})
	if _, err := s.DB.ExecContext(ctx, "UPDATE audit_log_coverage SET complete_since = ?", time.Now().Unix()); err != nil {
		t.Fatal(err)
	}

	all, err := s.UserChangeLog(ctx, userID, time.Now(), userChangeLogWindow)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Kind != "selections" {
		t.Fatalf("entries = %+v, want the pre-boundary selection still listed", all)
	}
}

// TestUserChangeLogDoesNotInheritReusedUserID checks that a new user who gets a
// deleted user's ID doesn't see that predecessor's history.
func TestUserChangeLogDoesNotInheritReusedUserID(t *testing.T) {
	s := openChangeLogStore(t)
	ctx := context.Background()
	oldID := plainChangeLogUser(t, s)
	recordSelectionAudit(t, s, oldID, time.Now().Add(-2*time.Hour).Unix(), nil, []string{"ai"})
	if err := s.DeleteUser(ctx, oldID, AuditMeta{Actor: "admin:203.0.113.7", UserAgent: "test", Action: "user.deleted"}); err != nil {
		t.Fatal(err)
	}
	newID, err := s.AddUser(ctx, User{Name: "successor", PeerIP: "172.16.9.2", PeerASN: 65098, Enabled: true,
		FilterMode: FilterModeGlobal, CatalogModeID: DefaultCatalogModeID, Networks: []string{"192.168.8.0/24"}})
	if err != nil {
		t.Fatal(err)
	}
	if newID != oldID {
		t.Skipf("SQLite did not reuse the ID (%d -> %d); the scenario needs reuse", oldID, newID)
	}

	entries, err := s.UserChangeLog(ctx, newID, time.Now(), userChangeLogWindow)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("successor with reused ID %d inherited %+v", newID, entries)
	}
}

// TestUserChangeLogOrdersSameSecondByCommit checks the listing itself: entries
// in one second come back newest first by commit order, not by insertion into
// the response.
func TestUserChangeLogOrdersSameSecondByCommit(t *testing.T) {
	s := openChangeLogStore(t)
	ctx := context.Background()
	userID := plainChangeLogUser(t, s)
	feedID := historyFeed(t, s)
	t0 := time.Now().Add(-5 * time.Hour).Unix()

	recordSelectionAudit(t, s, userID, t0, nil, []string{"ai"})
	recordHistorySync(t, s, feedID, t0, ServiceKey{Category: "ai", Service: "middle"})
	recordSelectionAudit(t, s, userID, t0, []string{"ai"}, []string{"ai", "tv"})

	all, err := s.UserChangeLog(ctx, userID, time.Now(), userChangeLogWindow)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("entries = %+v, want three", all)
	}
	if all[0].Kind != "selections" || len(all[0].Added.Categories) != 1 || all[0].Added.Categories[0] != "tv" {
		t.Fatalf("newest = %+v, want the second selection (tv) first", all[0])
	}
	if all[1].Kind != "feed_sync" {
		t.Fatalf("second = %+v, want the sync between the two selections", all[1])
	}
	if all[2].Kind != "selections" || len(all[2].Added.Categories) != 1 || all[2].Added.Categories[0] != "ai" {
		t.Fatalf("oldest = %+v, want the first selection (ai) last", all[2])
	}
}

// TestDeleteDisabledFeedRecordsNoRemoval checks that deleting a feed that was
// already disabled doesn't date its services' removal to the deletion: they
// left the modes when it was disabled.
func TestDeleteDisabledFeedRecordsNoRemoval(t *testing.T) {
	s := openChangeLogStore(t)
	ctx := context.Background()
	userID, feedID := userSelectingFeedChange(t, s, "ai", false)
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		return ReplaceCatalogEntries(ctx, tx, feedID, []CatalogEntry{{Category: "ai", Service: "gone-earlier", CIDR: "20.0.0.0/24"}})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, "UPDATE feeds SET enabled = 0 WHERE id = ?", feedID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteFeed(ctx, feedID, AuditMeta{}); err != nil {
		t.Fatal(err)
	}
	all, err := s.UserChangeLog(ctx, userID, time.Now(), userChangeLogWindow)
	if err != nil {
		t.Fatal(err)
	}
	if got := feedSyncEntries(all); len(got) != 0 {
		t.Fatalf("feed entries = %+v, want none for a feed disabled before deletion", got)
	}
}

// TestSyncSequenceSurvivesAuditPurge checks that a sync gets a usable sequence
// after the old audit rows are purged: the sequence is the audit high-water
// mark, not the newest row still kept, so it stays after the account's floor.
func TestSyncSequenceSurvivesAuditPurge(t *testing.T) {
	s := openChangeLogStore(t)
	ctx := context.Background()
	userID, feedID := userSelectingFeedChange(t, s, "ai", false)
	recordSelectionAudit(t, s, userID, time.Now().Add(-40*24*time.Hour).Unix(), nil, []string{"ai"})
	if err := s.PurgeAuditLog(ctx, 30); err != nil {
		t.Fatal(err)
	}
	recordHistorySync(t, s, feedID, time.Now().Add(-time.Hour).Unix(), ServiceKey{Category: "ai", Service: "post-purge"})

	var seq sql.NullInt64
	if err := s.DB.QueryRowContext(ctx, "SELECT audit_seq FROM feed_sync_changes ORDER BY id DESC LIMIT 1").Scan(&seq); err != nil {
		t.Fatal(err)
	}
	if !seq.Valid || seq.Int64 <= 0 {
		t.Fatalf("audit_seq after purge = %+v, want the audit high-water mark, not 0", seq)
	}
}

// TestSelectionAuditValueBoundsLargeSelections checks that a selection too
// large to store by name is audited as counts, which the reader treats as
// unknown, rather than as a payload of every name.
func TestSelectionAuditValueBoundsLargeSelections(t *testing.T) {
	small := selectionAuditPayload{ModeID: 1, Categories: []string{"ai"}, Services: []ServiceKey{{Category: "ai", Service: "x"}}}
	if _, ok := selectionAuditValue(small).(selectionAuditPayload); !ok {
		t.Fatal("small selection not audited by name")
	}
	large := selectionAuditPayload{ModeID: 1, Services: make([]ServiceKey, maxSelectionAuditNames+1)}
	if _, ok := selectionAuditValue(large).(boundedSelectionPayload); !ok {
		t.Fatalf("large selection audited as %#v, want counts", selectionAuditValue(large))
	}
	// Same counts, different names: the audited values must still differ.
	other := selectionAuditPayload{ModeID: 1, Services: make([]ServiceKey, maxSelectionAuditNames+1)}
	other.Services[0] = ServiceKey{Category: "x", Service: "y"}
	if fmt.Sprint(selectionAuditValue(large)) == fmt.Sprint(selectionAuditValue(other)) {
		t.Fatal("two large selections with equal counts audited as the same value")
	}
}

// TestUserChangeLogScansManyCategories checks that a selection spanning more
// categories than one scan statement can bind still places a sync, and that the
// entry's services come back sorted.
func TestUserChangeLogScansManyCategories(t *testing.T) {
	s := openChangeLogStore(t)
	ctx := context.Background()
	userID, feedID := userSelectingFeedChange(t, s, "ai", false)
	cats := make([]string, 0, maxScanCategories+100)
	for i := 0; i < maxScanCategories+100; i++ {
		cats = append(cats, fmt.Sprintf("cat-%04d", i))
	}
	cats = append(cats, "ai")
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		return SetUserModeSelection(ctx, tx, userID, DefaultCatalogModeID, cats, nil)
	}); err != nil {
		t.Fatal(err)
	}
	recordHistorySync(t, s, feedID, time.Now().Add(-time.Hour).Unix(),
		ServiceKey{Category: "ai", Service: "zeta"}, ServiceKey{Category: "ai", Service: "alpha"})

	all, err := s.UserChangeLog(ctx, userID, time.Now(), userChangeLogWindow)
	if err != nil {
		t.Fatal(err)
	}
	got := feedSyncEntries(all)
	if len(got) != 1 || len(got[0].Added.Services) != 2 || got[0].Added.Services[0].Service != "alpha" {
		t.Fatalf("feed entries = %+v, want the sync placed with its services sorted", got)
	}
}

// TestRecordFeedSyncWritesEveryServiceAcrossBatches checks that a sync with more
// services than one batch holds still records each of them.
func TestRecordFeedSyncWritesEveryServiceAcrossBatches(t *testing.T) {
	s := openChangeLogStore(t)
	ctx := context.Background()
	feedID := historyFeed(t, s)
	n := feedSyncRowsPerInsert*2 + 50
	added := make([]ServiceKey, 0, n)
	for i := 0; i < n; i++ {
		added = append(added, ServiceKey{Category: "bulk", Service: fmt.Sprintf("svc-%04d", i)})
	}
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		return RecordFeedSyncChangeTx(ctx, tx, feedID, FeedSyncDiff{AddedServices: n, AddedServiceKeys: added}, time.Now().Unix())
	}); err != nil {
		t.Fatal(err)
	}
	var got int
	if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM feed_sync_change_services WHERE kind = 'added'").Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != n {
		t.Fatalf("recorded services = %d, want %d", got, n)
	}
}

// TestBoundedSelectionKeepsItsMode checks that an oversized selection's audit
// value still names its mode, so the reader can scope it to that mode.
func TestBoundedSelectionKeepsItsMode(t *testing.T) {
	large := selectionAuditPayload{ModeID: 7, ModeName: "Lab", Services: make([]ServiceKey, maxSelectionAuditNames+1)}
	raw, err := json.Marshal(selectionAuditValue(large))
	if err != nil {
		t.Fatal(err)
	}
	b, ok := decodeBoundedSelection(string(raw))
	if !ok || b.ModeID != 7 || b.ModeName != "Lab" {
		t.Fatalf("bounded value = %s, want mode 7 (Lab) kept", raw)
	}
}

// TestBoundedSelectionOfShrinkingChange checks that a change whose bounded side
// is the before value still finds its mode.
func TestBoundedSelectionOfShrinkingChange(t *testing.T) {
	bounded := `{"mode_id":7,"mode_name":"Lab","oversized":true,"categories":0,"services":1001,"digest":"x"}`
	small := `{"mode_id":7,"mode_name":"Lab","categories":[],"services":[]}`
	b, ok := boundedSelectionOf(bounded, small)
	if !ok || b.ModeID != 7 {
		t.Fatalf("shrinking change: bounded side not found (got %+v, %v)", b, ok)
	}
}

// TestSelectionAuditValueBoundsByBytes checks that one very long name bounds the
// row even though the selection has few entries.
func TestSelectionAuditValueBoundsByBytes(t *testing.T) {
	long := selectionAuditPayload{ModeID: 1, Services: []ServiceKey{{Category: "ai", Service: strings.Repeat("x", maxSelectionAuditBytes+1)}}}
	if _, ok := selectionAuditValue(long).(boundedSelectionPayload); !ok {
		t.Fatal("a single oversized name was audited by name")
	}
}

// TestUserChangeLogCountsOmittedNamesExactly checks that a sync touching more
// services in a selected category than one entry lists reports the exact count
// as omitted, not the number of rows it happened to read.
func TestUserChangeLogCountsOmittedNamesExactly(t *testing.T) {
	s := openChangeLogStore(t)
	ctx := context.Background()
	userID, feedID := userSelectingFeedChange(t, s, "ai", false)
	n := maxListedNames + 50
	added := make([]ServiceKey, 0, n)
	for i := 0; i < n; i++ {
		added = append(added, ServiceKey{Category: "ai", Service: fmt.Sprintf("svc-%04d", i)})
	}
	recordHistorySync(t, s, feedID, time.Now().Add(-time.Hour).Unix(), added...)

	all, err := s.UserChangeLog(ctx, userID, time.Now(), userChangeLogWindow)
	if err != nil {
		t.Fatal(err)
	}
	got := feedSyncEntries(all)
	if len(got) != 1 || len(got[0].Added.Services) != maxListedNames || got[0].Added.Omitted != 50 {
		t.Fatalf("entry = %d listed, omitted %d; want %d listed and 50 omitted", len(got[0].Added.Services), got[0].Added.Omitted, maxListedNames)
	}
}
