package store

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/netip"
	"sort"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/andrey-vk/wdbgp/internal/prefixfilter"
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

// users.filter_mode and users.web_auth are INTEGER enums in the database
// (schema >= 33); the Go API keeps the string forms. The mappings below
// are the single source of truth for both directions (migration 33
// mirrors them for its one-time conversion).

func filterModeToInt(mode string) int {
	switch mode {
	case FilterModeExtend:
		return 1
	case FilterModeOverride:
		return 2
	default:
		return 0
	}
}

func filterModeFromInt(value int) string {
	switch value {
	case 1:
		return FilterModeExtend
	case 2:
		return FilterModeOverride
	default:
		return FilterModeGlobal
	}
}

func webAuthToInt(mode string) int {
	switch mode {
	case "login":
		return 1
	case "both":
		return 2
	case "any":
		return 3
	default: // "network"
		return 0
	}
}

func webAuthFromInt(value int) string {
	switch value {
	case 1:
		return "login"
	case 2:
		return "both"
	case 3:
		return "any"
	default:
		return "network"
	}
}

// decodeStoredAddr renders a stored address BLOB as text; nil (SQL NULL)
// becomes "".
func decodeStoredAddr(ip []byte) (string, error) {
	if len(ip) == 0 {
		return "", nil
	}
	addr, err := DecodeAddr(ip)
	if err != nil {
		return "", err
	}
	return addr.String(), nil
}

// encodeAddrArg converts a textual IP to its stored BLOB form for use as a
// query argument.
func encodeAddrArg(value string) ([]byte, error) {
	addr, err := netip.ParseAddr(strings.TrimSpace(value))
	if err != nil {
		return nil, err
	}
	return addr.AsSlice(), nil
}

// scanUserRow decodes one users row shared by Users and User; the query
// must select the columns in userSelectColumns order.
const userSelectColumns = `id, name, peer_ip, peer_asn, next_hop,
	                 COALESCE(bgp_password, ''), selection_locked, enabled,
	                 filter_mode, filter_editable,
	                 catalog_mode_id, catalog_mode_editable,
	                 COALESCE((SELECT name FROM catalog_modes WHERE id = users.catalog_mode_id), ''),
	                 active_dial, web_auth`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanUserRow(row rowScanner) (User, error) {
	var user User
	var peerIP, nextHop []byte
	var filterMode, webAuth int
	if err := row.Scan(
		&user.ID, &user.Name, &peerIP, &user.PeerASN, &nextHop,
		&user.BGPPassword, &user.SelectionLocked, &user.Enabled,
		&filterMode, &user.FilterEditable,
		&user.CatalogModeID, &user.CatalogEditable, &user.CatalogModeName,
		&user.ActiveDial, &webAuth,
	); err != nil {
		return User{}, err
	}
	var err error
	if user.PeerIP, err = decodeStoredAddr(peerIP); err != nil {
		return User{}, fmt.Errorf("user %d peer_ip: %w", user.ID, err)
	}
	if user.NextHop, err = decodeStoredAddr(nextHop); err != nil {
		return User{}, fmt.Errorf("user %d next_hop: %w", user.ID, err)
	}
	user.FilterMode = filterModeFromInt(filterMode)
	user.FilterOverride = user.FilterMode != FilterModeGlobal
	user.WebAuth = webAuthFromInt(webAuth)
	return user, nil
}

func (s *Store) Users(ctx context.Context, enabledOnly bool) ([]User, error) {
	query := "SELECT " + userSelectColumns + " FROM users"
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
		user, err := scanUserRow(rows)
		if err != nil {
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
	user, err := scanUserRow(s.DB.QueryRowContext(ctx,
		"SELECT "+userSelectColumns+" FROM users WHERE id = ?", id))
	if err != nil {
		return User{}, err
	}
	user.Networks, err = s.UserNetworks(ctx, id)
	return user, err
}

func (s *Store) UserNetworks(ctx context.Context, userID int64) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx,
		"SELECT ip, bits FROM user_networks WHERE user_id = ? ORDER BY ip, bits", userID)
	if err != nil {
		return nil, err
	}
	//nolint:errcheck
	defer func() { _ = rows.Close() }()
	var networks []string
	for rows.Next() {
		var ip []byte
		var bits int
		if err := rows.Scan(&ip, &bits); err != nil {
			return nil, err
		}
		prefix, err := DecodePrefix(ip, bits)
		if err != nil {
			return nil, err
		}
		networks = append(networks, prefix.String())
	}
	return networks, rows.Err()
}

