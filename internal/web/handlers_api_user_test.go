package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/andrey-vk/wdbgp/internal/store"
)

// =============================================================================
// TestUserAuthBothRequiresBothFactors — web_auth=both requires IP + cookie,
// and disabled users are rejected regardless of auth mode.
// =============================================================================

func TestUserAuthBothRequiresBothFactors(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)
	ctx := context.Background()

	// Create a "both" user
	if err := srv.settings.AllowDynamicPeers.Set(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	userBody := `{"name":"both-user","peer_ip":"10.0.0.1","peer_asn":65001,"networks":["10.1.1.0/24"],"web_auth":"both","enabled":true}`
	req := httptest.NewRequest("POST", "/api/admin/users", strings.NewReader(userBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create both user: %d", w.Code)
	}
	// Add credentials
	credBody := `{"login":"both-login","password":"test"}` //nolint:gosec // test credentials, not real
	req = httptest.NewRequest("PUT", "/api/admin/users/1/credentials", strings.NewReader(credBody))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "1")
	w = httptest.NewRecorder()
	srv.apiUserCredentialsSet(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("set credentials: %d", w.Code)
	}

	// 1. Request from matching IP WITHOUT cookie → should fail (both requires cookie too)
	req = httptest.NewRequest("GET", "/api/user/me", nil)
	req.RemoteAddr = "10.1.1.1:1234"
	w = httptest.NewRecorder()
	srv.requireUser(srv.apiUserMe).ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("ip-only to both user: got %d, want 401", w.Code)
	}

	// 2. Disable the user, then try with matching IP → should fail
	if _, err := st.DB.ExecContext(ctx, "UPDATE users SET enabled = 0 WHERE id = 1"); err != nil {
		t.Fatalf("disable user: %v", err)
	}
	req = httptest.NewRequest("GET", "/api/user/me", nil)
	req.RemoteAddr = "10.1.1.1:1234"
	w = httptest.NewRecorder()
	srv.requireUser(srv.apiUserMe).ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("disabled both user ip-match: got %d, want 401", w.Code)
	}

	// 3. Create a "network" user that is disabled → should also be rejected
	netBody := `{"name":"net-user","peer_ip":"10.0.0.2","peer_asn":65002,"networks":["10.2.2.0/24"],"web_auth":"network","enabled":false}`
	req = httptest.NewRequest("POST", "/api/admin/users", strings.NewReader(netBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	// Try accessing from matching IP
	req = httptest.NewRequest("GET", "/api/user/me", nil)
	req.RemoteAddr = "10.2.2.1:1234"
	w = httptest.NewRecorder()
	srv.requireUser(srv.apiUserMe).ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("disabled network user: got %d, want 401", w.Code)
	}
}

// =============================================================================
// TestUserAuthLoginModeCookieOnly — a web_auth=login user must be able to
// authenticate purely via session cookie, from an IP that does not match any
// of the user's networks (or when the user has no networks at all). This
// guards against a variable-shadowing regression in requireUser where the
// cookie-authenticated user was never actually read into the switch
// statement, making cookie-only auth silently fail for "login"/"any"/"both".
// =============================================================================

func TestUserAuthLoginModeCookieOnly(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)

	userBody := `{"name":"login-user","peer_ip":"10.9.9.9","peer_asn":65009,"web_auth":"login","enabled":true}`
	req := httptest.NewRequest("POST", "/api/admin/users", strings.NewReader(userBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create login user: %d body=%s", w.Code, w.Body.String())
	}
	var userResp userJSON
	if err := json.NewDecoder(w.Body).Decode(&userResp); err != nil {
		t.Fatal(err)
	}

	// Build a session cookie as if the user had just logged in via
	// POST /api/user/login, then call a protected endpoint from an
	// unrelated IP that cannot possibly satisfy IP-based auth.
	sessionToken := userSessionToken(srv.settings.SessionSecret.Get(), userResp.ID)
	req = httptest.NewRequest("GET", "/api/user/me", nil)
	req.RemoteAddr = "203.0.113.5:1234"
	req.AddCookie(&http.Cookie{Name: "wdbgp_user", Value: sessionToken}) //nolint:gosec // test cookie
	addCSRF(req)
	w = httptest.NewRecorder()
	srv.requireUser(srv.apiUserMe).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("cookie-only auth for web_auth=login user: got %d, want 200, body=%s", w.Code, w.Body.String())
	}
}

