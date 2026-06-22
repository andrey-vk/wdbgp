package migrations

import (
	"context"
	"database/sql"
)

func V020(ctx context.Context, tx *sql.Tx) error {
	// --- feed_adapters columns ---
	rows, err := tx.Query("SELECT name FROM pragma_table_info('feed_adapters')")
	if err != nil {
		return err
	}
	hasBuiltinVersion := false
	hasIsCustomized := false
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		switch name {
		case "builtin_version":
			hasBuiltinVersion = true
		case "is_customized":
			hasIsCustomized = true
		}
	}
	rows.Close()
	if !hasBuiltinVersion {
		if _, err := tx.Exec("ALTER TABLE feed_adapters ADD COLUMN builtin_version INTEGER NOT NULL DEFAULT 0"); err != nil {
			return err
		}
	}
	if !hasIsCustomized {
		if _, err := tx.Exec("ALTER TABLE feed_adapters ADD COLUMN is_customized INTEGER NOT NULL DEFAULT 0"); err != nil {
			return err
		}
	}

	// --- catalog_mode_feeds junction table ---
	var hasCMF int
	if err := tx.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='catalog_mode_feeds'").Scan(&hasCMF); err != nil {
		return err
	}
	if hasCMF == 0 {
		if _, err := tx.Exec(`CREATE TABLE catalog_mode_feeds (
			mode_id INTEGER NOT NULL REFERENCES catalog_modes(id) ON DELETE CASCADE,
			feed_id INTEGER NOT NULL REFERENCES feeds(id) ON DELETE CASCADE,
			PRIMARY KEY (mode_id, feed_id)
		)`); err != nil {
			return err
		}
		if _, err := tx.Exec("CREATE INDEX IF NOT EXISTS idx_catalog_mode_feeds_feed ON catalog_mode_feeds(feed_id)"); err != nil {
			return err
		}
		// Migrate existing feed→mode assignments (only when mode_id still exists)
		var hasModeID int
		if err := tx.QueryRow("SELECT COUNT(*) FROM pragma_table_info('feeds') WHERE name='mode_id'").Scan(&hasModeID); err != nil {
			return err
		}
		if hasModeID > 0 {
			if _, err := tx.Exec(`INSERT INTO catalog_mode_feeds (mode_id, feed_id)
				SELECT mode_id, id FROM feeds WHERE mode_id IS NOT NULL`); err != nil {
				return err
			}
		}
	}

	// --- Drop legacy feeds.mode_id column ---
	var hasModeID int
	if err := tx.QueryRow("SELECT COUNT(*) FROM pragma_table_info('feeds') WHERE name='mode_id'").Scan(&hasModeID); err != nil {
		return err
	}
	if hasModeID > 0 {
		if _, err := tx.Exec("DROP INDEX IF EXISTS idx_feeds_mode"); err != nil {
			return err
		}
		if _, err := tx.Exec("DROP INDEX IF EXISTS idx_feeds_enabled_mode"); err != nil {
			return err
		}
		if _, err := tx.Exec("ALTER TABLE feeds DROP COLUMN mode_id"); err != nil {
			return err
		}
	}
	return nil
}

// V020NoTxSQL removes peer_ip UNIQUE constraint (issue #17).
// Must run outside migration transaction because SQLite doesn't support
// ALTER TABLE DROP CONSTRAINT inside a transaction in some configurations.
// Uses PRAGMA foreign_keys = OFF to prevent cascading deletes during rebuild.
func V020NoTxSQL(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
PRAGMA foreign_keys = OFF;
CREATE TABLE IF NOT EXISTS users_new (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    peer_ip TEXT NOT NULL,
    peer_asn INTEGER NOT NULL,
    next_hop TEXT,
    bgp_password TEXT,
    selection_locked INTEGER NOT NULL DEFAULT 0,
    enabled INTEGER NOT NULL DEFAULT 1,
    filter_override_enabled INTEGER NOT NULL DEFAULT 0,
    filter_editable INTEGER NOT NULL DEFAULT 0,
    filter_mode TEXT NOT NULL DEFAULT 'global'
        CHECK (filter_mode IN ('global', 'extend', 'override')),
    catalog_mode_id INTEGER REFERENCES catalog_modes(id),
    catalog_mode_editable INTEGER NOT NULL DEFAULT 0,
    web_auth TEXT NOT NULL DEFAULT 'network',
    UNIQUE(peer_ip, peer_asn)
);
-- Only insert if users_new is empty (idempotent: safe for retry after partial run)
INSERT INTO users_new SELECT id, name, peer_ip, peer_asn, next_hop, bgp_password,
    selection_locked, enabled, filter_override_enabled, filter_editable,
    COALESCE(filter_mode, 'global'), catalog_mode_id, catalog_mode_editable,
    COALESCE(web_auth, 'network') FROM users
WHERE NOT EXISTS (SELECT 1 FROM users_new LIMIT 1);
DROP TABLE IF EXISTS users;
ALTER TABLE users_new RENAME TO users;
PRAGMA foreign_keys = ON;
`)
	return err
}
