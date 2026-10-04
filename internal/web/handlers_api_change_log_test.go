package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/andrey-vk/wdbgp/internal/store"
)

func changeLogRequest(t *testing.T, st *store.Store, userID int64) *http.Request {
	t.Helper()
	user, err := st.User(context.Background(), userID)
	if err != nil {
		t.Fatalf("read user: %v", err)
	}
	req := httptest.NewRequest("GET", "/api/user/change-log", nil)
	return req.WithContext(context.WithValue(req.Context(), userCtxKey{}, user))
}

func TestAPIUserChangeLogEmptyIsAnArray(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)
	userID, err := st.AddUser(t.Context(), store.User{Name: "quiet", PeerIP: "172.16.7.1", PeerASN: 65077, Enabled: true,
		FilterMode: store.FilterModeGlobal, CatalogModeID: store.DefaultCatalogModeID, Networks: []string{"192.168.7.0/24"}})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	srv.apiUserChangeLog(w, changeLogRequest(t, st, userID))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Entries []json.RawMessage `json:"entries"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Entries == nil || len(body.Entries) != 0 {
		t.Fatalf("entries = %s, want an empty array", w.Body.String())
	}
}

// TestAPIUserChangeLogShowsSelfChangeWithoutAdminAddress checks the response
// shape end to end: the user's own change comes back attributed to "self",
// with the names it touched, and an admin's audit actor never appears.
func TestAPIUserChangeLogShowsSelfChangeWithoutAdminAddress(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)
	ctx := t.Context()
	userID, err := st.AddUser(ctx, store.User{Name: "chosen", PeerIP: "172.16.7.2", PeerASN: 65078, Enabled: true,
		FilterMode: store.FilterModeGlobal, CatalogModeID: store.DefaultCatalogModeID, Networks: []string{"192.168.8.0/24"}})
	if err != nil {
		t.Fatal(err)
	}
	uid := strconv.FormatInt(userID, 10)
	if _, _, _, _, _, err := st.SaveUserSelectionCounts(ctx, userID, store.DefaultCatalogModeID, false,
		[]store.CategoryToggle{{Category: "ai", Checked: true}}, nil,
		store.AuditMeta{}, store.AuditMeta{Actor: "user:" + uid, Action: "user.selections_changed"}); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordAuditLog(ctx, store.AuditLogEntry{
		Actor: "admin:198.51.100.9", Action: "route_filters.user_updated", ObjectType: "user", ObjectID: uid,
		Before: `{"allow":{"entries":[],"truncated":0},"deny":{"entries":[],"truncated":0}}`,
		After:  `{"allow":{"entries":["10.0.0.0/8"],"truncated":0},"deny":{"entries":[],"truncated":0}}`,
	}); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	srv.apiUserChangeLog(w, changeLogRequest(t, st, userID))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if strings.Contains(body, "198.51.100.9") {
		t.Fatalf("admin address leaked into the user's change log: %s", body)
	}
	var resp struct {
		Entries []store.UserChangeEntry `json:"entries"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Entries) != 2 {
		t.Fatalf("entries = %s, want two", body)
	}
	if resp.Entries[0].Source != "admin" || resp.Entries[0].Kind != "route_filters" {
		t.Fatalf("newest entry = %+v, want admin route_filters", resp.Entries[0])
	}
	if resp.Entries[1].Source != "self" || len(resp.Entries[1].Added.Categories) != 1 || resp.Entries[1].Added.Categories[0] != "ai" {
		t.Fatalf("self entry = %+v, want self added ai", resp.Entries[1])
	}
}
