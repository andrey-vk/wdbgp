package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// =============================================================================
// TestModesList — GET /api/admin/modes returns built-in modes
// =============================================================================

func TestModesList(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)

	req := httptest.NewRequest("GET", "/api/admin/modes", nil)
	w := httptest.NewRecorder()
	srv.apiModesList(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Modes []modeJSON `json:"modes"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Modes) < 3 {
		t.Fatalf("modes count = %d, want at least 3", len(resp.Modes))
	}

	// Verify built-in modes have correct ids and keys
	expected := map[int64]string{
		1: "opencck",
		2: "ipranges",
		3: "singbox-srs",
	}
	for _, m := range resp.Modes {
		if m.ID >= 1 && m.ID <= 3 {
			if m.Key != expected[m.ID] {
				t.Errorf("mode id=%d: key = %q, want %q", m.ID, m.Key, expected[m.ID])
			}
			if m.Name == "" {
				t.Errorf("mode id=%d: name is empty", m.ID)
			}
			// feed_count may be 0 for new DB
		}
	}
}

// =============================================================================
// TestModesCRUD — create, read, update, delete lifecycle
// =============================================================================

func TestModesCRUD(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)

	// --- CREATE ---
	createBody := strings.NewReader(`{"name":"Custom Mode","enabled":true}`)
	req := httptest.NewRequest("POST", "/api/admin/modes", createBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiModesCreate(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201, body=%s", w.Code, w.Body.String())
	}
	var created modeJSON
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.ID <= 3 {
		t.Fatalf("created mode id = %d, want > 3 (beyond built-in modes)", created.ID)
	}
	if created.Name != "Custom Mode" {
		t.Fatalf("name = %q, want Custom Mode", created.Name)
	}
	if created.Key != "custom-mode" {
		t.Fatalf("key = %q, want custom-mode", created.Key)
	}
	if !created.Enabled {
		t.Fatal("enabled should be true")
	}
	modeID := created.ID

	// --- LIST confirms mode appears ---
	req = httptest.NewRequest("GET", "/api/admin/modes", nil)
	w = httptest.NewRecorder()
	srv.apiModesList(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("list: status = %d, want 200", w.Code)
	}
	var listResp struct {
		Modes []modeJSON `json:"modes"`
	}
	if err := json.NewDecoder(w.Body).Decode(&listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	found := false
	for _, m := range listResp.Modes {
		if m.ID == modeID {
			found = true
			if m.Name != "Custom Mode" {
				t.Errorf("mode in list: name = %q, want Custom Mode", m.Name)
			}
			break
		}
	}
	if !found {
		t.Fatalf("created mode id=%d not found in list", modeID)
	}

	// --- GET single ---
	req = httptest.NewRequest("GET", "/api/admin/modes/1", nil)
	req.SetPathValue("id", strconv.FormatInt(modeID, 10))
	w = httptest.NewRecorder()
	srv.apiModesGet(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("get single: status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var getResp struct {
		Mode  modeJSON       `json:"mode"`
		Feeds []modeFeedJSON `json:"feeds"`
	}
	if err := json.NewDecoder(w.Body).Decode(&getResp); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	if getResp.Mode.Name != "Custom Mode" {
		t.Fatalf("get name = %q, want Custom Mode", getResp.Mode.Name)
	}

	// --- UPDATE ---
	updateBody := strings.NewReader(`{"name":"Renamed","enabled":false}`)
	req = httptest.NewRequest("PUT", "/api/admin/modes/1", updateBody)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", strconv.FormatInt(modeID, 10))
	w = httptest.NewRecorder()
	srv.apiModesUpdate(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("update: status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var updated modeJSON
	if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
		t.Fatalf("decode update response: %v", err)
	}
	if updated.Name != "Renamed" {
		t.Fatalf("updated name = %q, want Renamed", updated.Name)
	}
	if updated.Enabled {
		t.Fatal("enabled should be false after update")
	}

	// --- GET single confirms update ---
	req = httptest.NewRequest("GET", "/api/admin/modes/1", nil)
	req.SetPathValue("id", strconv.FormatInt(modeID, 10))
	w = httptest.NewRecorder()
	srv.apiModesGet(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("get after update: status = %d", w.Code)
	}
	if err := json.NewDecoder(w.Body).Decode(&getResp); err != nil {
		t.Fatalf("decode get after update: %v", err)
	}
	if getResp.Mode.Name != "Renamed" {
		t.Fatalf("get after update name = %q, want Renamed", getResp.Mode.Name)
	}

	// --- DELETE custom mode ---
	req = httptest.NewRequest("DELETE", "/api/admin/modes/1", nil)
	req.SetPathValue("id", strconv.FormatInt(modeID, 10))
	w = httptest.NewRecorder()
	srv.apiModesDelete(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("delete: status = %d, want 200, body=%s", w.Code, w.Body.String())
	}

	// --- DELETE built-in mode (id=1) → should fail ---
	req = httptest.NewRequest("DELETE", "/api/admin/modes/1", nil)
	req.SetPathValue("id", "1")
	w = httptest.NewRecorder()
	srv.apiModesDelete(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("delete built-in: status = %d, want 400, body=%s", w.Code, w.Body.String())
	}
}

// =============================================================================
// TestModeFeedsAssign — assign feeds to mode and verify
// =============================================================================

func TestModeFeedsAssign(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)

	// --- Create a mode ---
	createBody := strings.NewReader(`{"name":"Feed Mode","enabled":true}`)
	req := httptest.NewRequest("POST", "/api/admin/modes", createBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiModesCreate(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("create mode: status = %d, want 201, body=%s", w.Code, w.Body.String())
	}
	var created modeJSON
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	modeID := created.ID

	// --- Create 2 feeds via store ---
	ctx := context.Background()
	feedID1, err := st.AddFeed(ctx, "Test Feed 1", "http://example.com/feed1.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatalf("create feed 1: %v", err)
	}
	feedID2, err := st.AddFeed(ctx, "Test Feed 2", "http://example.com/feed2.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatalf("create feed 2: %v", err)
	}

	// --- PUT feeds to mode ---
	assignBody := strings.NewReader(`{"feed_ids":[` + strconv.FormatInt(feedID1, 10) + `,` + strconv.FormatInt(feedID2, 10) + `]}`)
	req = httptest.NewRequest("PUT", "/api/admin/modes/1/feeds", assignBody)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", strconv.FormatInt(modeID, 10))
	w = httptest.NewRecorder()
	srv.apiModeFeedsSet(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("assign feeds: status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var assignResp struct {
		Feeds []modeFeedJSON `json:"feeds"`
	}
	if err := json.NewDecoder(w.Body).Decode(&assignResp); err != nil {
		t.Fatalf("decode assign response: %v", err)
	}
	if len(assignResp.Feeds) != 2 {
		t.Fatalf("assigned feeds count = %d, want 2", len(assignResp.Feeds))
	}

	// --- GET mode feeds ---
	req = httptest.NewRequest("GET", "/api/admin/modes/1/feeds", nil)
	req.SetPathValue("id", strconv.FormatInt(modeID, 10))
	w = httptest.NewRecorder()
	srv.apiModeFeedsGet(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("get mode feeds: status = %d, want 200", w.Code)
	}
	var feedsResp struct {
		Feeds []modeFeedJSON `json:"feeds"`
	}
	if err := json.NewDecoder(w.Body).Decode(&feedsResp); err != nil {
		t.Fatalf("decode feeds response: %v", err)
	}
	if len(feedsResp.Feeds) != 2 {
		t.Fatalf("mode feeds count = %d, want 2", len(feedsResp.Feeds))
	}
	for _, f := range feedsResp.Feeds {
		if f.Name != "Test Feed 1" && f.Name != "Test Feed 2" {
			t.Errorf("unexpected feed name: %q", f.Name)
		}
		if f.AdapterName == "" {
			t.Error("feed adapter_name is empty")
		}
	}

	// --- Verify via store ---
	modeFeeds, err := st.ModeFeeds(ctx, modeID)
	if err != nil {
		t.Fatalf("store ModeFeeds: %v", err)
	}
	if len(modeFeeds) != 2 {
		t.Fatalf("store ModeFeeds count = %d, want 2", len(modeFeeds))
	}
}

// =============================================================================
// TestModeCommunities — community CRUD: generate, get, put, reset
// =============================================================================

func TestModeCommunities(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)

	// --- Create a mode ---
	createBody := strings.NewReader(`{"name":"Comm Mode","enabled":true}`)
	req := httptest.NewRequest("POST", "/api/admin/modes", createBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiModesCreate(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("create mode: status = %d, want 201, body=%s", w.Code, w.Body.String())
	}
	var created modeJSON
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	modeID := created.ID

	// --- Create a feed and assign to mode ---
	ctx := context.Background()
	feedID, err := st.AddFeed(ctx, "Comm Feed", "http://example.com/comm.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	if _, err := st.DB.ExecContext(ctx, "INSERT INTO catalog_mode_feeds(mode_id, feed_id) VALUES (?, ?)", modeID, feedID); err != nil {
		t.Fatalf("assign feed to mode: %v", err)
	}

	// --- Insert catalog entry so GenerateCommunities has data to work with ---
	_, err = st.DB.ExecContext(ctx,
		"INSERT INTO catalog_entries(feed_id, category, service, cidr) VALUES (?, ?, ?, ?)",
		feedID, "test-category", "test-service", "10.0.0.0/8")
	if err != nil {
		t.Fatalf("insert catalog entry: %v", err)
	}

	// --- Generate communities ---
	req = httptest.NewRequest("POST", "/api/admin/modes/1/communities/generate", nil)
	req.SetPathValue("id", strconv.FormatInt(modeID, 10))
	w = httptest.NewRecorder()
	srv.apiModeCommunitiesGenerate(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("generate communities: status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var genResp struct {
		OK        bool `json:"ok"`
		Generated int  `json:"generated"`
	}
	if err := json.NewDecoder(w.Body).Decode(&genResp); err != nil {
		t.Fatalf("decode generate response: %v", err)
	}
	if !genResp.OK {
		t.Fatal("generate response ok should be true")
	}
	if genResp.Generated <= 0 {
		t.Fatalf("generated = %d, want > 0", genResp.Generated)
	}

	// --- GET communities ---
	req = httptest.NewRequest("GET", "/api/admin/modes/1/communities", nil)
	req.SetPathValue("id", strconv.FormatInt(modeID, 10))
	w = httptest.NewRecorder()
	srv.apiModeCommunitiesGet(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("get communities: status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var getCommResp struct {
		Communities []communityItemJSON `json:"communities"`
	}
	if err := json.NewDecoder(w.Body).Decode(&getCommResp); err != nil {
		t.Fatalf("decode communities response: %v", err)
	}
	if len(getCommResp.Communities) == 0 {
		t.Fatal("communities list is empty after generation")
	}

	// Find the test-category group community
	var groupComm uint32
	var svcComm uint32
	for _, c := range getCommResp.Communities {
		if c.Category != "test-category" {
			continue
		}
		switch c.Service {
		case "":
			groupComm = c.Community
		case "test-service":
			svcComm = c.Community
		}
	}
	if groupComm == 0 {
		t.Fatal("group community for test-category not found")
	}
	if svcComm == 0 {
		t.Fatal("service community for test-category/test-service not found")
	}

	// --- PUT communities with edited values ---
	newGroupComm := groupComm + 666
	newSvcComm := svcComm + 777
	putBody := `{"communities":[
		{"category":"test-category","service":"","community":` + strconv.FormatUint(uint64(newGroupComm), 10) + `},
		{"category":"test-category","service":"test-service","community":` + strconv.FormatUint(uint64(newSvcComm), 10) + `}
	]}`
	req = httptest.NewRequest("PUT", "/api/admin/modes/1/communities", strings.NewReader(putBody))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", strconv.FormatInt(modeID, 10))
	w = httptest.NewRecorder()
	srv.apiModeCommunitiesPut(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("put communities: status = %d, want 200, body=%s", w.Code, w.Body.String())
	}

	// --- GET communities to verify edited values persist ---
	req = httptest.NewRequest("GET", "/api/admin/modes/1/communities", nil)
	req.SetPathValue("id", strconv.FormatInt(modeID, 10))
	w = httptest.NewRecorder()
	srv.apiModeCommunitiesGet(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("get communities after put: status = %d", w.Code)
	}
	if err := json.NewDecoder(w.Body).Decode(&getCommResp); err != nil {
		t.Fatal(err)
	}
	var foundGroup, foundSvc bool
	for _, c := range getCommResp.Communities {
		if c.Category == "test-category" && c.Service == "" && c.Community == newGroupComm {
			foundGroup = true
		}
		if c.Category == "test-category" && c.Service == "test-service" && c.Community == newSvcComm {
			foundSvc = true
		}
	}
	if !foundGroup {
		t.Fatalf("group community not updated to %d", newGroupComm)
	}
	if !foundSvc {
		t.Fatalf("service community not updated to %d", newSvcComm)
	}

	// --- Reset communities ---
	req = httptest.NewRequest("POST", "/api/admin/modes/1/communities/reset", nil)
	req.SetPathValue("id", strconv.FormatInt(modeID, 10))
	w = httptest.NewRecorder()
	srv.apiModeCommunitiesReset(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("reset communities: status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var resetResp struct {
		OK        bool `json:"ok"`
		Generated int  `json:"generated"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resetResp); err != nil {
		t.Fatalf("decode reset response: %v", err)
	}
	if !resetResp.OK {
		t.Fatal("reset response ok should be true")
	}
	if resetResp.Generated <= 0 {
		t.Fatalf("reset generated = %d, want > 0", resetResp.Generated)
	}

	// --- Verify reset overwrote custom values ---
	req = httptest.NewRequest("GET", "/api/admin/modes/1/communities", nil)
	req.SetPathValue("id", strconv.FormatInt(modeID, 10))
	w = httptest.NewRecorder()
	srv.apiModeCommunitiesGet(w, req)

	if err := json.NewDecoder(w.Body).Decode(&getCommResp); err != nil {
		t.Fatal(err)
	}
	for _, c := range getCommResp.Communities {
		if c.Category == "test-category" && c.Service == "" && c.Community == newGroupComm {
			t.Fatal("group community should have been reset from custom value")
		}
		if c.Category == "test-category" && c.Service == "test-service" && c.Community == newSvcComm {
			t.Fatal("service community should have been reset from custom value")
		}
	}
}

