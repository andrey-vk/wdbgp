package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/andrey-vk/wdbgp/internal/prefixfilter"

	_ "modernc.org/sqlite"
)

type Migration struct {
	Version int
	Name    string
	SQL     string
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
CREATE TABLE user_route_filters (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    action TEXT NOT NULL CHECK (action IN ('allow', 'deny')),
    cidr TEXT NOT NULL,
    PRIMARY KEY (user_id, action, cidr)
);

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
}

type Store struct {
	DB *sql.DB
}

type Feed struct {
	ID          int64
	Name        string
	URL         string
	ModeID      int64
	Enabled     bool
	LastSuccess string
	LastError   string
}

type CatalogMode struct {
	ID      int64
	Key     string
	Name    string
	Enabled bool
}

const (
	DefaultCatalogModeID  int64 = 1
	IPRangesCatalogModeID int64 = 2
)

type User struct {
	ID              int64
	Name            string
	PeerIP          string
	PeerASN         uint32
	NextHop         string
	BGPPassword     string
	SelectionLocked bool
	Enabled         bool
	FilterOverride  bool
	FilterMode      string
	FilterEditable  bool
	CatalogModeID   int64
	CatalogModeName string
	CatalogEditable bool
	Networks        []string
}

type ServiceKey struct {
	Category string
	Service  string
}

type CatalogPrefix struct {
	ServiceKey
	CIDR string
}

type RouteFilters struct {
	Allow []string
	Deny  []string
}

func Open(path string) (*Store, error) {
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
	if _, err := db.Exec("PRAGMA foreign_keys = ON; PRAGMA journal_mode = WAL; PRAGMA busy_timeout = 30000"); err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{DB: db}
	if err := s.Migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.DB.Close()
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
	rows, err := s.DB.QueryContext(ctx, "SELECT version FROM schema_migrations ORDER BY version")
	if err != nil {
		return err
	}
	var applied []int
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			rows.Close()
			return err
		}
		applied = append(applied, version)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for index, version := range applied {
		expected := index + 1
		if version != expected {
			return fmt.Errorf("database migration history has version %d where %d was expected", version, expected)
		}
	}
	if len(applied) > len(migrations) {
		return fmt.Errorf("database schema version %d is newer than supported version %d",
			applied[len(applied)-1], len(migrations))
	}
	for _, migration := range migrations {
		if migration.Version <= len(applied) {
			continue
		}
		tx, err := s.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, migration.SQL); err == nil {
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
	return nil
}

func NormalizePrefix(value string) (string, error) {
	prefix, err := netip.ParsePrefix(strings.TrimSpace(value))
	if err != nil {
		return "", err
	}
	return prefix.Masked().String(), nil
}

func (s *Store) Transaction(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *Store) Feeds(ctx context.Context, enabledOnly bool) ([]Feed, error) {
	query := `SELECT f.id, f.name, f.url, f.mode_id, f.enabled,
	                 COALESCE(f.last_success, ''), COALESCE(f.last_error, '')
	          FROM feeds f JOIN catalog_modes m ON m.id = f.mode_id`
	if enabledOnly {
		query += " WHERE f.enabled = 1 AND m.enabled = 1"
	}
	query += " ORDER BY f.id"
	rows, err := s.DB.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var feeds []Feed
	for rows.Next() {
		var feed Feed
		if err := rows.Scan(
			&feed.ID, &feed.Name, &feed.URL, &feed.ModeID,
			&feed.Enabled, &feed.LastSuccess, &feed.LastError,
		); err != nil {
			return nil, err
		}
		feeds = append(feeds, feed)
	}
	return feeds, rows.Err()
}

func (s *Store) CatalogModes(ctx context.Context, enabledOnly bool) ([]CatalogMode, error) {
	query := "SELECT id, key, name, enabled FROM catalog_modes"
	if enabledOnly {
		query += " WHERE enabled = 1"
	}
	query += " ORDER BY id"
	rows, err := s.DB.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var modes []CatalogMode
	for rows.Next() {
		var mode CatalogMode
		if err := rows.Scan(&mode.ID, &mode.Key, &mode.Name, &mode.Enabled); err != nil {
			return nil, err
		}
		modes = append(modes, mode)
	}
	return modes, rows.Err()
}

