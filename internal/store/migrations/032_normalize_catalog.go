package migrations

import (
	"context"
	"database/sql"
	"fmt"
)

// V032 normalizes the catalog schema around numeric keys:
//
//   - categories(id, name) and services(id, category_id, name) dictionaries
//     replace the category/service TEXT columns that were repeated on every
//     row of catalog_entries, selected_categories, selected_services and
//     catalog_communities (and again in each of their text indexes).
//   - prefixes(id, ip BLOB, bits) stores each distinct CIDR once, as a
//     masked 4- or 16-byte address blob instead of text, shared across
//     feeds.
//   - catalog_entries becomes (feed_id, service_id, prefix_id) WITHOUT
//     ROWID. Its data is NOT converted: entries are fully regenerable, so
//     the table is dropped and the feed sync loop (which runs
//     unconditionally at startup) repopulates it. Dictionaries are seeded
//     from the old text data first so selections and communities survive.
//
// The actual work runs in V032NoTxSQL: the rebuilds need PRAGMA
// foreign_keys=OFF (a no-op inside a transaction), same as migrations 20
// and 31.
func V032(ctx context.Context, tx *sql.Tx) error {
	return nil
}

// V032NoTxSQL performs the catalog normalization. Runs outside the
// migration transaction and must be idempotent: it can be killed at any
// statement boundary and re-run from the top on next boot (see store.go).
//
// Idempotency works per table, following the migration 31 state machine:
// a table still having its old TEXT column means "not rebuilt yet"; the
// table missing entirely means a previous run crashed between DROP and
// RENAME, so only the rename remains; neither means the table is done.
// Dictionary seeding uses INSERT OR IGNORE and reads only from tables
// still in their old shape, so a resumed run never fails on an
// already-rebuilt source and never loses names: seeding always runs
// before any rebuild in the same pass, and the dictionaries persist.
func V032NoTxSQL(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `
PRAGMA foreign_keys = OFF;
CREATE TABLE IF NOT EXISTS categories (
    id   INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE
);
CREATE TABLE IF NOT EXISTS services (
    id          INTEGER PRIMARY KEY,
    category_id INTEGER NOT NULL REFERENCES categories(id),
    name        TEXT NOT NULL,
    UNIQUE(category_id, name)
);
CREATE TABLE IF NOT EXISTS prefixes (
    id   INTEGER PRIMARY KEY,
    ip   BLOB NOT NULL,
    bits INTEGER NOT NULL,
    UNIQUE(ip, bits)
);
`); err != nil {
		return fmt.Errorf("create dictionaries: %w", err)
	}

	if err := v032SeedDictionaries(ctx, db); err != nil {
		return err
	}

	selectedCategoriesShape := `(
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    mode_id     INTEGER NOT NULL REFERENCES catalog_modes(id) ON DELETE CASCADE,
    category_id INTEGER NOT NULL REFERENCES categories(id),
    PRIMARY KEY (user_id, mode_id, category_id)
) WITHOUT ROWID`
	if err := v032Rebuild(ctx, db, "selected_categories", "category", selectedCategoriesShape, `
INSERT OR IGNORE INTO selected_categories_new (user_id, mode_id, category_id)
SELECT sc.user_id, sc.mode_id, c.id
FROM selected_categories sc
JOIN categories c ON c.name = sc.category
WHERE NOT EXISTS (SELECT 1 FROM selected_categories_new LIMIT 1);
DROP TABLE IF EXISTS selected_categories;
ALTER TABLE selected_categories_new RENAME TO selected_categories;
`); err != nil {
		return err
	}

	selectedServicesShape := `(
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    mode_id    INTEGER NOT NULL REFERENCES catalog_modes(id) ON DELETE CASCADE,
    service_id INTEGER NOT NULL REFERENCES services(id),
    PRIMARY KEY (user_id, mode_id, service_id)
) WITHOUT ROWID`
	if err := v032Rebuild(ctx, db, "selected_services", "category", selectedServicesShape, `
INSERT OR IGNORE INTO selected_services_new (user_id, mode_id, service_id)
SELECT ss.user_id, ss.mode_id, s.id
FROM selected_services ss
JOIN categories c ON c.name = ss.category
JOIN services s ON s.category_id = c.id AND s.name = ss.service
WHERE NOT EXISTS (SELECT 1 FROM selected_services_new LIMIT 1);
DROP TABLE IF EXISTS selected_services;
ALTER TABLE selected_services_new RENAME TO selected_services;
`); err != nil {
		return err
	}

	// service_id 0 is the category-wide sentinel (old rows with service='');
	// it deliberately has no FK so no dummy services row is needed.
	catalogCommunitiesShape := `(
    mode_id     INTEGER NOT NULL REFERENCES catalog_modes(id) ON DELETE CASCADE,
    category_id INTEGER NOT NULL REFERENCES categories(id),
    service_id  INTEGER NOT NULL DEFAULT 0,
    community   INTEGER NOT NULL CHECK(community > 0 AND community <= 4294967295),
    PRIMARY KEY (mode_id, category_id, service_id)
) WITHOUT ROWID`
	if err := v032Rebuild(ctx, db, "catalog_communities", "category", catalogCommunitiesShape, `
INSERT OR IGNORE INTO catalog_communities_new (mode_id, category_id, service_id, community)
SELECT cc.mode_id, c.id, COALESCE(s.id, 0), cc.community
FROM catalog_communities cc
JOIN categories c ON c.name = cc.category
LEFT JOIN services s ON s.category_id = c.id AND s.name = cc.service AND cc.service != ''
WHERE NOT EXISTS (SELECT 1 FROM catalog_communities_new LIMIT 1);
DROP TABLE IF EXISTS catalog_communities;
ALTER TABLE catalog_communities_new RENAME TO catalog_communities;
`); err != nil {
		return err
	}

	// catalog_entries data is dropped, not converted (regenerated by feed
	// sync), so there is no copy step and no _new table: just replace it.
	hasOld, err := dbTableHasColumn(ctx, db, "catalog_entries", "category")
	if err != nil {
		return err
	}
	exists, err := dbTableExists(ctx, db, "catalog_entries")
	if err != nil {
		return err
	}
	if hasOld || !exists {
		if _, err := db.ExecContext(ctx, `
DROP TABLE IF EXISTS catalog_entries;
CREATE TABLE catalog_entries (
    feed_id    INTEGER NOT NULL REFERENCES feeds(id) ON DELETE CASCADE,
    service_id INTEGER NOT NULL REFERENCES services(id),
    prefix_id  INTEGER NOT NULL REFERENCES prefixes(id),
    PRIMARY KEY (feed_id, service_id, prefix_id)
) WITHOUT ROWID;
`); err != nil {
			return fmt.Errorf("rebuild catalog_entries: %w", err)
		}
	}

	_, err = db.ExecContext(ctx, `PRAGMA foreign_keys = ON;`)
	return err
}

