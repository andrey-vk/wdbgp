package web

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/andrey-vk/wdbgp/internal/store"
)

// validatePeerUniqueness validates a user's BGP peer configuration against
// existing peers. skipUserID is the user's own ID (0 for new users).
// Step A: Same IP + same ASN → reject (UNIQUE constraint)
// Step B: Dynamic peers (0.0.0.0 or ::) require globally unique ASN
// Step C: Shared IP + different ASN → password required when RequirePasswordForNonUniqueIP is ON
func (s *Server) validatePeerUniqueness(ctx context.Context, user store.User, skipUserID int64) error {
	// peer_ip is stored as a BLOB (schema >= 33), so comparisons need the
	// encoded form, not the string.
	peerIP, err := store.EncodeAddrString(user.PeerIP)
	if err != nil {
		return fmt.Errorf("invalid peer IP %q: %w", user.PeerIP, err)
	}
	var existingID int64
	var existingName string
	err = s.store.DB.QueryRowContext(ctx,
		"SELECT id, name FROM users WHERE peer_ip = ? AND peer_asn = ? AND id != ?",
		peerIP, user.PeerASN, skipUserID).Scan(&existingID, &existingName)
	if err == nil {
		return fmt.Errorf("peer %s with ASN %d already exists as user %s", user.PeerIP, user.PeerASN, existingName)
	}
	if err != sql.ErrNoRows {
		return fmt.Errorf("failed to check peer uniqueness: %w", err)
	}

	// Step B: Dynamic peers (0.0.0.0 or ::) require globally unique ASN
	if user.PeerIP == "0.0.0.0" || user.PeerIP == "::" {
		var count int
		err := s.store.DB.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM users WHERE peer_asn = ? AND id != ?",
			user.PeerASN, skipUserID).Scan(&count)
		if err != nil {
			return fmt.Errorf("failed to check dynamic peer uniqueness: %w", err)
		}
		if count > 0 {
			return fmt.Errorf("dynamic peer with ASN %d already exists", user.PeerASN)
		}
		return nil
	}

	// Step C: Shared IP + different ASN → require matching password
	var sharedCount int
	err = s.store.DB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM users WHERE peer_ip = ? AND id != ?",
		peerIP, skipUserID).Scan(&sharedCount)
	if err != nil {
		return fmt.Errorf("failed to check shared IP peers: %w", err)
	}
	if sharedCount > 0 {
		if s.settings.RequirePasswordForNonUniqueIP.Get() && user.BGPPassword == "" {
			return fmt.Errorf("BGP password required when sharing IP %s with another ASN", user.PeerIP)
		}
		// If new peer has password, existing same-IP peers must also have passwords
		if user.BGPPassword != "" {
			var pwLessCount int
			err = s.store.DB.QueryRowContext(ctx,
				"SELECT COUNT(*) FROM users WHERE peer_ip = ? AND id != ? AND (bgp_password = '' OR bgp_password IS NULL)",
				peerIP, skipUserID).Scan(&pwLessCount)
			if err != nil {
				return fmt.Errorf("failed to check shared IP passwords: %w", err)
			}
			if pwLessCount > 0 {
				return fmt.Errorf("cannot set BGP password on peer %s: existing peers on same IP have no password", user.PeerIP)
			}
		}
		// If any existing peer on same IP has a password, new peer's must match.
		var existingPwd string
		err = s.store.DB.QueryRowContext(ctx,
			"SELECT DISTINCT bgp_password FROM users WHERE peer_ip = ? AND id != ? AND bgp_password != ''",
			peerIP, skipUserID).Scan(&existingPwd)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("failed to check shared IP passwords: %w", err)
		}
		if existingPwd != "" && user.BGPPassword != existingPwd {
			return fmt.Errorf("BGP password must match existing peer on IP %s", user.PeerIP)
		}
	}

	return nil
}