func (s *Store) CatalogMode(ctx context.Context, id int64) (CatalogMode, error) {
	var mode CatalogMode
	err := s.DB.QueryRowContext(ctx,
		"SELECT id, key, name, enabled FROM catalog_modes WHERE id = ?", id).
		Scan(&mode.ID, &mode.Key, &mode.Name, &mode.Enabled)
	return mode, err
}

func (s *Store) UpdateCatalogMode(ctx context.Context, mode CatalogMode) error {
	result, err := s.DB.ExecContext(ctx,
		"UPDATE catalog_modes SET name = ?, enabled = ? WHERE id = ?",
		mode.Name, mode.Enabled, mode.ID)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) Users(ctx context.Context, enabledOnly bool) ([]User, error) {
	query := `SELECT id, name, peer_ip, peer_asn, COALESCE(next_hop, ''),
	                 COALESCE(bgp_password, ''), selection_locked, enabled,
	                 filter_override_enabled, COALESCE(filter_mode, ''), filter_editable,
	                 catalog_mode_id, catalog_mode_editable,
	                 COALESCE((SELECT name FROM catalog_modes WHERE id = users.catalog_mode_id), '')
	          FROM users`
	if enabledOnly {
		query += " WHERE enabled = 1"
	}
	query += " ORDER BY id"
	rows, err := s.DB.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []User
	for rows.Next() {
		var user User
		if err := rows.Scan(
			&user.ID, &user.Name, &user.PeerIP, &user.PeerASN, &user.NextHop,
			&user.BGPPassword, &user.SelectionLocked, &user.Enabled,
			&user.FilterOverride, &user.FilterMode, &user.FilterEditable,
			&user.CatalogModeID, &user.CatalogEditable, &user.CatalogModeName,
		); err != nil {
			return nil, err
		}
		user.FilterMode = normalizeFilterMode(user.FilterMode, user.FilterOverride)
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for index := range users {
		users[index].Networks, err = s.UserNetworks(ctx, users[index].ID)
		if err != nil {
			return nil, err
		}
	}
	return users, nil
}

func (s *Store) User(ctx context.Context, id int64) (User, error) {
	var user User
	err := s.DB.QueryRowContext(ctx, `SELECT id, name, peer_ip, peer_asn, COALESCE(next_hop, ''),
		COALESCE(bgp_password, ''), selection_locked, enabled,
		filter_override_enabled, COALESCE(filter_mode, ''), filter_editable,
		catalog_mode_id, catalog_mode_editable,
		COALESCE((SELECT name FROM catalog_modes WHERE id = users.catalog_mode_id), '')
		FROM users WHERE id = ?`, id).
		Scan(&user.ID, &user.Name, &user.PeerIP, &user.PeerASN, &user.NextHop,
			&user.BGPPassword, &user.SelectionLocked, &user.Enabled,
			&user.FilterOverride, &user.FilterMode, &user.FilterEditable,
			&user.CatalogModeID, &user.CatalogEditable, &user.CatalogModeName)
	if err != nil {
		return User{}, err
	}
	user.FilterMode = normalizeFilterMode(user.FilterMode, user.FilterOverride)
	user.Networks, err = s.UserNetworks(ctx, id)
	return user, err
}

func (s *Store) UserNetworks(ctx context.Context, userID int64) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT cidr FROM user_networks WHERE user_id = ? ORDER BY cidr", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var networks []string
	for rows.Next() {
		var network string
		if err := rows.Scan(&network); err != nil {
			return nil, err
		}
		networks = append(networks, network)
	}
	return networks, rows.Err()
}

