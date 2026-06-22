package bgp

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/andrey-vk/wdbgp/internal/config"
	"github.com/andrey-vk/wdbgp/internal/logging"
	"github.com/andrey-vk/wdbgp/internal/store"
)

// Manager manages BGP peers and route announcements using our custom Speaker.
type Manager struct {
	cfg         config.Config
	store       *store.Store
	mu          sync.Mutex
	speaker     *Speaker
	installed   map[string]instPrefix // global prefix → signature
	peerConfigs []store.User          // all configured peers
	peerRoutes  map[string][]Route    // per-peer last announced routes (keyed by peer address string)
}

func NewManager(cfg config.Config, s *store.Store) *Manager {
	return &Manager{
		cfg:         cfg,
		store:       s,
		installed:   map[string]instPrefix{},
		peerConfigs: []store.User{},
		peerRoutes:  map[string][]Route{},
	}
}

func (m *Manager) Start(ctx context.Context) error {
	logger := logging.FromContext(ctx)
	logger.Info("starting BGP manager",
		"asn", m.cfg.LocalASN,
		"router_id", m.cfg.RouterID,
		"bgp_port", m.cfg.BGPListenPort,
	)

	m.mu.Lock()
	defer m.mu.Unlock()
	return m.startLocked(ctx)
}

func (m *Manager) Stop(ctx context.Context) error {
	logger := logging.FromContext(ctx)
	logger.Info("stopping BGP manager")

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.speaker == nil {
		logger.Debug("BGP speaker already stopped")
		return nil
	}
	err := m.speaker.Stop()
	m.speaker = nil
	m.installed = map[string]instPrefix{}
	m.peerConfigs = []store.User{}
	m.peerRoutes = map[string][]Route{}

	if err != nil {
		logger.Error("failed to stop BGP speaker", "error", err)
	} else {
		logger.Info("BGP speaker stopped successfully")
	}
	return err
}

func (m *Manager) ReloadPeers(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	savedInstalled := m.installed
	savedPeerConfigs := m.peerConfigs
	if m.speaker != nil {
		if err := m.speaker.Stop(); err != nil {
			return err
		}
	}
	m.speaker = nil
	m.installed = savedInstalled
	m.peerConfigs = savedPeerConfigs
	m.peerRoutes = make(map[string][]Route)
	return m.startLocked(ctx)
}

func (m *Manager) startLocked(ctx context.Context) error {
	logger := logging.FromContext(ctx)

	routerID, err := netip.ParseAddr(m.cfg.RouterID)
	if err != nil {
		return fmt.Errorf("parse router ID %q: %w", m.cfg.RouterID, err)
	}
	localAddr, err := netip.ParseAddr(m.cfg.LocalAddressV4)
	if err != nil {
		return fmt.Errorf("parse local address %q: %w", m.cfg.LocalAddressV4, err)
	}
	localAddrV6 := netip.Addr{}
	if m.cfg.LocalAddressV6 != "" {
		localAddrV6, err = netip.ParseAddr(m.cfg.LocalAddressV6)
		if err != nil {
			return fmt.Errorf("parse local IPv6 address %q: %w", m.cfg.LocalAddressV6, err)
		}
	}

	speaker := NewSpeaker(SpeakerConfig{
		ASN:       m.cfg.LocalASN,
		RouterID:  routerID,
		Port:      m.cfg.BGPListenPort,
		LocalAddr: localAddr,
	}, logger.Logger)

	if err := speaker.Start(ctx); err != nil {
		return fmt.Errorf("start speaker: %w", err)
	}
	started := false
	defer func() {
		if !started {
			m.speaker = nil
			_ = speaker.Stop()
		}
	}()

	m.speaker = speaker

	// Load enabled users as peer configs
	users, err := m.store.Users(ctx, true)
	if err != nil {
		return fmt.Errorf("load peers: %w", err)
	}

	logger.Info("configuring BGP peers", "peer_count", len(users))
	m.peerConfigs = make([]store.User, 0, len(users))
	var peerConfigs []PeerConfig
	for _, u := range users {
		addr, err := netip.ParseAddr(u.PeerIP)
		if err != nil {
			logger.Warn("skipping peer with invalid IP", "peer_ip", u.PeerIP, "error", err)
			continue
		}
		// Determine per-peer local bind address based on peer IP version.
		peerLocalAddr := localAddr
		if addr.Is6() && localAddrV6.IsValid() {
			peerLocalAddr = localAddrV6
		} else if addr.Is6() {
			logger.Warn("skipping IPv6 peer without local IPv6 address", "peer_ip", u.PeerIP)
			continue
		}
		peerConfigs = append(peerConfigs, PeerConfig{
			ID:        u.ID,
			Address:   addr,
			Port:      peerPort(u.PeerIP, m.cfg.ActiveDial && u.ActiveDial),
			ASN:       u.PeerASN,
			Password:  u.BGPPassword,
			Name:      u.Name,
			LocalAddr: peerLocalAddr,
		})
		m.peerConfigs = append(m.peerConfigs, u)
		logger.Debug("configured BGP peer", "peer_ip", u.PeerIP, "peer_asn", u.PeerASN)
	}

	if err := speaker.SetPeers(peerConfigs); err != nil {
		return err
	}

	if err := m.reconcileLocked(ctx); err != nil {
		return err
	}
	logger.Info("BGP manager started successfully", "peer_count", len(users))
	started = true
	return nil
}