// AggregateNetworks parses each CIDR, then returns the minimal set of
// prefixes covering the same address space — masked, deduplicated, and with
// overlapping or adjacent prefixes merged (see prefixfilter.Aggregate).
// Returns an error naming the first entry that fails to parse.
func AggregateNetworks(raw []string) ([]string, error) {
	prefixes := make([]netip.Prefix, 0, len(raw))
	for _, r := range raw {
		p, err := netip.ParsePrefix(strings.TrimSpace(r))
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR %q: %w", r, err)
		}
		prefixes = append(prefixes, p.Masked())
	}
	aggregated := prefixfilter.Aggregate(prefixes)
	result := make([]string, len(aggregated))
	for i, p := range aggregated {
		result[i] = p.String()
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result, nil
}

// ActiveNetworksOverlap reports whether any candidate network overlaps with
// an active (web_auth network/both/any, and enabled) network belonging to a
// different user. Disabled users are excluded here on purpose: their
// networks are stale/vestigial, so a disabled user's leftover network must
// not be able to block an unrelated enabled user's save. UserByIP filters
// out disabled users' networks from its own candidate set for the same
// reason — that filter is load-bearing, not redundant: without it, a
// disabled user's more-specific network could win UserByIP's
// longest-prefix comparison and shadow an enabled user's broader,
// legitimately-overlapping one instead of falling through to it.
// excludeUserID is the user being saved (0 for a new user, so nothing is
// excluded). Candidates are expected already-normalized (see
// AggregateNetworks) — this only checks for cross-user conflicts, not
// internal redundancy. Returns an error naming the conflicting user and
// both CIDRs if a conflict is found.
func (s *Store) ActiveNetworksOverlap(ctx context.Context, candidates []string, excludeUserID int64) error {
	if len(candidates) == 0 {
		return nil
	}
	candidatePrefixes := make([]netip.Prefix, 0, len(candidates))
	for _, c := range candidates {
		p, err := netip.ParsePrefix(strings.TrimSpace(c))
		if err != nil {
			return fmt.Errorf("invalid CIDR %q: %w", c, err)
		}
		candidatePrefixes = append(candidatePrefixes, p.Masked())
	}

	rows, err := s.DB.QueryContext(ctx, `
		SELECT un.ip, un.bits, u.name
		FROM user_networks un
		JOIN users u ON u.id = un.user_id
		WHERE u.web_auth IN (`+activeWebAuthModesSQL+`) AND u.enabled = 1 AND un.user_id != ?
	`, excludeUserID)
	if err != nil {
		return err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("WARNING: rows close: %v", err)
		}
	}()

	for rows.Next() {
		var ip []byte
		var bits int
		var name string
		if err := rows.Scan(&ip, &bits, &name); err != nil {
			return err
		}
		otherPrefix, err := DecodePrefix(ip, bits)
		if err != nil {
			continue
		}
		for _, candidate := range candidatePrefixes {
			if candidate.Overlaps(otherPrefix) {
				return fmt.Errorf("network %s overlaps with %s, already assigned to user %q", candidate, otherPrefix, name)
			}
		}
	}
	return rows.Err()
}

// activeWebAuthModesSQL lists the web_auth enum values that use IP-based
// user resolution — everything except login (1). A user's networks only
// matter for UserByIP / cross-user overlap validation while their current
// mode is one of these; a login-mode user's networks are stale/vestigial
// (left over from a previous mode) and must be invisible to both. Fixed,
// literal values — safe to inline directly, no user input involved.
const activeWebAuthModesSQL = "0, 2, 3" // network, both, any

func (s *Store) UserByIP(ctx context.Context, address string) (User, error) {
	ip, err := netip.ParseAddr(address)
	if err != nil {
		return User{}, err
	}
	rows, err := s.DB.QueryContext(ctx, `
		SELECT un.user_id, un.ip, un.bits
		FROM user_networks un
		JOIN users u ON u.id = un.user_id
		WHERE u.web_auth IN (`+activeWebAuthModesSQL+`) AND u.enabled = 1
	`)
	if err != nil {
		return User{}, err
	}
	//nolint:errcheck
	defer func() { _ = rows.Close() }()
	bestBits, bestID := -1, int64(0)
	for rows.Next() {
		var userID int64
		var netIP []byte
		var bits int
		if err := rows.Scan(&userID, &netIP, &bits); err != nil {
			return User{}, err
		}
		prefix, err := DecodePrefix(netIP, bits)
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
	peerIP, nextHop, err := encodeUserAddrs(user)
	if err != nil {
		return 0, err
	}
	err = s.Transaction(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `INSERT INTO users
			(name, peer_ip, peer_asn, next_hop, bgp_password, selection_locked, enabled,
			 filter_mode, filter_editable,
			 catalog_mode_id, catalog_mode_editable, active_dial, web_auth)
			VALUES (?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, ?)`,
			user.Name, peerIP, user.PeerASN, nextHop, user.BGPPassword,
			user.SelectionLocked, user.Enabled, filterModeToInt(filterMode),
			user.FilterEditable, user.CatalogModeID, user.CatalogEditable,
			user.ActiveDial, webAuthToInt(user.WebAuth))
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

// encodeUserAddrs converts a user's textual peer_ip/next_hop to their
// stored BLOB forms; an empty next_hop becomes nil (SQL NULL).
func encodeUserAddrs(user User) (peerIP []byte, nextHop any, err error) {
	peerIP, err = encodeAddrArg(user.PeerIP)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid peer IP %q: %w", user.PeerIP, err)
	}
	if user.NextHop != "" {
		encoded, err := encodeAddrArg(user.NextHop)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid next hop %q: %w", user.NextHop, err)
		}
		nextHop = encoded
	}
	return peerIP, nextHop, nil
}