func (s *Store) UserByIP(ctx context.Context, address string) (User, error) {
	ip, err := netip.ParseAddr(address)
	if err != nil {
		return User{}, err
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT user_id, cidr FROM user_networks")
	if err != nil {
		return User{}, err
	}
	defer rows.Close()
	bestBits, bestID := -1, int64(0)
	for rows.Next() {
		var userID int64
		var cidr string
		if err := rows.Scan(&userID, &cidr); err != nil {
			return User{}, err
		}
		prefix, err := netip.ParsePrefix(cidr)
		if err == nil && prefix.Contains(ip) && prefix.Bits() > bestBits {
			bestBits, bestID = prefix.Bits(), userID
		}
	}
	if err := rows.Err(); err != nil {
		return User{}, err
	}
	if err := rows.Close(); err != nil {
		return User{}, err
	}
	if bestID == 0 {
		return User{}, sql.ErrNoRows
	}
	user, err := s.User(ctx, bestID)
	if err == nil && !user.Enabled {
		return User{}, sql.ErrNoRows
	}
	return user, err
}

func (s *Store) Catalog(ctx context.Context) (map[string][]string, error) {
	return s.CatalogForMode(ctx, DefaultCatalogModeID)
}

func (s *Store) CatalogForMode(ctx context.Context, modeID int64) (map[string][]string, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT DISTINCT ce.category, ce.service
FROM catalog_entries ce
JOIN feeds f ON f.id = ce.feed_id
WHERE f.enabled = 1 AND f.mode_id = ?
ORDER BY ce.category, ce.service`, modeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	catalog := map[string][]string{}
	for rows.Next() {
		var category, service string
		if err := rows.Scan(&category, &service); err != nil {
			return nil, err
		}
		catalog[category] = append(catalog[category], service)
	}
	return catalog, rows.Err()
}

func (s *Store) EnabledCatalogPrefixes(ctx context.Context, modeID int64) ([]CatalogPrefix, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT DISTINCT ce.category, ce.service, ce.cidr
FROM catalog_entries ce
JOIN feeds f ON f.id = ce.feed_id
JOIN catalog_modes m ON m.id = f.mode_id
WHERE f.enabled = 1 AND m.enabled = 1 AND f.mode_id = ?
ORDER BY ce.category, ce.service, ce.cidr`, modeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var prefixes []CatalogPrefix
	for rows.Next() {
		var prefix CatalogPrefix
		if err := rows.Scan(&prefix.Category, &prefix.Service, &prefix.CIDR); err != nil {
			return nil, err
		}
		prefixes = append(prefixes, prefix)
	}
	return prefixes, rows.Err()
}

func (s *Store) UserSelection(
	ctx context.Context,
	userID int64,
) (map[string]bool, map[ServiceKey]bool, error) {
	return s.UserModeSelection(ctx, userID, DefaultCatalogModeID)
}

func (s *Store) UserModeSelection(
	ctx context.Context,
	userID int64,
	modeID int64,
) (map[string]bool, map[ServiceKey]bool, error) {
	categories := map[string]bool{}
	services := map[ServiceKey]bool{}
	rows, err := s.DB.QueryContext(ctx,
		"SELECT category FROM selected_categories WHERE user_id = ? AND mode_id = ?", userID, modeID)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var category string
		if err := rows.Scan(&category); err != nil {
			rows.Close()
			return nil, nil, err
		}
		categories[category] = true
	}
	rows.Close()
	rows, err = s.DB.QueryContext(ctx,
		"SELECT category, service FROM selected_services WHERE user_id = ? AND mode_id = ?", userID, modeID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key ServiceKey
		if err := rows.Scan(&key.Category, &key.Service); err != nil {
			return nil, nil, err
		}
		services[key] = true
	}
	return categories, services, rows.Err()
}

func SetUserSelection(
	ctx context.Context,
	tx *sql.Tx,
	userID int64,
	categories []string,
	services []ServiceKey,
) error {
	return SetUserModeSelection(
		ctx, tx, userID, DefaultCatalogModeID, categories, services)
}

