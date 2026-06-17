package bgp

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"path/filepath"
	"sort"
	"strings"
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

func TestMergePreservesAllModeMetadata(t *testing.T) {
	// Simulate reconcileLocked output when the same prefix "10.0.0.0/24"
	// is selected in mode 1 (category=video, service=youtube) AND
	// mode 2 (category=chat, service=telegram).
	//
	// reconcileLocked sets mr.category = first mode's category (video).
	// path() then filters mode-scoped communities by that single category,
	// dropping mode 2's chat communities.
	manager := NewManager(config.Config{
		LocalASN: 64512, LocalAddressV4: "172.16.0.1", LocalAddressV6: "fd00::1",
	}, nil)

	// Communities map as built by reconcileLocked — mr.comms after merging.
	// Non-scoped keys (last-write-wins across modes):
	//   "video"=10000, "video|youtube"=10001, "chat"=20000, "chat|telegram"=20001
	// Mode-scoped keys (one per mode, unique):
	//   "1|video"=10000, "1|video|youtube"=10001, "2|chat"=20000, "2|chat|telegram"=20001
	comms := map[string]uint32{
		"video":            10000,
		"video|youtube":    10001,
		"chat":             20000,
		"chat|telegram":    20001,
		"1|video":          10000,
		"1|video|youtube":  10001,
		"2|chat":           20000,
		"2|chat|telegram":  20001,
	}

	path, err := manager.path("10.0.0.0/24", []int64{1, 2}, comms)
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
	if !found || len(communities.Values) == 0 {
		t.Fatalf("large communities not found: %#v", path.Attrs)
	}

	// The path should carry communities from BOTH modes.
	var hasMode1, hasMode2 bool
	var hasMode1Svc, hasMode2Svc bool
	for _, c := range communities.Values {
		if c.LocalData1 == 0 && c.LocalData2 == 10000 {
			hasMode1 = true
		}
		if c.LocalData1 == 0 && c.LocalData2 == 20000 {
			hasMode2 = true
		}
		if c.LocalData1 == 0 && c.LocalData2 == 10001 {
			hasMode1Svc = true
		}
		if c.LocalData1 == 0 && c.LocalData2 == 20001 {
			hasMode2Svc = true
		}
	}

	if !hasMode1 {
		t.Fatalf("missing mode 1 category community (10000): %#v", communities.Values)
	}
	if !hasMode2 {
		t.Fatalf("BUG: missing mode 2 category community (20000) — path() filters by mr.category='video' and drops mode 2's 'chat': %#v", communities.Values)
	}
	if !hasMode1Svc {
		t.Fatalf("missing mode 1 service community (10001): %#v", communities.Values)
	}
	if !hasMode2Svc {
		t.Fatalf("BUG: missing mode 2 service community (20001) — path() filters by mr.category='video'/'youtube' and drops mode 2's 'chat'/'telegram': %#v", communities.Values)
	}
}

