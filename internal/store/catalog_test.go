package store

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func TestCatalogModesKeepSelectionsAndRoutesIsolated(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	modes, err := s.CatalogModes(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(modes) != 3 || modes[0].Key != "opencck" || modes[1].Key != "ipranges" || modes[2].Key != "singbox-srs" {
		t.Fatalf("catalog modes = %#v", modes)
	}
	ipranges := modes[1]
	ipranges.Enabled = true
	if err := s.UpdateCatalogMode(ctx, ipranges); err != nil {
		t.Fatal(err)
	}

	var openCCKFeedID, ipRangesFeedID int64
	if err := s.DB.QueryRow(	"SELECT f.id FROM feeds f JOIN catalog_mode_feeds cmf ON cmf.feed_id = f.id WHERE cmf.mode_id = 1 ORDER BY f.id LIMIT 1").
		Scan(&openCCKFeedID); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(		"SELECT f.id FROM feeds f JOIN catalog_mode_feeds cmf ON cmf.feed_id = f.id WHERE cmf.mode_id = ?", ipranges.ID).
		Scan(&ipRangesFeedID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO catalog_entries(feed_id, category, service, cidr) VALUES
		(?, 'Open', 'Wide', '8.8.0.0/16'),
		(?, 'Precise', 'Narrow', '8.8.8.0/24')`,
		openCCKFeedID, ipRangesFeedID); err != nil {
		t.Fatal(err)
	}
	userID, err := s.AddUser(ctx, User{
		Name: "client", PeerIP: "172.16.0.2", PeerASN: 65001, Enabled: true,
		CatalogModeID: 1, CatalogEditable: true,
		Networks: []string{"192.168.20.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		if err := SetUserModeSelection(ctx, tx, userID, 1, []string{"Open"}, nil); err != nil {
			return err
		}
		return SetUserModeSelection(ctx, tx, userID, ipranges.ID, []string{"Precise"}, nil)
	}); err != nil {
		t.Fatal(err)
	}

	prefixes, _, err := s.DesiredPrefixes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(prefixes) != 1 || len(prefixes["8.8.0.0/16"]) != 1 {
		t.Fatalf("OpenCCK prefixes = %#v", prefixes)
	}
	if err := s.SetUserCatalogMode(ctx, userID, ipranges.ID, true); err != nil {
		t.Fatal(err)
	}
	prefixes, _, err = s.DesiredPrefixes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(prefixes) != 1 || len(prefixes["8.8.8.0/24"]) != 1 {
		t.Fatalf("IPRanges prefixes = %#v", prefixes)
	}

	ipranges.Enabled = false
	if err := s.UpdateCatalogMode(ctx, ipranges); err != nil {
		t.Fatal(err)
	}
	prefixes, _, err = s.DesiredPrefixes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(prefixes) != 0 {
		t.Fatalf("disabled mode prefixes = %#v", prefixes)
	}
	categories, _, err := s.UserModeSelection(ctx, userID, ipranges.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !categories["Precise"] {
		t.Fatalf("disabled mode selection was lost: %#v", categories)
	}
}

func TestUserCannotChangeCatalogModeWithoutPermission(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	modes, err := s.CatalogModes(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	ipranges := modes[1]
	ipranges.Enabled = true
	if err := s.UpdateCatalogMode(ctx, ipranges); err != nil {
		t.Fatal(err)
	}
	userID, err := s.AddUser(ctx, User{
		Name: "managed", PeerIP: "172.16.0.2", PeerASN: 65001, Enabled: true,
		CatalogModeID: 1, Networks: []string{"192.168.20.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetUserCatalogMode(ctx, userID, ipranges.ID, true); !IsNotFound(err) {
		t.Fatalf("mode change error = %v, want not found", err)
	}
	if err := s.SetUserCatalogMode(ctx, userID, 1, true); err != nil {
		t.Fatalf("saving current managed mode: %v", err)
	}
}

func TestDisabledFeedIsExcludedWithoutDeletingSnapshot(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.AddFeed(ctx, "custom", "https://example.test/feed.json", true, 0); err != nil {
		t.Fatal(err)
	}
	feeds, err := s.Feeds(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	feed := feeds[len(feeds)-1]
	if _, err := s.DB.Exec(`INSERT INTO catalog_entries(feed_id, category, service, cidr)
		VALUES (?, 'Custom', 'Example', '8.8.8.0/24')`, feed.ID); err != nil {
		t.Fatal(err)
	}
	userID, err := s.AddUser(ctx, User{
		Name: "client", PeerIP: "172.16.0.2", PeerASN: 65001, Enabled: true,
		Networks: []string{"192.168.20.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		return SetUserSelection(ctx, tx, userID, []string{"Custom"}, nil)
	}); err != nil {
		t.Fatal(err)
	}

	catalog, err := s.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog["Custom"]) != 1 {
		t.Fatalf("enabled feed missing from catalog: %#v", catalog)
	}
	prefixes, _, err := s.DesiredPrefixes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(prefixes["8.8.8.0/24"]) != 1 {
		t.Fatalf("enabled feed prefix missing: %#v", prefixes)
	}

	feed.Enabled = false
	if err := s.UpdateFeed(ctx, feed); err != nil {
		t.Fatal(err)
	}
	catalog, err = s.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := catalog["Custom"]; ok {
		t.Fatalf("disabled feed remains in catalog: %#v", catalog)
	}
	prefixes, _, err = s.DesiredPrefixes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(prefixes) != 0 {
		t.Fatalf("disabled feed remains announced: %#v", prefixes)
	}
	var entries int
	if err := s.DB.QueryRow("SELECT COUNT(*) FROM catalog_entries WHERE feed_id = ?", feed.ID).
		Scan(&entries); err != nil {
		t.Fatal(err)
	}
	if entries != 1 {
		t.Fatalf("disabled feed snapshot entries = %d, want 1", entries)
	}

	feed.Enabled = true
	if err := s.UpdateFeed(ctx, feed); err != nil {
		t.Fatal(err)
	}
	prefixes, _, err = s.DesiredPrefixes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(prefixes["8.8.8.0/24"]) != 1 {
		t.Fatalf("re-enabled feed prefix missing: %#v", prefixes)
	}
}

func TestSetVisibleUserSelectionPreservesDisabledOnlySelections(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.DB.Exec("UPDATE feeds SET enabled = 0"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddFeed(ctx, "enabled", "https://example.test/enabled", true, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.AddFeed(ctx, "disabled", "https://example.test/disabled", false, 0); err != nil {
		t.Fatal(err)
	}
	feeds, err := s.Feeds(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	var enabledID, disabledID int64
	for _, feed := range feeds {
		switch feed.Name {
		case "enabled":
			enabledID = feed.ID
		case "disabled":
			disabledID = feed.ID
		}
	}
	if _, err := s.DB.Exec(`INSERT INTO catalog_entries(feed_id, category, service, cidr) VALUES
		(?, 'Visible', 'Keep', '8.8.8.0/24'),
		(?, 'Visible', 'Remove', '8.8.4.0/24'),
		(?, 'Shared', 'Service', '9.9.9.0/24'),
		(?, 'HiddenCategory', 'Any', '1.1.1.0/24'),
		(?, 'HiddenServices', 'Hidden', '1.0.0.0/24'),
		(?, 'Shared', 'Service', '2.2.2.0/24')`,
		enabledID, enabledID, enabledID, disabledID, disabledID, disabledID); err != nil {
		t.Fatal(err)
	}
	userID, err := s.AddUser(ctx, User{
		Name: "client", PeerIP: "172.16.0.2", PeerASN: 65001, Enabled: true,
		Networks: []string{"192.168.20.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		return SetUserSelection(ctx, tx, userID,
			[]string{"HiddenCategory", "Shared"},
			[]ServiceKey{
				{Category: "HiddenServices", Service: "Hidden"},
				{Category: "Visible", Service: "Remove"},
			})
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		return SetVisibleUserSelection(ctx, tx, userID, nil,
			[]ServiceKey{{Category: "Visible", Service: "Keep"}})
	}); err != nil {
		t.Fatal(err)
	}

	categories, services, err := s.UserSelection(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(categories) != 1 || !categories["HiddenCategory"] {
		t.Fatalf("categories = %#v, want disabled-only category", categories)
	}
	wantServices := map[ServiceKey]bool{
		{Category: "HiddenServices", Service: "Hidden"}: true,
		{Category: "Visible", Service: "Keep"}:          true,
	}
	if len(services) != len(wantServices) {
		t.Fatalf("services = %#v, want %#v", services, wantServices)
	}
	for service := range wantServices {
		if !services[service] {
			t.Fatalf("service %v was not preserved/saved: %#v", service, services)
		}
	}
}

func TestCountSelectionPrefixes(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	// Get a feed ID for mode 1 (opencck, already enabled)
	var feedID int64
	if err := s.DB.QueryRow(	"SELECT f.id FROM feeds f JOIN catalog_mode_feeds cmf ON cmf.feed_id = f.id WHERE cmf.mode_id = 1 ORDER BY f.id LIMIT 1").Scan(&feedID); err != nil {
		t.Fatal(err)
	}

	// Insert catalog entries with both IPv4 and IPv6 prefixes
	if _, err := s.DB.Exec(`INSERT INTO catalog_entries(feed_id, category, service, cidr) VALUES
		(?, 'CountTest', 'ServiceA', '8.8.8.0/24'),
		(?, 'CountTest', 'ServiceA', '2a01::/32'),
		(?, 'CountTest', 'ServiceB', '37.228.0.0/24')`,
		feedID, feedID, feedID); err != nil {
		t.Fatal(err)
	}

	// Add a user with mode 1
	userID, err := s.AddUser(ctx, User{
		Name: "client", PeerIP: "172.16.0.2", PeerASN: 65001, Enabled: true,
		CatalogModeID: 1,
		Networks:      []string{"192.168.20.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Select the CountTest category
	err = s.Transaction(ctx, func(tx *sql.Tx) error {
		return SetUserSelection(ctx, tx, userID, []string{"CountTest"}, nil)
	})
	if err != nil {
		t.Fatal(err)
	}

	// Count all prefixes (both IPv4 and IPv6)
	v4, v6, err := s.CountSelectionPrefixes(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if v4 != 2 {
		t.Fatalf("CountSelectionPrefixes v4 = %d, want 2", v4)
	}
	if v6 != 1 {
		t.Fatalf("CountSelectionPrefixes v6 = %d, want 1", v6)
	}
}
