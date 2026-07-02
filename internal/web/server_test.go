package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrey-vk/wdbgp/internal/feeds"
	"github.com/andrey-vk/wdbgp/internal/settings"
	"github.com/andrey-vk/wdbgp/internal/store"
)

func testSettings() *settings.Settings {
	s, err := settings.New(settings.NewTestStore())
	if err != nil {
		panic(err)
	}
	// Set test-specific values (context not needed for tests since env isn't overriding).
	// testSettings has no *testing.T (it's called from many places as a bare
	// helper), so a Set() failure here panics same as the New() error above —
	// this is test setup, it must not fail silently.
	mustSet := func(err error) {
		if err != nil {
			panic(err)
		}
	}
	mustSet(s.AdminPassword.Set(context.Background(), "admin"))
	mustSet(s.SessionSecret.Set(context.Background(), "test-secret"))
	mustSet(s.AdminCookieSecure.Set(context.Background(), "true"))
	mustSet(s.DefaultLanguage.Set(context.Background(), "ru"))
	mustSet(s.RateLimitLogin.Set(context.Background(), 1000)) // max allowed; effectively unlimited in tests
	mustSet(s.RateLimitAdmin.Set(context.Background(), 1000)) // max allowed; effectively unlimited in tests
	mustSet(s.SessionMaxAge.Set(context.Background(), 28800)) // 8 hours
	mustSet(s.StatusAllowed.Set(context.Background(), "0.0.0.0/0"))
	return s
}

type fakeBGP struct {
	reconciles int
	reloads    int
	adds       int
	updates    int
	deletes    int

	// down/downErr let tests simulate a failed BGP speaker via Status().
	// Zero value is healthy (running=true, lastErr=nil), matching every
	// existing test that constructs a bare &fakeBGP{}.
	down    bool
	downErr error
}

func (f *fakeBGP) Reconcile(context.Context) error {
	f.reconciles++
	if f.down {
		return f.downErr
	}
	return nil
}

func (f *fakeBGP) ReloadPeers(context.Context) error {
	f.reloads++
	if f.down {
		return f.downErr
	}
	return nil
}

func (f *fakeBGP) PeerStates(context.Context) (map[string]string, error) {
	return map[string]string{"172.16.0.2:65001": "ESTABLISHED"}, nil
}

func (f *fakeBGP) AddPeer(context.Context, store.User) error {
	f.adds++
	return nil
}

func (f *fakeBGP) UpdatePeer(context.Context, store.User) error {
	f.updates++
	return nil
}

func (f *fakeBGP) DeletePeer(context.Context, string, int64) error {
	f.deletes++
	return nil
}

func (f *fakeBGP) Status() (bool, error) {
	return !f.down, f.downErr
}

// adminCookie returns a valid admin session cookie for API tests.
func adminCookie(s *settings.Settings) *http.Cookie {
	return &http.Cookie{Name: "wdbgp_admin", Value: sessionToken(s.SessionSecret.Get())} //nolint:gosec // test helper, no security attrs needed
}

// testCSRFToken is a fixed value shared between the cookie and header set by
// addCSRF — the double-submit check only cares that they match, not what the
// value is.
const testCSRFToken = "test-csrf-token" //nolint:gosec // test-only fixed value, not a real secret

// addCSRF attaches a matching CSRF cookie and header to req, satisfying
// validCSRFRequest for mutating requests routed through apiRequireAdmin or
// requireUser.
func addCSRF(req *http.Request) {
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: testCSRFToken}) //nolint:gosec // test helper
	req.Header.Set(csrfHeaderName, testCSRFToken)
}

// =============================================================================
// Status endpoint
// =============================================================================

