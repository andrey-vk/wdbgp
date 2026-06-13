package bgp

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"

	api "github.com/osrg/gobgp/v3/api"
	"github.com/osrg/gobgp/v3/pkg/server"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/andrey-vk/wdbgp/internal/config"
	"github.com/andrey-vk/wdbgp/internal/logging"
	"github.com/andrey-vk/wdbgp/internal/retry"
	"github.com/andrey-vk/wdbgp/internal/store"
)

type installedPath struct {
	UUID      []byte
	Signature string
}

type Manager struct {
	cfg        config.Config
	store      *store.Store
	mu         sync.Mutex
	server     *server.BgpServer
	installed  map[string]installedPath
	peerConfigs map[string]store.User // peerIP -> user config
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
		peerConfigs: map[string]store.User{},
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
	m.peerConfigs = map[string]store.User{}
	
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
	
	bgpServer := server.NewBgpServer()
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
	m.peerConfigs = make(map[string]store.User, len(users))
	for _, user := range users {
		if err := m.addPeerLocked(ctx, user); err != nil {
			m.server = nil
			logger.Error("failed to add BGP peer", "peer_ip", user.PeerIP, "error", err)
			return fmt.Errorf("add peer %s: %w", user.PeerIP, err)
		}
		m.peerConfigs[user.PeerIP] = user
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
			DefinedType: api.DefinedType_LARGE_COMMUNITY,
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

func (m *Manager) deletePeerLocked(ctx context.Context, peerIP string) error {
	// Find user by peerIP
	var user store.User
	for _, u := range m.peerConfigs {
		if u.PeerIP == peerIP {
			user = u
			break
		}
	}
	if user.ID == 0 {
		return fmt.Errorf("user not found for peer %s", peerIP)
	}
	// Delete defined sets
	communitySetName := userCommunitySetName(user.ID)
	if err := m.server.DeleteDefinedSet(ctx, &api.DeleteDefinedSetRequest{
		DefinedSet: &api.DefinedSet{
			DefinedType: api.DefinedType_LARGE_COMMUNITY,
			Name:        communitySetName,
		},
	}); err != nil {
		return err
	}
	neighborSetName := userNeighborSetName(user.ID)
	if err := m.server.DeleteDefinedSet(ctx, &api.DeleteDefinedSetRequest{
		DefinedSet: &api.DefinedSet{
			DefinedType: api.DefinedType_NEIGHBOR,
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
						Type: api.MatchSet_ANY,
					},
					LargeCommunitySet: &api.MatchSet{
						Name: userCommunitySetName(user.ID),
						Type: api.MatchSet_ANY,
					},
				},
				Actions: &api.Actions{
					RouteAction: api.RouteAction_ACCEPT,
					Nexthop:     nextHop,
				},
			})
		}
	}
	if err := m.server.AddPolicy(ctx, &api.AddPolicyRequest{
		Policy: &api.Policy{Name: exportPolicyName, Statements: statements},
	}); err != nil {
		return err
	}
	if err := m.server.SetPolicyAssignment(ctx, &api.SetPolicyAssignmentRequest{
		Assignment: &api.PolicyAssignment{
			Name:          globalPolicyTable,
			Direction:     api.PolicyDirection_EXPORT,
			Policies:      []*api.Policy{{Name: exportPolicyName}},
			DefaultAction: api.RouteAction_REJECT,
		},
	}); err != nil {
		return err
	}
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
	if m.server == nil {
		return fmt.Errorf("BGP server is not running")
	}
	// Check if peer already exists
	if _, exists := m.peerConfigs[user.PeerIP]; exists {
		return fmt.Errorf("peer %s already exists", user.PeerIP)
	}
	// Add the peer
	if err := m.addPeerLocked(ctx, user); err != nil {
		return err
	}
	// Store the config
	m.peerConfigs[user.PeerIP] = user
	// Update global policy to include new peer
	users := make([]store.User, 0, len(m.peerConfigs))
	for _, u := range m.peerConfigs {
		users = append(users, u)
	}
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
		if err := m.deletePeerLocked(ctx, oldUser.PeerIP); err != nil {
			return fmt.Errorf("delete old peer %s: %w", oldUser.PeerIP, err)
		}
		delete(m.peerConfigs, oldUser.PeerIP)
		// Add new peer
		if err := m.addPeerLocked(ctx, user); err != nil {
			return err
		}
		m.peerConfigs[user.PeerIP] = user
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
					DefinedType: api.DefinedType_LARGE_COMMUNITY,
					Name:        oldCommunitySetName,
				},
			}); err != nil {
				return err
			}
			
			// Add new community set
			if err := m.server.AddDefinedSet(ctx, &api.AddDefinedSetRequest{
				DefinedSet: &api.DefinedSet{
					DefinedType: api.DefinedType_LARGE_COMMUNITY,
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
					DefinedType: api.DefinedType_NEIGHBOR,
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
		m.peerConfigs[user.PeerIP] = user
	}
	// Update global policy
	users := make([]store.User, 0, len(m.peerConfigs))
	for _, u := range m.peerConfigs {
		users = append(users, u)
	}
	return m.configureGlobalPolicyLocked(ctx, users)
}

func (m *Manager) DeletePeer(ctx context.Context, peerIP string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.server == nil {
		return fmt.Errorf("BGP server is not running")
	}
	// Check if peer exists
	if _, exists := m.peerConfigs[peerIP]; !exists {
		return fmt.Errorf("peer %s does not exist", peerIP)
	}
	// Delete the peer
	if err := m.deletePeerLocked(ctx, peerIP); err != nil {
		return err
	}
	// Remove from configs
	delete(m.peerConfigs, peerIP)
	// Update global policy
	users := make([]store.User, 0, len(m.peerConfigs))
	for _, u := range m.peerConfigs {
		users = append(users, u)
	}
	return m.configureGlobalPolicyLocked(ctx, users)
}

func (m *Manager) reconcileLocked(ctx context.Context) error {
	if m.server == nil {
		return fmt.Errorf("BGP server is not running")
	}
	desired, err := m.store.DesiredPrefixes(ctx)
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
	
	// Use retry for BGP operations
	for prefix, installed := range m.installed {
		users, exists := desired[prefix]
		signature := signature(users)
		if exists && signature == installed.Signature {
			continue
		}
		
		err := retry.Do(ctx, retry.BGPConfig,
			func() error {
				return m.server.DeletePath(ctx, &api.DeletePathRequest{
					TableType: api.TableType_GLOBAL,
					Uuid:      installed.UUID,
				})
			},
			retry.TransientError,
		)
		
		if err != nil {
			return fmt.Errorf("withdraw %s: %w", prefix, err)
		}
		delete(m.installed, prefix)
	}
	
	for prefix, users := range desired {
		sig := signature(users)
		if installed, ok := m.installed[prefix]; ok && installed.Signature == sig {
			continue
		}
		
		path, err := m.path(prefix, users)
		if err != nil {
			return err
		}
		
		response, err := retry.DoWithResult(ctx, retry.BGPConfig,
			func() (*api.AddPathResponse, error) {
				return m.server.AddPath(ctx, &api.AddPathRequest{
					TableType: api.TableType_GLOBAL,
					Path:      path,
				})
			},
			retry.TransientError,
		)
		
		if err != nil {
			return fmt.Errorf("announce %s: %w", prefix, err)
		}
		m.installed[prefix] = installedPath{UUID: response.Uuid, Signature: sig}
	}
	return nil
}

func (m *Manager) path(rawPrefix string, userIDs []int64) (*api.Path, error) {
	prefix, err := netip.ParsePrefix(rawPrefix)
	if err != nil {
		return nil, err
	}
	nlri, err := anypb.New(&api.IPAddressPrefix{
		Prefix:    prefix.Addr().String(),
		PrefixLen: uint32(prefix.Bits()),
	})
	if err != nil {
		return nil, err
	}
	origin, _ := anypb.New(&api.OriginAttribute{Origin: 0})
	communities := make([]*api.LargeCommunity, 0, len(userIDs))
	for _, userID := range userIDs {
		if userID < 0 || userID > int64(^uint32(0)) {
			return nil, fmt.Errorf("user id %d is outside large community range", userID)
		}
		communities = append(communities, &api.LargeCommunity{
			GlobalAdmin: m.cfg.LocalASN,
			LocalData1:  uint32(userID),
			LocalData2:  0,
		})
	}
	communityAttribute, _ := anypb.New(&api.LargeCommunitiesAttribute{Communities: communities})
	if prefix.Addr().Is4() {
		nextHop, err := anypb.New(&api.NextHopAttribute{NextHop: m.cfg.LocalAddressV4})
		if err != nil {
			return nil, err
		}
		return &api.Path{
			Family: ipv4Family(),
			Nlri:   nlri,
			Pattrs: []*anypb.Any{origin, nextHop, communityAttribute},
		}, nil
	}
	if m.cfg.LocalAddressV6 == "" {
		return nil, fmt.Errorf("cannot announce IPv6 prefix %s without WDBGP_BGP_LOCAL_ADDRESS_V6", rawPrefix)
	}
	mpReach, err := anypb.New(&api.MpReachNLRIAttribute{
		Family:   ipv6Family(),
		NextHops: []string{m.cfg.LocalAddressV6},
		Nlris:    []*anypb.Any{nlri},
	})
	if err != nil {
		return nil, err
	}
	return &api.Path{
		Family: ipv6Family(),
		Nlri:   nlri,
		Pattrs: []*anypb.Any{origin, mpReach, communityAttribute},
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
		DefinedType: api.DefinedType_NEIGHBOR,
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

func ipv4Family() *api.Family {
	return &api.Family{Afi: api.Family_AFI_IP, Safi: api.Family_SAFI_UNICAST}
}

func ipv6Family() *api.Family {
	return &api.Family{Afi: api.Family_AFI_IP6, Safi: api.Family_SAFI_UNICAST}
}
