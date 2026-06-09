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

func TestSyncDiscardsDownloadWhenFeedURLChanges(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "feeds.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.DB.Exec("UPDATE feeds SET enabled = 0"); err != nil {
		t.Fatal(err)
	}
	const oldURL = "https://example.test/old"
	const newURL = "https://example.test/new"
	if err := db.AddFeed(ctx, "custom", oldURL, true); err != nil {
		t.Fatal(err)
	}
	feedList, err := db.Feeds(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	feed := feedList[0]

	syncer := NewSyncer(db)
	syncer.Client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != oldURL {
			t.Fatalf("download URL = %q, want %q", request.URL, oldURL)
		}
		feed.URL = newURL
		if err := db.UpdateFeed(ctx, feed); err != nil {
			t.Fatal(err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(`{"entries":[
				{"category":"Test","service":"Stale","cidrs":["8.8.8.0/24"]}
			]}`)),
			Header: make(http.Header),
		}, nil
	})}

	if syncErrors := syncer.SyncAll(ctx); len(syncErrors) != 0 {
		t.Fatalf("SyncAll errors = %v", syncErrors)
	}
	var url, lastSuccess, lastError string
	if err := db.DB.QueryRow(`
SELECT url, COALESCE(last_success, ''), COALESCE(last_error, '')
FROM feeds WHERE id = ?`, feed.ID).Scan(&url, &lastSuccess, &lastError); err != nil {
		t.Fatal(err)
	}
	if url != newURL || lastSuccess != "" || lastError != "" {
		t.Fatalf("feed state = url %q success %q error %q", url, lastSuccess, lastError)
	}
	var entries int
	if err := db.DB.QueryRow("SELECT COUNT(*) FROM catalog_entries WHERE feed_id = ?", feed.ID).
		Scan(&entries); err != nil {
		t.Fatal(err)
	}
	if entries != 0 {
		t.Fatalf("stale catalog entries = %d, want 0", entries)
	}
}

func TestDownloadIPRangesBuildsServiceCatalog(t *testing.T) {
	var requests int
	syncer := &Syncer{Client: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if request.URL.Host != "raw.githubusercontent.com" ||
			!strings.HasPrefix(request.URL.Path, "/antonme/ipranges/main/") {
			t.Fatalf("unexpected IPRanges URL: %s", request.URL)
		}
		body := "203.0.113.99/24\n"
		if strings.Contains(request.URL.Path, "ipv6_merged.txt") {
			body = "2001:db8:1::1/48\n"
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})}}

	entries, err := syncer.downloadIPRanges(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	expectedRequests := 0
	for _, service := range ipRangesServices {
		expectedRequests++
		if service.ipv6 {
			expectedRequests++
		}
	}
	if requests != expectedRequests || len(entries) != expectedRequests {
		t.Fatalf("requests=%d entries=%d, want %d", requests, len(entries), expectedRequests)
	}
	var telegramV4, googleCloudV6, youtubeV4 bool
	for _, entry := range entries {
		if entry.Service == "Telegram" && entry.CIDR == "203.0.113.0/24" {
			telegramV4 = true
		}
		if entry.Service == "Google Cloud" && entry.CIDR == "2001:db8:1::/48" {
			googleCloudV6 = true
		}
		if entry.Service == "YouTube" && entry.CIDR == "203.0.113.0/24" {
			youtubeV4 = true
		}
	}
	if !telegramV4 || !googleCloudV6 || !youtubeV4 {
		t.Fatalf("missing normalized service entries: %#v", entries)
	}
}

func TestSyncIPRangesFeedStoresModeCatalog(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "ipranges.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mode, err := db.CatalogMode(ctx, store.IPRangesCatalogModeID)
	if err != nil {
		t.Fatal(err)
	}
	mode.Enabled = true
	if err := db.UpdateCatalogMode(ctx, mode); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB.Exec(
		"UPDATE feeds SET enabled = CASE WHEN mode_id = ? THEN 1 ELSE 0 END",
		store.IPRangesCatalogModeID); err != nil {
		t.Fatal(err)
	}
	feedList, err := db.Feeds(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(feedList) != 1 || feedList[0].URL != ipRangesURL {
		t.Fatalf("enabled feeds = %#v", feedList)
	}
	syncer := NewSyncer(db)
	syncer.Client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body := "203.0.113.0/24\n"
		if strings.Contains(request.URL.Path, "ipv6_merged.txt") {
			body = "2001:db8::/32\n"
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})}
	if err := syncer.syncOne(ctx, feedList[0]); err != nil {
		t.Fatal(err)
	}
	catalog, err := db.CatalogForMode(ctx, store.IPRangesCatalogModeID)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog["Cloud providers"]) == 0 ||
		len(catalog["Infrastructure"]) == 0 ||
		len(catalog["Platforms"]) == 0 {
		t.Fatalf("IPRanges catalog = %#v", catalog)
	}
	var wrongModeEntries int
	if err := db.DB.QueryRow(`
SELECT COUNT(*)
FROM catalog_entries ce JOIN feeds f ON f.id = ce.feed_id
WHERE f.mode_id != ? AND ce.cidr IN ('203.0.113.0/24', '2001:db8::/32')`,
		store.IPRangesCatalogModeID).Scan(&wrongModeEntries); err != nil {
		t.Fatal(err)
	}
	if wrongModeEntries != 0 {
		t.Fatalf("IPRanges entries leaked into another mode: %d", wrongModeEntries)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}
