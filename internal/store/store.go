package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/andrey-vk/wdbgp/internal/config"
	"github.com/andrey-vk/wdbgp/internal/retry"

	_ "modernc.org/sqlite"
)

type Migration struct {
	Version  int
	Name     string
	SQL      string
	Go       func(*sql.Tx) error // optional post-SQL migration function
	NoTxSQL  string              // optional SQL to run outside the migration transaction
}

var migrations = []Migration{
	{
		Version: 1,
		Name:    "initial schema",
		SQL: `
CREATE TABLE IF NOT EXISTS feeds (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    url TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    last_success TEXT,
    last_error TEXT
);
CREATE TABLE IF NOT EXISTS catalog_entries (
    feed_id INTEGER NOT NULL REFERENCES feeds(id) ON DELETE CASCADE,
    category TEXT NOT NULL,
    service TEXT NOT NULL,
    cidr TEXT NOT NULL,
    PRIMARY KEY (feed_id, category, service, cidr)
);
CREATE TABLE IF NOT EXISTS users (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    peer_ip TEXT NOT NULL UNIQUE,
    peer_asn INTEGER NOT NULL,
    next_hop TEXT,
    bgp_password TEXT,
    selection_locked INTEGER NOT NULL DEFAULT 0,
    enabled INTEGER NOT NULL DEFAULT 1
);
CREATE TABLE IF NOT EXISTS user_networks (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    cidr TEXT NOT NULL UNIQUE,
    PRIMARY KEY (user_id, cidr)
);
CREATE TABLE IF NOT EXISTS selected_categories (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    category TEXT NOT NULL,
    PRIMARY KEY (user_id, category)
);
CREATE TABLE IF NOT EXISTS selected_services (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    category TEXT NOT NULL,
    service TEXT NOT NULL,
    PRIMARY KEY (user_id, category, service)
);
INSERT INTO feeds(name, url)
SELECT 'opencck-main', 'https://iplist.opencck.org/?format=json&data=cidr4'
WHERE NOT EXISTS (
    SELECT 1 FROM feeds WHERE url = 'https://iplist.opencck.org/?format=json&data=cidr4'
);
INSERT INTO feeds(name, url)
SELECT 'opencck-beta', 'https://beta.iplist.opencck.org/?format=json&data=cidr4'
WHERE NOT EXISTS (
    SELECT 1 FROM feeds WHERE url = 'https://beta.iplist.opencck.org/?format=json&data=cidr4'
);
`,
	},
	{
		Version: 2,
		Name:    "add OpenCCK IPv6 feeds",
		SQL: `
INSERT INTO feeds(name, url)
SELECT 'opencck-main-v6', 'https://iplist.opencck.org/?format=json&data=cidr6'
WHERE NOT EXISTS (
    SELECT 1 FROM feeds WHERE url = 'https://iplist.opencck.org/?format=json&data=cidr6'
);
INSERT INTO feeds(name, url)
SELECT 'opencck-beta-v6', 'https://beta.iplist.opencck.org/?format=json&data=cidr6'
WHERE NOT EXISTS (
    SELECT 1 FROM feeds WHERE url = 'https://beta.iplist.opencck.org/?format=json&data=cidr6'
);
`,
	},
	{
		Version: 3,
		Name:    "add lookup indexes",
		SQL: `
CREATE INDEX IF NOT EXISTS idx_catalog_category_service
    ON catalog_entries(category, service);
CREATE INDEX IF NOT EXISTS idx_selected_categories_user
    ON selected_categories(user_id);
CREATE INDEX IF NOT EXISTS idx_selected_services_user
    ON selected_services(user_id);
CREATE INDEX IF NOT EXISTS idx_user_networks_user
    ON user_networks(user_id);
`,
	},
	{
		Version: 4,
		Name:    "deduplicate feeds by URL",
		SQL: `
INSERT OR IGNORE INTO catalog_entries(feed_id, category, service, cidr)
SELECT keeper.id, ce.category, ce.service, ce.cidr
FROM catalog_entries ce
JOIN feeds duplicate ON duplicate.id = ce.feed_id
JOIN feeds keeper ON keeper.id = (
    SELECT MIN(candidate.id) FROM feeds candidate WHERE candidate.url = duplicate.url
)
WHERE duplicate.id != keeper.id;

DELETE FROM feeds
WHERE id NOT IN (SELECT MIN(id) FROM feeds GROUP BY url);

CREATE UNIQUE INDEX IF NOT EXISTS idx_feeds_url ON feeds(url);
`,
	},
	{
		Version: 5,
		Name:    "add route filters",
		SQL: `
ALTER TABLE users ADD COLUMN filter_override_enabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN filter_editable INTEGER NOT NULL DEFAULT 0;

CREATE TABLE global_route_filters (
    action TEXT NOT NULL CHECK (action IN ('allow', 'deny')),
    cidr TEXT NOT NULL,
    PRIMARY KEY (action, cidr)
);
CREATE INDEX idx_global_route_filters_action ON global_route_filters(action);
CREATE TABLE user_route_filters (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    action TEXT NOT NULL CHECK (action IN ('allow', 'deny')),
    cidr TEXT NOT NULL,
    PRIMARY KEY (user_id, action, cidr)
);
CREATE INDEX idx_user_route_filters_user ON user_route_filters(user_id);

INSERT INTO global_route_filters(action, cidr) VALUES
    ('deny', '0.0.0.0/8'),
    ('deny', '10.0.0.0/8'),
    ('deny', '100.64.0.0/10'),
    ('deny', '127.0.0.0/8'),
    ('deny', '169.254.0.0/16'),
    ('deny', '172.16.0.0/12'),
    ('deny', '192.0.0.0/24'),
    ('deny', '192.0.2.0/24'),
    ('deny', '192.168.0.0/16'),
    ('deny', '198.18.0.0/15'),
    ('deny', '198.51.100.0/24'),
    ('deny', '203.0.113.0/24'),
    ('deny', '224.0.0.0/4'),
    ('deny', '240.0.0.0/4'),
    ('deny', '::/128'),
    ('deny', '::1/128'),
    ('deny', '2001:db8::/32'),
    ('deny', 'fc00::/7'),
    ('deny', 'fe80::/10'),
    ('deny', 'ff00::/8');
`,
	},
	{
		Version: 6,
		Name:    "add user route filter mode",
		SQL: `
ALTER TABLE users ADD COLUMN filter_mode TEXT NOT NULL DEFAULT 'global'
    CHECK (filter_mode IN ('global', 'extend', 'override'));

UPDATE users
SET filter_mode = CASE WHEN filter_override_enabled THEN 'override' ELSE 'global' END;
`,
	},
	{
		Version: 7,
		Name:    "drop legacy OpenCCK feed category selections",
		SQL: `
DELETE FROM selected_categories
WHERE category IN (
    'opencck-main',
    'opencck-beta',
    'opencck-main-v4',
    'opencck-beta-v4',
    'opencck-main-v6',
    'opencck-beta-v6'
);
`,
	},
	{
		Version: 8,
		Name:    "drop orphan route selections",
		SQL: `
DELETE FROM selected_categories
WHERE NOT EXISTS (
    SELECT 1 FROM catalog_entries ce
    WHERE ce.category = selected_categories.category
);

DELETE FROM selected_services
WHERE NOT EXISTS (
    SELECT 1 FROM catalog_entries ce
    WHERE ce.category = selected_services.category
      AND ce.service = selected_services.service
);
`,
	},
	{
		Version: 9,
		Name:    "add catalog modes",
		SQL: `
CREATE TABLE catalog_modes (
    id INTEGER PRIMARY KEY,
    key TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL UNIQUE,
    enabled INTEGER NOT NULL DEFAULT 1
);
INSERT INTO catalog_modes(id, key, name, enabled) VALUES
    (1, 'opencck', 'OpenCCK', 1),
    (2, 'ipranges', 'IPRanges', 0);

ALTER TABLE feeds ADD COLUMN mode_id INTEGER REFERENCES catalog_modes(id);
UPDATE feeds SET mode_id = 1;
INSERT OR IGNORE INTO catalog_entries(feed_id, category, service, cidr)
SELECT keeper.id, ce.category, ce.service, ce.cidr
FROM catalog_entries ce
JOIN feeds duplicate ON duplicate.id = ce.feed_id
JOIN feeds keeper ON keeper.id = (
    SELECT MIN(candidate.id)
    FROM feeds candidate
    WHERE candidate.name = 'ipranges'
       OR candidate.url = 'https://github.com/lord-alfred/ipranges'
)
WHERE (duplicate.name = 'ipranges'
    OR duplicate.url = 'https://github.com/lord-alfred/ipranges')
  AND duplicate.id != keeper.id;

DELETE FROM feeds
WHERE (name = 'ipranges' OR url = 'https://github.com/lord-alfred/ipranges')
  AND id != (
      SELECT MIN(candidate.id)
      FROM feeds candidate
      WHERE candidate.name = 'ipranges'
         OR candidate.url = 'https://github.com/lord-alfred/ipranges'
  );

INSERT INTO feeds(name, url, enabled, mode_id)
SELECT 'ipranges', 'https://github.com/lord-alfred/ipranges', 1, 2
WHERE NOT EXISTS (
    SELECT 1 FROM feeds
    WHERE name = 'ipranges'
       OR url = 'https://github.com/lord-alfred/ipranges'
);

UPDATE feeds
SET name = 'ipranges',
    url = 'https://github.com/lord-alfred/ipranges',
    mode_id = 2
WHERE name = 'ipranges'
   OR url = 'https://github.com/lord-alfred/ipranges';

ALTER TABLE users ADD COLUMN catalog_mode_id INTEGER REFERENCES catalog_modes(id);
ALTER TABLE users ADD COLUMN catalog_mode_editable INTEGER NOT NULL DEFAULT 0;
UPDATE users SET catalog_mode_id = 1;

ALTER TABLE selected_categories RENAME TO selected_categories_legacy;
CREATE TABLE selected_categories (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    mode_id INTEGER NOT NULL REFERENCES catalog_modes(id) ON DELETE CASCADE,
    category TEXT NOT NULL,
    PRIMARY KEY (user_id, mode_id, category)
);
INSERT INTO selected_categories(user_id, mode_id, category)
SELECT user_id, 1, category FROM selected_categories_legacy;
DROP TABLE selected_categories_legacy;

ALTER TABLE selected_services RENAME TO selected_services_legacy;
CREATE TABLE selected_services (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    mode_id INTEGER NOT NULL REFERENCES catalog_modes(id) ON DELETE CASCADE,
    category TEXT NOT NULL,
    service TEXT NOT NULL,
    PRIMARY KEY (user_id, mode_id, category, service)
);
INSERT INTO selected_services(user_id, mode_id, category, service)
SELECT user_id, 1, category, service FROM selected_services_legacy;
DROP TABLE selected_services_legacy;

CREATE INDEX idx_selected_categories_user_mode
    ON selected_categories(user_id, mode_id);
CREATE INDEX idx_selected_services_user_mode
    ON selected_services(user_id, mode_id);
CREATE INDEX idx_feeds_mode ON feeds(mode_id);
`,
	},
	{
		Version: 10,
		Name:    "switch IPRanges source to antonme",
		SQL: `
DELETE FROM feeds
WHERE (
       name = 'ipranges'
    OR url = 'https://github.com/lord-alfred/ipranges'
    OR url = 'https://github.com/antonme/ipranges'
)
  AND id != (
      SELECT MIN(candidate.id)
      FROM feeds candidate
      WHERE candidate.name = 'ipranges'
         OR candidate.url = 'https://github.com/lord-alfred/ipranges'
         OR candidate.url = 'https://github.com/antonme/ipranges'
  );

UPDATE feeds
SET name = 'ipranges',
    url = 'https://github.com/antonme/ipranges',
    mode_id = 2,
    last_success = NULL,
    last_error = NULL
WHERE name = 'ipranges'
   OR url = 'https://github.com/lord-alfred/ipranges'
   OR url = 'https://github.com/antonme/ipranges';

DELETE FROM catalog_entries
WHERE feed_id = (
    SELECT id FROM feeds
    WHERE name = 'ipranges'
      AND url = 'https://github.com/antonme/ipranges'
);
`,
	},
	{
		Version: 11,
		Name:    "add script feed adapters",
		SQL: `
CREATE TABLE feed_adapters (
    id INTEGER PRIMARY KEY,
    key TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL UNIQUE,
    language TEXT NOT NULL DEFAULT 'javascript'
        CHECK (language = 'javascript'),
    api_version INTEGER NOT NULL DEFAULT 1,
    source TEXT NOT NULL DEFAULT '',
    allowed_hosts TEXT NOT NULL DEFAULT '',
    revision INTEGER NOT NULL DEFAULT 1
);

INSERT INTO feed_adapters(id, key, name, allowed_hosts) VALUES
    (1, 'canonical-json', 'Canonical JSON', ''),
    (2, 'opencck', 'OpenCCK', ''),
    (3, 'ipranges', 'IPRanges', 'raw.githubusercontent.com');

ALTER TABLE feeds ADD COLUMN adapter_id INTEGER REFERENCES feed_adapters(id);
UPDATE feeds SET adapter_id = CASE
    WHEN name = 'ipranges' OR url = 'https://github.com/antonme/ipranges' THEN 3
    WHEN url LIKE 'https://iplist.opencck.org/%'
      OR url LIKE 'https://beta.iplist.opencck.org/%' THEN 2
    ELSE 1
END;

CREATE INDEX idx_feeds_adapter ON feeds(adapter_id);
`,
	},
	{
		Version: 12,
		Name:    "query optimization indexes",
		SQL: `
-- Optimize DesiredPrefixes query
CREATE INDEX IF NOT EXISTS idx_users_enabled_catalog_mode ON users(enabled, catalog_mode_id);
CREATE INDEX IF NOT EXISTS idx_catalog_entries_feed_category_service ON catalog_entries(feed_id, category, service);
CREATE INDEX IF NOT EXISTS idx_feeds_enabled_mode ON feeds(enabled, mode_id, id);
CREATE INDEX IF NOT EXISTS idx_catalog_modes_enabled ON catalog_modes(enabled);

-- Additional indexes for common queries
CREATE INDEX IF NOT EXISTS idx_selected_categories_user_mode_category ON selected_categories(user_id, mode_id, category);
CREATE INDEX IF NOT EXISTS idx_selected_services_user_mode_category_service ON selected_services(user_id, mode_id, category, service);
`,
	},
	{
		Version: 13,
		Name:    "add catalog communities",
		SQL: `
CREATE TABLE catalog_communities (
    mode_id   INTEGER NOT NULL REFERENCES catalog_modes(id) ON DELETE CASCADE,
    category  TEXT NOT NULL,
    service   TEXT NOT NULL DEFAULT '',
    community INTEGER NOT NULL CHECK(community > 0 AND community <= 4294967295),
    PRIMARY KEY (mode_id, category, service)
);
CREATE INDEX idx_catalog_communities_mode ON catalog_communities(mode_id);
CREATE INDEX idx_catalog_communities_value ON catalog_communities(community);
`,
		Go: autoGenerateCommunities,
	},
	{
		Version: 14,
		Name:    "add web auth",
		SQL: `
ALTER TABLE users ADD COLUMN web_auth TEXT NOT NULL DEFAULT 'network';

CREATE TABLE user_credentials (
    user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    login         TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    PRIMARY KEY (user_id, login)
);
CREATE INDEX idx_user_credentials_login ON user_credentials(login);
`,
	},
	{
		Version: 15,
		Name:    "add app settings",
		SQL: `
CREATE TABLE app_settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
`,
	},
	{
		Version: 16,
		Name:    "make user credentials login globally unique",
		SQL: `
DROP INDEX IF EXISTS idx_user_credentials_login;
CREATE UNIQUE INDEX idx_user_credentials_login_unique ON user_credentials(login);
`,
	},
	{
		Version: 17,
		Name:    "add sync_interval to feeds",
		SQL: `
ALTER TABLE feeds ADD COLUMN sync_interval INTEGER NOT NULL DEFAULT 0;
`,
	},
	{
		Version: 18,
		Name:    "add data column to feeds",
		SQL: `
ALTER TABLE feeds ADD COLUMN data TEXT NOT NULL DEFAULT '';
`,
	},
	{
		Version: 19,
		Name:    "add sing-box SRS catalog mode",
		SQL: `
INSERT OR IGNORE INTO catalog_modes(key, name, enabled) VALUES ('singbox-srs', 'sing-box SRS', 0);
UPDATE catalog_modes SET name = 'sing-box SRS' WHERE key = 'singbox-srs';
INSERT OR IGNORE INTO feed_adapters(key, name) VALUES ('singbox-srs', 'sing-box SRS');
UPDATE feed_adapters SET name = 'sing-box SRS' WHERE key = 'singbox-srs' AND source = '';
INSERT OR IGNORE INTO feeds(name, url, mode_id, adapter_id, enabled, sync_interval, data)
SELECT 'Russia GeoIP (SRS)',
       'https://raw.githubusercontent.com/runetfreedom/russia-v2ray-rules-dat/release/sing-box/rule-set-geoip/geoip-ru.srs',
       (SELECT id FROM catalog_modes WHERE key = 'singbox-srs'),
       (SELECT id FROM feed_adapters WHERE key = 'singbox-srs'),
       0, 0,
       '{"category":"Russia","service":"geoip-ru"}'
WHERE EXISTS (SELECT 1 FROM catalog_modes WHERE key = 'singbox-srs')
  AND EXISTS (SELECT 1 FROM feed_adapters WHERE key = 'singbox-srs');
`,
	},
	{
		Version: 20,
		Name:    "add adapter upgrade support, catalog mode M:M",
		// DDL is idempotent — checks column/table existence before each operation
		// to allow safe re-run when migration record is replayed (e.g. after backup
		// restore or test scenarios that remove the migration marker).
		Go: func(tx *sql.Tx) error {
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
		},
		// NoTxSQL: remove peer_ip UNIQUE constraint (issue #17)
		// must run outside migration transaction because SQLite doesn't support
		// ALTER TABLE DROP CONSTRAINT inside a transaction in some configurations.
		// We use PRAGMA foreign_keys = OFF to prevent cascading deletes during rebuild.
		NoTxSQL: `
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
`,
	},
	{
		Version: 21,
		Name:    "add active_dial column to users",
		SQL:     "", // Schema change handled in Go function for idempotency.
		Go: func(tx *sql.Tx) error {
			var hasActiveDial int
			if err := tx.QueryRow("SELECT COUNT(*) FROM pragma_table_info('users') WHERE name='active_dial'").Scan(&hasActiveDial); err != nil {
				return err
			}
			if hasActiveDial == 0 {
				if _, err := tx.Exec("ALTER TABLE users ADD COLUMN active_dial INTEGER NOT NULL DEFAULT 1"); err != nil {
					return err
				}
			}
			return nil
		},
	},
}

