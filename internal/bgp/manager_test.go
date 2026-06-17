package bgp

import (
	"context"
	"database/sql"
	"net"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"log/slog"

	api "github.com/osrg/gobgp/v4/api"
	"github.com/osrg/gobgp/v4/pkg/apiutil"
	"github.com/osrg/gobgp/v4/pkg/packet/bgp"
	"github.com/osrg/gobgp/v4/pkg/server"

	"github.com/andrey-vk/wdbgp/internal/config"
	"github.com/andrey-vk/wdbgp/internal/store"
)

func TestPathCarriesUserCommunities(t *testing.T) {
	manager := NewManager(config.Config{
		LocalASN: 64512, LocalAddressV4: "172.16.0.1", LocalAddressV6: "fd00::1",
	}, nil)
	comms := map[string]uint32{"testcat": 10000, "testcat|testsvc": 10001}
	path, err := manager.path("149.154.160.0/20", []int64{2, 7}, "testcat", "testsvc", comms)
	if err != nil {
		t.Fatal(err)
	}
	var communities *bgp.PathAttributeLargeCommunities
	found := false
	for _, attr := range path.Attrs {
		if lc, ok := attr.(*bgp.PathAttributeLargeCommunities); ok {
			communities = lc
			found = true
			break
		}
	}
	if !found || len(communities.Values) != 4 {
		t.Fatalf("large communities not found: %#v", path.Attrs)
	}
	if communities.Values[1].LocalData1 != 7 {
		t.Fatalf("unexpected communities: %#v", communities.Values)
	}
	// Verify category and service communities are present.
	var catFound, svcFound bool
	for _, c := range communities.Values {
		if c.LocalData1 == 0 && c.LocalData2 == 10000 {
			catFound = true
		}
		if c.LocalData1 == 0 && c.LocalData2 == 10001 {
			svcFound = true
		}
	}
	if !catFound || !svcFound {
		t.Fatalf("category/service communities missing: %#v", communities.Values)
	}
}

func TestManagerStartsWithoutPeers(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "bgp.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	manager := NewManager(config.Config{
		LocalASN: 64512, RouterID: "192.0.2.1", BGPListenPort: -1,
		LocalAddressV4: "192.0.2.2",
	}, s)
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestReconcileSkipsIPv6WithoutLocalAddress(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(filepath.Join(t.TempDir(), "bgp.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	userID, err := s.AddUser(ctx, store.User{
		Name: "client", PeerIP: "192.0.2.2", PeerASN: 65001, Enabled: true,
		Networks: []string{"198.51.100.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO catalog_entries
		(feed_id, category, service, cidr) VALUES
		(1, 'test', 'dual-stack', '8.8.8.0/24'),
		(1, 'test', 'dual-stack', '2606:4700::/32')`); err != nil {
		t.Fatal(err)
	}
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		return store.SetUserSelection(ctx, tx, userID, []string{"test"}, nil)
	}); err != nil {
		t.Fatal(err)
	}

	manager := NewManager(config.Config{
		LocalASN: 64512, RouterID: "192.0.2.1", BGPListenPort: -1,
		LocalAddressV4: "192.0.2.1",
	}, s)
	if err := manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer manager.Stop(ctx)

	if len(manager.installed) != 1 {
		t.Fatalf("installed paths = %#v, want only IPv4", manager.installed)
	}
	if _, ok := manager.installed["8.8.8.0/24"]; !ok {
		t.Fatalf("IPv4 path was not installed: %#v", manager.installed)
	}
	if _, ok := manager.installed["2606:4700::/32"]; ok {
		t.Fatalf("IPv6 path installed without a local IPv6 address: %#v", manager.installed)
	}
}

func TestReconcileBuildsGlobalExportPolicy(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(filepath.Join(t.TempDir(), "bgp.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	firstID, err := s.AddUser(ctx, store.User{
		Name: "first", PeerIP: "192.0.2.2", PeerASN: 65001, Enabled: true,
		Networks: []string{"198.51.100.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	secondID, err := s.AddUser(ctx, store.User{
		Name: "second", PeerIP: "192.0.2.3", PeerASN: 65002, Enabled: true,
		Networks: []string{"198.51.101.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO catalog_entries
		(feed_id, category, service, cidr) VALUES
		(1, 'video', 'youtube', '8.8.8.0/24'),
		(1, 'video', 'youtube', '8.8.4.0/24'),
		(1, 'chat', 'telegram', '149.154.160.0/20')`); err != nil {
		t.Fatal(err)
	}
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		if err := store.SetUserSelection(ctx, tx, firstID, []string{"video"}, nil); err != nil {
			return err
		}
		return store.SetUserSelection(ctx, tx, secondID, nil,
			[]store.ServiceKey{{Category: "chat", Service: "telegram"}})
	}); err != nil {
		t.Fatal(err)
	}

	manager := NewManager(config.Config{
		LocalASN: 64512, RouterID: "192.0.2.1", BGPListenPort: -1,
		LocalAddressV4: "192.0.2.1",
	}, s)
	if err := manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer manager.Stop(ctx)

	sets := map[string][]string{}
	for _, definedType := range []api.DefinedType{api.DefinedType_DEFINED_TYPE_LARGE_COMMUNITY, api.DefinedType_DEFINED_TYPE_NEIGHBOR} {
		if err := manager.server.ListDefinedSet(ctx, &api.ListDefinedSetRequest{
			DefinedType: definedType,
		}, func(set *api.DefinedSet) {
			sets[set.Name] = append(sets[set.Name], set.List...)
		}); err != nil {
			t.Fatal(err)
		}
	}
	for name := range sets {
		sort.Strings(sets[name])
	}
	assertPrefixes(t, sets[userCommunitySetName(firstID)], []string{"^64512:1:0$"})
	assertPrefixes(t, sets[userCommunitySetName(secondID)], []string{"^64512:2:0$"})


}

