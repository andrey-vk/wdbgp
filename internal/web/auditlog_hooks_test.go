package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/andrey-vk/wdbgp/internal/store"
)

// latestAudit returns the single most recent audit log entry, failing the
// test if there isn't exactly one matching action.
func latestAuditByAction(t *testing.T, st *store.Store, action string) store.AuditLogEntry {
	t.Helper()
	entries, total, err := st.ListAuditLog(context.Background(), store.AuditLogFilter{Action: action}, 50, 0)
	if err != nil {
		t.Fatalf("list audit log: %v", err)
	}
	if total != 1 {
		t.Fatalf("action %q: total = %d, want exactly 1", action, total)
	}
	return entries[0]
}

func auditLogCount(t *testing.T, st *store.Store, action string) int {
	t.Helper()
	_, total, err := st.ListAuditLog(context.Background(), store.AuditLogFilter{Action: action}, 50, 0)
	if err != nil {
		t.Fatalf("list audit log: %v", err)
	}
	return total
}

// --- Communities hooks ---------------------------------------------------

// modeWithCatalogFixture creates a mode with one feed/catalog entry, ready
// for communities put/reset/generate.
func modeWithCatalogFixture(t *testing.T) (*Server, *store.Store, int64) {
	t.Helper()
	srv, st, _ := setupUserTestServer(t)
	ctx := context.Background()

	req := httptest.NewRequest("POST", "/api/admin/modes", strings.NewReader(`{"name":"Audit Mode","enabled":true}`))
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

	feedID, err := st.AddFeed(ctx, "Audit Feed", "http://example.com/audit.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatalf("add feed: %v", err)
	}
	if _, err := st.DB.ExecContext(ctx, "INSERT INTO catalog_mode_feeds(mode_id, feed_id) VALUES (?, ?)", modeID, feedID); err != nil {
		t.Fatalf("assign feed: %v", err)
	}
	if err := st.InsertCatalogEntries(ctx, feedID, []store.CatalogEntry{
		{Category: "cat-a", Service: "svc-a", CIDR: "10.0.0.0/8"},
	}); err != nil {
		t.Fatalf("insert catalog: %v", err)
	}
	return srv, st, modeID
}

func TestAuditHookCommunitiesGenerate(t *testing.T) {
	srv, st, modeID := modeWithCatalogFixture(t)

	req := httptest.NewRequest("POST", "/api/admin/modes/x/communities/generate", nil)
	req.SetPathValue("id", strconv.FormatInt(modeID, 10))
	w := httptest.NewRecorder()
	srv.apiModeCommunitiesGenerate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("generate: %d body=%s", w.Code, w.Body.String())
	}

	e := latestAuditByAction(t, st, "communities.generated")
	if e.ObjectType != "mode" || e.ObjectID != strconv.FormatInt(modeID, 10) {
		t.Fatalf("entry = %+v, want mode/%d", e, modeID)
	}
	if e.Before == e.After {
		t.Fatalf("before == after (%q), want a real change", e.Before)
	}
}

func TestAuditHookCommunitiesGenerateNoopWhenNothingToGenerate(t *testing.T) {
	srv, st, modeID := modeWithCatalogFixture(t)
	ctx := context.Background()

	// First generate fills everything; a second call has nothing left to do.
	if _, err := st.GenerateCommunities(ctx, modeID); err != nil {
		t.Fatalf("pre-generate: %v", err)
	}

	req := httptest.NewRequest("POST", "/api/admin/modes/x/communities/generate", nil)
	req.SetPathValue("id", strconv.FormatInt(modeID, 10))
	w := httptest.NewRecorder()
	srv.apiModeCommunitiesGenerate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("generate: %d body=%s", w.Code, w.Body.String())
	}

	if n := auditLogCount(t, st, "communities.generated"); n != 0 {
		t.Fatalf("audit log count = %d, want 0 (nothing changed)", n)
	}
}

