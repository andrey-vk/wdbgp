package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/andrey-vk/wdbgp/internal/settings"
	"github.com/andrey-vk/wdbgp/internal/store"
)

// latestAudit returns the single most recent audit log entry, failing the
// test if there isn't exactly one matching action.
func latestAuditByAction(t *testing.T, st *store.Store, action string) store.AuditLogEntry {
	t.Helper()
	entries, total, err := st.ListAuditLog(context.Background(), store.AuditLogFilter{Action: action}, 50, 0)
	if err != nil {
		t.Fatalf("list audit log: %v", err)
	}
	if total != 1 {
		t.Fatalf("action %q: total = %d, want exactly 1", action, total)
	}
	return entries[0]
}

func auditLogCount(t *testing.T, st *store.Store, action string) int {
	t.Helper()
	_, total, err := st.ListAuditLog(context.Background(), store.AuditLogFilter{Action: action}, 50, 0)
	if err != nil {
		t.Fatalf("list audit log: %v", err)
	}
	return total
}

// --- Communities hooks ---------------------------------------------------

// modeWithCatalogFixture creates a mode with one feed/catalog entry, ready
// for communities put/reset/generate.
func modeWithCatalogFixture(t *testing.T) (*Server, *store.Store, int64) {
	t.Helper()
	srv, st, _ := setupUserTestServer(t)
	ctx := context.Background()

	req := httptest.NewRequest("POST", "/api/admin/modes", strings.NewReader(`{"name":"Audit Mode","enabled":true}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiModesCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create mode: %d body=%s", w.Code, w.Body.String())
	}
	var created modeJSON
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	modeID := created.ID

	feedID, err := st.AddFeed(ctx, "Audit Feed", "http://example.com/audit.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatalf("add feed: %v", err)
	}
	if _, err := st.DB.ExecContext(ctx, "INSERT INTO catalog_mode_feeds(mode_id, feed_id) VALUES (?, ?)", modeID, feedID); err != nil {
		t.Fatalf("assign feed: %v", err)
	}
	if err := st.InsertCatalogEntries(ctx, feedID, []store.CatalogEntry{
		{Category: "cat-a", Service: "svc-a", CIDR: "10.0.0.0/8"},
	}); err != nil {
		t.Fatalf("insert catalog: %v", err)
	}
	return srv, st, modeID
}

// modeWithMultiCatalogFixture creates a mode with three catalog entries
// across two categories, all generated (assigned a community), ready for
// a test that edits only one assignment and checks the audit entry doesn't
// also report the other, untouched ones.
func modeWithMultiCatalogFixture(t *testing.T) (*Server, *store.Store, int64) {
	t.Helper()
	srv, st, _ := setupUserTestServer(t)
	ctx := context.Background()

	req := httptest.NewRequest("POST", "/api/admin/modes", strings.NewReader(`{"name":"Audit Multi Mode","enabled":true}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiModesCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create mode: %d body=%s", w.Code, w.Body.String())
	}
	var created modeJSON
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	modeID := created.ID

	feedID, err := st.AddFeed(ctx, "Audit Multi Feed", "http://example.com/audit-multi.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatalf("add feed: %v", err)
	}
	if _, err := st.DB.ExecContext(ctx, "INSERT INTO catalog_mode_feeds(mode_id, feed_id) VALUES (?, ?)", modeID, feedID); err != nil {
		t.Fatalf("assign feed: %v", err)
	}
	if err := st.InsertCatalogEntries(ctx, feedID, []store.CatalogEntry{
		{Category: "cat-a", Service: "svc-a", CIDR: "10.0.0.0/8"},
		{Category: "cat-a", Service: "svc-b", CIDR: "10.1.0.0/16"},
		{Category: "cat-b", Service: "svc-c", CIDR: "10.2.0.0/16"},
	}); err != nil {
		t.Fatalf("insert catalog: %v", err)
	}
	if _, _, _, err := st.GenerateCommunities(ctx, modeID, store.AuditMeta{}); err != nil {
		t.Fatalf("pre-generate: %v", err)
	}
	return srv, st, modeID
}

// TestAuditHookCommunitiesPutOnlyRecordsChangedAssignments checks that
// editing one assignment in a multi-entry mode records an audit before/after
// containing only that one changed (category, service) pair — not a full
// snapshot of every assignment in the mode, most of which this PUT never
// touched.
func TestAuditHookCommunitiesPutOnlyRecordsChangedAssignments(t *testing.T) {
	srv, st, modeID := modeWithMultiCatalogFixture(t)
	ctx := context.Background()

	before, err := st.CommunityRows(ctx, modeID)
	if err != nil {
		t.Fatalf("read rows: %v", err)
	}
	if len(before) < 3 {
		t.Fatalf("fixture rows = %d, want >= 3 (2 categories + 3 services)", len(before))
	}
	used := make(map[uint32]bool, len(before))
	for _, c := range before {
		used[c.Community] = true
	}
	newComm := uint32(90000)
	for used[newComm] {
		newComm++
	}

	body := fmt.Sprintf(`{"communities":[{"category":"cat-a","service":"svc-a","community":%d}]}`, newComm)
	req := httptest.NewRequest("PUT", "/api/admin/modes/x/communities", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", strconv.FormatInt(modeID, 10))
	w := httptest.NewRecorder()
	srv.apiModeCommunitiesPut(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("put: %d body=%s", w.Code, w.Body.String())
	}

	e := latestAuditByAction(t, st, "communities.updated")
	var afterEntries []store.Community
	if err := json.Unmarshal([]byte(e.After), &afterEntries); err != nil {
		t.Fatalf("unmarshal after %q: %v", e.After, err)
	}
	if len(afterEntries) != 1 {
		t.Fatalf("after entries = %d, want exactly 1 (only the changed assignment), got %q", len(afterEntries), e.After)
	}
	if afterEntries[0].Category != "cat-a" || afterEntries[0].Service != "svc-a" || afterEntries[0].Community != newComm {
		t.Fatalf("after entry = %+v, want cat-a/svc-a -> %d", afterEntries[0], newComm)
	}
	var beforeEntries []store.Community
	if err := json.Unmarshal([]byte(e.Before), &beforeEntries); err != nil {
		t.Fatalf("unmarshal before %q: %v", e.Before, err)
	}
	if len(beforeEntries) != 1 {
		t.Fatalf("before entries = %d, want exactly 1 (only the changed assignment), got %q", len(beforeEntries), e.Before)
	}
}

func TestAuditHookCommunitiesGenerate(t *testing.T) {
	srv, st, modeID := modeWithCatalogFixture(t)

	req := httptest.NewRequest("POST", "/api/admin/modes/x/communities/generate", nil)
	req.SetPathValue("id", strconv.FormatInt(modeID, 10))
	w := httptest.NewRecorder()
	srv.apiModeCommunitiesGenerate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("generate: %d body=%s", w.Code, w.Body.String())
	}

	e := latestAuditByAction(t, st, "communities.generated")
	if e.ObjectType != "mode" || e.ObjectID != strconv.FormatInt(modeID, 10) {
		t.Fatalf("entry = %+v, want mode/%d", e, modeID)
	}
	if e.Before == e.After {
		t.Fatalf("before == after (%q), want a real change", e.Before)
	}
}

func TestAuditHookCommunitiesGenerateNoopWhenNothingToGenerate(t *testing.T) {
	srv, st, modeID := modeWithCatalogFixture(t)
	ctx := context.Background()

	// First generate fills everything; a second call has nothing left to do.
	if _, _, _, err := st.GenerateCommunities(ctx, modeID, store.AuditMeta{}); err != nil {
		t.Fatalf("pre-generate: %v", err)
	}

	req := httptest.NewRequest("POST", "/api/admin/modes/x/communities/generate", nil)
	req.SetPathValue("id", strconv.FormatInt(modeID, 10))
	w := httptest.NewRecorder()
	srv.apiModeCommunitiesGenerate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("generate: %d body=%s", w.Code, w.Body.String())
	}

	if n := auditLogCount(t, st, "communities.generated"); n != 0 {
		t.Fatalf("audit log count = %d, want 0 (nothing changed)", n)
	}
}

