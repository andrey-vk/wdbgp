package store

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
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
func (s *Store) SaveFeedSnapshot(ctx context.Context, prefixes map[int64]int) error {
	j, err := json.Marshal(prefixes)
	if err != nil {
		return fmt.Errorf("marshal feed prefixes: %w", err)
	}

	var lastID int64
	var lastRecordedAtUnix int64
	var lastJSON string
	row := s.DB.QueryRowContext(ctx,
		"SELECT id, recorded_at, prefixes FROM feed_snapshots ORDER BY recorded_at DESC LIMIT 1")
	hasLast := row.Scan(&lastID, &lastRecordedAtUnix, &lastJSON) == nil

	if hasLast {
		// Unchanged — skip
		if lastJSON == string(j) {
			return nil
		}
		// Last row is within rate-limit window — update in place
		lastTime := time.Unix(lastRecordedAtUnix, 0).UTC()
		if time.Since(lastTime) < feedSnapshotMinInterval {
			_, err := s.DB.ExecContext(ctx,
				"UPDATE feed_snapshots SET prefixes=? WHERE id=?",
				string(j), lastID)
			return err
		}
	}
	// Insert new row
	_, err = s.DB.ExecContext(ctx,
		"INSERT INTO feed_snapshots(recorded_at, prefixes) VALUES (?, ?)",
		time.Now().UTC().Unix(), string(j))
	return err
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
	rows, err := s.DB.QueryContext(ctx,
		"SELECT recorded_at, prefixes FROM feed_snapshots WHERE recorded_at >= ? ORDER BY recorded_at",
		cutoff)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("WARNING: rows close: %v", err)
		}
	}()
	var snapshots []FeedSnapshot
	for rows.Next() {
		var fs FeedSnapshot
		var unix int64
		var j string
		if err := rows.Scan(&unix, &j); err != nil {
			return nil, err
		}
		fs.RecordedAt = time.Unix(unix, 0).UTC()
		if err := json.Unmarshal([]byte(j), &fs.Prefixes); err != nil {
			return nil, fmt.Errorf("unmarshal feed snapshot prefixes: %w", err)
		}
		snapshots = append(snapshots, fs)
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
	_, err := s.DB.ExecContext(ctx, `
		DELETE FROM feed_snapshots
		WHERE recorded_at < ?
		  AND id NOT IN (
		      SELECT id FROM feed_snapshots
		      WHERE recorded_at < ?
		      ORDER BY recorded_at DESC LIMIT 1
		  )`, cutoff, cutoff)
	return err
}
