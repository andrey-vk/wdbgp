package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/andrey-vk/wdbgp/internal/settings"
	"github.com/andrey-vk/wdbgp/internal/store"
)

// addPreviewTestUser mirrors blastradius_test.go's addBlastRadiusTestUser
// (internal/store), rebuilt at the handler layer so these tests exercise
// the real HTTP handlers rather than calling the store directly. CIDRs
// start at 20+n/8 — deliberately not one of the bogon/private ranges the
// product's default global filter_deny blocks (10/8, 127/8, 172.16/12,
// 192.168/16, etc.), which would otherwise make every global-mode user's
// prefix silently disappear behind the default deny list.
func addPreviewTestUser(t *testing.T, st *store.Store, modeID int64, filterMode, categoryName string, n uint32) int64 {
	t.Helper()
	ctx := context.Background()
	feedID, err := st.AddFeed(ctx, categoryName+"-feed", fmt.Sprintf("https://example.test/%s.json", categoryName), 1, true, 0, "", "", true)
	if err != nil {
		t.Fatalf("add feed: %v", err)
	}
	if _, err := st.DB.ExecContext(ctx, "INSERT INTO catalog_mode_feeds(mode_id, feed_id) VALUES (?, ?)", modeID, feedID); err != nil {
		t.Fatalf("link feed to mode: %v", err)
	}
	if err := st.InsertCatalogEntries(ctx, feedID, []store.CatalogEntry{
		{Category: categoryName, Service: "svc", CIDR: fmt.Sprintf("%d.0.0.0/8", 20+n)},
	}); err != nil {
		t.Fatalf("insert catalog: %v", err)
	}
	userID, err := st.AddUser(ctx, store.User{
		Name: fmt.Sprintf("%s-user-%d", categoryName, n), PeerIP: fmt.Sprintf("172.16.%d.1", n),
		PeerASN: 65000 + n, Enabled: true, FilterMode: filterMode, CatalogModeID: modeID,
		Networks: []string{fmt.Sprintf("192.168.%d.0/24", n)},
	})
	if err != nil {
		t.Fatalf("add user: %v", err)
	}
	if err := st.Transaction(ctx, func(tx *sql.Tx) error {
		return store.SetUserModeSelection(ctx, tx, userID, modeID, []string{categoryName}, nil)
	}); err != nil {
		t.Fatalf("select category: %v", err)
	}
	return userID
}

