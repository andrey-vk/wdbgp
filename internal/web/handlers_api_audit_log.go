package web

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/andrey-vk/wdbgp/internal/store"
)

type auditLogEntryJSON struct {
	ID         int64  `json:"id"`
	RecordedAt string `json:"recorded_at"`
	Actor      string `json:"actor"`
	UserAgent  string `json:"user_agent"`
	Action     string `json:"action"`
	ObjectType string `json:"object_type"`
	ObjectID   string `json:"object_id"`
	Before     string `json:"before"`
	After      string `json:"after"`
}

// apiAuditLogList handles GET /api/admin/audit-log.
// Query params: actor, action, object_type, object_id, since, until (RFC3339),
// limit (default 50, max 200), offset. Returns {entries, total}.
func (s *Server) apiAuditLogList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	filter := store.AuditLogFilter{
		Actor:      strings.TrimSpace(q.Get("actor")),
		Action:     strings.TrimSpace(q.Get("action")),
		ObjectType: strings.TrimSpace(q.Get("object_type")),
		ObjectID:   strings.TrimSpace(q.Get("object_id")),
	}
	if raw := strings.TrimSpace(q.Get("since")); raw != "" {
		since, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "invalid since (want RFC3339)"})
			return
		}
		filter.Since = since
	}
	if raw := strings.TrimSpace(q.Get("until")); raw != "" {
		until, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "invalid until (want RFC3339)"})
			return
		}
		filter.Until = until
	}

	limit := 50
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "invalid limit"})
			return
		}
		limit = parsed
	}
	if limit > 200 {
		limit = 200
	}
	offset := 0
	if raw := strings.TrimSpace(q.Get("offset")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "invalid offset"})
			return
		}
		offset = parsed
	}

	entries, total, err := s.store.ListAuditLog(r.Context(), filter, limit, offset)
	if err != nil {
		s.internalError(w, r, err)
		return
	}

	jsonEntries := make([]auditLogEntryJSON, 0, len(entries))
	for _, e := range entries {
		jsonEntries = append(jsonEntries, auditLogEntryJSON{
			ID:         e.ID,
			RecordedAt: e.RecordedAt.Format(time.RFC3339),
			Actor:      e.Actor,
			UserAgent:  e.UserAgent,
			Action:     e.Action,
			ObjectType: e.ObjectType,
			ObjectID:   e.ObjectID,
			Before:     e.Before,
			After:      e.After,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"entries": jsonEntries,
		"total":   total,
	})
}
