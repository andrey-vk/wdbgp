//nolint:errcheck // test file, errors in cleanup intentionally ignored
package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	m "github.com/andrey-vk/wdbgp/internal/store/migrations"
	_ "modernc.org/sqlite"
)

// buildVersion32DB creates a database migrated through version 32 (users
// still text-based) with representative user data, and returns its path.
func buildVersion32DB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "version-32.sqlite3")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	replayMigrationsBefore(t, db, 33)
	if _, err := db.Exec(`
INSERT INTO users(id, name, peer_ip, peer_asn, next_hop, bgp_password,
    enabled, filter_mode, filter_override_enabled, web_auth, catalog_mode_id)
VALUES
    (1, 'v4-user', '192.0.2.10', 65001, '192.0.2.1', 'secret', 1, 'extend', 1, 'both', 1),
    (2, 'v6-user', '2001:db8::7', 65002, NULL, NULL, 1, 'global', 0, 'login', 1),
    -- Legacy shape: filter_mode never written, only the override flag set.
    (3, 'legacy-override', '198.51.100.3', 65003, NULL, NULL, 0, 'global', 1, 'network', 1);
INSERT INTO user_networks(user_id, cidr) VALUES
    (1, '10.20.0.0/16'),
    (1, '2001:db8:1::/48');
INSERT INTO user_route_filters(user_id, action, cidr) VALUES
    (1, 'allow', '10.20.1.0/24'),
    (1, 'deny', '10.20.2.0/24');
`); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMigration33ConvertsUsersToBinaryAndEnums(t *testing.T) {
	path := buildVersion32DB(t)

	s, err := Open(path, false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	assertConverted := func() {
		t.Helper()
		u1, err := s.User(ctx, 1)
		if err != nil {
			t.Fatal(err)
		}
		if u1.PeerIP != "192.0.2.10" || u1.NextHop != "192.0.2.1" || u1.BGPPassword != "secret" {
			t.Fatalf("v4-user = %+v, addresses/password lost in conversion", u1)
		}
		if u1.FilterMode != FilterModeExtend || !u1.FilterOverride || u1.WebAuth != "both" {
			t.Fatalf("v4-user enums = mode %q override %v webauth %q, want extend/true/both",
				u1.FilterMode, u1.FilterOverride, u1.WebAuth)
		}
		if len(u1.Networks) != 2 || u1.Networks[0] != "10.20.0.0/16" || u1.Networks[1] != "2001:db8:1::/48" {
			t.Fatalf("v4-user networks = %v", u1.Networks)
		}
		filters, err := s.UserRouteFilters(ctx, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(filters.Allow) != 1 || filters.Allow[0] != "10.20.1.0/24" ||
			len(filters.Deny) != 1 || filters.Deny[0] != "10.20.2.0/24" {
			t.Fatalf("v4-user route filters = %+v", filters)
		}

		u2, err := s.User(ctx, 2)
		if err != nil {
			t.Fatal(err)
		}
		if u2.PeerIP != "2001:db8::7" || u2.NextHop != "" || u2.WebAuth != "login" ||
			u2.FilterMode != FilterModeGlobal {
			t.Fatalf("v6-user = %+v", u2)
		}

		// Old data where only filter_override_enabled was set must fold
		// into filter_mode=override, matching normalizeFilterMode.
		u3, err := s.User(ctx, 3)
		if err != nil {
			t.Fatal(err)
		}
		if u3.FilterMode != FilterModeOverride || !u3.FilterOverride {
			t.Fatalf("legacy-override user mode = %q override=%v, want override/true", u3.FilterMode, u3.FilterOverride)
		}

		var hasLegacyColumn int
		if err := s.DB.QueryRow(
			"SELECT COUNT(*) FROM pragma_table_info('users') WHERE name='filter_override_enabled'").
			Scan(&hasLegacyColumn); err != nil {
			t.Fatal(err)
		}
		if hasLegacyColumn != 0 {
			t.Fatal("filter_override_enabled column still present after migration 33")
		}
	}

	assertConverted()

	// Re-running the NoTxSQL body on an already-converted database (crash
	// after conversion, before schema_migrations was updated) must be a
	// clean no-op.
	if err := m.V033NoTxSQL(ctx, s.DB); err != nil {
		t.Fatalf("re-run on converted DB: %v", err)
	}
	assertConverted()

	// UserByIP must work against the binary networks.
	byIP, err := s.UserByIP(ctx, "10.20.5.5")
	if err != nil {
		t.Fatal(err)
	}
	if byIP.ID != 1 {
		t.Fatalf("UserByIP = user %d, want 1", byIP.ID)
	}
}