func SetUserModeSelection(
	ctx context.Context,
	tx *sql.Tx,
	userID int64,
	modeID int64,
	categories []string,
	services []ServiceKey,
) error {
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM selected_categories WHERE user_id = ? AND mode_id = ?", userID, modeID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM selected_services WHERE user_id = ? AND mode_id = ?", userID, modeID); err != nil {
		return err
	}
	sort.Strings(categories)
	for _, category := range categories {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO selected_categories(user_id, mode_id, category) VALUES (?, ?, ?)",
			userID, modeID, category); err != nil {
			return err
		}
	}
	sort.Slice(services, func(i, j int) bool {
		if services[i].Category == services[j].Category {
			return services[i].Service < services[j].Service
		}
		return services[i].Category < services[j].Category
	})
	for _, service := range services {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO selected_services(user_id, mode_id, category, service) VALUES (?, ?, ?, ?)",
			userID, modeID, service.Category, service.Service); err != nil {
			return err
		}
	}
	return nil
}

func SetVisibleUserSelection(
	ctx context.Context,
	tx *sql.Tx,
	userID int64,
	categories []string,
	services []ServiceKey,
) error {
	return SetVisibleUserModeSelection(
		ctx, tx, userID, DefaultCatalogModeID, categories, services)
}

func SetVisibleUserModeSelection(
	ctx context.Context,
	tx *sql.Tx,
	userID int64,
	modeID int64,
	categories []string,
	services []ServiceKey,
) error {
	rows, err := tx.QueryContext(ctx, `
SELECT sc.category
FROM selected_categories sc
WHERE sc.user_id = ? AND sc.mode_id = ?
  AND EXISTS (
      SELECT 1
      FROM catalog_entries ce
      JOIN feeds f ON f.id = ce.feed_id
      WHERE ce.category = sc.category AND f.mode_id = sc.mode_id AND f.enabled = 0
  )
  AND NOT EXISTS (
      SELECT 1
      FROM catalog_entries ce
      JOIN feeds f ON f.id = ce.feed_id
      WHERE ce.category = sc.category AND f.mode_id = sc.mode_id AND f.enabled = 1
  )`, userID, modeID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var category string
		if err := rows.Scan(&category); err != nil {
			rows.Close()
			return err
		}
		categories = append(categories, category)
	}
	if err := rows.Close(); err != nil {
		return err
	}

	rows, err = tx.QueryContext(ctx, `
SELECT ss.category, ss.service
FROM selected_services ss
WHERE ss.user_id = ? AND ss.mode_id = ?
  AND EXISTS (
      SELECT 1
      FROM catalog_entries ce
      JOIN feeds f ON f.id = ce.feed_id
      WHERE ce.category = ss.category
        AND ce.service = ss.service
        AND f.mode_id = ss.mode_id
        AND f.enabled = 0
  )
  AND NOT EXISTS (
      SELECT 1
      FROM catalog_entries ce
      JOIN feeds f ON f.id = ce.feed_id
      WHERE ce.category = ss.category
        AND ce.service = ss.service
        AND f.mode_id = ss.mode_id
        AND f.enabled = 1
  )`, userID, modeID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var service ServiceKey
		if err := rows.Scan(&service.Category, &service.Service); err != nil {
			rows.Close()
			return err
		}
		services = append(services, service)
	}
	if err := rows.Close(); err != nil {
		return err
	}

	return SetUserModeSelection(ctx, tx, userID, modeID,
		uniqueStrings(categories), uniqueServices(services))
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func uniqueServices(values []ServiceKey) []ServiceKey {
	seen := make(map[ServiceKey]bool, len(values))
	result := make([]ServiceKey, 0, len(values))
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func (s *Store) DesiredPrefixes(ctx context.Context) (map[string][]int64, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT DISTINCT ce.cidr, u.id, COALESCE(u.filter_mode, ''), u.filter_override_enabled
FROM users u
JOIN catalog_entries ce
  ON ce.category IN (
      SELECT category FROM selected_categories
      WHERE user_id = u.id AND mode_id = u.catalog_mode_id
  )
  OR EXISTS (
      SELECT 1 FROM selected_services ss
      WHERE ss.user_id = u.id
        AND ss.mode_id = u.catalog_mode_id
        AND ss.category = ce.category
        AND ss.service = ce.service
  )
JOIN feeds f ON f.id = ce.feed_id
JOIN catalog_modes m ON m.id = f.mode_id
WHERE u.enabled = 1
  AND f.enabled = 1
  AND m.enabled = 1
  AND f.mode_id = u.catalog_mode_id
ORDER BY ce.cidr, u.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type selectedUser struct {
		filterMode string
		prefixes   []netip.Prefix
	}
	selected := map[int64]*selectedUser{}
	for rows.Next() {
		var rawPrefix string
		var userID int64
		var filterMode string
		var override bool
		if err := rows.Scan(&rawPrefix, &userID, &filterMode, &override); err != nil {
			return nil, err
		}
		prefix, err := netip.ParsePrefix(rawPrefix)
		if err != nil {
			return nil, fmt.Errorf("parse selected prefix %q: %w", rawPrefix, err)
		}
		// A feed-provided default route is never a useful service route.
		if prefix.Bits() == 0 {
			continue
		}
		user := selected[userID]
		if user == nil {
			user = &selectedUser{filterMode: normalizeFilterMode(filterMode, override)}
			selected[userID] = user
		}
		user.prefixes = append(user.prefixes, prefix.Masked())
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	globalFilters, err := s.GlobalRouteFilters(ctx)
	if err != nil {
		return nil, err
	}
	result := map[string][]int64{}
	for userID, selectedUser := range selected {
		filtered, err := s.applyUserRouteFilters(
			ctx, userID, selectedUser.filterMode, selectedUser.prefixes, globalFilters)
		if err != nil {
			return nil, fmt.Errorf("filter routes for user %d: %w", userID, err)
		}
		for _, prefix := range filtered {
			result[prefix.String()] = append(result[prefix.String()], userID)
			if len(result) > prefixfilter.DefaultMaxPrefixes {
				return nil, fmt.Errorf("route filters produced more than %d unique routes",
					prefixfilter.DefaultMaxPrefixes)
			}
		}
	}
	return result, nil
}

func (s *Store) ApplyUserRouteFilters(
	ctx context.Context,
	user User,
	prefixes []netip.Prefix,
) ([]netip.Prefix, error) {
	globalFilters, err := s.GlobalRouteFilters(ctx)
	if err != nil {
		return nil, err
	}
	return s.applyUserRouteFilters(ctx, user.ID,
		normalizeFilterMode(user.FilterMode, user.FilterOverride), prefixes, globalFilters)
}

func (s *Store) applyUserRouteFilters(
	ctx context.Context,
	userID int64,
	filterMode string,
	prefixes []netip.Prefix,
	globalFilters RouteFilters,
) ([]netip.Prefix, error) {
	filters := globalFilters
	var err error
	switch filterMode {
	case FilterModeOverride:
		filters, err = s.UserRouteFilters(ctx, userID)
		if err != nil {
			return nil, err
		}
	case FilterModeExtend:
		userFilters, err := s.UserRouteFilters(ctx, userID)
		if err != nil {
			return nil, err
		}
		filters = mergeRouteFilters(globalFilters, userFilters)
	}
	lists, err := parseRouteFilters(filters)
	if err != nil {
		return nil, err
	}
	return prefixfilter.Apply(prefixes, lists, prefixfilter.DefaultMaxPrefixes)
}

func (s *Store) GlobalRouteFilters(ctx context.Context) (RouteFilters, error) {
	return readRouteFilters(ctx, s.DB, "SELECT action, cidr FROM global_route_filters ORDER BY action, cidr")
}

func (s *Store) UserRouteFilters(ctx context.Context, userID int64) (RouteFilters, error) {
	return readRouteFilters(ctx, s.DB,
		"SELECT action, cidr FROM user_route_filters WHERE user_id = ? ORDER BY action, cidr", userID)
}

func (s *Store) SetGlobalRouteFilters(ctx context.Context, filters RouteFilters) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM global_route_filters"); err != nil {
			return err
		}
		return insertRouteFilters(ctx, tx, 0, filters)
	})
}