func TestStatusEndpoint(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "status.sqlite3"), false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close() //nolint:errcheck,gosec // test cleanup

	// Create some test data
	_, err = db.AddUser(context.Background(), store.User{
		Name: "test-client", PeerIP: "172.16.0.3", PeerASN: 65002, Enabled: true,
		Networks: []string{"192.168.30.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Add a feed
	_, err = db.AddFeed(
		context.Background(),
		"test-feed",
		"http://example.com/feed.json",
		1,
		true,
		0,
		"",
		"",
		true,
	)
	if err != nil {
		t.Fatal(err)
	}

	s := testSettings()
	syncer := feeds.NewSyncer(db, testSettings())
	bgp := &fakeBGP{}
	server := New(s, db, syncer, bgp)

	req := httptest.NewRequest("GET", "/status", nil)
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, req)

	resp := w.Result()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", resp.StatusCode, body)
	}

	// Check that it's valid JSON
	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		t.Fatalf("failed to parse JSON response: %v\nbody: %s", err, body)
	}

	// Check basic structure
	if _, ok := data["uptime"]; !ok {
		t.Error("response missing uptime field")
	}
	if _, ok := data["database"]; !ok {
		t.Error("response missing database field")
	}
	if _, ok := data["bgp"]; !ok {
		t.Error("response missing bgp field")
	}
	if _, ok := data["feeds"]; !ok {
		t.Error("response missing feeds field")
	}
	if _, ok := data["prefixes"]; !ok {
		t.Error("response missing prefixes field")
	}
	if _, ok := data["build"]; !ok {
		t.Error("response missing build field")
	}

	// Check BGP data
	bgpData, ok := data["bgp"].(map[string]any)
	if !ok {
		t.Error("bgp field is not an object")
	}
	if total, ok := bgpData["total_peers"].(float64); !ok || total != 1 {
		t.Errorf("expected total_peers=1, got %v", total)
	}

	// Check database data
	dbData, ok := data["database"].(map[string]any)
	if !ok {
		t.Error("database field is not an object")
	}
	if connected, ok := dbData["connected"].(bool); !ok || !connected {
		t.Error("database should show as connected")
	}
}

// =============================================================================
// Degraded mode
// =============================================================================

func TestDegradedModeShowsErrorPage(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"), false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close() //nolint:errcheck,gosec // test cleanup

	s := testSettings()
	mustSetSetting(t, s.DefaultLanguage, "en")
	srv := New(s, db, feeds.NewSyncer(db, testSettings()), &fakeBGP{})
	srv.SetDegraded(DegradedInfo{
		CurrentVersion: 999,
		ServerVersion:  20,
		Reason:         "test reason",
	})
	handler := srv.Handler()

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.Code)
	}
	body := response.Body.String()
	if !strings.Contains(body, "999") {
		t.Fatalf("body missing current DB version 999: %s", body)
	}
	if !strings.Contains(body, "20") {
		t.Fatalf("body missing server version 20: %s", body)
	}
	if !strings.Contains(body, "Mismatch") {
		t.Fatalf("body missing 'Mismatch' from title.db_mismatch: %s", body)
	}
}

func TestDegradedModeAllRoutesShowError(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"), false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close() //nolint:errcheck,gosec // test cleanup

	s := testSettings()
	mustSetSetting(t, s.DefaultLanguage, "en")
	srv := New(s, db, feeds.NewSyncer(db, testSettings()), &fakeBGP{})
	srv.SetDegraded(DegradedInfo{
		CurrentVersion: 999,
		ServerVersion:  20,
		Reason:         "test reason",
	})
	handler := srv.Handler()

	for _, path := range []string{"/admin", "/login", "/healthz"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: status = %d, want 503", path, response.Code)
		}
		if !strings.Contains(response.Body.String(), "Mismatch") {
			t.Errorf("%s: body missing degraded page content", path)
		}
	}
}

