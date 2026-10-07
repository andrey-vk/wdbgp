package web

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/andrey-vk/wdbgp/internal/store"
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

func TestAPICommunityMatrixGenerateFillsMissingNumbers(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)
	modeID, err := st.AddCatalogMode(t.Context(), "Lab", true)
	if err != nil {
		t.Fatal(err)
	}
	feedID, err := st.AddFeed(t.Context(), "matrix-gen-feed", "https://example.test/matrix-gen.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.ExecContext(t.Context(), "INSERT INTO catalog_mode_feeds(mode_id, feed_id) VALUES (?, ?)", modeID, feedID); err != nil {
		t.Fatal(err)
	}
	if err := st.Transaction(t.Context(), func(tx *sql.Tx) error {
		return store.ReplaceCatalogEntries(t.Context(), tx, feedID, []store.CatalogEntry{{Category: "ai", Service: "x", CIDR: "20.0.0.0/24"}})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CommunityMatrix(t.Context()); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("POST", "/api/admin/communities/matrix/generate", nil)
	w := httptest.NewRecorder()
	srv.apiCommunityMatrixGenerate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}

	matrix, err := st.CommunityMatrix(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range matrix.Categories {
		if row.Category == "ai" && row.Values[modeID] == nil {
			t.Fatalf("ai in mode %d still missing after generate: %+v", modeID, row)
		}
	}
}
