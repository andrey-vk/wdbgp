package web

import (
	"bytes"
	"encoding/json"
	"errors"
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

// configImportTestServer wires settings to the SAME store as s.store, as
// production does (settings.New(db) in cmd/wdbgp/main.go) — unlike
// setupUserTestServer's testSettings(), which deliberately backs settings
// with its own disconnected in-memory map and would never show a test's
// settings writes reflected in st's real database.
func configImportTestServer(t *testing.T) (*Server, *store.Store, *fakeBGP) {
	t.Helper()
	st := setupUserTestStore(t)
	set, err := settings.New(st)
	if err != nil {
		t.Fatal(err)
	}
	bgp := &fakeBGP{}
	return &Server{settings: set, store: st, bgp: bgp}, st, bgp
}

func previewImport(t *testing.T, srv *Server, snapshot store.ConfigSnapshot) (store.ConfigDiff, string) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"snapshot": snapshot})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/admin/config/import/preview", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.apiConfigImportPreview(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("preview status = %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Diff   store.ConfigDiff `json:"diff"`
		Digest string           `json:"digest"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp.Diff, resp.Digest
}

func TestAPIConfigImportPreviewIsReadOnly(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)
	snapshot := store.ConfigSnapshot{
		SchemaVersion: store.ConfigSnapshotSchemaVersion,
		Modes:         []store.ConfigMode{{Name: "Imported", Enabled: true}},
		Users:         []store.ConfigUser{{Name: "carol", PeerIP: "20.0.0.3", PeerASN: 65003, CatalogMode: "Imported", Enabled: true}},
	}
	diff, digest := previewImport(t, srv, snapshot)
	if digest == "" {
		t.Fatal("digest is empty")
	}
	if len(diff.Modes.Added) != 1 || len(diff.Users.Added) != 1 {
		t.Fatalf("diff = %+v, want one added mode and one added user", diff)
	}

	var userCount int
	if err := st.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM users WHERE name = 'carol'").Scan(&userCount); err != nil {
		t.Fatal(err)
	}
	if userCount != 0 {
		t.Fatal("preview must not write anything: carol should not exist yet")
	}
}

func doImport(snapshot store.ConfigSnapshot, digest string) *http.Request {
	body, _ := json.Marshal(map[string]any{"snapshot": snapshot, "digest": digest}) //nolint:errcheck // fixed struct, cannot fail
	return httptest.NewRequest("POST", "/api/admin/config/import", bytes.NewReader(body))
}

func TestAPIConfigImportRejectsStaleDigest(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)
	snapshot := store.ConfigSnapshot{
		SchemaVersion: store.ConfigSnapshotSchemaVersion,
		Modes:         []store.ConfigMode{},
		Users:         []store.ConfigUser{{Name: "carol", PeerIP: "20.0.0.3", PeerASN: 65003, CatalogMode: "OpenCCK", Enabled: true}},
	}
	w := httptest.NewRecorder()
	srv.apiConfigImport(w, doImport(snapshot, "not-the-real-digest"))
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s, want 409", w.Code, w.Body.String())
	}
}

func TestAPIConfigImportRejectsUnsupportedSchemaVersion(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)
	// The zero value — e.g. an empty {} file — must not be accepted as a
	// valid, digest-able v1 document: every field would silently decode as
	// its zero value, including global filters that could then be cleared.
	snapshot := store.ConfigSnapshot{Users: []store.ConfigUser{{Name: "carol", CatalogMode: "OpenCCK", Enabled: true}}}

	body, err := json.Marshal(map[string]any{"snapshot": snapshot})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	srv.apiConfigImportPreview(w, httptest.NewRequest("POST", "/api/admin/config/import/preview", bytes.NewReader(body)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("preview status = %d body=%s, want 400", w.Code, w.Body.String())
	}

	w2 := httptest.NewRecorder()
	srv.apiConfigImport(w2, doImport(snapshot, "anything"))
	if w2.Code != http.StatusBadRequest {
		t.Fatalf("import status = %d body=%s, want 400", w2.Code, w2.Body.String())
	}
}

func TestAPIConfigImportRejectsDriftedLiveTarget(t *testing.T) {
	srv, st, _ := configImportTestServer(t)
	snapshot := store.ConfigSnapshot{
		SchemaVersion: store.ConfigSnapshotSchemaVersion,
		Modes:         []store.ConfigMode{},
		Users:         []store.ConfigUser{{Name: "carol", PeerIP: "20.0.0.3", PeerASN: 65003, CatalogMode: "OpenCCK", Enabled: true}},
	}
	_, digest := previewImport(t, srv, snapshot)

	// The live configuration changes after the preview but before the apply
	// (another admin's edit) — the digest must no longer match, even though
	// the uploaded snapshot itself is byte-for-byte what was previewed.
	if _, err := st.AddUser(t.Context(), store.User{
		Name: "dave", PeerIP: "20.0.0.4", PeerASN: 65004, Enabled: true,
		FilterMode: store.FilterModeGlobal, CatalogModeID: store.DefaultCatalogModeID,
	}); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	srv.apiConfigImport(w, doImport(snapshot, digest))
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s, want 409 (the live target drifted since the preview)", w.Code, w.Body.String())
	}

	var carolCount int
	if err := st.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM users WHERE name = 'carol'").Scan(&carolCount); err != nil {
		t.Fatal(err)
	}
	if carolCount != 0 {
		t.Fatal("carol was created despite the rejected, drifted apply")
	}
}

func TestAPIConfigImportRejectsInvalidGlobalFilterBeforeCommitting(t *testing.T) {
	srv, st, _ := configImportTestServer(t)
	snapshot := store.ConfigSnapshot{
		SchemaVersion: store.ConfigSnapshotSchemaVersion,
		GlobalFilters: store.RouteFilters{Allow: []string{"not-a-cidr"}},
		Modes:         []store.ConfigMode{},
		Users:         []store.ConfigUser{{Name: "carol", PeerIP: "20.0.0.3", PeerASN: 65003, CatalogMode: "OpenCCK", Enabled: true}},
	}
	_, digest := previewImport(t, srv, snapshot)

	w := httptest.NewRecorder()
	srv.apiConfigImport(w, doImport(snapshot, digest))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s, want 400 for an invalid global filter CIDR", w.Code, w.Body.String())
	}

	// Rejected before anything committed: carol must not have been created
	// despite the user portion of the snapshot being perfectly valid on its
	// own — an import must not partially apply behind a response that says
	// it failed.
	var carolCount int
	if err := st.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM users WHERE name = 'carol'").Scan(&carolCount); err != nil {
		t.Fatal(err)
	}
	if carolCount != 0 {
		t.Fatal("carol was created even though the import was rejected for an invalid global filter")
	}
}

func TestAPIConfigImportAppliesSnapshotAndGlobalFilters(t *testing.T) {
	srv, st, bgp := configImportTestServer(t)
	snapshot := store.ConfigSnapshot{
		SchemaVersion: store.ConfigSnapshotSchemaVersion,
		GlobalFilters: store.RouteFilters{Allow: []string{"10.0.0.0/8"}},
		Modes:         []store.ConfigMode{},
		Users: []store.ConfigUser{
			{Name: "carol", PeerIP: "20.0.0.3", PeerASN: 65003, CatalogMode: "OpenCCK", Enabled: true},
		},
	}
	_, digest := previewImport(t, srv, snapshot)

	w := httptest.NewRecorder()
	srv.apiConfigImport(w, doImport(snapshot, digest))
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
	// carol is new and enabled: syncBGPAfterConfigImport must have reloaded
	// her into the BGP manager's own peer cache, not relied on Reconcile
	// alone (Reconcile only reconciles routes against peers it already has).
	if bgp.updates != 1 {
		t.Fatalf("bgp.updates = %d, want 1 (carol synced via UpdatePeer, which also handles a not-yet-tracked peer)", bgp.updates)
	}

	// Re-applying the same snapshot: still valid (nothing has drifted, same
	// uploaded bytes), but a no-op for global filters.
	_, digest2 := previewImport(t, srv, snapshot)
	w2 := httptest.NewRecorder()
	srv.apiConfigImport(w2, doImport(snapshot, digest2))
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

func TestAPIConfigImportSyncsDisabledPeerWithBGP(t *testing.T) {
	srv, st, bgp := configImportTestServer(t)
	userID, err := st.AddUser(t.Context(), store.User{
		Name: "carol", PeerIP: "20.0.0.3", PeerASN: 65003, Enabled: true,
		FilterMode: store.FilterModeGlobal, CatalogModeID: store.DefaultCatalogModeID,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = userID

	snapshot := store.ConfigSnapshot{
		SchemaVersion: store.ConfigSnapshotSchemaVersion,
		Modes:         []store.ConfigMode{},
		Users: []store.ConfigUser{
			{Name: "carol", PeerIP: "20.0.0.3", PeerASN: 65003, CatalogMode: "OpenCCK", Enabled: false},
		},
	}
	_, digest := previewImport(t, srv, snapshot)
	w := httptest.NewRecorder()
	srv.apiConfigImport(w, doImport(snapshot, digest))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	if bgp.deletes != 1 {
		t.Fatalf("bgp.deletes = %d, want 1 (carol was disabled by the import)", bgp.deletes)
	}
}

func TestApplyGlobalFiltersFromImportRejectsDriftedTarget(t *testing.T) {
	srv, _, _ := configImportTestServer(t)
	req := httptest.NewRequest("POST", "/api/admin/config/import", nil)

	// before deliberately doesn't match the live value (empty, by default on
	// a fresh store) — simulating another admin's edit landing between
	// apiConfigImport's own read of "previous filters" and this call.
	err := srv.applyGlobalFiltersFromImport(req, store.RouteFilters{Allow: []string{"10.0.0.0/8"}},
		store.RouteFilters{Allow: []string{"not-what-is-actually-there"}})
	if !errors.Is(err, store.ErrConfigImportStale) {
		t.Fatalf("err = %v, want ErrConfigImportStale", err)
	}
}

func TestAPIConfigImportReportsPartialSuccessWhenFiltersDrift(t *testing.T) {
	srv, st, bgp := configImportTestServer(t)
	snapshot := store.ConfigSnapshot{
		SchemaVersion: store.ConfigSnapshotSchemaVersion,
		GlobalFilters: store.RouteFilters{Allow: []string{"10.0.0.0/8"}},
		Modes:         []store.ConfigMode{},
		Users: []store.ConfigUser{
			{Name: "carol", PeerIP: "20.0.0.3", PeerASN: 65003, CatalogMode: "OpenCCK", Enabled: true},
		},
	}
	_, digest := previewImport(t, srv, snapshot)

	// Simulates a concurrent admin edit to the global filters landing after
	// Store.ApplyConfigSnapshot's own digest check already passed (so the
	// entity changes below do commit) but before the separate filters step
	// re-reads the live value.
	apiConfigImportPreFiltersHook = func() {
		if err := srv.settings.FilterAllow.Set(t.Context(), "192.168.0.0/16"); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { apiConfigImportPreFiltersHook = nil })

	w := httptest.NewRecorder()
	srv.apiConfigImport(w, doImport(snapshot, digest))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200: entities already committed, so this must not report overall failure", w.Code, w.Body.String())
	}
	var resp struct {
		Result               store.ConfigApplyResult `json:"result"`
		GlobalFiltersApplied bool                    `json:"global_filters_applied"`
		GlobalFiltersError   string                  `json:"global_filters_error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.GlobalFiltersApplied {
		t.Fatal("global_filters_applied = true, want false: the drifted target must not have been overwritten")
	}
	if resp.GlobalFiltersError == "" {
		t.Fatal("global_filters_error is empty, want an explanation of the drift")
	}
	if len(resp.Result.UsersCreated) != 1 || resp.Result.UsersCreated[0] != "carol" {
		t.Fatalf("users created = %+v, want [carol]: the entity changes must still have committed", resp.Result.UsersCreated)
	}
	if bgp.updates != 1 {
		t.Fatalf("bgp.updates = %d, want 1: BGP sync must still run for the entities that did commit", bgp.updates)
	}
	if srv.settings.FilterAllow.Get() != "192.168.0.0/16" {
		t.Fatalf("FilterAllow = %q, want the concurrent edit left untouched, not overwritten", srv.settings.FilterAllow.Get())
	}

	var carolCount int
	if err := st.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM users WHERE name = 'carol'").Scan(&carolCount); err != nil {
		t.Fatal(err)
	}
	if carolCount != 1 {
		t.Fatal("carol was not actually created despite the reported success")
	}
}

