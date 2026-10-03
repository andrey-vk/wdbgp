package store

import (
	"context"
	"log"
	"time"
)

// AuditLogEntry is one row of the append-only audit_log table.
type AuditLogEntry struct {
	ID         int64
	RecordedAt time.Time
	Actor      string // "admin:<ip>" or "user:<id>"
	UserAgent  string
	Action     string // dot-namespaced, e.g. "communities.reset"
	ObjectType string // "mode" | "feed" | "user" | "settings"
	ObjectID   string // stringified ID; "" for global settings
	Before     string // JSON, may be empty
	After      string // JSON, may be empty
}

// RecordAuditLog inserts one audit log entry. Callers treat a failure here
// as best-effort (log and continue) — an audit-log write must never block
// the mutation it records.
func (s *Store) RecordAuditLog(ctx context.Context, e AuditLogEntry) error {
	recordedAt := e.RecordedAt
	if recordedAt.IsZero() {
		recordedAt = time.Now().UTC()
	}
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO audit_log (recorded_at, actor, user_agent, action, object_type, object_id, before, after)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		recordedAt.Unix(), e.Actor, e.UserAgent, e.Action, e.ObjectType, e.ObjectID, e.Before, e.After)
	return err
}

// AuditLogFilter narrows ListAuditLog. An empty string field or zero time
// means "no filter" for that field.
type AuditLogFilter struct {
	Actor      string
	Action     string
	ObjectType string
	ObjectID   string
	Since      time.Time
	Until      time.Time
}

// ListAuditLog returns entries matching filter, newest first, along with
// the total count of matching rows independent of limit/offset (for
// pagination).
func (s *Store) ListAuditLog(ctx context.Context, filter AuditLogFilter, limit, offset int) ([]AuditLogEntry, int, error) {
	where := "WHERE 1=1"
	var args []any
	if filter.Actor != "" {
		where += " AND actor = ?"
		args = append(args, filter.Actor)
	}
	if filter.Action != "" {
		where += " AND action = ?"
		args = append(args, filter.Action)
	}
	if filter.ObjectType != "" {
		where += " AND object_type = ?"
		args = append(args, filter.ObjectType)
	}
	if filter.ObjectID != "" {
		where += " AND object_id = ?"
		args = append(args, filter.ObjectID)
	}
	if !filter.Since.IsZero() {
		where += " AND recorded_at >= ?"
		args = append(args, filter.Since.UTC().Unix())
	}
	if !filter.Until.IsZero() {
		where += " AND recorded_at <= ?"
		args = append(args, filter.Until.UTC().Unix())
	}

	var total int
	if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_log "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := s.DB.QueryContext(ctx,
		"SELECT id, recorded_at, actor, user_agent, action, object_type, object_id, before, after FROM audit_log "+
			where+" ORDER BY recorded_at DESC, id DESC LIMIT ? OFFSET ?",
		append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("WARNING: rows close: %v", err)
		}
	}()

	var entries []AuditLogEntry
	for rows.Next() {
		var e AuditLogEntry
		var unix int64
		if err := rows.Scan(&e.ID, &unix, &e.Actor, &e.UserAgent, &e.Action, &e.ObjectType, &e.ObjectID, &e.Before, &e.After); err != nil {
			return nil, 0, err
		}
		e.RecordedAt = time.Unix(unix, 0).UTC()
		entries = append(entries, e)
	}
	return entries, total, rows.Err()
}

// PurgeAuditLog deletes entries older than `days`.
func (s *Store) PurgeAuditLog(ctx context.Context, days int) error {
	cutoff := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour).Unix()
	_, err := s.DB.ExecContext(ctx, "DELETE FROM audit_log WHERE recorded_at < ?", cutoff)
	return err
}
