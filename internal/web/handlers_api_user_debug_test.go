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

// userDebugFixture creates a mode-1 catalog with one feed entry (category
// "Test", service "Debug", CIDR "8.8.8.0/24") and a user on mode 1 reachable
// via network auth from 10.1.1.0/24, returning the server, store, and the
// created user's ID.
func userDebugFixture(t *testing.T) (*Server, *store.Store, int64) {
	t.Helper()
	srv, st, _ := setupUserTestServer(t)
	ctx := context.Background()

	feedID, err := st.AddFeed(ctx, "debug-feed", "http://example.com/debug.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatalf("add feed: %v", err)
	}
	if _, err := st.DB.ExecContext(ctx,
		"INSERT INTO catalog_mode_feeds(mode_id, feed_id) VALUES (1, ?)", feedID); err != nil {
		t.Fatalf("assign feed: %v", err)
	}
	if err := st.InsertCatalogEntries(ctx, feedID, []store.CatalogEntry{
		{Category: "Test", Service: "Debug", CIDR: "8.8.8.0/24"},
	}); err != nil {
		t.Fatalf("insert catalog entries: %v", err)
	}
	if err := st.RebuildModeEntries(ctx, 1); err != nil {
		t.Fatalf("rebuild mode entries: %v", err)
	}

	userBody := `{"name":"lookup-user","peer_ip":"10.1.1.1","peer_asn":65001,` +
		`"networks":["10.1.1.0/24"],"web_auth":"network","enabled":true,"catalog_mode_id":1}`
	req := httptest.NewRequest("POST", "/api/admin/users", strings.NewReader(userBody))
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
		t.Fatalf("decode created user: %v", err)
	}
	return srv, st, created.ID
}

func userDebugRequest(cidr string) *http.Request {
	req := httptest.NewRequest("GET", "/api/user/debug?cidr="+cidr, nil)
	req.RemoteAddr = "10.1.1.1:1234"
	return req
}

func userDebugSelect(t *testing.T, st *store.Store, userID int64, category, service string) {
	t.Helper()
	ctx := context.Background()
	if err := st.Transaction(ctx, func(tx *sql.Tx) error {
		return store.ToggleSelectedService(ctx, tx, userID, 1, category, service, true)
	}); err != nil {
		t.Fatalf("select service: %v", err)
	}
}

func TestUserDebugCIDRRequiresParam(t *testing.T) {
	srv, _, _ := userDebugFixture(t)

	w := httptest.NewRecorder()
	srv.requireUser(srv.apiUserDebugCIDR).ServeHTTP(w, userDebugRequest(""))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", w.Code, w.Body.String())
	}
}

func TestUserDebugCIDRInvalidCIDR(t *testing.T) {
	srv, _, _ := userDebugFixture(t)

	w := httptest.NewRecorder()
	srv.requireUser(srv.apiUserDebugCIDR).ServeHTTP(w, userDebugRequest("not-an-ip"))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", w.Code, w.Body.String())
	}
}

func TestUserDebugCIDRUnauthenticated(t *testing.T) {
	srv, _, _ := userDebugFixture(t)

	req := httptest.NewRequest("GET", "/api/user/debug?cidr=8.8.8.0/24", nil)
	req.RemoteAddr = "203.0.113.9:1234" // does not match any user's network
	w := httptest.NewRecorder()
	srv.requireUser(srv.apiUserDebugCIDR).ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body=%s", w.Code, w.Body.String())
	}
}

// TestUserDebugCIDRNoCatalogMatch covers a query disjoint from every
// catalog entry: no matches, zero percentages, not in the tunnel.
func TestUserDebugCIDRNoCatalogMatch(t *testing.T) {
	srv, _, _ := userDebugFixture(t)

	w := httptest.NewRecorder()
	srv.requireUser(srv.apiUserDebugCIDR).ServeHTTP(w, userDebugRequest("8.8.9.0/24"))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var resp userCIDRLookupResult
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Matches) != 0 {
		t.Fatalf("matches = %+v, want empty", resp.Matches)
	}
	if resp.BeforePercentage != 0 || resp.AfterPercentage != 0 {
		t.Fatalf("before=%v after=%v, want both 0", resp.BeforePercentage, resp.AfterPercentage)
	}
	if resp.InTunnel {
		t.Fatal("in_tunnel = true, want false")
	}
}

