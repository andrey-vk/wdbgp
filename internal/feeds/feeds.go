package feeds

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/andrey-vk/wdbgp/internal/config"
	"github.com/andrey-vk/wdbgp/internal/logging"
	"github.com/andrey-vk/wdbgp/internal/retry"
	"github.com/andrey-vk/wdbgp/internal/store"
)

type Entry struct {
	Category string
	Service  string
	CIDR     string
}

type canonicalEntry struct {
	Category string   `json:"category"`
	Service  string   `json:"service"`
	CIDRs    []string `json:"cidrs"`
}

type openCCKEntry struct {
	Name  string   `json:"name"`
	Group string   `json:"group"`
	CIDR4 []string `json:"cidr4"`
	CIDR6 []string `json:"cidr6"`
}

type AdapterLimits struct {
	MaxSourceBytes   int
	MaxResponseBytes int
	MaxTotalBytes    int
	MaxEntries       int
	MaxRequests      int
	MaxCallStack     int
}

type Syncer struct {
	Store         *store.Store
	Client        *http.Client
	ScriptTimeout time.Duration
	Limits        AdapterLimits
	feedLocks     map[int64]*sync.Mutex
	// feedLocks serializes concurrent syncs per feed.
	// NOTE: map grows without bound as feeds are created/deleted.
	// Entries are small (~40 bytes each); with <1000 feeds this is negligible.
	feedLocksMu   sync.Mutex
}

var errFeedChanged = errors.New("feed changed during synchronization")

const ipRangesURL = "https://github.com/antonme/ipranges"

func NewSyncer(s *store.Store, cfg config.Config) *Syncer {
	timeout := cfg.JSTimeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	return &Syncer{
		Store:         s,
		Client:        newHTTPClient(timeout),
		ScriptTimeout: timeout,
		Limits: AdapterLimits{
			MaxSourceBytes:   ifZero(cfg.JSMaxSourceBytes, 1<<20),
			MaxResponseBytes: ifZero(cfg.JSMaxResponseBytes, 16<<20),
			MaxTotalBytes:    ifZero(cfg.JSMaxTotalBytes, 64<<20),
			MaxEntries:       ifZero(cfg.JSMaxEntries, 1_000_000),
			MaxRequests:      ifZero(cfg.JSMaxRequests, 200),
			MaxCallStack:     ifZero(cfg.JSMaxCallStack, 1_000),
		},
		feedLocks: make(map[int64]*sync.Mutex),
	}
}

func ifZero(val, def int) int {
	if val <= 0 {
		return def
	}
	return val
}

// RemoveFeedLock cleans up the per-feed mutex after feed deletion.
// Uses TryLock to check whether the mutex is still held: if a sync is in
// progress the entry stays in the map (acceptable small memory leak) so that
// the old sync goroutine cannot accidentally collide with a reused feed ID.
func (s *Syncer) RemoveFeedLock(feedID int64) {
	s.feedLocksMu.Lock()
	mu := s.feedLocks[feedID]
	if mu != nil {
		if mu.TryLock() {
			mu.Unlock()
			delete(s.feedLocks, feedID)
		}
		// If TryLock fails: someone holds it, skip removal
	}
	s.feedLocksMu.Unlock()
}

// TryLockFeed attempts to acquire the per-feed mutex without blocking.
// Returns the mutex and true if the lock was acquired, nil and false if
// another sync is in progress.  The caller must pass the returned mutex
// to UnlockFeed — never look it up again from the map.
// Used for double-click prevention in force-sync handlers.
func (s *Syncer) TryLockFeed(feedID int64) (*sync.Mutex, bool) {
	s.feedLocksMu.Lock()
	if s.feedLocks[feedID] == nil {
		s.feedLocks[feedID] = &sync.Mutex{}
	}
	mu := s.feedLocks[feedID]
	s.feedLocksMu.Unlock()
	if mu.TryLock() {
		return mu, true
	}
	return nil, false
}

// UnlockFeed releases the per-feed mutex previously acquired by TryLockFeed.
func (s *Syncer) UnlockFeed(mu *sync.Mutex) {
	if mu != nil {
		mu.Unlock()
	}
}

