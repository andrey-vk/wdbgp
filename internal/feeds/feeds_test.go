package feeds

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/andrey-vk/wdbgp/internal/config"
	"github.com/andrey-vk/wdbgp/internal/store"
)

func testLimits() AdapterLimits {
	return AdapterLimits{
		MaxSourceBytes: 1 << 20, MaxResponseBytes: 16 << 20,
		MaxTotalBytes: 64 << 20, MaxEntries: 1_000_000,
		MaxRequests: 200, MaxCallStack: 1_000,
	}
}

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

func TestJavaScriptAdapterNormalizesResult(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "https://example.test/feed" {
			t.Fatalf("request URL = %q", request.URL)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`["149.154.167.99/20"]`)),
			Header:     make(http.Header),
		}, nil
	})}
	entries, err := (adapterRunner{limits: testLimits(), client: client, timeout: time.Second}).run(
		context.Background(),
		store.Feed{ID: 1, Name: "test", URL: "https://example.test/feed"},
		store.FeedAdapter{
			Key: "test", APIVersion: 1, Source: `
function sync(feed, api) {
    return [{
        category: "Messengers",
        service: "Telegram",
        cidrs: JSON.parse(api.httpGet(feed.url))
    }];
}`,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].CIDR != "149.154.160.0/20" {
		t.Fatalf("entries = %#v", entries)
	}
}

func TestJavaScriptAdapterSRSGet(t *testing.T) {
	// Build a tiny valid SRS binary in memory and serve it via mock HTTP.
	// The SRS v1 format: header (SRS+version) + zlib-compressed body.
	// Body: rule_count(uvarint=1), rule_type(0), item_type(6=ip_cidr),
	//       IPSet(version=1, range_count=2, ranges), item_type(0xFF), invert(0)

	// We'll generate a tiny SRS with two IPv4 CIDRs: 10.0.0.0/8 and 192.168.0.0/16

	var srsBuf bytes.Buffer
	srsBuf.Write([]byte{0x53, 0x52, 0x53, 0x01}) // SRS v1

	zw := zlib.NewWriter(&srsBuf)
	bw := bufio.NewWriter(zw)

	// rule_count = 1
	uvbuf := make([]byte, binary.MaxVarintLen64)
	n := binary.PutUvarint(uvbuf, 1)
	bw.Write(uvbuf[:n])

	// rule_type = 0 (default)
	bw.WriteByte(0)

	// item_type = 6 (ip_cidr)
	bw.WriteByte(6)

	// IPSet: version=1
	bw.WriteByte(1)
	// range_count = 2
	binary.Write(bw, binary.BigEndian, uint64(2))

	// Range 1: 10.0.0.0 -> 10.255.255.255 (/8)
	from1 := netip.MustParseAddr("10.0.0.0").AsSlice()
	to1 := netip.MustParseAddr("10.255.255.255").AsSlice()
	n1 := binary.PutUvarint(uvbuf, uint64(len(from1)))
	bw.Write(uvbuf[:n1])
	bw.Write(from1)
	n1 = binary.PutUvarint(uvbuf, uint64(len(to1)))
	bw.Write(uvbuf[:n1])
	bw.Write(to1)

	// Range 2: 192.168.0.0 -> 192.168.255.255 (/16)
	from2 := netip.MustParseAddr("192.168.0.0").AsSlice()
	to2 := netip.MustParseAddr("192.168.255.255").AsSlice()
	n2 := binary.PutUvarint(uvbuf, uint64(len(from2)))
	bw.Write(uvbuf[:n2])
	bw.Write(from2)
	n2 = binary.PutUvarint(uvbuf, uint64(len(to2)))
	bw.Write(uvbuf[:n2])
	bw.Write(to2)

	// item_type = 0xFF (final), invert = 0
	bw.WriteByte(0xFF)
	bw.WriteByte(0)

	bw.Flush()
	zw.Close()

	srsData := srsBuf.Bytes()

	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "https://example.test/feed.srs" {
			t.Fatalf("request URL = %q", request.URL)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(srsData)),
			Header:     make(http.Header),
		}, nil
	})}

	entries, err := (adapterRunner{limits: testLimits(), client: client, timeout: time.Second}).run(
		context.Background(),
		store.Feed{ID: 1, Name: "test", URL: "https://example.test/feed.srs"},
		store.FeedAdapter{
			Key: "test-srs", APIVersion: 1, Source: `
function sync(feed, api) {
    var entries = api.srsGet(feed.url, JSON.stringify({cidrs: true}));
    return entries.map(function(e) {
        return { category: "Test", service: "test-srs", cidrs: e.cidrs };
    });
}`,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	// We expect CIDRs: 10.0.0.0/8 and 192.168.0.0/16
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d: %#v", len(entries), entries)
	}
	expected := map[string]bool{"10.0.0.0/8": true, "192.168.0.0/16": true}
	for _, e := range entries {
		if !expected[e.CIDR] {
			t.Errorf("unexpected CIDR: %s", e.CIDR)
		}
	}
}

