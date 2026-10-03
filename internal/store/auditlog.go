package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"time"
	"unicode/utf8"
)

// maxAuditUserAgent bounds the User-Agent header value stored per audit
// row. It comes straight from the request with no length check of its
// own, and HTTP headers can be sent close to a server's ~1MiB header
// limit — without a cap, an authenticated caller alternating two valid
// mutation values on repeated requests could grow audit_log arbitrarily
// fast just by sending a huge User-Agent every time. Real User-Agent
// strings are a few hundred bytes at most, so this has no effect on any
// legitimate one.
const maxAuditUserAgent = 512

// truncateUserAgent bounds s to maxAuditUserAgent bytes — see
// truncateUTF8.
func truncateUserAgent(s string) string {
	return truncateUTF8(s, maxAuditUserAgent)
}

// truncateUTF8 bounds s to maxBytes, cutting at a valid UTF-8 rune
// boundary so truncation can't produce invalid UTF-8 that a later
// consumer (the admin UI, an API client decoding the stored value)
// chokes on.
func truncateUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

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
		recordedAt.Unix(), e.Actor, truncateUserAgent(e.UserAgent), e.Action, e.ObjectType, e.ObjectID, e.Before, e.After)
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

// AuditMeta carries the HTTP-request-derived fields for an audit entry
// that a mutation function records atomically inside its own transaction.
// ObjectType/ObjectID/before/after are supplied by the mutation function
// itself, which already computes them as part of its own work. A
// zero-value AuditMeta (empty Actor) means "don't audit this call" —
// callers that intentionally skip auditing (e.g. background feed sync's
// count-only community generation, or internal store tests) just pass
// AuditMeta{}.
type AuditMeta struct {
	Actor     string
	UserAgent string
	Action    string
}

// AuditEntryTx inserts one audit entry via tx — the same transaction as
// the mutation it describes, not a separate statement run after that
// transaction commits. Folding the write in here guarantees two things a
// separate post-commit write cannot: the audit row can never be recorded
// without the mutation it describes (or vice versa — they commit or roll
// back together), and two concurrent requests' audit rows land in
// audit_log in the same relative order their mutations actually committed
// in (an INSERT's rowid is assigned in commit order, same as the
// mutation's own writes in that same transaction; two separate post-commit
// INSERTs from different connections have no such guarantee relative to
// each other). Exported so a caller outside this package that manages its
// own Store.Transaction (e.g. apiSettingsPut, folding a generic Setting's
// SetTx/ResetTx write together with its audit entry) can use it directly.
//
// Skipped silently (no error) when meta.Actor is empty (auditing not
// requested) or, unless force is set, when before/after marshal to the
// same JSON — the "no spurious row when nothing actually changed" guard
// every hook point needs. force is for callers like a confirmed community
// reset that must always leave a record of the action even on the rare
// occasion the recomputed values happen to match what was there before.
func AuditEntryTx(ctx context.Context, tx *sql.Tx, meta AuditMeta, objectType, objectID string, before, after any, force bool) error {
	if meta.Actor == "" {
		return nil
	}
	beforeJSON, err := json.Marshal(before)
	if err != nil {
		return err
	}
	afterJSON, err := json.Marshal(after)
	if err != nil {
		return err
	}
	if !force && string(beforeJSON) == string(afterJSON) {
		return nil
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO audit_log (recorded_at, actor, user_agent, action, object_type, object_id, before, after)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		time.Now().UTC().Unix(), meta.Actor, truncateUserAgent(meta.UserAgent), meta.Action, objectType, objectID,
		string(beforeJSON), string(afterJSON))
	return err
}

// PurgeAuditLog deletes entries older than `days`.
func (s *Store) PurgeAuditLog(ctx context.Context, days int) error {
	cutoff := daysCutoff(days)
	_, err := s.DB.ExecContext(ctx, "DELETE FROM audit_log WHERE recorded_at < ?", cutoff)
	return err
}
