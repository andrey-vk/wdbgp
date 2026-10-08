package migrations

import (
	"context"
	"database/sql"
)

// V041 adds persistent undelivered-recovery tracking to
// user_prefix_alert_state, mirroring the drop_delivered column migration 040
// already added for drops — a recovery alert was previously best-effort,
// single-attempt: if its one delivery failed, it was lost for good, unlike a
// drop, which keeps retrying every check until it actually gets through.
//
// Guarded by existingColumns, like every other ALTER TABLE ADD COLUMN
// migration in this package: a migration can be re-run after its
// schema_migrations row was removed (e.g. simulating a resumed upgrade, see
// backup_test.go), and SQLite has no ADD COLUMN IF NOT EXISTS.
func V041(ctx context.Context, tx *sql.Tx) error {
	cols, err := existingColumns(tx, "user_prefix_alert_state")
	if err != nil {
		return err
	}
	for _, stmt := range []struct {
		column string
		ddl    string
	}{
		{"recovery_pending", "ALTER TABLE user_prefix_alert_state ADD COLUMN recovery_pending INTEGER NOT NULL DEFAULT 0"},
		{"recovery_detected_at", "ALTER TABLE user_prefix_alert_state ADD COLUMN recovery_detected_at INTEGER"},
		{"recovery_baseline_v4", "ALTER TABLE user_prefix_alert_state ADD COLUMN recovery_baseline_v4 INTEGER"},
		{"recovery_baseline_v6", "ALTER TABLE user_prefix_alert_state ADD COLUMN recovery_baseline_v6 INTEGER"},
		{"recovery_duration_seconds", "ALTER TABLE user_prefix_alert_state ADD COLUMN recovery_duration_seconds INTEGER"},
	} {
		if cols[stmt.column] {
			continue
		}
		if _, err := tx.ExecContext(ctx, stmt.ddl); err != nil {
			return err
		}
	}
	return nil
}
