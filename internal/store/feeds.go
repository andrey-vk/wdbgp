package store

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Feed represents a data feed source.
type Feed struct {
	ID            int64
	Name          string
	URL           string
	AdapterID     int64
	Enabled       bool
	SyncInterval  int
	Data          string // JSON parameterization for adapters
	AllowedHosts  string
	RestrictHosts bool
	LastSuccess   int64 // Unix epoch seconds; 0 = never synced
	LastError     string
}

func (s *Store) Feeds(ctx context.Context, enabledOnly bool) ([]Feed, error) {
	query := `SELECT f.id, f.name, f.url,
	                 f.adapter_id, f.enabled,
	                 COALESCE(f.sync_interval, 0),
	                 COALESCE(f.data, ''),
	                 f.allowed_hosts, f.restrict_hosts,
	                 COALESCE(f.last_success, 0), COALESCE(f.last_error, '')
	          FROM feeds f
	          JOIN feed_adapters a ON a.id = f.adapter_id`
	if enabledOnly {
		query += ` WHERE f.enabled = 1
		           AND EXISTS (SELECT 1 FROM catalog_mode_feeds cmf
		                       JOIN catalog_modes m ON m.id = cmf.mode_id
		                       WHERE cmf.feed_id = f.id AND m.enabled = 1)`
	}
	query += " ORDER BY f.id"
	rows, err := s.DB.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("WARNING: rows close: %v", err)
		}
	}()
	var feeds []Feed
	for rows.Next() {
		var feed Feed
		if err := rows.Scan(
			&feed.ID, &feed.Name, &feed.URL, &feed.AdapterID,
			&feed.Enabled, &feed.SyncInterval, &feed.Data, &feed.AllowedHosts, &feed.RestrictHosts,
			&feed.LastSuccess, &feed.LastError,
		); err != nil {
			return nil, err
		}
		feeds = append(feeds, feed)
	}
	return feeds, rows.Err()
}

func (s *Store) Feed(ctx context.Context, id int64) (Feed, error) {
	var feed Feed
	err := s.DB.QueryRowContext(ctx, `
SELECT id, name, url,
       adapter_id, enabled,
       COALESCE(sync_interval, 0),
       COALESCE(data, ''),
       allowed_hosts, restrict_hosts,
       COALESCE(last_success, 0), COALESCE(last_error, '')
FROM feeds
WHERE id = ?`, id).Scan(
		&feed.ID, &feed.Name, &feed.URL, &feed.AdapterID,
		&feed.Enabled, &feed.SyncInterval, &feed.Data, &feed.AllowedHosts, &feed.RestrictHosts,
		&feed.LastSuccess, &feed.LastError,
	)
	return feed, err
}

func (s *Store) AddFeed(
	ctx context.Context,
	name string,
	url string,
	adapterID int64,
	enabled bool,
	syncInterval int,
	data string,
	allowedHosts string,
	restrictHosts bool,
) (int64, error) {
	allowedHosts = s.mergedAllowedHosts(ctx, adapterID, allowedHosts)
	var feedID int64
	err := s.Transaction(ctx, func(tx *sql.Tx) error {
		var err error
		feedID, err = addFeedTx(ctx, tx, name, url, adapterID, enabled, syncInterval, data, allowedHosts, restrictHosts)
		return err
	})
	return feedID, err
}

// AddFeedWithMode is AddFeed plus a catalog_mode_feeds assignment, both in
// the same transaction — modeID <= 0 skips the assignment. Used instead of
// AddFeed + a separate catalog_mode_feeds insert wherever the caller needs
// mode assignment to be atomic with feed creation: a feed row must not be
// left committed and orphaned (invisible to mode-scoped sync, and a source
// of duplicate feeds on client retry) if the assignment can't be persisted.
func (s *Store) AddFeedWithMode(
	ctx context.Context,
	name string,
	url string,
	adapterID int64,
	enabled bool,
	syncInterval int,
	data string,
	allowedHosts string,
	restrictHosts bool,
	modeID int64,
) (int64, error) {
	allowedHosts = s.mergedAllowedHosts(ctx, adapterID, allowedHosts)
	var feedID int64
	err := s.Transaction(ctx, func(tx *sql.Tx) error {
		var err error
		feedID, err = addFeedTx(ctx, tx, name, url, adapterID, enabled, syncInterval, data, allowedHosts, restrictHosts)
		if err != nil {
			return err
		}
		if modeID > 0 {
			if _, err := tx.ExecContext(ctx,
				"INSERT INTO catalog_mode_feeds(mode_id, feed_id) VALUES (?, ?)",
				modeID, feedID); err != nil {
				return err
			}
		}
		return nil
	})
	return feedID, err
}