func TestAuditHookCommunitiesPut(t *testing.T) {
	srv, st, modeID := modeWithCatalogFixture(t)

	body := `{"communities":[{"category":"cat-a","service":"svc-a","community":12345}]}`
	req := httptest.NewRequest("PUT", "/api/admin/modes/x/communities", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", strconv.FormatInt(modeID, 10))
	w := httptest.NewRecorder()
	srv.apiModeCommunitiesPut(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("put: %d body=%s", w.Code, w.Body.String())
	}

	e := latestAuditByAction(t, st, "communities.updated")
	if e.ObjectType != "mode" || e.ObjectID != strconv.FormatInt(modeID, 10) {
		t.Fatalf("entry = %+v, want mode/%d", e, modeID)
	}
	if !strings.Contains(e.After, "12345") {
		t.Fatalf("after = %q, want it to mention the new community 12345", e.After)
	}
}

func TestAuditHookCommunitiesReset(t *testing.T) {
	srv, st, modeID := modeWithCatalogFixture(t)
	ctx := context.Background()
	if _, err := st.GenerateCommunities(ctx, modeID); err != nil {
		t.Fatalf("pre-generate: %v", err)
	}

	// Unconfirmed call just returns a preview — confirm with the digest.
	req := httptest.NewRequest("POST", "/api/admin/modes/x/communities/reset", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", strconv.FormatInt(modeID, 10))
	w := httptest.NewRecorder()
	srv.apiModeCommunitiesReset(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("preview: %d body=%s", w.Code, w.Body.String())
	}
	var preview struct {
		Digest string `json:"digest"`
	}
	if err := json.NewDecoder(w.Body).Decode(&preview); err != nil {
		t.Fatal(err)
	}

	confirmBody := `{"confirm":true,"digest":"` + preview.Digest + `"}`
	req = httptest.NewRequest("POST", "/api/admin/modes/x/communities/reset", strings.NewReader(confirmBody))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", strconv.FormatInt(modeID, 10))
	w = httptest.NewRecorder()
	srv.apiModeCommunitiesReset(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("confirm reset: %d body=%s", w.Code, w.Body.String())
	}

	if n := auditLogCount(t, st, "communities.reset"); n != 1 {
		t.Fatalf("audit log count for communities.reset = %d, want 1", n)
	}
}

// --- Global route filters (apiSettingsPut) --------------------------------

func TestAuditHookGlobalRouteFiltersUpdated(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)

	req := httptest.NewRequest("PUT", "/api/admin/settings", strings.NewReader(`{"filter_allow":"10.0.0.0/8"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiSettingsPut(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("settings put: %d body=%s", w.Code, w.Body.String())
	}

	e := latestAuditByAction(t, st, "route_filters.global_updated")
	if e.ObjectType != "settings" || e.ObjectID != "" {
		t.Fatalf("entry = %+v, want settings/\"\"", e)
	}
	if !strings.Contains(e.After, "10.0.0.0/8") {
		t.Fatalf("after = %q, want it to mention 10.0.0.0/8", e.After)
	}
}

func TestAuditHookGlobalRouteFiltersNoopWhenUnrelatedSettingChanges(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)

	req := httptest.NewRequest("PUT", "/api/admin/settings", strings.NewReader(`{"rate_limit_login":10}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiSettingsPut(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("settings put: %d body=%s", w.Code, w.Body.String())
	}

	if n := auditLogCount(t, st, "route_filters.global_updated"); n != 0 {
		t.Fatalf("audit log count = %d, want 0 (filter_allow/filter_deny untouched)", n)
	}
}

func TestAuditHookGlobalRouteFiltersNoopWhenSameValueResubmitted(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)

	req := httptest.NewRequest("PUT", "/api/admin/settings", strings.NewReader(`{"filter_allow":"10.0.0.0/8"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.apiSettingsPut(httptest.NewRecorder(), req)

	// Resubmit the identical value.
	req = httptest.NewRequest("PUT", "/api/admin/settings", strings.NewReader(`{"filter_allow":"10.0.0.0/8"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiSettingsPut(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("settings put: %d body=%s", w.Code, w.Body.String())
	}

	if n := auditLogCount(t, st, "route_filters.global_updated"); n != 1 {
		t.Fatalf("audit log count = %d, want 1 (only the first, real change)", n)
	}
}

// --- Feed enable/disable ---------------------------------------------------

func feedFixture(t *testing.T) (*Server, *store.Store, int64) {
	t.Helper()
	srv, st, _ := setupUserTestServer(t)
	ctx := context.Background()
	if _, err := st.DB.ExecContext(ctx, "INSERT OR IGNORE INTO feed_adapters(id, name, language, api_version, source, revision) VALUES (1, 'Test', 'javascript', 1, 'function sync(feed, api) { return []; }', 1)"); err != nil {
		t.Fatalf("setup adapter: %v", err)
	}
	req := httptest.NewRequest("POST", "/api/admin/feeds", strings.NewReader(
		`{"name":"audit-feed","url":"http://example.com/feed.json","enabled":true,"sync_interval":3600,"mode_id":1,"adapter_id":1}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiFeedsCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create feed: %d body=%s", w.Code, w.Body.String())
	}
	var created feedJSON
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	return srv, st, created.ID
}

func TestAuditHookFeedEnabledChanged(t *testing.T) {
	srv, st, feedID := feedFixture(t)
	idStr := strconv.FormatInt(feedID, 10)

	req := httptest.NewRequest("PUT", "/api/admin/feeds/"+idStr, strings.NewReader(
		`{"name":"audit-feed","url":"http://example.com/feed.json","enabled":false,"sync_interval":3600,"mode_id":1,"adapter_id":1}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", idStr)
	w := httptest.NewRecorder()
	srv.apiFeedsUpdate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update: %d body=%s", w.Code, w.Body.String())
	}

	e := latestAuditByAction(t, st, "feed.enabled_changed")
	if e.ObjectType != "feed" || e.ObjectID != idStr {
		t.Fatalf("entry = %+v, want feed/%s", e, idStr)
	}
	if e.Before != `{"enabled":true}` || e.After != `{"enabled":false}` {
		t.Fatalf("before=%q after=%q, want true->false", e.Before, e.After)
	}
}

func TestAuditHookFeedEnabledNoopWhenUnchanged(t *testing.T) {
	srv, st, feedID := feedFixture(t)
	idStr := strconv.FormatInt(feedID, 10)

	// Same enabled:true, only the name changes.
	req := httptest.NewRequest("PUT", "/api/admin/feeds/"+idStr, strings.NewReader(
		`{"name":"renamed-feed","url":"http://example.com/feed.json","enabled":true,"sync_interval":3600,"mode_id":1,"adapter_id":1}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", idStr)
	w := httptest.NewRecorder()
	srv.apiFeedsUpdate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update: %d body=%s", w.Code, w.Body.String())
	}

	if n := auditLogCount(t, st, "feed.enabled_changed"); n != 0 {
		t.Fatalf("audit log count = %d, want 0 (enabled unchanged)", n)
	}
}

// --- Admin user update: mode move + route filters -------------------------

func adminUserFixture(t *testing.T) (*Server, *store.Store, int64) {
	t.Helper()
	srv, st, _ := setupUserTestServer(t)
	req := httptest.NewRequest("POST", "/api/admin/users", strings.NewReader(
		`{"name":"audit-user","peer_ip":"10.9.9.1","peer_asn":65009,"networks":["10.9.9.0/24"],"web_auth":"network","enabled":true,"catalog_mode_id":1}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create user: %d body=%s", w.Code, w.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	return srv, st, created.ID
}

func createSecondModeFixture(t *testing.T, srv *Server) int64 {
	t.Helper()
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
	return created.ID
}

func TestAuditHookAdminUserModeChanged(t *testing.T) {
	srv, st, userID := adminUserFixture(t)
	modeBID := createSecondModeFixture(t, srv)
	idStr := strconv.FormatInt(userID, 10)

	req := httptest.NewRequest("PUT", "/api/admin/users/"+idStr, strings.NewReader(
		`{"catalog_mode_id":`+strconv.FormatInt(modeBID, 10)+`}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", idStr)
	w := httptest.NewRecorder()
	srv.apiUsersUpdate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update: %d body=%s", w.Code, w.Body.String())
	}

	e := latestAuditByAction(t, st, "user.mode_changed")
	if e.ObjectType != "user" || e.ObjectID != idStr {
		t.Fatalf("entry = %+v, want user/%s", e, idStr)
	}
	if e.Before != `{"catalog_mode_id":1}` {
		t.Fatalf("before = %q, want catalog_mode_id 1", e.Before)
	}
}

func TestAuditHookAdminUserModeChangedNoopWhenSameMode(t *testing.T) {
	srv, st, userID := adminUserFixture(t)
	idStr := strconv.FormatInt(userID, 10)

	req := httptest.NewRequest("PUT", "/api/admin/users/"+idStr, strings.NewReader(`{"catalog_mode_id":1}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", idStr)
	w := httptest.NewRecorder()
	srv.apiUsersUpdate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update: %d body=%s", w.Code, w.Body.String())
	}

	if n := auditLogCount(t, st, "user.mode_changed"); n != 0 {
		t.Fatalf("audit log count = %d, want 0 (mode unchanged)", n)
	}
}

func TestAuditHookAdminUserRouteFiltersUpdated(t *testing.T) {
	srv, st, userID := adminUserFixture(t)
	idStr := strconv.FormatInt(userID, 10)

	req := httptest.NewRequest("PUT", "/api/admin/users/"+idStr, strings.NewReader(
		`{"filter_allow":["192.168.0.0/16"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", idStr)
	w := httptest.NewRecorder()
	srv.apiUsersUpdate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update: %d body=%s", w.Code, w.Body.String())
	}

	e := latestAuditByAction(t, st, "route_filters.user_updated")
	if e.ObjectType != "user" || e.ObjectID != idStr {
		t.Fatalf("entry = %+v, want user/%s", e, idStr)
	}
	if !strings.Contains(e.After, "192.168.0.0/16") {
		t.Fatalf("after = %q, want it to mention 192.168.0.0/16", e.After)
	}
}

// TestAuditHookAdminUserRouteFiltersAfterIsNormalized mirrors the
// self-service version: apiUsersUpdate's audit "after" must be the
// normalized form SetUserRouteFilters actually persists, not the raw
// submitted list.
func TestAuditHookAdminUserRouteFiltersAfterIsNormalized(t *testing.T) {
	srv, st, userID := adminUserFixture(t)
	idStr := strconv.FormatInt(userID, 10)

	req := httptest.NewRequest("PUT", "/api/admin/users/"+idStr, strings.NewReader(
		`{"filter_allow":["192.168.0.1"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", idStr)
	w := httptest.NewRecorder()
	srv.apiUsersUpdate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update: %d body=%s", w.Code, w.Body.String())
	}

	e := latestAuditByAction(t, st, "route_filters.user_updated")
	if !strings.Contains(e.After, "192.168.0.1/32") {
		t.Fatalf("after = %q, want the normalized \"192.168.0.1/32\", not the raw bare IP", e.After)
	}
}

// --- Self-service user hooks ----------------------------------------------

func selfServiceUserFixture(t *testing.T, filterEditable, catalogEditable bool) (*Server, *store.Store, int64) {
	t.Helper()
	srv, st, _ := setupUserTestServer(t)
	req := httptest.NewRequest("POST", "/api/admin/users", strings.NewReader(
		`{"name":"self-user","peer_ip":"10.8.8.1","peer_asn":65008,"networks":["10.8.8.0/24"],`+
			`"web_auth":"network","enabled":true,"catalog_mode_id":1,`+
			`"filter_editable":`+strconv.FormatBool(filterEditable)+`,`+
			`"catalog_editable":`+strconv.FormatBool(catalogEditable)+`}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create user: %d body=%s", w.Code, w.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	return srv, st, created.ID
}

func TestAuditHookUserSaveFilters(t *testing.T) {
	srv, st, _ := selfServiceUserFixture(t, true, false)

	req := httptest.NewRequest("POST", "/api/user/filters", strings.NewReader(`{"allow":["10.1.0.0/16"],"deny":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "10.8.8.1:1234"
	addCSRF(req)
	w := httptest.NewRecorder()
	srv.requireUser(srv.apiUserSaveFilters).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("save filters: %d body=%s", w.Code, w.Body.String())
	}

	e := latestAuditByAction(t, st, "route_filters.user_updated")
	if !strings.HasPrefix(e.Actor, "user:") {
		t.Fatalf("actor = %q, want user:<id>", e.Actor)
	}
	if !strings.Contains(e.After, "10.1.0.0/16") {
		t.Fatalf("after = %q, want it to mention 10.1.0.0/16", e.After)
	}
}

// TestAuditHookUserSaveFiltersAfterIsNormalized guards against logging the
// raw submitted filter representation: SetUserRouteFilters persists the
// normalized form (a bare IP becomes a /32), so the audit "after" must
// match what's actually in the database, not what the client happened to
// type.
func TestAuditHookUserSaveFiltersAfterIsNormalized(t *testing.T) {
	srv, st, _ := selfServiceUserFixture(t, true, false)

	req := httptest.NewRequest("POST", "/api/user/filters", strings.NewReader(`{"allow":["10.1.0.1"],"deny":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "10.8.8.1:1234"
	addCSRF(req)
	w := httptest.NewRecorder()
	srv.requireUser(srv.apiUserSaveFilters).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("save filters: %d body=%s", w.Code, w.Body.String())
	}

	e := latestAuditByAction(t, st, "route_filters.user_updated")
	if !strings.Contains(e.After, "10.1.0.1/32") {
		t.Fatalf("after = %q, want the normalized \"10.1.0.1/32\", not the raw bare IP", e.After)
	}
}

// TestAuditHookUserSaveFiltersNoopWhenResubmittingNormalizedEquivalent
// guards against a false audit entry when the submitted representation
// normalizes to the already-stored value (a bare IP for an existing /32).
func TestAuditHookUserSaveFiltersNoopWhenResubmittingNormalizedEquivalent(t *testing.T) {
	srv, st, userID := selfServiceUserFixture(t, true, false)
	ctx := context.Background()
	if err := st.SetUserRouteFilters(ctx, userID, store.RouteFilters{Allow: []string{"10.1.0.1/32"}}); err != nil {
		t.Fatalf("pre-set filters: %v", err)
	}

	req := httptest.NewRequest("POST", "/api/user/filters", strings.NewReader(`{"allow":["10.1.0.1"],"deny":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "10.8.8.1:1234"
	addCSRF(req)
	w := httptest.NewRecorder()
	srv.requireUser(srv.apiUserSaveFilters).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("save filters: %d body=%s", w.Code, w.Body.String())
	}

	if n := auditLogCount(t, st, "route_filters.user_updated"); n != 0 {
		t.Fatalf("audit log count = %d, want 0 (bare IP normalizes to the already-stored /32)", n)
	}
}

func TestAuditHookUserSwitchMode(t *testing.T) {
	srv, st, _ := selfServiceUserFixture(t, false, true)
	modeBID := createSecondModeFixture(t, srv)

	req := httptest.NewRequest("PUT", "/api/user/mode", strings.NewReader(`{"mode_id":`+strconv.FormatInt(modeBID, 10)+`}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "10.8.8.1:1234"
	addCSRF(req)
	w := httptest.NewRecorder()
	srv.requireUser(srv.apiUserSwitchMode).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("switch mode: %d body=%s", w.Code, w.Body.String())
	}

	e := latestAuditByAction(t, st, "user.mode_changed")
	if !strings.HasPrefix(e.Actor, "user:") {
		t.Fatalf("actor = %q, want user:<id>", e.Actor)
	}
	if e.Before != `{"catalog_mode_id":1}` {
		t.Fatalf("before = %q, want catalog_mode_id 1", e.Before)
	}
}

func TestAuditHookUserSaveSelections(t *testing.T) {
	srv, st, _ := selfServiceUserFixture(t, false, false)
	ctx := context.Background()
	feedID, err := st.AddFeed(ctx, "sel-feed", "http://example.com/sel.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatalf("add feed: %v", err)
	}
	if _, err := st.DB.ExecContext(ctx, "INSERT INTO catalog_mode_feeds(mode_id, feed_id) VALUES (1, ?)", feedID); err != nil {
		t.Fatalf("assign feed: %v", err)
	}
	if err := st.InsertCatalogEntries(ctx, feedID, []store.CatalogEntry{
		{Category: "cat-x", Service: "svc-x", CIDR: "172.16.0.0/16"},
	}); err != nil {
		t.Fatalf("insert catalog: %v", err)
	}
	if err := st.RebuildModeEntries(ctx, 1); err != nil {
		t.Fatalf("rebuild mode entries: %v", err)
	}

	req := httptest.NewRequest("POST", "/api/user/selections", strings.NewReader(
		`{"categories":[{"category":"cat-x","checked":true}],"services":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "10.8.8.1:1234"
	addCSRF(req)
	w := httptest.NewRecorder()
	srv.requireUser(srv.apiUserSaveSelections).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("save selections: %d body=%s", w.Code, w.Body.String())
	}

	e := latestAuditByAction(t, st, "user.selections_changed")
	if !strings.HasPrefix(e.Actor, "user:") {
		t.Fatalf("actor = %q, want user:<id>", e.Actor)
	}
	if e.Before == e.After {
		t.Fatalf("before == after (%q), want a real change", e.Before)
	}
}

func TestAuditHookAdminUserSaveSelections(t *testing.T) {
	srv, st, userID := adminUserFixture(t)
	ctx := context.Background()
	feedID, err := st.AddFeed(ctx, "admin-sel-feed", "http://example.com/sel.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatalf("add feed: %v", err)
	}
	if _, err := st.DB.ExecContext(ctx, "INSERT INTO catalog_mode_feeds(mode_id, feed_id) VALUES (1, ?)", feedID); err != nil {
		t.Fatalf("assign feed: %v", err)
	}
	if err := st.InsertCatalogEntries(ctx, feedID, []store.CatalogEntry{
		{Category: "cat-y", Service: "svc-y", CIDR: "172.17.0.0/16"},
	}); err != nil {
		t.Fatalf("insert catalog: %v", err)
	}
	if err := st.RebuildModeEntries(ctx, 1); err != nil {
		t.Fatalf("rebuild mode entries: %v", err)
	}
	idStr := strconv.FormatInt(userID, 10)

	req := httptest.NewRequest("PUT", "/api/admin/users/"+idStr+"/selections", strings.NewReader(
		`{"categories":[{"category":"cat-y","checked":true}],"services":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", idStr)
	w := httptest.NewRecorder()
	srv.apiAdminUserSaveSelections(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("admin save selections: %d body=%s", w.Code, w.Body.String())
	}

	e := latestAuditByAction(t, st, "user.selections_changed")
	if !strings.HasPrefix(e.Actor, "admin:") {
		t.Fatalf("actor = %q, want admin:<ip>", e.Actor)
	}
	if e.ObjectType != "user" || e.ObjectID != idStr {
		t.Fatalf("entry = %+v, want user/%s", e, idStr)
	}
}

// TestAuditHookAdminUserSaveSelectionsModeSwitchNoopComparesTargetMode
// guards against comparing the before-snapshot (old mode) against the
// after-snapshot (target mode) when a save switches modes — two different
// modes' selection counts are not comparable, so a pure mode switch with no
// selection changes at all must not be misreported as a selections change.
func TestAuditHookAdminUserSaveSelectionsModeSwitchNoopComparesTargetMode(t *testing.T) {
	srv, st, userID := adminUserFixture(t)
	ctx := context.Background()

	// Give the user's current mode (1) one selected category, so its
	// selection count (1) differs from the brand-new target mode's (0).
	feedID, err := st.AddFeed(ctx, "mode1-feed", "http://example.com/mode1.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatalf("add feed: %v", err)
	}
	if _, err := st.DB.ExecContext(ctx, "INSERT INTO catalog_mode_feeds(mode_id, feed_id) VALUES (1, ?)", feedID); err != nil {
		t.Fatalf("assign feed: %v", err)
	}
	if err := st.InsertCatalogEntries(ctx, feedID, []store.CatalogEntry{
		{Category: "cat-old", Service: "svc-old", CIDR: "10.50.0.0/16"},
	}); err != nil {
		t.Fatalf("insert catalog: %v", err)
	}
	if err := st.RebuildModeEntries(ctx, 1); err != nil {
		t.Fatalf("rebuild mode entries: %v", err)
	}
	if err := st.Transaction(ctx, func(tx *sql.Tx) error {
		return store.ToggleSelectedCategory(ctx, tx, userID, 1, "cat-old", true)
	}); err != nil {
		t.Fatalf("pre-select category in mode 1: %v", err)
	}

	modeBID := createSecondModeFixture(t, srv)
	idStr := strconv.FormatInt(userID, 10)

	// Switch to mode B, toggling nothing — a pure mode switch.
	req := httptest.NewRequest("PUT", "/api/admin/users/"+idStr+"/selections", strings.NewReader(
		`{"mode_id":`+strconv.FormatInt(modeBID, 10)+`,"categories":[],"services":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", idStr)
	w := httptest.NewRecorder()
	srv.apiAdminUserSaveSelections(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("admin save selections: %d body=%s", w.Code, w.Body.String())
	}

	if n := auditLogCount(t, st, "user.selections_changed"); n != 0 {
		t.Fatalf("selections_changed count = %d, want 0 (mode 1's count of 1 must not be compared against target mode B's count of 0)", n)
	}
}