// TestAuditHookCommunitiesGenerateOnlyRecordsNewAssignments checks that
// generating communities for a mode that already has some assignments
// filled records an audit after containing only the newly filled entries
// — not the already-assigned ones GenerateCommunities left untouched.
func TestAuditHookCommunitiesGenerateOnlyRecordsNewAssignments(t *testing.T) {
	srv, st, modeID := modeWithCatalogFixture(t)
	ctx := context.Background()

	feedID, err := st.AddFeed(ctx, "Audit Feed 2", "http://example.com/audit2.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatalf("add feed: %v", err)
	}
	if _, err := st.DB.ExecContext(ctx, "INSERT INTO catalog_mode_feeds(mode_id, feed_id) VALUES (?, ?)", modeID, feedID); err != nil {
		t.Fatalf("assign feed: %v", err)
	}
	if err := st.InsertCatalogEntries(ctx, feedID, []store.CatalogEntry{
		{Category: "cat-b", Service: "svc-b", CIDR: "10.1.0.0/16"},
	}); err != nil {
		t.Fatalf("insert catalog: %v", err)
	}

	// Pre-assign cat-a's entries by hand, leaving only cat-b for Generate
	// to fill.
	if err := st.SetCommunity(ctx, modeID, "cat-a", "svc-a", 500); err != nil {
		t.Fatalf("pre-assign: %v", err)
	}
	if err := st.SetCommunity(ctx, modeID, "cat-a", "", 501); err != nil {
		t.Fatalf("pre-assign group: %v", err)
	}

	req := httptest.NewRequest("POST", "/api/admin/modes/x/communities/generate", nil)
	req.SetPathValue("id", strconv.FormatInt(modeID, 10))
	w := httptest.NewRecorder()
	srv.apiModeCommunitiesGenerate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("generate: %d body=%s", w.Code, w.Body.String())
	}

	e := latestAuditByAction(t, st, "communities.generated")
	var afterEntries []store.Community
	if err := json.Unmarshal([]byte(e.After), &afterEntries); err != nil {
		t.Fatalf("unmarshal after %q: %v", e.After, err)
	}
	if len(afterEntries) == 0 {
		t.Fatalf("after = %q, want at least the newly generated cat-b assignment(s)", e.After)
	}
	for _, c := range afterEntries {
		if c.Category == "cat-a" {
			t.Fatalf("after unexpectedly includes the already-assigned cat-a entry: %+v (full after=%s)", c, e.After)
		}
	}
}