// =============================================================================
// TestRequireUserSelfHealCSRFCookieHonorsProxyHTTPS /
// TestUserLogoutCookiesSecureBehindProxy — requireUser's CSRF self-heal and
// apiUserLogout used to compute Secure via a bare r.TLS != nil check, unlike
// apiUserLogin (and every admin cookie path) which uses adminCookieSecure —
// the helper that also honors TrustProxyHeaders + X-Forwarded-Proto for
// deployments terminating TLS at a reverse proxy, where r.TLS is always nil
// inside the Go process. Behind such a proxy, the old code would write/clear
// a non-Secure cookie while login correctly wrote a Secure one.
// =============================================================================

func TestRequireUserSelfHealCSRFCookieHonorsProxyHTTPS(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)

	// "auto" (neither explicit true nor false) so the check must fall
	// through to the TrustProxyHeaders/X-Forwarded-Proto branch, same as
	// adminCookieSecure — testSettings() defaults this to "true", which
	// would short-circuit before ever exercising that branch.
	mustSetSetting(t, srv.settings.AdminCookieSecure, "auto")
	mustSetSetting(t, srv.settings.TrustProxyHeaders, true)

	userBody := `{"name":"proxy-user","peer_ip":"10.5.5.5","peer_asn":65005,"networks":["10.5.5.0/24"],"web_auth":"network","enabled":true}`
	req := httptest.NewRequest("POST", "/api/admin/users", strings.NewReader(userBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create user: %d body=%s", w.Code, w.Body.String())
	}

	// No r.TLS set (as if TLS were terminated at a reverse proxy) — only
	// the X-Forwarded-Proto header signals HTTPS. No CSRF cookie on the
	// request, so requireUser's self-heal must issue one.
	req = httptest.NewRequest("GET", "/api/user/me", nil)
	req.RemoteAddr = "10.5.5.1:1234"
	req.Header.Set("X-Forwarded-Proto", "https")
	w = httptest.NewRecorder()
	srv.requireUser(srv.apiUserMe).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("requireUser: got %d, want 200, body=%s", w.Code, w.Body.String())
	}

	var csrfCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == csrfCookieName {
			csrfCookie = c
		}
	}
	if csrfCookie == nil {
		t.Fatal("expected a self-healed CSRF cookie in the response")
	}
	if !csrfCookie.Secure {
		t.Error("self-healed CSRF cookie must be Secure when behind a trusted TLS-terminating proxy (X-Forwarded-Proto: https)")
	}
}

func TestUserLogoutCookiesSecureBehindProxy(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)

	mustSetSetting(t, srv.settings.AdminCookieSecure, "auto")
	mustSetSetting(t, srv.settings.TrustProxyHeaders, true)

	req := httptest.NewRequest("POST", "/api/user/logout", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	srv.apiUserLogout(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("logout: got %d, want 200, body=%s", w.Code, w.Body.String())
	}

	secure := map[string]bool{}
	for _, c := range w.Result().Cookies() {
		secure[c.Name] = c.Secure
	}
	if !secure[userSessionCookieName] {
		t.Error("wdbgp_user clearing cookie must be Secure when behind a trusted TLS-terminating proxy")
	}
	if !secure[csrfCookieName] {
		t.Error("wdbgp_csrf clearing cookie must be Secure when behind a trusted TLS-terminating proxy")
	}
}

// =============================================================================
// TestUserLoginSessionCookieSentinel — session_max_age=0 must produce a
// browser-session-scoped wdbgp_user cookie (no Max-Age/Expires), matching
// apiAdminLogin's handling of the same sentinel, instead of always issuing a
// persistent 28800s cookie.
// =============================================================================

func TestUserLoginSessionCookieSentinel(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)
	srv.loginLimiter = newRateLimiter(time.Minute, 1000)

	mustSetSetting(t, srv.settings.SessionMaxAge, 0)

	userBody := `{"name":"login-user2","peer_ip":"10.9.9.10","peer_asn":65010,"web_auth":"login","enabled":true}`
	req := httptest.NewRequest("POST", "/api/admin/users", strings.NewReader(userBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create login user: %d body=%s", w.Code, w.Body.String())
	}

	credBody := `{"login":"session-login","password":"test"}` //nolint:gosec // test credentials, not real
	req = httptest.NewRequest("PUT", "/api/admin/users/1/credentials", strings.NewReader(credBody))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "1")
	w = httptest.NewRecorder()
	srv.apiUserCredentialsSet(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("set credentials: %d", w.Code)
	}

	loginBody := `{"login":"session-login","password":"test"}` //nolint:gosec // test credentials, not real
	req = httptest.NewRequest("POST", "/api/user/login", strings.NewReader(loginBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.apiUserLogin(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("login: %d body=%s", w.Code, w.Body.String())
	}

	var sessionCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == userSessionCookieName {
			sessionCookie = c
		}
	}
	if sessionCookie == nil {
		t.Fatal("no wdbgp_user cookie set")
	}
	if sessionCookie.MaxAge != 0 {
		t.Errorf("MaxAge = %d, want 0 (browser-session cookie) when session_max_age=0", sessionCookie.MaxAge)
	}
	if !sessionCookie.Expires.IsZero() {
		t.Errorf("Expires = %v, want zero (browser-session cookie) when session_max_age=0", sessionCookie.Expires)
	}
}