// TestUserDebugCIDRMatchedNotSelected covers a catalog hit the user has not
// selected: the match is reported (so the user can see why selecting it
// would help) but contributes nothing to before/after or in_tunnel.
func TestUserDebugCIDRMatchedNotSelected(t *testing.T) {
	srv, _, _ := userDebugFixture(t)

	w := httptest.NewRecorder()
	srv.requireUser(srv.apiUserDebugCIDR).ServeHTTP(w, userDebugRequest("8.8.8.0/24"))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var resp userCIDRLookupResult
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Matches) != 1 || resp.Matches[0].Category != "Test" || resp.Matches[0].Service != "Debug" {
		t.Fatalf("matches = %+v, want one Test/Debug match", resp.Matches)
	}
	if resp.Matches[0].Selected {
		t.Fatal("match reported as selected, but it wasn't")
	}
	if resp.BeforePercentage != 0 || resp.AfterPercentage != 0 {
		t.Fatalf("before=%v after=%v, want both 0 (nothing selected)", resp.BeforePercentage, resp.AfterPercentage)
	}
	if resp.InTunnel {
		t.Fatal("in_tunnel = true, want false")
	}
}

// TestUserDebugCIDRMatchedSelectedButFilteredOut covers the case the
// before/after split exists for: selected, fully covered, but a global deny
// filter removes it before it reaches the wire.
func TestUserDebugCIDRMatchedSelectedButFilteredOut(t *testing.T) {
	srv, st, userID := userDebugFixture(t)
	ctx := context.Background()
	userDebugSelect(t, st, userID, "Test", "Debug")
	// GlobalRouteFilters/ApplyUserRouteFilters read filter_deny from the
	// store's own app_settings table, not from srv.settings (which in
	// tests is an isolated in-memory stub unrelated to the store) — so the
	// deny filter must be written there directly to actually take effect.
	if err := st.SaveSetting(ctx, "filter_deny", "8.8.8.0/24"); err != nil {
		t.Fatalf("set global deny filter: %v", err)
	}

	w := httptest.NewRecorder()
	srv.requireUser(srv.apiUserDebugCIDR).ServeHTTP(w, userDebugRequest("8.8.8.0/24"))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var resp userCIDRLookupResult
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Matches) != 1 || !resp.Matches[0].Selected {
		t.Fatalf("matches = %+v, want one selected match", resp.Matches)
	}
	if resp.BeforePercentage == 0 {
		t.Fatal("before_percentage = 0, want > 0 (it was selected and fully covered)")
	}
	if resp.AfterPercentage != 0 {
		t.Fatalf("after_percentage = %v, want 0 (removed by the deny filter)", resp.AfterPercentage)
	}
	if resp.InTunnel {
		t.Fatal("in_tunnel = true, want false — a deny filter removed it")
	}
}

// TestUserDebugCIDRMatchedSelectedAndDelivered covers the "everything
// worked" case: selected, covered, no filter removes it.
func TestUserDebugCIDRMatchedSelectedAndDelivered(t *testing.T) {
	srv, st, userID := userDebugFixture(t)
	userDebugSelect(t, st, userID, "Test", "Debug")

	w := httptest.NewRecorder()
	srv.requireUser(srv.apiUserDebugCIDR).ServeHTTP(w, userDebugRequest("8.8.8.0/24"))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var resp userCIDRLookupResult
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.BeforePercentage == 0 || resp.BeforePercentage != resp.AfterPercentage {
		t.Fatalf("before=%v after=%v, want equal and > 0", resp.BeforePercentage, resp.AfterPercentage)
	}
	if !resp.InTunnel {
		t.Fatal("in_tunnel = false, want true")
	}
}

// TestUserDebugCIDRDoesNotLeakOtherUsersData covers the security property
// the shared design relies on: userDebugCIDR must never read or expose
// another user's row. Two users share mode 1 and the same selection
// covering the query; calling as A must not reveal B anywhere in the
// response — there is no users/name field in this shape at all to check
// that against raw field access, so this also asserts the wire shape itself
// carries no such field.
func TestUserDebugCIDRDoesNotLeakOtherUsersData(t *testing.T) {
	srv, st, userA := userDebugFixture(t)
	userDebugSelect(t, st, userA, "Test", "Debug")

	userBBody := `{"name":"other-user-should-not-leak","peer_ip":"10.1.1.2","peer_asn":65002,` +
		`"networks":["10.1.2.0/24"],"web_auth":"network","enabled":true,"catalog_mode_id":1}`
	req := httptest.NewRequest("POST", "/api/admin/users", strings.NewReader(userBBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create user B: %d body=%s", w.Code, w.Body.String())
	}
	var userB struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(w.Body).Decode(&userB); err != nil {
		t.Fatal(err)
	}
	userDebugSelect(t, st, userB.ID, "Test", "Debug")

	w = httptest.NewRecorder()
	srv.requireUser(srv.apiUserDebugCIDR).ServeHTTP(w, userDebugRequest("8.8.8.0/24"))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "other-user-should-not-leak") {
		t.Fatalf("response leaked another user's name: %s", w.Body.String())
	}
	var raw map[string]any
	if err := json.NewDecoder(strings.NewReader(w.Body.String())).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["users"]; ok {
		t.Fatal("response has a \"users\" field — the wire shape must not carry a cross-user list")
	}
	if _, ok := raw["name"]; ok {
		t.Fatal("response has a \"name\" field — the wire shape must not identify any user")
	}
}