// v032SeedDictionaries fills categories and services from every table that
// still carries the old TEXT columns. Selections and communities are
// included alongside catalog_entries so that names referenced only by a
// user selection or a community (e.g. while the catalog happens to be
// empty) still get a dictionary row to remap onto.
func v032SeedDictionaries(ctx context.Context, db *sql.DB) error {
	type source struct {
		table       string
		categoryCol string
		serviceCol  string // "" = table has no service column
		extraWhere  string
	}
	sources := []source{
		{"catalog_entries", "category", "service", ""},
		{"selected_categories", "category", "", ""},
		{"selected_services", "category", "service", ""},
		{"catalog_communities", "category", "service", "service != ''"},
	}
	for _, src := range sources {
		oldShape, err := dbTableHasColumn(ctx, db, src.table, src.categoryCol)
		if err != nil {
			return err
		}
		if !oldShape {
			continue
		}
		if _, err := db.ExecContext(ctx, fmt.Sprintf(
			`INSERT OR IGNORE INTO categories(name) SELECT DISTINCT %s FROM %s`,
			src.categoryCol, src.table)); err != nil {
			return fmt.Errorf("seed categories from %s: %w", src.table, err)
		}
		if src.serviceCol == "" {
			continue
		}
		where := ""
		if src.extraWhere != "" {
			where = " WHERE " + src.extraWhere
		}
		if _, err := db.ExecContext(ctx, fmt.Sprintf(`
INSERT OR IGNORE INTO services(category_id, name)
SELECT c.id, t.%s
FROM (SELECT DISTINCT %s AS category, %s FROM %s%s) t
JOIN categories c ON c.name = t.category`,
			src.serviceCol, src.categoryCol, src.serviceCol, src.table, where)); err != nil {
			return fmt.Errorf("seed services from %s: %w", src.table, err)
		}
	}
	return nil
}

// v032Rebuild applies the migration 31 per-table state machine, extended
// for tables that may not exist at all on very old databases:
//
//   - table has oldColumn      → run the copy SQL (CREATE _new + copy +
//     DROP + RENAME)
//   - table missing, _new here → a previous run crashed between DROP and
//     RENAME; finish the rename
//   - table and _new missing   → nothing to convert; create the new shape
//     directly
//   - otherwise                → already converted, done
//
// newShape is the column/constraint list shared by both CREATE paths.
func v032Rebuild(ctx context.Context, db *sql.DB, table, oldColumn, newShape, copySQL string) error {
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
			sql = fmt.Sprintf("ALTER TABLE %s_new RENAME TO %s;", table, table)
		} else {
			sql = fmt.Sprintf("CREATE TABLE %s %s;", table, newShape)
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
	if _, err := db.ExecContext(ctx, createSQL+copySQL); err != nil {
		return fmt.Errorf("rebuild %s: %w", table, err)
	}
	return nil
}
