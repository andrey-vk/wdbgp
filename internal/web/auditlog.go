package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/andrey-vk/wdbgp/internal/logging"
	"github.com/andrey-vk/wdbgp/internal/store"
)

// auditWriteTimeout bounds the detached audit-log write in recordAudit —
// long enough for a single INSERT, short enough that a stuck store doesn't
// leak goroutines indefinitely once detached from the request's own
// deadline.
const auditWriteTimeout = 5 * time.Second

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
// records. A nil store (some tests construct a *Server without one, for
// handlers that otherwise don't need it, e.g. settings-only tests) is
// treated the same as a write failure: skipped, not panicked on.
func (s *Server) recordAudit(ctx context.Context, r *http.Request, e store.AuditLogEntry) {
	if s.store == nil {
		return
	}
	e.UserAgent = r.Header.Get("User-Agent")
	// Detached from ctx's own cancellation: by this point the mutation
	// being audited has already committed, so a client that disconnects
	// right after (canceling the request context) must not also suppress
	// the record of that already-committed change. The timeout bounds the
	// write now that it's no longer tied to the request's own deadline.
	auditCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), auditWriteTimeout)
	defer cancel()
	if err := s.store.RecordAuditLog(auditCtx, e); err != nil {
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

// diffCommunityRows returns only the (category, service) assignments whose
// community number actually differs between before and after. The store
// layer's before/after returns are always a full snapshot of every
// assignment in the mode (needed there to compute the change itself), but
// logging that whole snapshot on every edit — most of it unchanged — would
// add a full catalog's worth of JSON to audit_log per edit on a large mode.
// Order is preserved from before, then any after-only keys.
func diffCommunityRows(before, after []store.Community) (changedBefore, changedAfter []store.Community) {
	type key = store.ServiceKey
	afterByKey := make(map[key]store.Community, len(after))
	for _, c := range after {
		afterByKey[key{Category: c.Category, Service: c.Service}] = c
	}
	seen := make(map[key]bool, len(before))
	for _, b := range before {
		k := key{Category: b.Category, Service: b.Service}
		seen[k] = true
		a, ok := afterByKey[k]
		if ok && a.Community == b.Community {
			continue
		}
		changedBefore = append(changedBefore, b)
		if ok {
			changedAfter = append(changedAfter, a)
		}
	}
	for _, a := range after {
		k := key{Category: a.Category, Service: a.Service}
		if seen[k] {
			continue
		}
		changedAfter = append(changedAfter, a)
	}
	return changedBefore, changedAfter
}
