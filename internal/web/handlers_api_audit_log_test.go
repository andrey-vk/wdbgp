package web

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/andrey-vk/wdbgp/internal/store"
)

func seedAuditLog(t *testing.T, st *store.Store, entries ...store.AuditLogEntry) {
	t.Helper()
	ctx := context.Background()
	for _, e := range entries {
		if err := st.RecordAuditLog(ctx, e); err != nil {
			t.Fatalf("seed audit log: %v", err)
		}
	}
}

func TestAuditLogListDefaultsAndShape(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)
	seedAuditLog(t, st,
		store.AuditLogEntry{Actor: "admin:1.1.1.1", Action: "feed.enabled_changed", ObjectType: "feed", ObjectID: "1", Before: `{"enabled":true}`, After: `{"enabled":false}`},
	)

	req := httptest.NewRequest("GET", "/api/admin/audit-log", nil)
	w := httptest.NewRecorder()
	srv.apiAuditLogList(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Entries []auditLogEntryJSON `json:"entries"`
		Total   int                 `json:"total"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Total != 1 || len(resp.Entries) != 1 {
		t.Fatalf("total=%d len=%d, want 1 and 1", resp.Total, len(resp.Entries))
	}
	e := resp.Entries[0]
	if e.Action != "feed.enabled_changed" || e.ObjectType != "feed" || e.ObjectID != "1" ||
		e.Before != `{"enabled":true}` || e.After != `{"enabled":false}` || e.Actor != "admin:1.1.1.1" {
		t.Fatalf("entry = %+v, unexpected shape", e)
	}
	if e.RecordedAt == "" {
		t.Fatal("recorded_at is empty")
	}
}

func TestAuditLogListFilters(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)
	seedAuditLog(t, st,
		store.AuditLogEntry{Actor: "admin:1.1.1.1", Action: "feed.enabled_changed", ObjectType: "feed", ObjectID: "1"},
		store.AuditLogEntry{Actor: "user:42", Action: "user.mode_changed", ObjectType: "user", ObjectID: "42"},
	)

	req := httptest.NewRequest("GET", "/api/admin/audit-log?action=user.mode_changed", nil)
	w := httptest.NewRecorder()
	srv.apiAuditLogList(w, req)

	var resp struct {
		Entries []auditLogEntryJSON `json:"entries"`
		Total   int                 `json:"total"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Total != 1 || resp.Entries[0].Action != "user.mode_changed" {
		t.Fatalf("resp = %+v, want only the user.mode_changed entry", resp)
	}
}

func TestAuditLogListPagination(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)
	for i := 0; i < 5; i++ {
		seedAuditLog(t, st, store.AuditLogEntry{Actor: "a", Action: "a", ObjectType: "t", ObjectID: "1"})
	}

	req := httptest.NewRequest("GET", "/api/admin/audit-log?limit=2&offset=0", nil)
	w := httptest.NewRecorder()
	srv.apiAuditLogList(w, req)

	var resp struct {
		Entries []auditLogEntryJSON `json:"entries"`
		Total   int                 `json:"total"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Total != 5 || len(resp.Entries) != 2 {
		t.Fatalf("total=%d len=%d, want 5 and 2", resp.Total, len(resp.Entries))
	}
}

func TestAuditLogListInvalidLimitAndSince(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)

	for _, query := range []string{"?limit=0", "?limit=-1", "?limit=abc", "?offset=-1", "?since=not-a-date", "?until=not-a-date"} {
		req := httptest.NewRequest("GET", "/api/admin/audit-log"+query, nil)
		w := httptest.NewRecorder()
		srv.apiAuditLogList(w, req)
		if w.Code != 400 {
			t.Errorf("query %q: status = %d, want 400", query, w.Code)
		}
	}
}

func TestAuditLogListLimitCappedAt200(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)
	seedAuditLog(t, st, store.AuditLogEntry{Actor: "a", Action: "a", ObjectType: "t", ObjectID: "1"})

	req := httptest.NewRequest("GET", "/api/admin/audit-log?limit=10000", nil)
	w := httptest.NewRecorder()
	srv.apiAuditLogList(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
}
