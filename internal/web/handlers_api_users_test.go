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

	"github.com/andrey-vk/wdbgp/internal/store"
)

func setupUserTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(":memory:", false, "", false)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	})
	return s
}

func setupUserTestServer(t *testing.T) (*Server, *store.Store, *fakeBGP) {
	t.Helper()
	st := setupUserTestStore(t)
	bgp := &fakeBGP{}
	srv := &Server{
		settings: testSettings(),
		store:    st,
		bgp:      bgp,
	}
	return srv, st, bgp
}

// =============================================================================
// TestUsersListEmpty — GET /api/admin/users with empty DB
// =============================================================================

func TestUsersListEmpty(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)

	req := httptest.NewRequest("GET", "/api/admin/users", nil)
	w := httptest.NewRecorder()
	srv.apiUsersList(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Users []userJSON `json:"users"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Users) != 0 {
		t.Fatalf("users = %d, want 0", len(resp.Users))
	}
}

// =============================================================================
// TestUsersCRUD — create, read, update, delete lifecycle
// =============================================================================

func TestUsersCRUD(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)

	// --- CREATE ---
	createBody := strings.NewReader(`{"name":"test-user","peer_ip":"192.168.1.1","peer_asn":65001,"networks":["10.0.0.0/8"],"web_auth":"network","enabled":true}`)
	req := httptest.NewRequest("POST", "/api/admin/users", createBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiUsersCreate(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201, body=%s", w.Code, w.Body.String())
	}
	var created userJSON
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("created user has no id")
	}
	if created.Name != "test-user" {
		t.Fatalf("name = %q, want test-user", created.Name)
	}
	if created.PeerIP != "192.168.1.1" {
		t.Fatalf("peer_ip = %q, want 192.168.1.1", created.PeerIP)
	}
	if created.HasPassword {
		t.Fatal("has_password should be false for user without password")
	}

	userID := created.ID

	// --- LIST ---
	req = httptest.NewRequest("GET", "/api/admin/users", nil)
	w = httptest.NewRecorder()
	srv.apiUsersList(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("list: status = %d, want 200", w.Code)
	}
	var listResp struct {
		Users []userJSON `json:"users"`
	}
	if err := json.NewDecoder(w.Body).Decode(&listResp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listResp.Users) != 1 {
		t.Fatalf("list users count = %d, want 1", len(listResp.Users))
	}
	if listResp.Users[0].ID != userID {
		t.Fatalf("list user id = %d, want %d", listResp.Users[0].ID, userID)
	}

	// --- GET single ---
	req = httptest.NewRequest("GET", "/api/admin/users/1", nil)
	req.SetPathValue("id", "1")
	w = httptest.NewRecorder()
	srv.apiUsersGet(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("get single: status = %d, want 200", w.Code)
	}
	var single userJSON
	if err := json.NewDecoder(w.Body).Decode(&single); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	if single.ID != userID {
		t.Fatalf("get id = %d, want %d", single.ID, userID)
	}
	if single.Name != "test-user" {
		t.Fatalf("get name = %q, want test-user", single.Name)
	}

	// --- UPDATE ---
	updateBody := strings.NewReader(`{"name":"updated-user","peer_ip":"192.168.1.1","peer_asn":65001,"networks":["10.0.0.0/8"],"web_auth":"network","enabled":true}`)
	req = httptest.NewRequest("PUT", "/api/admin/users/1", updateBody)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "1")
	w = httptest.NewRecorder()
	srv.apiUsersUpdate(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("update: status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var updated userJSON
	if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
		t.Fatalf("decode update response: %v", err)
	}
	if updated.Name != "updated-user" {
		t.Fatalf("updated name = %q, want updated-user", updated.Name)
	}

	// --- DELETE ---
	req = httptest.NewRequest("DELETE", "/api/admin/users/1", nil)
	req.SetPathValue("id", "1")
	w = httptest.NewRecorder()
	srv.apiUsersDelete(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("delete: status = %d, want 200", w.Code)
	}

	// Verify list empty after delete
	req = httptest.NewRequest("GET", "/api/admin/users", nil)
	w = httptest.NewRecorder()
	srv.apiUsersList(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("list after delete: status = %d", w.Code)
	}
	if err := json.NewDecoder(w.Body).Decode(&listResp); err != nil {
		t.Fatalf("decode list after delete: %v", err)
	}
	if len(listResp.Users) != 0 {
		t.Fatalf("users after delete = %d, want 0", len(listResp.Users))
	}
}

// =============================================================================
// TestUsersCreateWithPassword — POST with bgp_password
// =============================================================================

func TestUsersCreateWithPassword(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)

	createBody := strings.NewReader(`{"name":"pw-user","peer_ip":"10.0.0.1","peer_asn":65002,"bgp_password":"secret","password_enabled":true,"networks":["10.0.0.0/8"],"web_auth":"network","enabled":true}`)
	req := httptest.NewRequest("POST", "/api/admin/users", createBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiUsersCreate(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201, body=%s", w.Code, w.Body.String())
	}
	var created userJSON
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !created.HasPassword {
		t.Fatal("has_password should be true")
	}

	// Verify password value is never in response
	body := w.Body.String()
	if strings.Contains(body, "secret") {
		t.Fatal("response must not contain the password value")
	}
	if strings.Contains(body, "bgp_password") {
		t.Fatal("response must not contain bgp_password field")
	}

	// GET should also not expose password
	req = httptest.NewRequest("GET", "/api/admin/users/1", nil)
	req.SetPathValue("id", "1")
	w = httptest.NewRecorder()
	srv.apiUsersGet(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("get: status = %d, want 200", w.Code)
	}
	var got userJSON
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	if !got.HasPassword {
		t.Fatal("has_password should be true on GET")
	}
	body = w.Body.String()
	if strings.Contains(body, "secret") {
		t.Fatal("GET response must not contain the password value")
	}
}

// =============================================================================
// TestUsersCreateWithNetworks — POST with networks array
// =============================================================================

func TestUsersCreateWithNetworks(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)

	createBody := strings.NewReader(`{"name":"net-user","peer_ip":"192.168.2.1","peer_asn":65003,"networks":["10.0.0.0/8","192.168.0.0/16"],"web_auth":"network","enabled":true}`)
	req := httptest.NewRequest("POST", "/api/admin/users", createBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiUsersCreate(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201, body=%s", w.Code, w.Body.String())
	}
	var created userJSON
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(created.Networks) != 2 {
		t.Fatalf("networks count = %d, want 2", len(created.Networks))
	}
	if created.Networks[0] != "10.0.0.0/8" {
		t.Fatalf("networks[0] = %q, want 10.0.0.0/8", created.Networks[0])
	}
	if created.Networks[1] != "192.168.0.0/16" {
		t.Fatalf("networks[1] = %q, want 192.168.0.0/16", created.Networks[1])
	}

	// GET verifies networks
	req = httptest.NewRequest("GET", "/api/admin/users/1", nil)
	req.SetPathValue("id", "1")
	w = httptest.NewRecorder()
	srv.apiUsersGet(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("get: status = %d, want 200", w.Code)
	}
	var got userJSON
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	if len(got.Networks) != 2 {
		t.Fatalf("get networks count = %d, want 2", len(got.Networks))
	}
}

// =============================================================================
// TestUsersCreateValidation — required fields
// =============================================================================

func TestUsersCreateValidation(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)

	// Empty name
	body := strings.NewReader(`{"name":"","peer_ip":"192.168.1.1","peer_asn":65001,"networks":["10.0.0.0/8"],"web_auth":"network","enabled":true}`)
	req := httptest.NewRequest("POST", "/api/admin/users", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("empty name: status = %d, want 400, body=%s", w.Code, w.Body.String())
	}

	// Invalid peer_ip (not a valid IP)
	body = strings.NewReader(`{"name":"test","peer_ip":"not-an-ip","peer_asn":65001,"networks":["10.0.0.0/8"],"web_auth":"network","enabled":true}`)
	req = httptest.NewRequest("POST", "/api/admin/users", body)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid peer_ip: status = %d, want 400, body=%s", w.Code, w.Body.String())
	}

	// Empty peer_ip
	body = strings.NewReader(`{"name":"test","peer_ip":"","peer_asn":65001,"networks":["10.0.0.0/8"],"web_auth":"network","enabled":true}`)
	req = httptest.NewRequest("POST", "/api/admin/users", body)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("empty peer_ip: status = %d, want 400, body=%s", w.Code, w.Body.String())
	}

	// Empty networks
	body = strings.NewReader(`{"name":"test","peer_ip":"192.168.1.1","peer_asn":65001,"networks":[],"web_auth":"network","enabled":true}`)
	req = httptest.NewRequest("POST", "/api/admin/users", body)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("empty networks: status = %d, want 400, body=%s", w.Code, w.Body.String())
	}
}

// =============================================================================
// TestUsersCreateAnyModeAllowsEmptyNetworks — web_auth="any" authenticates
// by IP match OR credentials, so unlike "network"/"both" it must not require
// a network — matches the exemption "login" already gets.
// =============================================================================

func TestUsersCreateAnyModeAllowsEmptyNetworks(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)

	body := strings.NewReader(`{"name":"any-user","peer_ip":"192.168.9.1","peer_asn":65009,"networks":[],"web_auth":"any","enabled":true}`)
	req := httptest.NewRequest("POST", "/api/admin/users", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("any mode, no networks: status = %d, want 201, body=%s", w.Code, w.Body.String())
	}
}

// TestUsersCreateBothModeRequiresNetworks confirms "both" is not
// accidentally exempted alongside "any" — it genuinely needs IP match.
func TestUsersCreateBothModeRequiresNetworks(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)

	body := strings.NewReader(`{"name":"both-user","peer_ip":"192.168.9.2","peer_asn":65010,"networks":[],"web_auth":"both","enabled":true}`)
	req := httptest.NewRequest("POST", "/api/admin/users", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("both mode, no networks: status = %d, want 400, body=%s", w.Code, w.Body.String())
	}
}

// TestUsersUpdateNetworkModeRequiresNetworks guards a gap where
// apiUsersUpdate never enforced this at all — a user could be switched to
// web_auth="network" with zero networks via PUT, permanently unable to
// authenticate, with no server-side pushback.
func TestUsersUpdateNetworkModeRequiresNetworks(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)

	createBody := strings.NewReader(`{"name":"update-user","peer_ip":"192.168.9.3","peer_asn":65011,"networks":["10.0.0.0/8"],"web_auth":"network","enabled":true}`)
	req := httptest.NewRequest("POST", "/api/admin/users", createBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201, body=%s", w.Code, w.Body.String())
	}
	var created userJSON
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}

	updateBody := strings.NewReader(`{"networks":[]}`)
	req = httptest.NewRequest("PUT", fmt.Sprintf("/api/admin/users/%d", created.ID), updateBody)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", strconv.FormatInt(created.ID, 10))
	w = httptest.NewRecorder()
	srv.apiUsersUpdate(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("update to empty networks on network mode: status = %d, want 400, body=%s", w.Code, w.Body.String())
	}
}

// TestUsersUpdateAnyModeAllowsEmptyNetworks confirms the update path
// doesn't wrongly demand a network for "any" either.
func TestUsersUpdateAnyModeAllowsEmptyNetworks(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)

	createBody := strings.NewReader(`{"name":"update-any-user","peer_ip":"192.168.9.4","peer_asn":65012,"networks":["10.0.0.0/8"],"web_auth":"network","enabled":true}`)
	req := httptest.NewRequest("POST", "/api/admin/users", createBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201, body=%s", w.Code, w.Body.String())
	}
	var created userJSON
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}

	updateBody := strings.NewReader(`{"web_auth":"any","networks":[]}`)
	req = httptest.NewRequest("PUT", fmt.Sprintf("/api/admin/users/%d", created.ID), updateBody)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", strconv.FormatInt(created.ID, 10))
	w = httptest.NewRecorder()
	srv.apiUsersUpdate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update to any mode, empty networks: status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
}

// =============================================================================
// TestUsersUpdatePasswordToggle — password_enabled
// =============================================================================

func TestUsersUpdatePasswordToggle(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)

	// Create user with password
	createBody := strings.NewReader(`{"name":"pw-toggle","peer_ip":"10.0.1.1","peer_asn":65100,"bgp_password":"secret123","password_enabled":true,"networks":["10.0.0.0/8"],"web_auth":"network","enabled":true}`)
	req := httptest.NewRequest("POST", "/api/admin/users", createBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201", w.Code)
	}

	// GET → has_password=true
	req = httptest.NewRequest("GET", "/api/admin/users/1", nil)
	req.SetPathValue("id", "1")
	w = httptest.NewRecorder()
	srv.apiUsersGet(w, req)
	var u userJSON
	if err := json.NewDecoder(w.Body).Decode(&u); err != nil {
		t.Fatal(err)
	}
	if !u.HasPassword {
		t.Fatal("has_password should be true after create")
	}

	// PUT with password_enabled=false → clears password
	updateBody := strings.NewReader(`{"name":"pw-toggle","peer_ip":"10.0.1.1","peer_asn":65100,"password_enabled":false,"networks":["10.0.0.0/8"],"web_auth":"network","enabled":true}`)
	req = httptest.NewRequest("PUT", "/api/admin/users/1", updateBody)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "1")
	w = httptest.NewRecorder()
	srv.apiUsersUpdate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update clear pw: status = %d, want 200, body=%s", w.Code, w.Body.String())
	}

	// GET → has_password=false
	req = httptest.NewRequest("GET", "/api/admin/users/1", nil)
	req.SetPathValue("id", "1")
	w = httptest.NewRecorder()
	srv.apiUsersGet(w, req)
	if err := json.NewDecoder(w.Body).Decode(&u); err != nil {
		t.Fatal(err)
	}
	if u.HasPassword {
		t.Fatal("has_password should be false after password_enabled=false")
	}

	// PUT with password_enabled=true and new password → sets password
	updateBody = strings.NewReader(`{"name":"pw-toggle","peer_ip":"10.0.1.1","peer_asn":65100,"password_enabled":true,"bgp_password":"newsecret","networks":["10.0.0.0/8"],"web_auth":"network","enabled":true}`)
	req = httptest.NewRequest("PUT", "/api/admin/users/1", updateBody)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "1")
	w = httptest.NewRecorder()
	srv.apiUsersUpdate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update set pw: status = %d, want 200, body=%s", w.Code, w.Body.String())
	}

	// GET → has_password=true
	req = httptest.NewRequest("GET", "/api/admin/users/1", nil)
	req.SetPathValue("id", "1")
	w = httptest.NewRecorder()
	srv.apiUsersGet(w, req)
	if err := json.NewDecoder(w.Body).Decode(&u); err != nil {
		t.Fatal(err)
	}
	if !u.HasPassword {
		t.Fatal("has_password should be true after re-enabling")
	}
}

// =============================================================================
// TestUsersCredentialsLifecycle — credential management
// =============================================================================

func TestUsersCredentialsLifecycle(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)

	// Create user
	createBody := strings.NewReader(`{"name":"cred-user","peer_ip":"10.0.2.1","peer_asn":65101,"networks":["10.0.0.0/8"],"web_auth":"network","enabled":true}`)
	req := httptest.NewRequest("POST", "/api/admin/users", createBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201", w.Code)
	}

	// PUT /credentials with login and password
	credBody := strings.NewReader(`{"login":"test@test.com","password":"pw"}`)
	req = httptest.NewRequest("PUT", "/api/admin/users/1/credentials", credBody)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "1")
	w = httptest.NewRecorder()
	srv.apiUserCredentialsSet(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("set credentials: status = %d, want 200, body=%s", w.Code, w.Body.String())
	}

	// GET /credentials → should have login but NO password hash
	req = httptest.NewRequest("GET", "/api/admin/users/1/credentials", nil)
	req.SetPathValue("id", "1")
	w = httptest.NewRecorder()
	srv.apiUserCredentialsList(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list credentials: status = %d, want 200", w.Code)
	}
	var credListResp struct {
		Credentials []userCredentialJSON `json:"credentials"`
	}
	if err := json.NewDecoder(w.Body).Decode(&credListResp); err != nil {
		t.Fatalf("decode credentials: %v", err)
	}
	if len(credListResp.Credentials) != 1 {
		t.Fatalf("credentials count = %d, want 1", len(credListResp.Credentials))
	}
	if credListResp.Credentials[0].Login != "test@test.com" {
		t.Fatalf("login = %q, want test@test.com", credListResp.Credentials[0].Login)
	}
	// Ensure no password hash in response
	body := w.Body.String()
	if strings.Contains(body, "password_hash") || strings.Contains(body, "password") {
		t.Fatal("credentials response must not contain password or hash")
	}
	if strings.Contains(body, "pw") {
		t.Fatal("credentials response must not leak password value")
	}

	// DELETE /credentials
	deleteBody := strings.NewReader(`{"login":"test@test.com"}`)
	req = httptest.NewRequest("DELETE", "/api/admin/users/1/credentials", deleteBody)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "1")
	w = httptest.NewRecorder()
	srv.apiUserCredentialsDelete(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("delete credentials: status = %d, want 200, body=%s", w.Code, w.Body.String())
	}

	// GET /credentials → empty
	req = httptest.NewRequest("GET", "/api/admin/users/1/credentials", nil)
	req.SetPathValue("id", "1")
	w = httptest.NewRecorder()
	srv.apiUserCredentialsList(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list credentials after delete: status = %d", w.Code)
	}
	if err := json.NewDecoder(w.Body).Decode(&credListResp); err != nil {
		t.Fatalf("decode credentials: %v", err)
	}
	if len(credListResp.Credentials) != 0 {
		t.Fatalf("credentials after delete = %d, want 0", len(credListResp.Credentials))
	}
}

// =============================================================================
// TestUsersPeerState — peer state endpoint
// =============================================================================

func TestUsersPeerState(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)

	// Create user with peer_ip="172.16.0.2" (mock returns ESTABLISHED for this IP)
	createBody := strings.NewReader(`{"name":"peer-state","peer_ip":"172.16.0.2","peer_asn":65001,"networks":["10.0.0.0/8"],"web_auth":"network","enabled":true}`)
	req := httptest.NewRequest("POST", "/api/admin/users", createBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201", w.Code)
	}

	// GET /peer-state → should be ESTABLISHED (fakeBGP returns this for 172.16.0.2)
	req = httptest.NewRequest("GET", "/api/admin/users/1/peer-state", nil)
	req.SetPathValue("id", "1")
	w = httptest.NewRecorder()
	srv.apiUserPeerState(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("peer-state: status = %d, want 200", w.Code)
	}
	var stateResp struct {
		State string `json:"state"`
	}
	if err := json.NewDecoder(w.Body).Decode(&stateResp); err != nil {
		t.Fatalf("decode peer-state: %v", err)
	}
	if stateResp.State != "ESTABLISHED" {
		t.Fatalf("peer state = %q, want ESTABLISHED", stateResp.State)
	}

	// Create another user with different IP → peer state should be empty
	createBody = strings.NewReader(`{"name":"peer-unknown","peer_ip":"10.99.99.99","peer_asn":65002,"networks":["192.168.100.0/24"],"web_auth":"network","enabled":true}`)
	req = httptest.NewRequest("POST", "/api/admin/users", createBody)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create second: status = %d, want 201", w.Code)
	}

	req = httptest.NewRequest("GET", "/api/admin/users/2/peer-state", nil)
	req.SetPathValue("id", "2")
	w = httptest.NewRecorder()
	srv.apiUserPeerState(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("peer-state 2: status = %d, want 200", w.Code)
	}
	if err := json.NewDecoder(w.Body).Decode(&stateResp); err != nil {
		t.Fatalf("decode peer-state 2: %v", err)
	}
	if stateResp.State != "" {
		t.Fatalf("peer state for unknown IP = %q, want empty", stateResp.State)
	}

	// Verify list response includes peer_state
	req = httptest.NewRequest("GET", "/api/admin/users", nil)
	w = httptest.NewRecorder()
	srv.apiUsersList(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list: status = %d", w.Code)
	}
	var listResp struct {
		Users []userJSON `json:"users"`
	}
	if err := json.NewDecoder(w.Body).Decode(&listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listResp.Users) != 2 {
		t.Fatalf("list count = %d, want 2", len(listResp.Users))
	}
	foundEstablished := false
	foundEmpty := false
	for _, u := range listResp.Users {
		if u.PeerIP == "172.16.0.2" && u.PeerState == "ESTABLISHED" {
			foundEstablished = true
		}
		if u.PeerIP == "10.99.99.99" && u.PeerState == "" {
			foundEmpty = true
		}
	}
	if !foundEstablished {
		t.Fatal("list should show ESTABLISHED peer state for 172.16.0.2")
	}
	if !foundEmpty {
		t.Fatal("list should show empty peer state for unknown IP")
	}
}

// =============================================================================
// TestUsersStatuses — batch peer-status endpoint
// =============================================================================

func TestUsersStatuses(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)

	req := httptest.NewRequest("GET", "/api/admin/users/statuses", nil)
	w := httptest.NewRecorder()
	srv.apiUserStatuses(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("statuses: status = %d, want 200", w.Code)
	}

	var resp struct {
		PeerStates map[string]string `json:"peer_states"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode statuses: %v", err)
	}

	if resp.PeerStates["172.16.0.2:65001"] != "ESTABLISHED" {
		t.Fatalf("peer_states = %v, want ESTABLISHED for 172.16.0.2:65001", resp.PeerStates)
	}
}