// TestUserDebugCIDRIgnoresModeQueryParam covers the other half of the
// no-IDOR requirement: a caller cannot switch which mode's catalog they
// see by adding a mode= query parameter, since the handler never reads one
// — it always uses the authenticated user's own catalog_mode_id.
func TestUserDebugCIDRIgnoresModeQueryParam(t *testing.T) {
	srv, st, _ := userDebugFixture(t)
	ctx := context.Background()

	// A second mode whose catalog does NOT cover the query at all.
	otherModeID, err := st.AddCatalogMode(ctx, "Other Mode", true)
	if err != nil {
		t.Fatalf("add mode: %v", err)
	}
	otherFeedID, err := st.AddFeed(ctx, "other-feed", "http://example.com/other.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatalf("add feed: %v", err)
	}
	if _, err := st.DB.ExecContext(ctx,
		"INSERT INTO catalog_mode_feeds(mode_id, feed_id) VALUES (?, ?)", otherModeID, otherFeedID); err != nil {
		t.Fatalf("assign feed: %v", err)
	}
	if err := st.InsertCatalogEntries(ctx, otherFeedID, []store.CatalogEntry{
		{Category: "Unrelated", Service: "Other", CIDR: "198.51.100.0/24"},
	}); err != nil {
		t.Fatalf("insert catalog entries: %v", err)
	}
	if err := st.RebuildModeEntries(ctx, otherModeID); err != nil {
		t.Fatalf("rebuild mode entries: %v", err)
	}

	req := httptest.NewRequest("GET",
		"/api/user/debug?cidr=8.8.8.0/24&mode="+strconv.FormatInt(otherModeID, 10), nil)
	req.RemoteAddr = "10.1.1.1:1234"
	w := httptest.NewRecorder()
	srv.requireUser(srv.apiUserDebugCIDR).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var resp userCIDRLookupResult
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	// Still mode 1's catalog (Test/Debug), not mode otherModeID's (which
	// would report zero matches for this query).
	if len(resp.Matches) != 1 || resp.Matches[0].Category != "Test" {
		t.Fatalf("matches = %+v, want mode 1's Test/Debug match — "+
			"the mode= query parameter must have been ignored", resp.Matches)
	}
}

// TestUserDebugCIDRBareIPInput covers a bare IP address (not a CIDR block)
// as input, confirming the ParsePrefixOrAddr path works end-to-end here.
func TestUserDebugCIDRBareIPInput(t *testing.T) {
	srv, _, _ := userDebugFixture(t)

	w := httptest.NewRecorder()
	srv.requireUser(srv.apiUserDebugCIDR).ServeHTTP(w, userDebugRequest("8.8.8.3"))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var resp userCIDRLookupResult
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Query != "8.8.8.3/32" {
		t.Fatalf("query = %q, want %q", resp.Query, "8.8.8.3/32")
	}
	if len(resp.Matches) != 1 || resp.Matches[0].Percentage != 100 {
		t.Fatalf("matches = %+v, want one 100%% match", resp.Matches)
	}
}

// TestUserDebugCIDRBackendFailureIsNotExposedAsBadRequest covers the
// classification a backend failure (a DB outage, stored filter data that
// fails to parse) must get: 500 with a sanitized message, never a 400
// carrying the raw error — unlike an actually-invalid cidr, which is safe
// to echo back because it never contains anything derived from the
// database.
func TestUserDebugCIDRBackendFailureIsNotExposedAsBadRequest(t *testing.T) {
	srv, st, userID := userDebugFixture(t)
	ctx := context.Background()
	user, err := st.User(ctx, userID)
	if err != nil {
		t.Fatalf("read user: %v", err)
	}

	// Call the handler directly rather than through requireUser: the
	// middleware's own IP/session lookup would otherwise also fail once
	// the DB is closed below, masking the thing this test actually covers
	// (a backend failure inside userDebugCIDR itself, past authentication).
	req := userDebugRequest("8.8.8.0/24")
	req = req.WithContext(context.WithValue(req.Context(), userCtxKey{}, user))
	if err := st.DB.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	w := httptest.NewRecorder()
	srv.apiUserDebugCIDR(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "sql") || strings.Contains(w.Body.String(), "database") {
		t.Fatalf("response leaked a raw backend error: %s", w.Body.String())
	}
}
