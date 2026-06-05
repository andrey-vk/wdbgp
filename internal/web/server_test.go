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
	cfg := config.Config{AdminPassword: "admin", SessionSecret: "secret"}
	handler := New(cfg, db, feeds.NewSyncer(db), bgp).Handler()

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "192.168.20.15:12345"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Telegram") {
		t.Fatalf("user page: status=%d body=%s", response.Code, response.Body.String())
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

	request = httptest.NewRequest(http.MethodGet, "/admin/user/"+strconv.FormatInt(userID, 10), nil)
	request.AddCookie(cookies[0])
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Параметры пользователя") {
		t.Fatalf("admin user page: status=%d body=%s", response.Code, response.Body.String())
	}

	filterForm := url.Values{
		"filter_override": {"on"},
		"filter_deny":     {"1.1.1.1/32"},
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
	if !user.FilterOverride || len(filters.Deny) != 1 || filters.Deny[0] != "1.1.1.1/32" {
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
}