// =============================================================================
// TestModeGetNotFound — 404 for non-existent mode
// =============================================================================

// =============================================================================
// TestModeCommunitiesGroupAutoValueMatchesGeneration — guards against a bug
// where the category (group) row's auto_community preview called
// AutoCommunity(groupIndex, 0), which applies the "+1 for first service"
// offset meant for actual services — showing 10001 for the first category
// instead of the group's own base value, 10000, which is what
// GenerateCommunities actually assigns. Admins clearing a category-level
// override would see the wrong value as "what auto-generate would produce."
// =============================================================================

func TestModeCommunitiesGroupAutoValueMatchesGeneration(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)
	ctx := context.Background()

	createBody := strings.NewReader(`{"name":"Auto Comm Mode","enabled":true}`)
	req := httptest.NewRequest("POST", "/api/admin/modes", createBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiModesCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create mode: status = %d, want 201, body=%s", w.Code, w.Body.String())
	}
	var created modeJSON
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	modeID := created.ID

	feedID, err := st.AddFeed(ctx, "Auto Comm Feed", "http://example.com/auto.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	if _, err := st.DB.ExecContext(ctx, "INSERT INTO catalog_mode_feeds(mode_id, feed_id) VALUES (?, ?)", modeID, feedID); err != nil {
		t.Fatalf("assign feed to mode: %v", err)
	}
	// Two categories, alphabetically ordered, so groupIndex 0 and 1 both get checked.
	if _, err := st.DB.ExecContext(ctx,
		"INSERT INTO catalog_entries(feed_id, category, service, cidr) VALUES (?, ?, ?, ?)",
		feedID, "a-category", "svc", "10.0.0.0/8"); err != nil {
		t.Fatalf("insert catalog entry: %v", err)
	}
	if _, err := st.DB.ExecContext(ctx,
		"INSERT INTO catalog_entries(feed_id, category, service, cidr) VALUES (?, ?, ?, ?)",
		feedID, "b-category", "svc", "10.1.0.0/16"); err != nil {
		t.Fatalf("insert catalog entry: %v", err)
	}

	genReq := httptest.NewRequest("POST", "/api/admin/modes/1/communities/generate", nil)
	genReq.SetPathValue("id", strconv.FormatInt(modeID, 10))
	genW := httptest.NewRecorder()
	srv.apiModeCommunitiesGenerate(genW, genReq)
	if genW.Code != http.StatusOK {
		t.Fatalf("generate communities: status = %d, want 200, body=%s", genW.Code, genW.Body.String())
	}

	getReq := httptest.NewRequest("GET", "/api/admin/modes/1/communities", nil)
	getReq.SetPathValue("id", strconv.FormatInt(modeID, 10))
	getW := httptest.NewRecorder()
	srv.apiModeCommunitiesGet(getW, getReq)
	if getW.Code != http.StatusOK {
		t.Fatalf("get communities: status = %d, want 200, body=%s", getW.Code, getW.Body.String())
	}
	var resp struct {
		Communities []communityItemJSON `json:"communities"`
	}
	if err := json.NewDecoder(getW.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	groupAuto := map[string]uint32{}
	groupReal := map[string]uint32{}
	for _, c := range resp.Communities {
		if c.Service != "" {
			continue
		}
		groupAuto[c.Category] = c.AutoCommunity
		groupReal[c.Category] = c.Community
	}

	if groupAuto["a-category"] != 10000 {
		t.Errorf("a-category auto_community = %d, want 10000 (the group base, not +1)", groupAuto["a-category"])
	}
	if groupAuto["b-category"] != 20000 {
		t.Errorf("b-category auto_community = %d, want 20000", groupAuto["b-category"])
	}
	// The preview must match what GenerateCommunities actually assigned —
	// that's the whole point of showing it to the admin.
	if groupAuto["a-category"] != groupReal["a-category"] {
		t.Errorf("a-category auto_community (%d) != actual generated community (%d)", groupAuto["a-category"], groupReal["a-category"])
	}
	if groupAuto["b-category"] != groupReal["b-category"] {
		t.Errorf("b-category auto_community (%d) != actual generated community (%d)", groupAuto["b-category"], groupReal["b-category"])
	}
}

