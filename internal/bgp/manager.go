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
	cfg         config.Config
	store       *store.Store
	mu          sync.Mutex
	server      *server.BgpServer
	installed   map[string]installedPath
	peerConfigs []store.User // all configured peers (supports multiple per IP, dynamic 0.0.0.0)
}

const (
	globalPolicyTable = "global"
	exportPolicyName  = "wdbgp_export"
)

func NewManager(cfg config.Config, s *store.Store) *Manager {
	return &Manager{
		cfg:         cfg,
		store:       s,
		installed:   map[string]installedPath{},
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
	m.installed = savedInstalled     // Restore installed routes
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

	// Check if any other enabled peer already uses this IP.
	// If yes, we must use dynamic neighbor (peer group) instead of static AddPeer.
	otherUsesIP := false
	for _, u := range m.peerConfigs {
		if u.Enabled && u.PeerIP == user.PeerIP && u.ID != user.ID {
			otherUsesIP = true
			break
		}
	}

	if !otherUsesIP && user.PeerIP != "0.0.0.0" {
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

	// Compute all user communities for REMOVE action in per-peer-group policy.
	allUserComms := make([]string, 0, len(m.peerConfigs)+1)
	for _, u := range m.peerConfigs {
		allUserComms = append(allUserComms, largeCommunity(m.cfg.LocalASN, u.ID))
	}
	allUserComms = append(allUserComms, largeCommunity(m.cfg.LocalASN, user.ID))

	// If another peer already uses this IP, upgrade any existing static peers
	// at this IP to peer-groups before adding the new peer.
	if otherUsesIP {
		for _, existing := range m.peerConfigs {
			if existing.Enabled && existing.PeerIP == user.PeerIP && existing.ID != user.ID {
				if !m.hasPeerGroup(ctx, existing) {
					// This peer was added as a static peer (first at this IP).
					// Delete the static peer and re-create as peer-group.
					if err := m.server.DeletePeer(ctx, &api.DeletePeerRequest{Address: existing.PeerIP}); err != nil {
						return fmt.Errorf("delete static peer %s for upgrade: %w", existing.PeerIP, err)
					}
					if err := m.addPeerGroupForUserLocked(ctx, existing, allUserComms, localAddress); err != nil {
						return fmt.Errorf("upgrade peer %s to peer-group: %w", existing.PeerIP, err)
					}
				}
			}
		}
	}

	// Create peer-group + dynamic neighbor for the new peer.
	return m.addPeerGroupForUserLocked(ctx, user, allUserComms, localAddress)
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
	// Check if any other enabled user shares this peer's IP.
	// After the upgrade in addPeerLocked, all shared-IP peers are added via
	// peer-group + dynamic neighbor.  We always try peer-group cleanup first
	// and fall back to static DeletePeer when the dynamic neighbor is not found.
	pgName := fmt.Sprintf("user_%d_pg", user.ID)
	mask := "/32"
	if addr, err := netip.ParseAddr(peerIP); err == nil && addr.Is6() {
		mask = "/128"
	}
	dynPrefix := peerIP + mask
	if peerIP == "0.0.0.0" {
		dynPrefix = "0.0.0.0/0"
	}
	if err := m.server.DeleteDynamicNeighbor(ctx, &api.DeleteDynamicNeighborRequest{
		Prefix:    dynPrefix,
		PeerGroup: pgName,
	}); err != nil {
		if strings.Contains(err.Error(), "not found") {
			// This user was a static peer, not a dynamic neighbor.
			// Fall back to DeletePeer (only valid for non-dynamic peers).
			if peerIP == "0.0.0.0" {
				return fmt.Errorf("dynamic neighbor not found for dynamic peer: %w", err)
			}
			return m.server.DeletePeer(ctx, &api.DeletePeerRequest{
				Address: peerIP,
			})
		}
		return err
	}
	if err := m.server.DeletePeerGroup(ctx, &api.DeletePeerGroupRequest{
		Name: pgName,
	}); err != nil {
		return err
	}
	// Delete the per-peer-group policy
	policyName := fmt.Sprintf("user_%d_policy", user.ID)
	if err := m.server.DeletePolicy(ctx, &api.DeletePolicyRequest{
		Policy: &api.Policy{Name: policyName},
	}); err != nil {
		return err
	}
	return nil
}

// hasPeerGroupPolicy reports whether a user has a per-peer-group export policy
// in GoBGP. Checks the actual GoBGP state rather than inferring from peerConfigs,
// because a peer may remain a peer-group even after other shared-IP users are
// deleted.
func (m *Manager) hasPeerGroupPolicy(ctx context.Context, user store.User) bool {
	if user.PeerIP == "0.0.0.0" {
		return true
	}
	return m.hasPeerGroup(ctx, user)
}

// hasPeerGroup reports whether a user has a peer-group in GoBGP.
// True for dynamic peers (0.0.0.0) and for peers whose peer group
// actually exists in GoBGP's configuration.
func (m *Manager) hasPeerGroup(ctx context.Context, user store.User) bool {
	if user.PeerIP == "0.0.0.0" {
		return true
	}
	pgName := fmt.Sprintf("user_%d_pg", user.ID)
	found := false
	m.server.ListPeerGroup(ctx, &api.ListPeerGroupRequest{}, func(pg *api.PeerGroup) {
		if pg.Conf != nil && pg.Conf.PeerGroupName == pgName {
			found = true
		}
	})
	return found
}

// addPeerGroupForUserLocked creates a per-peer-group export policy, AddPeerGroup,
// and AddDynamicNeighbor for a given user. The caller must have already created
// the user's defined sets (community, neighbor).
// All peer-group lifecycle is delegated to rebuildPeerGroupPolicyLocked.
func (m *Manager) addPeerGroupForUserLocked(ctx context.Context, user store.User, allUserComms []string, localAddress string) error {
	return m.rebuildPeerGroupPolicyLocked(ctx, user, allUserComms)
}

// rebuildPeerGroupPolicyLocked tears down the per-peer-group policy (dynamic
// neighbor + peer group + policy), creates a new policy with a fresh REMOVE
// list, and recreates the peer group + dynamic neighbor.  The tear-down is
// necessary because GoBGP v4 does not clean up statement names unless the
// policy is fully unreferenced — the peer group must be deleted first.  Any
// "not found" errors during tear-down are ignored (first-time or already-
// cleaned-up cases).  This follows the same unassign-delete-add-reassign
// pattern as configureGlobalPolicyLocked.
func (m *Manager) rebuildPeerGroupPolicyLocked(ctx context.Context, user store.User, allUserComms []string) error {
	policyName := fmt.Sprintf("user_%d_policy", user.ID)
	pgName := fmt.Sprintf("user_%d_pg", user.ID)

	// Compute dynamic neighbor prefix (needed for both tear-down and recreation).
	mask := "/32"
	if addr, err := netip.ParseAddr(user.PeerIP); err == nil && addr.Is6() {
		mask = "/128"
	}
	dynPrefix := user.PeerIP + mask
	if user.PeerIP == "0.0.0.0" {
		dynPrefix = "0.0.0.0/0"
	}

	// Tear down existing resources when doing an update (peer group already exists).
	// On the first call the peer group does not exist yet, so we skip the
	// dynamic-neighbor and peer-group deletion to avoid GoBGP internal panics.
	// Use a direct ListPeerGroup check — hasPeerGroup short-circuits to true
	// for 0.0.0.0 peers which may not have a peer group yet on first call.
	pgExists := false
	m.server.ListPeerGroup(ctx, &api.ListPeerGroupRequest{}, func(pg *api.PeerGroup) {
		if pg.Conf != nil && pg.Conf.PeerGroupName == pgName {
			pgExists = true
		}
	})
	if pgExists {
		if err := m.server.DeleteDynamicNeighbor(ctx, &api.DeleteDynamicNeighborRequest{
			Prefix:    dynPrefix,
			PeerGroup: pgName,
		}); err != nil && !strings.Contains(err.Error(), "not found") {
			return fmt.Errorf("delete old dynamic neighbor for user %d: %w", user.ID, err)
		}
		if err := m.server.DeletePeerGroup(ctx, &api.DeletePeerGroupRequest{
			Name: pgName,
		}); err != nil && !strings.Contains(err.Error(), "not found") {
			return fmt.Errorf("delete old peer group for user %d: %w", user.ID, err)
		}
	}

	// Delete old policy; ignore "not found" errors.
	if err := m.server.DeletePolicy(ctx, &api.DeletePolicyRequest{
		Policy: &api.Policy{Name: policyName},
		All:    true,
	}); err != nil && !strings.Contains(err.Error(), "not found") {
		return fmt.Errorf("delete old policy %s: %w", policyName, err)
	}

	// Build new policy with fresh REMOVE lists.
	nextHopV4, err := nextHopAction(user, api.Family_AFI_IP)
	if err != nil {
		return err
	}
	nextHopV6, err := nextHopAction(user, api.Family_AFI_IP6)
	if err != nil {
		return err
	}

	if err := m.server.AddPolicy(ctx, &api.AddPolicyRequest{
		Policy: &api.Policy{
			Name: policyName,
			Statements: []*api.Statement{
				{
					Name: fmt.Sprintf("user_%d_v4", user.ID),
					Conditions: &api.Conditions{
						LargeCommunitySet: &api.MatchSet{
							Name: userCommunitySetName(user.ID),
							Type: api.MatchSet_TYPE_ANY,
						},
					},
					Actions: &api.Actions{
						RouteAction: api.RouteAction_ROUTE_ACTION_ACCEPT,
						LargeCommunity: &api.CommunityAction{
							Type:        api.CommunityAction_TYPE_REMOVE,
							Communities: allUserComms,
						},
						Nexthop: nextHopV4,
					},
				},
				{
					Name: fmt.Sprintf("user_%d_v6", user.ID),
					Conditions: &api.Conditions{
						LargeCommunitySet: &api.MatchSet{
							Name: userCommunitySetName(user.ID),
							Type: api.MatchSet_TYPE_ANY,
						},
					},
					Actions: &api.Actions{
						RouteAction: api.RouteAction_ROUTE_ACTION_ACCEPT,
						LargeCommunity: &api.CommunityAction{
							Type:        api.CommunityAction_TYPE_REMOVE,
							Communities: allUserComms,
						},
						Nexthop: nextHopV6,
					},
				},
			},
		},
	}); err != nil {
		return fmt.Errorf("add policy for peer group: %w", err)
	}

	// Recreate peer group with the new policy assigned.
	conf := m.peerGroupConfig(user, policyName, true)
	if err := m.server.AddPeerGroup(ctx, &api.AddPeerGroupRequest{
		PeerGroup: conf,
	}); err != nil {
		return fmt.Errorf("add peer group for user %d: %w", user.ID, err)
	}

	// Recreate dynamic neighbor.
	if err := m.server.AddDynamicNeighbor(ctx, &api.AddDynamicNeighborRequest{
		DynamicNeighbor: &api.DynamicNeighbor{
			Prefix:    dynPrefix,
			PeerGroup: pgName,
		},
	}); err != nil {
		return fmt.Errorf("add dynamic neighbor for user %d: %w", user.ID, err)
	}

	return nil
}

// peerGroupConfig builds a PeerGroup configuration for a user.
// When withPolicy is false, the ApplyPolicy is left nil so the peer-group
// "unassigns" the policy before deletion. When withPolicy is true, the
// full export policy assignment is included.
func (m *Manager) peerGroupConfig(user store.User, policyName string, withPolicy bool) *api.PeerGroup {
	pgName := fmt.Sprintf("user_%d_pg", user.ID)
	conf := &api.PeerGroupConf{
		PeerGroupName: pgName,
		PeerAsn:       uint32(user.PeerASN),
	}
	if user.BGPPassword != "" {
		conf.AuthPassword = user.BGPPassword
	}

	peerAddr, err := netip.ParseAddr(user.PeerIP)
	localAddress := m.cfg.LocalAddressV4
	if err == nil && peerAddr.Is6() {
		localAddress = m.cfg.LocalAddressV6
	}
	if localAddress == "" {
		localAddress = m.cfg.LocalAddressV4
	}

	pg := &api.PeerGroup{
		Conf: conf,
		Transport:    &api.Transport{LocalAddress: localAddress},
		EbgpMultihop: &api.EbgpMultihop{Enabled: true, MultihopTtl: 64},
		AfiSafis: []*api.AfiSafi{
			{Config: &api.AfiSafiConfig{Family: ipv4Family(), Enabled: true}},
			{Config: &api.AfiSafiConfig{Family: ipv6Family(), Enabled: true}},
		},
	}

	if withPolicy {
		pg.ApplyPolicy = &api.ApplyPolicy{
			ExportPolicy: &api.PolicyAssignment{
				Name:      fmt.Sprintf("export_peer_%d", user.ID),
				Direction: api.PolicyDirection_POLICY_DIRECTION_EXPORT,
				Policies: []*api.Policy{{
					Name: policyName,
				}},
				DefaultAction: api.RouteAction_ROUTE_ACTION_REJECT,
			},
		}
	}

	return pg
}

func (m *Manager) configureGlobalPolicyLocked(ctx context.Context, users []store.User) error {
	statements := make([]*api.Statement, 0, len(users)*2)
	allUserComms := make([]string, 0, len(users))
	for _, u := range users {
		allUserComms = append(allUserComms, largeCommunity(m.cfg.LocalASN, u.ID))
	}
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
						Type:        api.CommunityAction_TYPE_REMOVE,
						Communities: allUserComms,
					},
					Nexthop: nextHop,
				},
			})
		}
	}
	// Unassign the old global export policy so it is no longer "in use",
	// then delete it to clear statement names from GoBGP's internal registry.
	if err := m.server.SetPolicyAssignment(ctx, &api.SetPolicyAssignmentRequest{
		Assignment: &api.PolicyAssignment{
			Name:          globalPolicyTable,
			Direction:     api.PolicyDirection_POLICY_DIRECTION_EXPORT,
			DefaultAction: api.RouteAction_ROUTE_ACTION_REJECT,
		},
	}); err != nil {
		return fmt.Errorf("unassign old global export policy: %w", err)
	}
	if err := m.server.DeletePolicy(ctx, &api.DeletePolicyRequest{
		Policy: &api.Policy{Name: exportPolicyName},
		All:    true,
	}); err != nil {
		if !strings.Contains(err.Error(), "not found") {
			return fmt.Errorf("delete old global export policy: %w", err)
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
			Direction:     api.PolicyDirection_POLICY_DIRECTION_EXPORT,
			Policies:      []*api.Policy{{Name: exportPolicyName}},
			DefaultAction: api.RouteAction_ROUTE_ACTION_REJECT,
		},
	}); err != nil {
		return err
	}

	// Rebuild per-peer-group policies for all users to keep REMOVE lists fresh.
	for _, user := range users {
		if m.hasPeerGroupPolicy(ctx, user) {
			if err := m.rebuildPeerGroupPolicyLocked(ctx, user, allUserComms); err != nil {
				return err
			}
		}
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
		// Same IP — check whether this peer uses dynamic neighbor / peer-group.
		// If so, delete the old peer group and re-add with the new settings
		// (GoBGP's UpdatePeer only works for static peers, not peer groups).
		if m.hasPeerGroupPolicy(ctx, user) {
			// Delete old peer (peer group, dynamic neighbor, policy)
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
			// Add new peer with updated settings
			if err := m.addPeerLocked(ctx, user); err != nil {
				return err
			}
			m.peerConfigs = append(m.peerConfigs, user)
		} else {
			// Update existing static peer using GoBGP's UpdatePeer API
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
	var user store.User
	for _, u := range m.peerConfigs {
		if u.ID == userID && u.PeerIP == peerIP {
			found = true
			user = u
			break
		}
	}
	if !found {
		return fmt.Errorf("peer %s (user %d) does not exist", peerIP, userID)
	}
	// Check if any other enabled user shares this IP
	var otherUserHasIP bool
	for _, u := range m.peerConfigs {
		if u.Enabled && u.PeerIP == peerIP && u.ID != userID {
			otherUserHasIP = true
			break
		}
	}
	if !otherUserHasIP || peerIP == "0.0.0.0" || m.hasPeerGroupPolicy(ctx, user) {
		if err := m.deletePeerLocked(ctx, userID, peerIP); err != nil {
			return err
		}
	}
	// Remove from config list (after successful teardown, so error path preserves config)
	for i, u := range m.peerConfigs {
		if u.ID == userID && u.PeerIP == peerIP {
			m.peerConfigs = append(m.peerConfigs[:i], m.peerConfigs[i+1:]...)
			break
		}
	}
	return m.configureGlobalPolicyLocked(ctx, m.peerConfigs)
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
		userIDs []int64
		comms   map[string]uint32
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
					// Only include communities matching this prefix's category/service.
					if k != meta.Category && k != meta.Category+"|"+meta.Service {
						continue
					}
					mr.comms[k] = v
					// Also store mode-specific key to prevent cross-mode overwrite.
					mr.comms[fmt.Sprintf("%d|%s", meta.ModeID, k)] = v
				}
			}

		}
	}

	// Use retry for BGP operations — withdraw stale entries.
	for prefix, installed := range m.installed {
		mr, exists := perPrefix[prefix]
		sig := ""
		if exists {
			sig = signature(mr.userIDs, mr.comms)
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
		sig := signature(mr.userIDs, mr.comms)
		if installed, ok := m.installed[actualPrefix]; ok && installed.Signature == sig {
			continue
		}

		path, err := m.path(actualPrefix, mr.userIDs, mr.comms)
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

func (m *Manager) path(rawPrefix string, userIDs []int64, communities map[string]uint32) (*apiutil.Path, error) {
	prefix, err := netip.ParsePrefix(rawPrefix)
	if err != nil {
		return nil, err
	}
	nlri, err := bgp.NewIPAddrPrefix(prefix)
	if err != nil {
		return nil, err
	}
	origin := bgp.NewPathAttributeOrigin(0)
	comms := make([]*bgp.LargeCommunity, 0, len(userIDs)+len(communities))
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
	// Emit all mode metadata communities (per-mode category/service).
	// Filtering of unrelated modes' communities is already done by
	// reconcileLocked when it builds the communities map.
	for _, val := range communities {
		comms = append(comms, &bgp.LargeCommunity{
			ASN: m.cfg.LocalASN, LocalData1: 0, LocalData2: val,
		})
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
		if peer.State != nil {
			addr := ""
			if peer.Conf != nil && peer.Conf.NeighborAddress != "" {
				addr = peer.Conf.NeighborAddress
			} else if peer.State.NeighborAddress != "" {
				addr = peer.State.NeighborAddress
			}
			if addr != "" {
				state := peer.State.SessionState.String()
				state = strings.TrimPrefix(state, "SESSION_STATE_")
				states[addr] = state
			}
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

func signature(userIDs []int64, comms map[string]uint32) string {
	sorted := append([]int64(nil), userIDs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	parts := make([]string, 0, len(sorted)+len(comms)*2)
	for _, id := range sorted {
		parts = append(parts, strconv.FormatInt(id, 10))
	}
	// Sort community keys for deterministic output.
	commKeys := make([]string, 0, len(comms))
	for k := range comms {
		commKeys = append(commKeys, k)
	}
	sort.Strings(commKeys)
	for _, k := range commKeys {
		parts = append(parts, k, strconv.FormatUint(uint64(comms[k]), 10))
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
