package migrations

import (
	"context"
	"database/sql"
	"time"
)

// V039NoTxSQL moves feed_sync_changes.feed_id from ON DELETE CASCADE to SET
// NULL, so deleting a feed keeps the history of its syncs for the users it
// reached. Changing a foreign key needs a table rebuild, which has to run with
// foreign keys off, outside the migration transaction (as in migration 031).
//
// It works from the state the tables are in, so an interrupted run resumes
// cleanly: a staging table left behind is discarded and rebuilt from the
// original, which is only dropped once the copy is complete, and a rebuild
// interrupted after the drop is finished by the rename alone. Everything runs on
// one connection, so foreign keys are restored on the connection that turned
// them off.
func V039NoTxSQL(ctx context.Context, db *sql.DB) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }() //nolint:errcheck

	hasOriginal, err := tableExistsOn(ctx, conn, "feed_sync_changes")
	if err != nil {
		return err
	}
	hasStaging, err := tableExistsOn(ctx, conn, "feed_sync_changes_new")
	if err != nil {
		return err
	}
	switch {
	case hasOriginal:
		cascades, err := feedCascadeCountOn(ctx, conn)
		if err != nil {
			return err
		}
		if cascades > 0 {
			if err := rebuildFeedSyncChangesOn(ctx, conn); err != nil {
				return err
			}
		}
	case hasStaging:
		// The original was dropped but the rename didn't run.
		if _, err := conn.ExecContext(ctx, `ALTER TABLE feed_sync_changes_new RENAME TO feed_sync_changes`); err != nil {
			return err
		}
	default:
		return nil
	}
	_, err = conn.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_feed_sync_changes_feed ON feed_sync_changes(feed_id, synced_at)`)
	return err
}

func tableExistsOn(ctx context.Context, conn *sql.Conn, name string) (bool, error) {
	var n int
	if err := conn.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", name).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

func feedCascadeCountOn(ctx context.Context, conn *sql.Conn) (int, error) {
	var n int
	err := conn.QueryRowContext(ctx, `
