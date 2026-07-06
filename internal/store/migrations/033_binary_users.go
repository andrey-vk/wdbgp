package migrations

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/netip"
)

// V033 moves the user tables to binary IP storage and integer enums:
//
//   - users: peer_ip and next_hop become 4/16-byte address BLOBs;
//     filter_mode and web_auth become INTEGER enums; the legacy
//     filter_override_enabled flag is dropped (it always mirrored
//     filter_mode — normalizeFilterMode folded them together at read
//     time, so the fold happens once here instead, during the copy).
//   - user_networks: cidr TEXT becomes (ip BLOB, bits), WITHOUT ROWID.
//   - user_route_filters: action TEXT becomes 0=allow/1=deny and cidr
//     becomes (ip BLOB, bits), WITHOUT ROWID.
//
// Enum values (mirrored in internal/store/users.go):
// filter_mode 0=global 1=extend 2=override; web_auth 0=network 1=login
// 2=both 3=any.
//
// IP text can't be converted to BLOB in SQL, so the copies run as Go
// loops — all three tables are small (user-scale, not catalog-scale).
// Runs in NoTxSQL for the same reason as migrations 20/31/32: the
// rebuilds need PRAGMA foreign_keys=OFF.
func V033(ctx context.Context, tx *sql.Tx) error {
	return nil
}

// V033NoTxSQL performs the rebuilds. Idempotent via the same per-table
// state machine as migration 32: old column present → rebuild; table
// missing → finish the interrupted DROP/RENAME (or create fresh if the
// _new table is gone too); otherwise done. The Go copy loops only run
// when the _new table is empty, so a retry after a crash between copy
// and DROP doesn't duplicate rows.
func V033NoTxSQL(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		return err
	}

	if err := v033RebuildUsers(ctx, db); err != nil {
		return err
	}
	if err := v033RebuildUserNetworks(ctx, db); err != nil {
		return err
	}
	if err := v033RebuildUserRouteFilters(ctx, db); err != nil {
		return err
	}

	_, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON")
	return err
}

const v033UsersShape = `(
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    peer_ip BLOB NOT NULL,
    peer_asn INTEGER NOT NULL,
    next_hop BLOB,
    bgp_password TEXT,
    selection_locked INTEGER NOT NULL DEFAULT 0,
    enabled INTEGER NOT NULL DEFAULT 1,
    filter_editable INTEGER NOT NULL DEFAULT 0,
    filter_mode INTEGER NOT NULL DEFAULT 0 CHECK (filter_mode IN (0, 1, 2)),
    catalog_mode_id INTEGER REFERENCES catalog_modes(id),
    catalog_mode_editable INTEGER NOT NULL DEFAULT 0,
    web_auth INTEGER NOT NULL DEFAULT 0 CHECK (web_auth IN (0, 1, 2, 3)),
    active_dial INTEGER NOT NULL DEFAULT 0,
    UNIQUE(peer_ip, peer_asn)
)`

const v033UsersIndexes = `
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_ip_asn ON users(peer_ip, peer_asn);
CREATE INDEX IF NOT EXISTS idx_users_enabled_catalog_mode ON users(enabled, catalog_mode_id);
`

