package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/netip"
	"sort"

	"golang.org/x/crypto/bcrypt"
)

// User represents a BGP user/peer.
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
	ActiveDial      bool
	WebAuth         string // "network", "login", "both"
	Networks        []string
}

// UserCredential represents a login credential for web auth.
type UserCredential struct {
	UserID       int64
	Login        string
	PasswordHash string
}

func (s *Store) Users(ctx context.Context, enabledOnly bool) ([]User, error) {
	query := `SELECT id, name, peer_ip, peer_asn, COALESCE(next_hop, ''),
	                 COALESCE(bgp_password, ''), selection_locked, enabled,
	                 filter_override_enabled, COALESCE(filter_mode, ''), filter_editable,
	                 catalog_mode_id, catalog_mode_editable,
	                 COALESCE((SELECT name FROM catalog_modes WHERE id = users.catalog_mode_id), ''),
	                 active_dial, web_auth
	          FROM users`
	if enabledOnly {
		query += " WHERE enabled = 1"
	}
	query += " ORDER BY id"
	rows, err := s.DB.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	//nolint:errcheck
	defer func() { _ = rows.Close() }()
	var users []User
	for rows.Next() {
		var user User
		if err := rows.Scan(
			&user.ID, &user.Name, &user.PeerIP, &user.PeerASN, &user.NextHop,
			&user.BGPPassword, &user.SelectionLocked, &user.Enabled,
			&user.FilterOverride, &user.FilterMode, &user.FilterEditable,
			&user.CatalogModeID, &user.CatalogEditable, &user.CatalogModeName,
			&user.ActiveDial, &user.WebAuth,
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
		COALESCE((SELECT name FROM catalog_modes WHERE id = users.catalog_mode_id), ''),
		active_dial, web_auth
		FROM users WHERE id = ?`, id).
		Scan(&user.ID, &user.Name, &user.PeerIP, &user.PeerASN, &user.NextHop,
			&user.BGPPassword, &user.SelectionLocked, &user.Enabled,
			&user.FilterOverride, &user.FilterMode, &user.FilterEditable,
			&user.CatalogModeID, &user.CatalogEditable, &user.CatalogModeName,
			&user.ActiveDial, &user.WebAuth)
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
	//nolint:errcheck
	defer func() { _ = rows.Close() }()
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
	//nolint:errcheck
	defer func() { _ = rows.Close() }()
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

func (s *Store) AddUser(ctx context.Context, user User) (int64, error) {
	var id int64
	filterMode := normalizeFilterMode(user.FilterMode, user.FilterOverride)
	if user.CatalogModeID == 0 {
		user.CatalogModeID = DefaultCatalogModeID
	}
	if user.WebAuth == "" {
		user.WebAuth = "network"
	}
	err := s.Transaction(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `INSERT INTO users
			(name, peer_ip, peer_asn, next_hop, bgp_password, selection_locked, enabled,
			 filter_override_enabled, filter_mode, filter_editable,
			 catalog_mode_id, catalog_mode_editable, active_dial, web_auth)
			VALUES (?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			user.Name, user.PeerIP, user.PeerASN, user.NextHop, user.BGPPassword,
			user.SelectionLocked, user.Enabled, filterMode != FilterModeGlobal, filterMode,
			user.FilterEditable, user.CatalogModeID, user.CatalogEditable,
			user.ActiveDial, user.WebAuth)
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
		if user.WebAuth == "" {
			user.WebAuth = "network"
		}
		result, err := tx.ExecContext(ctx, `UPDATE users SET name=?, peer_ip=?, peer_asn=?,
			next_hop=NULLIF(?, ''), bgp_password=?, selection_locked=?, enabled=?,
			filter_override_enabled=?, filter_mode=?, filter_editable=?,
			catalog_mode_id=?, catalog_mode_editable=?, active_dial=?, web_auth=? WHERE id=?`,
			user.Name, user.PeerIP, user.PeerASN, user.NextHop, password,
			user.SelectionLocked, user.Enabled, filterMode != FilterModeGlobal, filterMode,
			user.FilterEditable, user.CatalogModeID, user.CatalogEditable,
			user.ActiveDial, user.WebAuth, user.ID)
		if err != nil {
			return err
		}
		if count, err := result.RowsAffected(); err != nil {
			return fmt.Errorf("rows affected: %w", err)
		} else if count == 0 {
			return sql.ErrNoRows
		}
		return replaceNetworks(ctx, tx, user.ID, user.Networks)
	})
}