// =============================================================================
// TestUsersNotFound — 404 cases
// =============================================================================

func TestUsersNotFound(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)

	// GET non-existent user
	req := httptest.NewRequest("GET", "/api/admin/users/99999", nil)
	req.SetPathValue("id", "99999")
	w := httptest.NewRecorder()
	srv.apiUsersGet(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("GET 404: status = %d, want 404", w.Code)
	}

	// PUT non-existent user
	updateBody := strings.NewReader(`{"name":"nope"}`)
	req = httptest.NewRequest("PUT", "/api/admin/users/99999", updateBody)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "99999")
	w = httptest.NewRecorder()
	srv.apiUsersUpdate(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("PUT 404: status = %d, want 404, body=%s", w.Code, w.Body.String())
	}

	// DELETE non-existent user
	req = httptest.NewRequest("DELETE", "/api/admin/users/99999", nil)
	req.SetPathValue("id", "99999")
	w = httptest.NewRecorder()
	srv.apiUsersDelete(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("DELETE 404: status = %d, want 404", w.Code)
	}
}

// =============================================================================
// TestUsersPartialUpdateEnabledOnly — PUT with only enabled field
// =============================================================================

func TestUsersPartialUpdateEnabledOnly(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)

	// Create user with enabled: true
	createBody := strings.NewReader(`{"name":"partial-enabled","peer_ip":"192.168.3.1","peer_asn":65004,"networks":["10.0.0.0/8"],"web_auth":"network","enabled":true}`)
	req := httptest.NewRequest("POST", "/api/admin/users", createBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201, body=%s", w.Code, w.Body.String())
	}

	// PUT only {"enabled": false}
	updateBody := strings.NewReader(`{"enabled": false}`)
	req = httptest.NewRequest("PUT", "/api/admin/users/1", updateBody)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "1")
	w = httptest.NewRecorder()
	srv.apiUsersUpdate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update: status = %d, want 200, body=%s", w.Code, w.Body.String())
	}

	// GET user → enabled is false, name unchanged
	req = httptest.NewRequest("GET", "/api/admin/users/1", nil)
	req.SetPathValue("id", "1")
	w = httptest.NewRecorder()
	srv.apiUsersGet(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get: status = %d, want 200", w.Code)
	}
	var got userJSON
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Enabled {
		t.Fatal("enabled should be false after partial update")
	}
	if got.Name != "partial-enabled" {
		t.Fatalf("name = %q, want partial-enabled", got.Name)
	}
}

