package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/andrey-vk/wdbgp/internal/store"
)

func TestAPIUserPrefixHistoryReturnsRecordedSnapshots(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)
	userID, err := st.AddUser(t.Context(), store.User{
		Name: "alice", PeerIP: "20.0.0.1", PeerASN: 65001, Enabled: true,
		FilterMode: store.FilterModeGlobal, CatalogModeID: store.DefaultCatalogModeID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RecordUserPrefixSnapshot(t.Context(), userID, 10, 2); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/api/admin/users/x/prefix-history", nil)
	req.SetPathValue("id", "999999")
	w := httptest.NewRecorder()
	srv.apiUserPrefixHistory(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for an unknown user", w.Code)
	}

	req2 := httptest.NewRequest("GET", "/api/admin/users/x/prefix-history", nil)
	req2.SetPathValue("id", strconv.FormatInt(userID, 10))
	w2 := httptest.NewRecorder()
	srv.apiUserPrefixHistory(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w2.Code, w2.Body.String())
	}
	var resp struct {
		History []store.UserPrefixSnapshot `json:"history"`
	}
	if err := json.Unmarshal(w2.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.History) != 1 || resp.History[0].V4Count != 10 || resp.History[0].V6Count != 2 {
		t.Fatalf("history = %+v, want one row {v4:10 v6:2}", resp.History)
	}
}

func TestAPIUserPrefixHistoryAppliesRetentionFallbackForNonPositiveDays(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)
	userID, err := st.AddUser(t.Context(), store.User{
		Name: "alice", PeerIP: "20.0.0.1", PeerASN: 65001, Enabled: true,
		FilterMode: store.FilterModeGlobal, CatalogModeID: store.DefaultCatalogModeID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RecordUserPrefixSnapshot(t.Context(), userID, 10, 2); err != nil {
		t.Fatal(err)
	}
	// MetricsHistoryDays has no range validator — an admin can set it to 0.
	if err := srv.settings.MetricsHistoryDays.Set(t.Context(), 0); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/api/admin/users/x/prefix-history", nil)
	req.SetPathValue("id", strconv.FormatInt(userID, 10))
	w := httptest.NewRecorder()
	srv.apiUserPrefixHistory(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		History []store.UserPrefixSnapshot `json:"history"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.History) != 1 {
		t.Fatalf("history = %+v, want the just-recorded snapshot despite metrics_history_days=0 (falls back to 14 days, same as purgeLoop)", resp.History)
	}
}

func TestAPISettingsPurgeMetricsClearsUserPrefixHistory(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)
	userID, err := st.AddUser(t.Context(), store.User{
		Name: "alice", PeerIP: "20.0.0.1", PeerASN: 65001, Enabled: true,
		FilterMode: store.FilterModeGlobal, CatalogModeID: store.DefaultCatalogModeID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RecordUserPrefixSnapshot(t.Context(), userID, 10, 2); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("POST", "/api/admin/settings/purge-metrics", nil)
	w := httptest.NewRecorder()
	srv.apiSettingsPurgeMetrics(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}

	history, err := st.UserPrefixHistory(t.Context(), userID, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 0 {
		t.Fatalf("history = %+v, want empty after a manual metrics purge", history)
	}
}
