package store

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"maps"
	"time"
)

// UserSnapshot is a point-in-time capture of user metrics.
type UserSnapshot struct {
	RecordedAt     time.Time `json:"recorded_at"`
	UsersDisabled  int       `json:"users_disabled"`
	UsersConnected int       `json:"users_connected"`
	UsersTotal     int       `json:"users_total"`
}

// FeedSnapshot is a point-in-time capture of feed prefix counts.
type FeedSnapshot struct {
	RecordedAt time.Time     `json:"recorded_at"`
	Prefixes   map[int64]int `json:"prefixes"`
}

const userSnapshotMinInterval = 60 * time.Second

// SaveUserSnapshot records user metrics, rate-limited to one row per userSnapshotMinInterval.
// If the last row is within the interval and values changed, it updates the row in place.
// If the last row is older than the interval, a new row is inserted.
// If values are unchanged, the call is a no-op.
func (s *Store) SaveUserSnapshot(ctx context.Context, disabled, connected, total int) error {
	var lastID int64
	var lastRecordedAtUnix int64
	var lastDisabled, lastConnected, lastTotal int
	row := s.DB.QueryRowContext(ctx,
		"SELECT id, recorded_at, users_disabled, users_connected, users_total FROM user_snapshots ORDER BY recorded_at DESC LIMIT 1")
	hasLast := row.Scan(&lastID, &lastRecordedAtUnix, &lastDisabled, &lastConnected, &lastTotal) == nil

	if hasLast {
		// Unchanged — skip
		if lastDisabled == disabled && lastConnected == connected && lastTotal == total {
			return nil
		}
		// Last row is within rate-limit window — update in place
		lastTime := time.Unix(lastRecordedAtUnix, 0).UTC()
		if time.Since(lastTime) < userSnapshotMinInterval {
			_, err := s.DB.ExecContext(ctx,
				"UPDATE user_snapshots SET users_disabled=?, users_connected=?, users_total=? WHERE id=?",
				disabled, connected, total, lastID)
			return err
		}
	}
	// Insert new row
	_, err := s.DB.ExecContext(ctx,
		"INSERT INTO user_snapshots(recorded_at, users_disabled, users_connected, users_total) VALUES (?, ?, ?, ?)",
		time.Now().UTC().Unix(), disabled, connected, total)
	return err
}

const feedSnapshotMinInterval = 300 * time.Second

// SaveFeedSnapshot records feed prefix counts, rate-limited to one row per feedSnapshotMinInterval.
// If the last row is within the interval and values changed, it updates the row in place.
// If the last row is older than the interval, a new row is inserted.
// If values are unchanged, the call is a no-op.
// Counts live in feed_snapshot_counts, one row per (snapshot, feed).
func (s *Store) SaveFeedSnapshot(ctx context.Context, prefixes map[int64]int) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		var lastID int64
		var lastRecordedAtUnix int64
		row := tx.QueryRowContext(ctx,
			"SELECT id, recorded_at FROM feed_snapshots ORDER BY recorded_at DESC LIMIT 1")
		hasLast := row.Scan(&lastID, &lastRecordedAtUnix) == nil

		if hasLast {
			lastCounts, err := feedSnapshotCounts(ctx, tx, lastID)
			if err != nil {
				return err
			}
			// Unchanged — skip
			if maps.Equal(lastCounts, prefixes) {
				return nil
			}
			// Last row is within rate-limit window — update in place
			lastTime := time.Unix(lastRecordedAtUnix, 0).UTC()
			if time.Since(lastTime) < feedSnapshotMinInterval {
				if _, err := tx.ExecContext(ctx,
					"DELETE FROM feed_snapshot_counts WHERE snapshot_id = ?", lastID); err != nil {
					return err
				}
				return insertFeedSnapshotCounts(ctx, tx, lastID, prefixes)
			}
		}
		// Insert new row
		result, err := tx.ExecContext(ctx,
			"INSERT INTO feed_snapshots(recorded_at) VALUES (?)", time.Now().UTC().Unix())
		if err != nil {
			return err
		}
		snapshotID, err := result.LastInsertId()
		if err != nil {
			return err
		}
		return insertFeedSnapshotCounts(ctx, tx, snapshotID, prefixes)
	})
}

func feedSnapshotCounts(ctx context.Context, tx *sql.Tx, snapshotID int64) (map[int64]int, error) {
	rows, err := tx.QueryContext(ctx,
		"SELECT feed_id, prefix_count FROM feed_snapshot_counts WHERE snapshot_id = ?", snapshotID)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("WARNING: rows close: %v", err)
		}
	}()
	counts := map[int64]int{}
	for rows.Next() {
		var feedID int64
		var count int
		if err := rows.Scan(&feedID, &count); err != nil {
			return nil, err
		}
		counts[feedID] = count
	}
	return counts, rows.Err()
}

func insertFeedSnapshotCounts(ctx context.Context, tx *sql.Tx, snapshotID int64, prefixes map[int64]int) error {
	for feedID, count := range prefixes {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO feed_snapshot_counts(snapshot_id, feed_id, prefix_count) VALUES (?, ?, ?)",
			snapshotID, feedID, count); err != nil {
			return err
		}
	}
	return nil
}