var ErrDBTooNew = errors.New("database schema is newer than this server version")

type Store struct {
	DB            *sql.DB
	dbPath        string // DB file path for backup
	backupEnabled bool
	backupDir     string
	autoRestore   bool // auto-restore from backup when DB is newer than server
	Degraded       bool   // true when DB is newer and auto-restore didn't run
	DBVersion      int    // current DB schema version (when degraded)
	ServerVersion  int    // expected server schema version (when degraded)
	DegradedReason string // why degraded: "no backup found", "auto-restore disabled", etc.
}

type FeedAdapter struct {
	ID           int64
	Key          string
	Name         string
	Language     string
	APIVersion   int
	Source       string
	AllowedHosts string
	Revision     int64
	BuiltIn      bool
}

const (
	DefaultCatalogModeID  int64 = 1
	IPRangesCatalogModeID int64 = 2
	SingboxSRSCatalogModeID int64 = 3
)

type ServiceKey struct {
	Category string
	Service  string
}

type RouteFilters struct {
	Allow []string
	Deny  []string
}

func Open(path string, cfg config.Config) (*Store, error) {
	if parent := filepath.Dir(path); parent != "." {
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`
		PRAGMA foreign_keys = ON;
		PRAGMA journal_mode = WAL;
		PRAGMA synchronous = NORMAL;
		PRAGMA cache_size = -2000;
		PRAGMA temp_store = MEMORY;
		PRAGMA busy_timeout = 30000;
	`); err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{DB: db, dbPath: path, backupEnabled: cfg.BackupEnabled, backupDir: cfg.BackupDir, autoRestore: cfg.AutoRestoreEnabled}
	if err := s.Migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.DB.Close()
}

