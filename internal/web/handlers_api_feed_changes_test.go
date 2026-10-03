package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestAPIFeedSyncChangesEmptyHistoryIsAnArray(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)
	feedID, err := st.AddFeed(t.Context(), "quiet-feed", "https://example.test/quiet.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	idStr := strconv.FormatInt(feedID, 10)
	req := httptest.NewRequest("GET", "/api/admin/feeds/"+idStr+"/sync-changes", nil)
	req.SetPathValue("id", idStr)
	w := httptest.NewRecorder()
	srv.apiFeedSyncChanges(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Changes []json.RawMessage `json:"changes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Changes == nil || len(body.Changes) != 0 {
		t.Fatalf("changes = %s, want an empty array", w.Body.String())
	}
}
