package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/andrey-vk/wdbgp/internal/store"
)

// exportFixture builds a server with one mode carrying one category/service
// with generated communities, and a status token set so the export is
// reachable.
func exportFixture(t *testing.T) (*Server, *fakeBGP, int64) {
	t.Helper()
	srv, st, bgp := setupUserTestServer(t)
	ctx := context.Background()

	if err := srv.settings.StatusToken.Set(ctx, "secret"); err != nil {
		t.Fatalf("set status token: %v", err)
	}
	// testSettings opens the /status CIDR allowlist to 0.0.0.0/0; clear it so
	// these tests exercise the token gate rather than the address gate.
	if err := srv.settings.StatusAllowed.Set(ctx, ""); err != nil {
		t.Fatalf("clear status allowlist: %v", err)
	}

	modeID, err := st.AddCatalogMode(ctx, "Export Mode", true)
	if err != nil {
		t.Fatalf("add mode: %v", err)
	}
	feedID, err := st.AddFeed(ctx, "Export Feed", "http://example.com/e.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatalf("add feed: %v", err)
	}
	if _, err := st.DB.ExecContext(ctx,
		"INSERT INTO catalog_mode_feeds(mode_id, feed_id) VALUES (?, ?)", modeID, feedID); err != nil {
		t.Fatalf("assign feed: %v", err)
	}
	if err := st.InsertCatalogEntries(ctx, feedID, []store.CatalogEntry{
		{Category: "AI", Service: "ChatGPT", CIDR: "10.0.0.0/8"},
		{Category: "AI", Service: "ChatGPT", CIDR: "2001:db8::/32"},
	}); err != nil {
		t.Fatalf("insert entries: %v", err)
	}
	if err := st.RebuildModeEntries(ctx, modeID); err != nil {
		t.Fatalf("rebuild mode entries: %v", err)
	}
	if _, err := st.GenerateCommunities(ctx, modeID); err != nil {
		t.Fatalf("generate communities: %v", err)
	}
	return srv, bgp, modeID
}

func exportRequest(token string) *http.Request {
	req := httptest.NewRequest("GET", "/api/communities", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req
}

func decodeExport(t *testing.T, body []byte) communityExportDoc {
	t.Helper()
	var doc communityExportDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("decode export: %v (body=%s)", err, body)
	}
	return doc
}

func TestCommunitiesExportRequiresToken(t *testing.T) {
	srv, _, _ := exportFixture(t)

	w := httptest.NewRecorder()
	srv.apiCommunitiesExport(w, exportRequest(""))
	if w.Code != http.StatusForbidden {
		t.Fatalf("no token: status = %d, want 403", w.Code)
	}

	w = httptest.NewRecorder()
	srv.apiCommunitiesExport(w, exportRequest("wrong"))
	if w.Code != http.StatusForbidden {
		t.Fatalf("wrong token: status = %d, want 403", w.Code)
	}

	w = httptest.NewRecorder()
	srv.apiCommunitiesExport(w, exportRequest("secret"))
	if w.Code != http.StatusOK {
		t.Fatalf("correct token: status = %d, want 200", w.Code)
	}

	// The address allowlist is the other accepted gate, same as /status.
	if err := srv.settings.StatusAllowed.Set(context.Background(), "0.0.0.0/0"); err != nil {
		t.Fatalf("set status allowlist: %v", err)
	}
	w = httptest.NewRecorder()
	srv.apiCommunitiesExport(w, exportRequest(""))
	if w.Code != http.StatusOK {
		t.Fatalf("allowlisted address without token: status = %d, want 200", w.Code)
	}
}

