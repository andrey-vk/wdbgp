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

	"github.com/andrey-vk/wdbgp/internal/config"
	"github.com/andrey-vk/wdbgp/internal/feeds"
	"github.com/andrey-vk/wdbgp/internal/store"
)

func testConfig() config.Config {
	return config.Config{
		AdminPassword:     "admin",
		SessionSecret:     "test-secret",
		AdminCookieSecure: "true",
		DefaultLanguage:   "ru",
		RateLimitLogin:    0,                     // Disable rate limiting in tests
		RateLimitAdmin:    0,                     // Disable rate limiting in tests
		SessionMaxAge:     28800,                 // 8 hours
		SecurityHeaders:   false,                 // Disable security headers in tests to avoid CSP issues
		StatusAllowed:     []string{"0.0.0.0/0"}, // Allow all IPs for tests
	}
}

type fakeBGP struct {
	reconciles int
	reloads    int
	adds       int
	updates    int
	deletes    int
}

func (f *fakeBGP) Reconcile(context.Context) error {
	f.reconciles++
	return nil
}

func (f *fakeBGP) ReloadPeers(context.Context) error {
	f.reloads++
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

// adminCookie returns a valid admin session cookie for API tests.
func adminCookie(cfg config.Config) *http.Cookie {
	return &http.Cookie{Name: "wdbgp_admin", Value: sessionToken(cfg.SessionSecret)} //nolint:gosec // test helper, no security attrs needed
}

// =============================================================================
// Status endpoint
// =============================================================================

func TestStatusEndpoint(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "status.sqlite3"), config.Config{})
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

	cfg := testConfig()
	syncer := feeds.NewSyncer(db, config.Config{})
	bgp := &fakeBGP{}
	server := New(cfg, db, syncer, bgp)

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
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"), config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close() //nolint:errcheck,gosec // test cleanup

	cfg := testConfig()
	cfg.DefaultLanguage = "en"
	srv := New(cfg, db, feeds.NewSyncer(db, config.Config{}), &fakeBGP{})
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
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"), config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close() //nolint:errcheck,gosec // test cleanup

	cfg := testConfig()
	cfg.DefaultLanguage = "en"
	srv := New(cfg, db, feeds.NewSyncer(db, config.Config{}), &fakeBGP{})
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
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"), config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close() //nolint:errcheck,gosec // test cleanup

	srv := New(testConfig(), db, feeds.NewSyncer(db, config.Config{}), &fakeBGP{})
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

func TestSelectionPluralTranslations(t *testing.T) {
	tests := []struct {
		lang  locale
		count int
		want  string
	}{
		{localeEnglish, 1, "category"},
		{localeEnglish, 2, "categories"},
		{localeRussian, 1, "категория"},
		{localeRussian, 2, "категории"},
		{localeRussian, 5, "категорий"},
		{localeRussian, 11, "категорий"},
		{localeRussian, 22, "категории"},
	}
	for _, test := range tests {
		got := pluralTranslation(test.lang, test.count,
			"selection.category_one", "selection.category_few", "selection.category_many")
		if got != test.want {
			t.Errorf("pluralTranslation(%q, %d) = %q, want %q",
				test.lang, test.count, got, test.want)
		}
	}
}

// =============================================================================
// Peer uniqueness tests
// =============================================================================

