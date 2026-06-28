//nolint:errcheck // test file, errors in cleanup intentionally ignored
package bgp

import (
	"context"
	"database/sql"
	"net/netip"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/andrey-vk/wdbgp/internal/config"
	"github.com/andrey-vk/wdbgp/internal/store"
)

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

func TestBuildRouteHonorsUserNextHop(t *testing.T) {
	manager := NewManager(config.Config{
		LocalASN: 64512, LocalAddressV4: "172.16.0.1", LocalAddressV6: "fd00::1",
	}, nil)

	// IPv4 prefix with user.NextHop override
	user := store.User{ID: 7, NextHop: "10.0.0.1"}
	v4prefix := netip.MustParsePrefix("8.8.8.0/24")
	v4route, err := manager.buildRoute(v4prefix, user, "cat", "svc", map[string]uint32{"cat": 10000, "cat|svc": 10001})
	if err != nil {
		t.Fatal(err)
	}
	if v4route.NextHop.String() != "10.0.0.1" {
		t.Fatalf("IPv4 next hop: expected 10.0.0.1 (user override), got %s", v4route.NextHop)
	}

	// IPv6 prefix with user.NextHop override
	userV6 := store.User{ID: 8, NextHop: "fd00::2"}
	v6prefix := netip.MustParsePrefix("2001:db8::/32")
	v6route, err := manager.buildRoute(v6prefix, userV6, "cat", "svc", map[string]uint32{"cat": 10000, "cat|svc": 10001})
	if err != nil {
		t.Fatal(err)
	}
	if v6route.NextHop.String() != "fd00::2" {
		t.Fatalf("IPv6 next hop: expected fd00::2 (user override), got %s", v6route.NextHop)
	}

	// User without NextHop set should still use config default
	userDefault := store.User{ID: 9}
	v4routeDefault, err := manager.buildRoute(v4prefix, userDefault, "cat", "svc", map[string]uint32{"cat": 10000, "cat|svc": 10001})
	if err != nil {
		t.Fatal(err)
	}
	if v4routeDefault.NextHop.String() != "172.16.0.1" {
		t.Fatalf("IPv4 next hop without override: expected 172.16.0.1 (config default), got %s", v4routeDefault.NextHop)
	}
}

func TestManagerStartsWithoutPeers(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "bgp.sqlite3"), config.Config{})
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
	s, err := store.Open(filepath.Join(t.TempDir(), "bgp.sqlite3"), config.Config{})
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

// =============================================================================
// Community export filter tests (Part B)
// =============================================================================

func TestBuildRouteHasUserCommunityButNotOtherUser(t *testing.T) {
	manager := NewManager(config.Config{
		LocalASN: 64512, LocalAddressV4: "172.16.0.1",
	}, nil)
	prefix := netip.MustParsePrefix("8.8.8.0/24")
	comms := map[string]uint32{"cat": 10000, "cat|svc": 10001}

	// Build route for user A (ID=1)
	routeA, err := manager.buildRoute(prefix, store.User{ID: 1}, "cat", "svc", comms)
	if err != nil {
		t.Fatal(err)
	}

	// Should have 3 communities: user A's ID, category, service
	if len(routeA.Communities) != 3 {
		t.Fatalf("expected 3 communities, got %d: %#v", len(routeA.Communities), routeA.Communities)
	}

	// Verify user A's community is present
	hasUserA := false
	hasUserB := false
	for _, c := range routeA.Communities {
		if c.GlobalAdmin == 64512 && c.LocalData1 == 1 && c.LocalData2 == 0 {
			hasUserA = true
		}
		if c.GlobalAdmin == 64512 && c.LocalData1 == 2 && c.LocalData2 == 0 {
			hasUserB = true
		}
	}
	if !hasUserA {
		t.Fatalf("route for user A missing user A's community: %#v", routeA.Communities)
	}
	if hasUserB {
		t.Fatalf("route for user A should NOT have user B's community: %#v", routeA.Communities)
	}
}

func TestBuildRouteIncludesCategoryAndServiceCommunities(t *testing.T) {
	manager := NewManager(config.Config{
		LocalASN: 64512, LocalAddressV4: "172.16.0.1",
	}, nil)
	prefix := netip.MustParsePrefix("149.154.160.0/20")
	comms := map[string]uint32{"Messengers": 20000, "Messengers|Telegram": 20001}

	route, err := manager.buildRoute(prefix, store.User{ID: 5}, "Messengers", "Telegram", comms)
	if err != nil {
		t.Fatal(err)
	}

	if len(route.Communities) != 3 {
		t.Fatalf("expected 3 communities, got %d: %#v", len(route.Communities), route.Communities)
	}

	var catFound, svcFound bool
	for _, c := range route.Communities {
		if c.LocalData1 == 0 && c.LocalData2 == 20000 {
			catFound = true
		}
		if c.LocalData1 == 0 && c.LocalData2 == 20001 {
			svcFound = true
		}
	}
	if !catFound {
		t.Fatalf("category community 20000 not found: %#v", route.Communities)
	}
	if !svcFound {
		t.Fatalf("service community 20001 not found: %#v", route.Communities)
	}
}