func (s *Store) SetUserRouteFilters(ctx context.Context, userID int64, filters RouteFilters) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM user_route_filters WHERE user_id = ?", userID); err != nil {
			return err
		}
		return insertRouteFilters(ctx, tx, userID, filters)
	})
}

func (s *Store) SetUserRouteFilterConfig(ctx context.Context, userID int64, mode string, filters RouteFilters) error {
	mode, err := NormalizeFilterMode(mode)
	if err != nil {
		return err
	}
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM user_route_filters WHERE user_id = ?", userID); err != nil {
			return err
		}
		if err := insertRouteFilters(ctx, tx, userID, filters); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx,
			"UPDATE users SET filter_override_enabled = ?, filter_mode = ? WHERE id = ?",
			mode != FilterModeGlobal, mode, userID)
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count == 0 {
			return sql.ErrNoRows
		}
		return nil
	})
}

func (s *Store) SetUserFilterOverride(ctx context.Context, userID int64, enabled bool) error {
	mode := FilterModeGlobal
	if enabled {
		mode = FilterModeOverride
	}
	result, err := s.DB.ExecContext(ctx,
		"UPDATE users SET filter_override_enabled = ?, filter_mode = ? WHERE id = ?", enabled, mode, userID)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

const (
	FilterModeGlobal   = "global"
	FilterModeExtend   = "extend"
	FilterModeOverride = "override"
)

func NormalizeFilterMode(mode string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", FilterModeGlobal:
		return FilterModeGlobal, nil
	case FilterModeExtend:
		return FilterModeExtend, nil
	case FilterModeOverride:
		return FilterModeOverride, nil
	default:
		return "", fmt.Errorf("invalid route filter mode %q", mode)
	}
}