func TestDegradedModeLanguageSwitch(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"), false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close() //nolint:errcheck,gosec // test cleanup

	srv := New(testSettings(), db, feeds.NewSyncer(db, testSettings()), &fakeBGP{})
	srv.SetDegraded(DegradedInfo{
		CurrentVersion: 999,
		ServerVersion:  20,
		Reason:         "test reason",
	})
	handler := srv.Handler()

	// Russian
	request := httptest.NewRequest(http.MethodGet, "/?lang=ru", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("ru: status = %d, want 503", response.Code)
	}
	body := response.Body.String()
	if !strings.Contains(body, "Несоответствие версии базы данных") {
		t.Fatalf("ru: body missing Russian title: %s", body)
	}

	// English
	request = httptest.NewRequest(http.MethodGet, "/?lang=en", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("en: status = %d, want 503", response.Code)
	}
	body = response.Body.String()
	if !strings.Contains(body, "Database Version Mismatch") {
		t.Fatalf("en: body missing English title: %s", body)
	}
}

// =============================================================================
// Translation / i18n
// =============================================================================

func TestTranslationCatalogsHaveMatchingKeys(t *testing.T) {
	for key := range translations[localeEnglish] {
		if translations[localeRussian][key] == "" {
			t.Errorf("Russian translation missing for %q", key)
		}
	}
	for key := range translations[localeRussian] {
		if translations[localeEnglish][key] == "" {
			t.Errorf("English translation missing for %q", key)
		}
	}
	if got := translate(localeEnglish, "missing.key"); got != "missing.key" {
		t.Fatalf("missing translation = %q, want key", got)
	}
}

// =============================================================================
// Peer uniqueness tests
// =============================================================================

func TestAddUserRejectsSameIPAndSameASN(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"), false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close() //nolint:errcheck,gosec // test cleanup
	ctx := context.Background()
	_, err = db.AddUser(ctx, store.User{
		Name: "first", PeerIP: "10.0.1.1", PeerASN: 65100, Enabled: true,
		Networks: []string{"192.168.1.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}

	s := testSettings()
	handler := New(s, db, feeds.NewSyncer(db, testSettings()), &fakeBGP{}).Handler()
	adminCookie := &http.Cookie{Name: "wdbgp_admin", Value: sessionToken(s.SessionSecret.Get())} //nolint:gosec // test code

	body := `{"name":"second","peer_ip":"10.0.1.1","peer_asn":65100,"networks":["192.168.2.0/24"],"enabled":true}`
	request := httptest.NewRequest(http.MethodPost, "/api/admin/users", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(adminCookie)
	addCSRF(request)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for duplicate IP+ASN, got %d body=%s",
			response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "already exists") {
		t.Fatalf("response should mention already exists: %s", response.Body.String())
	}
}

func TestAddUserAcceptsUniqueIPWithoutPassword(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"), false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close() //nolint:errcheck,gosec // test cleanup

	s := testSettings()
	handler := New(s, db, feeds.NewSyncer(db, testSettings()), &fakeBGP{}).Handler()
	adminCookie := &http.Cookie{Name: "wdbgp_admin", Value: sessionToken(s.SessionSecret.Get())} //nolint:gosec // test code

	body := `{"name":"unique-peer","peer_ip":"10.0.2.1","peer_asn":65100,"networks":["192.168.3.0/24"],"enabled":true}`
	request := httptest.NewRequest(http.MethodPost, "/api/admin/users", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(adminCookie)
	addCSRF(request)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for unique IP without password, got %d body=%s",
			response.Code, response.Body.String())
	}
}

func TestAddUserDynamicPeersNoPasswordRequired(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"), false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close() //nolint:errcheck,gosec // test cleanup

	s := testSettings()
	mustSetSetting(t, s.AllowDynamicPeers, true)
	handler := New(s, db, feeds.NewSyncer(db, testSettings()), &fakeBGP{}).Handler()
	adminCookie := &http.Cookie{Name: "wdbgp_admin", Value: sessionToken(s.SessionSecret.Get())} //nolint:gosec // test code

	// Without password — should succeed (password is not required for dynamic peers)
	body := `{"name":"dynamic-no-pw","peer_ip":"0.0.0.0","peer_asn":65100,"networks":["10.90.0.0/24"],"enabled":true}`
	request := httptest.NewRequest(http.MethodPost, "/api/admin/users", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(adminCookie)
	addCSRF(request)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for dynamic peer without password, got %d body=%s",
			response.Code, response.Body.String())
	}

	// With password — should also succeed
	body = `{"name":"dynamic-with-pw","peer_ip":"0.0.0.0","peer_asn":65101,"bgp_password":"secret123","password_enabled":true,"networks":["10.91.0.0/24"],"enabled":true}`
	request = httptest.NewRequest(http.MethodPost, "/api/admin/users", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(adminCookie)
	addCSRF(request)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for dynamic peer with password, got %d body=%s",
			response.Code, response.Body.String())
	}
}

