package bgp

import (
	"context"
	"database/sql"
	"net"
	"net/netip"
	"path/filepath"
	"sort"
	"testing"
	"time"

	api "github.com/osrg/gobgp/v3/api"
	"github.com/osrg/gobgp/v3/pkg/server"

	"github.com/andrey-vk/wdbgp/internal/config"
	"github.com/andrey-vk/wdbgp/internal/store"
)

// ipv4FamilyGoBGP returns the GoBGP IPv4 unicast family (used only by GoBGP test peers).
func ipv4FamilyGoBGP() *api.Family {
	return &api.Family{Afi: api.Family_AFI_IP, Safi: api.Family_SAFI_UNICAST}
}

func TestBuildRouteCarriesCommunities(t *testing.T) {
	manager := NewManager(config.Config{
		LocalASN: 64512, LocalAddressV4: "172.16.0.1", LocalAddressV6: "fd00::1",
	}, nil)
	prefix := netip.MustParsePrefix("149.154.160.0/20")
	comms := map[string]uint32{"testcat": 10000, "testcat|testsvc": 10001}
	user := store.User{ID: 7}
	route, err := manager.buildRoute(prefix, user, "testcat", "testsvc", comms)
	if err != nil {
		t.Fatal(err)
	}

	// Should have 3 communities: user ID, category, category|service
	if len(route.Communities) != 3 {
		t.Fatalf("expected 3 communities, got %d: %#v", len(route.Communities), route.Communities)
	}

	// First community: user ID (LocalData1=7, LocalData2=0)
	if route.Communities[0].GlobalAdmin != 64512 || route.Communities[0].LocalData1 != 7 || route.Communities[0].LocalData2 != 0 {
		t.Fatalf("expected user community 64512:7:0, got %d:%d:%d",
			route.Communities[0].GlobalAdmin, route.Communities[0].LocalData1, route.Communities[0].LocalData2)
	}

	// Check category and service communities
	var catFound, svcFound bool
	for _, c := range route.Communities {
		if c.LocalData1 == 0 && c.LocalData2 == 10000 {
			catFound = true
		}
		if c.LocalData1 == 0 && c.LocalData2 == 10001 {
			svcFound = true
		}
	}
	if !catFound || !svcFound {
		t.Fatalf("category/service communities missing: %#v", route.Communities)
	}

	// Next hop should be IPv4
	if route.NextHop.String() != "172.16.0.1" {
		t.Fatalf("expected next hop 172.16.0.1, got %s", route.NextHop)
	}
	if route.Prefix != prefix {
		t.Fatalf("expected prefix %s, got %s", prefix, route.Prefix)
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

func TestReconcileAssignsRoutesPerPeer(t *testing.T) {
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

	// First peer (user 1 with "video" category) should get video prefixes.
	firstRoutes := manager.peerRoutes["192.0.2.2"]
	firstPrefixes := routePrefixStrings(firstRoutes)
	sort.Strings(firstPrefixes)
	assertPrefixes(t, firstPrefixes, []string{"8.8.4.0/24", "8.8.8.0/24"})

	// Second peer (user 2 with "chat/telegram" service) should get chat prefix.
	secondRoutes := manager.peerRoutes["192.0.2.3"]
	secondPrefixes := routePrefixStrings(secondRoutes)
	assertPrefixes(t, secondPrefixes, []string{"149.154.160.0/20"})

	// Verify communities on first peer's route.
	if len(firstRoutes) > 0 {
		// First route should have user community for user 1.
		hasUserComm := false
		for _, c := range firstRoutes[0].Communities {
			if c.GlobalAdmin == 64512 && c.LocalData1 == uint32(firstID) {
				hasUserComm = true
			}
		}
		if !hasUserComm {
			t.Fatalf("first peer route missing user community: %#v", firstRoutes[0].Communities)
		}
	}
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

func routePrefixStrings(routes []Route) []string {
	var out []string
	for _, r := range routes {
		out = append(out, r.Prefix.String())
	}
	return out
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
	bgpServer := server.NewBgpServer()
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
			{Config: &api.AfiSafiConfig{Family: ipv4FamilyGoBGP(), Enabled: true}},
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
	err := bgpServer.ListPath(context.Background(), &api.ListPathRequest{
		TableType: api.TableType_GLOBAL,
		Family:    ipv4FamilyGoBGP(),
	}, func(destination *api.Destination) {
		if len(destination.Paths) > 0 {
			prefixes = append(prefixes, destination.Prefix)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(prefixes)
	return prefixes
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
