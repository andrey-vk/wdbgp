package migrations

import (
	"context"
	"database/sql"
)

// V031 drops feed_adapters.key. It was originally the join column back to
// the Go-source-hardcoded builtInAdapters map (see internal/store/
// feed_adapters.go), but the table already carries everything that
// identity check actually needs: is_builtin (added in migration 27)
// narrows candidates to the built-ins, and name (NOT NULL UNIQUE since the
// table was created) uniquely picks one of them — key added nothing name
// didn't already provide. For every non-built-in row it was dead weight:
// auto-generated from name at insert time and never read again.
//
// The actual rebuild runs in V031NoTxSQL: key carries a UNIQUE constraint,
// which SQLite's ALTER TABLE DROP COLUMN refuses to touch directly, so this
// needs the full create-copy-drop-rename rebuild — and feeds.adapter_id
// REFERENCES feed_adapters(id), so the implicit DELETE that DROP TABLE
// performs would trip that foreign key unless enforcement is off. PRAGMA
// foreign_keys is a no-op inside a transaction, so (like migration 20's
// users rebuild) this must run outside the migration tx via NoTxSQL.
func V031(ctx context.Context, tx *sql.Tx) error {
	return nil
}

// V031NoTxSQL performs the feed_adapters rebuild. Must run outside the
// migration transaction so PRAGMA foreign_keys actually takes effect.
//
// Runs as a single multi-statement exec with no surrounding transaction, so
// it must tolerate being killed mid-run and re-executed from the top on the
// next boot (NoTxSQL migrations must be idempotent — see store.go). Mirrors
// migration 20's users rebuild: CREATE TABLE IF NOT EXISTS so a retry after
// a crash between CREATE and DROP doesn't fail on "table already exists",
// and INSERT ... WHERE NOT EXISTS so that retry doesn't duplicate rows into
// the survivor from the killed run.
//
// feed_adapters not existing at all is its own case, checked first: DROP
// TABLE and the RENAME are separate autocommitting statements, so a crash
// between them leaves feed_adapters_new fully populated but no table named
// feed_adapters — pragma_table_info on a nonexistent table returns zero
// rows (not an error), which would otherwise be indistinguishable from "key
// already dropped, nothing to do" and let the caller mark this migration
// applied despite the rename never having finished.
func V031NoTxSQL(ctx context.Context, db *sql.DB) error {
	var feedAdaptersExists int
	if err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='feed_adapters'",
	).Scan(&feedAdaptersExists); err != nil {
		return err
	}
	if feedAdaptersExists == 0 {
		_, err := db.ExecContext(ctx, `
PRAGMA foreign_keys = OFF;
ALTER TABLE feed_adapters_new RENAME TO feed_adapters;
CREATE INDEX IF NOT EXISTS idx_feed_adapters_builtin ON feed_adapters(is_builtin);
PRAGMA foreign_keys = ON;
`)
		return err
	}

	var hasKey int
	if err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM pragma_table_info('feed_adapters') WHERE name='key'",
	).Scan(&hasKey); err != nil {
		return err
	}
	if hasKey == 0 {
		return nil
	}
	_, err := db.ExecContext(ctx, `
PRAGMA foreign_keys = OFF;
CREATE TABLE IF NOT EXISTS feed_adapters_new (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    language TEXT NOT NULL DEFAULT 'javascript'
        CHECK (language = 'javascript'),
    api_version INTEGER NOT NULL DEFAULT 1,
    source TEXT NOT NULL DEFAULT '',
    revision INTEGER NOT NULL DEFAULT 1,
    builtin_version INTEGER NOT NULL DEFAULT 0,
    is_customized INTEGER NOT NULL DEFAULT 0,
    forked_from INTEGER DEFAULT NULL,
    forked_version INTEGER NOT NULL DEFAULT 0,
    is_builtin INTEGER NOT NULL DEFAULT 0
);
INSERT INTO feed_adapters_new (id, name, language, api_version, source, revision, builtin_version, is_customized, forked_from, forked_version, is_builtin)
SELECT id, name, language, api_version, source, revision, builtin_version, is_customized, forked_from, forked_version, is_builtin
FROM feed_adapters
WHERE NOT EXISTS (SELECT 1 FROM feed_adapters_new LIMIT 1);
DROP TABLE IF EXISTS feed_adapters;
ALTER TABLE feed_adapters_new RENAME TO feed_adapters;
CREATE INDEX IF NOT EXISTS idx_feed_adapters_builtin ON feed_adapters(is_builtin);
PRAGMA foreign_keys = ON;
`)
	return err
}