func TestAddUserRejectsDuplicateDynamicPeerASN(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"), false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close() //nolint:errcheck,gosec // test cleanup
	ctx := context.Background()
	_, err = db.AddUser(ctx, store.User{
		Name: "first-dynamic", PeerIP: "0.0.0.0", PeerASN: 65100,
		BGPPassword: "secret1", Enabled: true,
		Networks: []string{"0.0.0.0/0"},
	})
	if err != nil {
		t.Fatal(err)
	}

	s := testSettings()
	mustSetSetting(t, s.AllowDynamicPeers, true)
	handler := New(s, db, feeds.NewSyncer(db, testSettings()), &fakeBGP{}).Handler()
	adminCookie := &http.Cookie{Name: "wdbgp_admin", Value: sessionToken(s.SessionSecret.Get())} //nolint:gosec // test code

	body := `{"name":"second-dynamic","peer_ip":"0.0.0.0","peer_asn":65100,"bgp_password":"secret2","password_enabled":true,"networks":["0.0.0.0/0"],"enabled":true}`
	request := httptest.NewRequest(http.MethodPost, "/api/admin/users", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(adminCookie)
	addCSRF(request)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for duplicate dynamic peer ASN, got %d body=%s",
			response.Code, response.Body.String())
	}
	// Step A (same IP + same ASN) fires before Step B (globally unique ASN for dynamic).
	// For 0.0.0.0, Step A catches it because the same IP (0.0.0.0) and same ASN (65100) are duplicated.
	if !strings.Contains(response.Body.String(), "already exists") {
		t.Fatalf("response should mention already exists: %s", response.Body.String())
	}
}