// =============================================================================
// TestUsersPartialUpdateNameOnly — PUT with only name field
// =============================================================================

func TestUsersPartialUpdateNameOnly(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)

	// Create user
	createBody := strings.NewReader(`{"name":"Original","peer_ip":"192.168.4.1","peer_asn":65005,"networks":["10.0.0.0/8"],"web_auth":"network","enabled":true}`)
	req := httptest.NewRequest("POST", "/api/admin/users", createBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201, body=%s", w.Code, w.Body.String())
	}

	// PUT only {"name": "Renamed"}
	updateBody := strings.NewReader(`{"name":"Renamed"}`)
	req = httptest.NewRequest("PUT", "/api/admin/users/1", updateBody)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "1")
	w = httptest.NewRecorder()
	srv.apiUsersUpdate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update: status = %d, want 200, body=%s", w.Code, w.Body.String())
	}

	// GET user → name is "Renamed", all other fields unchanged
	req = httptest.NewRequest("GET", "/api/admin/users/1", nil)
	req.SetPathValue("id", "1")
	w = httptest.NewRecorder()
	srv.apiUsersGet(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get: status = %d, want 200", w.Code)
	}
	var got userJSON
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Name != "Renamed" {
		t.Fatalf("name = %q, want Renamed", got.Name)
	}
	if got.PeerIP != "192.168.4.1" {
		t.Fatalf("peer_ip = %q, want 192.168.4.1", got.PeerIP)
	}
	if got.PeerASN != 65005 {
		t.Fatalf("peer_asn = %d, want 65005", got.PeerASN)
	}
}

