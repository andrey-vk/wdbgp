package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

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
	srv.cfg.AllowDynamicPeers = false
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
	if _, err := st.DB.ExecContext(ctx, `INSERT INTO catalog_entries(feed_id, category, service, cidr) VALUES
		(?, 'Cat1', 'Svc1', '10.0.0.0/24'),
		(?, 'Cat2', 'Svc2', '10.1.0.0/24')`,
		f1ID, f2ID); err != nil {
		t.Fatalf("insert catalog entries: %v", err)
	}

	// --- Step 6: save selections for both Cat1::Svc1 and Cat2::Svc2 ---
	// Build a session cookie so requireUser middleware can authenticate.
	sessionToken := userSessionToken(srv.cfg.SessionSecret, userID)
	selBody := `{"categories":[{"category":"Cat1","checked":true}],"services":[{"category":"Cat1","service":"Svc1","checked":true},{"category":"Cat2","service":"Svc2","checked":true}]}`
	req = httptest.NewRequest("POST", "/api/user/selections", strings.NewReader(selBody))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "10.0.0.1:12345"
	req.AddCookie(&http.Cookie{Name: "wdbgp_user", Value: sessionToken}) //nolint:gosec // test cookie
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