func TestAuditHookCommunitiesPut(t *testing.T) {
	srv, st, modeID := modeWithCatalogFixture(t)

	body := `{"communities":[{"category":"cat-a","service":"svc-a","community":12345}]}`
	req := httptest.NewRequest("PUT", "/api/admin/modes/x/communities", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", strconv.FormatInt(modeID, 10))
	w := httptest.NewRecorder()
	srv.apiModeCommunitiesPut(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("put: %d body=%s", w.Code, w.Body.String())
	}

	e := latestAuditByAction(t, st, "communities.updated")
	if e.ObjectType != "mode" || e.ObjectID != strconv.FormatInt(modeID, 10) {
		t.Fatalf("entry = %+v, want mode/%d", e, modeID)
	}
	if !strings.Contains(e.After, "12345") {
		t.Fatalf("after = %q, want it to mention the new community 12345", e.After)
	}
}

// TestAuditHookCommunitiesPutAfterIsImmuneToLaterConcurrentWrite shows the
// bug an independent post-mutation CommunityRows read (bracketing
// SetCommunities instead of using its own atomic return) would have: a
// concurrent request committing between this PUT's write and that
// independent read would make the audit entry describe the CONCURRENT
// request's result, not this PUT's own. SetCommunities' returned "after"
// is captured inside its own transaction and can't be affected by
// anything that commits afterward.
func TestAuditHookCommunitiesPutAfterIsImmuneToLaterConcurrentWrite(t *testing.T) {
	srv, st, modeID := modeWithCatalogFixture(t)
	ctx := context.Background()

	body := `{"communities":[{"category":"cat-a","service":"svc-a","community":100}]}`
	req := httptest.NewRequest("PUT", "/api/admin/modes/x/communities", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", strconv.FormatInt(modeID, 10))
	w := httptest.NewRecorder()
	srv.apiModeCommunitiesPut(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("put: %d body=%s", w.Code, w.Body.String())
	}
	e := latestAuditByAction(t, st, "communities.updated")
	if !strings.Contains(e.After, "100") {
		t.Fatalf("after = %q, want it to mention 100 (this PUT's own write)", e.After)
	}

	// A second, independent mutation commits afterward (e.g. a concurrent
	// admin request touching the same pair).
	if err := st.SetCommunity(ctx, modeID, "cat-a", "svc-a", 200); err != nil {
		t.Fatalf("concurrent write: %v", err)
	}
	// An independent post-read at this point would see the CONCURRENT
	// write's result, not the original PUT's — demonstrating exactly what
	// the old bracketing pattern was vulnerable to.
	rows, err := st.CommunityRows(ctx, modeID)
	if err != nil {
		t.Fatal(err)
	}
	sawConcurrentValue := false
	for _, row := range rows {
		if row.Category == "cat-a" && row.Service == "svc-a" && row.Community == 200 {
			sawConcurrentValue = true
		}
	}
	if !sawConcurrentValue {
		t.Fatal("expected the concurrent write to be visible to an independent post-read")
	}

	// The already-recorded audit entry for the original PUT must still
	// describe only that PUT's own result (100), unaffected by the later
	// concurrent write.
	e = latestAuditByAction(t, st, "communities.updated")
	if !strings.Contains(e.After, "100") || strings.Contains(e.After, "200") {
		t.Fatalf("audit entry after = %q, want it to still mention only 100, not the later concurrent write's 200", e.After)
	}
}

// TestAuditHookCommunitiesPutIsAtomic guards against a batch PUT partially
// committing: the first item in the batch is a real, otherwise-valid
// change; the second conflicts with a pre-existing assignment the batch
// doesn't otherwise touch. Before SetCommunities made the whole batch one
// transaction, the first item's own per-call transaction would already
// have committed by the time the second one failed — leaving the database
// mutated with no audit entry to show for it (and no indication in the
// error response that anything had already changed).
func TestAuditHookCommunitiesPutIsAtomic(t *testing.T) {
	srv, st, modeID := modeWithCatalogFixture(t)
	ctx := context.Background()
	feedID, err := st.AddFeed(ctx, "atomic-feed", "http://example.com/atomic.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatalf("add feed: %v", err)
	}
	if _, err := st.DB.ExecContext(ctx, "INSERT INTO catalog_mode_feeds(mode_id, feed_id) VALUES (?, ?)", modeID, feedID); err != nil {
		t.Fatalf("assign feed: %v", err)
	}
	if err := st.InsertCatalogEntries(ctx, feedID, []store.CatalogEntry{
		{Category: "cat-b", Service: "svc-b", CIDR: "10.60.0.0/16"},
		{Category: "cat-c", Service: "svc-c", CIDR: "10.61.0.0/16"},
	}); err != nil {
		t.Fatalf("insert catalog: %v", err)
	}
	// cat-b/svc-b already holds community 999, untouched by the batch below.
	if err := st.SetCommunity(ctx, modeID, "cat-b", "svc-b", 999); err != nil {
		t.Fatalf("pre-set community: %v", err)
	}

	body := `{"communities":[` +
		`{"category":"cat-a","service":"svc-a","community":111},` +
		`{"category":"cat-c","service":"svc-c","community":999}` +
		`]}`
	req := httptest.NewRequest("PUT", "/api/admin/modes/x/communities", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", strconv.FormatInt(modeID, 10))
	w := httptest.NewRecorder()
	srv.apiModeCommunitiesPut(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("put: %d body=%s, want 400 (cat-c/svc-c's community 999 collides with cat-b/svc-b)", w.Code, w.Body.String())
	}

	rows, err := st.CommunityRows(ctx, modeID)
	if err != nil {
		t.Fatalf("read rows: %v", err)
	}
	for _, row := range rows {
		if row.Category == "cat-a" && row.Service == "svc-a" && row.Community == 111 {
			t.Fatalf("cat-a/svc-a was set to 111 despite the batch failing on a later item — the batch is not atomic")
		}
	}
	if n := auditLogCount(t, st, "communities.updated"); n != 0 {
		t.Fatalf("audit log count = %d, want 0 (the failed batch must not be silently partially applied, with or without an audit trail)", n)
	}
}

func TestAuditHookCommunitiesReset(t *testing.T) {
	srv, st, modeID := modeWithCatalogFixture(t)
	ctx := context.Background()
	if _, _, _, err := st.GenerateCommunities(ctx, modeID, store.AuditMeta{}); err != nil {
		t.Fatalf("pre-generate: %v", err)
	}

	// Unconfirmed call just returns a preview — confirm with the digest.
	req := httptest.NewRequest("POST", "/api/admin/modes/x/communities/reset", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", strconv.FormatInt(modeID, 10))
	w := httptest.NewRecorder()
	srv.apiModeCommunitiesReset(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("preview: %d body=%s", w.Code, w.Body.String())
	}
	var preview struct {
		Digest string `json:"digest"`
	}
	if err := json.NewDecoder(w.Body).Decode(&preview); err != nil {
		t.Fatal(err)
	}

	confirmBody := `{"confirm":true,"digest":"` + preview.Digest + `"}`
	req = httptest.NewRequest("POST", "/api/admin/modes/x/communities/reset", strings.NewReader(confirmBody))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", strconv.FormatInt(modeID, 10))
	w = httptest.NewRecorder()
	srv.apiModeCommunitiesReset(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("confirm reset: %d body=%s", w.Code, w.Body.String())
	}

	if n := auditLogCount(t, st, "communities.reset"); n != 1 {
		t.Fatalf("audit log count for communities.reset = %d, want 1", n)
	}
}

// --- Global route filters (apiSettingsPut) --------------------------------

func TestAuditHookGlobalRouteFiltersUpdated(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)

	req := httptest.NewRequest("PUT", "/api/admin/settings", strings.NewReader(`{"filter_allow":"10.0.0.0/8"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiSettingsPut(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("settings put: %d body=%s", w.Code, w.Body.String())
	}

	e := latestAuditByAction(t, st, "route_filters.global_updated")
	if e.ObjectType != "settings" || e.ObjectID != "" {
		t.Fatalf("entry = %+v, want settings/\"\"", e)
	}
	if !strings.Contains(e.After, "10.0.0.0/8") {
		t.Fatalf("after = %q, want it to mention 10.0.0.0/8", e.After)
	}
}

// TestAuditHookGlobalRouteFiltersOnlyRecordsChangedLines checks that
// adding one line to an existing multi-line filter_allow records an audit
// entry containing only that one added line — not the complete list,
// which has no size limit and could otherwise make a single audit row
// arbitrarily large.
func TestAuditHookGlobalRouteFiltersOnlyRecordsChangedLines(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)

	put := func(value string) {
		req := httptest.NewRequest("PUT", "/api/admin/settings", strings.NewReader(`{"filter_allow":"`+value+`"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		srv.apiSettingsPut(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("settings put: %d body=%s", w.Code, w.Body.String())
		}
	}

	put(`10.0.0.0/8\n192.168.0.0/16\n172.16.0.0/12`)
	put(`10.0.0.0/8\n192.168.0.0/16\n172.16.0.0/12\n203.0.113.0/24`)

	entries, total, err := st.ListAuditLog(context.Background(), store.AuditLogFilter{Action: "route_filters.global_updated"}, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Fatalf("total = %d, want 2 (baseline + the one-line addition)", total)
	}
	e := entries[0] // newest first
	if !strings.Contains(e.After, "203.0.113.0/24") {
		t.Fatalf("after = %q, want it to mention the added line", e.After)
	}
	if strings.Contains(e.After, "10.0.0.0/8") || strings.Contains(e.After, "192.168.0.0/16") || strings.Contains(e.After, "172.16.0.0/12") {
		t.Fatalf("after = %q, want ONLY the added line, not the 3 unchanged ones", e.After)
	}
}

// TestAuditHookGlobalRouteFiltersAuditCapsEntireDisjointReplacement is the
// adversarial case diffing alone doesn't bound: replacing an entire large
// filter_allow with a disjoint large one puts every old line in "removed"
// and every new one in "added" — this checks the resulting audit entry is
// still capped to store.MaxAuditDiffEntries per side.
func TestAuditHookGlobalRouteFiltersAuditCapsEntireDisjointReplacement(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)

	put := func(value string) {
		req := httptest.NewRequest("PUT", "/api/admin/settings", strings.NewReader(`{"filter_allow":"`+value+`"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		srv.apiSettingsPut(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("settings put: %d body=%s", w.Code, w.Body.String())
		}
	}

	n := store.MaxAuditDiffEntries + 30
	oldLines := make([]string, n)
	for i := range oldLines {
		oldLines[i] = fmt.Sprintf("10.%d.0.0/16", i)
	}
	put(strings.Join(oldLines, `\n`))

	newLines := make([]string, n)
	for i := range newLines {
		newLines[i] = fmt.Sprintf("172.%d.0.0/16", i) // entirely disjoint from oldLines
	}
	put(strings.Join(newLines, `\n`))

	entries, total, err := st.ListAuditLog(context.Background(), store.AuditLogFilter{Action: "route_filters.global_updated"}, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Fatalf("total = %d, want 2 (baseline + the disjoint replacement)", total)
	}

	var before, after map[string]store.AuditStringList
	if err := json.Unmarshal([]byte(entries[0].Before), &before); err != nil {
		t.Fatalf("unmarshal before: %v", err)
	}
	if err := json.Unmarshal([]byte(entries[0].After), &after); err != nil {
		t.Fatalf("unmarshal after: %v", err)
	}
	if len(before["filter_allow"].Entries) != store.MaxAuditDiffEntries || before["filter_allow"].Truncated != 30 {
		t.Fatalf("before[filter_allow] = %+v, want %d entries and 30 truncated", before["filter_allow"], store.MaxAuditDiffEntries)
	}
	if len(after["filter_allow"].Entries) != store.MaxAuditDiffEntries || after["filter_allow"].Truncated != 30 {
		t.Fatalf("after[filter_allow] = %+v, want %d entries and 30 truncated", after["filter_allow"], store.MaxAuditDiffEntries)
	}
}

// TestAuditHookGlobalRouteFiltersCapsOversizedCommentLine covers a gap
// the entry-count cap alone doesn't: filter_allow/filter_deny accept
// #-prefixed comment lines stored verbatim with no length limit of their
// own, so a single huge comment line is still just one "entry" — well
// under the 50-entry cap — but could be most of the request body. This
// checks it's truncated in bytes too.
func TestAuditHookGlobalRouteFiltersCapsOversizedCommentLine(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)

	hugeComment := "# " + strings.Repeat("A", store.MaxAuditEntryBytes*4)
	req := httptest.NewRequest("PUT", "/api/admin/settings", strings.NewReader(`{"filter_allow":"`+hugeComment+`"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiSettingsPut(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("settings put: %d body=%s", w.Code, w.Body.String())
	}

	e := latestAuditByAction(t, st, "route_filters.global_updated")
	var after map[string]store.AuditStringList
	if err := json.Unmarshal([]byte(e.After), &after); err != nil {
		t.Fatalf("unmarshal after: %v", err)
	}
	if len(after["filter_allow"].Entries) != 1 {
		t.Fatalf("after[filter_allow].Entries = %v, want exactly 1 entry", after["filter_allow"].Entries)
	}
	if len(after["filter_allow"].Entries[0]) > store.MaxAuditEntryBytes {
		t.Fatalf("entry len = %d, want <= %d", len(after["filter_allow"].Entries[0]), store.MaxAuditEntryBytes)
	}
}

func TestAuditHookGlobalRouteFiltersNoopWhenUnrelatedSettingChanges(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)

	req := httptest.NewRequest("PUT", "/api/admin/settings", strings.NewReader(`{"rate_limit_login":10}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiSettingsPut(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("settings put: %d body=%s", w.Code, w.Body.String())
	}

	if n := auditLogCount(t, st, "route_filters.global_updated"); n != 0 {
		t.Fatalf("audit log count = %d, want 0 (filter_allow/filter_deny untouched)", n)
	}
}

func TestAuditHookGlobalRouteFiltersNoopWhenSameValueResubmitted(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)

	req := httptest.NewRequest("PUT", "/api/admin/settings", strings.NewReader(`{"filter_allow":"10.0.0.0/8"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.apiSettingsPut(httptest.NewRecorder(), req)

	// Resubmit the identical value.
	req = httptest.NewRequest("PUT", "/api/admin/settings", strings.NewReader(`{"filter_allow":"10.0.0.0/8"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiSettingsPut(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("settings put: %d body=%s", w.Code, w.Body.String())
	}

	if n := auditLogCount(t, st, "route_filters.global_updated"); n != 1 {
		t.Fatalf("audit log count = %d, want 1 (only the first, real change)", n)
	}
}

// TestAuditHookGlobalRouteFiltersConcurrentPutsAreSerialized forces two
// apiSettingsPut calls to genuinely overlap (A pauses mid-critical-section
// while holding globalFilterMu; B is launched while A is paused) and checks
// the resulting audit chain is coherent: entry 2's "before" equals entry 1's
// "after". Without the lock, both calls can capture the same "before" and
// each report their own "after", producing two rows that don't chain —
// exactly the misattribution this guards against.
func TestAuditHookGlobalRouteFiltersConcurrentPutsAreSerialized(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)
	// bgp is irrelevant to this test (it only checks audit serialization)
	// and fakeBGP isn't safe for genuinely concurrent calls, which this
	// test makes for real.
	srv.bgp = nil

	// Baseline: filter_allow = 10.0.0.0/8 ("A").
	req := httptest.NewRequest("PUT", "/api/admin/settings", strings.NewReader(`{"filter_allow":"10.0.0.0/8"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.apiSettingsPut(httptest.NewRecorder(), req)
	if n := auditLogCount(t, st, "route_filters.global_updated"); n != 1 {
		t.Fatalf("baseline audit count = %d, want 1", n)
	}

	var hookCalls int32
	entered := make(chan struct{})
	release := make(chan struct{})
	settingsPutFilterApplyHook = func() {
		if atomic.AddInt32(&hookCalls, 1) == 1 {
			close(entered)
			<-release
		}
	}
	t.Cleanup(func() { settingsPutFilterApplyHook = nil })

	put := func(body string) error {
		req := httptest.NewRequest("PUT", "/api/admin/settings", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		srv.apiSettingsPut(w, req)
		if w.Code != http.StatusOK {
			return fmt.Errorf("settings put: %d body=%s", w.Code, w.Body.String())
		}
		return nil
	}

	aDone := make(chan error, 1)
	go func() { aDone <- put(`{"filter_allow":"192.168.0.0/16"}`) }() // A -> B
	<-entered

	// A must hold globalFilterMu while paused: a TryLock from here must fail.
	if srv.globalFilterMu.TryLock() {
		srv.globalFilterMu.Unlock()
		t.Fatal("globalFilterMu was not held while A was paused mid-update")
	}

	bDone := make(chan error, 1)
	go func() { bDone <- put(`{"filter_allow":"172.16.0.0/12"}`) }() // B -> C, once unblocked

	// B must not be able to complete while A still holds the lock.
	select {
	case err := <-bDone:
		t.Fatalf("B completed (err=%v) before A released the lock — not serialized", err)
	case <-time.After(150 * time.Millisecond):
	}

	close(release)

	if err := <-aDone; err != nil {
		t.Fatalf("A: %v", err)
	}
	select {
	case err := <-bDone:
		if err != nil {
			t.Fatalf("B: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("B never completed after A released the lock")
	}

	entries, total, err := st.ListAuditLog(context.Background(), store.AuditLogFilter{Action: "route_filters.global_updated"}, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 { // baseline + A + B
		t.Fatalf("total = %d, want 3", total)
	}
	// Newest first: entries[0] is B, entries[1] is A, entries[2] is baseline.
	bEntry, aEntry := entries[0], entries[1]
	if !strings.Contains(aEntry.Before, "10.0.0.0/8") || !strings.Contains(aEntry.After, "192.168.0.0/16") {
		t.Fatalf("A entry: before=%q after=%q, want 10.0.0.0/8 -> 192.168.0.0/16", aEntry.Before, aEntry.After)
	}
	if aEntry.After != bEntry.Before {
		t.Fatalf("chain broken: A.after=%q != B.before=%q (two overlapping requests raced)", aEntry.After, bEntry.Before)
	}
	if !strings.Contains(bEntry.After, "172.16.0.0/12") {
		t.Fatalf("B entry after=%q, want it to mention 172.16.0.0/12", bEntry.After)
	}
}

// TestAuditHookGlobalRouteFiltersSettingRollsBackWithAuditFailure proves
// the filter_allow/filter_deny setting write and its audit entry commit
// or roll back together, the same atomicity guarantee every other
// tx-folded hook has — by forcing the audit insert to fail (via
// settingsPutFilterTxPreCommitHook) after the setting's own SetTx write
// already ran, and checking that write rolled back too. Before this hook
// folded its audit write into the setting's own transaction, the setting
// commit and the audit write were two separate statements with no
// atomicity between them at all, so a failure here couldn't have rolled
// the setting back — this is a stronger property than the pre-fix code
// had, and the ordering guarantee this finding asked for follows directly
// from it (two statements that always commit together can't land out of
// commit order with anything else in the same transaction).
func TestAuditHookGlobalRouteFiltersSettingRollsBackWithAuditFailure(t *testing.T) {
	_, st, _ := feedFixture(t)

	// apiSettingsPut needs settings actually backed by st — not the fake,
	// in-memory settings.NewTestStore() testSettings()/setupUserTestServer
	// normally use — so its SetTx call performs a real SQL write on the
	// same transaction this test forces to fail.
	realSettings, err := settings.New(st)
	if err != nil {
		t.Fatalf("settings.New: %v", err)
	}
	srv := &Server{settings: realSettings, store: st}

	settingsPutFilterTxPreCommitHook = func() error {
		return fmt.Errorf("forced failure")
	}
	t.Cleanup(func() { settingsPutFilterTxPreCommitHook = nil })

	req := httptest.NewRequest("PUT", "/api/admin/settings", strings.NewReader(`{"filter_allow":"10.0.0.0/8"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiSettingsPut(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("settings put: %d body=%s, want 400 (forced audit failure)", w.Code, w.Body.String())
	}

	if got := realSettings.FilterAllow.Get(); got != "" {
		t.Fatalf("FilterAllow = %q, want unchanged (the whole transaction, including the already-applied SetTx, must roll back)", got)
	}
	if n := auditLogCount(t, st, "route_filters.global_updated"); n != 0 {
		t.Fatalf("audit log count = %d, want 0 (rolled back)", n)
	}
}

// --- Feed enable/disable ---------------------------------------------------

func feedFixture(t *testing.T) (*Server, *store.Store, int64) {
	t.Helper()
	srv, st, _ := setupUserTestServer(t)
	ctx := context.Background()
	if _, err := st.DB.ExecContext(ctx, "INSERT OR IGNORE INTO feed_adapters(id, name, language, api_version, source, revision) VALUES (1, 'Test', 'javascript', 1, 'function sync(feed, api) { return []; }', 1)"); err != nil {
		t.Fatalf("setup adapter: %v", err)
	}
	req := httptest.NewRequest("POST", "/api/admin/feeds", strings.NewReader(
		`{"name":"audit-feed","url":"http://example.com/feed.json","enabled":true,"sync_interval":3600,"mode_id":1,"adapter_id":1}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiFeedsCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create feed: %d body=%s", w.Code, w.Body.String())
	}
	var created feedJSON
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	return srv, st, created.ID
}

func TestAuditHookFeedEnabledChanged(t *testing.T) {
	srv, st, feedID := feedFixture(t)
	idStr := strconv.FormatInt(feedID, 10)

	req := httptest.NewRequest("PUT", "/api/admin/feeds/"+idStr, strings.NewReader(
		`{"name":"audit-feed","url":"http://example.com/feed.json","enabled":false,"sync_interval":3600,"mode_id":1,"adapter_id":1}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", idStr)
	w := httptest.NewRecorder()
	srv.apiFeedsUpdate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update: %d body=%s", w.Code, w.Body.String())
	}

	e := latestAuditByAction(t, st, "feed.enabled_changed")
	if e.ObjectType != "feed" || e.ObjectID != idStr {
		t.Fatalf("entry = %+v, want feed/%s", e, idStr)
	}
	if e.Before != `{"enabled":true}` || e.After != `{"enabled":false}` {
		t.Fatalf("before=%q after=%q, want true->false", e.Before, e.After)
	}
}

// TestAuditHookTruncatesOversizedUserAgent checks the auditEntryTx insert
// path (every tx-folded hook since round 8) truncates an oversized
// User-Agent header the same way RecordAuditLog's direct path does — an
// authenticated caller could otherwise send a huge User-Agent on every
// mutating request to grow audit_log arbitrarily fast.
func TestAuditHookTruncatesOversizedUserAgent(t *testing.T) {
	srv, st, feedID := feedFixture(t)
	idStr := strconv.FormatInt(feedID, 10)

	req := httptest.NewRequest("PUT", "/api/admin/feeds/"+idStr, strings.NewReader(
		`{"name":"audit-feed","url":"http://example.com/feed.json","enabled":false,"sync_interval":3600,"mode_id":1,"adapter_id":1}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", strings.Repeat("A", 10_000))
	req.SetPathValue("id", idStr)
	w := httptest.NewRecorder()
	srv.apiFeedsUpdate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update: %d body=%s", w.Code, w.Body.String())
	}

	e := latestAuditByAction(t, st, "feed.enabled_changed")
	if len(e.UserAgent) >= 10_000 {
		t.Fatalf("stored UserAgent len = %d, want far less than 10000", len(e.UserAgent))
	}
}

func TestAuditHookFeedEnabledNoopWhenUnchanged(t *testing.T) {
	srv, st, feedID := feedFixture(t)
	idStr := strconv.FormatInt(feedID, 10)

	// Same enabled:true, only the name changes.
	req := httptest.NewRequest("PUT", "/api/admin/feeds/"+idStr, strings.NewReader(
		`{"name":"renamed-feed","url":"http://example.com/feed.json","enabled":true,"sync_interval":3600,"mode_id":1,"adapter_id":1}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", idStr)
	w := httptest.NewRecorder()
	srv.apiFeedsUpdate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update: %d body=%s", w.Code, w.Body.String())
	}

	if n := auditLogCount(t, st, "feed.enabled_changed"); n != 0 {
		t.Fatalf("audit log count = %d, want 0 (enabled unchanged)", n)
	}
}

// TestAuditHookFeedEnabledChangedTwoToggles exercises the real handler
// across two consecutive toggles (disable, then re-enable) and checks both
// resulting entries — proving the handler's "before" comes from
// UpdateFeed's own atomic return rather than a value read earlier and
// reused, which the race TestUpdateFeedPrevEnabledReflectsImmediatelyPriorState
// (internal/store/feeds_test.go) covers directly at the store layer.
func TestAuditHookFeedEnabledChangedTwoToggles(t *testing.T) {
	srv, st, feedID := feedFixture(t)
	idStr := strconv.FormatInt(feedID, 10)

	put := func(enabled bool) {
		body := `{"name":"audit-feed","url":"http://example.com/feed.json","enabled":` +
			strconv.FormatBool(enabled) + `,"sync_interval":3600,"mode_id":1,"adapter_id":1}`
		req := httptest.NewRequest("PUT", "/api/admin/feeds/"+idStr, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.SetPathValue("id", idStr)
		w := httptest.NewRecorder()
		srv.apiFeedsUpdate(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("update(enabled=%v): %d body=%s", enabled, w.Code, w.Body.String())
		}
	}

	put(false) // true -> false
	put(true)  // false -> true

	entries, total, err := st.ListAuditLog(context.Background(), store.AuditLogFilter{Action: "feed.enabled_changed"}, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Fatalf("total = %d, want 2 (both toggles are real changes)", total)
	}
	// Newest first.
	if entries[0].Before != `{"enabled":false}` || entries[0].After != `{"enabled":true}` {
		t.Fatalf("second toggle: before=%q after=%q, want false->true", entries[0].Before, entries[0].After)
	}
	if entries[1].Before != `{"enabled":true}` || entries[1].After != `{"enabled":false}` {
		t.Fatalf("first toggle: before=%q after=%q, want true->false", entries[1].Before, entries[1].After)
	}
}

// TestAuditHookFeedEnabledChangedSurvivesPostLookupFailure forces the
// post-UpdateFeed Feed() lookup (used only to build the HTTP response) to
// fail, and checks the audit entry for the already-committed enabled change
// was still recorded. Before the fix, the audit call sat after that lookup,
// so a failure there made the handler return before ever recording the
// committed transition.
func TestAuditHookFeedEnabledChangedSurvivesPostLookupFailure(t *testing.T) {
	srv, st, feedID := feedFixture(t)
	idStr := strconv.FormatInt(feedID, 10)

	feedUpdatePostAuditHook = func(id int64) {
		if err := st.DeleteFeed(context.Background(), id); err != nil {
			t.Fatalf("delete feed mid-update: %v", err)
		}
	}
	t.Cleanup(func() { feedUpdatePostAuditHook = nil })

	req := httptest.NewRequest("PUT", "/api/admin/feeds/"+idStr, strings.NewReader(
		`{"name":"audit-feed","url":"http://example.com/feed.json","enabled":false,"sync_interval":3600,"mode_id":1,"adapter_id":1}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", idStr)
	w := httptest.NewRecorder()
	srv.apiFeedsUpdate(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("update: %d body=%s, want 500 (forced post-lookup failure)", w.Code, w.Body.String())
	}

	e := latestAuditByAction(t, st, "feed.enabled_changed")
	if e.ObjectType != "feed" || e.ObjectID != idStr {
		t.Fatalf("entry = %+v, want feed/%s", e, idStr)
	}
	if e.Before != `{"enabled":true}` || e.After != `{"enabled":false}` {
		t.Fatalf("before=%q after=%q, want true->false", e.Before, e.After)
	}
}

// --- Admin user update: mode move + route filters -------------------------

func adminUserFixture(t *testing.T) (*Server, *store.Store, int64) {
	t.Helper()
	srv, st, _ := setupUserTestServer(t)
	req := httptest.NewRequest("POST", "/api/admin/users", strings.NewReader(
		`{"name":"audit-user","peer_ip":"10.9.9.1","peer_asn":65009,"networks":["10.9.9.0/24"],"web_auth":"network","enabled":true,"catalog_mode_id":1}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create user: %d body=%s", w.Code, w.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	return srv, st, created.ID
}

func createSecondModeFixture(t *testing.T, srv *Server) int64 {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/admin/modes", strings.NewReader(`{"name":"Mode B","enabled":true}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiModesCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create mode: %d body=%s", w.Code, w.Body.String())
	}
	var created modeJSON
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	return created.ID
}

func TestAuditHookAdminUserModeChanged(t *testing.T) {
	srv, st, userID := adminUserFixture(t)
	modeBID := createSecondModeFixture(t, srv)
	idStr := strconv.FormatInt(userID, 10)

	req := httptest.NewRequest("PUT", "/api/admin/users/"+idStr, strings.NewReader(
		`{"catalog_mode_id":`+strconv.FormatInt(modeBID, 10)+`}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", idStr)
	w := httptest.NewRecorder()
	srv.apiUsersUpdate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update: %d body=%s", w.Code, w.Body.String())
	}

	e := latestAuditByAction(t, st, "user.mode_changed")
	if e.ObjectType != "user" || e.ObjectID != idStr {
		t.Fatalf("entry = %+v, want user/%s", e, idStr)
	}
	if e.Before != `{"catalog_mode_id":1}` {
		t.Fatalf("before = %q, want catalog_mode_id 1", e.Before)
	}
}

func TestAuditHookAdminUserModeChangedNoopWhenSameMode(t *testing.T) {
	srv, st, userID := adminUserFixture(t)
	idStr := strconv.FormatInt(userID, 10)

	req := httptest.NewRequest("PUT", "/api/admin/users/"+idStr, strings.NewReader(`{"catalog_mode_id":1}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", idStr)
	w := httptest.NewRecorder()
	srv.apiUsersUpdate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update: %d body=%s", w.Code, w.Body.String())
	}

	if n := auditLogCount(t, st, "user.mode_changed"); n != 0 {
		t.Fatalf("audit log count = %d, want 0 (mode unchanged)", n)
	}
}

// TestAuditHookAdminUserModeChangeRevertedByStaleConcurrentUpdateIsAudited
// forces a genuine overlap: request A loads `current` (catalog_mode_id=1),
// pauses (via apiUsersUpdatePreWriteHook); request B then fully completes a
// real mode change (1 -> modeB); A resumes and writes its own now-stale
// `current`, silently reverting the user back to mode 1 — a real DB
// mutation A's own request body never asked for (it only touched `name`).
// Before this fix, apiUsersUpdate only populated modeMeta when its OWN
// request body mentioned catalog_mode_id, so this reversion — a real
// transition UpdateUser's own prior-state read would see as modeB -> 1 —
// went completely unaudited.
func TestAuditHookAdminUserModeChangeRevertedByStaleConcurrentUpdateIsAudited(t *testing.T) {
	srv, st, userID := adminUserFixture(t)
	modeBID := createSecondModeFixture(t, srv)
	idStr := strconv.FormatInt(userID, 10)

	entered := make(chan struct{})
	release := make(chan struct{})
	apiUsersUpdatePreWriteHook = func() {
		close(entered)
		<-release
	}
	t.Cleanup(func() { apiUsersUpdatePreWriteHook = nil })

	aDone := make(chan error, 1)
	go func() {
		req := httptest.NewRequest("PUT", "/api/admin/users/"+idStr, strings.NewReader(`{"name":"renamed-by-a"}`))
		req.Header.Set("Content-Type", "application/json")
		req.SetPathValue("id", idStr)
		w := httptest.NewRecorder()
		srv.apiUsersUpdate(w, req)
		if w.Code != http.StatusOK {
			aDone <- fmt.Errorf("request A: %d body=%s", w.Code, w.Body.String())
			return
		}
		aDone <- nil
	}()
	<-entered // A has loaded `current` (catalog_mode_id=1) and is now paused.

	// Request B: a real, independent, fully-completed mode change.
	reqB := httptest.NewRequest("PUT", "/api/admin/users/"+idStr, strings.NewReader(
		`{"catalog_mode_id":`+strconv.FormatInt(modeBID, 10)+`}`))
	reqB.Header.Set("Content-Type", "application/json")
	reqB.SetPathValue("id", idStr)
	wB := httptest.NewRecorder()
	apiUsersUpdatePreWriteHook = nil // B must not also pause on the hook.
	srv.apiUsersUpdate(wB, reqB)
	if wB.Code != http.StatusOK {
		t.Fatalf("request B: %d body=%s", wB.Code, wB.Body.String())
	}

	// Resume A: its stale `current.CatalogModeID` (1) overwrites B's write.
	close(release)
	if err := <-aDone; err != nil {
		t.Fatal(err)
	}

	entries, total, err := st.ListAuditLog(context.Background(), store.AuditLogFilter{Action: "user.mode_changed"}, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Fatalf("total = %d, want 2 (B's 1->modeB, then A's silent revert modeB->1)", total)
	}
	// Newest first: entries[0] is A's revert, entries[1] is B's move.
	aEntry, bEntry := entries[0], entries[1]
	if bEntry.Before != `{"catalog_mode_id":1}` || bEntry.After != `{"catalog_mode_id":`+strconv.FormatInt(modeBID, 10)+`}` {
		t.Fatalf("B's entry: before=%q after=%q, want 1 -> %d", bEntry.Before, bEntry.After, modeBID)
	}
	if aEntry.Before != `{"catalog_mode_id":`+strconv.FormatInt(modeBID, 10)+`}` || aEntry.After != `{"catalog_mode_id":1}` {
		t.Fatalf("A's entry: before=%q after=%q, want %d -> 1 (A's silent revert must still be audited)", aEntry.Before, aEntry.After, modeBID)
	}
}

// TestAuditHookAdminUserFilterModeChanged covers a gap the
// route_filters.user_updated hook alone can't: switching a user's
// filter_mode (global -> override) changes their effective route
// filtering even when filter_allow/filter_deny themselves aren't part of
// the request, so it needs its own audit entry.
func TestAuditHookAdminUserFilterModeChanged(t *testing.T) {
	srv, st, userID := adminUserFixture(t)
	idStr := strconv.FormatInt(userID, 10)

	req := httptest.NewRequest("PUT", "/api/admin/users/"+idStr, strings.NewReader(`{"filter_mode":"override"}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", idStr)
	w := httptest.NewRecorder()
	srv.apiUsersUpdate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update: %d body=%s", w.Code, w.Body.String())
	}

	e := latestAuditByAction(t, st, "user.filter_mode_changed")
	if e.ObjectType != "user" || e.ObjectID != idStr {
		t.Fatalf("entry = %+v, want user/%s", e, idStr)
	}
	if e.Before != `{"filter_mode":"global"}` || e.After != `{"filter_mode":"override"}` {
		t.Fatalf("before=%q after=%q, want global->override", e.Before, e.After)
	}
	// route_filters.user_updated must NOT fire for this — filter_allow/
	// filter_deny were never touched.
	if n := auditLogCount(t, st, "route_filters.user_updated"); n != 0 {
		t.Fatalf("route_filters.user_updated count = %d, want 0 (allow/deny untouched)", n)
	}
}

// TestAuditHookAdminUserFilterModeChangedViaLegacyOverrideField covers the
// same transition via the legacy filter_override boolean instead of the
// filter_mode string, since UpdateUser normalizes both into the same
// persisted value.
func TestAuditHookAdminUserFilterModeChangedViaLegacyOverrideField(t *testing.T) {
	srv, st, userID := adminUserFixture(t)
	idStr := strconv.FormatInt(userID, 10)

	req := httptest.NewRequest("PUT", "/api/admin/users/"+idStr, strings.NewReader(`{"filter_override":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", idStr)
	w := httptest.NewRecorder()
	srv.apiUsersUpdate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update: %d body=%s", w.Code, w.Body.String())
	}

	e := latestAuditByAction(t, st, "user.filter_mode_changed")
	if e.Before != `{"filter_mode":"global"}` || e.After != `{"filter_mode":"override"}` {
		t.Fatalf("before=%q after=%q, want global->override", e.Before, e.After)
	}
}

func TestAuditHookAdminUserFilterModeNoopWhenUnchanged(t *testing.T) {
	srv, st, userID := adminUserFixture(t)
	idStr := strconv.FormatInt(userID, 10)

	req := httptest.NewRequest("PUT", "/api/admin/users/"+idStr, strings.NewReader(`{"name":"renamed-user"}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", idStr)
	w := httptest.NewRecorder()
	srv.apiUsersUpdate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update: %d body=%s", w.Code, w.Body.String())
	}

	if n := auditLogCount(t, st, "user.filter_mode_changed"); n != 0 {
		t.Fatalf("audit log count = %d, want 0 (filter mode untouched)", n)
	}
}

func TestAuditHookAdminUserRouteFiltersUpdated(t *testing.T) {
	srv, st, userID := adminUserFixture(t)
	idStr := strconv.FormatInt(userID, 10)

	req := httptest.NewRequest("PUT", "/api/admin/users/"+idStr, strings.NewReader(
		`{"filter_allow":["192.168.0.0/16"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", idStr)
	w := httptest.NewRecorder()
	srv.apiUsersUpdate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update: %d body=%s", w.Code, w.Body.String())
	}

	e := latestAuditByAction(t, st, "route_filters.user_updated")
	if e.ObjectType != "user" || e.ObjectID != idStr {
		t.Fatalf("entry = %+v, want user/%s", e, idStr)
	}
	if !strings.Contains(e.After, "192.168.0.0/16") {
		t.Fatalf("after = %q, want it to mention 192.168.0.0/16", e.After)
	}
}

// TestAuditHookAdminUserRouteFiltersAfterIsNormalized mirrors the
// self-service version: apiUsersUpdate's audit "after" must be the
// normalized form SetUserRouteFilters actually persists, not the raw
// submitted list.
func TestAuditHookAdminUserRouteFiltersAfterIsNormalized(t *testing.T) {
	srv, st, userID := adminUserFixture(t)
	idStr := strconv.FormatInt(userID, 10)

	req := httptest.NewRequest("PUT", "/api/admin/users/"+idStr, strings.NewReader(
		`{"filter_allow":["192.168.0.1"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", idStr)
	w := httptest.NewRecorder()
	srv.apiUsersUpdate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update: %d body=%s", w.Code, w.Body.String())
	}

	e := latestAuditByAction(t, st, "route_filters.user_updated")
	if !strings.Contains(e.After, "192.168.0.1/32") {
		t.Fatalf("after = %q, want the normalized \"192.168.0.1/32\", not the raw bare IP", e.After)
	}
}

// TestAuditHookAdminUserRouteFiltersOnlyRecordsChangedEntries checks that
// adding one entry to a user's existing multi-entry filter_allow records
// an audit entry containing only that one added entry — not the complete
// list, which has no size limit (the request body itself allows up to
// 8 MiB) and could otherwise make a single audit row arbitrarily large.
func TestAuditHookAdminUserRouteFiltersOnlyRecordsChangedEntries(t *testing.T) {
	srv, st, userID := adminUserFixture(t)
	idStr := strconv.FormatInt(userID, 10)

	put := func(body string) {
		req := httptest.NewRequest("PUT", "/api/admin/users/"+idStr, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.SetPathValue("id", idStr)
		w := httptest.NewRecorder()
		srv.apiUsersUpdate(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("update: %d body=%s", w.Code, w.Body.String())
		}
	}

	put(`{"filter_allow":["10.0.0.0/8","192.168.0.0/16"]}`)
	put(`{"filter_allow":["10.0.0.0/8","192.168.0.0/16","172.16.0.0/12"]}`)

	entries, total, err := st.ListAuditLog(context.Background(), store.AuditLogFilter{Action: "route_filters.user_updated"}, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Fatalf("total = %d, want 2 (baseline + the one-entry addition)", total)
	}
	e := entries[0] // newest first
	if !strings.Contains(e.After, "172.16.0.0/12") {
		t.Fatalf("after = %q, want it to mention the added entry", e.After)
	}
	if strings.Contains(e.After, "10.0.0.0/8") || strings.Contains(e.After, "192.168.0.0/16") {
		t.Fatalf("after = %q, want ONLY the added entry, not the 2 unchanged ones", e.After)
	}
}

// --- Self-service user hooks ----------------------------------------------

func selfServiceUserFixture(t *testing.T, filterEditable, catalogEditable bool) (*Server, *store.Store, int64) {
	t.Helper()
	srv, st, _ := setupUserTestServer(t)
	req := httptest.NewRequest("POST", "/api/admin/users", strings.NewReader(
		`{"name":"self-user","peer_ip":"10.8.8.1","peer_asn":65008,"networks":["10.8.8.0/24"],`+
			`"web_auth":"network","enabled":true,"catalog_mode_id":1,`+
			`"filter_editable":`+strconv.FormatBool(filterEditable)+`,`+
			`"catalog_editable":`+strconv.FormatBool(catalogEditable)+`}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiUsersCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create user: %d body=%s", w.Code, w.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	return srv, st, created.ID
}

func TestAuditHookUserSaveFilters(t *testing.T) {
	srv, st, _ := selfServiceUserFixture(t, true, false)

	req := httptest.NewRequest("POST", "/api/user/filters", strings.NewReader(`{"allow":["10.1.0.0/16"],"deny":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "10.8.8.1:1234"
	addCSRF(req)
	w := httptest.NewRecorder()
	srv.requireUser(srv.apiUserSaveFilters).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("save filters: %d body=%s", w.Code, w.Body.String())
	}

	e := latestAuditByAction(t, st, "route_filters.user_updated")
	if !strings.HasPrefix(e.Actor, "user:") {
		t.Fatalf("actor = %q, want user:<id>", e.Actor)
	}
	if !strings.Contains(e.After, "10.1.0.0/16") {
		t.Fatalf("after = %q, want it to mention 10.1.0.0/16", e.After)
	}
}

// TestAuditHookUserSaveFiltersAfterIsNormalized guards against logging the
// raw submitted filter representation: SetUserRouteFilters persists the
// normalized form (a bare IP becomes a /32), so the audit "after" must
// match what's actually in the database, not what the client happened to
// type.
func TestAuditHookUserSaveFiltersAfterIsNormalized(t *testing.T) {
	srv, st, _ := selfServiceUserFixture(t, true, false)

	req := httptest.NewRequest("POST", "/api/user/filters", strings.NewReader(`{"allow":["10.1.0.1"],"deny":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "10.8.8.1:1234"
	addCSRF(req)
	w := httptest.NewRecorder()
	srv.requireUser(srv.apiUserSaveFilters).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("save filters: %d body=%s", w.Code, w.Body.String())
	}

	e := latestAuditByAction(t, st, "route_filters.user_updated")
	if !strings.Contains(e.After, "10.1.0.1/32") {
		t.Fatalf("after = %q, want the normalized \"10.1.0.1/32\", not the raw bare IP", e.After)
	}
}

// TestAuditHookUserSaveFiltersNoopWhenResubmittingNormalizedEquivalent
// guards against a false audit entry when the submitted representation
// normalizes to the already-stored value (a bare IP for an existing /32).
func TestAuditHookUserSaveFiltersNoopWhenResubmittingNormalizedEquivalent(t *testing.T) {
	srv, st, userID := selfServiceUserFixture(t, true, false)
	ctx := context.Background()
	if _, _, err := st.SetUserRouteFilters(ctx, userID, store.RouteFilters{Allow: []string{"10.1.0.1/32"}}, store.AuditMeta{}); err != nil {
		t.Fatalf("pre-set filters: %v", err)
	}

	req := httptest.NewRequest("POST", "/api/user/filters", strings.NewReader(`{"allow":["10.1.0.1"],"deny":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "10.8.8.1:1234"
	addCSRF(req)
	w := httptest.NewRecorder()
	srv.requireUser(srv.apiUserSaveFilters).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("save filters: %d body=%s", w.Code, w.Body.String())
	}

	if n := auditLogCount(t, st, "route_filters.user_updated"); n != 0 {
		t.Fatalf("audit log count = %d, want 0 (bare IP normalizes to the already-stored /32)", n)
	}
}

func TestAuditHookUserSwitchMode(t *testing.T) {
	srv, st, _ := selfServiceUserFixture(t, false, true)
	modeBID := createSecondModeFixture(t, srv)

	req := httptest.NewRequest("PUT", "/api/user/mode", strings.NewReader(`{"mode_id":`+strconv.FormatInt(modeBID, 10)+`}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "10.8.8.1:1234"
	addCSRF(req)
	w := httptest.NewRecorder()
	srv.requireUser(srv.apiUserSwitchMode).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("switch mode: %d body=%s", w.Code, w.Body.String())
	}

	e := latestAuditByAction(t, st, "user.mode_changed")
	if !strings.HasPrefix(e.Actor, "user:") {
		t.Fatalf("actor = %q, want user:<id>", e.Actor)
	}
	if e.Before != `{"catalog_mode_id":1}` {
		t.Fatalf("before = %q, want catalog_mode_id 1", e.Before)
	}
}

func TestAuditHookUserSaveSelections(t *testing.T) {
	srv, st, _ := selfServiceUserFixture(t, false, false)
	ctx := context.Background()
	feedID, err := st.AddFeed(ctx, "sel-feed", "http://example.com/sel.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatalf("add feed: %v", err)
	}
	if _, err := st.DB.ExecContext(ctx, "INSERT INTO catalog_mode_feeds(mode_id, feed_id) VALUES (1, ?)", feedID); err != nil {
		t.Fatalf("assign feed: %v", err)
	}
	if err := st.InsertCatalogEntries(ctx, feedID, []store.CatalogEntry{
		{Category: "cat-x", Service: "svc-x", CIDR: "172.16.0.0/16"},
	}); err != nil {
		t.Fatalf("insert catalog: %v", err)
	}
	if err := st.RebuildModeEntries(ctx, 1); err != nil {
		t.Fatalf("rebuild mode entries: %v", err)
	}

	req := httptest.NewRequest("POST", "/api/user/selections", strings.NewReader(
		`{"categories":[{"category":"cat-x","checked":true}],"services":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "10.8.8.1:1234"
	addCSRF(req)
	w := httptest.NewRecorder()
	srv.requireUser(srv.apiUserSaveSelections).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("save selections: %d body=%s", w.Code, w.Body.String())
	}

	e := latestAuditByAction(t, st, "user.selections_changed")
	if !strings.HasPrefix(e.Actor, "user:") {
		t.Fatalf("actor = %q, want user:<id>", e.Actor)
	}
	if e.Before == e.After {
		t.Fatalf("before == after (%q), want a real change", e.Before)
	}
}

func TestAuditHookAdminUserSaveSelections(t *testing.T) {
	srv, st, userID := adminUserFixture(t)
	ctx := context.Background()
	feedID, err := st.AddFeed(ctx, "admin-sel-feed", "http://example.com/sel.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatalf("add feed: %v", err)
	}
	if _, err := st.DB.ExecContext(ctx, "INSERT INTO catalog_mode_feeds(mode_id, feed_id) VALUES (1, ?)", feedID); err != nil {
		t.Fatalf("assign feed: %v", err)
	}
	if err := st.InsertCatalogEntries(ctx, feedID, []store.CatalogEntry{
		{Category: "cat-y", Service: "svc-y", CIDR: "172.17.0.0/16"},
	}); err != nil {
		t.Fatalf("insert catalog: %v", err)
	}
	if err := st.RebuildModeEntries(ctx, 1); err != nil {
		t.Fatalf("rebuild mode entries: %v", err)
	}
	idStr := strconv.FormatInt(userID, 10)

	req := httptest.NewRequest("PUT", "/api/admin/users/"+idStr+"/selections", strings.NewReader(
		`{"categories":[{"category":"cat-y","checked":true}],"services":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", idStr)
	w := httptest.NewRecorder()
	srv.apiAdminUserSaveSelections(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("admin save selections: %d body=%s", w.Code, w.Body.String())
	}

	e := latestAuditByAction(t, st, "user.selections_changed")
	if !strings.HasPrefix(e.Actor, "admin:") {
		t.Fatalf("actor = %q, want admin:<ip>", e.Actor)
	}
	if e.ObjectType != "user" || e.ObjectID != idStr {
		t.Fatalf("entry = %+v, want user/%s", e, idStr)
	}
}

// TestAuditHookAdminUserSaveSelectionsModeSwitchNoopComparesTargetMode
// guards against comparing the before-snapshot (old mode) against the
// after-snapshot (target mode) when a save switches modes — two different
// modes' selection counts are not comparable, so a pure mode switch with no
// selection changes at all must not be misreported as a selections change.
func TestAuditHookAdminUserSaveSelectionsModeSwitchNoopComparesTargetMode(t *testing.T) {
	srv, st, userID := adminUserFixture(t)
	ctx := context.Background()

	// Give the user's current mode (1) one selected category, so its
	// selection count (1) differs from the brand-new target mode's (0).
	feedID, err := st.AddFeed(ctx, "mode1-feed", "http://example.com/mode1.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatalf("add feed: %v", err)
	}
	if _, err := st.DB.ExecContext(ctx, "INSERT INTO catalog_mode_feeds(mode_id, feed_id) VALUES (1, ?)", feedID); err != nil {
		t.Fatalf("assign feed: %v", err)
	}
	if err := st.InsertCatalogEntries(ctx, feedID, []store.CatalogEntry{
		{Category: "cat-old", Service: "svc-old", CIDR: "10.50.0.0/16"},
	}); err != nil {
		t.Fatalf("insert catalog: %v", err)
	}
	if err := st.RebuildModeEntries(ctx, 1); err != nil {
		t.Fatalf("rebuild mode entries: %v", err)
	}
	if err := st.Transaction(ctx, func(tx *sql.Tx) error {
		return store.ToggleSelectedCategory(ctx, tx, userID, 1, "cat-old", true)
	}); err != nil {
		t.Fatalf("pre-select category in mode 1: %v", err)
	}

	modeBID := createSecondModeFixture(t, srv)
	idStr := strconv.FormatInt(userID, 10)

	// Switch to mode B, toggling nothing — a pure mode switch.
	req := httptest.NewRequest("PUT", "/api/admin/users/"+idStr+"/selections", strings.NewReader(
		`{"mode_id":`+strconv.FormatInt(modeBID, 10)+`,"categories":[],"services":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", idStr)
	w := httptest.NewRecorder()
	srv.apiAdminUserSaveSelections(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("admin save selections: %d body=%s", w.Code, w.Body.String())
	}

	if n := auditLogCount(t, st, "user.selections_changed"); n != 0 {
		t.Fatalf("selections_changed count = %d, want 0 (mode 1's count of 1 must not be compared against target mode B's count of 0)", n)
	}
}

// TestAuditHookModeDeleteReassignsUsersWithAudit guards against deleting a
// mode silently moving every user still pointing at it to mode 1 with no
// trace in the audit log — user.mode_changed is supposed to cover every way
// a user's mode can change, and this is one DeleteCatalogMode performs
// itself, not something apiModesDelete's caller does explicitly.
func TestAuditHookModeDeleteReassignsUsersWithAudit(t *testing.T) {
	srv, st, userID := adminUserFixture(t)
	modeBID := createSecondModeFixture(t, srv)
	ctx := context.Background()
	if _, err := st.DB.ExecContext(ctx, "UPDATE users SET catalog_mode_id = ? WHERE id = ?", modeBID, userID); err != nil {
		t.Fatalf("move user to mode B: %v", err)
	}

	req := httptest.NewRequest("DELETE", "/api/admin/modes/x", nil)
	req.SetPathValue("id", strconv.FormatInt(modeBID, 10))
	w := httptest.NewRecorder()
	srv.apiModesDelete(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("delete mode: %d body=%s", w.Code, w.Body.String())
	}

	e := latestAuditByAction(t, st, "user.mode_changed")
	if !strings.HasPrefix(e.Actor, "admin:") {
		t.Fatalf("actor = %q, want admin:<ip>", e.Actor)
	}
	if e.ObjectType != "user" || e.ObjectID != strconv.FormatInt(userID, 10) {
		t.Fatalf("entry = %+v, want user/%d", e, userID)
	}
	wantBefore := `{"catalog_mode_id":` + strconv.FormatInt(modeBID, 10) + `}`
	if e.Before != wantBefore || e.After != `{"catalog_mode_id":1}` {
		t.Fatalf("before=%q after=%q, want %q -> {\"catalog_mode_id\":1}", e.Before, e.After, wantBefore)
	}
}