// =============================================================================
// TestModesPartialUpdateEnabledOnly — partial update only sets enabled, name unchanged
// =============================================================================

func TestModesPartialUpdateEnabledOnly(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)

	// Create mode with enabled=true
	body := strings.NewReader(`{"name":"Test","enabled":true}`)
	req := httptest.NewRequest("POST", "/api/admin/modes", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiModesCreate(w, req)

	var created modeJSON
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}

	// Partial update: only enabled=false
	body = strings.NewReader(`{"enabled":false}`)
	req = httptest.NewRequest("PUT", "/api/admin/modes/"+strconv.FormatInt(created.ID, 10), body)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", strconv.FormatInt(created.ID, 10))
	w = httptest.NewRecorder()
	srv.apiModesUpdate(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200. body: %s", w.Code, w.Body.String())
	}

	var updated modeJSON
	if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
		t.Fatal(err)
	}
	if updated.Enabled {
		t.Fatal("enabled should be false after partial update")
	}
	if updated.Name != "Test" {
		t.Fatalf("name should be unchanged 'Test', got '%s'", updated.Name)
	}
}

func TestModeGetNotFound(t *testing.T) {
	srv, _, _ := setupUserTestServer(t)

	req := httptest.NewRequest("GET", "/api/admin/modes/99999", nil)
	req.SetPathValue("id", "99999")
	w := httptest.NewRecorder()
	srv.apiModesGet(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

// =============================================================================
// TestModeFeedReplaceAtomic — feed replacement uses a transaction: on invalid
// feed ID the original assignments must be preserved (rollback).
// =============================================================================

func TestModeFeedReplaceAtomic(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)
	ctx := context.Background()

	// Create a mode
	modeBody := `{"name":"AtomicMode","enabled":true}`
	req := httptest.NewRequest("POST", "/api/admin/modes", strings.NewReader(modeBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiModesCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create mode: %d %s", w.Code, w.Body.String())
	}
	var created modeJSON
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	modeID := created.ID

	// Create 2 feeds
	f1, err := st.AddFeed(ctx, "F1", "http://a.com/f1.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatalf("add feed F1: %v", err)
	}
	f2, err := st.AddFeed(ctx, "F2", "http://a.com/f2.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatalf("add feed F2: %v", err)
	}

	// Assign both feeds to mode
	assignBody := fmt.Sprintf(`{"feed_ids":[%d,%d]}`, f1, f2)
	req = httptest.NewRequest("PUT", "/api/admin/modes/"+strconv.FormatInt(modeID, 10)+"/feeds", strings.NewReader(assignBody))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", strconv.FormatInt(modeID, 10))
	w = httptest.NewRecorder()
	srv.apiModeFeedsSet(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("assign feeds: %d %s", w.Code, w.Body.String())
	}

	// Now try replacing with an INVALID feed ID (9999 doesn't exist → FK violation)
	badBody := fmt.Sprintf(`{"feed_ids":[%d,9999]}`, f1)
	req = httptest.NewRequest("PUT", "/api/admin/modes/"+strconv.FormatInt(modeID, 10)+"/feeds", strings.NewReader(badBody))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", strconv.FormatInt(modeID, 10))
	w = httptest.NewRecorder()
	srv.apiModeFeedsSet(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("invalid feed: got %d, want 500", w.Code)
	}

	// Verify BOTH original feeds are still assigned (rollback worked)
	req = httptest.NewRequest("GET", "/api/admin/modes/"+strconv.FormatInt(modeID, 10)+"/feeds", nil)
	req.SetPathValue("id", strconv.FormatInt(modeID, 10))
	w = httptest.NewRecorder()
	srv.apiModeFeedsGet(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get feeds: %d", w.Code)
	}
	var feedsResp struct {
		Feeds []modeFeedJSON `json:"feeds"`
	}
	if err := json.NewDecoder(w.Body).Decode(&feedsResp); err != nil {
		t.Fatal(err)
	}
	if len(feedsResp.Feeds) != 2 {
		t.Errorf("after failed replace: got %d feeds, want 2 (rollback should preserve both)", len(feedsResp.Feeds))
	}
}

// =============================================================================
// TestModeFeedsSetNoRollbackWarning — verifies no rollback warning is logged
// after a successful commit (the defer should skip rollback when committed).
// =============================================================================

func TestModeFeedsSetNoRollbackWarning(t *testing.T) {
	srv, st, _ := setupUserTestServer(t)

	// Create a mode
	createBody := strings.NewReader(`{"name":"NoWarn Mode","enabled":true}`)
	req := httptest.NewRequest("POST", "/api/admin/modes", createBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.apiModesCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create mode: status = %d, want 201, body=%s", w.Code, w.Body.String())
	}
	var created modeJSON
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	modeID := created.ID

	// Create a feed
	ctx := context.Background()
	feedID, err := st.AddFeed(ctx, "NoWarn Feed", "http://example.com/feed.json", 1, true, 0, "", "", true)
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	// Capture log output during the handler call
	var logBuf bytes.Buffer
	origOutput := log.Writer()
	log.SetOutput(&logBuf)
	defer log.SetOutput(origOutput)

	// Assign feed to mode (success path — commit should succeed)
	assignBody := strings.NewReader(`{"feed_ids":[` + strconv.FormatInt(feedID, 10) + `]}`)
	req = httptest.NewRequest("PUT", "/api/admin/modes/1/feeds", assignBody)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", strconv.FormatInt(modeID, 10))
	w = httptest.NewRecorder()
	srv.apiModeFeedsSet(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("assign feeds: status = %d, want 200, body=%s", w.Code, w.Body.String())
	}

	// Check that no rollback warning was logged (the bug: defer rollback runs after successful commit)
	logOutput := logBuf.String()
	if strings.Contains(logOutput, "mode feeds transaction rollback") {
		t.Errorf("rollback warning logged after successful commit:\n%s", logOutput)
	}
}
