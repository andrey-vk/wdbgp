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
	"github.com/andrey-vk/wdbgp/internal/store"
)

type installedPath struct {
	UUID      []byte
	Signature string
}

type Manager struct {
	cfg       config.Config
	store     *store.Store
	mu        sync.Mutex
	server    *server.BgpServer
	installed map[string]installedPath
}

func NewManager(cfg config.Config, s *store.Store) *Manager {
	return &Manager{cfg: cfg, store: s, installed: map[string]installedPath{}}
}

func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.startLocked(ctx)
}

func (m *Manager) Stop(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.server == nil {
		return nil
	}
	err := m.server.StopBgp(ctx, &api.StopBgpRequest{})
	m.server = nil
	m.installed = map[string]installedPath{}
	return err
}

func (m *Manager) ReloadPeers(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.server != nil {
		if err := m.server.StopBgp(ctx, &api.StopBgpRequest{}); err != nil {
			return err
		}
	}
	m.server = nil
	m.installed = map[string]installedPath{}
	return m.startLocked(ctx)
}

func (m *Manager) startLocked(ctx context.Context) error {
	bgpServer := server.NewBgpServer()
	go bgpServer.Serve()
	if err := bgpServer.StartBgp(ctx, &api.StartBgpRequest{
		Global: &api.Global{
			Asn:        m.cfg.LocalASN,
			RouterId:   m.cfg.RouterID,
			ListenPort: int32(m.cfg.BGPListenPort),
		},
	}); err != nil {
		return err
	}
	started := false
	defer func() {
		if !started {
			_ = bgpServer.StopBgp(context.Background(), &api.StopBgpRequest{})
		}
	}()
	m.server = bgpServer
	users, err := m.store.Users(ctx, true)
	if err != nil {
		m.server = nil
		return err
	}
	for _, user := range users {
		if err := m.addPeerLocked(ctx, user); err != nil {
			m.server = nil
			return fmt.Errorf("add peer %s: %w", user.PeerIP, err)
		}
	}
	if err := m.reconcileLocked(ctx); err != nil {
		m.server = nil
		return err
	}
	started = true
	return nil
}

func (m *Manager) addPeerLocked(ctx context.Context, user store.User) error {
	policyName := fmt.Sprintf("export_user_%d", user.ID)
	setName := fmt.Sprintf("user_%d_community", user.ID)
	community := largeCommunity(m.cfg.LocalASN, user.ID)
	if err := m.server.AddDefinedSet(ctx, &api.AddDefinedSetRequest{
		DefinedSet: &api.DefinedSet{
			DefinedType: api.DefinedType_LARGE_COMMUNITY,
			Name:        setName,
			List:        []string{community},
		},
	}); err != nil {
		return err
	}

	statements := []*api.Statement{}
	for _, family := range []api.Family_Afi{api.Family_AFI_IP, api.Family_AFI_IP6} {
		nextHop := &api.NexthopAction{Self: true}
		if user.NextHop != "" {
			address, err := netip.ParseAddr(user.NextHop)
			if err != nil {
				return err
			}
			if (address.Is4() && family == api.Family_AFI_IP) ||
				(address.Is6() && family == api.Family_AFI_IP6) {
				nextHop = &api.NexthopAction{Address: address.String()}
			}
		}
		statements = append(statements, &api.Statement{
			Name: fmt.Sprintf("%s_%s", policyName, strings.ToLower(family.String())),
			Conditions: &api.Conditions{
				LargeCommunitySet: &api.MatchSet{Name: setName, Type: api.MatchSet_ANY},
				AfiSafiIn: []*api.Family{{
					Afi:  family,
					Safi: api.Family_SAFI_UNICAST,
				}},
			},
			Actions: &api.Actions{
				RouteAction: api.RouteAction_ACCEPT,
				Nexthop:     nextHop,
			},
		})
	}
	policy := &api.Policy{Name: policyName, Statements: statements}
	if err := m.server.AddPolicy(ctx, &api.AddPolicyRequest{Policy: policy}); err != nil {
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
		ApplyPolicy: &api.ApplyPolicy{
			ImportPolicy: &api.PolicyAssignment{
				DefaultAction: api.RouteAction_REJECT,
			},
			ExportPolicy: &api.PolicyAssignment{
				Policies:      []*api.Policy{{Name: policyName}},
				DefaultAction: api.RouteAction_REJECT,
			},
		},
	}})
}

func (m *Manager) Reconcile(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reconcileLocked(ctx)
}

func (m *Manager) reconcileLocked(ctx context.Context) error {
	if m.server == nil {
		return fmt.Errorf("BGP server is not running")
	}
	desired, err := m.store.DesiredPrefixes(ctx)
	if err != nil {
		return err
	}
	for prefix, installed := range m.installed {
		users, exists := desired[prefix]
		signature := signature(users)
		if exists && signature == installed.Signature {
			continue
		}
		if err := m.server.DeletePath(ctx, &api.DeletePathRequest{
			TableType: api.TableType_GLOBAL,
			Uuid:      installed.UUID,
		}); err != nil {
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
		response, err := m.server.AddPath(ctx, &api.AddPathRequest{
			TableType: api.TableType_GLOBAL,
			Path:      path,
		})
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