func (s *Syncer) SyncAll(ctx context.Context) []error {
	logger := logging.FromContext(ctx)
	logger.Info("starting feed synchronization")
	
	feeds, err := s.Store.Feeds(ctx, true)
	if err != nil {
		logger.Error("failed to get feeds from store", "error", err)
		return []error{err}
	}
	
	logger.Debug("found feeds to sync", "feed_count", len(feeds), "enabled_only", true)
	var errors []error
	for _, feed := range feeds {
		logger.Debug("syncing feed", "name", feed.Name, "url", feed.URL, "feed_id", feed.ID)
		executedRevision, err := s.SyncOne(ctx, feed)
		if err != nil {
			logger.Error("feed sync failed", "name", feed.Name, "error", err)
			errors = append(errors, fmt.Errorf("%s: %w", feed.Name, err))
			// Guard against adapter source changes during sync: if the
			// adapter was edited (same adapter_id, new revision) after
			// SyncOne loaded it, the error belongs to the old revision
			// and must not overwrite the new feed status.
			if executedRevision > 0 {
				_, _ = s.Store.DB.ExecContext(ctx,
					`UPDATE feeds SET last_error = ? WHERE id = ? AND url = ? AND enabled = 1 
					 AND data = ? AND adapter_id = ? AND name = ?
					 AND adapter_id IN (SELECT id FROM feed_adapters WHERE id = ? AND revision = ?)`,
					err.Error(), feed.ID, feed.URL, feed.Data, feed.AdapterID, feed.Name,
					feed.AdapterID, executedRevision)
			} else {
				_, _ = s.Store.DB.ExecContext(ctx,
					`UPDATE feeds SET last_error = ? WHERE id = ? AND url = ? AND enabled = 1 
					 AND data = ? AND adapter_id = ? AND name = ?`,
					err.Error(), feed.ID, feed.URL, feed.Data, feed.AdapterID, feed.Name)
			}
		} else {
			logger.Info("feed synced successfully", "name", feed.Name, "feed_id", feed.ID)
		}
	}
	
	if len(errors) > 0 {
		logger.Error("feed synchronization completed with errors", "error_count", len(errors))
	} else {
		logger.Info("feed synchronization completed successfully", "feed_count", len(feeds))
	}
	return errors
}

func (s *Syncer) TestAdapter(
	ctx context.Context,
	feed store.Feed,
	adapter store.FeedAdapter,
) ([]Entry, error) {
	return (adapterRunner{
		client: s.Client, timeout: s.ScriptTimeout, limits: s.Limits,
	}).run(ctx, feed, adapter)
}

// SyncOne synchronizes one feed, acquiring the per-feed lock internally.
// Returns the adapter revision that was actually used during synchronization.
func (s *Syncer) SyncOne(ctx context.Context, feed store.Feed) (int64, error) {
	s.feedLocksMu.Lock()
	if s.feedLocks[feed.ID] == nil {
		s.feedLocks[feed.ID] = &sync.Mutex{}
	}
	mu := s.feedLocks[feed.ID]
	s.feedLocksMu.Unlock()

	mu.Lock()
	defer mu.Unlock()
	return s.syncOne(ctx, feed)
}

// SyncOneLocked synchronizes one feed. The caller must already hold the
// per-feed lock (acquired via TryLockFeed).
// Returns the adapter revision that was actually used during synchronization.
func (s *Syncer) SyncOneLocked(ctx context.Context, feed store.Feed) (int64, error) {
	return s.syncOne(ctx, feed)
}

