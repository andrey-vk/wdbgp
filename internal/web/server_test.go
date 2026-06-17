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

func (f *fakeBGP) DeletePeer(context.Context, int64, string) error {
	f.deletes++
	return nil
}

func TestUserSelectionAndAdminPages(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"))
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
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"))
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
	if err := db.UpdateCatalogMode(ctx, ipranges.ID, ipranges.Name, true); err != nil {
		t.Fatal(err)
	}
	if err := db.AddFeedForMode(
		ctx, "precise", "https://example.test/precise", ipranges.ID, 0); err != nil {
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
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"))
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
	if err := db.UpdateCatalogMode(ctx, ipranges.ID, ipranges.Name, true); err != nil {
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
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"))
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
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	bgp := &fakeBGP{}
	cfg := testConfig()
	handler := New(cfg, db, feeds.NewSyncer(db, config.Config{}), bgp).Handler()
	adminCookie := &http.Cookie{Name: "wdbgp_admin", Value: sessionToken(cfg.SessionSecret)}

	addForm := url.Values{
		"name":     {"custom"},
		"url":      {"https://example.test/feed.json"},
		"enabled":  {"on"},
		"mode_ids": {"1"},
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
	if feed.Name != "custom" {
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
	if feed.Name != "custom" {
		t.Fatalf("updated feed = %#v", feed)
	}
	var entries int
	if err := db.DB.QueryRow("SELECT COUNT(*) FROM catalog_entries WHERE feed_id = ?", feed.ID).
		Scan(&entries); err != nil {
		t.Fatal(err)
	}
	if entries != 1 {
		t.Fatalf("feed snapshot entries after update = %d, want 1", entries)
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
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"))
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
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.AddFeed(context.Background(), "preview",
		"https://example.test/feed", 0); err != nil {
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
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.AddFeed(context.Background(), "enabled", "https://example.test/enabled", 0); err != nil {
		t.Fatal(err)
	}
	if err := db.AddFeed(context.Background(), "disabled", "https://example.test/disabled", 0); err != nil {
		t.Fatal(err)
	}
	// Remove "disabled" feed from default enabled mode
	allFeeds, _ := db.Feeds(context.Background(), false)
	for _, f := range allFeeds {
		if f.Name == "disabled" {
			if err := db.RemoveFeedFromMode(context.Background(), store.DefaultCatalogModeID, f.ID); err != nil {
				t.Fatal(err)
			}
			break
		}
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
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"))
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
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"))
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
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"))
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

func TestStatusEndpoint(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "status.sqlite3"))
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
	err = db.AddFeedForModeAdapter(
		context.Background(),
		"test-feed",
		"http://example.com/feed.json",
		1,
		1,
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

func TestModeEditPageShowsFeedsWithCorrectCheckboxes(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()

	// Create a custom mode
	if err := db.AddCatalogMode(ctx, "test-mode", true); err != nil {
		t.Fatal(err)
	}
	modes, err := db.CatalogModes(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	var modeID int64
	for _, m := range modes {
		if m.Name == "test-mode" {
			modeID = m.ID
			break
		}
	}
	if modeID == 0 {
		t.Fatal("mode not found")
	}

	// Create 3 feeds and add all to the mode
	feedNames := []string{"feed-a", "feed-b", "feed-c"}
	for _, name := range feedNames {
		if err := db.AddFeed(ctx, name, "https://example.test/"+name, 0); err != nil {
			t.Fatal(err)
		}
		var feedID int64
		if err := db.DB.QueryRow("SELECT id FROM feeds WHERE name = ?", name).Scan(&feedID); err != nil {
			t.Fatal(err)
		}
		if err := db.AddFeedToMode(ctx, modeID, feedID); err != nil {
			t.Fatal(err)
		}
	}

	// Fetch the mode edit page as admin
	cfg := testConfig()
	handler := New(cfg, db, feeds.NewSyncer(db, config.Config{}), &fakeBGP{}).Handler()
	adminCookie := &http.Cookie{Name: "wdbgp_admin", Value: sessionToken(cfg.SessionSecret)}

	req := httptest.NewRequest(http.MethodGet, "/admin/mode/"+strconv.FormatInt(modeID, 10), nil)
	req.AddCookie(adminCookie)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	body := w.Body.String()

	// All 3 feeds should appear with checked checkboxes
	for _, name := range feedNames {
		if !strings.Contains(body, name) {
			t.Errorf("feed %s: name not found in mode edit page", name)
			continue
		}
		// Find the feed's ID from the DB
		var feedID int64
		if err := db.DB.QueryRow("SELECT id FROM feeds WHERE name = ?", name).Scan(&feedID); err != nil {
			t.Fatal(err)
		}
		// Each feed row should contain checked checkbox with its ID
		want := `value="` + strconv.FormatInt(feedID, 10) + `" checked`
		if !strings.Contains(body, want) {
			t.Errorf("feed %s (id=%d): expected checked checkbox, not found\nbody: %s", name, feedID, body)
		}
	}

	// Verify the mode enabled toggle button is present.
	if !strings.Contains(body, "Disable") && !strings.Contains(body, "Enable") && !strings.Contains(body, "Включить") && !strings.Contains(body, "Отключить") {
		t.Errorf("mode enable/disable toggle button not found in page")
	}
}

func TestUpdateFeedAtomicWithModeAssignment(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()

	// Create a feed with known name and URL.
	if err := db.AddFeed(ctx, "original-name", "https://original.example/feed.json", 0); err != nil {
		t.Fatal(err)
	}
	// Get feed ID.
	var feedID int64
	if err := db.DB.QueryRow("SELECT id FROM feeds WHERE name = ?", "original-name").Scan(&feedID); err != nil {
		t.Fatal(err)
	}
	// Verify it has mode 1 assigned (default from AddFeed).
	var modeCount int
	if err := db.DB.QueryRow("SELECT COUNT(*) FROM catalog_mode_feeds WHERE feed_id = ?", feedID).Scan(&modeCount); err != nil {
		t.Fatal(err)
	}
	if modeCount != 1 {
		t.Fatalf("expected 1 mode assignment, got %d", modeCount)
	}

	bgp := &fakeBGP{}
	cfg := testConfig()
	handler := New(cfg, db, feeds.NewSyncer(db, config.Config{}), bgp).Handler()
	adminCookie := &http.Cookie{Name: "wdbgp_admin", Value: sessionToken(cfg.SessionSecret)}

	// Try to update feed with a non-existent mode ID (99999) to trigger FK violation.
	changedName := "changed-name"
	changedURL := "https://changed.example/feed.json"
	updateForm := url.Values{
		"name":     {changedName},
		"url":      {changedURL},
		"mode_ids": {"99999"},
	}
	req := httptest.NewRequest(http.MethodPost,
		"/admin/feed/"+strconv.FormatInt(feedID, 10),
		strings.NewReader(updateForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(adminCookie)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// The request should fail (internal error, not redirect).
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for FK violation, got %d: body=%s", w.Code, w.Body.String())
	}

	// Verify feed data is unchanged — name/URL must be original, not changed.
	var actualName, actualURL string
	if err := db.DB.QueryRow("SELECT name, url FROM feeds WHERE id = ?", feedID).
		Scan(&actualName, &actualURL); err != nil {
		t.Fatal(err)
	}
	if actualName != "original-name" {
		t.Errorf("BUG: feed name changed to %q, expected %q (partial update)", actualName, "original-name")
	}
	if actualURL != "https://original.example/feed.json" {
		t.Errorf("BUG: feed URL changed to %q, expected original (partial update)", actualURL)
	}

	// Verify mode assignments are unchanged (still mode 1).
	if err := db.DB.QueryRow("SELECT COUNT(*) FROM catalog_mode_feeds WHERE feed_id = ?", feedID).
		Scan(&modeCount); err != nil {
		t.Fatal(err)
	}
	if modeCount != 1 {
		t.Errorf("mode assignments changed: got %d, expected 1 (partial update)", modeCount)
	}
}

func TestUpdateFeedPreservesEnabledState(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()

	// Create an enabled feed.
	if err := db.AddFeed(ctx, "enabled-feed", "https://example.test/feed.json", 0); err != nil {
		t.Fatal(err)
	}
	feedList, err := db.Feeds(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	var feedID int64
	var feedEnabled bool
	for _, f := range feedList {
		if f.Name == "enabled-feed" {
			feedID = f.ID
			feedEnabled = f.Enabled
			break
		}
	}
	if feedID == 0 {
		t.Fatal("created feed not found")
	}
	if !feedEnabled {
		t.Fatal("newly created feed should be enabled, but Enabled=false")
	}

	bgp := &fakeBGP{}
	cfg := testConfig()
	handler := New(cfg, db, feeds.NewSyncer(db, config.Config{}), bgp).Handler()
	adminCookie := &http.Cookie{Name: "wdbgp_admin", Value: sessionToken(cfg.SessionSecret)}

	// Update feed with a name change only — NO enabled field in form.
	updateForm := url.Values{
		"name": {"renamed-feed"},
		"url":  {"https://example.test/feed.json"},
	}
	req := httptest.NewRequest(http.MethodPost,
		"/admin/feed/"+strconv.FormatInt(feedID, 10),
		strings.NewReader(updateForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(adminCookie)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d: body=%s", w.Code, w.Body.String())
	}

	// Verify feed is still enabled after update (the form had no "enabled" field).
	updated, err := db.Feed(ctx, feedID)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.Enabled {
		t.Errorf("BUG: feed became disabled after update with no enabled field in form (got Enabled=%v, want true)", updated.Enabled)
	}
	if updated.Name != "renamed-feed" {
		t.Errorf("feed name not updated: got %q, want %q", updated.Name, "renamed-feed")
	}
}

func TestUpdateFeedCanDisableFeed(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()

	// Step 1: Create an enabled feed.
	if err := db.AddFeed(ctx, "toggle-enabled-feed", "https://example.test/toggle.json", 0); err != nil {
		t.Fatal(err)
	}
	feedList, err := db.Feeds(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	var feedID int64
	for _, f := range feedList {
		if f.Name == "toggle-enabled-feed" {
			feedID = f.ID
			break
		}
	}
	if feedID == 0 {
		t.Fatal("created feed not found")
	}

	// Verify feed starts enabled.
	feed, err := db.Feed(ctx, feedID)
	if err != nil {
		t.Fatal(err)
	}
	if !feed.Enabled {
		t.Fatal("newly created feed should be enabled")
	}

	bgp := &fakeBGP{}
	cfg := testConfig()
	handler := New(cfg, db, feeds.NewSyncer(db, config.Config{}), bgp).Handler()
	adminCookie := &http.Cookie{Name: "wdbgp_admin", Value: sessionToken(cfg.SessionSecret)}

	// Step 2: POST feed update with enabled=off (simulating unchecked checkbox).
	// With the hidden+checkbox pattern, unchecked checkbox submits enabled=off.
	updateForm := url.Values{
		"name":    {"toggle-enabled-feed"},
		"url":     {"https://example.test/toggle.json"},
		"enabled": {"off"},
	}
	req := httptest.NewRequest(http.MethodPost,
		"/admin/feed/"+strconv.FormatInt(feedID, 10),
		strings.NewReader(updateForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(adminCookie)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d: body=%s", w.Code, w.Body.String())
	}

	// Step 3: Verify feed becomes disabled.
	updated, err := db.Feed(ctx, feedID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Enabled {
		t.Errorf("feed should be disabled after update with enabled=off, but Enabled is true")
	}
}