func TestAPIConfigImportRejectsIncompleteSchemaV1Document(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)
	// A document that merely claims schema_version 1 without the rest of
	// the v1 shape: global_filters, modes, and users all decode to their
	// zero value, and confirming it would clear every global filter.
	incomplete := []byte(`{"schema_version":1}`)

	w := httptest.NewRecorder()
	body, err := json.Marshal(map[string]json.RawMessage{"snapshot": incomplete})
	if err != nil {
		t.Fatal(err)
	}
	srv.apiConfigImportPreview(w, httptest.NewRequest("POST", "/api/admin/config/import/preview", bytes.NewReader(body)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("preview status = %d body=%s, want 400", w.Code, w.Body.String())
	}

	w2 := httptest.NewRecorder()
	body2, err := json.Marshal(map[string]any{"snapshot": json.RawMessage(incomplete), "digest": "anything"})
	if err != nil {
		t.Fatal(err)
	}
	srv.apiConfigImport(w2, httptest.NewRequest("POST", "/api/admin/config/import", bytes.NewReader(body2)))
	if w2.Code != http.StatusBadRequest {
		t.Fatalf("import status = %d body=%s, want 400", w2.Code, w2.Body.String())
	}
}

func TestAPIConfigImportRejectsIncompleteGlobalFilters(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)
	// global_filters itself is present and non-null, passing the top-level
	// completeness check — but its own allow/deny keys are missing, which
	// decodes to an empty RouteFilters and would silently clear both on a
	// confirmed import, the exact same class of bug the top-level check
	// above exists to prevent, one level deeper.
	incomplete := []byte(`{"schema_version":1,"global_filters":{},"modes":[],"users":[]}`)

	w := httptest.NewRecorder()
	body, err := json.Marshal(map[string]json.RawMessage{"snapshot": incomplete})
	if err != nil {
		t.Fatal(err)
	}
	srv.apiConfigImportPreview(w, httptest.NewRequest("POST", "/api/admin/config/import/preview", bytes.NewReader(body)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("preview status = %d body=%s, want 400", w.Code, w.Body.String())
	}

	w2 := httptest.NewRecorder()
	body2, err := json.Marshal(map[string]any{"snapshot": json.RawMessage(incomplete), "digest": "anything"})
	if err != nil {
		t.Fatal(err)
	}
	srv.apiConfigImport(w2, httptest.NewRequest("POST", "/api/admin/config/import", bytes.NewReader(body2)))
	if w2.Code != http.StatusBadRequest {
		t.Fatalf("import status = %d body=%s, want 400", w2.Code, w2.Body.String())
	}
}