// =============================================================================
// TestUserSwitchModeSurfacesReconcileFailure — apiUserSwitchMode must not
// report {ok:true} when the catalog_mode_id write succeeds but the
// subsequent BGP reconcile fails, matching apiUserSaveSelections and
// apiUserSaveFilters' "saved but BGP reconciliation failed" behavior.
// =============================================================================

func TestUserSwitchModeSurfacesReconcileFailure(t *testing.T) {
	srv, st, fake := setupUserTestServer(t)
	ctx := context.Background()

	newModeID, err := st.AddCatalogMode(ctx, "New Mode", true)
	if err != nil {
		t.Fatalf("add mode: %v", err)
	}

	userBody := `{"name":"mode-switch-user","peer_ip":"10.4.4.1","peer_asn":65004,"networks":["10.4.4.0/24"],"web_auth":"network","enabled":true,"catalog_editable":true}`
	req := httptest.NewRequest("POST", "/api/admin/users", strings.NewReader(userBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create user: %d body=%s", w.Code, w.Body.String())
	}
	var userResp userJSON
	if err := json.NewDecoder(w.Body).Decode(&userResp); err != nil {
		t.Fatal(err)
	}

	fake.down = true
	fake.downErr = fmt.Errorf("bgp speaker is not running")

	body := fmt.Sprintf(`{"mode_id":%d}`, newModeID)
	req = httptest.NewRequest("PUT", "/api/user/mode", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "10.4.4.1:1234"
	addCSRF(req)
	w = httptest.NewRecorder()
	srv.requireUser(srv.apiUserSwitchMode).ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		t.Fatalf("status = %d, want non-200 when reconcile fails, body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "BGP reconciliation failed") {
		t.Errorf("body = %s, want it to mention the reconcile failure", w.Body.String())
	}

	updated, err := st.User(ctx, userResp.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if updated.CatalogModeID != newModeID {
		t.Fatalf("catalog_mode_id = %d, want %d (write should persist despite reconcile failure)", updated.CatalogModeID, newModeID)
	}
}

// =============================================================================
// TestUserSaveFiltersRejectsInvalidCIDR — apiUserSaveFilters must not persist
// a malformed CIDR. SetUserRouteFilters -> insertRouteFilters already runs
// every value through NormalizeRouteFilters before inserting, so this locks
// in behavior that already exists rather than testing a gap.
// =============================================================================

func TestUserSaveFiltersRejectsInvalidCIDR(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)
	ctx := context.Background()

	userBody := `{"name":"filter-user","peer_ip":"10.0.0.1","peer_asn":65001,"networks":["10.5.5.0/24"],"web_auth":"network","enabled":true,"filter_editable":true}`
	req := httptest.NewRequest("POST", "/api/admin/users", strings.NewReader(userBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create user: %d body=%s", w.Code, w.Body.String())
	}

	body := `{"allow":["10.0.0.0/33"],"deny":[]}`
	req = httptest.NewRequest("POST", "/api/user/filters", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "10.5.5.1:1234"
	w = httptest.NewRecorder()
	srv.requireUser(srv.apiUserSaveFilters).ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		t.Fatalf("expected an error status for invalid CIDR, got 200")
	}

	filters, err := st.UserRouteFilters(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(filters.Allow) != 0 {
		t.Errorf("invalid CIDR should not have been persisted, got Allow=%v", filters.Allow)
	}
}

// =============================================================================
// TestUserMeReturnsLowercaseFilterKeys — apiUserMe embeds store.RouteFilters
// directly in the response; the user SPA reads userData.filters?.allow and
// ?.deny (lowercase). Guards against the untagged-struct regression where
// the wire JSON came back as "Allow"/"Deny" and the filter editor always
// rendered empty despite filters being set.
// =============================================================================

func TestUserMeReturnsLowercaseFilterKeys(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)
	ctx := context.Background()

	userBody := `{"name":"filter-user","peer_ip":"10.0.0.1","peer_asn":65001,"networks":["10.5.5.0/24"],"web_auth":"network","enabled":true,"filter_editable":true}`
	req := httptest.NewRequest("POST", "/api/admin/users", strings.NewReader(userBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create user: %d body=%s", w.Code, w.Body.String())
	}

	if err := st.SetUserRouteFilters(ctx, 1, store.RouteFilters{
		Allow: []string{"10.0.0.0/8"},
		Deny:  []string{"192.168.0.0/16"},
	}); err != nil {
		t.Fatal(err)
	}

	req = httptest.NewRequest("GET", "/api/user/me", nil)
	req.RemoteAddr = "10.5.5.1:1234"
	w = httptest.NewRecorder()
	srv.requireUser(srv.apiUserMe).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("apiUserMe: %d body=%s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	filters, ok := resp["filters"].(map[string]any)
	if !ok {
		t.Fatalf("filters field missing or wrong shape: %v", resp["filters"])
	}
	allow, ok := filters["allow"].([]any)
	if !ok || len(allow) != 1 || allow[0] != "10.0.0.0/8" {
		t.Errorf("filters.allow = %v, want [\"10.0.0.0/8\"]", filters["allow"])
	}
	deny, ok := filters["deny"].([]any)
	if !ok || len(deny) != 1 || deny[0] != "192.168.0.0/16" {
		t.Errorf("filters.deny = %v, want [\"192.168.0.0/16\"]", filters["deny"])
	}
}

// =============================================================================
// TestUserSaveSelectionsPreservesHidden — when a feed is disabled, its catalog
// items are not sent in the save-selections payload, but the user's existing
// selections for those hidden items must be preserved (not deleted).
// =============================================================================

func TestUserSaveSelectionsPreservesHidden(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)
	ctx := context.Background()

	// --- Step 1: create a new catalog mode ---
	createModeBody := strings.NewReader(`{"name":"Hidden Test","enabled":true}`)
	req := httptest.NewRequest("POST", "/api/admin/modes", createModeBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiModesCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create mode: %d, body=%s", w.Code, w.Body.String())
	}
	var created modeJSON
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	modeID := created.ID

	// --- Step 2: create two feeds (f1 enabled, f2 disabled) ---
	createF1 := strings.NewReader(`{"name":"f1","url":"https://example.test/f1","adapter_id":1,"enabled":true}`)
	req = httptest.NewRequest("POST", "/api/admin/feeds", createF1)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.apiFeedsCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create f1: %d, body=%s", w.Code, w.Body.String())
	}
	var f1Resp feedJSON
	if err := json.NewDecoder(w.Body).Decode(&f1Resp); err != nil {
		t.Fatal(err)
	}
	f1ID := f1Resp.ID

	createF2 := strings.NewReader(`{"name":"f2","url":"https://example.test/f2","adapter_id":1,"enabled":false}`)
	req = httptest.NewRequest("POST", "/api/admin/feeds", createF2)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.apiFeedsCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create f2: %d, body=%s", w.Code, w.Body.String())
	}
	var f2Resp feedJSON
	if err := json.NewDecoder(w.Body).Decode(&f2Resp); err != nil {
		t.Fatal(err)
	}
	f2ID := f2Resp.ID

	// --- Step 3: assign both feeds to mode ---
	assignBody := strings.NewReader(`{"feed_ids":[` + formatInt(f1ID) + `,` + formatInt(f2ID) + `]}`)
	req = httptest.NewRequest("PUT", "/api/admin/modes/"+formatInt(modeID)+"/feeds", assignBody)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", formatInt(modeID))
	w = httptest.NewRecorder()
	srv.apiModeFeedsSet(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("assign feeds to mode: %d, body=%s", w.Code, w.Body.String())
	}

	// --- Step 4: create a user with web_auth=any, assigned to this mode ---
	userBody := `{"name":"hidden-user","peer_ip":"10.0.0.1","peer_asn":65001,"catalog_mode_id":` + formatInt(modeID) + `,"web_auth":"any","enabled":true,"networks":["10.0.0.0/8"]}`
	req = httptest.NewRequest("POST", "/api/admin/users", strings.NewReader(userBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create user: %d, body=%s", w.Code, w.Body.String())
	}
	var userResp userJSON
	if err := json.NewDecoder(w.Body).Decode(&userResp); err != nil {
		t.Fatal(err)
	}
	userID := userResp.ID

	// --- Step 5: insert catalog entries for both feeds ---
	if err := st.InsertCatalogEntries(ctx, f1ID, []store.CatalogEntry{
		{Category: "Cat1", Service: "Svc1", CIDR: "10.0.0.0/24"},
	}); err != nil {
		t.Fatalf("insert catalog entries: %v", err)
	}
	if err := st.InsertCatalogEntries(ctx, f2ID, []store.CatalogEntry{
		{Category: "Cat2", Service: "Svc2", CIDR: "10.1.0.0/24"},
	}); err != nil {
		t.Fatalf("insert catalog entries: %v", err)
	}

	// --- Step 6: save selections for both Cat1::Svc1 and Cat2::Svc2 ---
	// Build a session cookie so requireUser middleware can authenticate.
	sessionToken := userSessionToken(srv.settings.SessionSecret.Get(), userID)
	selBody := `{"categories":[{"category":"Cat1","checked":true}],"services":[{"category":"Cat1","service":"Svc1","checked":true},{"category":"Cat2","service":"Svc2","checked":true}]}`
	req = httptest.NewRequest("POST", "/api/user/selections", strings.NewReader(selBody))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "10.0.0.1:12345"
	req.AddCookie(&http.Cookie{Name: "wdbgp_user", Value: sessionToken}) //nolint:gosec // test cookie
	addCSRF(req)
	w = httptest.NewRecorder()
	srv.requireUser(srv.apiUserSaveSelections).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("save initial selections: %d, body=%s", w.Code, w.Body.String())
	}

	// Verify both are saved.
	cats, svcs, err := st.UserModeSelection(ctx, userID, modeID)
	if err != nil {
		t.Fatal(err)
	}
	if !cats["Cat1"] {
		t.Fatal("Cat1 not selected after initial save")
	}
	if !svcs[store.ServiceKey{Category: "Cat1", Service: "Svc1"}] {
		t.Fatal("Cat1::Svc1 not selected after initial save")
	}
	if !svcs[store.ServiceKey{Category: "Cat2", Service: "Svc2"}] {
		t.Fatal("Cat2::Svc2 not selected after initial save")
	}

	// --- Step 7: disable feed2 ---
	disableBody := `{"name":"f2","url":"https://example.test/f2","adapter_id":1,"enabled":false}`
	req = httptest.NewRequest("PUT", "/api/admin/feeds/"+formatInt(f2ID), strings.NewReader(disableBody))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", formatInt(f2ID))
	w = httptest.NewRecorder()
	srv.apiFeedsUpdate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("disable feed2: %d, body=%s", w.Code, w.Body.String())
	}

	// --- Step 8: save selections with only Cat1::Svc1 (checked=true).
	// Cat2::Svc2 is NOT in the payload because its feed is disabled and the UI
	// filters it out. The server must preserve Cat2::Svc2 in the DB.
	// ---
	selBody2 := `{"categories":[{"category":"Cat1","checked":true}],"services":[{"category":"Cat1","service":"Svc1","checked":true}]}`
	req = httptest.NewRequest("POST", "/api/user/selections", strings.NewReader(selBody2))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "10.0.0.1:12345"
	req.AddCookie(&http.Cookie{Name: "wdbgp_user", Value: sessionToken}) //nolint:gosec // test cookie
	addCSRF(req)
	w = httptest.NewRecorder()
	srv.requireUser(srv.apiUserSaveSelections).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("save selections with hidden feed: %d, body=%s", w.Code, w.Body.String())
	}

	// --- Step 9: verify Cat1::Svc1 remains selected ---
	cats2, svcs2, err := st.UserModeSelection(ctx, userID, modeID)
	if err != nil {
		t.Fatal(err)
	}
	if !cats2["Cat1"] {
		t.Fatal("Cat1 was lost after save with hidden feed items")
	}
	if !svcs2[store.ServiceKey{Category: "Cat1", Service: "Svc1"}] {
		t.Fatal("Cat1::Svc1 was lost after save with hidden feed items")
	}

	// --- Step 10: verify Cat2::Svc2 REMAINS in DB (hidden, disabled feed,
	// not in submission, but preserved) ---
	if !svcs2[store.ServiceKey{Category: "Cat2", Service: "Svc2"}] {
		t.Fatal("Cat2::Svc2 was deleted — hidden selections from disabled feed must be preserved")
	}
}

func formatInt(n int64) string {
	return strconv.FormatInt(n, 10)
}