// =============================================================================
// TestUsersPartialUpdateFiltersOnly — PUT with only filter fields
// =============================================================================

func TestUsersPartialUpdateFiltersOnly(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)

	// Create user
	createBody := strings.NewReader(`{"name":"filter-user","peer_ip":"192.168.5.1","peer_asn":65006,"networks":["10.0.0.0/8"],"web_auth":"network","enabled":true}`)
	req := httptest.NewRequest("POST", "/api/admin/users", createBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201, body=%s", w.Code, w.Body.String())
	}

	// PUT only {"filter_deny": ["1.1.1.1/32"]}
	updateBody := strings.NewReader(`{"filter_deny":["1.1.1.1/32"]}`)
	req = httptest.NewRequest("PUT", "/api/admin/users/1", updateBody)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "1")
	w = httptest.NewRecorder()
	srv.apiUsersUpdate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update: status = %d, want 200, body=%s", w.Code, w.Body.String())
	}

	// Verify filters saved via store query
	filters, err := st.UserRouteFilters(context.Background(), 1)
	if err != nil {
		t.Fatalf("get filters: %v", err)
	}
	if len(filters.Deny) != 1 || filters.Deny[0] != "1.1.1.1/32" {
		t.Fatalf("deny filters = %v, want [1.1.1.1/32]", filters.Deny)
	}
}

// TestUsersPartialUpdateFilterOneSidePreservesTheOther guards against a bug
// where PUT with only filter_allow (or only filter_deny) silently wiped the
// omitted side, since SetUserRouteFilters replaces both unconditionally —
// the handler must load the existing filters first and only override the
// side actually present in the request body.
func TestUsersPartialUpdateFilterOneSidePreservesTheOther(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)

	createBody := strings.NewReader(`{"name":"filter-user-2","peer_ip":"192.168.5.2","peer_asn":65007,"networks":["10.0.0.0/8"],"web_auth":"network","enabled":true,"filter_allow":["8.8.8.0/24"],"filter_deny":["1.1.1.1/32"]}`)
	req := httptest.NewRequest("POST", "/api/admin/users", createBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201, body=%s", w.Code, w.Body.String())
	}
	var created userJSON
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}

	// PUT only filter_deny — filter_allow must survive untouched.
	updateBody := strings.NewReader(`{"filter_deny":["2.2.2.2/32"]}`)
	req = httptest.NewRequest("PUT", fmt.Sprintf("/api/admin/users/%d", created.ID), updateBody)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", strconv.FormatInt(created.ID, 10))
	w = httptest.NewRecorder()
	srv.apiUsersUpdate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update: status = %d, want 200, body=%s", w.Code, w.Body.String())
	}

	filters, err := st.UserRouteFilters(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(filters.Allow) != 1 || filters.Allow[0] != "8.8.8.0/24" {
		t.Fatalf("filter_allow = %v, want unchanged [8.8.8.0/24] — PUT with only filter_deny must not wipe it", filters.Allow)
	}
	if len(filters.Deny) != 1 || filters.Deny[0] != "2.2.2.2/32" {
		t.Fatalf("filter_deny = %v, want [2.2.2.2/32]", filters.Deny)
	}

	// PUT only filter_allow — filter_deny (just set above) must survive too.
	updateBody2 := strings.NewReader(`{"filter_allow":["9.9.9.0/24"]}`)
	req = httptest.NewRequest("PUT", fmt.Sprintf("/api/admin/users/%d", created.ID), updateBody2)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", strconv.FormatInt(created.ID, 10))
	w = httptest.NewRecorder()
	srv.apiUsersUpdate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update 2: status = %d, want 200, body=%s", w.Code, w.Body.String())
	}

	filters2, err := st.UserRouteFilters(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(filters2.Allow) != 1 || filters2.Allow[0] != "9.9.9.0/24" {
		t.Fatalf("filter_allow = %v, want [9.9.9.0/24]", filters2.Allow)
	}
	if len(filters2.Deny) != 1 || filters2.Deny[0] != "2.2.2.2/32" {
		t.Fatalf("filter_deny = %v, want unchanged [2.2.2.2/32] — PUT with only filter_allow must not wipe it", filters2.Deny)
	}
}

// =============================================================================
// TestUsersPartialUpdatePasswordOnly — PUT with only password fields
// =============================================================================

func TestUsersPartialUpdatePasswordOnly(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)

	// Create user with password
	createBody := strings.NewReader(`{"name":"pw-partial","peer_ip":"192.168.6.1","peer_asn":65007,"bgp_password":"oldpass","password_enabled":true,"networks":["10.0.0.0/8"],"web_auth":"network","enabled":true}`)
	req := httptest.NewRequest("POST", "/api/admin/users", createBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201, body=%s", w.Code, w.Body.String())
	}

	// PUT only {"bgp_password": "new", "password_enabled": true}
	updateBody := strings.NewReader(`{"bgp_password":"new","password_enabled":true}`)
	req = httptest.NewRequest("PUT", "/api/admin/users/1", updateBody)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "1")
	w = httptest.NewRecorder()
	srv.apiUsersUpdate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update: status = %d, want 200, body=%s", w.Code, w.Body.String())
	}

	// GET → has_password: true
	req = httptest.NewRequest("GET", "/api/admin/users/1", nil)
	req.SetPathValue("id", "1")
	w = httptest.NewRecorder()
	srv.apiUsersGet(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get: status = %d, want 200", w.Code)
	}
	var got userJSON
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !got.HasPassword {
		t.Fatal("has_password should be true after password update")
	}
}

// =============================================================================
// TestUsersPartialUpdateInvalidEmptyName — PUT name="" returns 400
// =============================================================================

func TestUsersPartialUpdateInvalidEmptyName(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)

	// Create user
	createBody := strings.NewReader(`{"name":"valid-name","peer_ip":"192.168.7.1","peer_asn":65008,"networks":["10.0.0.0/8"],"web_auth":"network","enabled":true}`)
	req := httptest.NewRequest("POST", "/api/admin/users", createBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201, body=%s", w.Code, w.Body.String())
	}

	// PUT only {"name": ""} → should return 400
	updateBody := strings.NewReader(`{"name":""}`)
	req = httptest.NewRequest("PUT", "/api/admin/users/1", updateBody)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "1")
	w = httptest.NewRecorder()
	srv.apiUsersUpdate(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("empty name: status = %d, want 400, body=%s", w.Code, w.Body.String())
	}
}

// =============================================================================
// TestUsersPartialUpdateEnabledToggle — PUT {"enabled": false} only
// =============================================================================

func TestUsersPartialUpdateEnabledToggle(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)

	// Create user
	createBody := strings.NewReader(`{"name":"toggle-user","peer_ip":"192.168.8.1","peer_asn":65009,"networks":["10.0.0.0/8"],"web_auth":"network","enabled":true}`)
	req := httptest.NewRequest("POST", "/api/admin/users", createBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201, body=%s", w.Code, w.Body.String())
	}

	// PUT only {"enabled": false} → should return 200
	updateBody := strings.NewReader(`{"enabled": false}`)
	req = httptest.NewRequest("PUT", "/api/admin/users/1", updateBody)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "1")
	w = httptest.NewRecorder()
	srv.apiUsersUpdate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("enabled toggle (no password): status = %d, want 200, body=%s", w.Code, w.Body.String())
	}

	var updated userJSON
	if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if updated.Enabled {
		t.Fatal("enabled should be false after toggle")
	}

	// Verify it persisted in DB
	req = httptest.NewRequest("GET", "/api/admin/users/1", nil)
	req.SetPathValue("id", "1")
	w = httptest.NewRecorder()
	srv.apiUsersGet(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get after toggle: status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	if updated.Enabled {
		t.Fatal("enabled should still be false in GET")
	}
}

// =============================================================================
// TestUsersPartialUpdateEnabledToggleWithPassword — PUT {"enabled": false} on user WITH BGP password
// =============================================================================

