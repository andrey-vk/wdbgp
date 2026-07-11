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

	"github.com/andrey-vk/wdbgp/internal/logging"
	"github.com/andrey-vk/wdbgp/internal/retry"
	"github.com/andrey-vk/wdbgp/internal/settings"
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
	feedLocksMu sync.Mutex

	// syncing tracks in-flight syncs (feed ID → start time) so the API can
	// report live per-feed status; entries exist only while syncOne runs.
	// attempted records when the last attempt for a feed *finished*
	// (success or failure) — the SPA detects sync completion by watching
	// this value change, which stays correct even when a failed retry
	// rewrites last_error with the identical text. In-memory only: resets
	// on restart, grows like feedLocks (negligible, see note above).
	syncing   map[int64]time.Time
	attempted map[int64]time.Time
	syncingMu sync.Mutex
}

var errFeedChanged = errors.New("feed changed during synchronization")

const ipRangesURL = "https://github.com/antonme/ipranges"

func NewSyncer(s *store.Store, st *settings.Settings) *Syncer {
	timeout := time.Duration(st.JSTimeout.Get()) * time.Second
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	return &Syncer{
		Store:         s,
		Client:        newHTTPClient(timeout),
		ScriptTimeout: timeout,
		Limits: AdapterLimits{
			MaxSourceBytes:   ifZero(st.JSMaxSourceBytes.Get(), 1<<20),
			MaxResponseBytes: ifZero(st.JSMaxResponseBytes.Get(), 16<<20),
			MaxTotalBytes:    ifZero(st.JSMaxTotalBytes.Get(), 64<<20),
			MaxEntries:       ifZero(st.JSMaxEntries.Get(), 1_000_000),
			MaxRequests:      ifZero(st.JSMaxRequests.Get(), 200),
			MaxCallStack:     ifZero(st.JSMaxCallStack.Get(), 1_000),
		},
		feedLocks: make(map[int64]*sync.Mutex),
		syncing:   make(map[int64]time.Time),
		attempted: make(map[int64]time.Time),
	}
}

func ifZero(val, def int) int {
	if val <= 0 {
		return def
	}
	return val
}