func (s *Store) readAppliedMigrations(ctx context.Context) ([]int, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT version FROM schema_migrations ORDER BY version")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var applied []int
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return nil, err
		}
		applied = append(applied, version)
	}
	return applied, rows.Err()
}

func (s *Store) tryRestore(ctx context.Context, applied []int) error {
	targetVersion := len(migrations)
	currentVersion := applied[len(applied)-1]

	backupDir := s.backupDir
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return fmt.Errorf("scan backup dir %s: %w", backupDir, err)
	}

	type candidate struct {
		path    string
		version int
		modTime time.Time
	}
	var candidates []candidate
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sqlite3") || !strings.HasPrefix(e.Name(), "wdbgp-backup-") {
			continue
		}
		backupPath := filepath.Join(backupDir, e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}
		backupDB, err := sql.Open("sqlite", backupPath)
		if err != nil {
			continue
		}
		backupDB.SetMaxOpenConns(1)
		rows, err := backupDB.QueryContext(ctx, "SELECT version FROM schema_migrations ORDER BY version")
		if err != nil {
			backupDB.Close()
			continue
		}
		var versions []int
		for rows.Next() {
			var v int
			if err := rows.Scan(&v); err != nil {
				rows.Close()
				backupDB.Close()
				continue
			}
			versions = append(versions, v)
		}
		rows.Close()
		backupDB.Close()
		if len(versions) == 0 {
			continue
		}
		maxVersion := versions[len(versions)-1]
		if maxVersion != targetVersion {
			continue
		}
		candidates = append(candidates, candidate{backupPath, maxVersion, info.ModTime()})
	}

	if len(candidates) == 0 {
		return fmt.Errorf("no backup found matching server version %d in %s", targetVersion, backupDir)
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].modTime.After(candidates[j].modTime)
	})
	best := candidates[0]

	// Save current (newer) DB with distinctive name
	savedPath := strings.TrimSuffix(s.dbPath, ".sqlite3") + ".incompatible-v" + strconv.Itoa(currentVersion) + ".sqlite3"
	if err := os.Rename(s.dbPath, savedPath); err != nil {
		return fmt.Errorf("save current DB: %w", err)
	}

	// Copy backup into place
	src, err := os.Open(best.path)
	if err != nil {
		return fmt.Errorf("open backup: %w", err)
	}
	dst, err := os.Create(s.dbPath)
	if err != nil {
		src.Close()
		return fmt.Errorf("create new DB: %w", err)
	}
	if _, err := dst.ReadFrom(src); err != nil {
		src.Close()
		dst.Close()
		return fmt.Errorf("copy backup: %w", err)
	}
	src.Close()
	dst.Close()

	log.Printf("DB auto-restored from %s (saved incompatible DB as %s)", best.path, savedPath)
	return nil
}

