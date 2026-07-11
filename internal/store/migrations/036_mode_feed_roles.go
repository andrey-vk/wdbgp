package migrations

import (
	"context"
	"database/sql"
	"fmt"
)

// V036 adds include/exclude roles to mode↔feed links and the materialized
// per-mode entry table:
//
//   - catalog_mode_feeds.exclude: 0 = the feed's entries are part of the
//     mode's catalog (the previous behavior of every link), 1 = the feed's
//     prefixes are subtracted from the mode's built list. The same feed can
//     be an include in one mode and an exclude in another.
//   - catalog_mode_entries(mode_id, service_id, prefix_id): the merge
//     result — include entries with the exclude set hole-punched out,
//     fragments interned into prefixes like any other row. Rebuilt whole
//     per mode by store.RebuildModeEntries whenever a member feed syncs,
//     a membership/role changes, or a feed is enabled/disabled/deleted.
//     Communities are NOT copied here (they stay in catalog_communities,
//     one join away) and category is derivable via the service.
//
// The initial population is a plain copy of today's include-only state —
// no exclude links can exist yet, so the merge result is exactly the
// entries of every enabled feed linked to each mode.
//
// Idempotent (per-step guards) like the other schema migrations: the
// backup/restore path re-runs the newest migration on an already-migrated
// database.
func V036(ctx context.Context, tx *sql.Tx) error {
	var hasColumn int
	if err := tx.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM pragma_table_info('catalog_mode_feeds') WHERE name = 'exclude'").
		Scan(&hasColumn); err != nil {
		return fmt.Errorf("check exclude column: %w", err)
	}
	if hasColumn == 0 {
		if _, err := tx.ExecContext(ctx,
			"ALTER TABLE catalog_mode_feeds ADD COLUMN exclude INTEGER NOT NULL DEFAULT 0"); err != nil {
			return fmt.Errorf("add exclude column: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS catalog_mode_entries (
    mode_id    INTEGER NOT NULL REFERENCES catalog_modes(id) ON DELETE CASCADE,
    service_id INTEGER NOT NULL REFERENCES services(id),
    prefix_id  INTEGER NOT NULL REFERENCES prefixes(id),
    PRIMARY KEY (mode_id, service_id, prefix_id)
) WITHOUT ROWID;

INSERT OR IGNORE INTO catalog_mode_entries(mode_id, service_id, prefix_id)
SELECT DISTINCT cmf.mode_id, ce.service_id, ce.prefix_id
FROM catalog_mode_feeds cmf
JOIN feeds f ON f.id = cmf.feed_id AND f.enabled = 1
JOIN catalog_entries ce ON ce.feed_id = cmf.feed_id
WHERE cmf.exclude = 0;
`); err != nil {
		return fmt.Errorf("add mode feed roles: %w", err)
	}
	return nil
}
