package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
	credBody := `{"login":"both-login","password":"test"}`
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
	st.DB.ExecContext(ctx, "UPDATE users SET enabled = 0 WHERE id = 1")
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
