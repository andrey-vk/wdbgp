package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAPICommunityMatrixShapesResponse(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/admin/communities/matrix", nil)
	srv.apiCommunityMatrix(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Modes      []json.RawMessage `json:"modes"`
		Categories []json.RawMessage `json:"categories"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Modes) == 0 {
		t.Fatalf("modes = %s, want the catalog modes", w.Body.String())
	}
	if body.Categories == nil {
		t.Fatalf("categories = %s, want an array, not null", w.Body.String())
	}
}
