package migrations

import (
	"context"
	"database/sql"
	"fmt"
)

// V034 finishes the numeric-schema cleanup on the remaining tables:
//
//   - catalog_modes.key is dropped — same rationale as feed_adapters.key
//     in migration 31: modes are identified by id, and name is already
//     NOT NULL UNIQUE, so the slug column was pure duplication.
//   - feeds.last_success moves from RFC3339 TEXT to Unix epoch INTEGER
//     (the same conversion snapshots got earlier, for the same reason:
//     no time.Parse on read, smaller storage).
//   - app_settings.updated_at moves from datetime TEXT to epoch INTEGER.
//
// All three are SQL-only rebuilds (strftime('%s', ...) converts the
// timestamp text). Runs in NoTxSQL because feeds and catalog_modes are
// FK targets, so their DROP/RENAME needs PRAGMA foreign_keys=OFF — a
// no-op inside a transaction (same as migrations 20/31/32/33).
func V034(ctx context.Context, tx *sql.Tx) error {
	return nil
}

// V034NoTxSQL performs the rebuilds, idempotent via the migration 32/33
// per-table state machine.
func V034NoTxSQL(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		return err
	}

	if err := v034Rebuild(ctx, db, "catalog_modes", "key", `(
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    enabled INTEGER NOT NULL DEFAULT 1
)`, `
INSERT INTO catalog_modes_new (id, name, enabled)
SELECT id, name, enabled FROM catalog_modes
WHERE NOT EXISTS (SELECT 1 FROM catalog_modes_new LIMIT 1);
`, ""); err != nil {
		return err
	}

	// Detector: last_success declared TEXT means not yet converted. The
	// column survives the rebuild under the same name, so column presence
	// can't be the signal here — the declared type is.
	feedsOld, err := v034ColumnHasType(ctx, db, "feeds", "last_success", "TEXT")
	if err != nil {
		return err
	}
	if feedsOld {
		if _, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS feeds_new (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    url TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    last_success INTEGER,
    last_error TEXT,
    adapter_id INTEGER REFERENCES feed_adapters(id),
    sync_interval INTEGER NOT NULL DEFAULT 0,
    data TEXT NOT NULL DEFAULT '',
    allowed_hosts TEXT NOT NULL DEFAULT '',
    restrict_hosts INTEGER NOT NULL DEFAULT 1
);
INSERT INTO feeds_new (id, name, url, enabled, last_success, last_error,
    adapter_id, sync_interval, data, allowed_hosts, restrict_hosts)
SELECT id, name, url, enabled,
    CAST(strftime('%s', last_success) AS INTEGER),
    last_error, adapter_id, sync_interval, data, allowed_hosts, restrict_hosts
FROM feeds
WHERE NOT EXISTS (SELECT 1 FROM feeds_new LIMIT 1);
DROP TABLE IF EXISTS feeds;
ALTER TABLE feeds_new RENAME TO feeds;
`+v034FeedsIndexes); err != nil {
			return fmt.Errorf("rebuild feeds: %w", err)
		}
	} else if err := v034FinishIfMissing(ctx, db, "feeds", `(
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    url TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    last_success INTEGER,
    last_error TEXT,
    adapter_id INTEGER REFERENCES feed_adapters(id),
    sync_interval INTEGER NOT NULL DEFAULT 0,
    data TEXT NOT NULL DEFAULT '',
    allowed_hosts TEXT NOT NULL DEFAULT '',
    restrict_hosts INTEGER NOT NULL DEFAULT 1
)`, v034FeedsIndexes); err != nil {
		return err
	}

	settingsOld, err := v034ColumnHasType(ctx, db, "app_settings", "updated_at", "TEXT")
	if err != nil {
		return err
	}
	if settingsOld {
		if _, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS app_settings_new (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at INTEGER NOT NULL DEFAULT (unixepoch())
);
INSERT INTO app_settings_new (key, value, updated_at)
SELECT key, value, COALESCE(CAST(strftime('%s', updated_at) AS INTEGER), unixepoch())
FROM app_settings
WHERE NOT EXISTS (SELECT 1 FROM app_settings_new LIMIT 1);
DROP TABLE IF EXISTS app_settings;
ALTER TABLE app_settings_new RENAME TO app_settings;
`); err != nil {
			return fmt.Errorf("rebuild app_settings: %w", err)
		}
	} else if err := v034FinishIfMissing(ctx, db, "app_settings", `(
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at INTEGER NOT NULL DEFAULT (unixepoch())
)`, ""); err != nil {
		return err
	}

	_, err = db.ExecContext(ctx, "PRAGMA foreign_keys = ON")
	return err
}

const v034FeedsIndexes = `
CREATE UNIQUE INDEX IF NOT EXISTS idx_feeds_url ON feeds(url);
CREATE INDEX IF NOT EXISTS idx_feeds_adapter ON feeds(adapter_id);
`

// v034Rebuild is the migration 32 state machine for column-presence
// detected tables.
func v034Rebuild(ctx context.Context, db *sql.DB, table, oldColumn, newShape, copySQL, postRename string) error {
	exists, err := dbTableExists(ctx, db, table)
	if err != nil {
		return err
	}
	if !exists {
		newExists, err := dbTableExists(ctx, db, table+"_new")
		if err != nil {
			return err
		}
		var sql string
		if newExists {
			sql = fmt.Sprintf("ALTER TABLE %s_new RENAME TO %s;", table, table) + postRename
		} else {
			sql = fmt.Sprintf("CREATE TABLE %s %s;", table, newShape) + postRename
		}
		if _, err := db.ExecContext(ctx, sql); err != nil {
			return fmt.Errorf("finish %s: %w", table, err)
		}
		return nil
	}
	hasOld, err := dbTableHasColumn(ctx, db, table, oldColumn)
	if err != nil {
		return err
	}
	if !hasOld {
		return nil
	}
	createSQL := fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s_new %s;", table, newShape)
	dropRename := fmt.Sprintf("DROP TABLE IF EXISTS %s; ALTER TABLE %s_new RENAME TO %s;", table, table, table)
	if _, err := db.ExecContext(ctx, createSQL+copySQL+dropRename+postRename); err != nil {
		return fmt.Errorf("rebuild %s: %w", table, err)
	}
	return nil
}

// v034FinishIfMissing handles the type-detected tables when they don't
// exist at all: either a previous run crashed between DROP and RENAME
// (finish the rename), or the table never existed on this database
// (create the converted shape fresh — some very old fixture DBs predate
// it entirely).
func v034FinishIfMissing(ctx context.Context, db *sql.DB, table, newShape, postRename string) error {
	exists, err := dbTableExists(ctx, db, table)
	if err != nil || exists {
		return err
	}
	newExists, err := dbTableExists(ctx, db, table+"_new")
	if err != nil {
		return err
	}
	var sql string
	if newExists {
		sql = fmt.Sprintf("ALTER TABLE %s_new RENAME TO %s;", table, table) + postRename
	} else {
		sql = fmt.Sprintf("CREATE TABLE %s %s;", table, newShape) + postRename
	}
	if _, err := db.ExecContext(ctx, sql); err != nil {
		return fmt.Errorf("finish %s: %w", table, err)
	}
	return nil
}

func v034ColumnHasType(ctx context.Context, db *sql.DB, table, column, declaredType string) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ? AND type = ?",
		table, column, declaredType).Scan(&n)
	return n > 0, err
}
