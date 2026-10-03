package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/andrey-vk/wdbgp/internal/store"
)

// userRouteFiltersFixture creates a user reachable via network auth from
// 10.2.1.0/24, with filter_mode/filter_allow/filter_deny as given, plus a
// stale user_route_filters row ("9.9.9.0/24" deny) left in place regardless
// of mode, so tests can prove it's ignored under FilterModeGlobal.
func userRouteFiltersFixture(t *testing.T, filterMode string, ownAllow, ownDeny []string) (*Server, *store.Store, int64) {
	t.Helper()
	srv, st, _ := setupUserTestServer(t)

	allowJSON, err := json.Marshal(ownAllow)
	if err != nil {
		t.Fatalf("marshal allow: %v", err)
	}
	denyJSON, err := json.Marshal(ownDeny)
	if err != nil {
		t.Fatalf("marshal deny: %v", err)
	}
	userBody := `{"name":"filters-user","peer_ip":"10.2.1.1","peer_asn":65002,` +
		`"networks":["10.2.1.0/24"],"web_auth":"network","enabled":true,"catalog_mode_id":1,` +
		`"filter_mode":"` + filterMode + `","filter_allow":` + string(allowJSON) + `,"filter_deny":` + string(denyJSON) + `}`
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

// setGlobalRouteFilters writes the global filter_allow/filter_deny app_settings
// rows directly (newline-joined CIDR text per setting, as GlobalRouteFilters
// reads them). The test server's *Server.settings is a separate in-memory
// instance from its *store.Store (see setupUserTestServer), so this bypasses
// it and writes straight to the store GlobalRouteFilters actually reads from.
func setGlobalRouteFilters(t *testing.T, st *store.Store, allow, deny []string) {
	t.Helper()
	ctx := context.Background()
	if err := st.SaveSetting(ctx, "filter_allow", strings.Join(allow, "\n")); err != nil {
		t.Fatalf("set filter_allow: %v", err)
	}
	if err := st.SaveSetting(ctx, "filter_deny", strings.Join(deny, "\n")); err != nil {
		t.Fatalf("set filter_deny: %v", err)
	}
}

func routeFiltersRequest() *http.Request {
	req := httptest.NewRequest("GET", "/api/user/route-filters", nil)
	req.RemoteAddr = "10.2.1.1:1234"
	return req
}

func TestUserRouteFiltersUnauthenticated(t *testing.T) {
	srv, _, _ := userRouteFiltersFixture(t, "global", nil, nil)

	req := httptest.NewRequest("GET", "/api/user/route-filters", nil)
	req.RemoteAddr = "203.0.113.1:1234" // not in any user's network
	w := httptest.NewRecorder()
	srv.requireUser(srv.apiUserRouteFilters).ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body=%s", w.Code, w.Body.String())
	}
}

func TestUserRouteFiltersGlobalMode(t *testing.T) {
	srv, st, _ := userRouteFiltersFixture(t, "global", []string{"9.9.9.0/24"}, []string{"9.9.9.0/24"})
	setGlobalRouteFilters(t, st, []string{"10.0.0.0/8"}, []string{"10.1.0.0/16"})

	w := httptest.NewRecorder()
	srv.requireUser(srv.apiUserRouteFilters).ServeHTTP(w, routeFiltersRequest())
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var result userRouteFiltersResult
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.Mode != "global" {
		t.Fatalf("mode = %q, want global", result.Mode)
	}
	if len(result.Own.Allow) != 0 || len(result.Own.Deny) != 0 {
		t.Fatalf("own = %+v, want empty (stale per-user rows must be ignored)", result.Own)
	}
	if len(result.Effective.Allow) != 1 || result.Effective.Allow[0] != "10.0.0.0/8" {
		t.Fatalf("effective.allow = %v, want [10.0.0.0/8]", result.Effective.Allow)
	}
	if len(result.Effective.Deny) != 1 || result.Effective.Deny[0] != "10.1.0.0/16" {
		t.Fatalf("effective.deny = %v, want [10.1.0.0/16]", result.Effective.Deny)
	}
}

func TestUserRouteFiltersExtendMode(t *testing.T) {
	srv, st, _ := userRouteFiltersFixture(t, "extend", []string{"192.168.0.0/16"}, []string{"192.168.1.0/24"})
	setGlobalRouteFilters(t, st, []string{"10.0.0.0/8"}, []string{"10.1.0.0/16"})

	w := httptest.NewRecorder()
	srv.requireUser(srv.apiUserRouteFilters).ServeHTTP(w, routeFiltersRequest())
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var result userRouteFiltersResult
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.Mode != "extend" {
		t.Fatalf("mode = %q, want extend", result.Mode)
	}
	wantAllow := map[string]bool{"10.0.0.0/8": true, "192.168.0.0/16": true}
	if len(result.Effective.Allow) != len(wantAllow) {
		t.Fatalf("effective.allow = %v, want union of global+own", result.Effective.Allow)
	}
	for _, a := range result.Effective.Allow {
		if !wantAllow[a] {
			t.Fatalf("unexpected effective.allow entry %q", a)
		}
	}
	wantDeny := map[string]bool{"10.1.0.0/16": true, "192.168.1.0/24": true}
	if len(result.Effective.Deny) != len(wantDeny) {
		t.Fatalf("effective.deny = %v, want union of global+own", result.Effective.Deny)
	}
	for _, d := range result.Effective.Deny {
		if !wantDeny[d] {
			t.Fatalf("unexpected effective.deny entry %q", d)
		}
	}
}

func TestUserRouteFiltersOverrideMode(t *testing.T) {
	srv, st, _ := userRouteFiltersFixture(t, "override", []string{"192.168.0.0/16"}, nil)
	setGlobalRouteFilters(t, st, []string{"10.0.0.0/8"}, []string{"10.1.0.0/16"})

	w := httptest.NewRecorder()
	srv.requireUser(srv.apiUserRouteFilters).ServeHTTP(w, routeFiltersRequest())
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var result userRouteFiltersResult
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.Mode != "override" {
		t.Fatalf("mode = %q, want override", result.Mode)
	}
	if len(result.Global.Allow) != 1 || result.Global.Allow[0] != "10.0.0.0/8" {
		t.Fatalf("global.allow = %v, want [10.0.0.0/8] (must still be reported)", result.Global.Allow)
	}
	if len(result.Effective.Allow) != 1 || result.Effective.Allow[0] != "192.168.0.0/16" {
		t.Fatalf("effective.allow = %v, want own only [192.168.0.0/16]", result.Effective.Allow)
	}
	if len(result.Effective.Deny) != 0 {
		t.Fatalf("effective.deny = %v, want empty (global deny must not apply in override mode)", result.Effective.Deny)
	}
}

func TestUserRouteFiltersDoesNotLeakOtherUsersData(t *testing.T) {
	srv, _, _ := userRouteFiltersFixture(t, "override", []string{"192.168.0.0/16"}, nil)

	w := httptest.NewRecorder()
	srv.requireUser(srv.apiUserRouteFilters).ServeHTTP(w, routeFiltersRequest())
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var raw map[string]any
	if err := json.NewDecoder(w.Body).Decode(&raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, forbidden := range []string{"users", "name", "id", "user_id"} {
		if _, ok := raw[forbidden]; ok {
			t.Fatalf("response leaks a %q field: %v", forbidden, raw)
		}
	}
}
