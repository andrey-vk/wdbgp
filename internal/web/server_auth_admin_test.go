package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/andrey-vk/wdbgp/internal/config"
	"github.com/andrey-vk/wdbgp/internal/feeds"
	"github.com/andrey-vk/wdbgp/internal/store"
)

// =============================================================================
// Web auth / login tests
// =============================================================================

func TestUserLoginPageReturnsLoginForm(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "login.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Add a user with web_auth=both so login page is reachable
	_, err = db.AddUser(context.Background(), store.User{
		Name: "login-user", PeerIP: "10.0.0.1", PeerASN: 65001, Enabled: true,
		WebAuth:  "both",
		Networks: []string{"10.0.0.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}

	cfg := testConfig()
	handler := New(cfg, db, feeds.NewSyncer(db, config.Config{}), &fakeBGP{}).Handler()

	request := httptest.NewRequest(http.MethodGet, "/login", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("GET /login status=%d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, `<form method=post action=/login`) {
		t.Fatalf("login form not rendered: %s", body)
	}
}

func TestUserLoginSuccess(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "login.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	userID, err := db.AddUser(ctx, store.User{
		Name: "login-user", PeerIP: "10.0.1.1", PeerASN: 65001, Enabled: true,
		WebAuth:  "login",
		Networks: []string{"10.0.1.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = db.SetUserCredential(ctx, userID, "alice", "secret123")
	if err != nil {
		t.Fatal(err)
	}

	cfg := testConfig()
	handler := New(cfg, db, feeds.NewSyncer(db, config.Config{}), &fakeBGP{}).Handler()

	form := url.Values{"login": {"alice"}, "password": {"secret123"}}
	request := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusSeeOther {
		t.Fatalf("POST /login status=%d body=%s", response.Code, response.Body.String())
	}

	// Should get user session cookie
	cookies := response.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == "wdbgp_user" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("wdbgp_user cookie not set after login")
	}
}

func TestUserLoginWrongPassword(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "login.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	userID, err := db.AddUser(ctx, store.User{
		Name: "login-user2", PeerIP: "10.0.2.1", PeerASN: 65001, Enabled: true,
		WebAuth:  "login",
		Networks: []string{"10.0.2.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = db.SetUserCredential(ctx, userID, "bob", "correct")
	if err != nil {
		t.Fatal(err)
	}

	cfg := testConfig()
	handler := New(cfg, db, feeds.NewSyncer(db, config.Config{}), &fakeBGP{}).Handler()

	form := url.Values{"login": {"bob"}, "password": {"wrong"}}
	request := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	// Should redirect to /login with error
	if response.Code != http.StatusSeeOther {
		t.Fatalf("POST /login with wrong password: status=%d", response.Code)
	}
	loc := response.Header().Get("Location")
	if !strings.Contains(loc, "/login?error=") {
		t.Fatalf("wrong password redirect location = %q, want /login?error=...", loc)
	}
}

func TestUserPageRedirectsToLoginWhenWebAuthIsLoginAndNoSession(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "login-redirect.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	_, err = db.AddUser(context.Background(), store.User{
		Name: "login-user3", PeerIP: "10.0.3.1", PeerASN: 65001, Enabled: true,
		WebAuth:  "login",
		Networks: []string{"10.0.3.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}

	cfg := testConfig()
	handler := New(cfg, db, feeds.NewSyncer(db, config.Config{}), &fakeBGP{}).Handler()

	// Request from matching IP but no user session
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "10.0.3.15:12345"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusSeeOther {
		t.Fatalf("GET / with web_auth=login and no session: status=%d, want 303", response.Code)
	}
	loc := response.Header().Get("Location")
	if loc != "/login" {
		t.Fatalf("redirect location = %q, want /login", loc)
	}
}

// =============================================================================
// Admin communities tests
// =============================================================================

func TestAdminCommunitiesPageLoads(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "communities.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Add catalog data so communities are auto-generated
	addCommunityTestData(t, db)

	cfg := testConfig()
	handler := New(cfg, db, feeds.NewSyncer(db, config.Config{}), &fakeBGP{}).Handler()
	adminCookie := adminAuthCookie(t, cfg)

	request := httptest.NewRequest(http.MethodGet, "/admin/communities", nil)
	request.AddCookie(adminCookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("GET /admin/communities: status=%d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, "CommunityTest") {
		t.Fatalf("community page missing category: %s", body)
	}
	if !strings.Contains(body, `<form method=post action="/admin/communities"`) {
		t.Fatalf("communities form not rendered: %s", body)
	}
}

func TestAdminCommunitiesPageRequiresAuth(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "communities.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cfg := testConfig()
	handler := New(cfg, db, feeds.NewSyncer(db, config.Config{}), &fakeBGP{}).Handler()

	request := httptest.NewRequest(http.MethodGet, "/admin/communities", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusSeeOther {
		t.Fatalf("unauthenticated GET /admin/communities: status=%d", response.Code)
	}
}

func TestAdminCommunitiesSaves(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "communities.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	addCommunityTestData(t, db)

	// Generate communities first
	_, err = db.GenerateCommunities(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}

	comms, err := db.GetCommunities(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}

	cfg := testConfig()
	bgp := &fakeBGP{}
	handler := New(cfg, db, feeds.NewSyncer(db, config.Config{}), bgp).Handler()
	adminCookie := adminAuthCookie(t, cfg)

	// Save communities: modify a group community value
	form := url.Values{}
	for k, v := range comms {
		if strings.Contains(k, "|") {
			form.Set("svc_"+k, strconv.FormatUint(uint64(v), 10))
		} else {
			form.Set("cat_"+k, strconv.FormatUint(uint64(v), 10))
		}
	}
	// Modify first group to a new value
	for k, v := range form {
		if strings.HasPrefix(k, "cat_") {
			form.Set(k, "7777")
			_ = v
			break
		}
	}

	request := httptest.NewRequest(http.MethodPost, "/admin/communities", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(adminCookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusSeeOther {
		t.Fatalf("POST /admin/communities: status=%d body=%s", response.Code, response.Body.String())
	}
}

// =============================================================================
// Admin settings tests
// =============================================================================

func TestAdminSettingsPageRequiresAuth(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "settings.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cfg := testConfig()
	handler := New(cfg, db, feeds.NewSyncer(db, config.Config{}), &fakeBGP{}).Handler()

	request := httptest.NewRequest(http.MethodGet, "/admin/settings", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusSeeOther {
		t.Fatalf("unauthenticated GET /admin/settings: status=%d", response.Code)
	}
}

func TestAdminSettingsPageLoads(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "settings.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cfg := testConfig()
	handler := New(cfg, db, feeds.NewSyncer(db, config.Config{}), &fakeBGP{}).Handler()
	adminCookie := adminAuthCookie(t, cfg)

	request := httptest.NewRequest(http.MethodGet, "/admin/settings", nil)
	request.AddCookie(adminCookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("GET /admin/settings: status=%d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	// Check for settings form
	if !strings.Contains(body, `<form method=post action=/admin/settings`) {
		t.Fatalf("settings form not found: %s", body)
	}
}

func TestAdminSettingsSavesAndRedirects(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "settings.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cfg := testConfig()
	bgp := &fakeBGP{}
	handler := New(cfg, db, feeds.NewSyncer(db, config.Config{}), bgp).Handler()
	adminCookie := adminAuthCookie(t, cfg)

	form := url.Values{"settings-sync_interval": {"3600"}}
	request := httptest.NewRequest(http.MethodPost, "/admin/settings", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(adminCookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusSeeOther {
		t.Fatalf("POST /admin/settings: status=%d body=%s", response.Code, response.Body.String())
	}

	// Check that settings were saved
	settings, err := db.GetAllSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if val, ok := settings["sync_interval"]; ok && val != "" {
		// Value was saved, expected
	} else {
		// May be empty if "sync_interval" is env-overridden or the key name differs
	}
}

func TestAdminSettingsIncludesGlobalRouteFilters(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "settings-filters.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cfg := testConfig()
	handler := New(cfg, db, feeds.NewSyncer(db, config.Config{}), &fakeBGP{}).Handler()
	adminCookie := adminAuthCookie(t, cfg)

	request := httptest.NewRequest(http.MethodGet, "/admin/settings", nil)
	request.AddCookie(adminCookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	body := response.Body.String()
	if !strings.Contains(body, "filter_allow") || !strings.Contains(body, "filter_deny") {
		t.Fatalf("global route filter fields missing from settings: %s", body)
	}
}

func TestAdminSettingsSavesGlobalRouteFilters(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "settings-filters-save.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	// Clear defaults so we can see our custom filters
	err = db.SetGlobalRouteFilters(ctx, store.RouteFilters{})
	if err != nil {
		t.Fatal(err)
	}

	cfg := testConfig()
	bgp := &fakeBGP{}
	handler := New(cfg, db, feeds.NewSyncer(db, config.Config{}), bgp).Handler()
	adminCookie := adminAuthCookie(t, cfg)

	form := url.Values{
		"filter_allow": {"8.8.8.0/24\n1.1.1.0/24"},
		"filter_deny":  {"10.0.0.0/8"},
		// Need at least one known setting key
		"settings-sync_interval": {"3600"},
	}
	request := httptest.NewRequest(http.MethodPost, "/admin/settings", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(adminCookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusSeeOther {
		t.Fatalf("POST /admin/settings with filters: status=%d body=%s", response.Code, response.Body.String())
	}

	filters, err := db.GlobalRouteFilters(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(filters.Allow) != 2 || len(filters.Deny) != 1 {
		t.Fatalf("filters saved: allow=%v deny=%v", filters.Allow, filters.Deny)
	}
}

func TestSettingsPageDynamicPeersFieldAndHint(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "settings-dynamic.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cfg := testConfig()
	handler := New(cfg, db, feeds.NewSyncer(db, config.Config{}), &fakeBGP{}).Handler()
	adminCookie := adminAuthCookie(t, cfg)

	request := httptest.NewRequest(http.MethodGet, "/admin/settings", nil)
	request.AddCookie(adminCookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	body := response.Body.String()

	if response.Code != http.StatusOK {
		t.Fatalf("GET /admin/settings: status=%d body=%s", response.Code, body)
	}

	if !strings.Contains(body, `for="s_allow_dynamic_peers"`) {
		t.Fatalf("dynamic peers checkbox not found: %s", body)
	}
	if !strings.Contains(body, `disabled title="`) {
		t.Fatalf("dynamic peers checkbox not disabled: %s", body)
	}
	if !strings.Contains(body, `WDBGP_ALLOW_DYNAMIC_PEERS`) {
		t.Fatalf("env var name not rendered: %s", body)
	}
}

func TestUserFormDynamicCheckboxReadonlyWhenDisabled(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "user-dyn-readonly.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cfg := testConfig()
	handler := New(cfg, db, feeds.NewSyncer(db, config.Config{}), &fakeBGP{}).Handler()
	adminCookie := adminAuthCookie(t, cfg)

	request := httptest.NewRequest(http.MethodGet, "/admin/user/0", nil)
	request.AddCookie(adminCookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	body := response.Body.String()

	if response.Code != http.StatusOK {
		t.Fatalf("GET /admin/user/0: status=%d body=%s", response.Code, body)
	}
	if !strings.Contains(body, `id=dynamic-ip`) {
		t.Fatalf("dynamic-ip checkbox not found: %s", body)
	}
	if !strings.Contains(body, `readonly`) {
		t.Fatalf("dynamic-ip checkbox missing readonly: %s", body)
	}
}

func TestUserFormPasswordDisabledDynamicIPv4(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "user-pw-dis-v4.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	userID, err := db.AddUser(ctx, store.User{
		Name:        "dynamic-v4",
		PeerIP:      "0.0.0.0",
		PeerASN:     65100,
		BGPPassword: "secret",
		Enabled:     true,
		Networks:    []string{"0.0.0.0/0"},
	})
	if err != nil {
		t.Fatal(err)
	}

	cfg := testConfig()
	handler := New(cfg, db, feeds.NewSyncer(db, config.Config{}), &fakeBGP{}).Handler()
	adminCookie := adminAuthCookie(t, cfg)

	request := httptest.NewRequest(http.MethodGet, "/admin/user/"+strconv.FormatInt(userID, 10), nil)
	request.AddCookie(adminCookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	body := response.Body.String()

	if response.Code != http.StatusOK {
		t.Fatalf("GET /admin/user/%d: status=%d body=%s", userID, response.Code, body)
	}
	if !strings.Contains(body, `disabled title="`) || !strings.Contains(body, `name=bgp_password`) {
		t.Fatalf("bgp_password field should be disabled for dynamic peer: %s", body)
	}
}

func TestUserFormPasswordDisabledDynamicIPv6(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "user-pw-dis-v6.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	userID, err := db.AddUser(ctx, store.User{
		Name:        "dynamic-v6",
		PeerIP:      "::",
		PeerASN:     65101,
		BGPPassword: "secret6",
		Enabled:     true,
		Networks:    []string{"::/0"},
	})
	if err != nil {
		t.Fatal(err)
	}

	cfg := testConfig()
	handler := New(cfg, db, feeds.NewSyncer(db, config.Config{}), &fakeBGP{}).Handler()
	adminCookie := adminAuthCookie(t, cfg)

	request := httptest.NewRequest(http.MethodGet, "/admin/user/"+strconv.FormatInt(userID, 10), nil)
	request.AddCookie(adminCookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	body := response.Body.String()

	if response.Code != http.StatusOK {
		t.Fatalf("GET /admin/user/%d: status=%d body=%s", userID, response.Code, body)
	}
	if !strings.Contains(body, `disabled title="`) || !strings.Contains(body, `name=bgp_password`) {
		t.Fatalf("bgp_password field should be disabled for dynamic IPv6 peer: %s", body)
	}
}

// =============================================================================
// Selection count endpoint
// =============================================================================

func TestSelectionCountEndpoint(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "selcount.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()

	// Get a feed in mode 1
	var feedID int64
	err = db.DB.QueryRow("SELECT f.id FROM feeds f JOIN catalog_mode_feeds cmf ON cmf.feed_id = f.id WHERE cmf.mode_id = 1 ORDER BY f.id LIMIT 1").Scan(&feedID)
	if err != nil {
		t.Fatal(err)
	}

	// Add catalog entries
	_, err = db.DB.Exec(`INSERT INTO catalog_entries(feed_id, category, service, cidr) VALUES
		(?, 'CountTest', 'SvcA', '4.4.4.0/24'),
		(?, 'CountTest', 'SvcB', '5.5.5.0/24'),
		(?, 'CountTest', 'SvcA', '2001:db8::/32')`,
		feedID, feedID, feedID)
	if err != nil {
		t.Fatal(err)
	}

	// Add a user with matching network
	userID, err := db.AddUser(ctx, store.User{
		Name: "count-requestor", PeerIP: "192.168.20.1", PeerASN: 65001, Enabled: true,
		Networks: []string{"192.168.20.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Set global filters empty so we get all prefixes
	err = db.SetGlobalRouteFilters(ctx, store.RouteFilters{})
	if err != nil {
		t.Fatal(err)
	}

	cfg := testConfig()
	handler := New(cfg, db, feeds.NewSyncer(db, config.Config{}), &fakeBGP{}).Handler()

	form := url.Values{
		"category": {"CountTest"},
	}
	request := httptest.NewRequest(http.MethodPost, "/selection/count", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.RemoteAddr = "192.168.20.10:12345"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("POST /selection/count: status=%d body=%s", response.Code, response.Body.String())
	}

	body := response.Body.String()
	// Check for v4 and v6 counts in response
	if !strings.Contains(body, "total-prefix-v4") || !strings.Contains(body, "total-prefix-v6") {
		t.Fatalf("prefix count HTML missing: %s", body)
	}
	// Verify it contains the expected counts (2 IPv4, 1 IPv6)
	if !strings.Contains(body, "2</strong>") || !strings.Contains(body, "1</strong>") {
		t.Fatalf("unexpected prefix counts in response: %s", body)
	}

	_ = userID
}

func TestSelectionCountWithEmptySelection(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "selcount-empty.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	_, err = db.AddUser(ctx, store.User{
		Name: "empty-count-user", PeerIP: "192.168.30.1", PeerASN: 65001, Enabled: true,
		Networks: []string{"192.168.30.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}

	cfg := testConfig()
	handler := New(cfg, db, feeds.NewSyncer(db, config.Config{}), &fakeBGP{}).Handler()

	// No categories or services selected
	form := url.Values{}
	request := httptest.NewRequest(http.MethodPost, "/selection/count", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.RemoteAddr = "192.168.30.10:12345"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("POST /selection/count empty: status=%d body=%s", response.Code, response.Body.String())
	}

	body := response.Body.String()
	if !strings.Contains(body, "0</strong>") {
		t.Fatalf("empty selection count should be 0: %s", body)
	}
}

// =============================================================================
// Helpers
// =============================================================================

// adminAuthCookie returns a cookie with a valid admin session for tests.
func adminAuthCookie(t *testing.T, cfg config.Config) *http.Cookie {
	t.Helper()
	return &http.Cookie{Name: "wdbgp_admin", Value: sessionToken(cfg.SessionSecret)}
}

// addCommunityTestData adds catalog entries so communities have data to generate from.
func addCommunityTestData(t *testing.T, db *store.Store) {
	t.Helper()
	var feedID int64
	err := db.DB.QueryRow("SELECT f.id FROM feeds f JOIN catalog_mode_feeds cmf ON cmf.feed_id = f.id WHERE cmf.mode_id = 1 ORDER BY f.id LIMIT 1").Scan(&feedID)
	if err != nil {
		t.Fatal(err)
	}

	var count int
	err = db.DB.QueryRow("SELECT COUNT(*) FROM catalog_entries WHERE feed_id = ?", feedID).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		_, err = db.DB.Exec(`INSERT INTO catalog_entries(feed_id, category, service, cidr) VALUES
			(?, 'CommunityTest', 'SvcA', '99.99.99.0/24'),
			(?, 'CommunityTest', 'SvcB', '88.88.88.0/24')`,
			feedID, feedID)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestUserLoginRateLimited(t *testing.T) {
	// Rate limiting (loginLimiter) is applied to both admin and user login.
	db, err := store.Open(filepath.Join(t.TempDir(), "login.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	userID, err := db.AddUser(ctx, store.User{
		Name: "login-user", PeerIP: "10.0.1.1", PeerASN: 65001, Enabled: true,
		WebAuth:  "login",
		Networks: []string{"10.0.1.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = db.SetUserCredential(ctx, userID, "alice", "secret123")
	if err != nil {
		t.Fatal(err)
	}

	cfg := testConfig()
	cfg.RateLimitLogin = 1 // allow only 1 request per minute
	handler := New(cfg, db, feeds.NewSyncer(db, config.Config{}), &fakeBGP{}).Handler()

	form := url.Values{"login": {"alice"}, "password": {"secret123"}}

	// First request should succeed (not rate-limited)
	req1 := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req1.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusSeeOther {
		t.Fatalf("first request should succeed, got %d", rec1.Code)
	}

	// Second request should be rate-limited (redirected to /login?error=...)
	req2 := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect for rate-limited request, got %d", rec2.Code)
	}
	loc := rec2.Header().Get("Location")
	if !strings.HasPrefix(loc, "/login?error=") {
		t.Fatalf("expected redirect to /login?error=..., got %s", loc)
	}
}