// GetUserSnapshots returns user metrics for the last N days.
func (s *Store) GetUserSnapshots(ctx context.Context, days int) ([]UserSnapshot, error) {
	cutoff := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour).Unix()
	rows, err := s.DB.QueryContext(ctx,
		"SELECT recorded_at, users_disabled, users_connected, users_total FROM user_snapshots WHERE recorded_at >= ? ORDER BY recorded_at",
		cutoff)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("WARNING: rows close: %v", err)
		}
	}()
	var snapshots []UserSnapshot
	for rows.Next() {
		var s UserSnapshot
		var unix int64
		if err := rows.Scan(&unix, &s.UsersDisabled, &s.UsersConnected, &s.UsersTotal); err != nil {
			return nil, err
		}
		s.RecordedAt = time.Unix(unix, 0).UTC()
		snapshots = append(snapshots, s)
	}
	return snapshots, rows.Err()
}

// GetFeedSnapshots returns feed prefix counts for the last N days.
func (s *Store) GetFeedSnapshots(ctx context.Context, days int) ([]FeedSnapshot, error) {
	cutoff := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour).Unix()
	rows, err := s.DB.QueryContext(ctx, `
SELECT fs.id, fs.recorded_at, fsc.feed_id, fsc.prefix_count
FROM feed_snapshots fs
JOIN feed_snapshot_counts fsc ON fsc.snapshot_id = fs.id
WHERE fs.recorded_at >= ?
ORDER BY fs.recorded_at, fs.id`, cutoff)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("WARNING: rows close: %v", err)
		}
	}()
	var snapshots []FeedSnapshot
	lastID := int64(-1)
	for rows.Next() {
		var id, unix, feedID int64
		var count int
		if err := rows.Scan(&id, &unix, &feedID, &count); err != nil {
			return nil, err
		}
		// Rows are ordered by snapshot, so each snapshot's rows are
		// contiguous — a new snapshot starts whenever the id changes.
		if id != lastID {
			snapshots = append(snapshots, FeedSnapshot{
				RecordedAt: time.Unix(unix, 0).UTC(),
				Prefixes:   map[int64]int{},
			})
			lastID = id
		}
		snapshots[len(snapshots)-1].Prefixes[feedID] = count
	}
	return snapshots, rows.Err()
}

// PurgeUserSnapshots deletes user snapshots older than N days,
// keeping the newest record just outside the window for graph continuity.
func (s *Store) PurgeUserSnapshots(ctx context.Context, days int) error {
	cutoff := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour).Unix()
	_, err := s.DB.ExecContext(ctx, `
		DELETE FROM user_snapshots
		WHERE recorded_at < ?
		  AND id NOT IN (
		      SELECT id FROM user_snapshots
		      WHERE recorded_at < ?
		      ORDER BY recorded_at DESC LIMIT 1
		  )`, cutoff, cutoff)
	return err
}

// RecordFeedSnapshot saves a feed prefix count snapshot, only when metrics are enabled.
func (s *Store) RecordFeedSnapshot(ctx context.Context, metricsEnabled bool) {
	if !metricsEnabled {
		return
	}
	feeds, err := s.Feeds(ctx, false)
	if err != nil {
		return
	}
	counts := make(map[int64]int)
	for _, f := range feeds {
		var count int
		if err := s.DB.QueryRowContext(ctx,
			"SELECT COUNT(DISTINCT prefix_id) FROM catalog_entries WHERE feed_id = ?", f.ID).Scan(&count); err == nil && count > 0 {
			counts[f.ID] = count
		}
	}
	_ = s.SaveFeedSnapshot(ctx, counts) //nolint:errcheck // best-effort snapshot recording
}

// RecordUserSnapshot saves a user metric snapshot, only when metrics are enabled.
// peerStates is the BGP peer state map (peer IP:ASN → state string).
func (s *Store) RecordUserSnapshot(ctx context.Context, metricsEnabled bool, peerStates map[string]string) {
	if !metricsEnabled {
		return
	}
	users, err := s.Users(ctx, false)
	if err != nil {
		return
	}
	total := len(users)
	disabled := 0
	for _, u := range users {
		if !u.Enabled {
			disabled++
		}
	}
	var connected int
	for _, u := range users {
		key := fmt.Sprintf("%s:%d", u.PeerIP, u.PeerASN)
		if peerStates[key] == "ESTABLISHED" {
			connected++
		}
	}
	_ = s.SaveUserSnapshot(ctx, disabled, connected, total) //nolint:errcheck // best-effort snapshot recording
}

// PurgeFeedSnapshots deletes feed snapshots older than N days,
// keeping the newest record just outside the window.
func (s *Store) PurgeFeedSnapshots(ctx context.Context, days int) error {
	cutoff := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour).Unix()
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		// Child rows first — explicit rather than relying on the FK
		// cascade, so the purge works the same regardless of the
		// connection's foreign_keys pragma.
		if _, err := tx.ExecContext(ctx, `
		DELETE FROM feed_snapshot_counts
		WHERE snapshot_id IN (
		    SELECT id FROM feed_snapshots
		    WHERE recorded_at < ?
		      AND id NOT IN (
		          SELECT id FROM feed_snapshots
		          WHERE recorded_at < ?
		          ORDER BY recorded_at DESC LIMIT 1
		      )
		)`, cutoff, cutoff); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `
		DELETE FROM feed_snapshots
		WHERE recorded_at < ?
		  AND id NOT IN (
		      SELECT id FROM feed_snapshots
		      WHERE recorded_at < ?
		      ORDER BY recorded_at DESC LIMIT 1
		  )`, cutoff, cutoff)
		return err
	})
}
