package migrations

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// V035 replaces feed_snapshots.prefixes — a JSON TEXT map of
// feed_id→prefix count — with a proper child table:
//
//	feed_snapshot_counts(snapshot_id, feed_id, prefix_count) WITHOUT ROWID
//
// No JSON encode/decode on the metrics path, no text blob per snapshot,
// and history stays queryable with plain SQL. feed_id deliberately has no
// FK: the old JSON kept counts for since-deleted feeds, and the history
// keeps that property. Runs in NoTxSQL like the other rebuilds
// (migrations 20/31–34).
func V035(ctx context.Context, tx *sql.Tx) error {
	return nil
}

// V035NoTxSQL performs the conversion, idempotent via the usual per-table
// state machine. The JSON decode needs a Go loop; it runs before the
// parent rebuild (it reads the old prefixes column) and only when the
// counts table is still empty, so a crashed run never duplicates rows.
func V035NoTxSQL(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `
PRAGMA foreign_keys = OFF;
CREATE TABLE IF NOT EXISTS feed_snapshot_counts (
    snapshot_id  INTEGER NOT NULL REFERENCES feed_snapshots(id) ON DELETE CASCADE,
    feed_id      INTEGER NOT NULL,
    prefix_count INTEGER NOT NULL,
    PRIMARY KEY (snapshot_id, feed_id)
) WITHOUT ROWID;
`); err != nil {
		return fmt.Errorf("create feed_snapshot_counts: %w", err)
	}

	hasJSON, err := dbTableHasColumn(ctx, db, "feed_snapshots", "prefixes")
	if err != nil {
		return err
	}
	if hasJSON {
		if err := v035ConvertJSON(ctx, db); err != nil {
			return err
		}
		if _, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS feed_snapshots_new (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    recorded_at INTEGER NOT NULL
);
INSERT INTO feed_snapshots_new (id, recorded_at)
SELECT id, recorded_at FROM feed_snapshots
WHERE NOT EXISTS (SELECT 1 FROM feed_snapshots_new LIMIT 1);
DROP TABLE IF EXISTS feed_snapshots;
ALTER TABLE feed_snapshots_new RENAME TO feed_snapshots;
CREATE INDEX IF NOT EXISTS idx_feed_snapshots_time ON feed_snapshots(recorded_at);
`); err != nil {
			return fmt.Errorf("rebuild feed_snapshots: %w", err)
		}
	} else {
		exists, err := dbTableExists(ctx, db, "feed_snapshots")
		if err != nil {
			return err
		}
		if !exists {
			newExists, err := dbTableExists(ctx, db, "feed_snapshots_new")
			if err != nil {
				return err
			}
			var sql string
			if newExists {
				sql = "ALTER TABLE feed_snapshots_new RENAME TO feed_snapshots;"
			} else {
				sql = `CREATE TABLE feed_snapshots (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    recorded_at INTEGER NOT NULL
);`
			}
			sql += "CREATE INDEX IF NOT EXISTS idx_feed_snapshots_time ON feed_snapshots(recorded_at);"
			if _, err := db.ExecContext(ctx, sql); err != nil {
				return fmt.Errorf("finish feed_snapshots: %w", err)
			}
		}
	}

	_, err = db.ExecContext(ctx, "PRAGMA foreign_keys = ON")
	return err
}

func v035ConvertJSON(ctx context.Context, db *sql.DB) error {
	var alreadyConverted int
	if err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM feed_snapshot_counts").Scan(&alreadyConverted); err != nil {
		return err
	}
	if alreadyConverted > 0 {
		return nil
	}

	rows, err := db.QueryContext(ctx, "SELECT id, prefixes FROM feed_snapshots")
	if err != nil {
		return err
	}
	type snapshotRow struct {
		id       int64
		prefixes string
	}
	var snapshots []snapshotRow
	for rows.Next() {
		var s snapshotRow
		if err := rows.Scan(&s.id, &s.prefixes); err != nil {
			closeRowsLogged(rows)
			return err
		}
		snapshots = append(snapshots, s)
	}
	if err := rows.Err(); err != nil {
		closeRowsLogged(rows)
		return err
	}
	closeRowsLogged(rows)

	for _, snapshot := range snapshots {
		var counts map[int64]int
		if err := json.Unmarshal([]byte(snapshot.prefixes), &counts); err != nil {
			// Unparseable legacy metrics row — history isn't worth failing
			// the whole migration over, skip it.
			continue
		}
		for feedID, count := range counts {
			if _, err := db.ExecContext(ctx,
				"INSERT OR IGNORE INTO feed_snapshot_counts(snapshot_id, feed_id, prefix_count) VALUES (?, ?, ?)",
				snapshot.id, feedID, count); err != nil {
				return fmt.Errorf("convert snapshot %d: %w", snapshot.id, err)
			}
		}
	}
	return nil
}