func (m *Manager) Reconcile(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reconcileLocked(ctx)
}

func (m *Manager) AddPeer(ctx context.Context, user store.User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.speaker == nil {
		return fmt.Errorf("BGP speaker is not running")
	}
	// Check if peer already exists
	for _, u := range m.peerConfigs {
		if u.PeerIP == user.PeerIP && u.PeerASN == user.PeerASN {
			if user.PeerIP == "0.0.0.0" || user.PeerIP == "::" {
				return fmt.Errorf("dynamic peer with ASN %d already exists", user.PeerASN)
			}
			return fmt.Errorf("peer %s with ASN %d already exists", user.PeerIP, user.PeerASN)
		}
	}
	m.peerConfigs = append(m.peerConfigs, user)
	cfgs, err := m.buildPeerConfigs()
	if err != nil {
		// Rollback: remove the just-added peer from configs.
		m.peerConfigs = m.peerConfigs[:len(m.peerConfigs)-1]
		return err
	}
	if err := m.speaker.SetPeers(cfgs); err != nil {
		// Rollback: remove the just-added peer from configs.
		m.peerConfigs = m.peerConfigs[:len(m.peerConfigs)-1]
		return err
	}
	return nil
}

func (m *Manager) UpdatePeer(ctx context.Context, user store.User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.speaker == nil {
		return fmt.Errorf("BGP speaker is not running")
	}
	// Find existing peer by user ID
	found := false
	var oldPeerKey string
	var oldUser store.User
	for i, u := range m.peerConfigs {
		if u.ID == user.ID {
			// Remember old key and old user before update so we can roll back
			oldPeerKey = fmt.Sprintf("%s:%d", u.PeerIP, u.PeerASN)
			oldUser = u
			m.peerConfigs[i] = user
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("peer %s does not exist", user.PeerIP)
	}
	// Clear old peer routes when IP or ASN changed
	if oldPeerKey != "" {
		delete(m.peerRoutes, oldPeerKey)
	}
	// Clear new peer routes since the peer may have changed
	peerKey := fmt.Sprintf("%s:%d", user.PeerIP, user.PeerASN)
	if peerKey != oldPeerKey {
		delete(m.peerRoutes, peerKey)
	}
	cfgs, err := m.buildPeerConfigs()
	if err != nil {
		// Rollback: restore old peer config to avoid inconsistency.
		for i, u := range m.peerConfigs {
			if u.ID == user.ID {
				m.peerConfigs[i] = oldUser
				break
			}
		}
		return err
	}
	if err := m.speaker.SetPeers(cfgs); err != nil {
		// Rollback: restore old peer config and route caches.
		for i, u := range m.peerConfigs {
			if u.ID == user.ID {
				m.peerConfigs[i] = oldUser
				break
			}
		}
		if oldPeerKey != "" {
			m.peerRoutes[oldPeerKey] = nil // placeholder for reconcile to repopulate
		}
		if peerKey != oldPeerKey {
			delete(m.peerRoutes, peerKey)
		}
		return err
	}
	return nil
}

func (m *Manager) DeletePeer(ctx context.Context, peerIP string, userID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.speaker == nil {
		return fmt.Errorf("BGP speaker is not running")
	}
	// Find exact peer by user ID (not just IP) to avoid deleting wrong
	// same-IP different-ASN peer.
	idx := -1
	for i, u := range m.peerConfigs {
		if u.ID == userID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("peer %s with user ID %d does not exist", peerIP, userID)
	}

	removed := m.peerConfigs[idx]

	// Remove from configs
	m.peerConfigs = append(m.peerConfigs[:idx], m.peerConfigs[idx+1:]...)

	// Clean up peer routes using ip:asn key (matches reconcileLocked key format).
	peerKey := fmt.Sprintf("%s:%d", removed.PeerIP, removed.PeerASN)
	delete(m.peerRoutes, peerKey)

	cfgs, err := m.buildPeerConfigs()
	if err != nil {
		return err
	}
	if err := m.speaker.SetPeers(cfgs); err != nil {
		return err
	}
	return nil
}

func (m *Manager) PeerStates(ctx context.Context) (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.speaker == nil {
		return map[string]string{}, nil
	}
	return m.speaker.PeerStates(), nil
}

// buildPeerConfigs converts m.peerConfigs (store.User) to []PeerConfig.
// Returns an error if any peer is invalid (e.g., IPv6 peer with no LocalAddressV6).
func (m *Manager) buildPeerConfigs() ([]PeerConfig, error) {
	var configs []PeerConfig
	localAddr, err := netip.ParseAddr(m.cfg.LocalAddressV4)
	if err != nil {
		return configs, err
	}
	var localAddrV6 netip.Addr
	if m.cfg.LocalAddressV6 != "" {
		localAddrV6, err = netip.ParseAddr(m.cfg.LocalAddressV6)
		if err != nil {
			return configs, fmt.Errorf("parse local IPv6 address %q: %w", m.cfg.LocalAddressV6, err)
		}
	}
	for _, u := range m.peerConfigs {
		addr, err := netip.ParseAddr(u.PeerIP)
		if err != nil {
			continue
		}
		peerLocalAddr := localAddr
		if addr.Is6() && localAddrV6.IsValid() {
			peerLocalAddr = localAddrV6
		} else if addr.Is6() {
			return nil, fmt.Errorf("IPv6 peer %s AS%d requires LocalAddressV6 but none configured", u.PeerIP, u.PeerASN)
		}
		configs = append(configs, PeerConfig{
			ID:        u.ID,
			Address:   addr,
			Port:      peerPort(u.PeerIP, m.cfg.ActiveDial && u.ActiveDial),
			ASN:       u.PeerASN,
			Password:  u.BGPPassword,
			Name:      u.Name,
			LocalAddr: peerLocalAddr,
		})
	}
	return configs, nil
}

func (m *Manager) reconcileLocked(ctx context.Context) error {
	logger := logging.FromContext(ctx)
	if m.speaker == nil {
		return fmt.Errorf("BGP speaker is not running")
	}
	desired, prefixMeta, err := m.store.DesiredPrefixes(ctx)
	if err != nil {
		return err
	}
	for rawPrefix := range desired {
		prefix, err := netip.ParsePrefix(rawPrefix)
		if err != nil {
			return fmt.Errorf("parse desired prefix %q: %w", rawPrefix, err)
		}
		if prefix.Addr().Is6() && m.cfg.LocalAddressV6 == "" {
			delete(desired, rawPrefix)
		}
	}

	// Load communities for every mode seen across prefixes.
	modeCommunities := make(map[int64]map[string]uint32)
	for _, info := range prefixMeta {
		if _, ok := modeCommunities[info.ModeID]; !ok {
			comms, err := m.store.GetCommunities(ctx, info.ModeID)
			if err != nil {
				logger.Warn("get communities failed", "mode", info.ModeID, "error", err)
			}
			modeCommunities[info.ModeID] = comms
		}
	}

	// Track global prefix signatures for change detection
	newInstalled := make(map[string]instPrefix)
	for prefix, userIDs := range desired {
		newInstalled[prefix] = instPrefix{Signature: signature(userIDs)}
	}

	// For each enabled peer, compute desired routes and reconcile
	for _, user := range m.peerConfigs {
		if !user.Enabled {
			continue
		}
		addr, err := netip.ParseAddr(user.PeerIP)
		if err != nil {
			continue
		}

		// Build desired routes for this peer
		var desiredRoutes []Route
		for rawPrefix, userIDs := range desired {
			if !containsID(userIDs, user.ID) {
				continue
			}
			prefix, err := netip.ParsePrefix(rawPrefix)
			if err != nil {
				continue // skip invalid prefix, already logged elsewhere
			}
			metaKey := rawPrefix + ":" + strconv.FormatInt(user.ID, 10)
			meta, hasMeta := prefixMeta[metaKey]
			comms := map[string]uint32{}
			if hasMeta {
				comms = modeCommunities[meta.ModeID]
			}
			route, err := m.buildRoute(prefix, user, meta.Category, meta.Service, comms)
			if err != nil {
				return err
			}
			desiredRoutes = append(desiredRoutes, route)
		}

		// Sort for stable comparison
		sortRoutes(desiredRoutes)

		// Use "addr:asn" as the peer routes key for multi-peer-per-IP support
		peerKey := fmt.Sprintf("%s:%d", user.PeerIP, user.PeerASN)

		// Compare with previously announced
		prevRoutes := m.peerRoutes[peerKey]
		if routesEqual(prevRoutes, desiredRoutes) {
			continue
		}

		// Compute withdrawals (prefixes in prev but not in desired)
		desiredSet := routePrefixSet(desiredRoutes)
		var toWithdraw []netip.Prefix
		for _, r := range prevRoutes {
			if !desiredSet[r.Prefix.String()] {
				toWithdraw = append(toWithdraw, r.Prefix)
			}
		}

		if len(toWithdraw) > 0 {
			if err := m.speaker.Withdraw(addr, user.PeerASN, toWithdraw); err != nil {
				return fmt.Errorf("withdraw from %s: %w", user.PeerIP, err)
			}
		}

		if len(desiredRoutes) > 0 {
			if err := m.speaker.Announce(addr, user.PeerASN, desiredRoutes); err != nil {
				return fmt.Errorf("announce to %s: %w", user.PeerIP, err)
			}
		} else {
			// No routes — ensure peer has empty routes
			if err := m.speaker.Announce(addr, user.PeerASN, nil); err != nil {
				return fmt.Errorf("clear routes for %s: %w", user.PeerIP, err)
			}
		}

		m.peerRoutes[peerKey] = desiredRoutes
	}

	m.installed = newInstalled
	return nil
}

func signature(userIDs []int64) string {
	sorted := append([]int64(nil), userIDs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	parts := make([]string, len(sorted))
	for i, id := range sorted {
		parts[i] = strconv.FormatInt(id, 10)
	}
	return strings.Join(parts, ",")
}

func containsID(ids []int64, target int64) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}

// sortRoutes sorts routes by prefix string for stable comparison.
func sortRoutes(routes []Route) {
	sort.Slice(routes, func(i, j int) bool {
		return routes[i].Prefix.String() < routes[j].Prefix.String()
	})
}

func routesEqual(a, b []Route) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Prefix != b[i].Prefix {
			return false
		}
		if !communitiesEqual(a[i].Communities, b[i].Communities) {
			return false
		}
		if a[i].NextHop != b[i].NextHop {
			return false
		}
	}
	return true
}

func communitiesEqual(a, b []LargeCommunity) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func routePrefixSet(routes []Route) map[string]bool {
	set := make(map[string]bool, len(routes))
	for _, r := range routes {
		set[r.Prefix.String()] = true
	}
	return set
}

// peerPort returns the destination port for a peer connection.
// Dynamic peers (0.0.0.0/::) are passive-only: Port=-1 prevents active dialing.
// When activeDial is false, the peer is also passive-only (Port=-1).
// All other peers use Port=0 (defaults to 179 in connectAndRun).
func peerPort(peerIP string, activeDial bool) int32 {
	if peerIP == "0.0.0.0" || peerIP == "::" {
		return -1
	}
	if !activeDial {
		return -1
	}
	return 0
}