func TestAddUserRejectsSharedIPWithoutPasswordWhenRequired(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"), false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close() //nolint:errcheck,gosec // test cleanup
	ctx := context.Background()
	_, err = db.AddUser(ctx, store.User{
		Name: "first-shared", PeerIP: "10.0.3.1", PeerASN: 65101, Enabled: true,
		Networks: []string{"192.168.10.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}

	s := testSettings()
	mustSetSetting(t, s.RequirePasswordForNonUniqueIP, true)
	handler := New(s, db, feeds.NewSyncer(db, testSettings()), &fakeBGP{}).Handler()
	adminCookie := &http.Cookie{Name: "wdbgp_admin", Value: sessionToken(s.SessionSecret.Get())} //nolint:gosec // test code

	body := `{"name":"second-shared","peer_ip":"10.0.3.1","peer_asn":65102,"networks":["192.168.11.0/24"],"enabled":true}`
	request := httptest.NewRequest(http.MethodPost, "/api/admin/users", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(adminCookie)
	addCSRF(request)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for shared IP without password, got %d body=%s",
			response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "password required") {
		t.Fatalf("response should mention password required: %s", response.Body.String())
	}
}

func TestAddUserAcceptsSharedIPWithPassword(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"), false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close() //nolint:errcheck,gosec // test cleanup
	ctx := context.Background()
	_, err = db.AddUser(ctx, store.User{
		Name: "first-shared-pw", PeerIP: "10.0.4.1", PeerASN: 65101, Enabled: true,
		BGPPassword: "shared-secret",
		Networks:    []string{"192.168.20.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}

	s := testSettings()
	handler := New(s, db, feeds.NewSyncer(db, testSettings()), &fakeBGP{}).Handler()
	adminCookie := &http.Cookie{Name: "wdbgp_admin", Value: sessionToken(s.SessionSecret.Get())} //nolint:gosec // test code

	body := `{"name":"second-shared-pw","peer_ip":"10.0.4.1","peer_asn":65102,"bgp_password":"shared-secret","password_enabled":true,"networks":["192.168.21.0/24"],"enabled":true}`
	request := httptest.NewRequest(http.MethodPost, "/api/admin/users", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(adminCookie)
	addCSRF(request)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for shared IP with password, got %d body=%s",
			response.Code, response.Body.String())
	}
}

func TestSameIPv4RequiresMatchingPassword(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "same-ipv4-pw.sqlite3"), false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close() //nolint:errcheck,gosec // test cleanup
	ctx := context.Background()

	// Add first peer with password "apple"
	_, err = db.AddUser(ctx, store.User{
		Name: "alice", PeerIP: "192.0.2.1", PeerASN: 65001,
		BGPPassword: "apple", Enabled: true,
		Networks: []string{"192.168.100.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}

	s := testSettings()
	handler := New(s, db, feeds.NewSyncer(db, testSettings()), &fakeBGP{}).Handler()
	adminCookie := &http.Cookie{Name: "wdbgp_admin", Value: sessionToken(s.SessionSecret.Get())} //nolint:gosec // test code

	// Try to add second peer with different password "banana"
	body := `{"name":"bob","peer_ip":"192.0.2.1","peer_asn":65002,"bgp_password":"banana","password_enabled":true,"networks":["192.168.101.0/24"],"enabled":true}`
	request := httptest.NewRequest(http.MethodPost, "/api/admin/users", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(adminCookie)
	addCSRF(request)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for mismatched password on shared IP, got %d body=%s",
			response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "password must match") {
		t.Fatalf("response should mention password must match: %s", response.Body.String())
	}
}

func TestSameIPv6RequiresMatchingPassword(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "same-ipv6-pw.sqlite3"), false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close() //nolint:errcheck,gosec // test cleanup
	ctx := context.Background()

	// Add first peer with password "apple"
	_, err = db.AddUser(ctx, store.User{
		Name: "alice6", PeerIP: "fd00::1", PeerASN: 65101,
		BGPPassword: "apple", Enabled: true,
		Networks: []string{"fd00:cafe::/48"},
	})
	if err != nil {
		t.Fatal(err)
	}

	s := testSettings()
	handler := New(s, db, feeds.NewSyncer(db, testSettings()), &fakeBGP{}).Handler()
	adminCookie := &http.Cookie{Name: "wdbgp_admin", Value: sessionToken(s.SessionSecret.Get())} //nolint:gosec // test code

	// Try to add second peer with different password "banana"
	body := `{"name":"bob6","peer_ip":"fd00::1","peer_asn":65102,"bgp_password":"banana","password_enabled":true,"networks":["fd00:babe::/48"],"enabled":true}`
	request := httptest.NewRequest(http.MethodPost, "/api/admin/users", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(adminCookie)
	addCSRF(request)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for mismatched password on shared IPv6, got %d body=%s",
			response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "password must match") {
		t.Fatalf("response should mention password must match: %s", response.Body.String())
	}
}

// =============================================================================
// API tests
// =============================================================================

// TestAdminLoginAPI tests POST /api/admin/login
func TestAdminLoginAPI(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "api.sqlite3"), false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	}()

	s := testSettings()
	handler := New(s, db, feeds.NewSyncer(db, testSettings()), &fakeBGP{}).Handler()

	// Wrong password
	body := `{"password": "wrong"}`
	req := httptest.NewRequest(http.MethodPost, "/api/admin/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: status=%d, want 401", rec.Code)
	}

	// Correct password
	body = `{"password": "admin"}`
	req = httptest.NewRequest(http.MethodPost, "/api/admin/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("correct password: status=%d, want 200", rec.Code)
	}
	// Check cookies — login sets both the session cookie and a paired CSRF cookie.
	cookies := rec.Result().Cookies()
	if len(cookies) != 2 || cookies[0].Name != "wdbgp_admin" || cookies[1].Name != csrfCookieName {
		t.Fatalf("expected wdbgp_admin and %s cookies, got %v", csrfCookieName, cookies)
	}

	// Test /api/admin/me with the cookie
	req = httptest.NewRequest(http.MethodGet, "/api/admin/me", nil)
	req.AddCookie(cookies[0])
	addCSRF(req)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/admin/me: status=%d", rec.Code)
	}
}

// TestAdminSettingsAPI tests GET/PUT /api/admin/settings
func TestAdminSettingsAPI(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "api.sqlite3"), false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	}()

	s := testSettings()
	handler := New(s, db, feeds.NewSyncer(db, testSettings()), &fakeBGP{}).Handler()
	cookie := adminCookie(s)

	// GET settings
	req := httptest.NewRequest(http.MethodGet, "/api/admin/settings", nil)
	req.AddCookie(cookie)
	addCSRF(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET settings: status=%d", rec.Code)
	}
	var resp settings.SettingsJSON
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Port.DefaultValue != 8080 {
		t.Fatal("port default should be 8080 in typed SettingsJSON")
	}

	// PUT settings
	body := `{"default_language": "ru"}`
	req = httptest.NewRequest(http.MethodPut, "/api/admin/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	addCSRF(req)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT settings: status=%d", rec.Code)
	}
}