func v033RebuildUsers(ctx context.Context, db *sql.DB) error {
	state, err := v033TableState(ctx, db, "users", "filter_override_enabled", v033UsersShape, v033UsersIndexes)
	if err != nil || state != v033NeedsCopy {
		return err
	}

	empty, err := v033TableEmpty(ctx, db, "users_new")
	if err != nil {
		return err
	}
	if empty {
		rows, err := db.QueryContext(ctx, `
SELECT id, name, peer_ip, peer_asn, COALESCE(next_hop, ''), bgp_password,
       selection_locked, enabled, filter_editable,
       COALESCE(filter_mode, ''), filter_override_enabled,
       catalog_mode_id, catalog_mode_editable, COALESCE(web_auth, 'network'),
       active_dial
FROM users`)
		if err != nil {
			return err
		}
		type userRow struct {
			id                                              int64
			catalogModeID                                   sql.NullInt64
			peerASN                                         int64
			name, peerIP, nextHop, filterMode, webAuth      string
			bgpPassword                                     sql.NullString
			selectionLocked, enabled, filterEditable        bool
			filterOverride, catalogModeEditable, activeDial bool
		}
		var users []userRow
		for rows.Next() {
			var u userRow
			if err := rows.Scan(
				&u.id, &u.name, &u.peerIP, &u.peerASN, &u.nextHop, &u.bgpPassword,
				&u.selectionLocked, &u.enabled, &u.filterEditable,
				&u.filterMode, &u.filterOverride,
				&u.catalogModeID, &u.catalogModeEditable, &u.webAuth,
				&u.activeDial,
			); err != nil {
				closeRowsLogged(rows)
				return err
			}
			users = append(users, u)
		}
		if err := rows.Err(); err != nil {
			closeRowsLogged(rows)
			return err
		}
		closeRowsLogged(rows)

		for _, u := range users {
			peerIP, err := v033EncodeAddr(u.peerIP)
			if err != nil {
				return fmt.Errorf("user %d (%s): peer_ip: %w", u.id, u.name, err)
			}
			var nextHop any
			if u.nextHop != "" {
				encoded, err := v033EncodeAddr(u.nextHop)
				if err != nil {
					return fmt.Errorf("user %d (%s): next_hop: %w", u.id, u.name, err)
				}
				nextHop = encoded
			}
			if _, err := db.ExecContext(ctx, `
INSERT INTO users_new (id, name, peer_ip, peer_asn, next_hop, bgp_password,
    selection_locked, enabled, filter_editable, filter_mode,
    catalog_mode_id, catalog_mode_editable, web_auth, active_dial)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				u.id, u.name, peerIP, u.peerASN, nextHop, u.bgpPassword,
				u.selectionLocked, u.enabled, u.filterEditable,
				v033FilterModeInt(u.filterMode, u.filterOverride),
				u.catalogModeID, u.catalogModeEditable,
				v033WebAuthInt(u.webAuth), u.activeDial,
			); err != nil {
				return fmt.Errorf("copy user %d (%s): %w", u.id, u.name, err)
			}
		}
	}

	if _, err := db.ExecContext(ctx,
		"DROP TABLE IF EXISTS users; ALTER TABLE users_new RENAME TO users;"+v033UsersIndexes); err != nil {
		return fmt.Errorf("finish users rebuild: %w", err)
	}
	return nil
}

const v033UserNetworksShape = `(
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    ip BLOB NOT NULL,
    bits INTEGER NOT NULL,
    PRIMARY KEY (user_id, ip, bits),
    UNIQUE(ip, bits)
) WITHOUT ROWID`

func v033RebuildUserNetworks(ctx context.Context, db *sql.DB) error {
	state, err := v033TableState(ctx, db, "user_networks", "cidr", v033UserNetworksShape, "")
	if err != nil || state != v033NeedsCopy {
		return err
	}
	empty, err := v033TableEmpty(ctx, db, "user_networks_new")
	if err != nil {
		return err
	}
	if empty {
		type networkRow struct {
			userID int64
			cidr   string
		}
		rows, err := db.QueryContext(ctx, "SELECT user_id, cidr FROM user_networks")
		if err != nil {
			return err
		}
		var networks []networkRow
		for rows.Next() {
			var n networkRow
			if err := rows.Scan(&n.userID, &n.cidr); err != nil {
				closeRowsLogged(rows)
				return err
			}
			networks = append(networks, n)
		}
		if err := rows.Err(); err != nil {
			closeRowsLogged(rows)
			return err
		}
		closeRowsLogged(rows)

		for _, n := range networks {
			ip, bits, err := v033EncodePrefix(n.cidr)
			if err != nil {
				return fmt.Errorf("user %d network %q: %w", n.userID, n.cidr, err)
			}
			if _, err := db.ExecContext(ctx,
				"INSERT OR IGNORE INTO user_networks_new (user_id, ip, bits) VALUES (?, ?, ?)",
				n.userID, ip, bits); err != nil {
				return fmt.Errorf("copy network %q: %w", n.cidr, err)
			}
		}
	}
	if _, err := db.ExecContext(ctx,
		"DROP TABLE IF EXISTS user_networks; ALTER TABLE user_networks_new RENAME TO user_networks;"); err != nil {
		return fmt.Errorf("finish user_networks rebuild: %w", err)
	}
	return nil
}

const v033UserRouteFiltersShape = `(
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    action INTEGER NOT NULL CHECK (action IN (0, 1)),
    ip BLOB NOT NULL,
    bits INTEGER NOT NULL,
    PRIMARY KEY (user_id, action, ip, bits)
) WITHOUT ROWID`

func v033RebuildUserRouteFilters(ctx context.Context, db *sql.DB) error {
	state, err := v033TableState(ctx, db, "user_route_filters", "cidr", v033UserRouteFiltersShape, "")
	if err != nil || state != v033NeedsCopy {
		return err
	}
	empty, err := v033TableEmpty(ctx, db, "user_route_filters_new")
	if err != nil {
		return err
	}
	if empty {
		type filterRow struct {
			userID int64
			action string
			cidr   string
		}
		rows, err := db.QueryContext(ctx, "SELECT user_id, action, cidr FROM user_route_filters")
		if err != nil {
			return err
		}
		var filters []filterRow
		for rows.Next() {
			var f filterRow
			if err := rows.Scan(&f.userID, &f.action, &f.cidr); err != nil {
				closeRowsLogged(rows)
				return err
			}
			filters = append(filters, f)
		}
		if err := rows.Err(); err != nil {
			closeRowsLogged(rows)
			return err
		}
		closeRowsLogged(rows)

		for _, f := range filters {
			ip, bits, err := v033EncodePrefix(f.cidr)
			if err != nil {
				return fmt.Errorf("user %d route filter %q: %w", f.userID, f.cidr, err)
			}
			action := 0
			if f.action == "deny" {
				action = 1
			}
			if _, err := db.ExecContext(ctx,
				"INSERT OR IGNORE INTO user_route_filters_new (user_id, action, ip, bits) VALUES (?, ?, ?, ?)",
				f.userID, action, ip, bits); err != nil {
				return fmt.Errorf("copy route filter %q: %w", f.cidr, err)
			}
		}
	}
	if _, err := db.ExecContext(ctx,
		"DROP TABLE IF EXISTS user_route_filters; ALTER TABLE user_route_filters_new RENAME TO user_route_filters;"); err != nil {
		return fmt.Errorf("finish user_route_filters rebuild: %w", err)
	}
	return nil
}

type v033State int

const (
	v033Done v033State = iota
	v033NeedsCopy
)

// v033TableState resolves the per-table resume point. It returns
// v033NeedsCopy when the caller must run its copy loop and finish the
// DROP/RENAME; v033Done means the table is already converted (finishing
// any interrupted rename or fresh creation itself, as a side effect).
func v033TableState(ctx context.Context, db *sql.DB, table, oldColumn, shape, postRename string) (v033State, error) {
	exists, err := dbTableExists(ctx, db, table)
	if err != nil {
		return v033Done, err
	}
	if !exists {
		newExists, err := dbTableExists(ctx, db, table+"_new")
		if err != nil {
			return v033Done, err
		}
		var sql string
		if newExists {
			sql = fmt.Sprintf("ALTER TABLE %s_new RENAME TO %s;", table, table) + postRename
		} else {
			sql = fmt.Sprintf("CREATE TABLE %s %s;", table, shape) + postRename
		}
		if _, err := db.ExecContext(ctx, sql); err != nil {
			return v033Done, fmt.Errorf("finish %s: %w", table, err)
		}
		return v033Done, nil
	}
	hasOld, err := dbTableHasColumn(ctx, db, table, oldColumn)
	if err != nil {
		return v033Done, err
	}
	if !hasOld {
		return v033Done, nil
	}
	if _, err := db.ExecContext(ctx,
		fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s_new %s", table, shape)); err != nil {
		return v033Done, fmt.Errorf("create %s_new: %w", table, err)
	}
	return v033NeedsCopy, nil
}

func v033TableEmpty(ctx context.Context, db *sql.DB, table string) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", table)).Scan(&n) //nolint:gosec // table name is a compile-time constant
	return n == 0, err
}

// v033FilterModeInt folds the old (filter_mode TEXT, filter_override_enabled)
// pair into the integer enum, replicating store.normalizeFilterMode: a valid
// non-global mode string wins; otherwise the legacy override flag decides.
func v033FilterModeInt(mode string, override bool) int {
	switch mode {
	case "extend":
		return 1
	case "override":
		return 2
	}
	if override {
		return 2
	}
	return 0
}

func v033WebAuthInt(mode string) int {
	switch mode {
	case "login":
		return 1
	case "both":
		return 2
	case "any":
		return 3
	default: // "network" and anything unexpected
		return 0
	}
}

func v033EncodeAddr(value string) ([]byte, error) {
	addr, err := netip.ParseAddr(value)
	if err != nil {
		return nil, err
	}
	return addr.AsSlice(), nil
}

func v033EncodePrefix(value string) ([]byte, int, error) {
	prefix, err := netip.ParsePrefix(value)
	if err != nil {
		return nil, 0, err
	}
	masked := prefix.Masked()
	return masked.Addr().AsSlice(), masked.Bits(), nil
}

func closeRowsLogged(rows *sql.Rows) {
	if err := rows.Close(); err != nil {
		log.Printf("WARNING: rows close: %v", err)
	}
}