// ForgetFeed cleans up the syncer's per-feed state after feed deletion, so
// a later feed reusing the SQLite rowid doesn't inherit live status or a
// completion baseline. The mutex uses TryLock to check whether it is still
// held: if a sync is in progress the entry stays in the map (acceptable
// small memory leak) so that the old sync goroutine cannot accidentally
// collide with a reused feed ID — and unmarkSyncing skips the attempted
// bump for feeds no longer tracked in syncing, so that in-flight sync
// can't re-plant a stale baseline either.
func (s *Syncer) ForgetFeed(feedID int64) {
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
	s.syncingMu.Lock()
	delete(s.syncing, feedID)
	delete(s.attempted, feedID)
	s.syncingMu.Unlock()
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
		_, err := s.SyncOne(ctx, feed)
		if err != nil {
			// last_error is already persisted by syncOne, before it
			// published the attempt as finished.
			logger.Error("feed sync failed", "name", feed.Name, "error", err)
			errors = append(errors, fmt.Errorf("%s: %w", feed.Name, err))
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
	return (feedRunner{
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

// SyncingSince reports whether a sync for the feed is currently running,
// and when it started.
func (s *Syncer) SyncingSince(feedID int64) (time.Time, bool) {
	s.syncingMu.Lock()
	defer s.syncingMu.Unlock()
	started, ok := s.syncing[feedID]
	return started, ok
}

// SyncingFeeds returns a snapshot of all in-flight syncs (feed ID → start
// time), for decorating list responses without locking per feed.
func (s *Syncer) SyncingFeeds() map[int64]time.Time {
	s.syncingMu.Lock()
	defer s.syncingMu.Unlock()
	snapshot := make(map[int64]time.Time, len(s.syncing))
	for id, started := range s.syncing {
		snapshot[id] = started
	}
	return snapshot
}

// LastAttempt reports when the last sync attempt for the feed finished
// (success or failure) since process start.
func (s *Syncer) LastAttempt(feedID int64) (time.Time, bool) {
	s.syncingMu.Lock()
	defer s.syncingMu.Unlock()
	finished, ok := s.attempted[feedID]
	return finished, ok
}

// AttemptedFeeds returns a snapshot of when the last sync attempt per feed
// finished (success or failure) since process start.
func (s *Syncer) AttemptedFeeds() map[int64]time.Time {
	s.syncingMu.Lock()
	defer s.syncingMu.Unlock()
	snapshot := make(map[int64]time.Time, len(s.attempted))
	for id, finished := range s.attempted {
		snapshot[id] = finished
	}
	return snapshot
}

// MarkSyncing registers the feed as having a sync in flight. syncOne calls
// it on entry; the manual-sync handler additionally calls it synchronously
// after acquiring the feed lock, so a concurrent sync-all admission check
// (SyncingFeeds) can't miss a sync whose goroutine hasn't started yet.
func (s *Syncer) MarkSyncing(feedID int64) {
	s.syncingMu.Lock()
	s.syncing[feedID] = time.Now()
	s.syncingMu.Unlock()
}

// ClearSyncing removes the in-flight marker WITHOUT recording a finished
// attempt — for backing out an admitted-but-never-started manual sync.
// Bumping attempted here would falsely fire completion watches.
func (s *Syncer) ClearSyncing(feedID int64) {
	s.syncingMu.Lock()
	delete(s.syncing, feedID)
	s.syncingMu.Unlock()
}

func (s *Syncer) unmarkSyncing(feedID int64) {
	s.syncingMu.Lock()
	// Only feeds still tracked get an attempted timestamp: if the feed was
	// deleted mid-sync (ForgetFeed cleared it), recording the attempt would
	// plant a stale completion baseline on a later feed reusing the rowid.
	if _, tracked := s.syncing[feedID]; tracked {
		s.attempted[feedID] = time.Now()
	}
	delete(s.syncing, feedID)
	s.syncingMu.Unlock()
}

// persistSyncError records a failed sync in feeds.last_error, guarded so
// that a feed edited mid-sync doesn't receive an error belonging to its old
// configuration. When executedRevision > 0 the write additionally requires
// the adapter revision to be unchanged: if the adapter was edited (same
// adapter_id, new revision) after the sync loaded it, the error belongs to
// the old revision and must not overwrite the new feed status.
func (s *Syncer) persistSyncError(ctx context.Context, feed store.Feed, executedRevision int64, syncErr error) {
	// The write must survive cancellation: at shutdown the app context that
	// cancelled the sync IS the error being recorded, and WaitBackground
	// holds db.Close open precisely so this lands. Detach, keep a bound.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	query := `UPDATE feeds SET last_error = ? WHERE id = ? AND url = ? AND enabled = 1
		 AND data = ? AND adapter_id = ? AND name = ?`
	args := []any{syncErr.Error(), feed.ID, feed.URL, feed.Data, feed.AdapterID, feed.Name}
	if executedRevision > 0 {
		query += ` AND adapter_id IN (SELECT id FROM feed_adapters WHERE id = ? AND revision = ?)`
		args = append(args, feed.AdapterID, executedRevision)
	}
	if _, err := s.Store.DB.ExecContext(ctx, query, args...); err != nil {
		log.Printf("WARNING: cleanup: %v", err)
	}
}

func (s *Syncer) syncOne(ctx context.Context, feed store.Feed) (int64, error) {
	s.MarkSyncing(feed.ID)
	// unmarkSyncing publishes the attempt as finished (sync_attempted_at),
	// which the SPA takes as "the outcome is now readable" — so every
	// last_error/last_success write must happen in the function body, before
	// the defer runs. That's why failures are persisted here and not by the
	// callers.
	defer s.unmarkSyncing(feed.ID)

	logger := logging.FromContext(ctx)

	adapter, err := s.Store.FeedAdapter(ctx, feed.AdapterID)
	if err != nil {
		logger.Error("failed to get feed adapter", "feed_id", feed.ID, "adapter_id", feed.AdapterID, "error", err)
		s.persistSyncError(ctx, feed, 0, err)
		return 0, err
	}
	logger.Debug("testing adapter", "feed", feed.Name, "adapter", adapter.Name, "adapter_revision", adapter.Revision)

	entries, err := s.TestAdapter(ctx, feed, adapter)
	if err != nil {
		logger.Error("adapter test failed", "feed", feed.Name, "adapter", adapter.Name, "error", err)
		s.persistSyncError(ctx, feed, adapter.Revision, err)
		return adapter.Revision, err
	}
	logger.Debug("adapter executed successfully", "feed", feed.Name, "entry_count", len(entries))
	err = s.Store.Transaction(ctx, func(tx *sql.Tx) error {
		var currentURL string
		var currentAdapterID int64
		var currentAdapterRevision int64
		var currentData string
		var currentName string
		var currentAllowedHosts string
		var currentRestrictHosts bool
		var enabled bool
		if err := tx.QueryRowContext(ctx,
			`SELECT f.url, f.adapter_id, f.enabled, f.data, f.name,
			        f.allowed_hosts, f.restrict_hosts,
			        a.revision
			 FROM feeds f
			 JOIN feed_adapters a ON a.id = f.adapter_id
			 WHERE f.id = ?`, feed.ID).
			Scan(
				&currentURL, &currentAdapterID, &enabled,
				&currentData, &currentName,
				&currentAllowedHosts, &currentRestrictHosts,
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
			currentAllowedHosts != feed.AllowedHosts ||
			currentRestrictHosts != feed.RestrictHosts ||
			!enabled {
			return errFeedChanged
		}
		if err := store.ReplaceCatalogEntries(ctx, tx, feed.ID, toCatalogEntries(entries)); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx,
			"UPDATE feeds SET last_success = ?, last_error = NULL WHERE id = ? AND url = ? AND enabled = 1",
			time.Now().Unix(), feed.ID, feed.URL)
		return err
	})
	if errors.Is(err, errFeedChanged) {
		return adapter.Revision, nil
	}
	if err != nil {
		s.persistSyncError(ctx, feed, adapter.Revision, err)
		return adapter.Revision, err
	}
	// The feed's contribution changed — rebuild the materialized merge of
	// every mode linking it (include or exclude role). Must run before the
	// orphan prune: the rebuild interns split fragments into prefixes.
	if rebuildErr := s.Store.RebuildModeEntriesForFeed(ctx, feed.ID); rebuildErr != nil {
		logger.Warn("failed to rebuild mode entries after sync", "feed_id", feed.ID, "error", rebuildErr)
	}
	// A resync replaces the feed's entries wholesale, so prefixes that
	// dropped out of the feed may no longer be referenced by anything.
	if pruned, pruneErr := s.Store.PruneOrphanPrefixes(ctx); pruneErr != nil {
		logger.Warn("failed to prune orphan prefixes after sync", "feed_id", feed.ID, "error", pruneErr)
	} else if pruned > 0 {
		logger.Debug("pruned orphan prefixes", "feed", feed.Name, "count", pruned)
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

// toCatalogEntries converts adapter entries to the store's external
// catalog-entry form; the store resolves names and CIDRs onto its
// dictionary tables internally (batched, see store.ReplaceCatalogEntries).
func toCatalogEntries(entries []Entry) []store.CatalogEntry {
	result := make([]store.CatalogEntry, len(entries))
	for i, entry := range entries {
		result[i] = store.CatalogEntry{
			Category: entry.Category,
			Service:  entry.Service,
			CIDR:     entry.CIDR,
		}
	}
	return result
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
		} else if isOpenCCKFull(value) { //nolint:gocritic // if/else chain is for SRS format detection, switch doesn't improve
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
		encoded, err := json.Marshal(item)
		if err != nil {
			log.Printf("WARNING: json marshal item: %v", err)
			continue
		}
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