// TestSettingsNullValueDeletesOverride verifies that sending null for a setting
// key removes it from the database (use default / clear override).
func TestSettingsNullValueDeletesOverride(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "null-settings.sqlite3"), false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	}()

	s, err := settings.New(db)
	if err != nil {
		t.Fatal(err)
	}
	mustSetSetting(t, s.AdminPassword, "admin")
	mustSetSetting(t, s.SessionSecret, "test-secret")
	mustSetSetting(t, s.AdminCookieSecure, "true")
	mustSetSetting(t, s.RateLimitLogin, 1000)
	mustSetSetting(t, s.RateLimitAdmin, 1000)
	mustSetSetting(t, s.SessionMaxAge, 28800)
	mustSetSetting(t, s.StatusAllowed, "0.0.0.0/0")

	handler := New(s, db, feeds.NewSyncer(db, s), &fakeBGP{}).Handler()
	cookie := adminCookie(s)

	// 1. Save a setting with a non-null value.
	put1 := `{"default_language":"en"}`
	req := httptest.NewRequest(http.MethodPut, "/api/admin/settings", strings.NewReader(put1))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	addCSRF(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT settings (set): status=%d", rec.Code)
	}

	// Verify it exists in DB.
	all, err := db.GetAllSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if all["default_language"] != "en" {
		t.Fatalf("expected default_language=en, got %q", all["default_language"])
	}

	// 2. PUT with null value for that key — should delete the override.
	put2 := `{"default_language":null}`
	req = httptest.NewRequest(http.MethodPut, "/api/admin/settings", strings.NewReader(put2))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	addCSRF(req)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT settings (null): status=%d", rec.Code)
	}

	// Verify it is gone from DB.
	all, err = db.GetAllSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := all["default_language"]; exists {
		t.Fatal("expected default_language to be deleted from DB, but it still exists")
	}
}

