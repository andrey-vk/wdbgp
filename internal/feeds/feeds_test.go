package feeds

import (
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrey-vk/wdbgp/internal/store"
)

func TestParseCanonicalNormalizesAndDeduplicates(t *testing.T) {
	entries, err := Parse([]byte(`{"entries":[
		{"category":"Messengers","service":"Telegram","cidrs":["149.154.167.99/20","149.154.160.0/20"]}
	]}`), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].CIDR != "149.154.160.0/20" {
		t.Fatalf("unexpected entries: %#v", entries)
	}
}

func TestParseOpenCCKFlatUsesGroupLookup(t *testing.T) {
	entries, err := Parse([]byte(`{"Telegram":["149.154.167.99/20"]}`),
		map[string][]string{"Telegram": {"Messengers"}}, "fallback")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Category != "Messengers" ||
		entries[0].Service != "Telegram" || entries[0].CIDR != "149.154.160.0/20" {
		t.Fatalf("unexpected entries: %#v", entries)
	}
}

func TestMetadataURL(t *testing.T) {
	got := MetadataURL("https://iplist.opencck.org/?format=json&data=cidr4")
	want := "https://iplist.opencck.org/?data=group&format=json"
	if got != want {
		t.Fatalf("MetadataURL = %q, want %q", got, want)
	}
}

func TestLegacyOpenCCKFeedCategory(t *testing.T) {
	for _, category := range []string{"opencck-main", "opencck-beta", "opencck-main-v6", "opencck-beta-v6"} {
		if !isLegacyOpenCCKFeedCategory(category) {
			t.Fatalf("%q was not recognized as a legacy OpenCCK feed category", category)
		}
	}
	if isLegacyOpenCCKFeedCategory("Messengers") {
		t.Fatal("normal category was treated as legacy OpenCCK feed category")
	}
}

func TestSyncAllSkipsDisabledFeeds(t *testing.T) {
	var requests int
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(`{"entries":[
				{"category":"Test","service":"Enabled","cidrs":["8.8.8.0/24"]}
			]}`)),
			Header: make(http.Header),
		}, nil
	})}

	db, err := store.Open(filepath.Join(t.TempDir(), "feeds.sqlite3"))
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

	syncer := NewSyncer(db)
	syncer.Client = client
	if syncErrors := syncer.SyncAll(context.Background()); len(syncErrors) != 0 {
		t.Fatalf("SyncAll errors = %v", syncErrors)
	}
	if requests != 1 {
		t.Fatalf("HTTP requests = %d, want 1", requests)
	}
	var enabledEntries, disabledEntries int
	if err := db.DB.QueryRow(`
SELECT COUNT(*) FROM catalog_entries ce JOIN feeds f ON f.id = ce.feed_id
WHERE f.name = 'enabled'`).Scan(&enabledEntries); err != nil {
		t.Fatal(err)
	}
	if err := db.DB.QueryRow(`
SELECT COUNT(*) FROM catalog_entries ce JOIN feeds f ON f.id = ce.feed_id
WHERE f.name = 'disabled'`).Scan(&disabledEntries); err != nil {
		t.Fatal(err)
	}
	if enabledEntries != 1 || disabledEntries != 0 {
		t.Fatalf("entries enabled=%d disabled=%d, want 1 and 0", enabledEntries, disabledEntries)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}
