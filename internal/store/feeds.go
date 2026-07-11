package store

import (
	"context"
	"database/sql"
	"log"
	"strings"
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

func (s *Store) UpdateFeed(ctx context.Context, feed Feed) error {
	err := s.Transaction(ctx, func(tx *sql.Tx) error {
		var oldURL string
		var oldAdapterID int64
		var oldData string
		var oldName string
		var oldAllowedHosts string
		var oldRestrictHosts bool
		if err := tx.QueryRowContext(ctx,
			"SELECT url, adapter_id, data, name, allowed_hosts, restrict_hosts FROM feeds WHERE id = ?", feed.ID).
			Scan(&oldURL, &oldAdapterID, &oldData, &oldName, &oldAllowedHosts, &oldRestrictHosts); err != nil {
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
			return nil
		}
		if _, err := tx.ExecContext(ctx,
			"DELETE FROM catalog_entries WHERE feed_id = ?", feed.ID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			"UPDATE feeds SET last_success = NULL, last_error = NULL WHERE id = ?", feed.ID)
		return err
	})
	if err != nil {
		return err
	}
	// The update may have flipped enabled or wiped the feed's entries (the
	// config-change branch above) — either way the materialized merge of
	// every mode linking this feed is stale now.
	return s.RebuildModeEntriesForFeed(ctx, feed.ID)
}

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

func (s *Store) DeleteFeed(ctx context.Context, id int64) error {
	// Collect the affected modes before the delete cascades the
	// catalog_mode_feeds links away.
	modeIDs, err := s.FeedModes(ctx, id)
	if err != nil {
		return err
	}
	err = s.Transaction(ctx, func(tx *sql.Tx) error {
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
		_, err = tx.ExecContext(ctx, `
DELETE FROM selected_services
WHERE NOT EXISTS (
    SELECT 1 FROM catalog_entries ce JOIN feeds f ON f.id = ce.feed_id
    JOIN catalog_mode_feeds cmf ON cmf.feed_id = f.id
    WHERE ce.service_id = selected_services.service_id
      AND cmf.mode_id = selected_services.mode_id
      AND cmf.exclude = 0
)`)
		return err
	})
	if err != nil {
		return err
	}
	return s.rebuildModes(ctx, modeIDs)
}
