package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrey-vk/wdbgp/internal/config"
	"github.com/andrey-vk/wdbgp/internal/feeds"
	"github.com/andrey-vk/wdbgp/internal/store"
)

func TestCIDRDebugCoverageByServiceAndUser(t *testing.T) {
	db := debugTestStore(t)
	ctx := context.Background()
	feedID := addDebugFeed(t, db, "enabled", true)
	disabledID := addDebugFeed(t, db, "disabled", false)
	if _, err := db.DB.Exec(`INSERT INTO catalog_entries(feed_id, category, service, cidr) VALUES
		(?, 'Full', 'Whole', '10.0.0.0/24'),
		(?, 'Parts', 'First half', '10.0.0.0/25'),
		(?, 'Parts', 'First half', '10.0.0.0/26'),
		(?, 'Parts', 'Second half', '10.0.0.128/25'),
		(?, 'Parts', 'Quarter', '10.0.0.0/26'),
		(?, 'Full', 'Split whole', '10.0.0.0/25'),
		(?, 'Full', 'Split whole', '10.0.0.128/25'),
		(?, 'IPv6', 'V6 service', '2001:db8::/64'),
		(?, 'Disabled', 'Ignored', '10.0.0.0/24')`,
		feedID, feedID, feedID, feedID, feedID, feedID, feedID, feedID, disabledID); err != nil {
		t.Fatal(err)
	}

	addDebugUser(t, db, "full-user", "192.168.20.0/24",
		[]string{"Full"}, nil)
	addDebugUser(t, db, "half-user", "192.168.21.0/24", nil,
		[]store.ServiceKey{{Category: "Parts", Service: "First half"}})
	addDebugUser(t, db, "parts-user", "192.168.22.0/24",
		[]string{"Parts"}, nil)

	server := New(config.Config{}, db, feeds.NewSyncer(db), &fakeBGP{})
	result, err := server.debugCIDR(ctx, "10.0.0.0/24")
	if err != nil {
		t.Fatal(err)
	}
	if result.Query != "10.0.0.0/24" {
		t.Fatalf("query = %q", result.Query)
	}
	full := coverageByService(result.FullServices)
	if len(result.FullServices) != 2 ||
		full["Full/Whole"] != 100 ||
		full["Full/Split whole"] != 100 {
		t.Fatalf("full services = %#v", result.FullServices)
	}
	partial := coverageByService(result.PartialServices)
	if partial["Parts/First half"] != 50 ||
		partial["Parts/Second half"] != 50 ||
		partial["Parts/Quarter"] != 25 {
		t.Fatalf("partial services = %#v", result.PartialServices)
	}
	if result.CombinedPercentage != 0 || len(result.CombinedServices) != 0 {
		t.Fatalf("combined coverage should be omitted when a full service exists: %#v", result)
	}
	for _, item := range append(result.FullServices, result.PartialServices...) {
		if item.Category == "Disabled" {
			t.Fatalf("disabled feed included: %#v", item)
		}
	}
	users := coverageByName(result.Users)
	if users["full-user"] != 100 || users["half-user"] != 50 || users["parts-user"] != 100 {
		t.Fatalf("users = %#v", result.Users)
	}
	for _, user := range result.Users {
		if len(user.Matches) == 0 {
			t.Fatalf("user matches missing: %#v", user)
		}
	}

	ipResult, err := server.debugCIDR(ctx, "2001:db8::1")
	if err != nil {
		t.Fatal(err)
	}
	if ipResult.Query != "2001:db8::1/128" ||
		len(ipResult.FullServices) != 1 ||
		ipResult.FullServices[0].Service != "V6 service" {
		t.Fatalf("IPv6 result = %#v", ipResult)
	}
}

func TestCIDRDebugCombinedCoverageFromMultipleServices(t *testing.T) {
	db := debugTestStore(t)
	feedID := addDebugFeed(t, db, "enabled", true)
	if _, err := db.DB.Exec(`INSERT INTO catalog_entries(feed_id, category, service, cidr) VALUES
		(?, 'Parts', 'First half', '10.0.0.0/25'),
		(?, 'Parts', 'Second half', '10.0.0.128/25')`, feedID, feedID); err != nil {
		t.Fatal(err)
	}

	result, err := New(config.Config{}, db, feeds.NewSyncer(db), &fakeBGP{}).
		debugCIDR(context.Background(), "10.0.0.0/24")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.FullServices) != 0 || len(result.PartialServices) != 2 ||
		len(result.CombinedServices) != 2 || result.CombinedPercentage != 100 {
		t.Fatalf("combined result = %#v", result)
	}
}

func TestCIDRDebugEndpointRequiresAdminAndReturnsJSON(t *testing.T) {
	db := debugTestStore(t)
	feedID := addDebugFeed(t, db, "enabled", true)
	if _, err := db.DB.Exec(`INSERT INTO catalog_entries(feed_id, category, service, cidr)
		VALUES (?, 'DNS', 'Resolver', '8.8.8.0/24')`, feedID); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{SessionSecret: "secret"}
	handler := New(cfg, db, feeds.NewSyncer(db), &fakeBGP{}).Handler()

	request := httptest.NewRequest(http.MethodGet, "/admin/debug/cidr?cidr=8.8.8.8", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("unauthenticated status = %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodGet, "/admin/debug/cidr?cidr=8.8.8.8", nil)
	request.AddCookie(&http.Cookie{Name: "wdbgp_admin", Value: sessionToken(cfg.SessionSecret)})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK ||
		!strings.HasPrefix(response.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("authenticated response: status=%d content-type=%q body=%s",
			response.Code, response.Header().Get("Content-Type"), response.Body.String())
	}
	var result cidrDebugResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result.FullServices) != 1 || result.FullServices[0].Service != "Resolver" {
		t.Fatalf("endpoint result = %#v", result)
	}

	request = httptest.NewRequest(http.MethodGet, "/admin/debug/cidr?cidr=invalid", nil)
	request.AddCookie(&http.Cookie{Name: "wdbgp_admin", Value: sessionToken(cfg.SessionSecret)})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid CIDR status = %d, body=%s", response.Code, response.Body.String())
	}
}

func debugTestStore(t *testing.T) *store.Store {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "debug.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.DB.Exec("UPDATE feeds SET enabled = 0"); err != nil {
		t.Fatal(err)
	}
	return db
}

func addDebugFeed(t *testing.T, db *store.Store, name string, enabled bool) int64 {
	t.Helper()
	if err := db.AddFeed(context.Background(), name, "https://example.test/"+name, enabled); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := db.DB.QueryRow("SELECT id FROM feeds WHERE name = ?", name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func addDebugUser(
	t *testing.T,
	db *store.Store,
	name string,
	network string,
	categories []string,
	services []store.ServiceKey,
) {
	t.Helper()
	userID, err := db.AddUser(context.Background(), store.User{
		Name: name, PeerIP: network[:strings.IndexByte(network, '/')],
		PeerASN: 65001, Enabled: true, Networks: []string{network},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Transaction(context.Background(), func(tx *sql.Tx) error {
		return store.SetUserSelection(context.Background(), tx, userID, categories, services)
	}); err != nil {
		t.Fatal(err)
	}
}

func coverageByService(items []coverageItem) map[string]float64 {
	result := make(map[string]float64, len(items))
	for _, item := range items {
		result[item.Category+"/"+item.Service] = item.Percentage
	}
	return result
}

func coverageByName(items []coverageItem) map[string]float64 {
	result := make(map[string]float64, len(items))
	for _, item := range items {
		result[item.Name] = item.Percentage
	}
	return result
}
