package store

import (
	"context"
	"database/sql"
	"errors"
	"net/netip"
	"sort"
	"time"
)

// feedSyncChangeRetention bounds the per-feed sync history kept for the
// admin view, so the table can't grow with every sync interval.
const feedSyncChangeRetention = 50

// userFeedChangeWindow bounds how far back the user-facing note looks, so a
// user who has never opened their page isn't shown every change since the
// feed existed.
const userFeedChangeWindow = 14 * 24 * time.Hour

// FeedSyncDiff is what one sync changed for a feed, relative to the entries
// it replaced. Associations are the full category/service/prefix triples, so
// a feed that only moves prefixes between services still registers a change.
type FeedSyncDiff struct {
	AddedServices       int
	RemovedServices     int
	AddedPrefixes       int
	RemovedPrefixes     int
	AddedAssociations   int
	RemovedAssociations int
	AddedByCategory     map[string]int
	// AddedServiceKeys and RemovedServiceKeys name the services behind the
	// counts above, sorted, so a user's change log can say which ones moved.
	AddedServiceKeys   []ServiceKey
	RemovedServiceKeys []ServiceKey
}

func (d FeedSyncDiff) HasChanges() bool {
	return d.AddedServices+d.RemovedServices+d.AddedPrefixes+d.RemovedPrefixes+
		d.AddedAssociations+d.RemovedAssociations > 0
}

type serviceIdentity struct{ category, service string }

type associationIdentity struct {
	category, service string
	prefix            netip.Prefix
}

// DiffCatalogEntries compares a feed's previous and new entries by service
// (category, name), by distinct masked prefix, and by full association.
func DiffCatalogEntries(prev, next []CatalogEntry) FeedSyncDiff {
	prevServices, nextServices := map[serviceIdentity]bool{}, map[serviceIdentity]bool{}
	prevPrefixes, nextPrefixes := map[netip.Prefix]bool{}, map[netip.Prefix]bool{}
	prevAssoc, nextAssoc := map[associationIdentity]bool{}, map[associationIdentity]bool{}
	for _, e := range prev {
		prevServices[serviceIdentity{e.Category, e.Service}] = true
		if p, err := netip.ParsePrefix(e.CIDR); err == nil {
			prevPrefixes[p.Masked()] = true
			prevAssoc[associationIdentity{e.Category, e.Service, p.Masked()}] = true
		}
	}
	for _, e := range next {
		nextServices[serviceIdentity{e.Category, e.Service}] = true
		if p, err := netip.ParsePrefix(e.CIDR); err == nil {
			nextPrefixes[p.Masked()] = true
			nextAssoc[associationIdentity{e.Category, e.Service, p.Masked()}] = true
		}
	}
	d := FeedSyncDiff{AddedByCategory: map[string]int{}}
	for s := range nextServices {
		if !prevServices[s] {
			d.AddedServices++
			d.AddedByCategory[s.category]++
			d.AddedServiceKeys = append(d.AddedServiceKeys, ServiceKey{Category: s.category, Service: s.service})
		}
	}
	for s := range prevServices {
		if !nextServices[s] {
			d.RemovedServices++
			d.RemovedServiceKeys = append(d.RemovedServiceKeys, ServiceKey{Category: s.category, Service: s.service})
		}
	}
	sortServiceKeys(d.AddedServiceKeys)
	sortServiceKeys(d.RemovedServiceKeys)
	for p := range nextPrefixes {
		if !prevPrefixes[p] {
			d.AddedPrefixes++
		}
	}
	for p := range prevPrefixes {
		if !nextPrefixes[p] {
			d.RemovedPrefixes++
		}
	}
	for a := range nextAssoc {
		if !prevAssoc[a] {
			d.AddedAssociations++
		}
	}
	for a := range prevAssoc {
		if !nextAssoc[a] {
			d.RemovedAssociations++
		}
	}
	return d
}

func sortServiceKeys(keys []ServiceKey) {
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Category != keys[j].Category {
			return keys[i].Category < keys[j].Category
		}
		return keys[i].Service < keys[j].Service
	})
}