// mergedAllowedHosts merges the adapter's declared additional hosts with
// user-provided ones. Must be called before opening a transaction: it
// queries s.DB directly rather than a *sql.Tx, and this store's
// single-connection pool (SetMaxOpenConns(1)) would deadlock waiting for a
// second connection if called from inside an already-open transaction.
func (s *Store) mergedAllowedHosts(ctx context.Context, adapterID int64, allowedHosts string) string {
	if extraHosts := s.BuiltinAdapterAllowedHosts(ctx, adapterID); extraHosts != "" {
		return mergeHosts(allowedHosts, extraHosts)
	}
	return allowedHosts
}

// addFeedTx inserts a feeds row within an existing transaction, shared by
// AddFeed and AddFeedWithMode.
func addFeedTx(
	ctx context.Context,
	tx *sql.Tx,
	name string,
	url string,
	adapterID int64,
	enabled bool,
	syncInterval int,
	data string,
	allowedHosts string,
	restrictHosts bool,
) (int64, error) {
	result, err := tx.ExecContext(ctx,
		"INSERT INTO feeds(name, url, adapter_id, enabled, sync_interval, data, allowed_hosts, restrict_hosts) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		name, url, adapterID, enabled, syncInterval, data, allowedHosts, restrictHosts)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// mergeHosts adds host to a comma-separated hosts string if not already present.
func mergeHosts(hosts, host string) string {
	host = strings.TrimSpace(host)
	for _, h := range strings.Split(hosts, ",") {
		if strings.TrimSpace(h) == host {
			return hosts
		}
	}
	if hosts == "" {
		return host
	}
	return hosts + "," + host
}

// UpdateFeed updates feed and returns the Enabled value it had immediately
// before this update, read in the same transaction as the write — so a
// caller comparing before/after enabled state (e.g. for an audit log) gets
// the value this specific call actually overwrote, not a value read by an
// independent, unsynchronized query that a concurrent update could have
// already changed underneath it.
func (s *Store) UpdateFeed(ctx context.Context, feed Feed, meta AuditMeta) (prevEnabled bool, err error) {
	err = s.Transaction(ctx, func(tx *sql.Tx) error {
		var oldURL string
		var oldAdapterID int64
		var oldData string
		var oldName string
		var oldAllowedHosts string
		var oldRestrictHosts bool
		if err := tx.QueryRowContext(ctx,
			"SELECT url, adapter_id, data, name, allowed_hosts, restrict_hosts, enabled FROM feeds WHERE id = ?", feed.ID).
			Scan(&oldURL, &oldAdapterID, &oldData, &oldName, &oldAllowedHosts, &oldRestrictHosts, &prevEnabled); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE feeds
			 SET name = ?, url = ?, adapter_id = ?, enabled = ?, sync_interval = ?, data = ?, allowed_hosts = ?, restrict_hosts = ?
			 WHERE id = ?`,
			feed.Name, feed.URL, feed.AdapterID, feed.Enabled, feed.SyncInterval, feed.Data,
			feed.AllowedHosts, feed.RestrictHosts, feed.ID); err != nil {
			return err
		}
		if oldURL == feed.URL && oldAdapterID == feed.AdapterID && oldData == feed.Data && oldName == feed.Name && oldAllowedHosts == feed.AllowedHosts && oldRestrictHosts == feed.RestrictHosts {
			// Config unchanged — but enabled may have flipped, which
			// changes what the feed contributes.
			if err := RebuildModeEntriesForFeedTx(ctx, tx, feed.ID); err != nil {
				return err
			}
		} else {
			if _, err := tx.ExecContext(ctx,
				"DELETE FROM catalog_entries WHERE feed_id = ?", feed.ID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx,
				"UPDATE feeds SET last_success = NULL, last_error = NULL WHERE id = ?", feed.ID); err != nil {
				return err
			}
			// The update may have flipped enabled or wiped the feed's
			// entries — rebuild in the SAME transaction, so a
			// disabled/reconfigured feed can never keep being announced
			// from a stale materialized merge.
			if err := RebuildModeEntriesForFeedTx(ctx, tx, feed.ID); err != nil {
				return err
			}
		}
		if updateFeedPreCommitHook != nil {
			updateFeedPreCommitHook()
		}
		return AuditEntryTx(ctx, tx, meta, "feed", strconv.FormatInt(feed.ID, 10),
			map[string]bool{"enabled": prevEnabled}, map[string]bool{"enabled": feed.Enabled}, false)
	})
	return prevEnabled, err
}

// updateFeedPreCommitHook, if set, runs once per UpdateFeed attempt, right
// before its transaction's final write (the audit insert) and commit — a
// test seam used to hold a transaction open deterministically, so a test
// can verify a concurrent UpdateFeed call genuinely blocks until this one
// commits, rather than relying on timing.
var updateFeedPreCommitHook func()

// FeedModes returns the mode IDs associated with a feed.
func (s *Store) FeedModes(ctx context.Context, feedID int64) ([]int64, error) {
	rows, err := s.DB.QueryContext(ctx,
		"SELECT mode_id FROM catalog_mode_feeds WHERE feed_id = ? ORDER BY mode_id", feedID)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("WARNING: rows close: %v", err)
		}
	}()
	var modeIDs []int64
	for rows.Next() {
		var modeID int64
		if err := rows.Scan(&modeID); err != nil {
			return nil, err
		}
		modeIDs = append(modeIDs, modeID)
	}
	return modeIDs, rows.Err()
}

// selectionStateKey identifies one user's selection in one mode.
type selectionStateKey struct{ userID, modeID int64 }

// selectionStatesTx reads every user's selection in every mode in two queries,
// with the mode names the audit rows carry. Users with no selection have no key.
func selectionStatesTx(ctx context.Context, tx *sql.Tx) (map[selectionStateKey]selectionAuditPayload, error) {
	names := map[int64]string{}
	nameRows, err := tx.QueryContext(ctx, "SELECT id, name FROM catalog_modes")
	if err != nil {
		return nil, err
	}
	for nameRows.Next() {
		var id int64
		var name string
		if err := nameRows.Scan(&id, &name); err != nil {
			_ = nameRows.Close() //nolint:errcheck
			return nil, err
		}
		names[id] = name
	}
	if err := nameRows.Close(); err != nil {
		return nil, err
	}
	if err := nameRows.Err(); err != nil {
		return nil, err
	}

	categories := map[selectionStateKey]map[string]bool{}
	services := map[selectionStateKey]map[ServiceKey]bool{}
	catRows, err := tx.QueryContext(ctx, `
SELECT sc.user_id, sc.mode_id, c.name FROM selected_categories sc
JOIN categories c ON c.id = sc.category_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = catRows.Close() }() //nolint:errcheck
	for catRows.Next() {
		var key selectionStateKey
		var category string
		if err := catRows.Scan(&key.userID, &key.modeID, &category); err != nil {
			return nil, err
		}
		if categories[key] == nil {
			categories[key] = map[string]bool{}
		}
		categories[key][category] = true
	}
	if err := catRows.Err(); err != nil {
		return nil, err
	}
	svcRows, err := tx.QueryContext(ctx, `
SELECT ss.user_id, ss.mode_id, c.name, sv.name FROM selected_services ss
JOIN services sv ON sv.id = ss.service_id
JOIN categories c ON c.id = sv.category_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = svcRows.Close() }() //nolint:errcheck
	for svcRows.Next() {
		var key selectionStateKey
		var svc ServiceKey
		if err := svcRows.Scan(&key.userID, &key.modeID, &svc.Category, &svc.Service); err != nil {
			return nil, err
		}
		if services[key] == nil {
			services[key] = map[ServiceKey]bool{}
		}
		services[key][svc] = true
	}
	if err := svcRows.Err(); err != nil {
		return nil, err
	}

	out := map[selectionStateKey]selectionAuditPayload{}
	for key := range categories {
		out[key] = selectionAuditState(key.modeID, names[key.modeID], categories[key], services[key])
	}
	for key := range services {
		if _, ok := out[key]; !ok {
			out[key] = selectionAuditState(key.modeID, names[key.modeID], nil, services[key])
		}
	}
	return out, nil
}

// DeleteFeed deletes a feed and prunes the selections that no longer have any
// service behind them. meta is the admin action; each pruned selection is
// audited under it, and the services the feed took away are recorded as a sync
// change, so the users' change logs can show both.
func (s *Store) DeleteFeed(ctx context.Context, id int64, meta AuditMeta) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		// Collect the affected modes before the delete cascades the
		// catalog_mode_feeds links away — inside the transaction, so the
		// list can't race a concurrent membership change.
		modeIDs, err := feedModeIDsTx(ctx, tx, id)
		if err != nil {
			return err
		}
		// Deleting the feed removes its services from every mode that used it.
		// Record that as a sync change, so users' history still shows the
		// services going away after the feed row is gone (migration 039 keeps
		// the change rows with SET NULL). It is recorded before the pruning
		// below, so its audit sequence orders it ahead of the selection rows
		// the pruning writes, even within the same second.
		// A disabled feed already left its modes when it was disabled, so its
		// services were removed then, not now; recording them again would
		// misdate the removal.
		var enabled int
		if err := tx.QueryRowContext(ctx, "SELECT enabled FROM feeds WHERE id = ?", id).Scan(&enabled); err != nil {
			return err
		}
		prev, err := CatalogEntriesForFeedTx(ctx, tx, id)
		if err != nil {
			return err
		}
		if diff := DiffCatalogEntries(prev, nil); enabled != 0 && diff.HasChanges() {
			if err := RecordFeedSyncChangeTx(ctx, tx, id, diff, time.Now().Unix()); err != nil {
				return err
			}
		}
		// The pruning below is unscoped: it drops selections that no feed backs in
		// any mode, including modes the feed was detached from earlier. So every
		// selection is read before, not only the ones in the feed's current modes.
		before, err := selectionStatesTx(ctx, tx)
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, "DELETE FROM feeds WHERE id = ?", id)
		if err != nil {
			return err
		}
		deleted, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if deleted == 0 {
			return sql.ErrNoRows
		}
		if _, err := tx.ExecContext(ctx, `
DELETE FROM selected_categories
WHERE NOT EXISTS (
    SELECT 1 FROM catalog_entries ce
    JOIN services sv ON sv.id = ce.service_id
    JOIN feeds f ON f.id = ce.feed_id
    JOIN catalog_mode_feeds cmf ON cmf.feed_id = f.id
    WHERE sv.category_id = selected_categories.category_id
      AND cmf.mode_id = selected_categories.mode_id
      AND cmf.exclude = 0
)`); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `
DELETE FROM selected_services
WHERE NOT EXISTS (
    SELECT 1 FROM catalog_entries ce JOIN feeds f ON f.id = ce.feed_id
    JOIN catalog_mode_feeds cmf ON cmf.feed_id = f.id
    WHERE ce.service_id = selected_services.service_id
      AND cmf.mode_id = selected_services.mode_id
      AND cmf.exclude = 0
)`); err != nil {
			return err
		}
		// Rebuild in the SAME transaction: a deleted feed must never keep
		// being advertised from a stale materialized merge.
		for _, modeID := range modeIDs {
			if err := rebuildModeEntriesTx(ctx, tx, modeID); err != nil {
				return fmt.Errorf("rebuild mode %d: %w", modeID, err)
			}
		}
		selectionMeta := AuditMeta{Actor: meta.Actor, UserAgent: meta.UserAgent, Action: "user.selections_changed"}
		after, err := selectionStatesTx(ctx, tx)
		if err != nil {
			return err
		}
		keys := make([]selectionStateKey, 0, len(before))
		for key := range before {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].userID != keys[j].userID {
				return keys[i].userID < keys[j].userID
			}
			return keys[i].modeID < keys[j].modeID
		})
		for _, key := range keys {
			b := before[key]
			a, ok := after[key]
			if !ok {
				a = selectionAuditState(key.modeID, b.ModeName, nil, nil)
			}
			if err := AuditEntryTx(ctx, tx, selectionMeta, "user", strconv.FormatInt(key.userID, 10), selectionAuditValue(b), selectionAuditValue(a), false); err != nil {
				return err
			}
		}
		return nil
	})
}