func TestPeersReceiveOnlyTheirOwnPrefixes(t *testing.T) {
	if !canBindLoopback(t, "127.0.0.2") || !canBindLoopback(t, "127.0.0.3") {
		t.Skip("multiple loopback addresses are not available")
	}
	ctx := context.Background()
	s, err := store.Open(filepath.Join(t.TempDir(), "bgp.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	firstID, err := s.AddUser(ctx, store.User{
		Name: "first", PeerIP: "127.0.0.2", PeerASN: 65001, Enabled: true,
		Networks: []string{"198.51.100.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	secondID, err := s.AddUser(ctx, store.User{
		Name: "second", PeerIP: "127.0.0.3", PeerASN: 65002, Enabled: true,
		Networks: []string{"198.51.101.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO catalog_entries
		(feed_id, category, service, cidr) VALUES
		(1, 'video', 'youtube', '8.8.8.0/24'),
		(1, 'chat', 'telegram', '149.154.160.0/20')`); err != nil {
		t.Fatal(err)
	}
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		if err := store.SetUserSelection(ctx, tx, firstID, []string{"video"}, nil); err != nil {
			return err
		}
		return store.SetUserSelection(ctx, tx, secondID, []string{"chat"}, nil)
	}); err != nil {
		t.Fatal(err)
	}

	port := freeTCPPort(t)
	manager := NewManager(config.Config{
		LocalASN: 64512, RouterID: "192.0.2.1", BGPListenPort: int32(port),
		LocalAddressV4: "127.0.0.1",
	}, s)
	if err := manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer manager.Stop(ctx)

	firstClient := startTestPeer(t, "127.0.0.2", 65001, "192.0.2.11", port)
	defer firstClient.StopBgp(ctx, &api.StopBgpRequest{})
	secondClient := startTestPeer(t, "127.0.0.3", 65002, "192.0.2.12", port)
	defer secondClient.StopBgp(ctx, &api.StopBgpRequest{})

	waitForPrefixes(t, firstClient, []string{"8.8.8.0/24"})
	waitForPrefixes(t, secondClient, []string{"149.154.160.0/20"})

	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		return store.SetUserSelection(ctx, tx, firstID, nil, nil)
	}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	waitForPrefixes(t, firstClient, nil)
	waitForPrefixes(t, secondClient, []string{"149.154.160.0/20"})
}

func assertPrefixes(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("prefixes = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("prefixes = %v, want %v", got, want)
		}
	}
}



func canBindLoopback(t *testing.T, address string) bool {
	t.Helper()
	listener, err := net.Listen("tcp", net.JoinHostPort(address, "0"))
	if err != nil {
		return false
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return true
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func startTestPeer(t *testing.T, localAddress string, localASN uint32, routerID string, remotePort int) *server.BgpServer {
	t.Helper()
	ctx := context.Background()
	levelVar := &slog.LevelVar{}
	levelVar.Set(slog.LevelWarn)
	bgpServer := server.NewBgpServer(server.LoggerOption(slog.Default(), levelVar))
	go bgpServer.Serve()
	if err := bgpServer.StartBgp(ctx, &api.StartBgpRequest{
		Global: &api.Global{
			Asn:        localASN,
			RouterId:   routerID,
			ListenPort: -1,
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := bgpServer.AddPeer(ctx, &api.AddPeerRequest{Peer: &api.Peer{
		Conf: &api.PeerConf{
			NeighborAddress: "127.0.0.1",
			PeerAsn:         64512,
		},
		Transport: &api.Transport{
			LocalAddress: localAddress,
			RemotePort:   uint32(remotePort),
		},
		EbgpMultihop: &api.EbgpMultihop{
			Enabled:     true,
			MultihopTtl: 64,
		},
		AfiSafis: []*api.AfiSafi{
			{Config: &api.AfiSafiConfig{Family: ipv4Family(), Enabled: true}},
		},
	}}); err != nil {
		t.Fatal(err)
	}
	return bgpServer
}

func waitForPrefixes(t *testing.T, bgpServer *server.BgpServer, want []string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var got []string
	for time.Now().Before(deadline) {
		got = receivedPrefixes(t, bgpServer)
		if equalStrings(got, want) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("received prefixes = %v, want %v", got, want)
}

func receivedPrefixes(t *testing.T, bgpServer *server.BgpServer) []string {
	t.Helper()
	var prefixes []string
	err := bgpServer.ListPath(apiutil.ListPathRequest{
		TableType: api.TableType_TABLE_TYPE_GLOBAL,
		Family:    bgp.RF_IPv4_UC,
	}, func(prefix bgp.NLRI, paths []*apiutil.Path) {
		if ip, ok := prefix.(*bgp.IPAddrPrefix); ok && len(paths) > 0 {
			prefixes = append(prefixes, ip.Prefix.String())
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(prefixes)
	return prefixes
}

func TestExportCommunitiesStripOtherUsers(t *testing.T) {
	if !canBindLoopback(t, "127.0.0.2") || !canBindLoopback(t, "127.0.0.3") {
		t.Skip("multiple loopback addresses are not available")
	}
	ctx := context.Background()
	s, err := store.Open(filepath.Join(t.TempDir(), "bgp.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Insert catalog entry and community mappings for 8.8.8.0/24.
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO catalog_entries
		(feed_id, category, service, cidr) VALUES
		(1, 'video', 'youtube', '8.8.8.0/24')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO catalog_communities
		(mode_id, category, service, community) VALUES
		(1, 'video', '', 10000),
		(1, 'video', 'youtube', 10001)`); err != nil {
		t.Fatal(err)
	}

	// Create user A (will get ID=1).
	userAID, err := s.AddUser(ctx, store.User{
		Name: "user-a", PeerIP: "127.0.0.2", PeerASN: 65001, Enabled: true,
		Networks: []string{"198.51.100.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Create user B (will get ID=2).
	userBID, err := s.AddUser(ctx, store.User{
		Name: "user-b", PeerIP: "127.0.0.3", PeerASN: 65002, Enabled: true,
		Networks: []string{"198.51.101.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Both users select category 'video' so they both get 8.8.8.0/24.
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		if err := store.SetUserSelection(ctx, tx, userAID, []string{"video"}, nil); err != nil {
			return err
		}
		return store.SetUserSelection(ctx, tx, userBID, []string{"video"}, nil)
	}); err != nil {
		t.Fatal(err)
	}

	port := freeTCPPort(t)
	manager := NewManager(config.Config{
		LocalASN: 64512, RouterID: "192.0.2.1", BGPListenPort: int32(port),
		LocalAddressV4: "127.0.0.1",
	}, s)
	if err := manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer manager.Stop(ctx)

	// Both peers connect.
	clientA := startTestPeer(t, "127.0.0.2", 65001, "192.0.2.11", port)
	defer clientA.StopBgp(ctx, &api.StopBgpRequest{})
	clientB := startTestPeer(t, "127.0.0.3", 65002, "192.0.2.12", port)
	defer clientB.StopBgp(ctx, &api.StopBgpRequest{})

	// Both should receive the shared prefix.
	waitForPrefixes(t, clientA, []string{"8.8.8.0/24"})
	waitForPrefixes(t, clientB, []string{"8.8.8.0/24"})

	// Check that ID 1 and 2 match expectations.
	if userAID != 1 || userBID != 2 {
		t.Fatalf("user IDs: A=%d B=%d, want A=1 B=2", userAID, userBID)
	}

	// Verify from User A's perspective: should NOT have {64512:2:0}.
	commsA := getLargeCommunities(t, clientA)
	for _, c := range commsA {
		if c.ASN == 64512 && c.LocalData1 == 2 && c.LocalData2 == 0 {
			t.Fatalf("User A received User B's community {64512:2:0}: %#v", c)
		}
	}

	// Category/service communities should survive on export to User A.
	nonUserCount := 0
	for _, c := range commsA {
		if c.ASN == 64512 && c.LocalData1 == 0 && c.LocalData2 != 0 {
			nonUserCount++
		}
	}
	if nonUserCount == 0 {
		t.Fatalf("no category/service communities survived on export to User A: %#v", commsA)
	}

	// Verify from User B's perspective too: should NOT have {64512:1:0}.
	commsB := getLargeCommunities(t, clientB)
	for _, c := range commsB {
		if c.ASN == 64512 && c.LocalData1 == 1 && c.LocalData2 == 0 {
			t.Fatalf("User B received User A's community {64512:1:0}: %#v", c)
		}
	}
	nonUserCount = 0
	for _, c := range commsB {
		if c.ASN == 64512 && c.LocalData1 == 0 && c.LocalData2 != 0 {
			nonUserCount++
		}
	}
	if nonUserCount == 0 {
		t.Fatalf("no category/service communities survived on export to User B: %#v", commsB)
	}
}

func TestCommunityFilterUpdatesOnUserChange(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(filepath.Join(t.TempDir(), "bgp.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Insert catalog entry so routes can be reconciled.
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO catalog_entries
		(feed_id, category, service, cidr) VALUES
		(1, 'chat', 'telegram', '149.154.160.0/20')`); err != nil {
		t.Fatal(err)
	}

	// Create user A only.
	userAID, err := s.AddUser(ctx, store.User{
		Name: "user-a", PeerIP: "192.0.2.2", PeerASN: 65001, Enabled: true,
		Networks: []string{"198.51.100.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		return store.SetUserSelection(ctx, tx, userAID, []string{"chat"}, nil)
	}); err != nil {
		t.Fatal(err)
	}

	// Phase 1: Start manager with only user A in store.
	// REMOVE list should contain only {64512:1:0}.
	manager1 := NewManager(config.Config{
		LocalASN: 64512, RouterID: "192.0.2.1", BGPListenPort: -1,
		LocalAddressV4: "192.0.2.1",
	}, s)
	if err := manager1.Start(ctx); err != nil {
		t.Fatal(err)
	}
	assertRemoveCommunities(t, manager1, []string{"^64512:1:0$"})
	if err := manager1.Stop(ctx); err != nil {
		t.Fatal(err)
	}

	// Phase 2: Add user B to store, start new manager.
	// REMOVE list should contain both communities.
	userBID, err := s.AddUser(ctx, store.User{
		Name: "user-b", PeerIP: "192.0.2.3", PeerASN: 65002, Enabled: true,
		Networks: []string{"198.51.101.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		return store.SetUserSelection(ctx, tx, userBID, []string{"chat"}, nil)
	}); err != nil {
		t.Fatal(err)
	}
	manager2 := NewManager(config.Config{
		LocalASN: 64512, RouterID: "192.0.2.1", BGPListenPort: -1,
		LocalAddressV4: "192.0.2.1",
	}, s)
	if err := manager2.Start(ctx); err != nil {
		t.Fatal(err)
	}
	assertRemoveCommunities(t, manager2, []string{"^64512:1:0$", "^64512:2:0$"})
	if err := manager2.Stop(ctx); err != nil {
		t.Fatal(err)
	}

	// Phase 3: Disable user B, start new manager.
	// REMOVE list should be back to only {64512:1:0}.
	if _, err := s.DB.ExecContext(ctx, `UPDATE users SET enabled = 0 WHERE id = ?`, userBID); err != nil {
		t.Fatal(err)
	}
	manager3 := NewManager(config.Config{
		LocalASN: 64512, RouterID: "192.0.2.1", BGPListenPort: -1,
		LocalAddressV4: "192.0.2.1",
	}, s)
	if err := manager3.Start(ctx); err != nil {
		t.Fatal(err)
	}
	assertRemoveCommunities(t, manager3, []string{"^64512:1:0$"})
	if err := manager3.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}

// getLargeCommunities extracts all large community values from a peer's
// BgpServer global table.
func getLargeCommunities(t *testing.T, bgpServer *server.BgpServer) []*bgp.LargeCommunity {
	t.Helper()
	var allComms []*bgp.LargeCommunity
	err := bgpServer.ListPath(apiutil.ListPathRequest{
		TableType: api.TableType_TABLE_TYPE_GLOBAL,
		Family:    bgp.RF_IPv4_UC,
	}, func(prefix bgp.NLRI, paths []*apiutil.Path) {
		for _, path := range paths {
			for _, attr := range path.Attrs {
				if lc, ok := attr.(*bgp.PathAttributeLargeCommunities); ok {
					allComms = append(allComms, lc.Values...)
				}
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	return allComms
}

// assertRemoveCommunities checks that the export policy's REMOVE community list
// matches the expected set (order-independent).
func assertRemoveCommunities(t *testing.T, manager *Manager, expected []string) {
	t.Helper()
	var got []string
	err := manager.server.ListPolicy(context.Background(), &api.ListPolicyRequest{
		Name: exportPolicyName,
	}, func(policy *api.Policy) {
		for _, stmt := range policy.Statements {
			if stmt.Actions != nil && stmt.Actions.LargeCommunity != nil {
				got = stmt.Actions.LargeCommunity.Communities
				return
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	want := append([]string(nil), expected...)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("REMOVE communities = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("REMOVE communities = %v, want %v", got, want)
		}
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range want {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}
