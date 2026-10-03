package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/andrey-vk/wdbgp/internal/logging"
	"github.com/andrey-vk/wdbgp/internal/store"
)

// adminActor builds the "admin:<ip>" actor string for an admin-triggered
// mutation. There is no per-admin identity in this codebase today (the
// admin session is a single shared password/token, see session.go) — IP
// is the only "who" signal available.
func (s *Server) adminActor(r *http.Request) string {
	return "admin:" + s.clientIP(r)
}

// userActor builds the "user:<id>" actor string for a self-service
// mutation made by an authenticated end user.
func userActor(userID int64) string {
	return "user:" + strconv.FormatInt(userID, 10)
}

// recordAudit writes one audit log entry, logging and continuing on
// failure — an audit-log write must never block or fail the mutation it
// records.
func (s *Server) recordAudit(ctx context.Context, r *http.Request, e store.AuditLogEntry) {
	e.UserAgent = r.Header.Get("User-Agent")
	if err := s.store.RecordAuditLog(ctx, e); err != nil {
		logging.FromContext(ctx).Error("audit log write failed", "action", e.Action, "error", err)
	}
}

// recordAuditIfChanged marshals before/after to JSON and records an audit
// entry only when they differ — the shared "no spurious row when nothing
// actually changed" guard used by every hook point, so a handler called
// with a no-op payload (e.g. re-submitting the same value) never writes a
// misleading row.
func (s *Server) recordAuditIfChanged(ctx context.Context, r *http.Request, actor, action, objectType, objectID string, before, after any) {
	beforeJSON, err := json.Marshal(before)
	if err != nil {
		logging.FromContext(ctx).Error("audit log marshal before failed", "action", action, "error", err)
		return
	}
	afterJSON, err := json.Marshal(after)
	if err != nil {
		logging.FromContext(ctx).Error("audit log marshal after failed", "action", action, "error", err)
		return
	}
	if string(beforeJSON) == string(afterJSON) {
		return
	}
	s.recordAudit(ctx, r, store.AuditLogEntry{
		Actor: actor, Action: action, ObjectType: objectType, ObjectID: objectID,
		Before: string(beforeJSON), After: string(afterJSON),
	})
}