func TestUsersPartialUpdateEnabledToggleWithPassword(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)

	// Create user with BGP password
	createBody := strings.NewReader(`{"name":"toggle-pw-user","peer_ip":"192.168.9.1","peer_asn":65010,"bgp_password":"secret123","password_enabled":true,"networks":["10.0.0.0/8"],"web_auth":"network","enabled":true}`)
	req := httptest.NewRequest("POST", "/api/admin/users", createBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201, body=%s", w.Code, w.Body.String())
	}

	// PUT only {"enabled": false} → should return 200 (password from DB should be preserved)
	updateBody := strings.NewReader(`{"enabled": false}`)
	req = httptest.NewRequest("PUT", "/api/admin/users/1", updateBody)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "1")
	w = httptest.NewRecorder()
	srv.apiUsersUpdate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("enabled toggle (with password): status = %d, want 200, body=%s", w.Code, w.Body.String())
	}

	var updated userJSON
	if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if updated.Enabled {
		t.Fatal("enabled should be false after toggle")
	}
	if !updated.HasPassword {
		t.Fatal("has_password should still be true after toggle")
	}
}

// =============================================================================
// TestUsersPartialUpdateDoubleToggleWithPassword — toggle enabled twice on user WITH BGP password
// =============================================================================

func TestUsersPartialUpdateDoubleToggleWithPassword(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)

	// Create user with BGP password and enabled=true
	createBody := strings.NewReader(`{"name":"double-toggle-pw","peer_ip":"192.168.10.1","peer_asn":65011,"bgp_password":"secret123","password_enabled":true,"networks":["10.0.0.0/8"],"web_auth":"network","enabled":true}`)
	req := httptest.NewRequest("POST", "/api/admin/users", createBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201, body=%s", w.Code, w.Body.String())
	}

	// PUT {"enabled": false} — first toggle
	req = httptest.NewRequest("PUT", "/api/admin/users/1", strings.NewReader(`{"enabled": false}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "1")
	w = httptest.NewRecorder()
	srv.apiUsersUpdate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("first toggle (false): status = %d, want 200, body=%s", w.Code, w.Body.String())
	}

	// PUT {"enabled": true} — second toggle (the second click)
	req = httptest.NewRequest("PUT", "/api/admin/users/1", strings.NewReader(`{"enabled": true}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "1")
	w = httptest.NewRecorder()
	srv.apiUsersUpdate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("second toggle (true): status = %d, want 200, body=%s", w.Code, w.Body.String())
	}

	var updated userJSON
	if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !updated.Enabled {
		t.Fatal("enabled should be true after second toggle")
	}
	if !updated.HasPassword {
		t.Fatal("has_password should still be true after double toggle")
	}

	// Verify persisted in DB
	req = httptest.NewRequest("GET", "/api/admin/users/1", nil)
	req.SetPathValue("id", "1")
	w = httptest.NewRecorder()
	srv.apiUsersGet(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get after double toggle: status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	if !updated.Enabled {
		t.Fatal("enabled should be true in GET after second toggle")
	}
}

// =============================================================================
// TestUsersPartialUpdateDoubleToggleNoPassword — toggle enabled twice on user WITHOUT BGP password
// =============================================================================

func TestUsersPartialUpdateDoubleToggleNoPassword(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)

	// Create user without BGP password, enabled=true
	createBody := strings.NewReader(`{"name":"double-toggle-nopw","peer_ip":"192.168.11.1","peer_asn":65012,"networks":["10.0.0.0/8"],"web_auth":"network","enabled":true}`)
	req := httptest.NewRequest("POST", "/api/admin/users", createBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201, body=%s", w.Code, w.Body.String())
	}

	// PUT {"enabled": false} — first toggle
	req = httptest.NewRequest("PUT", "/api/admin/users/1", strings.NewReader(`{"enabled": false}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "1")
	w = httptest.NewRecorder()
	srv.apiUsersUpdate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("first toggle (false): status = %d, want 200, body=%s", w.Code, w.Body.String())
	}

	// PUT {"enabled": true} — second toggle (the second click)
	req = httptest.NewRequest("PUT", "/api/admin/users/1", strings.NewReader(`{"enabled": true}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "1")
	w = httptest.NewRecorder()
	srv.apiUsersUpdate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("second toggle (true): status = %d, want 200, body=%s", w.Code, w.Body.String())
	}

	var updated userJSON
	if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !updated.Enabled {
		t.Fatal("enabled should be true after second toggle")
	}

	// Verify persisted in DB
	req = httptest.NewRequest("GET", "/api/admin/users/1", nil)
	req.SetPathValue("id", "1")
	w = httptest.NewRecorder()
	srv.apiUsersGet(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get after double toggle: status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	if !updated.Enabled {
		t.Fatal("enabled should be true in GET after second toggle")
	}
}

// =============================================================================
// TestAdminSaveSelectionsPreservesHidden — admin saves selections, disabled feed
// items are preserved (per-item INSERT/DELETE leaves unmentioned items unchanged).
// =============================================================================