// TestAdapterCRUDAPI tests adapter CRUD + fork + acknowledge
func TestAdapterCRUDAPI(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "api.sqlite3"), false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	}()

	s := testSettings()
	handler := New(s, db, feeds.NewSyncer(db, testSettings()), &fakeBGP{}).Handler()
	cookie := adminCookie(s)

	// List adapters
	req := httptest.NewRequest(http.MethodGet, "/api/admin/adapters", nil)
	req.AddCookie(cookie)
	addCSRF(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatal("list adapters failed")
	}
	var listResp struct {
		Adapters []struct {
			ID         int64  `json:"id"`
			BuiltIn    bool   `json:"builtin"`
			Name       string `json:"name"`
			ForkedFrom int64  `json:"forked_from"`
		} `json:"adapters"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&listResp); err != nil {
		t.Fatal(err)
	}
	if len(listResp.Adapters) == 0 {
		t.Fatal("no adapters")
	}

	// Find a built-in adapter
	var builtin int64
	for _, a := range listResp.Adapters {
		if a.BuiltIn {
			builtin = a.ID
			break
		}
	}
	if builtin == 0 {
		t.Fatal("no built-in adapter found")
	}

	// Fork it
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/admin/adapters/%d/fork", builtin), nil)
	req.AddCookie(cookie)
	addCSRF(req)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("fork adapter: status=%d, body=%s", rec.Code, rec.Body.String())
	}
	var forked struct {
		ID         int64  `json:"id"`
		Name       string `json:"name"`
		ForkedFrom int64  `json:"forked_from"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&forked); err != nil {
		t.Fatal(err)
	}
	if forked.ForkedFrom == 0 {
		t.Fatal("forked adapter missing forked_from")
	}
	if !strings.Contains(forked.Name, "_copy_") {
		t.Fatalf("forked name should contain _copy_: %s", forked.Name)
	}

	// Acknowledge the fork
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/admin/adapters/%d/acknowledge", forked.ID), nil)
	req.AddCookie(cookie)
	addCSRF(req)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("acknowledge: status=%d", rec.Code)
	}

	// Delete forked adapter
	req = httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/admin/adapters/%d", forked.ID), nil)
	req.AddCookie(cookie)
	addCSRF(req)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete forked: status=%d", rec.Code)
	}

	// Verify built-in cannot be deleted
	req = httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/admin/adapters/%d", builtin), nil)
	req.AddCookie(cookie)
	addCSRF(req)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("delete builtin: status=%d, want 400", rec.Code)
	}

	// Verify built-in cannot be edited
	editBody := `{"name": "hacked", "source": "function sync(){}", "allowed_hosts": ""}`
	req = httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/admin/adapters/%d", builtin), strings.NewReader(editBody))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	addCSRF(req)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("edit builtin: status=%d, want 400", rec.Code)
	}
}

// TestAdminLogoutAPI tests POST /api/admin/logout
func TestAdminLogoutAPI(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "api.sqlite3"), false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	}()

	s := testSettings()
	handler := New(s, db, feeds.NewSyncer(db, testSettings()), &fakeBGP{}).Handler()

	// Login to get real cookie
	loginBody := `{"password": "admin"}`
	req := httptest.NewRequest(http.MethodPost, "/api/admin/login", strings.NewReader(loginBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	cookies := rec.Result().Cookies()

	// Logout
	req = httptest.NewRequest(http.MethodPost, "/api/admin/logout", nil)
	req.AddCookie(cookies[0])
	addCSRF(req)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("logout: status=%d", rec.Code)
	}

	// Verify /me is now unauthorized (browser would have deleted the cookie)
	req = httptest.NewRequest(http.MethodGet, "/api/admin/me", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("/me after logout: status=%d, want 401", rec.Code)
	}
}

// =============================================================================
// BGP status/reload API
// =============================================================================