func normalizeFilterMode(mode string, override bool) string {
	normalized, err := NormalizeFilterMode(mode)
	if err == nil && normalized != FilterModeGlobal {
		return normalized
	}
	if override {
		return FilterModeOverride
	}
	return FilterModeGlobal
}

func mergeRouteFilters(global, user RouteFilters) RouteFilters {
	return RouteFilters{
		Allow: append(append([]string{}, global.Allow...), user.Allow...),
		Deny:  append(append([]string{}, global.Deny...), user.Deny...),
	}
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func readRouteFilters(ctx context.Context, db queryer, query string, args ...any) (RouteFilters, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return RouteFilters{}, err
	}
	defer rows.Close()
	var filters RouteFilters
	for rows.Next() {
		var action, cidr string
		if err := rows.Scan(&action, &cidr); err != nil {
			return RouteFilters{}, err
		}
		if action == "allow" {
			filters.Allow = append(filters.Allow, cidr)
		} else {
			filters.Deny = append(filters.Deny, cidr)
		}
	}
	return filters, rows.Err()
}

func insertRouteFilters(ctx context.Context, tx *sql.Tx, userID int64, filters RouteFilters) error {
	normalized, err := NormalizeRouteFilters(filters)
	if err != nil {
		return err
	}
	for _, item := range []struct {
		action string
		cidrs  []string
	}{
		{"allow", normalized.Allow},
		{"deny", normalized.Deny},
	} {
		for _, cidr := range item.cidrs {
			query := "INSERT INTO global_route_filters(action, cidr) VALUES (?, ?)"
			args := []any{item.action, cidr}
			if userID != 0 {
				query = "INSERT INTO user_route_filters(user_id, action, cidr) VALUES (?, ?, ?)"
				args = []any{userID, item.action, cidr}
			}
			if _, err := tx.ExecContext(ctx, query, args...); err != nil {
				return err
			}
		}
	}
	return nil
}

