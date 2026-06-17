package bgp

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"

	"log/slog"

	"github.com/google/uuid"

	api "github.com/osrg/gobgp/v4/api"
	"github.com/osrg/gobgp/v4/pkg/apiutil"
	"github.com/osrg/gobgp/v4/pkg/packet/bgp"
	"github.com/osrg/gobgp/v4/pkg/server"

	"github.com/andrey-vk/wdbgp/internal/config"
	"github.com/andrey-vk/wdbgp/internal/logging"
	"github.com/andrey-vk/wdbgp/internal/retry"
	"github.com/andrey-vk/wdbgp/internal/store"
)

type installedPath struct {
	UUID      uuid.UUID
	Signature string
}

type Manager struct {
	cfg        config.Config
	store      *store.Store
	mu         sync.Mutex
	server     *server.BgpServer
	installed  map[string]installedPath
	peerConfigs []store.User // all configured peers (supports multiple per IP, dynamic 0.0.0.0)
}

const (
	globalPolicyTable = "global"
	exportPolicyName  = "wdbgp_export"
)

func NewManager(cfg config.Config, s *store.Store) *Manager {
	return &Manager{
		cfg:        cfg,
		store:      s,
		installed:  map[string]installedPath{},
		peerConfigs: []store.User{},
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
	if m.server == nil {
		logger.Debug("BGP server already stopped")
		return nil
	}
	err := m.server.StopBgp(ctx, &api.StopBgpRequest{})
	m.server = nil
	m.installed = map[string]installedPath{}
	m.peerConfigs = []store.User{}
	
	if err != nil {
		logger.Error("failed to stop BGP server", "error", err)
	} else {
		logger.Info("BGP server stopped successfully")
	}
	return err
}

func (m *Manager) ReloadPeers(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Save state before restart
	savedInstalled := m.installed
	savedPeerConfigs := m.peerConfigs
	if m.server != nil {
		if err := m.server.StopBgp(ctx, &api.StopBgpRequest{}); err != nil {
			return err
		}
	}
	m.server = nil
	m.installed = savedInstalled // Restore installed routes
	m.peerConfigs = savedPeerConfigs // Restore peer configs
	return m.startLocked(ctx)
}

func (m *Manager) startLocked(ctx context.Context) error {
	logger := logging.FromContext(ctx)
	
	levelVar := &slog.LevelVar{}
	levelVar.Set(slog.LevelInfo)
	bgpServer := server.NewBgpServer(server.LoggerOption(slog.Default(), levelVar))
	go bgpServer.Serve()
	if err := bgpServer.StartBgp(ctx, &api.StartBgpRequest{
		Global: &api.Global{
			Asn:        m.cfg.LocalASN,
			RouterId:   m.cfg.RouterID,
			ListenPort: m.cfg.BGPListenPort,
		},
	}); err != nil {
		logger.Error("failed to start BGP server", "error", err)
		return err
	}
	logger.Debug("BGP server started")
	
	started := false
	defer func() {
		if !started {
			logger.Debug("cleaning up BGP server after failed initialization")
			_ = bgpServer.StopBgp(context.Background(), &api.StopBgpRequest{})
		}
	}()
	m.server = bgpServer
	users, err := m.store.Users(ctx, true)
	if err != nil {
		m.server = nil
		logger.Error("failed to get users from store", "error", err)
		return err
	}
	
	logger.Info("configuring BGP peers", "peer_count", len(users))
	// Store peer configs for later updates
	m.peerConfigs = make([]store.User, 0, len(users))
	for _, user := range users {
		if err := m.addPeerLocked(ctx, user); err != nil {
			m.server = nil
			logger.Error("failed to add BGP peer", "peer_ip", user.PeerIP, "error", err)
			return fmt.Errorf("add peer %s: %w", user.PeerIP, err)
		}
		m.peerConfigs = append(m.peerConfigs, user)
		logger.Debug("added BGP peer", "peer_ip", user.PeerIP, "peer_asn", user.PeerASN)
	}
	if err := m.configureGlobalPolicyLocked(ctx, users); err != nil {
		m.server = nil
		logger.Error("failed to configure global policy", "error", err)
		return err
	}
	logger.Debug("global policy configured")
	
	if err := m.reconcileLocked(ctx); err != nil {
		m.server = nil
		logger.Error("failed to reconcile routes", "error", err)
		return err
	}
	logger.Info("BGP manager started successfully", "peer_count", len(users))
	started = true
	return nil
}

func (m *Manager) addPeerLocked(ctx context.Context, user store.User) error {
	communitySetName := userCommunitySetName(user.ID)
	community := largeCommunity(m.cfg.LocalASN, user.ID)
	if err := m.server.AddDefinedSet(ctx, &api.AddDefinedSetRequest{
		DefinedSet: &api.DefinedSet{
			DefinedType: api.DefinedType_DEFINED_TYPE_LARGE_COMMUNITY,
			Name:        communitySetName,
			List:        []string{community},
		},
		Replace: true,
	}); err != nil {
		return err
	}
	neighborSet, err := neighborDefinedSet(userNeighborSetName(user.ID), user.PeerIP)
	if err != nil {
		return err
	}
	if err := m.server.AddDefinedSet(ctx, &api.AddDefinedSetRequest{
		DefinedSet: neighborSet,
		Replace:    true,
	}); err != nil {
		return err
	}
	peerAddress, err := netip.ParseAddr(user.PeerIP)
	if err != nil {
		return err
	}
	localAddress := m.cfg.LocalAddressV4
	if peerAddress.Is6() {
		localAddress = m.cfg.LocalAddressV6
	}
	if localAddress == "" {
		return fmt.Errorf("no local BGP address configured for peer family")
	}
	return m.server.AddPeer(ctx, &api.AddPeerRequest{Peer: &api.Peer{
		Conf: &api.PeerConf{
			NeighborAddress: user.PeerIP,
			PeerAsn:         user.PeerASN,
			AuthPassword:    user.BGPPassword,
			Description:     user.Name,
		},
		Transport: &api.Transport{LocalAddress: localAddress},
		EbgpMultihop: &api.EbgpMultihop{
			Enabled:     true,
			MultihopTtl: 64,
		},
		AfiSafis: []*api.AfiSafi{
			{Config: &api.AfiSafiConfig{Family: ipv4Family(), Enabled: true}},
			{Config: &api.AfiSafiConfig{Family: ipv6Family(), Enabled: true}},
		},
	}})
}

func (m *Manager) deletePeerLocked(ctx context.Context, userID int64, peerIP string) error {
	// Find user by userID AND peerIP
	var user store.User
	for _, u := range m.peerConfigs {
		if u.ID == userID && u.PeerIP == peerIP {
			user = u
			break
		}
	}
	if user.ID == 0 {
		return fmt.Errorf("user %d not found for peer %s", userID, peerIP)
	}
	// Delete defined sets
	communitySetName := userCommunitySetName(user.ID)
	if err := m.server.DeleteDefinedSet(ctx, &api.DeleteDefinedSetRequest{
		DefinedSet: &api.DefinedSet{
			DefinedType: api.DefinedType_DEFINED_TYPE_LARGE_COMMUNITY,
			Name:        communitySetName,
		},
	}); err != nil {
		return err
	}
	neighborSetName := userNeighborSetName(user.ID)
	if err := m.server.DeleteDefinedSet(ctx, &api.DeleteDefinedSetRequest{
		DefinedSet: &api.DefinedSet{
			DefinedType: api.DefinedType_DEFINED_TYPE_NEIGHBOR,
			Name:        neighborSetName,
		},
	}); err != nil {
		return err
	}
	// Delete the peer
	return m.server.DeletePeer(ctx, &api.DeletePeerRequest{
		Address: peerIP,
	})
}

func (m *Manager) configureGlobalPolicyLocked(ctx context.Context, users []store.User) error {
	statements := make([]*api.Statement, 0, len(users)*2)
	for _, user := range users {
		for _, family := range []api.Family_Afi{api.Family_AFI_IP, api.Family_AFI_IP6} {
			nextHop, err := nextHopAction(user, family)
			if err != nil {
				return err
			}
			statements = append(statements, &api.Statement{
				Name: fmt.Sprintf("export_user_%d_%s", user.ID, strings.ToLower(family.String())),
				Conditions: &api.Conditions{
					NeighborSet: &api.MatchSet{
						Name: userNeighborSetName(user.ID),
						Type: api.MatchSet_TYPE_ANY,
					},
					LargeCommunitySet: &api.MatchSet{
						Name: userCommunitySetName(user.ID),
						Type: api.MatchSet_TYPE_ANY,
					},
				},
				Actions: &api.Actions{
					RouteAction: api.RouteAction_ROUTE_ACTION_ACCEPT,
					LargeCommunity: &api.CommunityAction{
						Type:        api.CommunityAction_TYPE_REPLACE,
						Communities: []string{largeCommunity(m.cfg.LocalASN, user.ID)},
					},
					Nexthop: nextHop,
				},
			})
		}
	}
	if err := m.server.AddPolicy(ctx, &api.AddPolicyRequest{
		Policy: &api.Policy{Name: exportPolicyName, Statements: statements},
	}); err != nil {
		return err
	}
	return m.server.SetPolicyAssignment(ctx, &api.SetPolicyAssignmentRequest{
		Assignment: &api.PolicyAssignment{
			Name:          globalPolicyTable,
			Direction:     api.PolicyDirection_POLICY_DIRECTION_EXPORT,
			Policies:      []*api.Policy{{Name: exportPolicyName}},
			DefaultAction: api.RouteAction_ROUTE_ACTION_REJECT,
		},
	})
}

func (m *Manager) Reconcile(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reconcileLocked(ctx)
}

func (m *Manager) AddPeer(ctx context.Context, user store.User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.server == nil {
		return fmt.Errorf("BGP server is not running")
	}
	// Check if peer already exists (match by IP+ASN for non-dynamic, or by ASN+password for dynamic)
	for _, u := range m.peerConfigs {
		if u.PeerIP == user.PeerIP && u.PeerASN == user.PeerASN {
			// For dynamic peers (0.0.0.0), also check password
			if user.PeerIP == "0.0.0.0" && u.BGPPassword == user.BGPPassword {
				return fmt.Errorf("dynamic peer with ASN %d already exists", user.PeerASN)
			}
			if user.PeerIP != "0.0.0.0" {
				return fmt.Errorf("peer %s with ASN %d already exists", user.PeerIP, user.PeerASN)
			}
		}
	}
	// Add the peer
	if err := m.addPeerLocked(ctx, user); err != nil {
		return err
	}
	// Store the config
	m.peerConfigs = append(m.peerConfigs, user)
	// Update global policy to include new peer
	users := m.peerConfigs
	return m.configureGlobalPolicyLocked(ctx, users)
}

func (m *Manager) UpdatePeer(ctx context.Context, user store.User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.server == nil {
		return fmt.Errorf("BGP server is not running")
	}
	// Find existing peer by user ID (IP may have changed)
	var oldUser store.User
	found := false
	for _, u := range m.peerConfigs {
		if u.ID == user.ID {
			oldUser = u
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("peer %s does not exist", user.PeerIP)
	}
	// If peer IP changed, we need to delete old peer and add new one
	if oldUser.PeerIP != user.PeerIP {
		// Delete old peer
		if err := m.deletePeerLocked(ctx, oldUser.ID, oldUser.PeerIP); err != nil {
			return fmt.Errorf("delete old peer %s: %w", oldUser.PeerIP, err)
		}
		// Remove old user from slice
		for i, u := range m.peerConfigs {
			if u.ID == user.ID {
				m.peerConfigs = append(m.peerConfigs[:i], m.peerConfigs[i+1:]...)
				break
			}
		}
		// Add new peer
		if err := m.addPeerLocked(ctx, user); err != nil {
			return err
		}
		m.peerConfigs = append(m.peerConfigs, user)
	} else {
		// Update existing peer using GoBGP's UpdatePeer API
		peerAddress, err := netip.ParseAddr(user.PeerIP)
		if err != nil {
			return err
		}
		localAddress := m.cfg.LocalAddressV4
		if peerAddress.Is6() {
			localAddress = m.cfg.LocalAddressV6
		}
		if localAddress == "" {
			return fmt.Errorf("no local BGP address configured for peer family")
		}
		// Update the peer
		_, updateErr := m.server.UpdatePeer(ctx, &api.UpdatePeerRequest{
			Peer: &api.Peer{
				Conf: &api.PeerConf{
					NeighborAddress: user.PeerIP,
					PeerAsn:         user.PeerASN,
					AuthPassword:    user.BGPPassword,
					Description:     user.Name,
				},
				Transport: &api.Transport{LocalAddress: localAddress},
				EbgpMultihop: &api.EbgpMultihop{
					Enabled:     true,
					MultihopTtl: 64,
				},
				AfiSafis: []*api.AfiSafi{
					{Config: &api.AfiSafiConfig{Family: ipv4Family(), Enabled: true}},
					{Config: &api.AfiSafiConfig{Family: ipv6Family(), Enabled: true}},
				},
			},
		})
		if updateErr != nil {
			return updateErr
		}
		// Update defined sets if needed
		if oldUser.ID != user.ID {
			// Update community set
			oldCommunitySetName := userCommunitySetName(oldUser.ID)
			newCommunitySetName := userCommunitySetName(user.ID)
			newCommunity := largeCommunity(m.cfg.LocalASN, user.ID)
			
			// Delete old community set
			if err := m.server.DeleteDefinedSet(ctx, &api.DeleteDefinedSetRequest{
				DefinedSet: &api.DefinedSet{
					DefinedType: api.DefinedType_DEFINED_TYPE_LARGE_COMMUNITY,
					Name:        oldCommunitySetName,
				},
			}); err != nil {
				return err
			}
			
			// Add new community set
			if err := m.server.AddDefinedSet(ctx, &api.AddDefinedSetRequest{
				DefinedSet: &api.DefinedSet{
					DefinedType: api.DefinedType_DEFINED_TYPE_LARGE_COMMUNITY,
					Name:        newCommunitySetName,
					List:        []string{newCommunity},
				},
				Replace: true,
			}); err != nil {
				return err
			}

			// Update neighbor set
			oldNeighborSetName := userNeighborSetName(oldUser.ID)
			newNeighborSetName := userNeighborSetName(user.ID)
			newNeighborSet, err := neighborDefinedSet(newNeighborSetName, user.PeerIP)
			if err != nil {
				return err
			}

			// Delete old neighbor set
			if err := m.server.DeleteDefinedSet(ctx, &api.DeleteDefinedSetRequest{
				DefinedSet: &api.DefinedSet{
					DefinedType: api.DefinedType_DEFINED_TYPE_NEIGHBOR,
					Name:        oldNeighborSetName,
				},
			}); err != nil {
				return err
			}

			// Add new neighbor set
			if err := m.server.AddDefinedSet(ctx, &api.AddDefinedSetRequest{
				DefinedSet: newNeighborSet,
				Replace:    true,
			}); err != nil {
				return err
			}
		}
		// Store updated config
		for i, u := range m.peerConfigs {
			if u.ID == user.ID {
				m.peerConfigs[i] = user
				break
			}
		}
	}
	// Update global policy
	users := m.peerConfigs
	return m.configureGlobalPolicyLocked(ctx, users)
}

func (m *Manager) DeletePeer(ctx context.Context, userID int64, peerIP string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.server == nil {
		return fmt.Errorf("BGP server is not running")
	}
	// Check if peer exists — match by userID+peerIP
	found := false
	for _, u := range m.peerConfigs {
		if u.ID == userID && u.PeerIP == peerIP {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("peer %s (user %d) does not exist", peerIP, userID)
	}
	// Delete the peer
	if err := m.deletePeerLocked(ctx, userID, peerIP); err != nil {
		return err
	}
	// Remove from configs — match by userID+peerIP
	for i, u := range m.peerConfigs {
		if u.ID == userID && u.PeerIP == peerIP {
			m.peerConfigs = append(m.peerConfigs[:i], m.peerConfigs[i+1:]...)
			break
		}
	}
	// Update global policy
	users := m.peerConfigs
	return m.configureGlobalPolicyLocked(ctx, users)
}

// findPeer matches an incoming BGP session to a user.
//
// Step 1: exact IP match — if exactly one user matches, verify password if set.
// Step 2: multiple IP matches — narrow by ASN, then verify password if set.
// Step 3: dynamic (IP 0.0.0.0) — match by ASN + password (password mandatory).
func (m *Manager) findPeer(peerIP string, peerASN uint32, password string) (store.User, bool) {
	// Step 1: exact IP match
	var matches []store.User
	for _, u := range m.peerConfigs {
		if u.Enabled && u.PeerIP == peerIP {
			matches = append(matches, u)
		}
	}

	if len(matches) == 1 {
		if matches[0].BGPPassword == "" || matches[0].BGPPassword == password {
			return matches[0], true
		}
		return store.User{}, false
	}

	if len(matches) > 1 {
		// Step 2: multiple IP matches — narrow by ASN
		for _, u := range matches {
			if uint32(u.PeerASN) == peerASN {
				if u.BGPPassword == "" || u.BGPPassword == password {
					return u, true
				}
			}
		}
		return store.User{}, false
	}

	// Step 3: dynamic (IP 0.0.0.0) — match by ASN + password
	for _, u := range m.peerConfigs {
		if u.Enabled && u.PeerIP == "0.0.0.0" && uint32(u.PeerASN) == peerASN && u.BGPPassword != "" && u.BGPPassword == password {
			return u, true
		}
	}

	return store.User{}, false
}

func (m *Manager) reconcileLocked(ctx context.Context) error {
	if m.server == nil {
		return fmt.Errorf("BGP server is not running")
	}
	desired, prefixMeta, err := m.store.DesiredPrefixes(ctx)
	if err != nil {
		return err
	}
	for rawPrefix := range desired {
		prefix, err := netip.ParsePrefix(splitCompoundKey(rawPrefix))
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
			comms, _ := m.store.GetCommunities(ctx, info.ModeID)
			modeCommunities[info.ModeID] = comms
		}
	}

	// Merge desired by actual prefix (not compound key) so that identical
	// prefixes from different modes share one NLRI with merged communities.
	type mergedRoute struct {
		userIDs  []int64
		comms    map[string]uint32
		category string
		service  string
	}
	perPrefix := map[string]*mergedRoute{}

	for rawPrefix, users := range desired {
		actualPrefix := splitCompoundKey(rawPrefix)
		mr, ok := perPrefix[actualPrefix]
		if !ok {
			mr = &mergedRoute{comms: map[string]uint32{}}
			perPrefix[actualPrefix] = mr
		}
		// Deduplicate user IDs across modes.
		seen := map[int64]bool{}
		for _, u := range mr.userIDs {
			seen[u] = true
		}
		for _, u := range users {
			if !seen[u] {
				mr.userIDs = append(mr.userIDs, u)
				seen[u] = true
			}
		}
		// Merge communities from this mode's modeID.
		if meta, hasMeta := prefixMeta[rawPrefix]; hasMeta {
			if modeComms, ok := modeCommunities[meta.ModeID]; ok {
				for k, v := range modeComms {
					mr.comms[k] = v
				}
			}
			if mr.category == "" && meta.Category != "" {
				mr.category = meta.Category
				mr.service = meta.Service
			}
		}
	}

	// Use retry for BGP operations — withdraw stale entries.
	for prefix, installed := range m.installed {
		mr, exists := perPrefix[prefix]
		sig := ""
		if exists {
			sig = signature(mr.userIDs)
		}
		if exists && sig == installed.Signature {
			continue
		}

		err := retry.Do(ctx, retry.BGPConfig,
			func() error {
			return m.server.DeletePath(apiutil.DeletePathRequest{
				UUIDs: []uuid.UUID{installed.UUID},
			})
			},
			retry.TransientError,
		)

		if err != nil {
			return fmt.Errorf("withdraw %s: %w", prefix, err)
		}
		delete(m.installed, prefix)
	}

	// Announce one NLRI per unique prefix with merged communities.
	for actualPrefix, mr := range perPrefix {
		sig := signature(mr.userIDs)
		if installed, ok := m.installed[actualPrefix]; ok && installed.Signature == sig {
			continue
		}

		path, err := m.path(actualPrefix, mr.userIDs, mr.category, mr.service, mr.comms)
		if err != nil {
			return err
		}

		responses, err := retry.DoWithResult(ctx, retry.BGPConfig,
			func() ([]apiutil.AddPathResponse, error) {
			return m.server.AddPath(apiutil.AddPathRequest{
				Paths: []*apiutil.Path{path},
			})
			},
			retry.TransientError,
		)

		if err != nil {
			return fmt.Errorf("announce %s: %w", actualPrefix, err)
		}
		if len(responses) == 0 || responses[0].UUID == uuid.Nil {
			return fmt.Errorf("announce %s: no UUID returned", actualPrefix)
		}
		m.installed[actualPrefix] = installedPath{UUID: responses[0].UUID, Signature: sig}
	}
	return nil
}

func (m *Manager) path(rawPrefix string, userIDs []int64, category, service string, communities map[string]uint32) (*apiutil.Path, error) {
	prefix, err := netip.ParsePrefix(rawPrefix)
	if err != nil {
		return nil, err
	}
	nlri, err := bgp.NewIPAddrPrefix(prefix)
	if err != nil {
		return nil, err
	}
	origin := bgp.NewPathAttributeOrigin(0)
	comms := make([]*bgp.LargeCommunity, 0, len(userIDs)+2)
	for _, userID := range userIDs {
		if userID < 0 || userID > int64(^uint32(0)) {
			return nil, fmt.Errorf("user id %d is outside large community range", userID)
		}
		comms = append(comms, &bgp.LargeCommunity{
			ASN:        m.cfg.LocalASN,
			LocalData1: uint32(userID),
			LocalData2: 0,
		})
	}
	// Attach category and service communities if available.
	if category != "" {
		if c, ok := communities[category]; ok {
			comms = append(comms, &bgp.LargeCommunity{
				ASN: m.cfg.LocalASN, LocalData1: 0, LocalData2: c,
			})
		}
		if service != "" {
			if c, ok := communities[category+"|"+service]; ok {
				comms = append(comms, &bgp.LargeCommunity{
					ASN: m.cfg.LocalASN, LocalData1: 0, LocalData2: c,
				})
			}
		}
	}
	communityAttribute := bgp.NewPathAttributeLargeCommunities(comms)
	if prefix.Addr().Is4() {
		nextHop, err := bgp.NewPathAttributeNextHop(netip.MustParseAddr(m.cfg.LocalAddressV4))
		if err != nil {
			return nil, err
		}
		return &apiutil.Path{
			Nlri:   nlri,
			Family: bgp.RF_IPv4_UC,
			Attrs:  []bgp.PathAttributeInterface{origin, nextHop, communityAttribute},
		}, nil
	}
	if m.cfg.LocalAddressV6 == "" {
		return nil, fmt.Errorf("cannot announce IPv6 prefix %s without WDBGP_BGP_LOCAL_ADDRESS_V6", rawPrefix)
	}
	mpReach, err := bgp.NewPathAttributeMpReachNLRI(bgp.RF_IPv6_UC, []bgp.PathNLRI{{NLRI: nlri}}, netip.MustParseAddr(m.cfg.LocalAddressV6))
	if err != nil {
		return nil, err
	}
	return &apiutil.Path{
		Nlri:   nlri,
		Family: bgp.RF_IPv6_UC,
		Attrs:  []bgp.PathAttributeInterface{origin, mpReach, communityAttribute},
	}, nil
}

func (m *Manager) PeerStates(ctx context.Context) (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	states := map[string]string{}
	if m.server == nil {
		return states, nil
	}
	err := m.server.ListPeer(ctx, &api.ListPeerRequest{}, func(peer *api.Peer) {
		if peer.Conf != nil && peer.State != nil {
			states[peer.Conf.NeighborAddress] = peer.State.SessionState.String()
		}
	})
	return states, err
}

func largeCommunity(asn uint32, userID int64) string {
	return fmt.Sprintf("%d:%d:0", asn, userID)
}

func nextHopAction(user store.User, family api.Family_Afi) (*api.NexthopAction, error) {
	nextHop := &api.NexthopAction{Self: true}
	if user.NextHop == "" {
		return nextHop, nil
	}
	address, err := netip.ParseAddr(user.NextHop)
	if err != nil {
		return nil, err
	}
	if (address.Is4() && family == api.Family_AFI_IP) ||
		(address.Is6() && family == api.Family_AFI_IP6) {
		nextHop = &api.NexthopAction{Address: address.String()}
	}
	return nextHop, nil
}

func userNeighborSetName(userID int64) string {
	return fmt.Sprintf("user_%d_neighbor", userID)
}

func userCommunitySetName(userID int64) string {
	return fmt.Sprintf("user_%d_community", userID)
}

func neighborDefinedSet(name, rawAddress string) (*api.DefinedSet, error) {
	address, err := netip.ParseAddr(rawAddress)
	if err != nil {
		return nil, err
	}
	mask := 32
	if address.Is6() {
		mask = 128
	}
	return &api.DefinedSet{
		DefinedType: api.DefinedType_DEFINED_TYPE_NEIGHBOR,
		Name:        name,
		List:        []string{fmt.Sprintf("%s/%d", address, mask)},
	}, nil
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

// splitCompoundKey extracts the prefix from a "prefix\x00modeID" compound key.
func splitCompoundKey(key string) string {
	if idx := strings.IndexByte(key, 0); idx >= 0 {
		return key[:idx]
	}
	return key
}

func buildCompoundKey(prefix string, modeID int64) string {
	return prefix + "\x00" + strconv.FormatInt(modeID, 10)
}

func ipv4Family() *api.Family {
	return &api.Family{Afi: api.Family_AFI_IP, Safi: api.Family_SAFI_UNICAST}
}

func ipv6Family() *api.Family {
	return &api.Family{Afi: api.Family_AFI_IP6, Safi: api.Family_SAFI_UNICAST}
}
