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
}

type Store struct {
	DB *sql.DB
}

type Feed struct {
	ID          int64
	Name        string
	URL         string
	Enabled     bool
	LastSuccess string
	LastError   string
}

type User struct {
	ID              int64
	Name            string
	PeerIP          string
	PeerASN         uint32
	NextHop         string
	BGPPassword     string
	SelectionLocked bool
	Enabled         bool
	Networks        []string
}

type ServiceKey struct {
	Category string
	Service  string
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
	query := "SELECT id, name, url, enabled, COALESCE(last_success, ''), COALESCE(last_error, '') FROM feeds"
	if enabledOnly {
		query += " WHERE enabled = 1"
	}
	query += " ORDER BY id"
	rows, err := s.DB.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var feeds []Feed
	for rows.Next() {
		var feed Feed
		if err := rows.Scan(&feed.ID, &feed.Name, &feed.URL, &feed.Enabled, &feed.LastSuccess, &feed.LastError); err != nil {
			return nil, err
		}
		feeds = append(feeds, feed)
	}
	return feeds, rows.Err()
}

func (s *Store) Users(ctx context.Context, enabledOnly bool) ([]User, error) {
	query := `SELECT id, name, peer_ip, peer_asn, COALESCE(next_hop, ''),
	                 COALESCE(bgp_password, ''), selection_locked, enabled
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
		); err != nil {
			return nil, err
		}
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
		COALESCE(bgp_password, ''), selection_locked, enabled FROM users WHERE id = ?`, id).
		Scan(&user.ID, &user.Name, &user.PeerIP, &user.PeerASN, &user.NextHop,
			&user.BGPPassword, &user.SelectionLocked, &user.Enabled)
	if err != nil {
		return User{}, err
	}
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
	rows, err := s.DB.QueryContext(ctx,
		"SELECT DISTINCT category, service FROM catalog_entries ORDER BY category, service")
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

func (s *Store) UserSelection(ctx context.Context, userID int64) (map[string]bool, map[ServiceKey]bool, error) {
	categories := map[string]bool{}
	services := map[ServiceKey]bool{}
	rows, err := s.DB.QueryContext(ctx, "SELECT category FROM selected_categories WHERE user_id = ?", userID)
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
		"SELECT category, service FROM selected_services WHERE user_id = ?", userID)
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

func SetUserSelection(ctx context.Context, tx *sql.Tx, userID int64, categories []string, services []ServiceKey) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM selected_categories WHERE user_id = ?", userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM selected_services WHERE user_id = ?", userID); err != nil {
		return err
	}
	sort.Strings(categories)
	for _, category := range categories {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO selected_categories(user_id, category) VALUES (?, ?)", userID, category); err != nil {
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
			"INSERT INTO selected_services(user_id, category, service) VALUES (?, ?, ?)",
			userID, service.Category, service.Service); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) DesiredPrefixes(ctx context.Context) (map[string][]int64, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT DISTINCT ce.cidr, u.id
FROM users u
JOIN catalog_entries ce
  ON ce.category IN (SELECT category FROM selected_categories WHERE user_id = u.id)
  OR EXISTS (
      SELECT 1 FROM selected_services ss
      WHERE ss.user_id = u.id
        AND ss.category = ce.category
        AND ss.service = ce.service
  )
WHERE u.enabled = 1
ORDER BY ce.cidr, u.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string][]int64{}
	for rows.Next() {
		var prefix string
		var userID int64
		if err := rows.Scan(&prefix, &userID); err != nil {
			return nil, err
		}
		result[prefix] = append(result[prefix], userID)
	}
	return result, rows.Err()
}

func (s *Store) AddFeed(ctx context.Context, name, url string) error {
	_, err := s.DB.ExecContext(ctx, "INSERT INTO feeds(name, url) VALUES (?, ?)", name, url)
	return err
}

func (s *Store) AddUser(ctx context.Context, user User) (int64, error) {
	var id int64
	err := s.Transaction(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `INSERT INTO users
			(name, peer_ip, peer_asn, next_hop, bgp_password, selection_locked, enabled)
			VALUES (?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?)`,
			user.Name, user.PeerIP, user.PeerASN, user.NextHop, user.BGPPassword,
			user.SelectionLocked, user.Enabled)
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
			next_hop=NULLIF(?, ''), bgp_password=?, selection_locked=?, enabled=? WHERE id=?`,
			user.Name, user.PeerIP, user.PeerASN, user.NextHop, password,
			user.SelectionLocked, user.Enabled, user.ID)
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count == 0 {
			return sql.ErrNoRows
		}
		return replaceNetworks(ctx, tx, user.ID, user.Networks)
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