func (s *Syncer) syncOne(ctx context.Context, feed store.Feed) (int64, error) {
	logger := logging.FromContext(ctx)
	
	adapter, err := s.Store.FeedAdapter(ctx, feed.AdapterID)
	if err != nil {
		logger.Error("failed to get feed adapter", "feed_id", feed.ID, "adapter_id", feed.AdapterID, "error", err)
		return 0, err
	}
	logger.Debug("testing adapter", "feed", feed.Name, "adapter", adapter.Name, "adapter_revision", adapter.Revision)
	
	entries, err := s.TestAdapter(ctx, feed, adapter)
	if err != nil {
		logger.Error("adapter test failed", "feed", feed.Name, "adapter", adapter.Name, "error", err)
		return adapter.Revision, err
	}
	logger.Debug("adapter executed successfully", "feed", feed.Name, "entry_count", len(entries))
	err = s.Store.Transaction(ctx, func(tx *sql.Tx) error {
		var currentURL string
		var currentAdapterID int64
		var currentAdapterRevision int64
		var currentData string
		var currentName string
		var enabled bool
		if err := tx.QueryRowContext(ctx,
			`SELECT f.url, f.adapter_id, f.enabled, f.data, f.name, a.revision
			 FROM feeds f
			 JOIN feed_adapters a ON a.id = f.adapter_id
			 WHERE f.id = ?`, feed.ID).
			Scan(
				&currentURL, &currentAdapterID, &enabled,
				&currentData, &currentName,
				&currentAdapterRevision,
			); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errFeedChanged
			}
			return err
		}
		if currentURL != feed.URL ||
			currentAdapterID != feed.AdapterID ||
			currentAdapterRevision != adapter.Revision ||
			currentData != feed.Data ||
			currentName != feed.Name ||
			!enabled {
			return errFeedChanged
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM catalog_entries WHERE feed_id = ?", feed.ID); err != nil {
			return err
		}
		statement, err := tx.PrepareContext(ctx,
			"INSERT INTO catalog_entries(feed_id, category, service, cidr) VALUES (?, ?, ?, ?)")
		if err != nil {
			return err
		}
		defer func() {
			if err := statement.Close(); err != nil {
				log.Printf("DEBUG: close statement: %v", err)
			}
		}()
		for _, entry := range entries {
			if _, err := statement.ExecContext(ctx,
				feed.ID, entry.Category, entry.Service, entry.CIDR); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx,
			"UPDATE feeds SET last_success = ?, last_error = NULL WHERE id = ? AND url = ? AND enabled = 1",
			time.Now().UTC().Format(time.RFC3339Nano), feed.ID, feed.URL)
		return err
	})
	if errors.Is(err, errFeedChanged) {
		return adapter.Revision, nil
	}
	if err != nil {
		return adapter.Revision, err
	}
	// Generate communities for newly added categories/services across ALL modes the feed belongs to.
	modeIDs, modeErr := s.Store.FeedModes(ctx, feed.ID)
	if modeErr != nil {
		logger.Warn("failed to get feed modes for community gen", "feed_id", feed.ID, "error", modeErr)
	} else {
		for _, mid := range modeIDs {
			if _, genErr := s.Store.GenerateCommunities(ctx, mid); genErr != nil {
				logger.Warn("failed to generate communities after sync", "mode_id", mid, "error", genErr)
			}
		}
	}
	return adapter.Revision, nil
}

func isLegacyOpenCCKFeedCategory(category string) bool {
	switch category {
	case "opencck-main", "opencck-beta", "opencck-main-v4", "opencck-beta-v4",
		"opencck-main-v6", "opencck-beta-v6":
		return true
	default:
		return false
	}
}

func (s *Syncer) download(ctx context.Context, rawURL string) ([]byte, error) {
	// Use retry with exponential backoff for feed downloads
	result, err := retry.DoWithResult(ctx, retry.HTTPConfig,
		func() ([]byte, error) {
			return s.doDownload(ctx, rawURL)
		},
		retry.HTTPTransientError,
	)
	
	if err != nil {
		// Check if it's a context error
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("feed download timeout for %s: %w", rawURL, err)
		}
		return nil, fmt.Errorf("feed download failed for %s: %w", rawURL, err)
	}
	
	return result, nil
}

func (s *Syncer) doDownload(ctx context.Context, rawURL string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "wdbgp-go/1.0")
	
	response, err := s.Client.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			log.Printf("DEBUG: close response body: %v", err)
		}
	}()
	
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %s", response.Status)
	}
	
	return io.ReadAll(response.Body)
}

func MetadataURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Hostname() != "iplist.opencck.org" &&
		parsed.Hostname() != "beta.iplist.opencck.org") {
		return rawURL
	}
	query := parsed.Query()
	if data := query.Get("data"); data != "cidr4" && data != "cidr6" {
		return rawURL
	}
	query.Set("data", "group")
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func Parse(payload []byte, lookup map[string][]string, defaultCategory string) ([]Entry, error) {
	var raw any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil, err
	}
	var entries []Entry
	switch value := raw.(type) {
	case map[string]any:
		if wrapped, ok := value["entries"]; ok {
			items, ok := wrapped.([]any)
			if !ok {
				return nil, fmt.Errorf("entries must be an array")
			}
			var err error
			entries, err = parseCanonical(items)
			if err != nil {
				return nil, err
			}
		} else if isOpenCCKFull(value) {
			var err error
			entries, err = parseOpenCCKFull(payload)
			if err != nil {
				return nil, err
			}
		} else if isOpenCCKFlat(value) {
			var err error
			entries, err = parseOpenCCKFlat(value, lookup, defaultCategory)
			if err != nil {
				return nil, err
			}
		} else {
			one, err := parseCanonical([]any{value})
			if err != nil {
				return nil, err
			}
			entries = one
		}
	case []any:
		var err error
		entries, err = parseCanonical(value)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported feed JSON shape")
	}
	return deduplicate(entries), nil
}