func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.DB.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    applied_at TEXT NOT NULL
)`); err != nil {
		return err
	}
	applied, err := s.readAppliedMigrations(ctx)
	if err != nil {
		return err
	}
	for index, version := range applied {
		expected := index + 1
		if version != expected {
			return fmt.Errorf("database migration history has version %d where %d was expected", version, expected)
		}
	}
	if len(applied) > len(migrations) {
		if s.autoRestore {
			if err := s.tryRestore(ctx, applied); err != nil {
				// Auto-restore failed — enter degraded mode so the UI
				// can show the error with instructions.
				s.Degraded = true
				s.DBVersion = applied[len(applied)-1]
				s.ServerVersion = len(migrations)
				s.DegradedReason = err.Error()
				return nil
			}
			// Re-open the restored DB
			s.DB.Close()
			db, err := sql.Open("sqlite", s.dbPath)
			if err != nil {
				return fmt.Errorf("reopen after restore: %w", err)
			}
			db.SetMaxOpenConns(1)
			if _, err := db.Exec(`
				PRAGMA foreign_keys = ON;
				PRAGMA journal_mode = WAL;
				PRAGMA synchronous = NORMAL;
				PRAGMA cache_size = -2000;
				PRAGMA temp_store = MEMORY;
				PRAGMA busy_timeout = 30000;
			`); err != nil {
				db.Close()
				return fmt.Errorf("reopen pragmas: %w", err)
			}
			s.DB = db
			// Re-read applied migrations from restored DB
			applied, err = s.readAppliedMigrations(ctx)
			if err != nil {
				return err
			}
		} else {
			s.Degraded = true
			s.DegradedReason = "auto-restore disabled (set WDBGP_AUTO_RESTORE_ENABLED=true)"
			s.DBVersion = applied[len(applied)-1]
			s.ServerVersion = len(migrations)
			return nil
		}
	}

	// Backup DB before running pending migrations.
	// Only backup when there are existing applied migrations — fresh
	// installs have nothing to preserve.
	if s.backupEnabled && len(applied) > 0 && len(applied) < len(migrations) {
		if err := os.MkdirAll(s.backupDir, 0755); err != nil {
			return fmt.Errorf("backup: create dir: %w", err)
		}
		backupName := "wdbgp-backup-" + time.Now().UTC().Format("20060102150405") + ".sqlite3"
		backupPath := filepath.Join(s.backupDir, backupName)

		// Checkpoint WAL so the file copy includes all committed changes.
		if _, err := s.DB.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
			return fmt.Errorf("backup: wal checkpoint: %w", err)
		}

		// Copy the DB file
		src, err := os.Open(s.dbPath)
		if err != nil {
			return fmt.Errorf("backup: open source: %w", err)
		}
		srcInfo, _ := src.Stat()
		dst, err := os.Create(backupPath)
		if err != nil {
			src.Close()
			return fmt.Errorf("backup: create backup: %w", err)
		}
		if _, err := dst.ReadFrom(src); err != nil {
			src.Close()
			dst.Close()
			return fmt.Errorf("backup: copy: %w", err)
		}
		src.Close()
		dst.Close()
		if srcInfo != nil {
			os.Chmod(backupPath, srcInfo.Mode())
		}

		// Strip catalog_entries from backup (recreatable by feed sync)
		backupDB, err := sql.Open("sqlite", backupPath)
		if err != nil {
			return fmt.Errorf("backup: open backup DB: %w", err)
		}
		backupDB.SetMaxOpenConns(1)
		backupDB.Exec("DELETE FROM catalog_entries")
		backupDB.Exec("VACUUM")
		backupDB.Close()

		log.Printf("DB backup saved to %s", backupPath)
	}

	for _, migration := range migrations {
		if migration.Version <= len(applied) {
			continue
		}
		// Run NoTxSQL BEFORE the transaction so that a failure does not
		// leave the DB in a partial state with the version already committed.
		// NoTxSQL must be idempotent so it is safe to re-run on retry.
		if migration.NoTxSQL != "" {
			if _, err := s.DB.ExecContext(ctx, migration.NoTxSQL); err != nil {
				return fmt.Errorf("migration %d NoTxSQL (%s): %w", migration.Version, migration.Name, err)
			}
		}
		tx, err := s.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, migration.SQL); err == nil && migration.Go != nil {
			err = migration.Go(tx)
		}
		if err == nil {
			_, err = tx.ExecContext(ctx,
				"INSERT INTO schema_migrations(version, name, applied_at) VALUES (?, ?, ?)",
				migration.Version, migration.Name, time.Now().UTC().Format(time.RFC3339Nano),
			)
		}
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d (%s): %w", migration.Version, migration.Name, err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	// Migration 20 backfill: ensure UNIQUE(peer_ip, peer_asn) exists (for DBs
	// that were already migrated before the constraint was added to the DDL).
	if _, err := s.DB.ExecContext(ctx,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_users_ip_asn ON users(peer_ip, peer_asn)`,
	); err != nil {
		return fmt.Errorf("users UNIQUE(peer_ip, peer_asn) index: %w", err)
	}
	// Backfill: index for filtering enabled users by catalog mode.
	if _, err := s.DB.ExecContext(ctx,
		`CREATE INDEX IF NOT EXISTS idx_users_enabled_catalog_mode ON users(enabled, catalog_mode_id)`,
	); err != nil {
		return fmt.Errorf("users enabled+catalog_mode index: %w", err)
	}
	return s.seedBuiltInAdapters(ctx)
}