func TestCommunitiesExportShape(t *testing.T) {
	srv, _, modeID := exportFixture(t)

	w := httptest.NewRecorder()
	srv.apiCommunitiesExport(w, exportRequest("secret"))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	doc := decodeExport(t, w.Body.Bytes())

	if doc.SchemaVersion != communityExportSchemaVersion {
		t.Fatalf("schema_version = %d, want %d", doc.SchemaVersion, communityExportSchemaVersion)
	}
	if doc.GeneratedAt == "" {
		t.Fatal("generated_at is empty")
	}

	var mode *communityExportMode
	for i := range doc.Modes {
		if doc.Modes[i].ModeID == modeID {
			mode = &doc.Modes[i]
		}
	}
	if mode == nil {
		t.Fatalf("mode %d missing from export: %+v", modeID, doc.Modes)
	}
	if len(mode.Categories) != 1 || mode.Categories[0].Name != "AI" {
		t.Fatalf("categories = %+v, want one named AI", mode.Categories)
	}
	category := mode.Categories[0]
	if category.Community == 0 {
		t.Fatal("category community not assigned")
	}
	// Counts are per address family, deduped from the materialized entries.
	if category.PrefixCountV4 != 1 || category.PrefixCountV6 != 1 {
		t.Fatalf("category counts = v4:%d v6:%d, want 1/1",
			category.PrefixCountV4, category.PrefixCountV6)
	}
	if len(category.Services) != 1 || category.Services[0].Name != "ChatGPT" {
		t.Fatalf("services = %+v, want one named ChatGPT", category.Services)
	}
	service := category.Services[0]
	if service.Community == category.Community {
		t.Fatalf("service and category share community %d", service.Community)
	}

	// The rendered wire form must match what buildRoute stamps: <asn>:0:<n>.
	wantCategory := largeCommunityString(doc.ASN, category.Community)
	if category.LargeCommunity != wantCategory {
		t.Fatalf("category large_community = %q, want %q", category.LargeCommunity, wantCategory)
	}
	wantService := largeCommunityString(doc.ASN, service.Community)
	if service.LargeCommunity != wantService {
		t.Fatalf("service large_community = %q, want %q", service.LargeCommunity, wantService)
	}
}

// TestCommunitiesExportETag covers the polling contract: a repeat request
// with the previous ETag must answer 304, and the ETag must not churn just
// because time passed (generated_at is excluded from the hash).
func TestCommunitiesExportETag(t *testing.T) {
	srv, _, _ := exportFixture(t)

	w := httptest.NewRecorder()
	srv.apiCommunitiesExport(w, exportRequest("secret"))
	etag := w.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag on first response")
	}

	w2 := httptest.NewRecorder()
	srv.apiCommunitiesExport(w2, exportRequest("secret"))
	if got := w2.Header().Get("ETag"); got != etag {
		t.Fatalf("ETag changed between identical requests: %q then %q", etag, got)
	}

	req := exportRequest("secret")
	req.Header.Set("If-None-Match", etag)
	w3 := httptest.NewRecorder()
	srv.apiCommunitiesExport(w3, req)
	if w3.Code != http.StatusNotModified {
		t.Fatalf("matching If-None-Match: status = %d, want 304", w3.Code)
	}
	if w3.Body.Len() != 0 {
		t.Fatalf("304 carried a body: %s", w3.Body.String())
	}
}

// TestCommunitiesExportETagChangesWithData verifies the hash actually tracks
// content, so a consumer polling on 304 is not left stale after a renumber.
func TestCommunitiesExportETagChangesWithData(t *testing.T) {
	srv, _, modeID := exportFixture(t)

	w := httptest.NewRecorder()
	srv.apiCommunitiesExport(w, exportRequest("secret"))
	before := w.Header().Get("ETag")

	if err := srv.store.SetCommunity(context.Background(), modeID, "AI", "", 12345); err != nil {
		t.Fatalf("set community: %v", err)
	}

	w2 := httptest.NewRecorder()
	srv.apiCommunitiesExport(w2, exportRequest("secret"))
	if after := w2.Header().Get("ETag"); after == before {
		t.Fatalf("ETag %q unchanged after renumbering a community", after)
	}
}

// TestCommunitiesExportASNReporting covers the honesty requirement: the
// document reports the ASN actually on the wire, and discloses a diverging
// configured value rather than quietly presenting it as live.
func TestCommunitiesExportASNReporting(t *testing.T) {
	srv, bgp, _ := exportFixture(t)
	ctx := context.Background()

	configured := srv.settings.LocalASN.Get()

	// Speaker running with the configured ASN: no divergence to report.
	bgp.activeASN = configured
	w := httptest.NewRecorder()
	srv.apiCommunitiesExport(w, exportRequest("secret"))
	doc := decodeExport(t, w.Body.Bytes())
	if !doc.BGPRunning {
		t.Fatal("bgp_running = false, want true")
	}
	if doc.ASN != configured {
		t.Fatalf("asn = %d, want %d", doc.ASN, configured)
	}
	if doc.ASNConfigured != nil {
		t.Fatalf("asn_configured = %d, want omitted when equal", *doc.ASNConfigured)
	}

	// Setting changed but the session has not restarted: the wire still
	// carries the old ASN, and the new one must be disclosed separately.
	bgp.activeASN = configured
	newASN := configured + 1
	if err := srv.settings.LocalASN.Set(ctx, newASN); err != nil {
		t.Fatalf("set local asn: %v", err)
	}
	w = httptest.NewRecorder()
	srv.apiCommunitiesExport(w, exportRequest("secret"))
	doc = decodeExport(t, w.Body.Bytes())
	if doc.ASN != configured {
		t.Fatalf("asn = %d, want the live snapshot %d", doc.ASN, configured)
	}
	if doc.ASNConfigured == nil || *doc.ASNConfigured != newASN {
		t.Fatalf("asn_configured = %v, want %d", doc.ASNConfigured, newASN)
	}

	// No speaker: nothing is announced, so report the configured value and
	// say the speaker is down instead of inventing a wire ASN.
	bgp.down = true
	w = httptest.NewRecorder()
	srv.apiCommunitiesExport(w, exportRequest("secret"))
	doc = decodeExport(t, w.Body.Bytes())
	if doc.BGPRunning {
		t.Fatal("bgp_running = true, want false")
	}
	if doc.ASN != newASN {
		t.Fatalf("asn = %d, want configured %d when speaker is down", doc.ASN, newASN)
	}
}