func TestBuildRouteSkipsCommunityForUnknownCategory(t *testing.T) {
	manager := NewManager(config.Config{
		LocalASN: 64512, LocalAddressV4: "172.16.0.1",
	}, nil)
	prefix := netip.MustParsePrefix("1.1.1.0/24")
	// Category "Unknown" has no community in the map
	comms := map[string]uint32{"Known": 10000}

	route, err := manager.buildRoute(prefix, store.User{ID: 3}, "Unknown", "unknown-svc", comms)
	if err != nil {
		t.Fatal(err)
	}

	// Should have only 1 community: user ID. Category/service are unknown so skipped.
	if len(route.Communities) != 1 {
		t.Fatalf("expected 1 community (user only), got %d: %#v", len(route.Communities), route.Communities)
	}
	if route.Communities[0].GlobalAdmin != 64512 || route.Communities[0].LocalData1 != 3 {
		t.Fatalf("expected user community 64512:3:0, got %d:%d:%d",
			route.Communities[0].GlobalAdmin, route.Communities[0].LocalData1, route.Communities[0].LocalData2)
	}
}

func TestBuildRouteIPv6NoPanic(t *testing.T) {
	manager := NewManager(config.Config{
		LocalASN: 64512, LocalAddressV4: "172.16.0.1", LocalAddressV6: "fd00::1",
	}, nil)
	prefix := netip.MustParsePrefix("2001:db8::/32")
	comms := map[string]uint32{"cat": 10000}
	route, err := manager.buildRoute(prefix, store.User{ID: 1}, "cat", "svc", comms)
	if err != nil {
		t.Fatal(err)
	}

	// IPv6 routes must use MpReachNLRIAttribute instead of NextHopAttribute
	// to avoid As4() panic.
	attrs := []PathAttribute{
		OriginAttribute(OriginIGP),
		&ASPathAttribute{ASN: 64512},
		&MpReachNLRIAttribute{NextHop: route.NextHop},
		&LargeCommunitiesAttribute{Communities: route.Communities},
	}
	update := &UpdateMessage{
		PathAttributes: attrs,
		NLRI:           []netip.Prefix{route.Prefix},
	}
	_ = update.Serialize()

	// Also verify IPv4 routes still use NextHopAttribute correctly
	v4prefix := netip.MustParsePrefix("8.8.8.0/24")
	v4route, err := manager.buildRoute(v4prefix, store.User{ID: 1}, "cat", "svc", comms)
	if err != nil {
		t.Fatal(err)
	}
	v4attrs := []PathAttribute{
		OriginAttribute(OriginIGP),
		&ASPathAttribute{ASN: 64512},
		&NextHopAttribute{NextHop: v4route.NextHop},
		&LargeCommunitiesAttribute{Communities: v4route.Communities},
	}
	v4update := &UpdateMessage{
		PathAttributes: v4attrs,
		NLRI:           []netip.Prefix{v4route.Prefix},
	}
	_ = v4update.Serialize()
}

func TestReconcileAssignsRoutesPerPeer(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(filepath.Join(t.TempDir(), "bgp.sqlite3"), config.Config{})
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
	firstRoutes := manager.peerRoutes["192.0.2.2:65001"]
	firstPrefixes := routePrefixStrings(firstRoutes)
	sort.Strings(firstPrefixes)
	assertPrefixes(t, firstPrefixes, []string{"8.8.4.0/24", "8.8.8.0/24"})

	// Second peer (user 2 with "chat/telegram" service) should get chat prefix.
	secondRoutes := manager.peerRoutes["192.0.2.3:65002"]
	secondPrefixes := routePrefixStrings(secondRoutes)
	assertPrefixes(t, secondPrefixes, []string{"149.154.160.0/20"})

	// Verify communities on first peer's route.
	if len(firstRoutes) > 0 {
		// First route should have user community for user 1.
		hasUserComm := false
		for _, c := range firstRoutes[0].Communities {
			if c.GlobalAdmin == 64512 && c.LocalData1 == uint32(firstID) { //nolint:gosec // value is in safe range
				hasUserComm = true
			}
		}
		if !hasUserComm {
			t.Fatalf("first peer route missing user community: %#v", firstRoutes[0].Communities)
		}
	}
}

