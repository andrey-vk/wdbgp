package web

import (
	"context"
	"database/sql"
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

type fakeBGP struct {
	reconciles int
	reloads    int
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
	cfg := config.Config{
		AdminPassword: "admin", SessionSecret: "secret",
		AdminCookieSecure: "true", DefaultLanguage: "ru",
	}
	handler := New(cfg, db, feeds.NewSyncer(db), bgp).Handler()

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
	request = httptest.NewRequest(http.MethodGet, "/admin", nil)
	request.AddCookie(cookies[0])
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Глобальная фильтрация маршрутов") {
		t.Fatalf("admin filter page: status=%d body=%s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/admin", nil)
	request.Header.Set("Accept-Language", "en")
	request.AddCookie(cookies[0])
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), "Global route filtering") ||
		!strings.Contains(response.Body.String(), `<html lang="en">`) {
		t.Fatalf("English admin page: status=%d body=%s", response.Code, response.Body.String())
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
	if err := db.UpdateCatalogMode(ctx, ipranges); err != nil {
		t.Fatal(err)
	}
	if err := db.AddFeedForMode(
		ctx, "precise", "https://example.test/precise", ipranges.ID, true); err != nil {
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
	handler := New(config.Config{}, db, feeds.NewSyncer(db), bgp).Handler()
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
	New(config.Config{DefaultLanguage: "ru"}, db, feeds.NewSyncer(db), &fakeBGP{}).
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
	if !strings.Contains(body, `value="Messengers:Telegram"  disabled`) {
		t.Fatalf("contained selected service is not disabled: %s", body)
	}
	if !strings.Contains(body, `value="Messengers:Signal"  disabled`) {
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
	cfg := config.Config{
		AdminPassword: "admin", SessionSecret: "secret", DefaultLanguage: "en",
	}
	handler := New(cfg, db, feeds.NewSyncer(db), bgp).Handler()
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
		"name": {"custom-renamed"},
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
	if feed.Name != "custom-renamed" || feed.Enabled {
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
	if response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), `value="custom-renamed"`) ||
		!strings.Contains(response.Body.String(), "/admin/feed/"+strconv.FormatInt(feed.ID, 10)+"/delete") {
		t.Fatalf("managed feed not rendered: status=%d body=%s", response.Code, response.Body.String())
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

func TestSelectionSavesPreserveDisabledFeedSelections(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "web.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.DB.Exec("UPDATE feeds SET enabled = 0"); err != nil {
		t.Fatal(err)
	}
	if err := db.AddFeed(context.Background(), "enabled", "https://example.test/enabled", true); err != nil {
		t.Fatal(err)
	}
	if err := db.AddFeed(context.Background(), "disabled", "https://example.test/disabled", false); err != nil {
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

	cfg := config.Config{
		AdminPassword: "admin", SessionSecret: "secret", DefaultLanguage: "en",
	}
	bgp := &fakeBGP{}
	handler := New(cfg, db, feeds.NewSyncer(db), bgp).Handler()
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
				db, feeds.NewSyncer(db), &fakeBGP{}).Handler()
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
	handler := New(cfg, db, feeds.NewSyncer(db), &fakeBGP{}).Handler()

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
	if response.Code != http.StatusOK {
		t.Fatalf("admin page after HTTP login: status=%d body=%s", response.Code, response.Body.String())
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
	handler := New(cfg, db, feeds.NewSyncer(db), &fakeBGP{}).Handler()

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