func (s *Store) UpdateUser(ctx context.Context, user User) error {
	filterMode := normalizeFilterMode(user.FilterMode, user.FilterOverride)
	if user.CatalogModeID == 0 {
		user.CatalogModeID = DefaultCatalogModeID
	}
	if user.WebAuth == "" {
		user.WebAuth = "network"
	}
	peerIP, nextHop, err := encodeUserAddrs(user)
	if err != nil {
		return err
	}
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE users SET name=?, peer_ip=?, peer_asn=?,
			next_hop=?, bgp_password=?, selection_locked=?, enabled=?,
			filter_mode=?, filter_editable=?,
			catalog_mode_id=?, catalog_mode_editable=?, active_dial=?, web_auth=? WHERE id=?`,
			user.Name, peerIP, user.PeerASN, nextHop, user.BGPPassword,
			user.SelectionLocked, user.Enabled, filterModeToInt(filterMode),
			user.FilterEditable, user.CatalogModeID, user.CatalogEditable,
			user.ActiveDial, webAuthToInt(user.WebAuth), user.ID)
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
		ip, bits, err := EncodePrefixString(network)
		if err != nil {
			return fmt.Errorf("invalid network %q: %w", network, err)
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO user_networks(user_id, ip, bits) VALUES (?, ?, ?)", userID, ip, bits); err != nil {
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
	rows, err := s.DB.QueryContext(ctx, `
SELECT c.name FROM selected_categories sc
JOIN categories c ON c.id = sc.category_id
WHERE sc.user_id = ? AND sc.mode_id = ?`, userID, modeID)
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
	rows, err = s.DB.QueryContext(ctx, `
SELECT c.name, sv.name FROM selected_services ss
JOIN services sv ON sv.id = ss.service_id
JOIN categories c ON c.id = sv.category_id
WHERE ss.user_id = ? AND ss.mode_id = ?`, userID, modeID)
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
		categoryID, err := EnsureCategoryID(ctx, tx, category)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO selected_categories(user_id, mode_id, category_id) VALUES (?, ?, ?)",
			userID, modeID, categoryID); err != nil {
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
		serviceID, err := EnsureServiceID(ctx, tx, service.Category, service.Service)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO selected_services(user_id, mode_id, service_id) VALUES (?, ?, ?)",
			userID, modeID, serviceID); err != nil {
			return err
		}
	}
	return nil
}

// ToggleSelectedCategory adds or removes a single category selection.
func ToggleSelectedCategory(ctx context.Context, tx *sql.Tx, userID, modeID int64, category string, checked bool) error {
	if checked {
		categoryID, err := EnsureCategoryID(ctx, tx, category)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx,
			"INSERT OR IGNORE INTO selected_categories(user_id, mode_id, category_id) VALUES (?, ?, ?)",
			userID, modeID, categoryID)
		return err
	}
	_, err := tx.ExecContext(ctx, `
DELETE FROM selected_categories
WHERE user_id = ? AND mode_id = ?
  AND category_id = (SELECT id FROM categories WHERE name = ?)`,
		userID, modeID, category)
	return err
}

// ToggleSelectedService adds or removes a single service selection.
func ToggleSelectedService(ctx context.Context, tx *sql.Tx, userID, modeID int64, category, service string, checked bool) error {
	if checked {
		serviceID, err := EnsureServiceID(ctx, tx, category, service)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx,
			"INSERT OR IGNORE INTO selected_services(user_id, mode_id, service_id) VALUES (?, ?, ?)",
			userID, modeID, serviceID)
		return err
	}
	_, err := tx.ExecContext(ctx, `
DELETE FROM selected_services
WHERE user_id = ? AND mode_id = ?
  AND service_id = (
      SELECT s.id FROM services s
      JOIN categories c ON c.id = s.category_id
      WHERE c.name = ? AND s.name = ?)`,
		userID, modeID, category, service)
	return err
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
	hiddenCategories, err := hiddenSelectedCategories(ctx, tx, userID, modeID)
	if err != nil {
		return err
	}
	categories = append(categories, hiddenCategories...)

	hiddenServices, err := hiddenSelectedServices(ctx, tx, userID, modeID)
	if err != nil {
		return err
	}
	services = append(services, hiddenServices...)

	return SetUserModeSelection(ctx, tx, userID, modeID,
		uniqueStrings(categories), uniqueServices(services))
}

// hiddenSelectedCategories returns categories the user has selected whose
// entries are now entirely hidden (feed disabled) — these must be preserved
// across a visible-selection overwrite rather than dropped.
func hiddenSelectedCategories(ctx context.Context, tx *sql.Tx, userID, modeID int64) (categories []string, err error) {
	rows, err := tx.QueryContext(ctx, `
SELECT c.name
FROM selected_categories sc
JOIN categories c ON c.id = sc.category_id
WHERE sc.user_id = ? AND sc.mode_id = ?
  AND EXISTS (
      SELECT 1
      FROM catalog_entries ce
      JOIN services sv ON sv.id = ce.service_id
      JOIN feeds f ON f.id = ce.feed_id
      JOIN catalog_mode_feeds cmf ON cmf.feed_id = f.id
      WHERE sv.category_id = sc.category_id AND cmf.mode_id = sc.mode_id AND cmf.exclude = 0 AND f.enabled = 0
  )
  AND NOT EXISTS (
      SELECT 1
      FROM catalog_entries ce
      JOIN services sv ON sv.id = ce.service_id
      JOIN feeds f ON f.id = ce.feed_id
      JOIN catalog_mode_feeds cmf ON cmf.feed_id = f.id
      WHERE sv.category_id = sc.category_id AND cmf.mode_id = sc.mode_id AND cmf.exclude = 0 AND f.enabled = 1
  )`, userID, modeID)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	for rows.Next() {
		var category string
		if err := rows.Scan(&category); err != nil {
			return nil, err
		}
		categories = append(categories, category)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return categories, nil
}

// hiddenSelectedServices is hiddenSelectedCategories' service-level
// counterpart — see its doc comment.
func hiddenSelectedServices(ctx context.Context, tx *sql.Tx, userID, modeID int64) (services []ServiceKey, err error) {
	rows, err := tx.QueryContext(ctx, `
SELECT c.name, sv.name
FROM selected_services ss
JOIN services sv ON sv.id = ss.service_id
JOIN categories c ON c.id = sv.category_id
WHERE ss.user_id = ? AND ss.mode_id = ?
  AND EXISTS (
      SELECT 1
      FROM catalog_entries ce
      JOIN feeds f ON f.id = ce.feed_id
      JOIN catalog_mode_feeds cmf ON cmf.feed_id = f.id
      WHERE ce.service_id = ss.service_id
        AND cmf.mode_id = ss.mode_id
        AND cmf.exclude = 0
        AND f.enabled = 0
  )
  AND NOT EXISTS (
      SELECT 1
      FROM catalog_entries ce
      JOIN feeds f ON f.id = ce.feed_id
      JOIN catalog_mode_feeds cmf ON cmf.feed_id = f.id
      WHERE ce.service_id = ss.service_id
        AND cmf.mode_id = ss.mode_id
        AND cmf.exclude = 0
        AND f.enabled = 1
  )`, userID, modeID)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	for rows.Next() {
		var service ServiceKey
		if err := rows.Scan(&service.Category, &service.Service); err != nil {
			return nil, err
		}
		services = append(services, service)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return services, nil
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

// SetUserCatalogModeTx sets a user's catalog_mode_id within an existing
// transaction, so callers that also need to persist selection changes in
// the same transaction don't have to duplicate this query. Returns
// sql.ErrNoRows if modeID doesn't reference an enabled catalog mode (or,
// when requireEditable, if switching from the user's current mode isn't
// allowed).
func SetUserCatalogModeTx(
	ctx context.Context,
	tx *sql.Tx,
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
	result, err := tx.ExecContext(ctx, query, args...)
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

func (s *Store) SetUserCatalogMode(
	ctx context.Context,
	userID int64,
	modeID int64,
	requireEditable bool,
) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		return SetUserCatalogModeTx(ctx, tx, userID, modeID, requireEditable)
	})
}