func TestReconcileClearsRoutesOnModeChange(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(filepath.Join(t.TempDir(), "bgp.sqlite3"), config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	userID, err := s.AddUser(ctx, store.User{
		Name: "modeswitch", PeerIP: "192.0.2.2", PeerASN: 65001, Enabled: true,
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
		return store.SetUserSelection(ctx, tx, userID, []string{"video"}, nil)
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

	// Verify routes were announced.
	manager.mu.Lock()
	routes := manager.peerRoutes["192.0.2.2:65001"]
	manager.mu.Unlock()
	if len(routes) == 0 {
		t.Fatal("expected routes after initial reconcile, got none")
	}

	// Change user to mode 2 (IPRanges, disabled by default).
	// Enable it first so the disabled filter doesn't hide the bug:
	// UpdatePeer clears peerRoutes, so Reconcile sees nil vs empty
	// and never withdraws old routes from the speaker.
	_, err = s.DB.ExecContext(ctx, "UPDATE catalog_modes SET enabled = 1 WHERE id = 2")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.DB.ExecContext(ctx, "UPDATE users SET catalog_mode_id = 2 WHERE id = ?", userID)
	if err != nil {
		t.Fatal(err)
	}

	// Fetch updated user from store.
	users, err := s.Users(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	var updatedUser store.User
	for _, u := range users {
		if u.ID == userID {
			updatedUser = u
			break
		}
	}

	// Update peer in manager and reconcile.
	if err := manager.UpdatePeer(ctx, updatedUser); err != nil {
		t.Fatal(err)
	}
	if err := manager.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}

	// Verify routes were cleared — check speaker's peer routes (the wire),
	// not manager's peerRoutes cache which UpdatePeer already emptied.
	manager.speaker.mu.Lock()
	speakerPeer := manager.speaker.peers["192.0.2.2:65001"]
	manager.speaker.mu.Unlock()
	if speakerPeer != nil {
		speakerPeer.mu.Lock()
		routes = speakerPeer.routes
		speakerPeer.mu.Unlock()
	}
	if len(routes) != 0 {
		t.Errorf("peer still has %d routes on speaker after mode change, want 0", len(routes))
	}
}

func TestDeletePeerHandlesSameIPDifferentASN(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(filepath.Join(t.TempDir(), "bgp.sqlite3"), config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Create two users at the same IP, different ASNs
	userAID, err := s.AddUser(ctx, store.User{
		Name: "userA", PeerIP: "192.0.2.2", PeerASN: 65001, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	userBID, err := s.AddUser(ctx, store.User{
		Name: "userB", PeerIP: "192.0.2.2", PeerASN: 65002, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = userBID // used later in verification

	manager := NewManager(config.Config{
		LocalASN: 64512, RouterID: "192.0.2.1", BGPListenPort: -1,
		LocalAddressV4: "192.0.2.1",
	}, s)
	if err := manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer manager.Stop(ctx)

	// Simulate what reconcileLocked does: store routes with ip:asn keys
	manager.mu.Lock()
	manager.peerRoutes = map[string][]Route{
		"192.0.2.2:65001": {{Prefix: netip.MustParsePrefix("8.8.8.0/24")}},
		"192.0.2.2:65002": {{Prefix: netip.MustParsePrefix("8.8.4.0/24")}},
	}
	manager.mu.Unlock()

	// Delete user A by peerIP + userID — must delete only userA, not userB
	err = manager.DeletePeer(ctx, "192.0.2.2", userAID)
	if err != nil {
		t.Fatal(err)
	}

	manager.mu.Lock()
	defer manager.mu.Unlock()

	// Verify user A's peerRoutes are cleaned (using ip:asn key).
	if _, ok := manager.peerRoutes["192.0.2.2:65001"]; ok {
		t.Fatal("peerRoutes for user A (65001) should have been deleted")
	}

	// Verify user B's peerRoutes are preserved.
	if _, ok := manager.peerRoutes["192.0.2.2:65002"]; !ok {
		t.Fatal("peerRoutes for user B (65002) should still exist")
	}

	// Verify user A is removed from peerConfigs and user B remains.
	foundA := false
	foundB := false
	for _, u := range manager.peerConfigs {
		if u.ID == userAID {
			foundA = true
		}
		if u.ID == userBID {
			foundB = true
		}
	}
	if foundA {
		t.Fatal("user A should have been deleted from peerConfigs")
	}
	if !foundB {
		t.Fatal("user B should still be in peerConfigs")
	}
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

func TestDynamicPeerIsPassiveOnly(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(filepath.Join(t.TempDir(), "bgp.sqlite3"), config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Add a dynamic peer (0.0.0.0) — must be passive-only, never dial.
	_, err = s.AddUser(ctx, store.User{
		Name:        "dynamic-peer",
		PeerIP:      "0.0.0.0",
		PeerASN:     65001,
		Enabled:     true,
		BGPPassword: "secret",
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

	// 1. Verify the peer exists in the speaker's peer map.
	manager.speaker.mu.Lock()
	p, ok := manager.speaker.peers["0.0.0.0:65001"]
	manager.speaker.mu.Unlock()
	if !ok {
		t.Fatal("dynamic peer not found in speaker peers")
	}

	// 2. Verify Port=-1 (passive-only, never dials).
	cfg := p.PeerConfig()
	if cfg.Port != -1 {
		t.Fatalf("dynamic peer Port should be -1 (passive only), got %d", cfg.Port)
	}

	// 3. Verify the peer does not actively dial: after a brief wait,
	//    the state must not be ESTABLISHED (which requires a TCP connection).
	time.Sleep(100 * time.Millisecond)
	state := p.State()
	if state == StateEstablished || state == StateOpenSent || state == StateOpenConfirm {
		t.Fatalf("dynamic peer should not actively connect, state=%s", state)
	}
}