func TestAPISettingsPreviewFilters(t *testing.T) {
	st := setupUserTestStore(t)
	realSettings, err := settings.New(st)
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{settings: realSettings, store: st}
	ctx := context.Background()
	userID := addPreviewTestUser(t, st, store.DefaultCatalogModeID, store.FilterModeGlobal, "cat-a", 1)

	beforeFilters, err := st.GlobalRouteFilters(ctx)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("POST", "/api/admin/settings/preview-filters", strings.NewReader(`{"filter_allow":"","filter_deny":"21.0.0.0/8"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiSettingsPreviewFilters(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("preview: %d body=%s", w.Code, w.Body.String())
	}

	var preview store.BlastRadiusPreview
	if err := json.NewDecoder(w.Body).Decode(&preview); err != nil {
		t.Fatal(err)
	}
	if len(preview.AffectedUsers) != 1 || preview.AffectedUsers[0].UserID != userID || !preview.AffectedUsers[0].LostRoutes {
		t.Fatalf("preview = %+v, want 1 affected user (id %d) that loses routes", preview, userID)
	}

	// Nothing persisted — the real filter_deny must still be exactly what
	// it was before this preview, not the trial "21.0.0.0/8" replacement.
	afterFilters, err := st.GlobalRouteFilters(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(afterFilters.Deny) != len(beforeFilters.Deny) {
		t.Fatalf("GlobalRouteFilters changed after a preview: before=%+v after=%+v", beforeFilters, afterFilters)
	}
}

func TestAPIUserPreviewFilterChange(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)
	ctx := context.Background()
	userID := addPreviewTestUser(t, st, store.DefaultCatalogModeID, store.FilterModeOverride, "cat-a", 1)
	idStr := strconv.FormatInt(userID, 10)

	req := httptest.NewRequest("POST", "/api/admin/users/"+idStr+"/preview", strings.NewReader(
		`{"enabled":true,"filter_mode":"override","allow":[],"deny":["21.0.0.0/8"],"catalog_mode_id":`+strconv.FormatInt(store.DefaultCatalogModeID, 10)+`}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", idStr)
	w := httptest.NewRecorder()
	srv.apiUserPreview(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("preview: %d body=%s", w.Code, w.Body.String())
	}

	var preview store.BlastRadiusPreview
	if err := json.NewDecoder(w.Body).Decode(&preview); err != nil {
		t.Fatal(err)
	}
	if len(preview.AffectedUsers) != 1 || preview.AffectedUsers[0].BeforeV4 != 1 || preview.AffectedUsers[0].AfterV4 != 0 {
		t.Fatalf("preview = %+v, want user %d: 1 -> 0", preview, userID)
	}

	persisted, err := st.UserRouteFilters(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.Deny) != 0 {
		t.Fatalf("UserRouteFilters changed after a preview: %+v", persisted)
	}
}

func TestAPIUserPreviewNotFound(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)
	req := httptest.NewRequest("POST", "/api/admin/users/999/preview", strings.NewReader(
		`{"enabled":true,"filter_mode":"global","allow":[],"deny":[],"catalog_mode_id":`+strconv.FormatInt(store.DefaultCatalogModeID, 10)+`}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "999")
	w := httptest.NewRecorder()
	srv.apiUserPreview(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("preview: %d body=%s, want 404", w.Code, w.Body.String())
	}
}

func TestAPIModeFeedsPreview(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)
	ctx := context.Background()

	req := httptest.NewRequest("POST", "/api/admin/modes", strings.NewReader(`{"name":"Mode A","enabled":true}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiModesCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create mode: %d body=%s", w.Code, w.Body.String())
	}
	var created modeJSON
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	modeID := created.ID
	userID := addPreviewTestUser(t, st, modeID, store.FilterModeGlobal, "cat-a", 1)
	idStr := strconv.FormatInt(modeID, 10)

	req = httptest.NewRequest("POST", "/api/admin/modes/x/feeds/preview", strings.NewReader(`{"feed_ids":[],"enabled":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", idStr)
	w = httptest.NewRecorder()
	srv.apiModeFeedsPreview(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("preview: %d body=%s", w.Code, w.Body.String())
	}

	var preview store.BlastRadiusPreview
	if err := json.NewDecoder(w.Body).Decode(&preview); err != nil {
		t.Fatal(err)
	}
	if len(preview.AffectedUsers) != 1 || preview.AffectedUsers[0].UserID != userID || !preview.AffectedUsers[0].LostRoutes {
		t.Fatalf("preview = %+v, want 1 affected user (id %d) that loses routes", preview, userID)
	}

	v4, _, err := st.CountSelectionPrefixes(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if v4 != 1 {
		t.Fatalf("user's real prefix count = %d after a preview, want unchanged 1", v4)
	}
}

// TestAPIModeFeedsPreviewIncludesEnabledTransition checks that the
// "enabled" field in the preview body reaches PreviewModeFeedChange — a
// feed-membership-only preview against a disabled mode must report no
// impact (countSelectionPrefixesTx requires catalog_modes.enabled = 1), but
// the SAME feeds with enabled:true must show the user actually gaining
// routes.
func TestAPIModeFeedsPreviewIncludesEnabledTransition(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)

	req := httptest.NewRequest("POST", "/api/admin/modes", strings.NewReader(`{"name":"Disabled Mode","enabled":false}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiModesCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create mode: %d body=%s", w.Code, w.Body.String())
	}
	var created modeJSON
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	modeID := created.ID
	userID := addPreviewTestUser(t, st, modeID, store.FilterModeGlobal, "cat-a", 1)
	idStr := strconv.FormatInt(modeID, 10)

	feeds, err := st.ModeFeeds(context.Background(), modeID)
	if err != nil {
		t.Fatal(err)
	}
	if len(feeds) != 1 {
		t.Fatalf("ModeFeeds = %+v, want exactly 1", feeds)
	}
	feedsBody := fmt.Sprintf(`{"feed_ids":[%d]`, feeds[0].ID)

	req = httptest.NewRequest("POST", "/api/admin/modes/x/feeds/preview", strings.NewReader(feedsBody+`,"enabled":false}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", idStr)
	w = httptest.NewRecorder()
	srv.apiModeFeedsPreview(w, req)
	var staysDisabled store.BlastRadiusPreview
	if err := json.NewDecoder(w.Body).Decode(&staysDisabled); err != nil {
		t.Fatal(err)
	}
	if staysDisabled.AffectedUsers[0].BeforeV4 != 0 || staysDisabled.AffectedUsers[0].AfterV4 != 0 {
		t.Fatalf("staysDisabled preview = %+v, want 0 -> 0", staysDisabled.AffectedUsers[0])
	}

	req = httptest.NewRequest("POST", "/api/admin/modes/x/feeds/preview", strings.NewReader(feedsBody+`,"enabled":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", idStr)
	w = httptest.NewRecorder()
	srv.apiModeFeedsPreview(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("preview: %d body=%s", w.Code, w.Body.String())
	}
	var becomesEnabled store.BlastRadiusPreview
	if err := json.NewDecoder(w.Body).Decode(&becomesEnabled); err != nil {
		t.Fatal(err)
	}
	a := becomesEnabled.AffectedUsers[0]
	if a.UserID != userID || a.BeforeV4 != 0 || a.AfterV4 != 1 {
		t.Fatalf("becomesEnabled preview = %+v, want user %d: 0 -> 1", a, userID)
	}

	mode, err := st.CatalogMode(context.Background(), modeID)
	if err != nil {
		t.Fatal(err)
	}
	if mode.Enabled {
		t.Fatalf("mode enabled = true after previews, want unchanged false")
	}
}

func TestAPIUserPreviewModeMove(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)

	req := httptest.NewRequest("POST", "/api/admin/modes", strings.NewReader(`{"name":"Mode B","enabled":true}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiModesCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create mode: %d body=%s", w.Code, w.Body.String())
	}
	var created modeJSON
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	modeBID := created.ID
	userID := addPreviewTestUser(t, st, store.DefaultCatalogModeID, store.FilterModeGlobal, "cat-a", 1)
	idStr := strconv.FormatInt(userID, 10)

	req = httptest.NewRequest("POST", "/api/admin/users/"+idStr+"/preview",
		strings.NewReader(fmt.Sprintf(`{"enabled":true,"filter_mode":"global","allow":[],"deny":[],"catalog_mode_id":%d}`, modeBID)))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", idStr)
	w = httptest.NewRecorder()
	srv.apiUserPreview(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("preview: %d body=%s", w.Code, w.Body.String())
	}

	var preview store.BlastRadiusPreview
	if err := json.NewDecoder(w.Body).Decode(&preview); err != nil {
		t.Fatal(err)
	}
	if len(preview.AffectedUsers) != 1 || preview.AffectedUsers[0].BeforeV4 != 1 || preview.AffectedUsers[0].AfterV4 != 0 {
		t.Fatalf("preview = %+v, want user %d: 1 -> 0 (no selection saved for mode B yet)", preview, userID)
	}

	user, err := st.User(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	if user.CatalogModeID != store.DefaultCatalogModeID {
		t.Fatalf("CatalogModeID = %d after a preview, want unchanged %d", user.CatalogModeID, store.DefaultCatalogModeID)
	}
}

// TestAPIUserPreviewRejectsDisabledOrMissingModeWhenModeChanges checks that
// previewing a move into a disabled or nonexistent catalog mode answers the
// same way apiUsersUpdate's own real-save validation does (400, with a
// message naming the actual problem) — not the misleading 404 "User not
// found" that the old, now-removed PreviewUserModeMove used to produce by
// reusing SetUserCatalogModeTx's enabled-mode gate (sql.ErrNoRows ->
// store.IsNotFound), a gate the real admin save applies nowhere at the
// database layer.
func TestAPIUserPreviewRejectsDisabledOrMissingModeWhenModeChanges(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)
	ctx := context.Background()

	disabledModeID, err := st.AddCatalogMode(ctx, "Disabled Mode", false)
	if err != nil {
		t.Fatal(err)
	}
	userID := addPreviewTestUser(t, st, store.DefaultCatalogModeID, store.FilterModeGlobal, "cat-a", 1)
	idStr := strconv.FormatInt(userID, 10)

	for _, tc := range []struct {
		name   string
		modeID int64
	}{
		{"disabled mode", disabledModeID},
		{"nonexistent mode", disabledModeID + 1000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/api/admin/users/"+idStr+"/preview",
				strings.NewReader(fmt.Sprintf(`{"enabled":true,"filter_mode":"global","allow":[],"deny":[],"catalog_mode_id":%d}`, tc.modeID)))
			req.Header.Set("Content-Type", "application/json")
			req.SetPathValue("id", idStr)
			w := httptest.NewRecorder()
			srv.apiUserPreview(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("preview: %d body=%s, want 400", w.Code, w.Body.String())
			}
		})
	}
}

// TestAPIUserPreviewAllowsUnchangedDisabledMode checks the mirror case:
// when catalog_mode_id in the preview body equals the user's CURRENT mode
// (only filters are actually changing), the preview must succeed even if
// that mode has since been disabled — matching apiUsersUpdate's own "only
// validate the mode when this request actually asks to change it" rule, so
// an unrelated filter edit isn't blocked by a mode that was fine when
// assigned.
func TestAPIUserPreviewAllowsUnchangedDisabledMode(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)
	ctx := context.Background()

	userID := addPreviewTestUser(t, st, store.DefaultCatalogModeID, store.FilterModeOverride, "cat-a", 1)
	if err := st.UpdateCatalogMode(ctx, store.CatalogMode{ID: store.DefaultCatalogModeID, Name: "OpenCCK", Enabled: false}); err != nil {
		t.Fatal(err)
	}
	idStr := strconv.FormatInt(userID, 10)

	req := httptest.NewRequest("POST", "/api/admin/users/"+idStr+"/preview", strings.NewReader(
		`{"enabled":true,"filter_mode":"override","allow":[],"deny":["21.0.0.0/8"],"catalog_mode_id":`+strconv.FormatInt(store.DefaultCatalogModeID, 10)+`}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", idStr)
	w := httptest.NewRecorder()
	srv.apiUserPreview(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("preview: %d body=%s, want 200 (mode unchanged, so not re-validated)", w.Code, w.Body.String())
	}
}

func TestAPIModeSaveAppliesRenameEnableAndFeedsTogether(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)
	ctx := context.Background()
	modeID, err := st.AddCatalogMode(ctx, "Before", false)
	if err != nil {
		t.Fatal(err)
	}
	feedID, err := st.AddFeed(ctx, "save-feed", "https://example.test/save.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	idStr := strconv.FormatInt(modeID, 10)

	req := httptest.NewRequest("PUT", "/api/admin/modes/"+idStr+"/save",
		strings.NewReader(fmt.Sprintf(`{"name":"After","enabled":true,"feeds":[{"id":%d,"exclude":false}]}`, feedID)))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", idStr)
	w := httptest.NewRecorder()
	srv.apiModeSave(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("save: %d body=%s", w.Code, w.Body.String())
	}

	mode, err := st.CatalogMode(ctx, modeID)
	if err != nil {
		t.Fatal(err)
	}
	if mode.Name != "After" || !mode.Enabled {
		t.Fatalf("mode = %+v, want renamed to After and enabled", mode)
	}
	feeds, err := st.ModeFeeds(ctx, modeID)
	if err != nil {
		t.Fatal(err)
	}
	if len(feeds) != 1 || feeds[0].ID != feedID {
		t.Fatalf("ModeFeeds = %+v, want exactly feed %d", feeds, feedID)
	}
}