func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	result, err := s.DB.ExecContext(ctx, "DELETE FROM users WHERE id = ?", id)
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("rows affected: %w", err)
	} else if count == 0 {
		return sql.ErrNoRows
	}
	return nil
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

// AuthenticateUser looks up a user by login credential (login + password).
// It joins user_credentials with users and compares the bcrypt hash.
// Returns the User on success, or an error (sql.ErrNoRows if not found,
// bcrypt.ErrMismatchedHashAndPassword if password is wrong).
func (s *Store) AuthenticateUser(ctx context.Context, login, password string) (User, error) {
	var userID int64
	var passwordHash string
	err := s.DB.QueryRowContext(ctx, `
		SELECT uc.user_id, uc.password_hash
		FROM user_credentials uc
		JOIN users u ON u.id = uc.user_id
		WHERE uc.login = ? AND u.enabled = 1`, login).Scan(&userID, &passwordHash)
	if err != nil {
		return User{}, err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)); err != nil {
		return User{}, err
	}
	return s.User(ctx, userID)
}

// GetUserCredentials returns all login credentials for a user.
func (s *Store) GetUserCredentials(ctx context.Context, userID int64) ([]UserCredential, error) {
	rows, err := s.DB.QueryContext(ctx,
		"SELECT login FROM user_credentials WHERE user_id = ? ORDER BY login", userID)
	if err != nil {
		return nil, err
	}
	//nolint:errcheck
	defer func() { _ = rows.Close() }()
	var credentials []UserCredential
	for rows.Next() {
		var cred UserCredential
		if err := rows.Scan(&cred.Login); err != nil {
			return nil, err
		}
		cred.UserID = userID
		credentials = append(credentials, cred)
	}
	return credentials, rows.Err()
}

// SetUserCredential creates or updates a credential for a user.
// If password is empty, deletes the credential. Passwords are bcrypt-hashed.
func (s *Store) SetUserCredential(ctx context.Context, userID int64, login, password string) error {
	if password == "" {
		_, err := s.DB.ExecContext(ctx,
			"DELETE FROM user_credentials WHERE user_id = ? AND login = ?", userID, login)
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx,
		`INSERT OR REPLACE INTO user_credentials(user_id, login, password_hash) VALUES (?, ?, ?)`,
		userID, login, string(hash))
	return err
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
			_ = rows.Close() //nolint:errcheck
			return nil, nil, err
		}
		categories[category] = true
	}
	_ = rows.Close() //nolint:errcheck
	rows, err = s.DB.QueryContext(ctx,
		"SELECT category, service FROM selected_services WHERE user_id = ? AND mode_id = ?", userID, modeID)
	if err != nil {
		return nil, nil, err
	}
	//nolint:errcheck
	defer func() { _ = rows.Close() }()
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
      JOIN catalog_mode_feeds cmf ON cmf.feed_id = f.id
      WHERE ce.category = sc.category AND cmf.mode_id = sc.mode_id AND f.enabled = 0
  )
  AND NOT EXISTS (
      SELECT 1
      FROM catalog_entries ce
      JOIN feeds f ON f.id = ce.feed_id
      JOIN catalog_mode_feeds cmf ON cmf.feed_id = f.id
      WHERE ce.category = sc.category AND cmf.mode_id = sc.mode_id AND f.enabled = 1
  )`, userID, modeID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var category string
		if err := rows.Scan(&category); err != nil {
			_ = rows.Close() //nolint:errcheck
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
      JOIN catalog_mode_feeds cmf ON cmf.feed_id = f.id
      WHERE ce.category = ss.category
        AND ce.service = ss.service
        AND cmf.mode_id = ss.mode_id
        AND f.enabled = 0
  )
  AND NOT EXISTS (
      SELECT 1
      FROM catalog_entries ce
      JOIN feeds f ON f.id = ce.feed_id
      JOIN catalog_mode_feeds cmf ON cmf.feed_id = f.id
      WHERE ce.category = ss.category
        AND ce.service = ss.service
        AND cmf.mode_id = ss.mode_id
        AND f.enabled = 1
  )`, userID, modeID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var service ServiceKey
		if err := rows.Scan(&service.Category, &service.Service); err != nil {
			_ = rows.Close() //nolint:errcheck
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
	if count, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("rows affected: %w", err)
	} else if count == 0 {
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
		if count, err := result.RowsAffected(); err != nil {
			return fmt.Errorf("rows affected: %w", err)
		} else if count == 0 {
			return sql.ErrNoRows
		}
		return SetVisibleUserModeSelection(
			ctx, tx, userID, modeID, categories, services)
	})
}