func TestBGPStatusReflectsSpeakerHealth(t *testing.T) {
	s := testSettings()
	fake := &fakeBGP{}
	server := New(s, nil, nil, fake)
	handler := server.Handler()
	cookie := adminCookie(s)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/bgp/status", nil)
	req.AddCookie(cookie)
	addCSRF(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp bgpStatusJSON
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Running || resp.RestartPending || resp.LastError != "" {
		t.Fatalf("healthy fakeBGP: got %+v, want running=true, restart_pending=false, last_error=\"\"", resp)
	}

	// Simulate a failed speaker.
	fake.down = true
	fake.downErr = fmt.Errorf("bind: address already in use")

	req = httptest.NewRequest(http.MethodGet, "/api/admin/bgp/status", nil)
	req.AddCookie(cookie)
	addCSRF(req)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Running || resp.LastError != "bind: address already in use" {
		t.Fatalf("down fakeBGP: got %+v, want running=false, last_error set", resp)
	}
}

func TestBGPStatusReflectsRestartPendingAfterSettingChange(t *testing.T) {
	s := testSettings()
	server := New(s, nil, nil, &fakeBGP{})
	handler := server.Handler()
	cookie := adminCookie(s)

	assertPending := func(want bool) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/admin/bgp/status", nil)
		req.AddCookie(cookie)
		addCSRF(req)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		var resp bgpStatusJSON
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatal(err)
		}
		if resp.RestartPending != want {
			t.Fatalf("restart_pending = %v, want %v", resp.RestartPending, want)
		}
	}

	assertPending(false)

	if err := s.LocalASN.Set(context.Background(), 65001); err != nil {
		t.Fatal(err)
	}
	assertPending(true)
}

// TestBGPStatusReflectsRestartPendingAfterActiveDialChange guards against
// active_dial being able to change an already-connected peer's dial mode
// (buildPeerConfigs reads it live) without any way for the admin to notice
// or apply it — Reconcile only re-announces routes, it never calls
// SetPeers, so nothing actually picks up the new dial mode until a reload.
func TestBGPStatusReflectsRestartPendingAfterActiveDialChange(t *testing.T) {
	s := testSettings()
	server := New(s, nil, nil, &fakeBGP{})
	handler := server.Handler()
	cookie := adminCookie(s)

	assertPending := func(want bool) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/admin/bgp/status", nil)
		req.AddCookie(cookie)
		addCSRF(req)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		var resp bgpStatusJSON
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatal(err)
		}
		if resp.RestartPending != want {
			t.Fatalf("restart_pending = %v, want %v", resp.RestartPending, want)
		}
	}

	assertPending(false)

	if err := s.ActiveDial.Set(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	assertPending(true)
}

func TestBGPReloadClearsRestartPendingOnSuccess(t *testing.T) {
	s := testSettings()
	fake := &fakeBGP{}
	server := New(s, nil, nil, fake)
	handler := server.Handler()
	cookie := adminCookie(s)

	if err := s.RouterID.Set(context.Background(), "192.0.2.9"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/admin/bgp/reload", nil)
	req.AddCookie(cookie)
	addCSRF(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("reload status = %d, want 200", rec.Code)
	}
	if fake.reloads != 1 {
		t.Fatalf("ReloadPeers called %d times, want 1", fake.reloads)
	}
	var resp bgpStatusJSON
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.RestartPending {
		t.Fatal("restart_pending should be cleared after a successful reload")
	}
}

func TestBGPReloadReportsFailureAndKeepsRestartPending(t *testing.T) {
	s := testSettings()
	fake := &fakeBGP{down: true, downErr: fmt.Errorf("bind: address already in use")}
	server := New(s, nil, nil, fake)
	handler := server.Handler()
	cookie := adminCookie(s)

	if err := s.LocalASN.Set(context.Background(), 65001); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/admin/bgp/reload", nil)
	req.AddCookie(cookie)
	addCSRF(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("reload status = %d, want 500", rec.Code)
	}
	var resp bgpStatusJSON
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Running {
		t.Fatal("running should be false after a failed reload")
	}
	if resp.LastError != "bind: address already in use" {
		t.Fatalf("last_error = %q, want the reload failure", resp.LastError)
	}
	if !resp.RestartPending {
		t.Fatal("restart_pending should stay true — the failed reload never applied the pending change")
	}
}