func TestJavaScriptAdapterRejectsUnlistedHost(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		t.Fatal("HTTP client must not be called")
		return nil, nil
	})}
	_, err := (adapterRunner{limits: testLimits(), client: client, timeout: time.Second}).run(
		context.Background(),
		store.Feed{URL: "https://example.test/feed"},
		store.FeedAdapter{
			Key: "test", APIVersion: 1,
			Source: `function sync(feed, api) {
                api.httpGet("https://other.test/feed");
                return [];
            }`,
		},
	)
	if err == nil || !strings.Contains(err.Error(), "is not allowed") {
		t.Fatalf("error = %v, want disallowed host", err)
	}
}

func TestJavaScriptAdapterTimesOut(t *testing.T) {
	_, err := (adapterRunner{
		limits: testLimits(), client: &http.Client{}, timeout: 10 * time.Millisecond,
	}).run(
		context.Background(),
		store.Feed{URL: "https://example.test/feed"},
		store.FeedAdapter{
			Key: "test", APIVersion: 1,
			Source: `function sync() { while (true) {} }`,
		},
	)
	if err == nil ||
		(!strings.Contains(err.Error(), "timed out") &&
			!strings.Contains(err.Error(), "deadline exceeded")) {
		t.Fatalf("error = %v, want timeout", err)
	}
}

func TestJavaScriptAdapterStopsWhenContextIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	started := time.Now()
	_, err := (adapterRunner{
		limits: testLimits(), client: &http.Client{}, timeout: 5 * time.Second,
	}).run(
		ctx,
		store.Feed{URL: "https://example.test/feed"},
		store.FeedAdapter{
			Key: "test", APIVersion: 1,
			Source: `function sync() { while (true) {} }`,
		},
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("context cancellation took %s", elapsed)
	}
}

func TestAdapterHTTPFollowsValidatedRedirect(t *testing.T) {
	var requests []string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests = append(requests, request.URL.String())
		if len(requests) == 1 {
			return &http.Response{
				StatusCode: http.StatusFound,
				Header: http.Header{
					"Location": {"https://example.test/redirected"},
				},
				Body: io.NopCloser(strings.NewReader("")),
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("ok")),
		}, nil
	})}
	api, err := newAdapterHTTP(
		context.Background(), client, "https://example.test/feed", "",
		AdapterLimits{MaxRequests: 200, MaxResponseBytes: 16 << 20, MaxTotalBytes: 64 << 20})
	if err != nil {
		t.Fatal(err)
	}
	body, err := api.get("https://example.test/feed")
	if err != nil {
		t.Fatal(err)
	}
	if body != "ok" || len(requests) != 2 || api.requests != 2 {
		t.Fatalf("body=%q requests=%v count=%d", body, requests, api.requests)
	}
}

