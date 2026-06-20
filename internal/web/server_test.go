package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
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

func testConfig() config.Config {
	return config.Config{
		AdminPassword:     "admin",
		SessionSecret:     "test-secret",
		AdminCookieSecure: "true",
		DefaultLanguage:   "ru",
		RateLimitLogin:    0,  // Disable rate limiting in tests
		RateLimitAdmin:    0,  // Disable rate limiting in tests
		SessionMaxAge:     28800, // 8 hours
		SecurityHeaders:   false, // Disable security headers in tests to avoid CSP issues
		StatusAllowed:    []string{"0.0.0.0/0"}, // Allow all IPs for tests
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
	return map[string]string{"172.16.0.2": "ESTABLISHED"}, nil
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

func TestUserSelectionAndAdminPages(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"), config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	userID, err := db.AddUser(context.Background(), store.User{
		Name: "client", PeerIP: "172.16.0.2", PeerASN: 65001, Enabled: true,
		Networks: []string{"192.168.20.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB.Exec(`INSERT INTO catalog_entries(feed_id, category, service, cidr)
		VALUES (1, 'Messengers', 'Telegram', '149.154.160.0/20')`); err != nil {
		t.Fatal(err)
	}
	bgp := &fakeBGP{}
	cfg := testConfig()
	handler := New(cfg, db, feeds.NewSyncer(db, config.Config{}), bgp).Handler()

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "192.168.20.15:12345"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Telegram") {
		t.Fatalf("user page: status=%d body=%s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Accept-Language", "en")
	request.RemoteAddr = "192.168.20.15:12345"
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), "Service selection") ||
		!strings.Contains(response.Body.String(), `<html lang="en">`) {
		t.Fatalf("English user page: status=%d body=%s", response.Code, response.Body.String())
	}

	form := url.Values{"service": {serviceValue("Messengers", "Telegram")}}
	request = httptest.NewRequest(http.MethodPost, "/selection", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.RemoteAddr = "192.168.20.15:12345"
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || bgp.reconciles != 1 {
		t.Fatalf("save selection: status=%d reconciles=%d", response.Code, bgp.reconciles)
	}

	login := url.Values{"password": {"admin"}}
	request = httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(login.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	result := response.Result()
	cookies := result.Cookies()
	if response.Code != http.StatusSeeOther || len(cookies) != 1 {
		t.Fatalf("login: status=%d cookies=%d", response.Code, len(cookies))
	}
	if !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("admin cookie security attributes: %#v", cookies[0])
	}

	request = httptest.NewRequest(http.MethodGet, "/admin/user/"+strconv.FormatInt(userID, 10), nil)
	request.AddCookie(cookies[0])
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Параметры пользователя") {
		t.Fatalf("admin user page: status=%d body=%s", response.Code, response.Body.String())
	}

	filterForm := url.Values{
		"filter_mode": {"override"},
		"filter_deny": {"1.1.1.1/32"},
	}
	request = httptest.NewRequest(http.MethodPost, "/filters", strings.NewReader(filterForm.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.RemoteAddr = "192.168.20.15:12345"
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("locked filter update status = %d, want 403", response.Code)
	}

	if _, err := db.DB.Exec("UPDATE users SET filter_editable = 1 WHERE id = ?", userID); err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodPost, "/filters", strings.NewReader(filterForm.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.RemoteAddr = "192.168.20.15:12345"
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || bgp.reconciles != 2 {
		t.Fatalf("filter update: status=%d reconciles=%d", response.Code, bgp.reconciles)
	}
	user, err := db.User(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	filters, err := db.UserRouteFilters(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	if user.FilterMode != store.FilterModeOverride || !user.FilterOverride ||
		len(filters.Deny) != 1 || filters.Deny[0] != "1.1.1.1/32" {
		t.Fatalf("saved user filters: user=%#v filters=%#v", user, filters)
	}
	request = httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "192.168.20.15:12345"
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Фильтрация маршрутов") {
		t.Fatalf("user filter page: status=%d body=%s", response.Code, response.Body.String())
	}

	globalForm := url.Values{"filter_deny": {"8.8.8.8/32"}}
	request = httptest.NewRequest(http.MethodPost, "/admin/filters", strings.NewReader(globalForm.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(cookies[0])
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || bgp.reconciles != 3 {
		t.Fatalf("global filter update: status=%d reconciles=%d", response.Code, bgp.reconciles)
	}
	// /admin now redirects to /admin/dashboard
	request = httptest.NewRequest(http.MethodGet, "/admin", nil)
	request.AddCookie(cookies[0])
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("admin redirect: status=%d body=%s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/admin/dashboard", nil)
	request.Header.Set("Accept-Language", "en")
	request.AddCookie(cookies[0])
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), `<html lang="en">`) {
		t.Fatalf("English dashboard page: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestUserCatalogModeChangeRequiresPermission(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"), config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	modes, err := db.CatalogModes(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	ipranges := modes[1]
	ipranges.Enabled = true
	if err := db.UpdateCatalogMode(ctx, ipranges); err != nil {
		t.Fatal(err)
	}
	if err := db.AddFeedForMode(
		ctx, "precise", "https://example.test/precise", ipranges.ID, true, 0); err != nil {
		t.Fatal(err)
	}
	var feedID int64
	if err := db.DB.QueryRow("SELECT id FROM feeds WHERE name = 'precise'").Scan(&feedID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB.Exec(`INSERT INTO catalog_entries(feed_id, category, service, cidr)
		VALUES (?, 'Precise', 'Resolver', '8.8.8.0/24')`, feedID); err != nil {
		t.Fatal(err)
	}
	userID, err := db.AddUser(ctx, store.User{
		Name: "client", PeerIP: "172.16.0.2", PeerASN: 65001, Enabled: true,
		CatalogModeID: 1, Networks: []string{"192.168.20.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	bgp := &fakeBGP{}
	handler := New(testConfig(), db, feeds.NewSyncer(db, config.Config{}), bgp).Handler()
	form := url.Values{
		"catalog_mode_id": {strconv.FormatInt(ipranges.ID, 10)},
		"service":         {serviceValue("Precise", "Resolver")},
	}
	save := func() *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/selection", strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.RemoteAddr = "192.168.20.15:12345"
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	if response := save(); response.Code != http.StatusForbidden {
		t.Fatalf("managed mode change status=%d body=%s", response.Code, response.Body.String())
	}
	if _, err := db.DB.Exec(
		"UPDATE users SET catalog_mode_editable = 1 WHERE id = ?", userID); err != nil {
		t.Fatal(err)
	}
	if response := save(); response.Code != http.StatusSeeOther {
		t.Fatalf("editable mode change status=%d body=%s", response.Code, response.Body.String())
	}
	user, err := db.User(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	_, services, err := db.UserModeSelection(ctx, userID, ipranges.ID)
	if err != nil {
		t.Fatal(err)
	}
	if user.CatalogModeID != ipranges.ID ||
		!services[store.ServiceKey{Category: "Precise", Service: "Resolver"}] ||
		bgp.reconciles != 1 {
		t.Fatalf("user=%#v services=%#v reconciles=%d", user, services, bgp.reconciles)
	}
}

func TestLockedUserCanChangeEditableCatalogMode(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"), config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	modes, err := db.CatalogModes(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	ipranges := modes[1]
	ipranges.Enabled = true
	if err := db.UpdateCatalogMode(ctx, ipranges); err != nil {
		t.Fatal(err)
	}
	userID, err := db.AddUser(ctx, store.User{
		Name: "client", PeerIP: "172.16.0.2", PeerASN: 65001, Enabled: true,
		SelectionLocked: true, CatalogEditable: true, CatalogModeID: 1,
		Networks: []string{"192.168.20.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Transaction(ctx, func(tx *sql.Tx) error {
		return store.SetUserModeSelection(
			ctx, tx, userID, 1, []string{"Messengers"}, nil)
	}); err != nil {
		t.Fatal(err)
	}
	bgp := &fakeBGP{}
	handler := New(testConfig(), db, feeds.NewSyncer(db, config.Config{}), bgp).Handler()
	form := url.Values{
		"catalog_mode_id": {strconv.FormatInt(ipranges.ID, 10)},
	}
	request := httptest.NewRequest(
		http.MethodPost, "/selection", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.RemoteAddr = "192.168.20.15:12345"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("mode change status=%d body=%s", response.Code, response.Body.String())
	}
	user, err := db.User(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	categories, _, err := db.UserModeSelection(ctx, userID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if user.CatalogModeID != ipranges.ID || !categories["Messengers"] ||
		bgp.reconciles != 1 {
		t.Fatalf("user=%#v categories=%#v reconciles=%d", user, categories, bgp.reconciles)
	}
}

func TestCategorySelectionDisablesContainedServices(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"), config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	userID, err := db.AddUser(context.Background(), store.User{
		Name: "client", PeerIP: "172.16.0.2", PeerASN: 65001, Enabled: true,
		Networks: []string{"192.168.20.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB.Exec(`INSERT INTO catalog_entries(feed_id, category, service, cidr) VALUES
		(1, 'Messengers', 'Telegram', '149.154.160.0/20'),
		(1, 'Messengers', 'Signal', '76.223.92.0/24'),
		(1, 'AI', 'Copilot', '140.82.112.0/20')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Transaction(context.Background(), func(tx *sql.Tx) error {
		return store.SetUserSelection(context.Background(), tx, userID, []string{"Messengers"},
			[]store.ServiceKey{{Category: "AI", Service: "Copilot"}})
	}); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "192.168.20.15:12345"
	response := httptest.NewRecorder()
	cfg := testConfig()
	cfg.DefaultLanguage = "ru"
	New(cfg, db, feeds.NewSyncer(db, config.Config{}), &fakeBGP{}).
		Handler().ServeHTTP(response, request)
	body := response.Body.String()
	if response.Code != http.StatusOK {
		t.Fatalf("user page status=%d body=%s", response.Code, body)
	}
	for _, want := range []string{
		`<span id=selected-category-count>1</span>`,
		`>категория</span>`,
		`<span id=selected-covered-service-count>2</span>`,
		`>сервиса</span> в них`,
		`<span id=selected-service-count>1</span>`,
		`>отдельный сервис</span>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("selection details %q not rendered: %s", want, body)
		}
	}
	if !strings.Contains(body, `name=service value="Messengers:Telegram"`) || !strings.Contains(body, `disabled`) {
		t.Fatalf("contained selected service is not disabled: %s", body)
	}
	if !strings.Contains(body, `name=service value="Messengers:Signal"`) || !strings.Contains(body, `disabled`) {
		t.Fatalf("contained unselected service is not disabled: %s", body)
	}
}

func TestAdminCanManageFeeds(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"), config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	bgp := &fakeBGP{}
	cfg := testConfig()
	handler := New(cfg, db, feeds.NewSyncer(db, config.Config{}), bgp).Handler()
	adminCookie := &http.Cookie{Name: "wdbgp_admin", Value: sessionToken(cfg.SessionSecret)}

	addForm := url.Values{
		"name":    {"custom"},
		"url":     {"https://example.test/feed.json"},
		"enabled": {"on"},
	}
	request := httptest.NewRequest(http.MethodPost, "/admin/feed", strings.NewReader(addForm.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(adminCookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("add feed: status=%d body=%s", response.Code, response.Body.String())
	}

	feedList, err := db.Feeds(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	feed := feedList[len(feedList)-1]
	if feed.Name != "custom" || !feed.Enabled {
		t.Fatalf("added feed = %#v", feed)
	}
	if _, err := db.DB.Exec(`INSERT INTO catalog_entries(feed_id, category, service, cidr)
		VALUES (?, 'Custom', 'Example', '203.0.113.0/24')`, feed.ID); err != nil {
		t.Fatal(err)
	}

	updateForm := url.Values{
		"name": {"custom"},
		"url":  {feed.URL},
	}
	request = httptest.NewRequest(http.MethodPost,
		"/admin/feed/"+strconv.FormatInt(feed.ID, 10), strings.NewReader(updateForm.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(adminCookie)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || bgp.reconciles != 1 {
		t.Fatalf("update feed: status=%d reconciles=%d body=%s",
			response.Code, bgp.reconciles, response.Body.String())
	}
	feedList, err = db.Feeds(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	feed = feedList[len(feedList)-1]
	if feed.Name != "custom" || feed.Enabled {
		t.Fatalf("updated feed = %#v", feed)
	}
	var entries int
	if err := db.DB.QueryRow("SELECT COUNT(*) FROM catalog_entries WHERE feed_id = ?", feed.ID).
		Scan(&entries); err != nil {
		t.Fatal(err)
	}
	if entries != 1 {
		t.Fatalf("disabled feed snapshot entries = %d, want 1", entries)
	}

	request = httptest.NewRequest(http.MethodGet, "/admin", nil)
	request.AddCookie(adminCookie)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("admin redirect after feed update: status=%d body=%s", response.Code, response.Body.String())
	}

	// Verify dashboard loads after redirect
	request = httptest.NewRequest(http.MethodGet, "/admin/dashboard", nil)
	request.AddCookie(adminCookie)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("dashboard not rendered: status=%d body=%s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost,
		"/admin/feed/"+strconv.FormatInt(feed.ID, 10)+"/delete", nil)
	request.AddCookie(adminCookie)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || bgp.reconciles != 2 {
		t.Fatalf("delete feed: status=%d reconciles=%d body=%s",
			response.Code, bgp.reconciles, response.Body.String())
	}
	feedList, err = db.Feeds(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	for _, existing := range feedList {
		if existing.ID == feed.ID {
			t.Fatalf("deleted feed remains: %#v", existing)
		}
	}
}

func TestAdminCanEditFeedAdapter(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"), config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cfg := testConfig()
	handler := New(cfg, db, feeds.NewSyncer(db, config.Config{}), &fakeBGP{}).Handler()
	adminCookie := &http.Cookie{Name: "wdbgp_admin", Value: sessionToken(cfg.SessionSecret)}
	adapter, err := db.FeedAdapter(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/admin/adapter/1", nil)
	request.AddCookie(adminCookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), "api.httpGet(feed.url)") ||
		!strings.Contains(response.Body.String(), "/admin/adapter/1/reset") {
		t.Fatalf("adapter editor: status=%d body=%s",
			response.Code, response.Body.String())
	}
	form := url.Values{
		"name":          {"Edited canonical"},
		"allowed_hosts": {"cdn.example.test"},
		"source":        {`function sync(feed, api) { return []; }`},
	}
	request = httptest.NewRequest(http.MethodPost, "/admin/adapter/1",
		strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(adminCookie)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("edit adapter: status=%d body=%s", response.Code, response.Body.String())
	}
	updated, err := db.FeedAdapter(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "Edited canonical" ||
		updated.AllowedHosts != "cdn.example.test" ||
		updated.Revision != adapter.Revision+1 {
		t.Fatalf("updated adapter = %#v", updated)
	}

	form.Set("source", `function sync(`)
	request = httptest.NewRequest(http.MethodPost, "/admin/adapter/1",
		strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(adminCookie)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest ||
		!strings.Contains(response.Body.String(), "feed-adapter.js") ||
		!strings.Contains(response.Body.String(), "function sync(") {
		t.Fatalf("invalid adapter: status=%d body=%s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/admin/adapter/1/reset", nil)
	request.AddCookie(adminCookie)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("reset adapter: status=%d body=%s", response.Code, response.Body.String())
	}
	reset, err := db.FeedAdapter(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if reset.Name != adapter.Name ||
		reset.Source != adapter.Source ||
		reset.AllowedHosts != adapter.AllowedHosts ||
		reset.Revision != adapter.Revision+2 {
		t.Fatalf("reset adapter = %#v, original = %#v", reset, adapter)
	}
}

func TestAdminCanTestUnsavedFeedAdapterWithoutWritingCatalog(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"), config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.DB.Exec("UPDATE feeds SET enabled = 0"); err != nil {
		t.Fatal(err)
	}
	if err := db.AddFeed(context.Background(), "preview",
		"https://example.test/feed", false, 0); err != nil {
		t.Fatal(err)
	}
	feedList, err := db.Feeds(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	feed := feedList[len(feedList)-1]
	adapter, err := db.FeedAdapter(context.Background(), feed.AdapterID)
	if err != nil {
		t.Fatal(err)
	}
	syncer := feeds.NewSyncer(db, config.Config{})
	syncer.Client = &http.Client{Transport: testRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != feed.URL {
			t.Fatalf("request URL = %q, want %q", request.URL, feed.URL)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`["149.154.167.99/20"]`)),
			Header:     make(http.Header),
		}, nil
	})}
	cfg := testConfig()
	handler := New(cfg, db, syncer, &fakeBGP{}).Handler()
	adminCookie := &http.Cookie{Name: "wdbgp_admin", Value: sessionToken(cfg.SessionSecret)}
	form := url.Values{
		"name":          {"Unsaved name"},
		"allowed_hosts": {adapter.AllowedHosts},
		"feed_id":       {strconv.FormatInt(feed.ID, 10)},
		"source": {`function sync(feed, api) {
            return [{
                category: "Messengers",
                service: "Telegram",
                cidrs: JSON.parse(api.httpGet(feed.url))
            }];
        }`},
	}
	request := httptest.NewRequest(http.MethodPost,
		"/admin/adapter/"+strconv.FormatInt(adapter.ID, 10)+"/test",
		strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(adminCookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), "149.154.160.0/20") ||
		!strings.Contains(response.Body.String(), "Unsaved name") {
		t.Fatalf("test adapter: status=%d body=%s", response.Code, response.Body.String())
	}
	var entries int
	if err := db.DB.QueryRow(
		"SELECT COUNT(*) FROM catalog_entries WHERE feed_id = ?", feed.ID).
		Scan(&entries); err != nil {
		t.Fatal(err)
	}
	stored, err := db.FeedAdapter(context.Background(), adapter.ID)
	if err != nil {
		t.Fatal(err)
	}
	if entries != 0 || stored.Revision != adapter.Revision || stored.Name != adapter.Name {
		t.Fatalf("test changed state: entries=%d adapter=%#v", entries, stored)
	}

	form.Set("source", `function sync() {
        throw new Error("preview failed");
    }`)
	request = httptest.NewRequest(http.MethodPost,
		"/admin/adapter/"+strconv.FormatInt(adapter.ID, 10)+"/test",
		strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(adminCookie)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest ||
		!strings.Contains(response.Body.String(), "preview failed") ||
		!strings.Contains(response.Body.String(), "canonical-json.js:2") {
		t.Fatalf("runtime adapter error: status=%d body=%s",
			response.Code, response.Body.String())
	}
}

func TestSelectionSavesPreserveDisabledFeedSelections(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"), config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.DB.Exec("UPDATE feeds SET enabled = 0"); err != nil {
		t.Fatal(err)
	}
	if err := db.AddFeed(context.Background(), "enabled", "https://example.test/enabled", true, 0); err != nil {
		t.Fatal(err)
	}
	if err := db.AddFeed(context.Background(), "disabled", "https://example.test/disabled", false, 0); err != nil {
		t.Fatal(err)
	}
	feedList, err := db.Feeds(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	var enabledID, disabledID int64
	for _, feed := range feedList {
		switch feed.Name {
		case "enabled":
			enabledID = feed.ID
		case "disabled":
			disabledID = feed.ID
		}
	}
	if _, err := db.DB.Exec(`INSERT INTO catalog_entries(feed_id, category, service, cidr) VALUES
		(?, 'Visible', 'Keep', '8.8.8.0/24'),
		(?, 'Visible', 'Remove', '8.8.4.0/24'),
		(?, 'HiddenCategory', 'Any', '1.1.1.0/24'),
		(?, 'HiddenServices', 'Hidden', '1.0.0.0/24')`,
		enabledID, enabledID, disabledID, disabledID); err != nil {
		t.Fatal(err)
	}

	cfg := testConfig()
	bgp := &fakeBGP{}
	handler := New(cfg, db, feeds.NewSyncer(db, config.Config{}), bgp).Handler()
	adminCookie := &http.Cookie{Name: "wdbgp_admin", Value: sessionToken(cfg.SessionSecret)}

	tests := []struct {
		name       string
		network    string
		admin      bool
		wantStatus int
	}{
		{name: "user save", network: "192.168.20.0/24", wantStatus: http.StatusSeeOther},
		{name: "admin save", network: "192.168.30.0/24", admin: true, wantStatus: http.StatusSeeOther},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			userID, err := db.AddUser(context.Background(), store.User{
				Name:    "client-" + strconv.Itoa(index),
				PeerIP:  "172.16.0." + strconv.Itoa(index+2),
				PeerASN: 65001 + uint32(index), Enabled: true,
				Networks: []string{test.network},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Transaction(context.Background(), func(tx *sql.Tx) error {
				return store.SetUserSelection(context.Background(), tx, userID,
					[]string{"HiddenCategory"},
					[]store.ServiceKey{
						{Category: "HiddenServices", Service: "Hidden"},
						{Category: "Visible", Service: "Remove"},
					})
			}); err != nil {
				t.Fatal(err)
			}

			form := url.Values{"service": {serviceValue("Visible", "Keep")}}
			target := "/selection"
			if test.admin {
				target = "/admin/user/" + strconv.FormatInt(userID, 10)
				form.Set("action", "selection")
			}
			request := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.RemoteAddr = strings.TrimSuffix(test.network, "0/24") + "15:12345"
			if test.admin {
				request.AddCookie(adminCookie)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}

			categories, services, err := db.UserSelection(context.Background(), userID)
			if err != nil {
				t.Fatal(err)
			}
			if len(categories) != 1 || !categories["HiddenCategory"] {
				t.Fatalf("categories = %#v", categories)
			}
			if len(services) != 2 ||
				!services[store.ServiceKey{Category: "HiddenServices", Service: "Hidden"}] ||
				!services[store.ServiceKey{Category: "Visible", Service: "Keep"}] {
				t.Fatalf("services = %#v", services)
			}
		})
	}
	if bgp.reconciles != len(tests) {
		t.Fatalf("BGP reconciles = %d, want %d", bgp.reconciles, len(tests))
	}
}

func TestLocalizedPages(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"), config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	tests := []struct {
		name            string
		defaultLanguage string
		target          string
		accept          string
		cookie          *http.Cookie
		wantLang        string
		wantText        string
		wantCookie      string
	}{
		{
			name:     "English default",
			target:   "/",
			wantLang: "en",
			wantText: "Access denied",
		},
		{
			name:            "Configured Russian default",
			defaultLanguage: "ru",
			target:          "/",
			wantLang:        "ru",
			wantText:        "Нет доступа",
		},
		{
			name:            "Unsupported browser language uses configured default",
			defaultLanguage: "ru",
			target:          "/",
			accept:          "de",
			wantLang:        "ru",
			wantText:        "Нет доступа",
		},
		{
			name:     "English browser preference",
			target:   "/",
			accept:   "en-US,en;q=0.9,ru;q=0.8",
			wantLang: "en",
			wantText: "Access denied",
		},
		{
			name:       "Query overrides browser and persists",
			target:     "/?lang=en",
			accept:     "ru",
			wantLang:   "en",
			wantText:   "Access denied",
			wantCookie: "en",
		},
		{
			name:     "Cookie overrides browser",
			target:   "/",
			accept:   "en",
			cookie:   &http.Cookie{Name: languageCookieName, Value: "ru"},
			wantLang: "ru",
			wantText: "Нет доступа",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := New(config.Config{DefaultLanguage: test.defaultLanguage},
				db, feeds.NewSyncer(db, config.Config{}), &fakeBGP{}).Handler()
			request := httptest.NewRequest(http.MethodGet, test.target, nil)
			request.RemoteAddr = "192.0.2.10:12345"
			request.Header.Set("Accept-Language", test.accept)
			if test.cookie != nil {
				request.AddCookie(test.cookie)
			}
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			body := response.Body.String()
			if response.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusForbidden, body)
			}
			if !strings.Contains(body, `<html lang="`+test.wantLang+`">`) {
				t.Fatalf("language %q not rendered: %s", test.wantLang, body)
			}
			if !strings.Contains(body, test.wantText) {
				t.Fatalf("text %q not rendered: %s", test.wantText, body)
			}
			if test.wantCookie != "" {
				cookies := response.Result().Cookies()
				if len(cookies) != 1 || cookies[0].Name != languageCookieName ||
					cookies[0].Value != test.wantCookie || cookies[0].MaxAge <= 0 {
					t.Fatalf("language cookie = %#v", cookies)
				}
			}
		})
	}
}

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

func TestSelectionFromValuesDropsServicesInsideSelectedCategory(t *testing.T) {
	values := url.Values{
		"category": {"Messengers"},
		"service": {
			serviceValue("Messengers", "Telegram"),
			serviceValue("AI", "Copilot"),
		},
	}
	categories, services, err := selectionFromValues(values)
	if err != nil {
		t.Fatal(err)
	}
	if len(categories) != 1 || categories[0] != "Messengers" {
		t.Fatalf("categories = %#v", categories)
	}
	if len(services) != 1 || services[0] != (store.ServiceKey{Category: "AI", Service: "Copilot"}) {
		t.Fatalf("services = %#v", services)
	}
}

func TestAdminCookieSecureAutoAllowsPlainHTTP(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"), config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cfg := config.Config{AdminPassword: "admin", SessionSecret: "secret", AdminCookieSecure: "auto"}
	handler := New(cfg, db, feeds.NewSyncer(db, config.Config{}), &fakeBGP{}).Handler()

	login := url.Values{"password": {"admin"}}
	request := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(login.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	cookies := response.Result().Cookies()
	if response.Code != http.StatusSeeOther || len(cookies) != 1 {
		t.Fatalf("login: status=%d cookies=%d", response.Code, len(cookies))
	}
	if cookies[0].Secure {
		t.Fatalf("plain HTTP admin cookie must be usable without Secure: %#v", cookies[0])
	}
	if !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("admin cookie security attributes: %#v", cookies[0])
	}

	request = httptest.NewRequest(http.MethodGet, "/admin", nil)
	request.AddCookie(cookies[0])
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("admin redirect after HTTP login: status=%d body=%s", response.Code, response.Body.String())
	}

	// Verify dashboard loads after redirect
	request = httptest.NewRequest(http.MethodGet, "/admin/dashboard", nil)
	request.AddCookie(cookies[0])
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("dashboard page after HTTP login: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestAdminCookieSecureAutoHonorsTrustedForwardedProto(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"), config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cfg := config.Config{
		AdminPassword: "admin", SessionSecret: "secret",
		AdminCookieSecure: "auto", TrustProxyHeader: true,
	}
	handler := New(cfg, db, feeds.NewSyncer(db, config.Config{}), &fakeBGP{}).Handler()

	login := url.Values{"password": {"admin"}}
	request := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(login.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("X-Forwarded-Proto", "https")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	cookies := response.Result().Cookies()
	if response.Code != http.StatusSeeOther || len(cookies) != 1 {
		t.Fatalf("login: status=%d cookies=%d", response.Code, len(cookies))
	}
	if !cookies[0].Secure {
		t.Fatalf("HTTPS admin cookie must be Secure: %#v", cookies[0])
	}
}

type testRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn testRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

// =============================================================================
// Peer uniqueness tests (Part A)
// =============================================================================

func TestAddUserRejectsSameIPAndSameASN(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"), config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
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
	adminCookie := &http.Cookie{Name: "wdbgp_admin", Value: sessionToken(cfg.SessionSecret)}

	form := url.Values{
		"name":     {"second"},
		"peer_ip":  {"10.0.1.1"},
		"peer_asn": {"65100"},
		"networks": {"192.168.2.0/24"},
		"enabled":  {"on"},
	}
	request := httptest.NewRequest(http.MethodPost, "/admin/user", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
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
	defer db.Close()

	cfg := testConfig()
	handler := New(cfg, db, feeds.NewSyncer(db, config.Config{}), &fakeBGP{}).Handler()
	adminCookie := &http.Cookie{Name: "wdbgp_admin", Value: sessionToken(cfg.SessionSecret)}

	form := url.Values{
		"name":     {"unique-peer"},
		"peer_ip":  {"10.0.2.1"},
		"peer_asn": {"65100"},
		"networks": {"192.168.3.0/24"},
		"enabled":  {"on"},
	}
	request := httptest.NewRequest(http.MethodPost, "/admin/user", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(adminCookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect for unique IP without password, got %d body=%s",
			response.Code, response.Body.String())
	}
}

func TestAddUserRequiresPasswordForDynamicPeers(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"), config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cfg := testConfig()
	handler := New(cfg, db, feeds.NewSyncer(db, config.Config{}), &fakeBGP{}).Handler()
	adminCookie := &http.Cookie{Name: "wdbgp_admin", Value: sessionToken(cfg.SessionSecret)}

	// Without password — should reject
	form := url.Values{
		"name":     {"dynamic-no-pw"},
		"peer_ip":  {"0.0.0.0"},
		"peer_asn": {"65100"},
		"networks": {"0.0.0.0/0"},
		"enabled":  {"on"},
	}
	request := httptest.NewRequest(http.MethodPost, "/admin/user", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(adminCookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for dynamic peer without password, got %d body=%s",
			response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "dynamic") || !strings.Contains(response.Body.String(), "password") {
		t.Fatalf("response should mention dynamic peer requiring password: %s", response.Body.String())
	}

	// With password — should succeed
	form.Set("bgp_password", "secret123")
	request = httptest.NewRequest(http.MethodPost, "/admin/user", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(adminCookie)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect for dynamic peer with password, got %d body=%s",
			response.Code, response.Body.String())
	}
}

func TestAddUserRejectsDuplicateDynamicPeerASN(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"), config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
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
	handler := New(cfg, db, feeds.NewSyncer(db, config.Config{}), &fakeBGP{}).Handler()
	adminCookie := &http.Cookie{Name: "wdbgp_admin", Value: sessionToken(cfg.SessionSecret)}

	form := url.Values{
		"name":         {"second-dynamic"},
		"peer_ip":      {"0.0.0.0"},
		"peer_asn":     {"65100"},
		"bgp_password": {"secret2"},
		"networks":     {"0.0.0.0/0"},
		"enabled":      {"on"},
	}
	request := httptest.NewRequest(http.MethodPost, "/admin/user", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
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
	defer db.Close()
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
	adminCookie := &http.Cookie{Name: "wdbgp_admin", Value: sessionToken(cfg.SessionSecret)}

	form := url.Values{
		"name":     {"second-shared"},
		"peer_ip":  {"10.0.3.1"},
		"peer_asn": {"65102"},
		"networks": {"192.168.11.0/24"},
		"enabled":  {"on"},
	}
	request := httptest.NewRequest(http.MethodPost, "/admin/user", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
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
	defer db.Close()
	ctx := context.Background()
	_, err = db.AddUser(ctx, store.User{
		Name: "first-shared-pw", PeerIP: "10.0.4.1", PeerASN: 65101, Enabled: true,
		BGPPassword: "shared-secret",
		Networks: []string{"192.168.20.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}

	cfg := testConfig()
	handler := New(cfg, db, feeds.NewSyncer(db, config.Config{}), &fakeBGP{}).Handler()
	adminCookie := &http.Cookie{Name: "wdbgp_admin", Value: sessionToken(cfg.SessionSecret)}

	form := url.Values{
		"name":         {"second-shared-pw"},
		"peer_ip":      {"10.0.4.1"},
		"peer_asn":     {"65102"},
		"bgp_password": {"shared-secret"},
		"networks":     {"192.168.21.0/24"},
		"enabled":      {"on"},
	}
	request := httptest.NewRequest(http.MethodPost, "/admin/user", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(adminCookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect for shared IP with password, got %d body=%s",
			response.Code, response.Body.String())
	}
}

func TestSameIPv4RequiresMatchingPassword(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "same-ipv4-pw.sqlite3"), config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
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
	adminCookie := &http.Cookie{Name: "wdbgp_admin", Value: sessionToken(cfg.SessionSecret)}

	// Try to add second peer with different password "banana"
	form := url.Values{
		"name":         {"bob"},
		"peer_ip":      {"192.0.2.1"},
		"peer_asn":     {"65002"},
		"bgp_password": {"banana"},
		"networks":     {"192.168.101.0/24"},
		"enabled":      {"on"},
	}
	request := httptest.NewRequest(http.MethodPost, "/admin/user", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
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
	defer db.Close()
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
	adminCookie := &http.Cookie{Name: "wdbgp_admin", Value: sessionToken(cfg.SessionSecret)}

	// Try to add second peer with different password "banana"
	form := url.Values{
		"name":         {"bob6"},
		"peer_ip":      {"fd00::1"},
		"peer_asn":     {"65102"},
		"bgp_password": {"banana"},
		"networks":     {"fd00:babe::/48"},
		"enabled":      {"on"},
	}
	request := httptest.NewRequest(http.MethodPost, "/admin/user", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
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

func TestStatusEndpoint(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "status.sqlite3"), config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Create some test data
	_, err = db.AddUser(context.Background(), store.User{
		Name: "test-client", PeerIP: "172.16.0.3", PeerASN: 65002, Enabled: true,
		Networks: []string{"192.168.30.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Add a feed
	_, err = db.AddFeedForModeAdapter(
		context.Background(),
		"test-feed",
		"http://example.com/feed.json",
		1,
		1,
		true,
		0,
		"",
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

func TestDegradedModeShowsErrorPage(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"), config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

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
	defer db.Close()

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
	defer db.Close()

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