func TestAdminSaveSelectionsPreservesHidden(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)
	ctx := context.Background()

	// 1. Create a catalog mode (id > 3 so it's custom)
	modeID, err := st.AddCatalogMode(ctx, "test-mode", "Test Mode", true)
	if err != nil {
		t.Fatalf("add mode: %v", err)
	}
	if modeID <= 3 {
		t.Fatalf("mode id %d should be > 3", modeID)
	}

	// 2. Create two feeds: f1 (enabled), f2 (enabled initially)
	f1ID, err := st.AddFeed(ctx, "f1", "https://example.test/f1.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatalf("add feed1: %v", err)
	}
	f2ID, err := st.AddFeed(ctx, "f2", "https://example.test/f2.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatalf("add feed2: %v", err)
	}

	// 3. Assign both feeds to the mode
	if err := st.AddFeedToMode(ctx, modeID, f1ID); err != nil {
		t.Fatalf("assign f1 to mode: %v", err)
	}
	if err := st.AddFeedToMode(ctx, modeID, f2ID); err != nil {
		t.Fatalf("assign f2 to mode: %v", err)
	}

	// 4. Create catalog entries: feed1 has "Cat1::Svc1", feed2 has "Cat2::Svc2"
	if _, err := st.DB.ExecContext(ctx,
		"INSERT INTO catalog_entries(feed_id, category, service, cidr) VALUES (?, ?, ?, ?)",
		f1ID, "Cat1", "Svc1", "10.0.0.0/8"); err != nil {
		t.Fatalf("insert entry f1: %v", err)
	}
	if _, err := st.DB.ExecContext(ctx,
		"INSERT INTO catalog_entries(feed_id, category, service, cidr) VALUES (?, ?, ?, ?)",
		f2ID, "Cat2", "Svc2", "192.168.0.0/16"); err != nil {
		t.Fatalf("insert entry f2: %v", err)
	}

	// 5. Create a user with catalog_mode_id = modeID
	userID, err := st.AddUser(ctx, store.User{
		Name:          "test-user",
		PeerIP:        "172.16.0.1",
		PeerASN:       65001,
		Enabled:       true,
		CatalogModeID: modeID,
		Networks:      []string{"10.0.0.0/8"},
	})
	if err != nil {
		t.Fatalf("add user: %v", err)
	}

	s := testSettings()

	// 6. Save selections for both via admin endpoint
	body := fmt.Sprintf(`{"categories":[{"category":"Cat1","checked":true},{"category":"Cat2","checked":true}],"services":[{"category":"Cat1","service":"Svc1","checked":true},{"category":"Cat2","service":"Svc2","checked":true}],"mode_id":%d}`, modeID)
	req := httptest.NewRequest("PUT", fmt.Sprintf("/api/admin/users/%d/selections", userID), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", fmt.Sprintf("%d", userID))
	req.AddCookie(adminCookie(s))
	w := httptest.NewRecorder()
	srv.apiAdminUserSaveSelections(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("save selections: status = %d, want 200, body=%s", w.Code, w.Body.String())
	}

	// 7. Disable feed2
	if _, err := st.DB.ExecContext(ctx, "UPDATE feeds SET enabled = 0 WHERE id = ?", f2ID); err != nil {
		t.Fatalf("disable f2: %v", err)
	}

	// 8. Call admin save again with ONLY Cat1::Svc1 (Cat2 not in payload — preserved by omission)
	body = fmt.Sprintf(`{"categories":[{"category":"Cat1","checked":true}],"services":[{"category":"Cat1","service":"Svc1","checked":true}],"mode_id":%d}`, modeID)
	req = httptest.NewRequest("PUT", fmt.Sprintf("/api/admin/users/%d/selections", userID), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", fmt.Sprintf("%d", userID))
	req.AddCookie(adminCookie(s))
	w = httptest.NewRecorder()
	srv.apiAdminUserSaveSelections(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("save selections after disable: status = %d, want 200, body=%s", w.Code, w.Body.String())
	}

	// 9. Verify Cat1::Svc1 remains selected
	// 10. Verify Cat2::Svc2 is ALSO still selected (hidden, from disabled feed, preserved)
	categories, services, err := st.UserModeSelection(ctx, userID, modeID)
	if err != nil {
		t.Fatalf("get selections: %v", err)
	}
	if !categories["Cat1"] {
		t.Fatal("Cat1 should be selected")
	}
	if !categories["Cat2"] {
		t.Fatal("Cat2 should be selected (preserved from disabled feed)")
	}
	if !services[store.ServiceKey{Category: "Cat1", Service: "Svc1"}] {
		t.Fatal("Cat1::Svc1 should be selected")
	}
	if !services[store.ServiceKey{Category: "Cat2", Service: "Svc2"}] {
		t.Fatal("Cat2::Svc2 should be selected (preserved from disabled feed)")
	}
}

// =============================================================================
// TestAdminSaveSelectionsUpdatesUserCatalogMode — switching mode via the
// selections save endpoint must persist users.catalog_mode_id, not just
// scope the selection rows to the new mode.
// =============================================================================

func TestAdminSaveSelectionsUpdatesUserCatalogMode(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)
	ctx := context.Background()

	oldModeID, err := st.AddCatalogMode(ctx, "old-mode", "Old Mode", true)
	if err != nil {
		t.Fatalf("add old mode: %v", err)
	}
	newModeID, err := st.AddCatalogMode(ctx, "new-mode", "New Mode", true)
	if err != nil {
		t.Fatalf("add new mode: %v", err)
	}

	userID, err := st.AddUser(ctx, store.User{
		Name:          "mode-switch-user",
		PeerIP:        "172.16.0.2",
		PeerASN:       65002,
		Enabled:       true,
		CatalogModeID: oldModeID,
		Networks:      []string{"10.0.0.0/8"},
	})
	if err != nil {
		t.Fatalf("add user: %v", err)
	}

	s := testSettings()
	body := fmt.Sprintf(`{"categories":[],"services":[],"mode_id":%d}`, newModeID)
	req := httptest.NewRequest("PUT", fmt.Sprintf("/api/admin/users/%d/selections", userID), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", fmt.Sprintf("%d", userID))
	req.AddCookie(adminCookie(s))
	w := httptest.NewRecorder()
	srv.apiAdminUserSaveSelections(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("save selections: status = %d, want 200, body=%s", w.Code, w.Body.String())
	}

	updated, err := st.User(ctx, userID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if updated.CatalogModeID != newModeID {
		t.Fatalf("catalog_mode_id = %d, want %d (mode switch should persist)", updated.CatalogModeID, newModeID)
	}
}

// TestAdminSaveSelectionsRejectsDisabledMode — mode_id pointing at a
// disabled mode must not silently switch the user onto it.
func TestAdminSaveSelectionsRejectsDisabledMode(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)
	ctx := context.Background()

	oldModeID, err := st.AddCatalogMode(ctx, "old-mode2", "Old Mode 2", true)
	if err != nil {
		t.Fatalf("add old mode: %v", err)
	}
	disabledModeID, err := st.AddCatalogMode(ctx, "disabled-mode", "Disabled Mode", false)
	if err != nil {
		t.Fatalf("add disabled mode: %v", err)
	}

	userID, err := st.AddUser(ctx, store.User{
		Name:          "mode-switch-user2",
		PeerIP:        "172.16.0.3",
		PeerASN:       65003,
		Enabled:       true,
		CatalogModeID: oldModeID,
		Networks:      []string{"10.0.0.0/8"},
	})
	if err != nil {
		t.Fatalf("add user: %v", err)
	}

	s := testSettings()
	body := fmt.Sprintf(`{"categories":[],"services":[],"mode_id":%d}`, disabledModeID)
	req := httptest.NewRequest("PUT", fmt.Sprintf("/api/admin/users/%d/selections", userID), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", fmt.Sprintf("%d", userID))
	req.AddCookie(adminCookie(s))
	w := httptest.NewRecorder()
	srv.apiAdminUserSaveSelections(w, req)
	if w.Code == http.StatusOK {
		t.Fatalf("save selections with disabled mode: status = %d, want non-200", w.Code)
	}

	updated, err := st.User(ctx, userID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if updated.CatalogModeID != oldModeID {
		t.Fatalf("catalog_mode_id = %d, want unchanged %d", updated.CatalogModeID, oldModeID)
	}
}

// =============================================================================
// TestUserBGPPasswordToggle — 4-way BGP password enable/disable logic
// =============================================================================

func TestUserBGPPasswordToggle(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)

	// Helper: create user with password
	createWithPW := func(name, ip, password, network string) int64 {
		body := fmt.Sprintf(`{"name":"%s","peer_ip":"%s","peer_asn":65100,"bgp_password":"%s","password_enabled":true,"networks":["%s"],"web_auth":"network","enabled":true}`, name, ip, password, network)
		req := httptest.NewRequest("POST", "/api/admin/users", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		srv.apiUsersCreate(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("create %s: %d %s", name, w.Code, w.Body.String())
		}
		var u userJSON
		if err := json.NewDecoder(w.Body).Decode(&u); err != nil {
			t.Fatal(err)
		}
		return u.ID
	}

	// Case 1: enable + set new password → works
	t.Run("enableWithPassword", func(t *testing.T) {
		id := createWithPW("pwt-1", "10.99.1.1", "secret1", "10.99.1.0/24")
		body := `{"bgp_password":"newsecret","password_enabled":true}`
		req := httptest.NewRequest("PUT", "/api/admin/users/"+strconv.FormatInt(id, 10), strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.SetPathValue("id", strconv.FormatInt(id, 10))
		w := httptest.NewRecorder()
		srv.apiUsersUpdate(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("enable+password: %d %s", w.Code, w.Body.String())
		}
		// Verify password changed
		u, err := st.User(context.Background(), id)
		if err != nil {
			t.Fatalf("read user: %v", err)
		}
		if u.BGPPassword != "newsecret" {
			t.Fatalf("BGPPassword = %q, want newsecret", u.BGPPassword)
		}
	})

	// Case 2: enable + empty password, has existing → works
	t.Run("enableKeepExisting", func(t *testing.T) {
		id := createWithPW("pwt-2", "10.99.2.1", "existing", "10.99.2.0/24")
		body := `{"bgp_password":"","password_enabled":true}`
		req := httptest.NewRequest("PUT", "/api/admin/users/"+strconv.FormatInt(id, 10), strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.SetPathValue("id", strconv.FormatInt(id, 10))
		w := httptest.NewRecorder()
		srv.apiUsersUpdate(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("enable keep existing: %d %s", w.Code, w.Body.String())
		}
		u, err := st.User(context.Background(), id)
		if err != nil {
			t.Fatalf("read user: %v", err)
		}
		if u.BGPPassword != "existing" {
			t.Fatalf("BGPPassword = %q, want existing", u.BGPPassword)
		}
	})

	// Case 3: enable + empty password, no existing → error 400
	t.Run("enableNoPassword", func(t *testing.T) {
		// Create user WITHOUT password first
		body := `{"name":"pwt-3","peer_ip":"10.99.3.1","peer_asn":65100,"networks":["10.99.3.0/24"],"web_auth":"network","enabled":true,"password_enabled":false}`
		req := httptest.NewRequest("POST", "/api/admin/users", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		srv.apiUsersCreate(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("create: %d", w.Code)
		}
		var u userJSON
		if err := json.NewDecoder(w.Body).Decode(&u); err != nil {
			t.Fatal(err)
		}

		body = `{"bgp_password":"","password_enabled":true}`
		req = httptest.NewRequest("PUT", "/api/admin/users/"+strconv.FormatInt(u.ID, 10), strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.SetPathValue("id", strconv.FormatInt(u.ID, 10))
		w = httptest.NewRecorder()
		srv.apiUsersUpdate(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("enable without password: got %d, want 400", w.Code)
		}
	})

	// Case 4: disable + empty password → clears password
	t.Run("disableClearPassword", func(t *testing.T) {
		id := createWithPW("pwt-4", "10.99.4.1", "willclear", "10.99.4.0/24")
		body := `{"bgp_password":"","password_enabled":false}`
		req := httptest.NewRequest("PUT", "/api/admin/users/"+strconv.FormatInt(id, 10), strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.SetPathValue("id", strconv.FormatInt(id, 10))
		w := httptest.NewRecorder()
		srv.apiUsersUpdate(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("disable clear: %d %s", w.Code, w.Body.String())
		}
		u, err := st.User(context.Background(), id)
		if err != nil {
			t.Fatalf("read user: %v", err)
		}
		if u.BGPPassword != "" {
			t.Fatalf("BGPPassword = %q, want empty", u.BGPPassword)
		}
	})

	// Case 5: disable + non-empty password → error 400
	t.Run("disableWithPassword", func(t *testing.T) {
		id := createWithPW("pwt-5", "10.99.5.1", "secret", "10.99.5.0/24")
		body := `{"bgp_password":"newpass","password_enabled":false}`
		req := httptest.NewRequest("PUT", "/api/admin/users/"+strconv.FormatInt(id, 10), strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.SetPathValue("id", strconv.FormatInt(id, 10))
		w := httptest.NewRecorder()
		srv.apiUsersUpdate(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("disable with password: got %d, want 400", w.Code)
		}
	})

	// Case 6: create without password_enabled → works, no password
	t.Run("createNoToggle", func(t *testing.T) {
		body := `{"name":"pwt-6","peer_ip":"10.99.6.1","peer_asn":65100,"networks":["10.99.6.0/24"],"web_auth":"network","enabled":true}`
		req := httptest.NewRequest("POST", "/api/admin/users", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		srv.apiUsersCreate(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("create no toggle: %d %s", w.Code, w.Body.String())
		}
		var u userJSON
		if err := json.NewDecoder(w.Body).Decode(&u); err != nil {
			t.Fatal(err)
		}
		usr, err := st.User(context.Background(), u.ID)
		if err != nil {
			t.Fatalf("read user: %v", err)
		}
		if usr.BGPPassword != "" {
			t.Fatal("password should be empty when no toggle provided")
		}
	})
}

// =============================================================================
// TestCreateUserInvalidWebAuth — POST with invalid web_auth returns 400
// =============================================================================

func TestCreateUserInvalidWebAuth(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)

	body := `{"name":"bad-auth","peer_ip":"10.0.99.1","peer_asn":65099,"networks":["10.0.0.0/8"],"web_auth":"logn","enabled":true}`
	req := httptest.NewRequest("POST", "/api/admin/users", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiUsersCreate(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid web_auth create: status = %d, want 400, body=%s", w.Code, w.Body.String())
	}
}

// =============================================================================
// TestUpdateUserInvalidWebAuth — PUT with invalid web_auth returns 400
// =============================================================================

func TestUpdateUserInvalidWebAuth(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)

	// Create a user first
	createBody := strings.NewReader(`{"name":"auth-update","peer_ip":"10.0.98.1","peer_asn":65098,"networks":["10.0.0.0/8"],"web_auth":"network","enabled":true}`)
	req := httptest.NewRequest("POST", "/api/admin/users", createBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201, body=%s", w.Code, w.Body.String())
	}
	var created userJSON
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	// Try to update with invalid web_auth
	updateBody := strings.NewReader(`{"web_auth":"bogus"}`)
	req = httptest.NewRequest("PUT", "/api/admin/users/"+strconv.FormatInt(created.ID, 10), updateBody)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", strconv.FormatInt(created.ID, 10))
	w = httptest.NewRecorder()
	srv.apiUsersUpdate(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid web_auth update: status = %d, want 400, body=%s", w.Code, w.Body.String())
	}
}

// =============================================================================
// TestCreateUserRejectsUnnormalizedNetworks — networks with unmasked host
// bits, overlaps, or combinable-adjacent ranges must be rejected, not
// silently accepted verbatim.
// =============================================================================

func TestCreateUserRejectsUnnormalizedNetworks(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)

	// Each case needs its own peer_ip/ASN — otherwise a case that's wrongly
	// accepted (the exact regression this test guards against) would occupy
	// the identity and make the next case fail for the unrelated reason of
	// "peer already exists" instead of the network check being exercised.
	cases := []struct {
		name     string
		peerIP   string
		peerASN  int
		networks string
	}{
		// Distinct, non-overlapping address ranges per case — this test is
		// only about per-user normalization, not the separate (and, for
		// this step, explicitly out of scope) cross-user duplicate-CIDR
		// constraint, so cases must never share an address range.
		{"unmasked host bits", "10.0.97.1", 65097, `["10.1.0.5/24"]`},
		{"overlapping ranges", "10.0.97.2", 65098, `["10.2.0.0/24", "10.2.0.128/25"]`},
		{"adjacent ranges", "10.0.97.3", 65099, `["10.3.0.0/25", "10.3.0.128/25"]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"name":"unnorm-%s","peer_ip":"%s","peer_asn":%d,"networks":%s,"web_auth":"network","enabled":true}`,
				tc.name, tc.peerIP, tc.peerASN, tc.networks)
			req := httptest.NewRequest("POST", "/api/admin/users", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			srv.apiUsersCreate(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400, body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestCreateUserAcceptsAlreadyNormalizedNetworks(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)

	body := `{"name":"norm-ok","peer_ip":"10.0.96.1","peer_asn":65096,"networks":["10.0.0.0/24","10.0.2.0/24"],"web_auth":"network","enabled":true}`
	req := httptest.NewRequest("POST", "/api/admin/users", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", w.Code, w.Body.String())
	}
}

func TestUpdateUserRejectsUnnormalizedNetworks(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)

	createBody := strings.NewReader(`{"name":"unnorm-update","peer_ip":"10.0.95.1","peer_asn":65095,"networks":["10.0.0.0/24"],"web_auth":"network","enabled":true}`)
	req := httptest.NewRequest("POST", "/api/admin/users", createBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201, body=%s", w.Code, w.Body.String())
	}
	var created userJSON
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	updateBody := strings.NewReader(`{"networks":["10.0.0.0/25","10.0.0.128/25"]}`)
	req = httptest.NewRequest("PUT", "/api/admin/users/"+strconv.FormatInt(created.ID, 10), updateBody)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", strconv.FormatInt(created.ID, 10))
	w = httptest.NewRecorder()
	srv.apiUsersUpdate(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", w.Code, w.Body.String())
	}
}

// =============================================================================
// TestApiUsersNormalizeNetworks — POST /api/admin/users/normalize-networks
// returns the minimal equivalent set for the admin UI's preview/auto-fix
// button and pre-save validation.
// =============================================================================

func TestApiUsersNormalizeNetworks(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)

	body := `{"networks":["10.0.0.0/25","10.0.0.128/25","10.0.2.5/24"]}`
	req := httptest.NewRequest("POST", "/api/admin/users/normalize-networks", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiUsersNormalizeNetworks(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Networks []string `json:"networks"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	want := []string{"10.0.0.0/24", "10.0.2.0/24"}
	if len(resp.Networks) != len(want) {
		t.Fatalf("networks = %v, want %v", resp.Networks, want)
	}
	got := map[string]bool{}
	for _, n := range resp.Networks {
		got[n] = true
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("networks = %v, missing %v", resp.Networks, w)
		}
	}
}

func TestApiUsersNormalizeNetworksRejectsInvalidCIDR(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)

	body := `{"networks":["not-a-cidr"]}`
	req := httptest.NewRequest("POST", "/api/admin/users/normalize-networks", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiUsersNormalizeNetworks(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", w.Code, w.Body.String())
	}
}

// =============================================================================
// Cross-user network overlap validation (apiUsersCreate/apiUsersUpdate)
// =============================================================================

func TestCreateUserRejectsOverlapWithActiveUser(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)

	createBody := `{"name":"existing","peer_ip":"10.0.94.1","peer_asn":65090,"networks":["10.0.0.0/24"],"web_auth":"network","enabled":true}`
	req := httptest.NewRequest("POST", "/api/admin/users", strings.NewReader(createBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create existing: status = %d, want 201, body=%s", w.Code, w.Body.String())
	}

	conflictBody := `{"name":"conflicting","peer_ip":"10.0.94.2","peer_asn":65091,"networks":["10.0.0.128/25"],"web_auth":"network","enabled":true}`
	req = httptest.NewRequest("POST", "/api/admin/users", strings.NewReader(conflictBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("create conflicting: status = %d, want 400, body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "existing") {
		t.Errorf("error body = %s, want it to name the conflicting user", w.Body.String())
	}
}

func TestCreateUserIgnoresOverlapWithLoginModeUser(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)
	ctx := context.Background()

	loginBody := `{"name":"login-user","peer_ip":"10.0.93.1","peer_asn":65092,"web_auth":"login","enabled":true}`
	req := httptest.NewRequest("POST", "/api/admin/users", strings.NewReader(loginBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create login-user: status = %d, want 201, body=%s", w.Code, w.Body.String())
	}
	var loginUser userJSON
	if err := json.NewDecoder(w.Body).Decode(&loginUser); err != nil {
		t.Fatal(err)
	}
	// Give the login-mode user a stale network directly (bypassing the API,
	// simulating data left over from before it switched to login mode) —
	// must not affect the create below at all, even though it overlaps
	// with what network-user is about to claim.
	if _, err := st.DB.ExecContext(ctx, "INSERT INTO user_networks(user_id, cidr) VALUES (?, ?)", loginUser.ID, "10.5.0.128/25"); err != nil {
		t.Fatal(err)
	}

	networkBody := `{"name":"network-user","peer_ip":"10.0.93.2","peer_asn":65093,"networks":["10.5.0.0/24"],"web_auth":"network","enabled":true}`
	req = httptest.NewRequest("POST", "/api/admin/users", strings.NewReader(networkBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create network-user: status = %d, want 201, body=%s", w.Code, w.Body.String())
	}
}

func TestUpdateUserRejectsReactivatingConflictingNetworks(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)
	ctx := context.Background()

	// A network-mode user occupies 10.0.0.0/24 first.
	activeBody := `{"name":"active-user","peer_ip":"10.0.92.1","peer_asn":65094,"networks":["10.0.0.0/24"],"web_auth":"network","enabled":true}`
	req := httptest.NewRequest("POST", "/api/admin/users", strings.NewReader(activeBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create active-user: status = %d, want 201, body=%s", w.Code, w.Body.String())
	}

	// A second user starts in login mode with an overlapping network,
	// stored directly (not through the API, which would reject it outright
	// since it would already be active) to simulate this user having sat
	// dormant in login mode since before active-user was created.
	dormantID, err := st.AddUser(ctx, store.User{
		Name: "dormant-user", PeerIP: "10.0.92.2", PeerASN: 65095, Enabled: true, WebAuth: "login",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec("INSERT INTO user_networks(user_id, cidr) VALUES (?, ?)", dormantID, "10.0.0.128/25"); err != nil {
		t.Fatal(err)
	}

	// Switching dormant-user back to network mode, without touching
	// networks at all, must be rejected — its now-active networks conflict.
	switchBody := `{"web_auth":"network"}`
	req = httptest.NewRequest("PUT", "/api/admin/users/"+strconv.FormatInt(dormantID, 10), strings.NewReader(switchBody))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", strconv.FormatInt(dormantID, 10))
	w = httptest.NewRecorder()
	srv.apiUsersUpdate(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "active-user") {
		t.Errorf("error body = %s, want it to name the conflicting user", w.Body.String())
	}

	// The mode switch must not have gone through.
	reloaded, err := st.User(ctx, dormantID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.WebAuth != "login" {
		t.Errorf("web_auth = %q after rejected update, want unchanged \"login\"", reloaded.WebAuth)
	}
}

func TestUpdateUserAllowsReactivatingNonConflictingNetworks(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)
	ctx := context.Background()

	dormantID, err := st.AddUser(ctx, store.User{
		Name: "dormant-user", PeerIP: "10.0.91.2", PeerASN: 65096, Enabled: true, WebAuth: "login",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec("INSERT INTO user_networks(user_id, cidr) VALUES (?, ?)", dormantID, "10.0.7.0/24"); err != nil {
		t.Fatal(err)
	}

	switchBody := `{"web_auth":"network"}`
	req := httptest.NewRequest("PUT", "/api/admin/users/"+strconv.FormatInt(dormantID, 10), strings.NewReader(switchBody))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", strconv.FormatInt(dormantID, 10))
	w := httptest.NewRecorder()
	srv.apiUsersUpdate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}

	reloaded, err := st.User(ctx, dormantID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.WebAuth != "network" {
		t.Errorf("web_auth = %q, want \"network\"", reloaded.WebAuth)
	}
}

// TestCreateUserRejectsInvalidNextHop guards against buildRoute silently
// ignoring an unparseable next_hop (it only applies the override if
// netip.ParseAddr succeeds) — a typo must be rejected at write time instead
// of being accepted and then quietly falling back to the default next hop.
func TestCreateUserRejectsInvalidNextHop(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)

	body := `{"name":"nh-user","peer_ip":"10.0.93.1","peer_asn":65100,"next_hop":"not-an-ip","web_auth":"login","enabled":true}`
	req := httptest.NewRequest("POST", "/api/admin/users", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", w.Code, w.Body.String())
	}

	users, err := st.Users(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range users {
		if u.Name == "nh-user" {
			t.Fatalf("user %q must not have been created after rejected next_hop", u.Name)
		}
	}
}

func TestCreateUserAcceptsValidNextHop(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)

	body := `{"name":"nh-user-ok","peer_ip":"10.0.93.2","peer_asn":65101,"next_hop":"10.0.93.254","web_auth":"login","enabled":true}`
	req := httptest.NewRequest("POST", "/api/admin/users", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", w.Code, w.Body.String())
	}
}

// TestUpdateUserRejectsInvalidNextHop mirrors TestCreateUserRejectsInvalidNextHop
// for the partial-update path.
func TestUpdateUserRejectsInvalidNextHop(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)
	ctx := context.Background()

	createBody := `{"name":"nh-update-user","peer_ip":"10.0.93.3","peer_asn":65102,"next_hop":"10.0.93.253","web_auth":"login","enabled":true}`
	req := httptest.NewRequest("POST", "/api/admin/users", strings.NewReader(createBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201, body=%s", w.Code, w.Body.String())
	}
	var created userJSON
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}

	updateBody := `{"next_hop":"garbage"}`
	req = httptest.NewRequest("PUT", fmt.Sprintf("/api/admin/users/%d", created.ID), strings.NewReader(updateBody))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", strconv.FormatInt(created.ID, 10))
	w = httptest.NewRecorder()
	srv.apiUsersUpdate(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", w.Code, w.Body.String())
	}

	reloaded, err := st.User(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.NextHop != "10.0.93.253" {
		t.Errorf("next_hop = %q after rejected update, want unchanged \"10.0.93.253\"", reloaded.NextHop)
	}
}

// TestCreateUserRejectsMalformedFilterWithoutCreatingUser guards against a
// bug where AddUser committed the user row before SetUserRouteFilters
// rejected a malformed CIDR, leaving a filterless user behind despite the
// request having failed with an error.
func TestCreateUserRejectsMalformedFilterWithoutCreatingUser(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)

	body := `{"name":"bad-filter-user","peer_ip":"10.0.93.4","peer_asn":65103,"web_auth":"login","enabled":true,"filter_allow":["not-a-cidr"]}`
	req := httptest.NewRequest("POST", "/api/admin/users", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", w.Code, w.Body.String())
	}

	users, err := st.Users(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range users {
		if u.Name == "bad-filter-user" {
			t.Fatalf("user %q must not have been created after rejected filter_allow", u.Name)
		}
	}
}

// TestUpdateUserRejectsMalformedFilterWithoutMutatingUser mirrors the create
// case for the partial-update path: a malformed filter CIDR must be caught
// before UpdateUser writes the merged record, not after.
func TestUpdateUserRejectsMalformedFilterWithoutMutatingUser(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)
	ctx := context.Background()

	createBody := `{"name":"filter-update-user","peer_ip":"10.0.93.5","peer_asn":65104,"web_auth":"login","enabled":true}`
	req := httptest.NewRequest("POST", "/api/admin/users", strings.NewReader(createBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201, body=%s", w.Code, w.Body.String())
	}
	var created userJSON
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}

	updateBody := `{"name":"renamed","filter_deny":["also-not-a-cidr"]}`
	req = httptest.NewRequest("PUT", fmt.Sprintf("/api/admin/users/%d", created.ID), strings.NewReader(updateBody))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", strconv.FormatInt(created.ID, 10))
	w = httptest.NewRecorder()
	srv.apiUsersUpdate(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", w.Code, w.Body.String())
	}

	reloaded, err := st.User(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Name != "filter-update-user" {
		t.Errorf("name = %q after rejected update, want unchanged \"filter-update-user\"", reloaded.Name)
	}
	filters, err := st.UserRouteFilters(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(filters.Deny) != 0 {
		t.Errorf("filter_deny = %v after rejected update, want empty (unchanged)", filters.Deny)
	}
}