// TestCommunitiesExportIncludesDisabled keeps the map complete: a disabled
// mode's communities still exist, and a policy generated from a map that
// omitted them would break the moment the mode is re-enabled.
func TestCommunitiesExportIncludesDisabledMode(t *testing.T) {
	srv, _, modeID := exportFixture(t)
	ctx := context.Background()

	if err := srv.store.UpdateCatalogMode(ctx,
		store.CatalogMode{ID: modeID, Name: "Export Mode", Enabled: false}); err != nil {
		t.Fatalf("disable mode: %v", err)
	}

	w := httptest.NewRecorder()
	srv.apiCommunitiesExport(w, exportRequest("secret"))
	doc := decodeExport(t, w.Body.Bytes())

	for _, mode := range doc.Modes {
		if mode.ModeID != modeID {
			continue
		}
		if mode.Enabled {
			t.Fatal("disabled mode reported as enabled")
		}
		if len(mode.Categories) == 0 {
			t.Fatal("disabled mode lost its categories")
		}
		return
	}
	t.Fatalf("disabled mode %d missing from export", modeID)
}

// TestCommunitiesExportAcceptsAdminSession keeps the admin UI able to fetch
// the document from the browser, where no status token is presented.
func TestCommunitiesExportAcceptsAdminSession(t *testing.T) {
	srv, _, _ := exportFixture(t)

	req := exportRequest("")
	req.AddCookie(adminCookie(srv.settings))
	w := httptest.NewRecorder()
	srv.apiCommunitiesExport(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("admin session: status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
}

// TestCommunitiesExportHandlesPipeInCategoryName covers a real collision in
// the naive "category|service" map key: a category literally named "a|b"
// and the pair (category "a", service "b") both flatten to the string
// "a|b" and would overwrite each other in a single map. The export must
// build its lookup from structured rows instead, so both keep their own,
// independently assigned community.
func TestCommunitiesExportHandlesPipeInCategoryName(t *testing.T) {
	srv, _, modeID := exportFixture(t)
	ctx := context.Background()

	feedID, err := srv.store.AddFeed(ctx, "Pipe Feed", "http://example.com/pipe.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatalf("add feed: %v", err)
	}
	if _, err := srv.store.DB.ExecContext(ctx,
		"INSERT INTO catalog_mode_feeds(mode_id, feed_id) VALUES (?, ?)", modeID, feedID); err != nil {
		t.Fatalf("assign feed: %v", err)
	}
	if err := srv.store.InsertCatalogEntries(ctx, feedID, []store.CatalogEntry{
		{Category: "a", Service: "b", CIDR: "172.16.0.0/24"},
		{Category: "a|b", Service: "x", CIDR: "172.16.1.0/24"},
	}); err != nil {
		t.Fatalf("insert entries: %v", err)
	}
	if err := srv.store.RebuildModeEntries(ctx, modeID); err != nil {
		t.Fatalf("rebuild mode entries: %v", err)
	}
	if _, err := srv.store.GenerateCommunities(ctx, modeID); err != nil {
		t.Fatalf("generate communities: %v", err)
	}

	// Ground truth, from the same structured rows the export is supposed to
	// use — not from the flattened GetCommunities map this test exists to
	// route around.
	rows, err := srv.store.CommunityRows(ctx, modeID)
	if err != nil {
		t.Fatalf("read community rows: %v", err)
	}
	var wantGroupAB, wantServiceAB uint32
	for _, row := range rows {
		switch {
		case row.Category == "a|b" && row.Service == "":
			wantGroupAB = row.Community
		case row.Category == "a" && row.Service == "b":
			wantServiceAB = row.Community
		}
	}
	if wantGroupAB == 0 || wantServiceAB == 0 {
		t.Fatalf("fixture did not produce both assignments: group(a|b)=%d service(a,b)=%d",
			wantGroupAB, wantServiceAB)
	}
	if wantGroupAB == wantServiceAB {
		t.Fatalf("fixture produced colliding communities by coincidence: both %d", wantGroupAB)
	}

	w := httptest.NewRecorder()
	srv.apiCommunitiesExport(w, exportRequest("secret"))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	doc := decodeExport(t, w.Body.Bytes())

	var mode *communityExportMode
	for i := range doc.Modes {
		if doc.Modes[i].ModeID == modeID {
			mode = &doc.Modes[i]
		}
	}
	if mode == nil {
		t.Fatalf("mode %d missing from export", modeID)
	}

	var gotGroupAB, gotServiceAB uint32
	var foundGroupAB, foundServiceAB bool
	for _, category := range mode.Categories {
		if category.Name == "a|b" {
			gotGroupAB = category.Community
			foundGroupAB = true
		}
		if category.Name == "a" {
			for _, service := range category.Services {
				if service.Name == "b" {
					gotServiceAB = service.Community
					foundServiceAB = true
				}
			}
		}
	}
	if !foundGroupAB || !foundServiceAB {
		t.Fatalf("export missing one of the colliding pairs: group(a|b) found=%v, service(a,b) found=%v",
			foundGroupAB, foundServiceAB)
	}
	if gotGroupAB != wantGroupAB {
		t.Fatalf("category a|b community = %d, want %d (would be %d if collided with service a/b)",
			gotGroupAB, wantGroupAB, wantServiceAB)
	}
	if gotServiceAB != wantServiceAB {
		t.Fatalf("service a/b community = %d, want %d (would be %d if collided with group a|b)",
			gotServiceAB, wantServiceAB, wantGroupAB)
	}
}

// TestCommunitiesExportGeneratesMissingAssignments covers the window a feed
// sync leaves open: it publishes the catalog and generates communities for
// it as two separate transactions, so a request landing in between would
// otherwise see a service with no assignment yet. This reproduces exactly
// that intermediate state (catalog published via RebuildModeEntries,
// GenerateCommunities deliberately not called) and asserts the export's
// atomic ModeCommunitySnapshot (generate-then-read in one transaction)
// still returns a real assignment rather than community 0.
func TestCommunitiesExportGeneratesMissingAssignments(t *testing.T) {
	srv, _, modeID := exportFixture(t)
	ctx := context.Background()

	feedID, err := srv.store.AddFeed(ctx, "Midsync Feed", "http://example.com/midsync.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatalf("add feed: %v", err)
	}
	if _, err := srv.store.DB.ExecContext(ctx,
		"INSERT INTO catalog_mode_feeds(mode_id, feed_id) VALUES (?, ?)", modeID, feedID); err != nil {
		t.Fatalf("assign feed: %v", err)
	}
	if err := srv.store.InsertCatalogEntries(ctx, feedID, []store.CatalogEntry{
		{Category: "Midsync", Service: "NewSvc", CIDR: "172.20.0.0/24"},
	}); err != nil {
		t.Fatalf("insert entries: %v", err)
	}
	// Publish the catalog, deliberately without calling GenerateCommunities —
	// this is exactly the state a request can observe between a feed sync's
	// two separate transactions.
	if err := srv.store.RebuildModeEntries(ctx, modeID); err != nil {
		t.Fatalf("rebuild mode entries: %v", err)
	}

	w := httptest.NewRecorder()
	srv.apiCommunitiesExport(w, exportRequest("secret"))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	doc := decodeExport(t, w.Body.Bytes())

	var mode *communityExportMode
	for i := range doc.Modes {
		if doc.Modes[i].ModeID == modeID {
			mode = &doc.Modes[i]
		}
	}
	if mode == nil {
		t.Fatalf("mode %d missing from export", modeID)
	}
	var found *communityExportCategory
	for i := range mode.Categories {
		if mode.Categories[i].Name == "Midsync" {
			found = &mode.Categories[i]
		}
	}
	if found == nil {
		t.Fatal("category added mid-sync is missing from the export")
	}
	if found.Community == 0 || found.LargeCommunity == "" {
		t.Fatalf("category added mid-sync exported with no assignment: community=%d large_community=%q",
			found.Community, found.LargeCommunity)
	}
	if len(found.Services) != 1 || found.Services[0].Community == 0 {
		t.Fatalf("service added mid-sync exported with no assignment: %+v", found.Services)
	}
}

// TestCommunitiesExportReconcileStatus covers the honesty requirement the
// previous ASN fix established, extended to community values themselves: a
// community edit/reset commits to the database and reconciles best-effort,
// so the document must disclose whether that last push to BGP peers
// actually succeeded rather than silently presenting database state as if
// it were already confirmed live.
func TestCommunitiesExportReconcileStatus(t *testing.T) {
	srv, bgp, _ := exportFixture(t)

	// Never attempted (e.g. a speaker that hasn't completed its first
	// reconcile) must omit the field entirely, not report a false "ok".
	w := httptest.NewRecorder()
	srv.apiCommunitiesExport(w, exportRequest("secret"))
	doc := decodeExport(t, w.Body.Bytes())
	if doc.ReconcileOK != nil {
		t.Fatalf("reconcile_ok = %v, want omitted when never attempted", *doc.ReconcileOK)
	}
	if doc.ReconcileAt != "" || doc.ReconcileError != "" {
		t.Fatalf("reconcile_at/reconcile_error set despite no attempt: at=%q error=%q",
			doc.ReconcileAt, doc.ReconcileError)
	}

	// A successful reconcile reports ok with a timestamp, no error.
	bgp.reconcileAt = time.Now()
	w = httptest.NewRecorder()
	srv.apiCommunitiesExport(w, exportRequest("secret"))
	doc = decodeExport(t, w.Body.Bytes())
	if doc.ReconcileOK == nil || !*doc.ReconcileOK {
		t.Fatalf("reconcile_ok = %v, want true", doc.ReconcileOK)
	}
	if doc.ReconcileAt == "" {
		t.Fatal("reconcile_at missing on a successful reconcile")
	}
	if doc.ReconcileError != "" {
		t.Fatalf("reconcile_error = %q, want empty on success", doc.ReconcileError)
	}

	// A failed reconcile — the database has moved ahead of what peers
	// actually received — must report ok=false with the error, not silently
	// look identical to a healthy document.
	bgp.reconcileErr = fmt.Errorf("announce to 10.0.0.1 AS65001: write: broken pipe")
	w = httptest.NewRecorder()
	srv.apiCommunitiesExport(w, exportRequest("secret"))
	doc = decodeExport(t, w.Body.Bytes())
	if doc.ReconcileOK == nil || *doc.ReconcileOK {
		t.Fatalf("reconcile_ok = %v, want false after a failed reconcile", doc.ReconcileOK)
	}
	if doc.ReconcileError == "" {
		t.Fatal("reconcile_error missing after a failed reconcile")
	}
}

// TestCommunitiesExportReconcileAtExcludedFromETag covers the same
// no-timestamp-churn contract GeneratedAt already has: a reconcile that
// re-runs with an unchanged outcome (still ok, or still the same error)
// must not change the ETag just because the clock moved, or a poller would
// never see a 304 on an otherwise-idle instance. A genuine ok -> not-ok
// transition must still change it, since that is meaningful content.
func TestCommunitiesExportReconcileAtExcludedFromETag(t *testing.T) {
	srv, bgp, _ := exportFixture(t)
	bgp.reconcileAt = time.Now()

	w := httptest.NewRecorder()
	srv.apiCommunitiesExport(w, exportRequest("secret"))
	etag := w.Header().Get("ETag")

	// Same outcome, later timestamp: ETag must not change.
	bgp.reconcileAt = time.Now().Add(time.Minute)
	w2 := httptest.NewRecorder()
	srv.apiCommunitiesExport(w2, exportRequest("secret"))
	if got := w2.Header().Get("ETag"); got != etag {
		t.Fatalf("ETag changed from a later reconcile_at with the same outcome: %q -> %q", etag, got)
	}

	// Outcome actually changes: ETag must change.
	bgp.reconcileErr = fmt.Errorf("announce failed")
	w3 := httptest.NewRecorder()
	srv.apiCommunitiesExport(w3, exportRequest("secret"))
	if got := w3.Header().Get("ETag"); got == etag {
		t.Fatal("ETag unchanged after reconcile_ok flipped from true to false")
	}
}