// CatalogEntriesForFeedTx reads a feed's current entries in the same form
// ReplaceCatalogEntries accepts, so a sync can diff before replacing them.
func CatalogEntriesForFeedTx(ctx context.Context, tx *sql.Tx, feedID int64) ([]CatalogEntry, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT c.name, sv.name, p.ip, p.bits
FROM catalog_entries ce
JOIN services sv ON sv.id = ce.service_id
JOIN categories c ON c.id = sv.category_id
JOIN prefixes p ON p.id = ce.prefix_id
WHERE ce.feed_id = ?`, feedID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck
	var entries []CatalogEntry
	for rows.Next() {
		var category, service string
		var ip []byte
		var bits int
		if err := rows.Scan(&category, &service, &ip, &bits); err != nil {
			return nil, err
		}
		prefix, err := DecodePrefix(ip, bits)
		if err != nil {
			return nil, err
		}
		entries = append(entries, CatalogEntry{Category: category, Service: service, CIDR: prefix.String()})
	}
	return entries, rows.Err()
}

// RecordFeedSyncChangeTx stores one sync's diff and trims the feed's history
// to the retention bound, inside the sync's own transaction.
func RecordFeedSyncChangeTx(ctx context.Context, tx *sql.Tx, feedID int64, diff FeedSyncDiff, syncedAt int64) error {
	res, err := tx.ExecContext(ctx, `
INSERT INTO feed_sync_changes(feed_id, synced_at, added_services, removed_services, added_prefixes, removed_prefixes,
	added_associations, removed_associations, feed_name)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, COALESCE((SELECT name FROM feeds WHERE id = ?), ''))`,
		feedID, syncedAt, diff.AddedServices, diff.RemovedServices, diff.AddedPrefixes, diff.RemovedPrefixes,
		diff.AddedAssociations, diff.RemovedAssociations, feedID)
	if err != nil {
		return err
	}
	changeID, err := res.LastInsertId()
	if err != nil {
		return err
	}
	for category, n := range diff.AddedByCategory {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO feed_sync_change_categories(change_id, category, added_services) VALUES (?, ?, ?)",
			changeID, category, n); err != nil {
			return err
		}
	}
	for kind, keys := range map[string][]ServiceKey{"added": diff.AddedServiceKeys, "removed": diff.RemovedServiceKeys} {
		for _, k := range keys {
			if _, err := tx.ExecContext(ctx,
				"INSERT INTO feed_sync_change_services(change_id, kind, category, service) VALUES (?, ?, ?, ?)",
				changeID, kind, k.Category, k.Service); err != nil {
				return err
			}
		}
	}
	// The modes that include this feed right now are the ones this sync reaches,
	// since the catalog it publishes is what those modes serve.
	if _, err := tx.ExecContext(ctx, `