SELECT COUNT(*) FROM pragma_foreign_key_list('feed_sync_changes')
WHERE "table" = 'feeds' AND on_delete = 'CASCADE'`).Scan(&n)
	return n, err
}

// rebuildFeedSyncChangesOn copies feed_sync_changes into a table with the new
// foreign key and renames it into place. Foreign keys are turned back on even
// when a step fails, since the connection outlives this call.
func rebuildFeedSyncChangesOn(ctx context.Context, conn *sql.Conn) error {
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return err
	}
	defer func() { _, _ = conn.ExecContext(ctx, `PRAGMA foreign_keys = ON`) }() //nolint:errcheck
	stmts := []string{
		`DROP TABLE IF EXISTS feed_sync_changes_new`,
		`CREATE TABLE feed_sync_changes_new (
			id                   INTEGER PRIMARY KEY AUTOINCREMENT,
			feed_id              INTEGER REFERENCES feeds(id) ON DELETE SET NULL,
			synced_at            INTEGER NOT NULL,
			added_services       INTEGER NOT NULL,
			removed_services     INTEGER NOT NULL,
			added_prefixes       INTEGER NOT NULL,
			removed_prefixes     INTEGER NOT NULL,
			added_associations   INTEGER NOT NULL,
			removed_associations INTEGER NOT NULL,
			feed_name            TEXT NOT NULL DEFAULT ''
		)`,
		`INSERT INTO feed_sync_changes_new (id, feed_id, synced_at, added_services, removed_services, added_prefixes,
			removed_prefixes, added_associations, removed_associations, feed_name)
		SELECT id, feed_id, synced_at, added_services, removed_services, added_prefixes, removed_prefixes,
			added_associations, removed_associations,
			COALESCE((SELECT name FROM feeds WHERE feeds.id = feed_sync_changes.feed_id), '')
		FROM feed_sync_changes`,
		`DROP TABLE feed_sync_changes`,
		`ALTER TABLE feed_sync_changes_new RENAME TO feed_sync_changes`,
	}
	for _, stmt := range stmts {
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func addColumnIfMissing(ctx context.Context, tx *sql.Tx, table, name, def string) error {
	_, err := addColumnIfMissingReport(ctx, tx, table, name, def)
	return err
}

// addColumnIfMissingReport adds the column and reports whether it did.
func addColumnIfMissingReport(ctx context.Context, tx *sql.Tx, table, name, def string) (bool, error) {
	var n int
	if err := tx.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?", table, name).Scan(&n); err != nil {
		return false, err
	}
	if n > 0 {
		return false, nil
	}
	_, err := tx.ExecContext(ctx, "ALTER TABLE "+table+" ADD COLUMN "+name+" "+def)
	return err == nil, err
}

// V039 records what each feed sync changed, by name, for the user change log:
// the services it added or removed, and the enabled modes that included the feed
// at the time. Recording the modes then, rather than reading the current
// assignments, keeps later mode edits from rewriting which users a sync reached.
// Rows follow their feed_sync_changes row and are pruned with it.
func V039(ctx context.Context, tx *sql.Tx) error {
	// ADD COLUMN isn't idempotent, and the last migration can be re-run (see the
	// backup tests), so each column is added only when it's missing.
	for _, col := range []struct{ table, name, def string }{
		{"feed_sync_changes", "feed_name", "TEXT NOT NULL DEFAULT ''"},
		// The audit row ID the sync came after; see syncPoint in user_change_log.go.
		{"feed_sync_changes", "audit_seq", "INTEGER"},
	} {
		if err := addColumnIfMissing(ctx, tx, col.table, col.name, col.def); err != nil {
			return err
		}
	}
	// Existing users' history starts at the newest audit row, so nothing from
	// before the upgrade is read for them. Deletions before this weren't audited,
	// so those rows can't be tied to the account that owns the ID today.
	added, err := addColumnIfMissingReport(ctx, tx, "users", "history_from", "INTEGER NOT NULL DEFAULT 0")
	if err != nil {
		return err
	}
	if added {
		if _, err := tx.ExecContext(ctx,
			"UPDATE users SET history_from = (SELECT COALESCE((SELECT seq FROM sqlite_sequence WHERE name = 'audit_log'), 0))"); err != nil {
			return err
		}
	}
	// The audit log is complete from this point on. Purges move the boundary
	// forward, so raising retention later can't widen the change log's window
	// back over rows that were already deleted. Audit rows older than this
	// migration may be missing changes, so history before it isn't placed.
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS audit_log_coverage (
		id             INTEGER PRIMARY KEY CHECK (id = 1),
		complete_since INTEGER NOT NULL
	)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT OR IGNORE INTO audit_log_coverage(id, complete_since) VALUES (1, ?)", time.Now().Unix()); err != nil {
		return err
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS feed_sync_change_services (
			change_id INTEGER NOT NULL REFERENCES feed_sync_changes(id) ON DELETE CASCADE,
			kind      TEXT NOT NULL CHECK (kind IN ('added', 'removed')),
			category  TEXT NOT NULL,
			service   TEXT NOT NULL,
			PRIMARY KEY (change_id, kind, category, service)
		)`,
		`CREATE TABLE IF NOT EXISTS feed_sync_change_modes (
			change_id INTEGER NOT NULL REFERENCES feed_sync_changes(id) ON DELETE CASCADE,
			mode_id   INTEGER NOT NULL,
			mode_name TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (change_id, mode_id)
		)`,
	}
	// The history scan filters by category, mode, and sync time, so these keep
	// it to the matching rows instead of scanning every service row.
	stmts = append(stmts,
		`CREATE INDEX IF NOT EXISTS idx_feed_sync_change_services_category ON feed_sync_change_services(category, change_id)`,
		`CREATE INDEX IF NOT EXISTS idx_feed_sync_change_modes_mode ON feed_sync_change_modes(mode_id, change_id)`,
		`CREATE INDEX IF NOT EXISTS idx_feed_sync_changes_synced ON feed_sync_changes(synced_at)`,
	)
	for _, stmt := range stmts {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}
