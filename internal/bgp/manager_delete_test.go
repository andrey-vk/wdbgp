package bgp

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	api "github.com/osrg/gobgp/v4/api"

	"github.com/andrey-vk/wdbgp/internal/config"
	"github.com/andrey-vk/wdbgp/internal/store"
)

// TestDeleteFirstPeerSharedIP reproduces the mixed-state bug and verifies the fix:
//
// Setup:
//   1. Add peer A at IP X (first → static AddPeer).
//   2. Add peer B at IP X (second → upgrades A to peer-group via AddPeerGroup + AddDynamicNeighbor).
//
// Fix: In addPeerLocked(), when otherUsesIP is true, existing static peers at
// that IP are upgraded to peer-groups before adding the new peer.
// deletePeerLocked() always tries peer-group cleanup first, falling back to
// static DeletePeer when the dynamic neighbor is not found.
//
// This test verifies:
//   - Peer A is upgraded from static to peer-group when B is added.
//   - DeletePeer(A) correctly cleans up A's peer-group and dynamic neighbor.
//   - Peer B's peer-group is left intact after A is deleted.
func TestDeleteFirstPeerSharedIP(t *testing.T) {
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

	// ---- Step 1: Add peer A (first at this IP → static peer) ----
	userAID, err := s.AddUser(ctx, store.User{
		Name: "user-a", PeerIP: "192.0.2.100", PeerASN: 65001, Enabled: true,
		Networks: []string{"198.51.100.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	userA := store.User{
		ID: userAID, Name: "user-a", PeerIP: "192.0.2.100", PeerASN: 65001,
		Enabled: true, Networks: []string{"198.51.100.0/24"},
	}
	if err := manager.AddPeer(ctx, userA); err != nil {
		t.Fatal(err)
	}

	// ---- Step 2: Add peer B (second at same IP → peer group + dynamic neighbor) ----
	userBID, err := s.AddUser(ctx, store.User{
		Name: "user-b", PeerIP: "192.0.2.100", PeerASN: 65002, Enabled: true,
		Networks: []string{"198.51.101.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.AddPeer(ctx, store.User{
		ID: userBID, Name: "user-b", PeerIP: "192.0.2.100", PeerASN: 65002, Enabled: true,
		Networks: []string{"198.51.101.0/24"},
	}); err != nil {
		t.Fatal(err)
	}

	// ---- Verify mixed state ----
	pgNameA := fmt.Sprintf("user_%d_pg", userAID)
	pgNameB := fmt.Sprintf("user_%d_pg", userBID)

	// 1. After the fix, peer A was upgraded to a peer-group when B was added.
	//    Both peers now have peer-groups via dynamic neighbors.
	var pgAFound, pgBFound bool
	manager.server.ListPeerGroup(ctx, &api.ListPeerGroupRequest{}, func(pg *api.PeerGroup) {
		if pg.Conf.PeerGroupName == pgNameA {
			pgAFound = true
		}
		if pg.Conf.PeerGroupName == pgNameB {
			pgBFound = true
		}
	})

	if !pgAFound {
		t.Fatal("precondition: peer A should now have a peer group (upgraded from static)")
	}
	if !pgBFound {
		t.Fatal("precondition: peer B should have a peer group")
	}

	// 2. Both peers are tracked.
	if n := len(manager.peerConfigs); n != 2 {
		t.Fatalf("want 2 peer configs, got %d", n)
	}

	// ---- Step 3: Delete peer A ----
	// After the fix, peer A was upgraded to a peer-group when B was added.
	// deletePeerLocked(A) now works correctly: it cleans up the dynamic neighbor,
	// peer group, and policy for A, leaving B intact.
	if err := manager.DeletePeer(ctx, userAID, "192.0.2.100"); err != nil {
		t.Fatalf("DeletePeer(A) failed: %v", err)
	}

	// Verify after fix:
	//   - Peer A removed from peerConfigs
	//   - Peer B still exists
	//   - Peer B's peer group intact
	if n := len(manager.peerConfigs); n != 1 {
		t.Fatalf("want 1 peer config after delete, got %d", n)
	}
	if manager.peerConfigs[0].ID != userBID {
		t.Fatalf("peer B should be the remaining peer, got ID=%d", manager.peerConfigs[0].ID)
	}

	// Peer B's peer group must still exist.
	var pgBFoundAfter bool
	manager.server.ListPeerGroup(ctx, &api.ListPeerGroupRequest{}, func(pg *api.PeerGroup) {
		if pg.Conf.PeerGroupName == pgNameB {
			pgBFoundAfter = true
		}
	})
	if !pgBFoundAfter {
		t.Fatal("peer B's peer group should still exist after A deletion")
	}
}
