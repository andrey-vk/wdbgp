package bgp

import (
	"fmt"
	"net/netip"

	"github.com/andrey-vk/wdbgp/internal/store"
)

// instPrefix tracks a globally installed prefix and its routing signature.
type instPrefix struct {
	Signature string
}

// buildRoute creates a Route for a single prefix for a specific peer.
func (m *Manager) buildRoute(prefix netip.Prefix, user store.User, category, service string, communities map[string]uint32) (Route, error) {
	// Guard against user IDs that overflow uint32 (community LocalData1 field).
	if user.ID > int64(^uint32(0)) {
		return Route{}, fmt.Errorf("user ID %d exceeds max uint32", user.ID)
	}

	// m.localASN is a snapshot taken when the speaker (re)started — see the
	// Manager field comments. Using the live setting here would embed a new
	// ASN into announced communities before the session actually restarts.
	globalAdmin := m.localASN

	comms := make([]LargeCommunity, 0, 3)
	// User ID community
	comms = append(comms, LargeCommunity{
		GlobalAdmin: globalAdmin,
		LocalData1:  uint32(user.ID), //nolint:gosec // user IDs are within uint32 range in practice
		LocalData2:  0,
	})
	// Category and service communities if available
	if category != "" {
		if c, ok := communities[category]; ok {
			comms = append(comms, LargeCommunity{
				GlobalAdmin: globalAdmin, LocalData1: 0, LocalData2: c,
			})
		}
		if service != "" {
			if c, ok := communities[category+"|"+service]; ok {
				comms = append(comms, LargeCommunity{
					GlobalAdmin: globalAdmin, LocalData1: 0, LocalData2: c,
				})
			}
		}
	}

	// Determine next hop
	nextHop := m.localAddrV4
	if prefix.Addr().Is6() {
		if !m.localAddrV6.IsValid() {
			return Route{}, fmt.Errorf("cannot build IPv6 route %s without local IPv6 address", prefix)
		}
		nextHop = m.localAddrV6
	}
	if user.NextHop != "" {
		// Only apply user's next-hop override if it matches the prefix family
		if userNH, parseErr := netip.ParseAddr(user.NextHop); parseErr == nil && userNH.Is4() == prefix.Addr().Is4() {
			nextHop = userNH
		}
	}

	return Route{
		Prefix:      prefix,
		Communities: comms,
		NextHop:     nextHop,
	}, nil
}