func TestAddUserRejectsSameIPAndSameASN(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"), config.Config{})
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

	cfg := testConfig()
	handler := New(cfg, db, feeds.NewSyncer(db, config.Config{}), &fakeBGP{}).Handler()
	adminCookie := &http.Cookie{Name: "wdbgp_admin", Value: sessionToken(cfg.SessionSecret)} //nolint:gosec // test code

	body := `{"name":"second","peer_ip":"10.0.1.1","peer_asn":65100,"networks":["192.168.2.0/24"],"enabled":true}`
	request := httptest.NewRequest(http.MethodPost, "/api/admin/users", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(adminCookie)
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
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"), config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close() //nolint:errcheck,gosec // test cleanup

	cfg := testConfig()
	handler := New(cfg, db, feeds.NewSyncer(db, config.Config{}), &fakeBGP{}).Handler()
	adminCookie := &http.Cookie{Name: "wdbgp_admin", Value: sessionToken(cfg.SessionSecret)} //nolint:gosec // test code

	body := `{"name":"unique-peer","peer_ip":"10.0.2.1","peer_asn":65100,"networks":["192.168.3.0/24"],"enabled":true}`
	request := httptest.NewRequest(http.MethodPost, "/api/admin/users", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(adminCookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for unique IP without password, got %d body=%s",
			response.Code, response.Body.String())
	}
}

func TestAddUserDynamicPeersNoPasswordRequired(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"), config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close() //nolint:errcheck,gosec // test cleanup

	cfg := testConfig()
	cfg.AllowDynamicPeers = true
	handler := New(cfg, db, feeds.NewSyncer(db, config.Config{}), &fakeBGP{}).Handler()
	adminCookie := &http.Cookie{Name: "wdbgp_admin", Value: sessionToken(cfg.SessionSecret)} //nolint:gosec // test code

	// Without password — should succeed (password is not required for dynamic peers)
	body := `{"name":"dynamic-no-pw","peer_ip":"0.0.0.0","peer_asn":65100,"networks":["0.0.0.0/0"],"enabled":true}`
	request := httptest.NewRequest(http.MethodPost, "/api/admin/users", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(adminCookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for dynamic peer without password, got %d body=%s",
			response.Code, response.Body.String())
	}

	// With password — should also succeed
	body = `{"name":"dynamic-with-pw","peer_ip":"0.0.0.0","peer_asn":65101,"bgp_password":"secret123","password_enabled":true,"networks":["10.0.0.0/8"],"enabled":true}`
	request = httptest.NewRequest(http.MethodPost, "/api/admin/users", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(adminCookie)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for dynamic peer with password, got %d body=%s",
			response.Code, response.Body.String())
	}
}