func NormalizeRouteFilters(filters RouteFilters) (RouteFilters, error) {
	normalize := func(values []string) ([]string, error) {
		unique := map[string]struct{}{}
		for _, value := range values {
			if strings.TrimSpace(value) == "" {
				continue
			}
			cidr, err := NormalizePrefix(value)
			if err != nil {
				return nil, err
			}
			unique[cidr] = struct{}{}
		}
		result := make([]string, 0, len(unique))
		for cidr := range unique {
			result = append(result, cidr)
		}
		sort.Strings(result)
		return result, nil
	}
	allow, err := normalize(filters.Allow)
	if err != nil {
		return RouteFilters{}, fmt.Errorf("invalid allow prefix: %w", err)
	}
	deny, err := normalize(filters.Deny)
	if err != nil {
		return RouteFilters{}, fmt.Errorf("invalid deny prefix: %w", err)
	}
	return RouteFilters{Allow: allow, Deny: deny}, nil
}

func parseRouteFilters(filters RouteFilters) (prefixfilter.Lists, error) {
	parse := func(values []string) ([]netip.Prefix, error) {
		result := make([]netip.Prefix, 0, len(values))
		for _, value := range values {
			prefix, err := netip.ParsePrefix(value)
			if err != nil {
				return nil, err
			}
			result = append(result, prefix.Masked())
		}
		return result, nil
	}
	allow, err := parse(filters.Allow)
	if err != nil {
		return prefixfilter.Lists{}, err
	}
	deny, err := parse(filters.Deny)
	if err != nil {
		return prefixfilter.Lists{}, err
	}
	return prefixfilter.Lists{Allow: allow, Deny: deny}, nil
}

func (s *Store) AddFeed(ctx context.Context, name, url string, enabled bool) error {
	return s.AddFeedForMode(ctx, name, url, DefaultCatalogModeID, enabled)
}

func (s *Store) AddFeedForMode(
	ctx context.Context,
	name string,
	url string,
	modeID int64,
	enabled bool,
) error {
	_, err := s.DB.ExecContext(ctx,
		"INSERT INTO feeds(name, url, mode_id, enabled) VALUES (?, ?, ?, ?)",
		name, url, modeID, enabled)
	return err
}

func (s *Store) UpdateFeed(ctx context.Context, feed Feed) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		var oldURL string
		if err := tx.QueryRowContext(ctx, "SELECT url FROM feeds WHERE id = ?", feed.ID).
			Scan(&oldURL); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			"UPDATE feeds SET name = ?, url = ?, mode_id = ?, enabled = ? WHERE id = ?",
			feed.Name, feed.URL, feed.ModeID, feed.Enabled, feed.ID); err != nil {
			return err
		}
		if oldURL == feed.URL {
			return nil
		}
		if _, err := tx.ExecContext(ctx,
			"DELETE FROM catalog_entries WHERE feed_id = ?", feed.ID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			"UPDATE feeds SET last_success = NULL, last_error = NULL WHERE id = ?", feed.ID)
		return err
	})
}