func TestAdapterHTTPRejectsRedirectToUnlistedHost(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusFound,
			Header: http.Header{
				"Location": {"https://other.test/redirected"},
			},
			Body: io.NopCloser(strings.NewReader("")),
		}, nil
	})}
	api, err := newAdapterHTTP(
		context.Background(), client, "https://example.test/feed", "",
		AdapterLimits{MaxRequests: 200, MaxResponseBytes: 16 << 20, MaxTotalBytes: 64 << 20})
	if err != nil {
		t.Fatal(err)
	}
	_, err = api.get("https://example.test/feed")
	if err == nil || !strings.Contains(err.Error(), "is not allowed") {
		t.Fatalf("error = %v, want disallowed redirect host", err)
	}
}

func TestPublicDialContextTriesAllPublicAddresses(t *testing.T) {
	first := netip.MustParseAddr("2001:4860:4860::8888")
	second := netip.MustParseAddr("8.8.8.8")
	var attempts []string
	clientSide, serverSide := net.Pipe()
	defer serverSide.Close()
	dial := publicDialContext(
		func(context.Context, string, string) ([]netip.Addr, error) {
			return []netip.Addr{
				netip.MustParseAddr("127.0.0.1"), first, second,
			}, nil
		},
		func(_ context.Context, _, address string) (net.Conn, error) {
			attempts = append(attempts, address)
			if strings.HasPrefix(address, "[") {
				return nil, errors.New("IPv6 unavailable")
			}
			return clientSide, nil
		},
		nil,
	)
	connection, err := dial(context.Background(), "tcp", "example.test:443")
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if len(attempts) != 2 ||
		attempts[0] != net.JoinHostPort(first.String(), "443") ||
		attempts[1] != net.JoinHostPort(second.String(), "443") {
		t.Fatalf("dial attempts = %v", attempts)
	}
}

func TestPublicDialContextAllowsConfiguredProxy(t *testing.T) {
	clientSide, serverSide := net.Pipe()
	defer serverSide.Close()
	dial := publicDialContext(
		func(context.Context, string, string) ([]netip.Addr, error) {
			t.Fatal("proxy endpoint must not use public destination lookup")
			return nil, nil
		},
		func(_ context.Context, _, address string) (net.Conn, error) {
			if address != "proxy.internal:3128" {
				t.Fatalf("proxy address = %q", address)
			}
			return clientSide, nil
		},
		map[string]bool{"proxy.internal": true},
	)
	connection, err := dial(context.Background(), "tcp", "proxy.internal:3128")
	if err != nil {
		t.Fatal(err)
	}
	connection.Close()
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
	if err := db.AddFeed(context.Background(), "enabled", "https://example.test/enabled", true, 0); err != nil {
		t.Fatal(err)
	}
	if err := db.AddFeed(context.Background(), "disabled", "https://example.test/disabled", false, 0); err != nil {
		t.Fatal(err)
	}

	syncer := NewSyncer(db, config.Config{})
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
	if err := db.AddFeed(ctx, "custom", oldURL, true, 0); err != nil {
		t.Fatal(err)
	}
	feedList, err := db.Feeds(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	feed := feedList[0]

	syncer := NewSyncer(db, config.Config{})
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

func TestSyncDiscardsResultWhenAdapterChanges(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "feeds.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.DB.Exec("UPDATE feeds SET enabled = 0"); err != nil {
		t.Fatal(err)
	}
	if err := db.AddFeed(ctx, "custom", "https://example.test/feed", true, 0); err != nil {
		t.Fatal(err)
	}
	feedList, err := db.Feeds(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	feed := feedList[0]
	adapter, err := db.FeedAdapter(ctx, feed.AdapterID)
	if err != nil {
		t.Fatal(err)
	}

	syncer := NewSyncer(db, config.Config{})
	syncer.Client = &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		adapter.Name = "changed during sync"
		if err := db.UpdateFeedAdapter(ctx, adapter); err != nil {
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
	var entries int
	if err := db.DB.QueryRow(
		"SELECT COUNT(*) FROM catalog_entries WHERE feed_id = ?", feed.ID).
		Scan(&entries); err != nil {
		t.Fatal(err)
	}
	if entries != 0 {
		t.Fatalf("stale catalog entries = %d, want 0", entries)
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
	syncer := NewSyncer(db, config.Config{})
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
	catalog, err := db.CatalogForMode(ctx, store.IPRangesCatalogModeID, false)
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
