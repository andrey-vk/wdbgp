package migrations

import (
	"context"
	"database/sql"
	"time"
)

// V039 records what each feed sync changed, by name, for the user change log:
// the services it added or removed, and the enabled modes that included the feed
// at the time. Recording the modes then, rather than reading the current
// assignments, keeps later mode edits from rewriting which users a sync reached.
// Rows follow their feed_sync_changes row and are pruned with it.
func V039(ctx context.Context, tx *sql.Tx) error {
	// ADD COLUMN isn't idempotent, and the last migration can be re-run (see the
	// backup tests), so add feed_name only when it's missing.
	var hasFeedName int
	if err := tx.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM pragma_table_info('feed_sync_changes') WHERE name = 'feed_name'").Scan(&hasFeedName); err != nil {
		return err
	}
	if hasFeedName == 0 {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE feed_sync_changes ADD COLUMN feed_name TEXT NOT NULL DEFAULT ''`); err != nil {
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
	for _, stmt := range stmts {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}