func TestPathCarriesUserCommunities(t *testing.T) {
	manager := NewManager(config.Config{
		LocalASN: 64512, LocalAddressV4: "172.16.0.1", LocalAddressV6: "fd00::1",
	}, nil)
	comms := map[string]uint32{"testcat": 10000, "testcat|testsvc": 10001}
	path, err := manager.path("149.154.160.0/20", []int64{2, 7}, comms)
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

func TestPathPreservesPerModeCommunities(t *testing.T) {
	manager := NewManager(config.Config{
		LocalASN: 64512, LocalAddressV4: "172.16.0.1", LocalAddressV6: "fd00::1",
	}, nil)

	// Simulate what reconcileLocked produces when the same prefix "10.0.0.0/24"
	// is selected in two different modes with different community values.
	// Mode 1: category=video, community 10000, service community 10001
	// Mode 2: category=video, community 20000, service community 20001
	// After merge, mr.comms contains both mode-scoped and non-scoped keys.
	comms := map[string]uint32{
		"1|video":          10000,
		"2|video":          20000,
		"video":            10000, // non-scoped fallback (from whichever mode merged last)
		"1|video|youtube":  10001,
		"2|video|youtube":  20001,
		"video|youtube":    10001, // non-scoped service fallback
	}

	path, err := manager.path("10.0.0.0/24", []int64{1, 2}, comms)
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
	if !found || len(communities.Values) == 0 {
		t.Fatalf("large communities not found: %#v", path.Attrs)
	}

	// The path should carry communities from BOTH modes.
	var hasMode1, hasMode2 bool
	var hasMode1Svc, hasMode2Svc bool
	for _, c := range communities.Values {
		if c.LocalData1 == 0 && c.LocalData2 == 10000 {
			hasMode1 = true
		}
		if c.LocalData1 == 0 && c.LocalData2 == 20000 {
			hasMode2 = true
		}
		if c.LocalData1 == 0 && c.LocalData2 == 10001 {
			hasMode1Svc = true
		}
		if c.LocalData1 == 0 && c.LocalData2 == 20001 {
			hasMode2Svc = true
		}
	}

	if !hasMode1 {
		t.Fatalf("missing mode 1 category community (10000): %#v", communities.Values)
	}
	if !hasMode2 {
		t.Fatalf("missing mode 2 category community (20000): mode-scoped key not looked up, per-mode metadata lost — BUG: %#v", communities.Values)
	}
	if !hasMode1Svc {
		t.Fatalf("missing mode 1 service community (10001): %#v", communities.Values)
	}
	if !hasMode2Svc {
		t.Fatalf("missing mode 2 service community (20001): mode-scoped service key not looked up, per-mode metadata lost — BUG: %#v", communities.Values)
	}
}

func TestPathFiltersUnmatchedModeScopedCommunities(t *testing.T) {
	// Communities map for mode 1 with two different categories.
	// reconcileLocked() stores both plain and mode-scoped keys.
	// Here we simulate the mode-scoped portion that path() iterates.
	comms := map[string]uint32{
		"1|video": 10000,
		"1|chat":  20000,
	}

	manager := NewManager(config.Config{
		LocalASN: 64512, LocalAddressV4: "172.16.0.1", LocalAddressV6: "fd00::1",
	}, nil)

	// path() emits ALL communities from the map — per-mode filtering
	// is now done at the reconcileLocked level, not in path().
	path, err := manager.path("10.0.0.0/24", []int64{1}, comms)
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
	if !found || len(communities.Values) == 0 {
		t.Fatalf("large communities not found: %#v", path.Attrs)
	}

	var hasVideo, hasChat bool
	for _, c := range communities.Values {
		if c.LocalData1 == 0 && c.LocalData2 == 10000 {
			hasVideo = true
		}
		if c.LocalData1 == 0 && c.LocalData2 == 20000 {
			hasChat = true
		}
	}

	if !hasVideo {
		t.Fatalf("missing 'video' category community (10000): %#v", communities.Values)
	}
	if !hasChat {
		t.Fatalf("missing 'chat' category community (20000): path() should emit all communities, filtering is done in reconcileLocked: %#v", communities.Values)
	}
}

func TestDynamicNeighborSameIPDifferentASN(t *testing.T) {
	ctx := context.Background()
	levelVar := &slog.LevelVar{}
	levelVar.Set(slog.LevelWarn)
	bgpServer := server.NewBgpServer(server.LoggerOption(slog.Default(), levelVar))
	go bgpServer.Serve()

	if err := bgpServer.StartBgp(ctx, &api.StartBgpRequest{
		Global: &api.Global{Asn: 64512, RouterId: "192.0.2.1", ListenPort: -1},
	}); err != nil {
		t.Fatal(err)
	}
	defer bgpServer.StopBgp(ctx, &api.StopBgpRequest{})

	// Peer group for user 1
	if err := bgpServer.AddPeerGroup(ctx, &api.AddPeerGroupRequest{PeerGroup: &api.PeerGroup{
		Conf: &api.PeerGroupConf{PeerGroupName: "user1_pg", PeerAsn: 65001},
	}}); err != nil {
		t.Fatal(err)
	}
	// Dynamic neighbor on 127.0.0.100/32 → user1_pg
	if err := bgpServer.AddDynamicNeighbor(ctx, &api.AddDynamicNeighborRequest{DynamicNeighbor: &api.DynamicNeighbor{
		Prefix: "127.0.0.100/32", PeerGroup: "user1_pg",
	}}); err != nil {
		t.Fatal(err)
	}

	// Peer group for user 2
	if err := bgpServer.AddPeerGroup(ctx, &api.AddPeerGroupRequest{PeerGroup: &api.PeerGroup{
		Conf: &api.PeerGroupConf{PeerGroupName: "user2_pg", PeerAsn: 65002},
	}}); err != nil {
		t.Fatal(err)
	}
	// Dynamic neighbor on same IP → user2_pg
	if err := bgpServer.AddDynamicNeighbor(ctx, &api.AddDynamicNeighborRequest{DynamicNeighbor: &api.DynamicNeighbor{
		Prefix: "127.0.0.100/32", PeerGroup: "user2_pg",
	}}); err != nil {
		t.Fatal(err)
	}

	t.Log("SUCCESS: two peer groups with dynamic neighbor on same IP")

	// Verify peer groups exist
	bgpServer.ListPeerGroup(ctx, &api.ListPeerGroupRequest{}, func(pg *api.PeerGroup) {
		t.Logf("Peer group: %s ASN=%d", pg.Conf.PeerGroupName, pg.Conf.PeerAsn)
	})
}

func TestDeletePeerCleansUpSharedIPPeerGroup(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(filepath.Join(t.TempDir(), "bgp.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	manager := NewManager(config.Config{
		LocalASN: 64512, RouterID: "192.0.2.1", BGPListenPort: -1,
		LocalAddressV4: "192.0.2.1",
	}, s)
	if err := manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer manager.Stop(ctx)

	// Add first peer (will become peer group — no other peer shares IP yet, so static).
	user1ID, err := s.AddUser(ctx, store.User{
		Name: "user1", PeerIP: "192.0.2.100", PeerASN: 65001, Enabled: true,
		Networks: []string{"198.51.100.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.AddPeer(ctx, store.User{
		ID: user1ID, Name: "user1", PeerIP: "192.0.2.100", PeerASN: 65001, Enabled: true,
		Networks: []string{"198.51.100.0/24"},
	}); err != nil {
		t.Fatal(err)
	}

	// Add second peer (same IP, different ASN — installed as peer group + dynamic neighbor
	// because user1 already uses that IP).
	user2ID, err := s.AddUser(ctx, store.User{
		Name: "user2", PeerIP: "192.0.2.100", PeerASN: 65002, Enabled: true,
		Networks: []string{"198.51.101.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.AddPeer(ctx, store.User{
		ID: user2ID, Name: "user2", PeerIP: "192.0.2.100", PeerASN: 65002, Enabled: true,
		Networks: []string{"198.51.101.0/24"},
	}); err != nil {
		t.Fatal(err)
	}

	pgName1 := fmt.Sprintf("user_%d_pg", user1ID)
	pgName2 := fmt.Sprintf("user_%d_pg", user2ID)

	// After the fix, user1 was upgraded to a peer-group when user2 was added.
	// Both peers now have peer-groups.
	var pg1Found, pg2Found bool
	manager.server.ListPeerGroup(ctx, &api.ListPeerGroupRequest{}, func(pg *api.PeerGroup) {
		if pg.Conf.PeerGroupName == pgName1 {
			pg1Found = true
		}
		if pg.Conf.PeerGroupName == pgName2 {
			pg2Found = true
		}
	})
	if !pg1Found {
		t.Fatal("user1 should have a peer group (upgraded from static when user2 shared IP)")
	}
	if !pg2Found {
		t.Fatal("user2 should have a peer group (same IP as user1, installed via dynamic neighbor)")
	}

	// Delete second peer — this should clean up user2's peer group and dynamic neighbor.
	if err := manager.DeletePeer(ctx, user2ID, "192.0.2.100"); err != nil {
		t.Fatalf("DeletePeer failed: %v", err)
	}

	// Verify user2's peer group is gone.
	pg2Found = false
	manager.server.ListPeerGroup(ctx, &api.ListPeerGroupRequest{}, func(pg *api.PeerGroup) {
		if pg.Conf.PeerGroupName == pgName2 {
			pg2Found = true
		}
	})
	if pg2Found {
		t.Fatal("BUG: user2's peer group was NOT cleaned up after DeletePeer — same-IP peer group leaked")
	}
}

func TestUpdateDynamicPeerReconfiguresPeerGroup(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(filepath.Join(t.TempDir(), "bgp.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Create a dynamic peer (0.0.0.0) in the DB before starting the manager,
	// so startLocked configures it via addPeerLocked (which works during init).
	userID, err := s.AddUser(ctx, store.User{
		Name: "dynamic-peer", PeerIP: "0.0.0.0", PeerASN: 65001, Enabled: true,
		Networks: []string{"198.51.100.0/24"},
	})
	if err != nil {
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

	pgName := fmt.Sprintf("user_%d_pg", userID)

	// Verify peer group exists with ASN 65001.
	var initialASN uint32
	manager.server.ListPeerGroup(ctx, &api.ListPeerGroupRequest{}, func(pg *api.PeerGroup) {
		if pg.Conf.PeerGroupName == pgName {
			initialASN = pg.Conf.PeerAsn
		}
	})
	if initialASN != 65001 {
		t.Fatalf("initial peer group ASN = %d, want 65001", initialASN)
	}

	// Update the peer: change ASN to 65002. Currently this calls
	// GoBGP's UpdatePeer which does NOT work for peer-group peers.
	user := store.User{
		ID: userID, Name: "dynamic-peer", PeerIP: "0.0.0.0", PeerASN: 65002,
		Enabled: true, Networks: []string{"198.51.100.0/24"},
	}
	if err := manager.UpdatePeer(ctx, user); err != nil {
		t.Fatalf("UpdatePeer failed: %v", err)
	}

	// Verify peer group now has ASN 65002.
	var updatedASN uint32
	manager.server.ListPeerGroup(ctx, &api.ListPeerGroupRequest{}, func(pg *api.PeerGroup) {
		if pg.Conf.PeerGroupName == pgName {
			updatedASN = pg.Conf.PeerAsn
		}
	})
	if updatedASN != 65002 {
		t.Fatalf("peer group ASN after update = %d, want 65002; UpdatePeer did not reconfigure the peer group", updatedASN)
	}
}

func TestExportPolicyRebuildIncludesNewUsers(t *testing.T) {
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

	// Phase 1: Start manager with user A only (2 export statements).
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

	manager := NewManager(config.Config{
		LocalASN: 64512, RouterID: "192.0.2.1", BGPListenPort: -1,
		LocalAddressV4: "192.0.2.1",
	}, s)
	if err := manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer manager.Stop(ctx)

	// Verify phase 1: policy has 2 statements (v4+v6) for user A.
	assertExportStatementCount(t, manager, 2)

	// Phase 2: Add user B via AddPeer. This calls configureGlobalPolicyLocked again
	// within the same server instance, which triggers "already defined" for existing
	// statement names. The bug: we tolerate the error by returning nil, but the new
	// user's statements are never installed.
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

	if err := manager.AddPeer(ctx, store.User{
		ID: userBID, Name: "user-b", PeerIP: "192.0.2.3", PeerASN: 65002,
		Enabled: true, Networks: []string{"198.51.101.0/24"},
	}); err != nil {
		t.Fatal(err)
	}

	// Verify: the policy should have 4 statements (v4+v6 for each of user A and user B).
	// The fix ensures configureGlobalPolicyLocked unassigns, deletes, and recreates the
	// global export policy so that new users' statements are always installed.
	assertExportStatementCount(t, manager, 4)
}

func TestDynamicNeighborPrefixIPv6(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(filepath.Join(t.TempDir(), "bgp.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	manager := NewManager(config.Config{
		LocalASN: 64512, RouterID: "192.0.2.1", BGPListenPort: -1,
		LocalAddressV4: "192.0.2.1",
		LocalAddressV6: "fd00::1",
	}, s)
	if err := manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer manager.Stop(ctx)

	// Add first peer with IPv6 address (static, since no other peer shares this IP).
	user1ID, err := s.AddUser(ctx, store.User{
		Name: "ipv6-user1", PeerIP: "fd00::2", PeerASN: 65001, Enabled: true,
		Networks: []string{"198.51.100.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.AddPeer(ctx, store.User{
		ID: user1ID, Name: "ipv6-user1", PeerIP: "fd00::2", PeerASN: 65001,
		Enabled: true, Networks: []string{"198.51.100.0/24"},
	}); err != nil {
		t.Fatal(err)
	}

	// Add second peer with same IPv6 address, different ASN.
	// This triggers dynamic neighbor + peer-group for both peers.
	user2ID, err := s.AddUser(ctx, store.User{
		Name: "ipv6-user2", PeerIP: "fd00::2", PeerASN: 65002, Enabled: true,
		Networks: []string{"198.51.101.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.AddPeer(ctx, store.User{
		ID: user2ID, Name: "ipv6-user2", PeerIP: "fd00::2", PeerASN: 65002,
		Enabled: true, Networks: []string{"198.51.101.0/24"},
	}); err != nil {
		t.Fatal(err)
	}

	// Verify dynamic neighbors have /128 prefix, not /32.
	var prefixes []string
	manager.server.ListDynamicNeighbor(ctx, &api.ListDynamicNeighborRequest{},
		func(dn *api.DynamicNeighbor) {
			prefixes = append(prefixes, dn.Prefix)
		})
	sort.Strings(prefixes)

	if len(prefixes) != 2 {
		t.Fatalf("expected 2 dynamic neighbors, got %d: %v", len(prefixes), prefixes)
	}

	for _, p := range prefixes {
		if !strings.HasSuffix(p, "/128") {
			t.Fatalf("IPv6 dynamic neighbor prefix should be /128, got %q", p)
		}
	}
}

func TestHasPeerGroupPolicyAfterSharedPeerDeleted(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(filepath.Join(t.TempDir(), "bgp.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	manager := NewManager(config.Config{
		LocalASN:       64512,
		RouterID:       "192.0.2.1",
		BGPListenPort:  -1,
		LocalAddressV4: "192.0.2.1",
	}, s)
	if err := manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer manager.Stop(ctx)

	// Step 1: Add first user at 192.0.2.100 — static peer (no other user shares IP).
	userAID, err := s.AddUser(ctx, store.User{
		Name: "user-a", PeerIP: "192.0.2.100", PeerASN: 65001, Enabled: true,
		Networks: []string{"198.51.100.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.AddPeer(ctx, store.User{
		ID: userAID, Name: "user-a", PeerIP: "192.0.2.100", PeerASN: 65001,
		Enabled: true, Networks: []string{"198.51.100.0/24"},
	}); err != nil {
		t.Fatal(err)
	}

	// Step 2: Add second user at same IP — upgrades both to peer-group.
	userBID, err := s.AddUser(ctx, store.User{
		Name: "user-b", PeerIP: "192.0.2.100", PeerASN: 65002, Enabled: true,
		Networks: []string{"198.51.101.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.AddPeer(ctx, store.User{
		ID: userBID, Name: "user-b", PeerIP: "192.0.2.100", PeerASN: 65002,
		Enabled: true, Networks: []string{"198.51.101.0/24"},
	}); err != nil {
		t.Fatal(err)
	}

	// Step 3: Delete second user.
	if err := manager.DeletePeer(ctx, userBID, "192.0.2.100"); err != nil {
		t.Fatalf("DeletePeer failed: %v", err)
	}

	// Step 4: hasPeerGroupPolicy for remaining user should be TRUE.
	// After deletion, user A is the only remaining peer at this IP,
	// but its GoBGP resource is still a peer-group (created when B was added).
	var userA store.User
	for _, u := range manager.peerConfigs {
		if u.ID == userAID {
			userA = u
			break
		}
	}
	if userA.ID == 0 {
		t.Fatal("user A not found in peerConfigs after deletion")
	}

	// Verify the peer group still exists in GoBGP.
	pgNameA := fmt.Sprintf("user_%d_pg", userAID)
	var pgFound bool
	manager.server.ListPeerGroup(ctx, &api.ListPeerGroupRequest{}, func(pg *api.PeerGroup) {
		if pg.Conf.PeerGroupName == pgNameA {
			pgFound = true
		}
	})
	if !pgFound {
		t.Fatal("user A's peer group should still exist in GoBGP after B deletion")
	}

	// BUG: hasPeerGroupPolicy returns false because no other user now shares the IP.
	// The remaining user still IS a peer-group in GoBGP, so this should be TRUE.
	if !manager.hasPeerGroupPolicy(ctx, userA) {
		t.Fatal("BUG: hasPeerGroupPolicy returns false for a peer that still has a peer-group in GoBGP")
	}
}

func TestPeerGroupPolicyRebuildUpdatesRemoveList(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(filepath.Join(t.TempDir(), "bgp.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	manager := NewManager(config.Config{
		LocalASN: 64512, RouterID: "192.0.2.1", BGPListenPort: -1,
		LocalAddressV4: "192.0.2.1",
	}, s)
	if err := manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer manager.Stop(ctx)

	// Phase 1: Add user A at a unique IP — static peer, no peer-group policy yet.
	userAID, err := s.AddUser(ctx, store.User{
		Name: "user-a", PeerIP: "192.0.2.50", PeerASN: 65001, Enabled: true,
		Networks: []string{"198.51.100.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.AddPeer(ctx, store.User{
		ID: userAID, Name: "user-a", PeerIP: "192.0.2.50", PeerASN: 65001,
		Enabled: true, Networks: []string{"198.51.100.0/24"},
	}); err != nil {
		t.Fatal(err)
	}

	// Phase 2: Add user B at the same IP. This triggers upgrade of user A
	// from static peer to peer-group, and then configureGlobalPolicyLocked
	// calls rebuildPeerGroupPolicyLocked for A. The REMOVE list must include
	// both A's and B's communities. Currently, if GoBGP returns "already
	// defined", rebuildPeerGroupPolicyLocked silently returns nil, keeping
	// the stale REMOVE list (or no policy at all after DeletePolicy).
	userBID, err := s.AddUser(ctx, store.User{
		Name: "user-b", PeerIP: "192.0.2.50", PeerASN: 65002, Enabled: true,
		Networks: []string{"198.51.101.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.AddPeer(ctx, store.User{
		ID: userBID, Name: "user-b", PeerIP: "192.0.2.50", PeerASN: 65002,
		Enabled: true, Networks: []string{"198.51.101.0/24"},
	}); err != nil {
		t.Fatal(err)
	}

	// User A should now have a peer-group policy.
	userA := store.User{ID: userAID, PeerIP: "192.0.2.50", PeerASN: 65001}
	if !manager.hasPeerGroupPolicy(ctx, userA) {
		t.Fatal("user A should have peer group policy after B's arrival")
	}

	// The REMOVE list in user A's peer-group policy should contain both
	// user A's and user B's user-ID communities.
	communityA := fmt.Sprintf("^%d:%d:0$", manager.cfg.LocalASN, userAID)
	communityB := fmt.Sprintf("^%d:%d:0$", manager.cfg.LocalASN, userBID)
	expected := []string{communityA, communityB}
	assertPolicyRemoveCommunities(t, manager, fmt.Sprintf("user_%d_policy", userAID), expected)
}

// assertPolicyRemoveCommunities checks that policy `name` has a REMOVE community
// list matching `expected` (order-independent).
func assertPolicyRemoveCommunities(t *testing.T, manager *Manager, name string, expected []string) {
	t.Helper()
	var got []string
	err := manager.server.ListPolicy(context.Background(), &api.ListPolicyRequest{
		Name: name,
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
		t.Fatalf("policy %q REMOVE communities = %v, want %v", name, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("policy %q REMOVE communities = %v, want %v", name, got, want)
		}
	}
}

// assertExportStatementCount checks that the wdbgp_export policy has exactly n statements.
func assertExportStatementCount(t *testing.T, manager *Manager, want int) {
	t.Helper()
	var count int
	err := manager.server.ListPolicy(context.Background(), &api.ListPolicyRequest{
		Name: exportPolicyName,
	}, func(policy *api.Policy) {
		count = len(policy.Statements)
	})
	if err != nil {
		t.Fatal(err)
	}
	if count != want {
		var names []string
		_ = manager.server.ListPolicy(context.Background(), &api.ListPolicyRequest{
			Name: exportPolicyName,
		}, func(policy *api.Policy) {
			for _, stmt := range policy.Statements {
				names = append(names, stmt.Name)
			}
		})
		t.Fatalf("export policy statement count = %d (names=%v), want %d", count, names, want)
	}
}

