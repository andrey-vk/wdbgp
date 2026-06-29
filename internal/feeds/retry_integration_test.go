package feeds

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/andrey-vk/wdbgp/internal/store"
)

func TestJavaScriptAdapter_HTTPRetry(t *testing.T) {
	callCount := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		callCount++
		if callCount < 3 {
			// Simulate transient failure - use error message that will be recognized
			return nil, &mockNetError{
				timeout:   true,
				temporary: true,
				message:   "connection timeout",
			}
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`["149.154.167.99/20"]`)),
			Header:     make(http.Header),
		}, nil
	})}

	entries, err := (feedRunner{limits: testLimits(), client: client, timeout: 5 * time.Second}).run(
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
		t.Fatalf("expected success after retry, got error: %v", err)
	}
	if callCount != 3 {
		t.Fatalf("expected 3 HTTP calls (2 failures + 1 success), got %d", callCount)
	}
	if len(entries) != 1 || entries[0].CIDR != "149.154.160.0/20" {
		t.Fatalf("unexpected entries: %#v", entries)
	}
}

func TestDownloadWithRetry(t *testing.T) {
	callCount := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		callCount++
		if callCount < 2 {
			// Simulate transient failure
			return nil, &mockNetError{
				timeout:   true,
				temporary: true,
				message:   "connection timeout",
			}
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"test": "data"}`)),
			Header:     make(http.Header),
		}, nil
	})}

	syncer := &Syncer{Client: client}
	payload, err := syncer.download(context.Background(), "https://example.test/data")

	if err != nil {
		t.Fatalf("expected success after retry, got error: %v", err)
	}
	if callCount != 2 {
		t.Fatalf("expected 2 HTTP calls (1 failure + 1 success), got %d", callCount)
	}
	if string(payload) != `{"test": "data"}` {
		t.Fatalf("unexpected payload: %s", payload)
	}
}

func TestDownloadRetryFailure(t *testing.T) {
	callCount := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		callCount++
		// Always fail
		return nil, &mockNetError{
			timeout:   true,
			temporary: true,
			message:   "connection timeout",
		}
	})}

	syncer := &Syncer{Client: client}
	_, err := syncer.download(context.Background(), "https://example.test/data")

	if err == nil {
		t.Fatal("expected error after max retries")
	}
	if !strings.Contains(err.Error(), "feed download failed") {
		t.Fatalf("expected download error, got: %v", err)
	}
	if callCount < 2 {
		t.Fatalf("expected multiple retry attempts, got %d calls", callCount)
	}
}

// mockNetError implements net.Error for testing
type mockNetError struct {
	timeout   bool
	temporary bool
	message   string
}

func (e *mockNetError) Error() string {
	if e.message != "" {
		return e.message
	}
	return "mock network error"
}

func (e *mockNetError) Timeout() bool   { return e.timeout }
func (e *mockNetError) Temporary() bool { return e.temporary }