func parseCanonical(items []any) ([]Entry, error) {
	var entries []Entry
	for _, item := range items {
		encoded, _ := json.Marshal(item)
		var value canonicalEntry
		if err := json.Unmarshal(encoded, &value); err != nil {
			return nil, err
		}
		value.Category = strings.TrimSpace(value.Category)
		value.Service = strings.TrimSpace(value.Service)
		if value.Category == "" || value.Service == "" || value.CIDRs == nil {
			return nil, fmt.Errorf("each entry requires category, service and cidrs[]")
		}
		for _, cidr := range value.CIDRs {
			normalized, err := normalize(cidr)
			if err != nil {
				return nil, err
			}
			entries = append(entries, Entry{value.Category, value.Service, normalized})
		}
	}
	return entries, nil
}

func isOpenCCKFull(value map[string]any) bool {
	if len(value) == 0 {
		return false
	}
	for _, raw := range value {
		entry, ok := raw.(map[string]any)
		if !ok {
			return false
		}
		if _, ok := entry["group"].(string); !ok {
			return false
		}
		if _, ok := entry["cidr4"].([]any); !ok {
			return false
		}
	}
	return true
}

func parseOpenCCKFull(payload []byte) ([]Entry, error) {
	var values map[string]openCCKEntry
	if err := json.Unmarshal(payload, &values); err != nil {
		return nil, err
	}
	var entries []Entry
	for key, value := range values {
		service := strings.TrimSpace(value.Name)
		if service == "" {
			service = strings.TrimSpace(key)
		}
		category := strings.TrimSpace(value.Group)
		if service == "" || category == "" {
			return nil, fmt.Errorf("OpenCCK entry requires group and name")
		}
		for _, cidr := range append(value.CIDR4, value.CIDR6...) {
			normalized, err := normalize(cidr)
			if err != nil {
				return nil, err
			}
			entries = append(entries, Entry{category, service, normalized})
		}
	}
	return entries, nil
}

func isOpenCCKFlat(value map[string]any) bool {
	if len(value) == 0 {
		return false
	}
	for key, raw := range value {
		if key == "" {
			return false
		}
		if _, ok := raw.([]any); !ok {
			return false
		}
	}
	return true
}

func parseOpenCCKFlat(value map[string]any, lookup map[string][]string, defaultCategory string) ([]Entry, error) {
	if defaultCategory == "" {
		return nil, fmt.Errorf("flat OpenCCK CIDR data requires a default category")
	}
	var entries []Entry
	for service, rawCIDRs := range value {
		categories := lookup[service]
		if len(categories) == 0 {
			categories = []string{defaultCategory}
		}
		rawCIDRList, ok := rawCIDRs.([]any)
		if !ok {
			return nil, fmt.Errorf("CIDR list must be an array")
		}
		for _, rawCIDR := range rawCIDRList {
			cidr, ok := rawCIDR.(string)
			if !ok {
				return nil, fmt.Errorf("CIDR must be a string")
			}
			normalized, err := normalize(cidr)
			if err != nil {
				return nil, err
			}
			for _, category := range categories {
				entries = append(entries, Entry{category, service, normalized})
			}
		}
	}
	return entries, nil
}

func normalize(value string) (string, error) {
	prefix, err := netip.ParsePrefix(strings.TrimSpace(value))
	if err != nil {
		return "", err
	}
	return prefix.Masked().String(), nil
}

func deduplicate(entries []Entry) []Entry {
	unique := map[string]Entry{}
	for _, entry := range entries {
		key := entry.Category + "\x00" + entry.Service + "\x00" + entry.CIDR
		unique[key] = entry
	}
	result := make([]Entry, 0, len(unique))
	for _, entry := range unique {
		result = append(result, entry)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Category != result[j].Category {
			return result[i].Category < result[j].Category
		}
		if result[i].Service != result[j].Service {
			return result[i].Service < result[j].Service
		}
		return result[i].CIDR < result[j].CIDR
	})
	return result
}