func TestAddUserRejectsDuplicateDynamicPeerASN(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"), config.Config{})
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

	cfg := testConfig()
	cfg.AllowDynamicPeers = true
	handler := New(cfg, db, feeds.NewSyncer(db, config.Config{}), &fakeBGP{}).Handler()
	adminCookie := &http.Cookie{Name: "wdbgp_admin", Value: sessionToken(cfg.SessionSecret)} //nolint:gosec // test code

	body := `{"name":"second-dynamic","peer_ip":"0.0.0.0","peer_asn":65100,"bgp_password":"secret2","password_enabled":true,"networks":["0.0.0.0/0"],"enabled":true}`
	request := httptest.NewRequest(http.MethodPost, "/api/admin/users", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(adminCookie)
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
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"), config.Config{})
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

	cfg := testConfig()
	cfg.RequirePasswordForNonUniqueIP = true
	handler := New(cfg, db, feeds.NewSyncer(db, config.Config{}), &fakeBGP{}).Handler()
	adminCookie := &http.Cookie{Name: "wdbgp_admin", Value: sessionToken(cfg.SessionSecret)} //nolint:gosec // test code

	body := `{"name":"second-shared","peer_ip":"10.0.3.1","peer_asn":65102,"networks":["192.168.11.0/24"],"enabled":true}`
	request := httptest.NewRequest(http.MethodPost, "/api/admin/users", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(adminCookie)
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
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"), config.Config{})
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

	cfg := testConfig()
	handler := New(cfg, db, feeds.NewSyncer(db, config.Config{}), &fakeBGP{}).Handler()
	adminCookie := &http.Cookie{Name: "wdbgp_admin", Value: sessionToken(cfg.SessionSecret)} //nolint:gosec // test code

	body := `{"name":"second-shared-pw","peer_ip":"10.0.4.1","peer_asn":65102,"bgp_password":"shared-secret","password_enabled":true,"networks":["192.168.21.0/24"],"enabled":true}`
	request := httptest.NewRequest(http.MethodPost, "/api/admin/users", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(adminCookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for shared IP with password, got %d body=%s",
			response.Code, response.Body.String())
	}
}

func TestSameIPv4RequiresMatchingPassword(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "same-ipv4-pw.sqlite3"), config.Config{})
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

	cfg := testConfig()
	handler := New(cfg, db, feeds.NewSyncer(db, config.Config{}), &fakeBGP{}).Handler()
	adminCookie := &http.Cookie{Name: "wdbgp_admin", Value: sessionToken(cfg.SessionSecret)} //nolint:gosec // test code

	// Try to add second peer with different password "banana"
	body := `{"name":"bob","peer_ip":"192.0.2.1","peer_asn":65002,"bgp_password":"banana","password_enabled":true,"networks":["192.168.101.0/24"],"enabled":true}`
	request := httptest.NewRequest(http.MethodPost, "/api/admin/users", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(adminCookie)
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
	db, err := store.Open(filepath.Join(t.TempDir(), "same-ipv6-pw.sqlite3"), config.Config{})
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

	cfg := testConfig()
	handler := New(cfg, db, feeds.NewSyncer(db, config.Config{}), &fakeBGP{}).Handler()
	adminCookie := &http.Cookie{Name: "wdbgp_admin", Value: sessionToken(cfg.SessionSecret)} //nolint:gosec // test code

	// Try to add second peer with different password "banana"
	body := `{"name":"bob6","peer_ip":"fd00::1","peer_asn":65102,"bgp_password":"banana","password_enabled":true,"networks":["fd00:babe::/48"],"enabled":true}`
	request := httptest.NewRequest(http.MethodPost, "/api/admin/users", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(adminCookie)
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
	db, err := store.Open(filepath.Join(t.TempDir(), "api.sqlite3"), config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	}()

	cfg := testConfig()
	handler := New(cfg, db, feeds.NewSyncer(db, config.Config{}), &fakeBGP{}).Handler()

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
	// Check cookie
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "wdbgp_admin" {
		t.Fatalf("expected wdbgp_admin cookie, got %v", cookies)
	}

	// Test /api/admin/me with the cookie
	req = httptest.NewRequest(http.MethodGet, "/api/admin/me", nil)
	req.AddCookie(cookies[0])
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/admin/me: status=%d", rec.Code)
	}
}

// TestAdminSettingsAPI tests GET/PUT /api/admin/settings
func TestAdminSettingsAPI(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "api.sqlite3"), config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	}()

	cfg := testConfig()
	handler := New(cfg, db, feeds.NewSyncer(db, config.Config{}), &fakeBGP{}).Handler()
	cookie := adminCookie(cfg)

	// GET settings
	req := httptest.NewRequest(http.MethodGet, "/api/admin/settings", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET settings: status=%d", rec.Code)
	}
	var resp struct {
		Sections []struct {
			Name   string `json:"name"`
			Fields []struct {
				Key  string `json:"key"`
				Type string `json:"type"`
			} `json:"fields"`
		} `json:"sections"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Sections) == 0 {
		t.Fatal("no settings sections returned")
	}

	// PUT settings
	body := `{"default_language": "ru"}`
	req = httptest.NewRequest(http.MethodPut, "/api/admin/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT settings: status=%d", rec.Code)
	}
}

// TestSettingsNullValueDeletesOverride verifies that sending null for a setting
// key removes it from the database (use default / clear override).
func TestSettingsNullValueDeletesOverride(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "null-settings.sqlite3"), config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	}()

	cfg := testConfig()
	handler := New(cfg, db, feeds.NewSyncer(db, config.Config{}), &fakeBGP{}).Handler()
	cookie := adminCookie(cfg)

	// 1. Save a setting with a non-null value.
	put1 := `{"default_language":"en"}`
	req := httptest.NewRequest(http.MethodPut, "/api/admin/settings", strings.NewReader(put1))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
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
	db, err := store.Open(filepath.Join(t.TempDir(), "api.sqlite3"), config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	}()

	cfg := testConfig()
	handler := New(cfg, db, feeds.NewSyncer(db, config.Config{}), &fakeBGP{}).Handler()
	cookie := adminCookie(cfg)

	// List adapters
	req := httptest.NewRequest(http.MethodGet, "/api/admin/adapters", nil)
	req.AddCookie(cookie)
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
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("acknowledge: status=%d", rec.Code)
	}

	// Delete forked adapter
	req = httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/admin/adapters/%d", forked.ID), nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete forked: status=%d", rec.Code)
	}

	// Verify built-in cannot be deleted
	req = httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/admin/adapters/%d", builtin), nil)
	req.AddCookie(cookie)
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
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("edit builtin: status=%d, want 400", rec.Code)
	}
}

// TestAdminLogoutAPI tests POST /api/admin/logout
func TestAdminLogoutAPI(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "api.sqlite3"), config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	}()

	cfg := testConfig()
	handler := New(cfg, db, feeds.NewSyncer(db, config.Config{}), &fakeBGP{}).Handler()

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
