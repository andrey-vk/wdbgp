package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/netip"
	"testing"

	_ "modernc.org/sqlite"
)

func TestDesiredPrefixesForCategoryAndService(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	userID, err := s.AddUser(ctx, User{
		Name: "client", PeerIP: "172.16.0.2", PeerASN: 65001, Enabled: true,
		Networks: []string{"192.168.20.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = s.InsertCatalogEntries(ctx, 1, []CatalogEntry{
		{Category: "Messengers", Service: "Telegram", CIDR: "149.154.160.0/20"},
		{Category: "Messengers", Service: "Signal", CIDR: "76.223.92.0/24"},
		{Category: "AI", Service: "Copilot", CIDR: "140.82.112.0/20"},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = s.Transaction(ctx, func(tx *sql.Tx) error {
		return SetUserSelection(ctx, tx, userID, []string{"Messengers"},
			[]ServiceKey{{Category: "AI", Service: "Copilot"}})
	})
	if err != nil {
		t.Fatal(err)
	}
	prefixes, _, err := s.DesiredPrefixes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(prefixes) != 3 {
		t.Fatalf("prefix count = %d, want 3: %#v", len(prefixes), prefixes)
	}
	for prefix, users := range prefixes {
		if len(users) != 1 || users[0] != userID {
			t.Fatalf("%s has unexpected users: %#v", prefix, users)
		}
	}
}

func TestDesiredPrefixesEmptyWithoutSelection(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.AddUser(ctx, User{
		Name: "client", PeerIP: "172.16.0.2", PeerASN: 65001, Enabled: true,
		Networks: []string{"192.168.20.0/24"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertCatalogEntries(ctx, 1, []CatalogEntry{
		{Category: "Messengers", Service: "Telegram", CIDR: "149.154.160.0/20"},
		{Category: "AI", Service: "Copilot", CIDR: "140.82.112.0/20"},
	}); err != nil {
		t.Fatal(err)
	}

	prefixes, _, err := s.DesiredPrefixes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(prefixes) != 0 {
		t.Fatalf("prefixes = %#v, want empty without user selection", prefixes)
	}
}

func TestDesiredPrefixesSubtractsGlobalDeny(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	userID := addFilteredTestUser(t, s, false)
	if _, err := s.DB.ExecContext(ctx,
		`INSERT OR REPLACE INTO app_settings(key, value, updated_at) VALUES ('filter_deny', '1.1.1.1/32', datetime('now'))`); err != nil {
		t.Fatal(err)
	}

	prefixes, _, err := s.DesiredPrefixes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(prefixes) != 24 {
		t.Fatalf("prefix count = %d, want 24", len(prefixes))
	}
	for rawPrefix, users := range prefixes {
		prefix := netip.MustParsePrefix(rawPrefix)
		if prefix.Contains(netip.MustParseAddr("1.1.1.1")) {
			t.Fatalf("denied address remains covered by %s", prefix)
		}
		if len(users) != 1 || users[0] != userID {
			t.Fatalf("%s has unexpected users: %v", rawPrefix, users)
		}
	}
}

func TestDesiredPrefixesUsesUserOverride(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	userID := addFilteredTestUser(t, s, true)
	if _, err := s.DB.ExecContext(ctx,
		`INSERT OR REPLACE INTO app_settings(key, value, updated_at) VALUES ('filter_deny', '1.1.1.1/32', datetime('now'))`); err != nil {
		t.Fatal(err)
	}
	if err := s.SetUserRouteFilters(ctx, userID, RouteFilters{Allow: []string{"1.1.0.0/16"}}); err != nil {
		t.Fatal(err)
	}

	prefixes, _, err := s.DesiredPrefixes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(prefixes) != 1 {
		t.Fatalf("prefixes = %v, want one user override prefix", prefixes)
	}
	if users := prefixes["1.1.0.0/16"]; len(users) != 1 || users[0] != userID {
		t.Fatalf("override prefix users = %v", users)
	}
}

func TestDesiredPrefixesExtendsGlobalFilters(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	userID := addFilteredTestUser(t, s, false)
	if err := s.SetUserFilterOverride(ctx, userID, false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetUserRouteFilterConfig(ctx, userID, FilterModeExtend,
		RouteFilters{Allow: []string{"1.1.0.0/16"}, Deny: []string{"1.1.1.1/32"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx,
		`INSERT OR REPLACE INTO app_settings(key, value, updated_at) VALUES ('filter_deny', '1.1.2.0/24', datetime('now'))`); err != nil {
		t.Fatal(err)
	}

	prefixes, _, err := s.DesiredPrefixes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for rawPrefix, users := range prefixes {
		prefix := netip.MustParsePrefix(rawPrefix)
		if !prefixContains(netip.MustParsePrefix("1.1.0.0/16"), prefix) {
			t.Fatalf("extended allow leaked prefix %s", rawPrefix)
		}
		if prefix.Contains(netip.MustParseAddr("1.1.1.1")) || prefix.Contains(netip.MustParseAddr("1.1.2.1")) {
			t.Fatalf("extended deny remains covered by %s", rawPrefix)
		}
		if len(users) != 1 || users[0] != userID {
			t.Fatalf("%s has unexpected users: %v", rawPrefix, users)
		}
	}
	if len(prefixes) == 0 {
		t.Fatal("extended filters produced no prefixes")
	}
}

func TestDesiredPrefixesDropsFeedDefaultRoute(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	userID, err := s.AddUser(ctx, User{
		Name: "default-route", PeerIP: "172.16.0.2", PeerASN: 65001, Enabled: true,
		Networks: []string{"192.168.20.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.InsertCatalogEntries(ctx, 1, []CatalogEntry{
		{Category: "test", Service: "default", CIDR: "0.0.0.0/0"},
		{Category: "test", Service: "public", CIDR: "8.8.8.0/24"},
	}); err != nil {
		t.Fatal(err)
	}
	// Global route filters are empty by default (migration 029 moved them to app_settings).
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		return SetUserSelection(ctx, tx, userID, []string{"test"}, nil)
	}); err != nil {
		t.Fatal(err)
	}

	prefixes, _, err := s.DesiredPrefixes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(prefixes) != 1 {
		t.Fatalf("prefixes = %v, want only the non-default route", prefixes)
	}
	if users := prefixes["8.8.8.0/24"]; len(users) != 1 || users[0] != userID {
		t.Fatalf("public prefix users = %v", users)
	}
}

func TestDesiredPrefixesClearsRoutesOnModeChange(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	// Step 1: Create a user in mode 1 (default) with selections
	userID := addFilteredTestUser(t, s, false)

	// Verify user is in mode 1 with selections → prefixes exist
	prefixes, _, err := s.DesiredPrefixes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(prefixes) == 0 {
		t.Fatal("expected prefixes in mode 1 with selections")
	}

	// Step 2: Change user to mode 2 (IPRanges is id 2, should already exist)
	_, err = s.DB.ExecContext(ctx, "UPDATE users SET catalog_mode_id = 2 WHERE id = ?", userID)
	if err != nil {
		t.Fatal(err)
	}

	// Step 3: DesiredPrefixes should return 0 for this user
	// (mode 2 has no selections for this user)
	prefixes, _, err = s.DesiredPrefixes(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Find prefixes for this specific user
	var userPrefixes int
	for _, users := range prefixes {
		for _, uid := range users {
			if uid == userID {
				userPrefixes++
			}
		}
	}

	if userPrefixes > 0 {
		t.Errorf("user %d still has %d prefixes after mode change, want 0", userID, userPrefixes)
	}
}

func addFilteredTestUser(t *testing.T, s *Store, override bool) int64 {
	t.Helper()
	ctx := context.Background()
	userID, err := s.AddUser(ctx, User{
		Name: "filtered", PeerIP: "172.16.0.2", PeerASN: 65001, Enabled: true,
		FilterOverride: override, Networks: []string{"192.168.20.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.InsertCatalogEntries(ctx, 1, []CatalogEntry{
		{Category: "test", Service: "wide", CIDR: "1.0.0.0/8"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		return SetUserSelection(ctx, tx, userID, []string{"test"}, nil)
	}); err != nil {
		t.Fatal(err)
	}
	return userID
}

func prefixContains(parent, child netip.Prefix) bool {
	return parent.Contains(child.Addr()) && child.Bits() >= parent.Bits()
}

func TestSplitNewlines(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "empty string",
			input: "",
			want:  nil,
		},
		{
			name:  "whitespace only",
			input: "  \n  \t  ",
			want:  nil,
		},
		{
			name:  "single line",
			input: "1.1.1.1/32",
			want:  []string{"1.1.1.1/32"},
		},
		{
			name:  "multiple lines",
			input: "1.1.1.1/32\n8.8.8.0/24",
			want:  []string{"1.1.1.1/32", "8.8.8.0/24"},
		},
		{
			name:  "trims whitespace",
			input: "  1.1.1.1/32  \n  8.8.8.0/24  ",
			want:  []string{"1.1.1.1/32", "8.8.8.0/24"},
		},
		{
			name:  "skips empty lines",
			input: "1.1.1.1/32\n\n8.8.8.0/24\n\n",
			want:  []string{"1.1.1.1/32", "8.8.8.0/24"},
		},
		{
			name:  "skips comment lines",
			input: "# this is a comment\n1.1.1.1/32\n# another comment\n8.8.8.0/24\n# trailing",
			want:  []string{"1.1.1.1/32", "8.8.8.0/24"},
		},
		{
			name:  "skips inline comments and trims",
			input: "  1.1.1.1/32  # inline comment\n  # just a comment  \n  8.8.8.0/24  ",
			want:  []string{"1.1.1.1/32  # inline comment", "8.8.8.0/24"},
		},
		{
			name:  "only comments",
			input: "# line 1\n# line 2",
			want:  nil,
		},
		{
			name:  "mixed blank and comments",
			input: "\n# comment\n\n1.1.1.1/32\n\n# another\n\n8.8.8.0/24\n\n",
			want:  []string{"1.1.1.1/32", "8.8.8.0/24"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := splitNewlines(tc.input)
			if tc.want == nil && got != nil {
				t.Fatalf("got %#v, want nil", got)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("len = %d, want %d: got %#v, want %#v", len(got), len(tc.want), got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestNormalizeRouteFiltersBareIP — a bare address is shorthand for a
// single-host prefix: it must normalize to /32 (or /128) and dedupe
// against an explicit x.x.x.x/32 entry for the same host.
func TestNormalizeRouteFiltersBareIP(t *testing.T) {
	got, err := NormalizeRouteFilters(RouteFilters{
		Allow: []string{"1.2.3.4", "1.2.3.4/32", "10.0.0.0/8"},
		Deny:  []string{"fd00::1", " 192.168.1.1 "},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantAllow := []string{"1.2.3.4/32", "10.0.0.0/8"}
	wantDeny := []string{"192.168.1.1/32", "fd00::1/128"}
	if len(got.Allow) != len(wantAllow) {
		t.Fatalf("Allow = %v, want %v", got.Allow, wantAllow)
	}
	for i := range wantAllow {
		if got.Allow[i] != wantAllow[i] {
			t.Errorf("Allow[%d] = %q, want %q", i, got.Allow[i], wantAllow[i])
		}
	}
	if len(got.Deny) != len(wantDeny) {
		t.Fatalf("Deny = %v, want %v", got.Deny, wantDeny)
	}
	for i := range wantDeny {
		if got.Deny[i] != wantDeny[i] {
			t.Errorf("Deny[%d] = %q, want %q", i, got.Deny[i], wantDeny[i])
		}
	}

	if _, err := NormalizeRouteFilters(RouteFilters{Allow: []string{"not-an-ip"}}); err == nil {
		t.Error("expected error for malformed entry, got none")
	}
}

// TestApplyRouteFiltersBareIP — global filter_allow/filter_deny reach
// parseRouteFilters as the raw text the user typed, so a bare address must
// work at apply time too, acting as the single-host prefix it abbreviates.
func TestApplyRouteFiltersBareIP(t *testing.T) {
	prefixes := []netip.Prefix{
		netip.MustParsePrefix("1.2.3.4/32"),
		netip.MustParsePrefix("8.8.8.0/24"),
	}
	got, err := applyRouteFiltersToPrefixes(prefixes, RouteFilters{Deny: []string{"1.2.3.4"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != netip.MustParsePrefix("8.8.8.0/24") {
		t.Errorf("filtered prefixes = %v, want [8.8.8.0/24]", got)
	}
}

// TestRouteFiltersJSONLowercaseKeys guards against RouteFilters marshaling
// as "Allow"/"Deny" — the user SPA reads userData.filters?.allow/?.deny
// (lowercase), so an untagged struct silently marshals to keys the frontend
// never matches and the filter editor always renders empty.
func TestRouteFiltersJSONLowercaseKeys(t *testing.T) {
	raw, err := json.Marshal(RouteFilters{Allow: []string{"10.0.0.0/8"}, Deny: []string{"192.168.0.0/16"}})
	if err != nil {
		t.Fatal(err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, ok := decoded["allow"]; !ok {
		t.Errorf("marshaled JSON = %s, want lowercase \"allow\" key", raw)
	}
	if _, ok := decoded["deny"]; !ok {
		t.Errorf("marshaled JSON = %s, want lowercase \"deny\" key", raw)
	}
	if _, ok := decoded["Allow"]; ok {
		t.Errorf("marshaled JSON = %s, should not have capitalized \"Allow\" key", raw)
	}
}
