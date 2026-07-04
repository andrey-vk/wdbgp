package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestAdaptersDeleteNotFound guards against apiAdaptersDelete's initial
// s.store.FeedAdapter lookup surfacing sql.ErrNoRows as a raw 500 instead
// of the 404 apiAdaptersGet/apiAdaptersUpdate already return for the same
// nonexistent-ID case.
func TestAdaptersDeleteNotFound(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)

	req := httptest.NewRequest("DELETE", "/api/admin/adapters/99999", nil)
	req.SetPathValue("id", "99999")
	w := httptest.NewRecorder()
	srv.apiAdaptersDelete(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", w.Code, w.Body.String())
	}
}