func NormalizePrefix(value string) (string, error) {
	prefix, err := netip.ParsePrefix(strings.TrimSpace(value))
	if err != nil {
		return "", err
	}
	return prefix.Masked().String(), nil
}

func (s *Store) Transaction(ctx context.Context, fn func(*sql.Tx) error) error {
	// Use retry for database transactions (especially useful for SQLite busy errors)
	return retry.Do(ctx, retry.DatabaseConfig,
		func() error {
			tx, err := s.DB.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			if err := fn(tx); err != nil {
				tx.Rollback()
				return err
			}
			return tx.Commit()
		},
		retry.TransientError,
	)
}

func (s *Store) FeedAdapters(ctx context.Context) ([]FeedAdapter, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT id, key, name, language, api_version, source, allowed_hosts, revision
FROM feed_adapters
ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var adapters []FeedAdapter
	for rows.Next() {
		var adapter FeedAdapter
		if err := rows.Scan(
			&adapter.ID, &adapter.Key, &adapter.Name, &adapter.Language,
			&adapter.APIVersion, &adapter.Source, &adapter.AllowedHosts,
			&adapter.Revision,
		); err != nil {
			return nil, err
		}
		adapter.BuiltIn = IsBuiltInFeedAdapter(adapter.Key)
		adapters = append(adapters, adapter)
	}
	return adapters, rows.Err()
}

