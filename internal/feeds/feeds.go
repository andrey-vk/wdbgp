package feeds

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"time"

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

type Syncer struct {
	Store  *store.Store
	Client *http.Client
}

var errFeedChanged = errors.New("feed changed during synchronization")

func NewSyncer(s *store.Store) *Syncer {
	return &Syncer{
		Store:  s,
		Client: &http.Client{Timeout: 120 * time.Second},
	}
}

func (s *Syncer) SyncAll(ctx context.Context) []error {
	feeds, err := s.Store.Feeds(ctx, true)
	if err != nil {
		return []error{err}
	}
	var errors []error
	for _, feed := range feeds {
		if err := s.syncOne(ctx, feed); err != nil {
			errors = append(errors, fmt.Errorf("%s: %w", feed.Name, err))
			_, _ = s.Store.DB.ExecContext(ctx,
				"UPDATE feeds SET last_error = ? WHERE id = ? AND url = ? AND enabled = 1",
				err.Error(), feed.ID, feed.URL)
		}
	}
	return errors
}

func (s *Syncer) syncOne(ctx context.Context, feed store.Feed) error {
	lookup, err := s.categoryLookup(ctx, feed)
	if err != nil {
		return err
	}
	payload, err := s.download(ctx, feed.URL)
	if err != nil {
		return err
	}
	entries, err := Parse(payload, lookup, feed.Name)
	if err != nil {
		return err
	}
	err = s.Store.Transaction(ctx, func(tx *sql.Tx) error {
		var currentURL string
		var enabled bool
		if err := tx.QueryRowContext(ctx,
			"SELECT url, enabled FROM feeds WHERE id = ?", feed.ID).
			Scan(&currentURL, &enabled); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errFeedChanged
			}
			return err
		}
		if currentURL != feed.URL || !enabled {
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
		defer statement.Close()
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
		return nil
	}
	return err
}

func (s *Syncer) categoryLookup(ctx context.Context, feed store.Feed) (map[string][]string, error) {
	lookup := map[string][]string{}
	rows, err := s.Store.DB.QueryContext(ctx, `
SELECT DISTINCT ce.category, ce.service
FROM catalog_entries ce
JOIN feeds f ON f.id = ce.feed_id
WHERE ce.feed_id != ? AND f.enabled = 1`, feed.ID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var category, service string
		if err := rows.Scan(&category, &service); err != nil {
			rows.Close()
			return nil, err
		}
		if isLegacyOpenCCKFeedCategory(category) {
			continue
		}
		lookup[service] = appendUnique(lookup[service], category)
	}
	rows.Close()

	metadata := MetadataURL(feed.URL)
	if metadata == feed.URL {
		return lookup, nil
	}
	payload, err := s.download(ctx, metadata)
	if err != nil {
		return nil, err
	}
	var groups map[string]string
	if err := json.Unmarshal(payload, &groups); err != nil {
		return nil, fmt.Errorf("parse OpenCCK group metadata: %w", err)
	}
	for service, category := range groups {
		if service == "" || category == "" {
			return nil, fmt.Errorf("OpenCCK group response contains an empty service or category")
		}
		lookup[service] = appendUnique(lookup[service], category)
	}
	return lookup, nil
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
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "wdbgp-go/1.0")
	response, err := s.Client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
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
		for _, rawCIDR := range rawCIDRs.([]any) {
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

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