func (s *Store) DeleteFeed(ctx context.Context, id int64) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, "DELETE FROM feeds WHERE id = ?", id)
		if err != nil {
			return err
		}
		deleted, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if deleted == 0 {
			return sql.ErrNoRows
		}
		if _, err := tx.ExecContext(ctx, `
DELETE FROM selected_categories
WHERE NOT EXISTS (
    SELECT 1 FROM catalog_entries ce JOIN feeds f ON f.id = ce.feed_id
    WHERE ce.category = selected_categories.category
      AND f.mode_id = selected_categories.mode_id
)`); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
DELETE FROM selected_services
WHERE NOT EXISTS (
    SELECT 1 FROM catalog_entries ce JOIN feeds f ON f.id = ce.feed_id
    WHERE ce.category = selected_services.category
      AND ce.service = selected_services.service
      AND f.mode_id = selected_services.mode_id
)`)
		return err
	})
}

func (s *Store) AddUser(ctx context.Context, user User) (int64, error) {
	var id int64
	filterMode := normalizeFilterMode(user.FilterMode, user.FilterOverride)
	if user.CatalogModeID == 0 {
		user.CatalogModeID = DefaultCatalogModeID
	}
	err := s.Transaction(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `INSERT INTO users
			(name, peer_ip, peer_asn, next_hop, bgp_password, selection_locked, enabled,
			 filter_override_enabled, filter_mode, filter_editable,
			 catalog_mode_id, catalog_mode_editable)
			VALUES (?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?)`,
			user.Name, user.PeerIP, user.PeerASN, user.NextHop, user.BGPPassword,
			user.SelectionLocked, user.Enabled, filterMode != FilterModeGlobal, filterMode,
			user.FilterEditable, user.CatalogModeID, user.CatalogEditable)
		if err != nil {
			return err
		}
		id, err = result.LastInsertId()
		if err != nil {
			return err
		}
		return replaceNetworks(ctx, tx, id, user.Networks)
	})
	return id, err
}

func (s *Store) UpdateUser(ctx context.Context, user User, clearPassword bool) error {
	filterMode := normalizeFilterMode(user.FilterMode, user.FilterOverride)
	if user.CatalogModeID == 0 {
		user.CatalogModeID = DefaultCatalogModeID
	}
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		var password any = user.BGPPassword
		if clearPassword {
			password = nil
		} else if user.BGPPassword == "" {
			if err := tx.QueryRowContext(ctx, "SELECT bgp_password FROM users WHERE id = ?", user.ID).
				Scan(&password); err != nil {
				return err
			}
		}
		result, err := tx.ExecContext(ctx, `UPDATE users SET name=?, peer_ip=?, peer_asn=?,
			next_hop=NULLIF(?, ''), bgp_password=?, selection_locked=?, enabled=?,
			filter_override_enabled=?, filter_mode=?, filter_editable=?,
			catalog_mode_id=?, catalog_mode_editable=? WHERE id=?`,
			user.Name, user.PeerIP, user.PeerASN, user.NextHop, password,
			user.SelectionLocked, user.Enabled, filterMode != FilterModeGlobal, filterMode,
			user.FilterEditable, user.CatalogModeID, user.CatalogEditable, user.ID)
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count == 0 {
			return sql.ErrNoRows
		}
		return replaceNetworks(ctx, tx, user.ID, user.Networks)
	})
}

func (s *Store) SetUserCatalogMode(
	ctx context.Context,
	userID int64,
	modeID int64,
	requireEditable bool,
) error {
	query := `UPDATE users
SET catalog_mode_id = ?
WHERE id = ?
  AND EXISTS (SELECT 1 FROM catalog_modes WHERE id = ? AND enabled = 1)`
	args := []any{modeID, userID, modeID}
	if requireEditable {
		query += " AND (catalog_mode_id = ? OR catalog_mode_editable = 1)"
		args = append(args, modeID)
	}
	result, err := s.DB.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) SetUserCatalogModeSelection(
	ctx context.Context,
	userID int64,
	modeID int64,
	requireEditable bool,
	categories []string,
	services []ServiceKey,
) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		query := `UPDATE users
SET catalog_mode_id = ?
WHERE id = ?
  AND EXISTS (SELECT 1 FROM catalog_modes WHERE id = ? AND enabled = 1)`
		args := []any{modeID, userID, modeID}
		if requireEditable {
			query += " AND (catalog_mode_id = ? OR catalog_mode_editable = 1)"
			args = append(args, modeID)
		}
		result, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count == 0 {
			return sql.ErrNoRows
		}
		return SetVisibleUserModeSelection(
			ctx, tx, userID, modeID, categories, services)
	})
}

func replaceNetworks(ctx context.Context, tx *sql.Tx, userID int64, networks []string) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM user_networks WHERE user_id = ?", userID); err != nil {
		return err
	}
	for _, network := range networks {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO user_networks(user_id, cidr) VALUES (?, ?)", userID, network); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	result, err := s.DB.ExecContext(ctx, "DELETE FROM users WHERE id = ?", id)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) Stats(ctx context.Context) (int, int, int, error) {
	var categories, services, entries int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(DISTINCT category),
		COUNT(DISTINCT service), COUNT(*) FROM catalog_entries`).
		Scan(&categories, &services, &entries)
	return categories, services, entries, err
}

func IsNotFound(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}