func (s *Store) FeedAdapter(ctx context.Context, id int64) (FeedAdapter, error) {
	var adapter FeedAdapter
	err := s.DB.QueryRowContext(ctx, `
SELECT id, key, name, language, api_version, source, allowed_hosts, revision
FROM feed_adapters
WHERE id = ?`, id).Scan(
		&adapter.ID, &adapter.Key, &adapter.Name, &adapter.Language,
		&adapter.APIVersion, &adapter.Source, &adapter.AllowedHosts,
		&adapter.Revision,
	)
	adapter.BuiltIn = IsBuiltInFeedAdapter(adapter.Key)
	return adapter, err
}

func (s *Store) Stats(ctx context.Context) (int, int, int, error) {
	var categories, services, entries int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(DISTINCT category),
		COUNT(DISTINCT service), COUNT(DISTINCT cidr) FROM catalog_entries`).
		Scan(&categories, &services, &entries)
	return categories, services, entries, err
}

func IsNotFound(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}

// autoGenerateCommunities is the migration 13 post-SQL function that fills
// communities for all existing catalog data.
func autoGenerateCommunities(tx *sql.Tx) error {
	_, err := genCommunities(tx, nil, 0)
	return err
}

// genCommunities generates communities for categories and services that don't have one yet.
// If existing is non-nil, it's used as the set of already-assigned keys to skip.
// If modeID is 0, generates for all modes; otherwise only for the specified mode.
func genCommunities(tx *sql.Tx, existing map[string]bool, modeID int64) (int, error) {
	var modeIDs []int64
	if modeID > 0 {
		modeIDs = []int64{modeID}
	} else {
		modes, err := tx.Query("SELECT DISTINCT id FROM catalog_modes ORDER BY id")
		if err != nil {
			return 0, err
		}
		defer modes.Close()

		for modes.Next() {
			var id int64
			if err := modes.Scan(&id); err != nil {
				return 0, err
			}
			modeIDs = append(modeIDs, id)
		}
		if err := modes.Err(); err != nil {
			return 0, err
		}
	}

	generated := 0
	for _, mid := range modeIDs {
		// Load all currently-assigned communities for this mode.
		commRows, err := tx.Query(
			"SELECT category, service, community FROM catalog_communities WHERE mode_id = ? ORDER BY community",
			mid)
		if err != nil {
			return 0, err
		}
		used := make(map[uint32]bool)
		keyComm := make(map[string]uint32)
		for commRows.Next() {
			var category, service string
			var community uint32
			if err := commRows.Scan(&category, &service, &community); err != nil {
				commRows.Close()
				return 0, err
			}
			used[community] = true
			if service == "" {
				keyComm["grp:"+category] = community
			} else {
				keyComm["svc:"+category+"|"+service] = community
			}
		}
		commRows.Close()

		// Get categories in alphabetical order.
		catRows, err := tx.Query(`
SELECT DISTINCT ce.category
FROM catalog_entries ce
JOIN feeds f ON f.id = ce.feed_id
WHERE f.mode_id = ?
ORDER BY ce.category`, mid)
		if err != nil {
			return 0, err
		}

		var categories []string
		for catRows.Next() {
			var cat string
			if err := catRows.Scan(&cat); err != nil {
				catRows.Close()
				return 0, err
			}
			categories = append(categories, cat)
		}
		catRows.Close()

		groupIndex := 0
		for _, category := range categories {
			groupKey := "grp:" + category

			// Determine group community: use existing assignment or find a free one.
			var groupCommunity uint32
			if existing != nil && existing[groupKey] {
				var ok bool
				groupCommunity, ok = keyComm[groupKey]
				if !ok {
					// Existing group expected to have a community; skip if missing.
					groupIndex++
					continue
				}
			} else {
				groupCommunity = findFirstFree(uint32((groupIndex+1)*10000), used)
				if _, err := tx.Exec(
					"INSERT OR IGNORE INTO catalog_communities(mode_id, category, service, community) VALUES (?, ?, '', ?)",
					mid, category, groupCommunity); err != nil {
					return generated, err
				}
				used[groupCommunity] = true
				generated++
			}

			// Get services in alphabetical order.
			svcRows, err := tx.Query(`
SELECT DISTINCT ce.service
FROM catalog_entries ce
JOIN feeds f ON f.id = ce.feed_id
WHERE f.mode_id = ? AND ce.category = ?
ORDER BY ce.service`, mid, category)
			if err != nil {
				return generated, err
			}

			var services []string
			for svcRows.Next() {
				var svc string
				if err := svcRows.Scan(&svc); err != nil {
					svcRows.Close()
					return generated, err
				}
				services = append(services, svc)
			}
			svcRows.Close()

			for _, service := range services {
				svcKey := "svc:" + category + "|" + service
				if existing != nil && existing[svcKey] {
					continue
				}
				// Find first free community starting from group_community+1.
				// Each insertion adds to used, so subsequent services naturally
				// get the next free number.  Overflow past 9999 services spills
				// into the next block — findFirstFree skips any used numbers.
				svcCommunity := findFirstFree(groupCommunity+1, used)

				if _, err := tx.Exec(
					"INSERT OR IGNORE INTO catalog_communities(mode_id, category, service, community) VALUES (?, ?, ?, ?)",
					mid, category, service, svcCommunity); err != nil {
					return generated, err
				}
				used[svcCommunity] = true
				generated++
			}
			groupIndex++
		}
	}
	return generated, nil
}



