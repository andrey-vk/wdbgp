//nolint:errcheck,gosec // test file, errors in cleanup/generators intentionally ignored
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

	"github.com/andrey-vk/wdbgp/internal/settings"
	"github.com/andrey-vk/wdbgp/internal/store"
)

func testSettings() *settings.Settings {
	s, _ := settings.New(settings.NewTestStore())
	return s
}

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
	entries, err := (feedRunner{limits: testLimits(), client: client, timeout: time.Second}).run(
		context.Background(),
		store.Feed{ID: 1, Name: "test", URL: "https://example.test/feed"},
		store.FeedAdapter{
			APIVersion: 1, Source: `
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
	bw.Write(uvbuf[:n]) //nolint:gosec // test buffer write

	// rule_type = 0 (default)
	bw.WriteByte(0) //nolint:gosec // test buffer write

	// item_type = 6 (ip_cidr)
	bw.WriteByte(6) //nolint:gosec // test buffer write

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

	entries, err := (feedRunner{limits: testLimits(), client: client, timeout: time.Second}).run(
		context.Background(),
		store.Feed{ID: 1, Name: "test", URL: "https://example.test/feed.srs"},
		store.FeedAdapter{
			APIVersion: 1, Source: `
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
	_, err := (feedRunner{limits: testLimits(), client: client, timeout: time.Second}).run(
		context.Background(),
		store.Feed{URL: "https://example.test/feed", RestrictHosts: true},
		store.FeedAdapter{
			APIVersion: 1,
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
	_, err := (feedRunner{
		limits: testLimits(), client: &http.Client{}, timeout: 10 * time.Millisecond,
	}).run(
		context.Background(),
		store.Feed{URL: "https://example.test/feed"},
		store.FeedAdapter{
			APIVersion: 1,
			Source:     `function sync() { while (true) {} }`,
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
	_, err := (feedRunner{
		limits: testLimits(), client: &http.Client{}, timeout: 5 * time.Second,
	}).run(
		ctx,
		store.Feed{URL: "https://example.test/feed"},
		store.FeedAdapter{
			APIVersion: 1,
			Source:     `function sync() { while (true) {} }`,
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
	api, err := newAdapterRunner(
		context.Background(), client, "https://example.test/feed", "", true,
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
	api, err := newAdapterRunner(
		context.Background(), client, "https://example.test/feed", "", true,
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

func TestIsPublicAddressRejectsNonGlobalRanges(t *testing.T) {
	blocked := []string{
		// stdlib-covered basics
		"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.1.1",
		"0.0.0.0", "224.0.0.1", "::1", "fe80::1", "fc00::1", "ff02::1", "::",
		// IANA special-purpose ranges the stdlib predicates miss
		"0.255.255.255",                 // 0.0.0.0/8
		"100.64.0.1",                    // CGNAT
		"100.127.255.254",               // CGNAT upper edge
		"192.0.0.8",                     // IETF protocol assignments
		"192.0.2.55",                    // TEST-NET-1
		"192.88.99.1",                   // 6to4 relay anycast
		"198.18.0.1",                    // benchmarking
		"198.19.255.255",                // benchmarking upper half
		"198.51.100.7",                  // TEST-NET-2
		"203.0.113.99",                  // TEST-NET-3
		"240.0.0.1",                     // reserved
		"255.255.255.255",               // broadcast
		"::ffff:10.0.0.1",               // IPv4-mapped private
		"::ffff:100.64.0.1",             // IPv4-mapped CGNAT
		"64:ff9b:1::1",                  // local-use NAT64
		"64:ff9b::a00:1",                // NAT64 embedding 10.0.0.1
		"100::1",                        // discard-only
		"100:0:0:1::1",                  // dummy prefix (non-global)
		"100:0:0:1:ffff:ffff:ffff:ffff", // dummy prefix upper edge
		"2001::1",                       // TEREDO
		"2001:2::1",                     // benchmarking
		"2001:db8::1",                   // documentation
		"2002::1",                       // 6to4
		"3fff::1",                       // documentation (RFC 9637)
		"5f00::1",                       // SRv6 SIDs (reserved space, outside 2000::/3)
		// IPv6 outside the 2000::/3 global-unicast allocation — reserved or
		// deprecated space a deny-list can't enumerate
		"fec0::1",      // deprecated site-local
		"4000::1",      // IETF reserved
		"8000::1",      // IETF reserved
		"e000::1",      // IETF reserved
		"100:0:0:2::1", // reserved space just past the dummy prefix
		"1fff:ffff::1", // last address block below 2000::/3
		"4::1",         // low reserved space
	}
	for _, raw := range blocked {
		if isPublicAddress(netip.MustParseAddr(raw)) {
			t.Errorf("isPublicAddress(%s) = true, want false", raw)
		}
	}

	allowed := []string{
		"8.8.8.8", "1.1.1.1", "203.0.112.255", "198.20.0.1",
		"2001:4860:4860::8888", "2620:fe::fe",
		"::ffff:8.8.8.8",   // IPv4-mapped public
		"64:ff9b::808:808", // NAT64 embedding 8.8.8.8 (legit DNS64 synthesis)
		// globally reachable exceptions inside otherwise-blocked ranges
		"192.0.0.9",     // PCP anycast inside 192.0.0.0/24
		"192.0.0.10",    // NAT traversal anycast inside 192.0.0.0/24
		"2001:1::1",     // PCP anycast inside 2001::/23
		"2001:1::2",     // TURN anycast inside 2001::/23
		"2001:3::1",     // AMT inside 2001::/23
		"2001:4:112::1", // AS112-v6 inside 2001::/23
		"2001:20::1",    // ORCHIDv2 inside 2001::/23
		"2000::1",       // first address of the global-unicast block
		"3ffe::1",       // inside 2000::/3 (former 6bone, returned to the free pool)
	}
	for _, raw := range allowed {
		if !isPublicAddress(netip.MustParseAddr(raw)) {
			t.Errorf("isPublicAddress(%s) = false, want true", raw)
		}
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

	db, err := store.Open(filepath.Join(t.TempDir(), "feeds.sqlite3"), false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.DB.Exec("UPDATE feeds SET enabled = 0"); err != nil {
		t.Fatal(err)
	}
	enabledID, err := db.AddFeed(context.Background(), "enabled", "https://example.test/enabled", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	disabledID, err := db.AddFeed(context.Background(), "disabled", "https://example.test/disabled", 1, false, 0, "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB.ExecContext(context.Background(), "INSERT INTO catalog_mode_feeds(mode_id, feed_id) VALUES (1, ?), (1, ?)", enabledID, disabledID); err != nil {
		t.Fatal(err)
	}

	syncer := NewSyncer(db, testSettings())
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

// TestForgetFeedClearsLiveState — deleting a feed must drop the syncer's
// live-status and completion-baseline entries, and an in-flight sync of a
// forgotten feed must not plant a baseline a future feed reusing the
// SQLite rowid would inherit.
func TestForgetFeedClearsLiveState(t *testing.T) {
	s := NewSyncer(nil, testSettings())

	s.MarkSyncing(42)
	if _, ok := s.SyncingSince(42); !ok {
		t.Fatal("MarkSyncing not visible via SyncingSince")
	}
	s.ForgetFeed(42)
	if _, ok := s.SyncingSince(42); ok {
		t.Fatal("syncing entry survived ForgetFeed")
	}
	// The deleted feed's still-running sync finishing must not record an
	// attempt for the forgotten ID.
	s.unmarkSyncing(42)
	if _, ok := s.LastAttempt(42); ok {
		t.Fatal("unmarkSyncing planted a baseline for a forgotten feed")
	}
	// Normal tracked completion still records the attempt, and ForgetFeed
	// clears that too.
	s.MarkSyncing(42)
	s.unmarkSyncing(42)
	if _, ok := s.LastAttempt(42); !ok {
		t.Fatal("attempted missing after tracked sync completion")
	}
	s.ForgetFeed(42)
	if _, ok := s.LastAttempt(42); ok {
		t.Fatal("attempted entry survived ForgetFeed")
	}
	// ClearSyncing backs out an admitted sync without recording an attempt.
	s.MarkSyncing(42)
	s.ClearSyncing(42)
	if _, ok := s.LastAttempt(42); ok {
		t.Fatal("ClearSyncing recorded an attempt")
	}
}

// TestFailedSyncRecordsErrorAfterCancellation — persistSyncError must land
// even when the sync failed because its context was cancelled (shutdown):
// recording that outcome is the point of WaitBackground holding db.Close.
func TestFailedSyncRecordsErrorAfterCancellation(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "feeds.sqlite3"), false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.DB.Exec("UPDATE feeds SET enabled = 0"); err != nil {
		t.Fatal(err)
	}
	feedID, err := db.AddFeed(ctx, "cancelled", "https://example.test/feed", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB.ExecContext(ctx, "INSERT INTO catalog_mode_feeds(mode_id, feed_id) VALUES (1, ?)", feedID); err != nil {
		t.Fatal(err)
	}
	feedList, err := db.Feeds(ctx, true)
	if err != nil {
		t.Fatal(err)
	}

	syncer := NewSyncer(db, testSettings())
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := syncer.SyncOne(cancelled, feedList[0]); err == nil {
		t.Fatal("SyncOne succeeded on a cancelled context")
	}
	var lastError string
	if err := db.DB.QueryRow(
		"SELECT COALESCE(last_error, '') FROM feeds WHERE id = ?", feedID).Scan(&lastError); err != nil {
		t.Fatal(err)
	}
	if lastError == "" {
		t.Fatal("last_error empty after cancelled sync, want the cancellation recorded")
	}
}

// TestSyncOneFailurePersistsErrorBeforeAttempt — a failed sync must have
// last_error written by the syncer itself, in syncOne's body, so that by
// the time the attempt is published as finished (LastAttempt, the API's
// sync_attempted_at) the outcome is already readable in the DB. Callers
// writing last_error after SyncOne returns would race polling clients.
func TestSyncOneFailurePersistsErrorBeforeAttempt(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "feeds.sqlite3"), false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.DB.Exec("UPDATE feeds SET enabled = 0"); err != nil {
		t.Fatal(err)
	}
	feedID, err := db.AddFeed(ctx, "failing", "https://example.test/feed", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB.ExecContext(ctx, "INSERT INTO catalog_mode_feeds(mode_id, feed_id) VALUES (1, ?)", feedID); err != nil {
		t.Fatal(err)
	}
	feedList, err := db.Feeds(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	feed := feedList[0]

	syncer := NewSyncer(db, testSettings())
	syncer.Client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("connection refused")
	})}

	if _, err := syncer.SyncOne(ctx, feed); err == nil {
		t.Fatal("SyncOne succeeded, want download failure")
	}
	var lastError string
	if err := db.DB.QueryRow(
		"SELECT COALESCE(last_error, '') FROM feeds WHERE id = ?", feedID).Scan(&lastError); err != nil {
		t.Fatal(err)
	}
	if lastError == "" {
		t.Fatal("last_error empty after failed SyncOne, want the failure recorded by the syncer itself")
	}
	if _, ok := syncer.LastAttempt(feedID); !ok {
		t.Fatal("LastAttempt not recorded after failed SyncOne")
	}
}

func TestSyncDiscardsDownloadWhenFeedURLChanges(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "feeds.sqlite3"), false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.DB.Exec("UPDATE feeds SET enabled = 0"); err != nil {
		t.Fatal(err)
	}
	const oldURL = "https://example.test/old"
	const newURL = "https://example.test/new"
	feedID, err := db.AddFeed(ctx, "custom", oldURL, 1, true, 0, "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB.ExecContext(ctx, "INSERT INTO catalog_mode_feeds(mode_id, feed_id) VALUES (1, ?)", feedID); err != nil {
		t.Fatal(err)
	}
	feedList, err := db.Feeds(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	feed := feedList[0]

	syncer := NewSyncer(db, testSettings())
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
	var url, lastError string
	var lastSuccess int64
	if err := db.DB.QueryRow(`
SELECT url, COALESCE(last_success, 0), COALESCE(last_error, '')
FROM feeds WHERE id = ?`, feed.ID).Scan(&url, &lastSuccess, &lastError); err != nil {
		t.Fatal(err)
	}
	if url != newURL || lastSuccess != 0 || lastError != "" {
		t.Fatalf("feed state = url %q success %d error %q", url, lastSuccess, lastError)
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
	db, err := store.Open(filepath.Join(t.TempDir(), "feeds.sqlite3"), false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.DB.Exec("UPDATE feeds SET enabled = 0"); err != nil {
		t.Fatal(err)
	}
	feedID, err := db.AddFeed(ctx, "custom", "https://example.test/feed", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB.ExecContext(ctx, "INSERT INTO catalog_mode_feeds(mode_id, feed_id) VALUES (1, ?)", feedID); err != nil {
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

	syncer := NewSyncer(db, testSettings())
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
	db, err := store.Open(filepath.Join(t.TempDir(), "ipranges.sqlite3"), false, "", false)
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
		"UPDATE feeds SET enabled = CASE WHEN id IN (SELECT feed_id FROM catalog_mode_feeds WHERE mode_id = ?) THEN 1 ELSE 0 END",
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
	syncer := NewSyncer(db, testSettings())
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
	if _, err := syncer.SyncOne(ctx, feedList[0]); err != nil {
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
	v4IP, v4Bits, err := store.EncodePrefixString("203.0.113.0/24")
	if err != nil {
		t.Fatal(err)
	}
	v6IP, v6Bits, err := store.EncodePrefixString("2001:db8::/32")
	if err != nil {
		t.Fatal(err)
	}
	var wrongModeEntries int
	if err := db.DB.QueryRow(`
SELECT COUNT(*)
FROM catalog_entries ce
JOIN feeds f ON f.id = ce.feed_id
JOIN prefixes p ON p.id = ce.prefix_id
WHERE f.id NOT IN (SELECT feed_id FROM catalog_mode_feeds WHERE mode_id = ?)
  AND (p.ip, p.bits) IN (VALUES (?, ?), (?, ?))`,
		store.IPRangesCatalogModeID, v4IP, v4Bits, v6IP, v6Bits).Scan(&wrongModeEntries); err != nil {
		t.Fatal(err)
	}
	if wrongModeEntries != 0 {
		t.Fatalf("IPRanges entries leaked into another mode: %d", wrongModeEntries)
	}
}

func TestSyncASNFeedFetchesAnnouncedPrefixes(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "asn.sqlite3"), false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.DB.Exec("UPDATE feeds SET enabled = 0"); err != nil {
		t.Fatal(err)
	}
	var adapterID int64
	if err := db.DB.QueryRowContext(ctx,
		"SELECT id FROM feed_adapters WHERE name = ?", "ASN").Scan(&adapterID); err != nil {
		t.Fatal(err)
	}
	data := `{"asns":[15169,32934],"category":"Cloud providers","service":"Google"}`
	feedID, err := db.AddFeed(ctx, "asn-feed", "https://stat.ripe.net", adapterID, true, 0, data, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB.ExecContext(ctx, "INSERT INTO catalog_mode_feeds(mode_id, feed_id) VALUES (1, ?)", feedID); err != nil {
		t.Fatal(err)
	}
	feedList, err := db.Feeds(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(feedList) != 1 {
		t.Fatalf("enabled feeds = %#v", feedList)
	}
	feed := feedList[0]
	if feed.AllowedHosts != "stat.ripe.net" {
		t.Fatalf("allowed hosts = %q, want stat.ripe.net (merged from the builtin adapter)", feed.AllowedHosts)
	}

	syncer := NewSyncer(db, testSettings())
	syncer.Client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Query().Get("resource") {
		case "AS15169":
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"data":{"prefixes":[{"prefix":"8.8.8.0/24"},{"prefix":"8.8.4.0/24"}]}}`)),
				Header:     make(http.Header),
			}, nil
		case "AS32934":
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"data":{"prefixes":[{"prefix":"157.240.0.0/16"},{"prefix":"8.8.8.0/24"}]}}`)),
				Header:     make(http.Header),
			}, nil
		default:
			t.Fatalf("unexpected resource %q", request.URL.Query().Get("resource"))
			return nil, nil
		}
	})}
	if _, err := syncer.SyncOne(ctx, feed); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := db.DB.QueryRow(
		"SELECT COUNT(*) FROM catalog_entries WHERE feed_id = ?", feed.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	// 8.8.8.0/24 is announced by both ASNs and must be deduplicated.
	if count != 3 {
		t.Fatalf("catalog entries = %d, want 3 (deduped across both ASNs)", count)
	}
}

func TestJavaScriptASNAdapterValidatesConfig(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "asn-validate.sqlite3"), false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var adapterID int64
	if err := db.DB.QueryRowContext(ctx,
		"SELECT id FROM feed_adapters WHERE name = ?", "ASN").Scan(&adapterID); err != nil {
		t.Fatal(err)
	}
	adapter, err := db.FeedAdapter(ctx, adapterID)
	if err != nil {
		t.Fatal(err)
	}

	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("HTTP client must not be called for a config that fails validation")
		return nil, nil
	})}

	cases := []struct {
		name string
		data string
		want string
	}{
		{"empty data", "", "Data JSON is required"},
		{"invalid JSON", "{not json", "not valid JSON"},
		{"missing asns", `{"category":"c","service":"s"}`, "'asns' must be a non-empty array"},
		{"empty asns", `{"asns":[],"category":"c","service":"s"}`, "'asns' must be a non-empty array"},
		{"invalid asn", `{"asns":[-1],"category":"c","service":"s"}`, "invalid AS number"},
		{"missing category", `{"asns":[15169],"service":"s"}`, "'category' must be a non-empty string"},
		{"missing service", `{"asns":[15169],"category":"c"}`, "'service' must be a non-empty string"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := (feedRunner{limits: testLimits(), client: client, timeout: time.Second}).run(
				ctx,
				store.Feed{URL: "https://stat.ripe.net/", Data: tc.data, RestrictHosts: true, AllowedHosts: "stat.ripe.net"},
				adapter,
			)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

// Batch-boundary coverage for catalog entry insertion lives with the
// implementation now: see store.ReplaceCatalogEntries and its tests in
// internal/store/dictionaries_test.go.
