package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/andrey-vk/wdbgp/internal/settings"
	"github.com/andrey-vk/wdbgp/internal/store"
)

func TestAPIConfigExportShapesResponse(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)
	if _, err := st.AddUser(t.Context(), store.User{
		Name: "alice", PeerIP: "20.0.0.1", PeerASN: 65001, Enabled: true,
		FilterMode: store.FilterModeGlobal, CatalogModeID: store.DefaultCatalogModeID,
	}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/api/admin/config/export", nil)
	w := httptest.NewRecorder()
	srv.apiConfigExport(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var snap store.ConfigSnapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatal(err)
	}
	if len(snap.Users) != 1 || snap.Users[0].Name != "alice" {
		t.Fatalf("users = %+v, want [alice]", snap.Users)
	}
}

func TestAPIConfigDiffIsPure(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)
	body, err := json.Marshal(map[string]any{
		"a": store.ConfigSnapshot{Users: []store.ConfigUser{{Name: "alice", CatalogMode: "OpenCCK", Enabled: true}}},
		"b": store.ConfigSnapshot{Users: []store.ConfigUser{{Name: "alice", CatalogMode: "OpenCCK", Enabled: false}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/admin/config/diff", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.apiConfigDiff(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Diff store.ConfigDiff `json:"diff"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Diff.Users.Changed) != 1 || resp.Diff.Users.Changed[0].Name != "alice" {
		t.Fatalf("changed = %+v, want [alice]", resp.Diff.Users.Changed)
	}
}

func TestAPIConfigImportPreviewIsReadOnly(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)
	snapshot := store.ConfigSnapshot{
		Modes: []store.ConfigMode{{Name: "Imported", Enabled: true}},
		Users: []store.ConfigUser{{Name: "carol", PeerIP: "20.0.0.3", PeerASN: 65003, CatalogMode: "Imported", Enabled: true}},
	}
	body, err := json.Marshal(map[string]any{"snapshot": snapshot})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/admin/config/import/preview", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.apiConfigImportPreview(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Diff   store.ConfigDiff `json:"diff"`
		Digest string           `json:"digest"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Digest == "" {
		t.Fatal("digest is empty")
	}
	if len(resp.Diff.Modes.Added) != 1 || len(resp.Diff.Users.Added) != 1 {
		t.Fatalf("diff = %+v, want one added mode and one added user", resp.Diff)
	}

	var userCount int
	if err := st.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM users WHERE name = 'carol'").Scan(&userCount); err != nil {
		t.Fatal(err)
	}
	if userCount != 0 {
		t.Fatal("preview must not write anything: carol should not exist yet")
	}
}

func TestAPIConfigImportRejectsStaleDigest(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)
	snapshot := store.ConfigSnapshot{Users: []store.ConfigUser{{Name: "carol", PeerIP: "20.0.0.3", PeerASN: 65003, CatalogMode: "OpenCCK", Enabled: true}}}
	body, err := json.Marshal(map[string]any{"snapshot": snapshot, "digest": "not-the-real-digest"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/admin/config/import", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.apiConfigImport(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s, want 409", w.Code, w.Body.String())
	}
}

func TestAPIConfigImportAppliesSnapshotAndGlobalFilters(t *testing.T) {
	// Builds its own server, with settings backed by the SAME store as
	// s.store (as production wires them via settings.New(db) in
	// cmd/wdbgp/main.go), instead of setupUserTestServer's testSettings(),
	// which deliberately backs settings with its own disconnected in-memory
	// map — fine for settings-only tests, but it would never show this
	// test's writes reflected in st's real database.
	st := setupUserTestStore(t)
	set, err := settings.New(st)
	if err != nil {
		t.Fatal(err)
	}
	bgp := &fakeBGP{}
	srv := &Server{settings: set, store: st, bgp: bgp}

	snapshot := store.ConfigSnapshot{
		GlobalFilters: store.RouteFilters{Allow: []string{"10.0.0.0/8"}},
		Users: []store.ConfigUser{
			{Name: "carol", PeerIP: "20.0.0.3", PeerASN: 65003, CatalogMode: "OpenCCK", Enabled: true},
		},
	}
	digest, err := snapshot.Digest()
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{"snapshot": snapshot, "digest": digest})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/admin/config/import", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.apiConfigImport(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Result               store.ConfigApplyResult `json:"result"`
		GlobalFiltersApplied bool                    `json:"global_filters_applied"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Result.UsersCreated) != 1 || resp.Result.UsersCreated[0] != "carol" {
		t.Fatalf("users created = %+v, want [carol]", resp.Result.UsersCreated)
	}
	if !resp.GlobalFiltersApplied {
		t.Fatal("global_filters_applied = false, want true: the snapshot's filters differ from the empty default")
	}

	filters, err := st.GlobalRouteFilters(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(filters.Allow) != 1 || filters.Allow[0] != "10.0.0.0/8" {
		t.Fatalf("stored global filters = %+v, want allow 10.0.0.0/8", filters)
	}
	if srv.settings.FilterAllow.Get() != "10.0.0.0/8" {
		t.Fatalf("cached FilterAllow = %q, want 10.0.0.0/8 (SetTx must update the in-memory cache)", srv.settings.FilterAllow.Get())
	}
	if bgp.reconciles != 1 {
		t.Fatalf("bgp.reconciles = %d, want 1", bgp.reconciles)
	}

	// Re-applying the same snapshot is a no-op for global filters.
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("POST", "/api/admin/config/import", bytes.NewReader(body))
	srv.apiConfigImport(w2, req2)
	var resp2 struct {
		GlobalFiltersApplied bool `json:"global_filters_applied"`
	}
	if err := json.Unmarshal(w2.Body.Bytes(), &resp2); err != nil {
		t.Fatal(err)
	}
	if resp2.GlobalFiltersApplied {
		t.Fatal("global_filters_applied = true on a repeat import with unchanged filters, want false")
	}
}
