package store

import (
	"context"
	"database/sql"
	"net/netip"
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
// it replaced.
type FeedSyncDiff struct {
	AddedServices   int
	RemovedServices int
	AddedPrefixes   int
	RemovedPrefixes int
	AddedByCategory map[string]int
}

func (d FeedSyncDiff) HasChanges() bool {
	return d.AddedServices+d.RemovedServices+d.AddedPrefixes+d.RemovedPrefixes > 0
}

type serviceIdentity struct{ category, service string }

// DiffCatalogEntries compares a feed's previous and new entries by service
// (category, name) and by distinct masked prefix.
func DiffCatalogEntries(prev, next []CatalogEntry) FeedSyncDiff {
	prevServices, nextServices := map[serviceIdentity]bool{}, map[serviceIdentity]bool{}
	prevPrefixes, nextPrefixes := map[netip.Prefix]bool{}, map[netip.Prefix]bool{}
	for _, e := range prev {
		prevServices[serviceIdentity{e.Category, e.Service}] = true
		if p, err := netip.ParsePrefix(e.CIDR); err == nil {
			prevPrefixes[p.Masked()] = true
		}
	}
	for _, e := range next {
		nextServices[serviceIdentity{e.Category, e.Service}] = true
		if p, err := netip.ParsePrefix(e.CIDR); err == nil {
			nextPrefixes[p.Masked()] = true
		}
	}
	d := FeedSyncDiff{AddedByCategory: map[string]int{}}
	for s := range nextServices {
		if !prevServices[s] {
			d.AddedServices++
			d.AddedByCategory[s.category]++
		}
	}
	for s := range prevServices {
		if !nextServices[s] {
			d.RemovedServices++
		}
	}
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
	return d
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
func RecordFeedSyncChangeTx(ctx context.Context, tx *sql.Tx, feedID int64, diff FeedSyncDiff, growth []ModeCategoryGrowth, syncedAt int64) error {
	res, err := tx.ExecContext(ctx, `
INSERT INTO feed_sync_changes(feed_id, synced_at, added_services, removed_services, added_prefixes, removed_prefixes)
VALUES (?, ?, ?, ?, ?, ?)`,
		feedID, syncedAt, diff.AddedServices, diff.RemovedServices, diff.AddedPrefixes, diff.RemovedPrefixes)
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
	for _, g := range growth {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO feed_sync_mode_growth(change_id, mode_id, category, added_prefixes) VALUES (?, ?, ?, ?)",
			changeID, g.ModeID, g.Category, g.Added); err != nil {
			return err
		}
	}
	const stale = `SELECT id FROM feed_sync_changes WHERE feed_id = ?1 AND id NOT IN (
		SELECT id FROM feed_sync_changes WHERE feed_id = ?1 ORDER BY synced_at DESC, id DESC LIMIT ?2)`
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM feed_sync_change_categories WHERE change_id IN ("+stale+")", feedID, feedSyncChangeRetention); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM feed_sync_mode_growth WHERE change_id IN ("+stale+")", feedID, feedSyncChangeRetention); err != nil {
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
	SyncedAt        int64              `json:"synced_at"`
	AddedServices   int                `json:"added_services"`
	RemovedServices int                `json:"removed_services"`
	AddedPrefixes   int                `json:"added_prefixes"`
	RemovedPrefixes int                `json:"removed_prefixes"`
	Categories      []FeedSyncCategory `json:"categories"`
}

// RecentFeedSyncChanges returns a feed's most recent sync changes, newest first.
func (s *Store) RecentFeedSyncChanges(ctx context.Context, feedID int64, limit int) ([]FeedSyncChange, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT id, synced_at, added_services, removed_services, added_prefixes, removed_prefixes
FROM feed_sync_changes WHERE feed_id = ?
ORDER BY synced_at DESC, id DESC LIMIT ?`, feedID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck
	var out []FeedSyncChange
	var ids []int64
	for rows.Next() {
		var id int64
		var c FeedSyncChange
		if err := rows.Scan(&id, &c.SyncedAt, &c.AddedServices, &c.RemovedServices, &c.AddedPrefixes, &c.RemovedPrefixes); err != nil {
			return nil, err
		}
		c.Categories = []FeedSyncCategory{}
		out = append(out, c)
		ids = append(ids, id)
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

// UserFeedChange is prefixes a feed sync newly made announced in the user's
// mode, through categories they have selected. Those routes reached the user
// because of the feed, not because of anything they did.
type UserFeedChange struct {
	ChangeID   int64                    `json:"change_id"`
	FeedName   string                   `json:"feed_name"`
	SyncedAt   int64                    `json:"synced_at"`
	Categories []UserFeedChangeCategory `json:"categories"`
}

type UserFeedChangeCategory struct {
	Category      string `json:"category"`
	AddedPrefixes int    `json:"added_prefixes"`
}

// UserFeedChanges returns the feed-driven growth the user hasn't acknowledged
// (changes newer than their cursor, within userFeedChangeWindow), oldest first.
func (s *Store) UserFeedChanges(ctx context.Context, userID int64, now time.Time) ([]UserFeedChange, error) {
	var seenID, modeID int64
	if err := s.DB.QueryRowContext(ctx,
		"SELECT feed_changes_seen_id, catalog_mode_id FROM users WHERE id = ?", userID).Scan(&seenID, &modeID); err != nil {
		return nil, err
	}
	since := now.Add(-userFeedChangeWindow).Unix()
	rows, err := s.DB.QueryContext(ctx, `
SELECT f.name, c.id, c.synced_at, g.category, g.added_prefixes
FROM feed_sync_mode_growth g
JOIN feed_sync_changes c ON c.id = g.change_id
JOIN feeds f ON f.id = c.feed_id
JOIN categories cat ON cat.name = g.category
JOIN selected_categories sc ON sc.user_id = ? AND sc.mode_id = g.mode_id AND sc.category_id = cat.id
WHERE g.mode_id = ? AND c.id > ? AND c.synced_at >= ? AND f.enabled = 1
ORDER BY c.id, g.category`, userID, modeID, seenID, since)
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
			out = append(out, UserFeedChange{ChangeID: changeID, FeedName: feedName, SyncedAt: syncedAt, Categories: []UserFeedChangeCategory{}})
			lastChangeID = changeID
		}
		last := &out[len(out)-1]
		last.Categories = append(last.Categories, UserFeedChangeCategory{Category: category, AddedPrefixes: added})
	}
	return out, rows.Err()
}

// AckUserFeedChanges marks changes up to throughID as seen. The cursor only
// moves forward, so a stale acknowledgement can't re-surface old changes.
func (s *Store) AckUserFeedChanges(ctx context.Context, userID, throughID int64) error {
	_, err := s.DB.ExecContext(ctx,
		"UPDATE users SET feed_changes_seen_id = MAX(feed_changes_seen_id, ?) WHERE id = ?", throughID, userID)
	return err
}