INSERT INTO feed_sync_change_modes(change_id, mode_id, mode_name)
SELECT ?, cmf.mode_id, m.name FROM catalog_mode_feeds cmf
JOIN catalog_modes m ON m.id = cmf.mode_id AND m.enabled = 1
WHERE cmf.feed_id = ? AND cmf.exclude = 0`, changeID, feedID); err != nil {
		return err
	}
	const stale = `SELECT id FROM feed_sync_changes WHERE feed_id = ?1 AND id NOT IN (
		SELECT id FROM feed_sync_changes WHERE feed_id = ?1 ORDER BY synced_at DESC, id DESC LIMIT ?2)`
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM feed_sync_change_categories WHERE change_id IN ("+stale+")", feedID, feedSyncChangeRetention); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM feed_sync_change_services WHERE change_id IN ("+stale+")", feedID, feedSyncChangeRetention); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM feed_sync_change_modes WHERE change_id IN ("+stale+")", feedID, feedSyncChangeRetention); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "DELETE FROM feed_sync_changes WHERE id IN ("+stale+")", feedID, feedSyncChangeRetention)
	return err
}

type FeedSyncCategory struct {
	Category      string `json:"category"`
	AddedServices int    `json:"added_services"`
}

type FeedSyncChange struct {
	ID                  int64              `json:"change_id"`
	SyncedAt            int64              `json:"synced_at"`
	AddedServices       int                `json:"added_services"`
	RemovedServices     int                `json:"removed_services"`
	AddedPrefixes       int                `json:"added_prefixes"`
	RemovedPrefixes     int                `json:"removed_prefixes"`
	AddedAssociations   int                `json:"added_associations"`
	RemovedAssociations int                `json:"removed_associations"`
	Categories          []FeedSyncCategory `json:"categories"`
}

// RecentFeedSyncChanges returns a feed's most recent sync changes, newest first.
func (s *Store) RecentFeedSyncChanges(ctx context.Context, feedID int64, limit int) ([]FeedSyncChange, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT id, synced_at, added_services, removed_services, added_prefixes, removed_prefixes,
	added_associations, removed_associations
FROM feed_sync_changes WHERE feed_id = ?
ORDER BY synced_at DESC, id DESC LIMIT ?`, feedID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck
	var out []FeedSyncChange
	var ids []int64
	for rows.Next() {
		var c FeedSyncChange
		if err := rows.Scan(&c.ID, &c.SyncedAt, &c.AddedServices, &c.RemovedServices, &c.AddedPrefixes, &c.RemovedPrefixes,
			&c.AddedAssociations, &c.RemovedAssociations); err != nil {
			return nil, err
		}
		c.Categories = []FeedSyncCategory{}
		out = append(out, c)
		ids = append(ids, c.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, id := range ids {
		cats, err := s.DB.QueryContext(ctx,
			"SELECT category, added_services FROM feed_sync_change_categories WHERE change_id = ? ORDER BY added_services DESC, category", id)
		if err != nil {
			return nil, err
		}
		for cats.Next() {
			var cat FeedSyncCategory
			if err := cats.Scan(&cat.Category, &cat.AddedServices); err != nil {
				_ = cats.Close() //nolint:errcheck
				return nil, err
			}
			out[i].Categories = append(out[i].Categories, cat)
		}
		if err := cats.Err(); err != nil {
			_ = cats.Close() //nolint:errcheck
			return nil, err
		}
		_ = cats.Close() //nolint:errcheck
	}
	return out, nil
}

// UserFeedChange is services a feed update added to categories the user has
// selected, in their current mode. A selected category includes all of its
// services, so these may add routes, or be already covered by another feed.
type UserFeedChange struct {
	ChangeID   int64                    `json:"change_id"`
	ModeID     int64                    `json:"mode_id"`
	FeedName   string                   `json:"feed_name"`
	SyncedAt   int64                    `json:"synced_at"`
	Categories []UserFeedChangeCategory `json:"categories"`
}

type UserFeedChangeCategory struct {
	Category      string `json:"category"`
	AddedServices int    `json:"added_services"`
}

// UserFeedChanges returns the feed updates the user hasn't acknowledged in their
// current mode (within userFeedChangeWindow), oldest first.
func (s *Store) UserFeedChanges(ctx context.Context, userID int64, now time.Time) ([]UserFeedChange, error) {
	var modeID int64
	if err := s.DB.QueryRowContext(ctx,
		"SELECT catalog_mode_id FROM users WHERE id = ?", userID).Scan(&modeID); err != nil {
		return nil, err
	}
	var seenID int64
	err := s.DB.QueryRowContext(ctx,
		"SELECT change_id FROM user_feed_changes_seen WHERE user_id = ? AND mode_id = ?", userID, modeID).Scan(&seenID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	since := now.Add(-userFeedChangeWindow).Unix()
	rows, err := s.DB.QueryContext(ctx, `
SELECT f.name, c.id, c.synced_at, fc.category, fc.added_services
FROM feed_sync_change_categories fc
JOIN feed_sync_changes c ON c.id = fc.change_id
JOIN feeds f ON f.id = c.feed_id AND f.enabled = 1
JOIN catalog_mode_feeds cmf ON cmf.feed_id = c.feed_id AND cmf.mode_id = ? AND cmf.exclude = 0
JOIN catalog_modes m ON m.id = cmf.mode_id AND m.enabled = 1
JOIN categories cat ON cat.name = fc.category
JOIN selected_categories sc ON sc.user_id = ? AND sc.mode_id = cmf.mode_id AND sc.category_id = cat.id
WHERE c.id > ? AND c.synced_at >= ?
ORDER BY c.id, fc.category`, modeID, userID, seenID, since)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck
	var out []UserFeedChange
	var lastChangeID int64 = -1
	for rows.Next() {
		var feedName, category string
		var changeID, syncedAt int64
		var added int
		if err := rows.Scan(&feedName, &changeID, &syncedAt, &category, &added); err != nil {
			return nil, err
		}
		if changeID != lastChangeID {
			out = append(out, UserFeedChange{ChangeID: changeID, ModeID: modeID, FeedName: feedName, SyncedAt: syncedAt, Categories: []UserFeedChangeCategory{}})
			lastChangeID = changeID
		}
		last := &out[len(out)-1]
		last.Categories = append(last.Categories, UserFeedChangeCategory{Category: category, AddedServices: added})
	}
	return out, rows.Err()
}

// AckUserFeedChanges marks changes up to throughID as seen in modeID — the
// mode the changes were shown for, which the client sends back, so an
// acknowledgement can't advance a different mode's cursor. The cursor only
// moves forward, so a stale acknowledgement can't re-surface old changes.
func (s *Store) AckUserFeedChanges(ctx context.Context, userID, modeID, throughID int64) error {
	_, err := s.DB.ExecContext(ctx, `
INSERT INTO user_feed_changes_seen(user_id, mode_id, change_id) VALUES (?, ?, ?)
ON CONFLICT(user_id, mode_id) DO UPDATE SET change_id = MAX(change_id, excluded.change_id)`,
		userID, modeID, throughID)
	return err
}
