package bgp

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	api "github.com/osrg/gobgp/v3/api"

	"github.com/andrey-vk/wdbgp/internal/config"
	"github.com/andrey-vk/wdbgp/internal/store"
)

func TestPathCarriesUserCommunities(t *testing.T) {
	manager := NewManager(config.Config{
		LocalASN: 64512, LocalAddressV4: "172.16.0.1", LocalAddressV6: "fd00::1",
	}, nil)
	path, err := manager.path("149.154.160.0/20", []int64{2, 7})
	if err != nil {
		t.Fatal(err)
	}
	var communities api.LargeCommunitiesAttribute
	found := false
	for _, attribute := range path.Pattrs {
		if attribute.MessageIs(&communities) {
			if err := attribute.UnmarshalTo(&communities); err != nil {
				t.Fatal(err)
			}
			found = true
		}
	}
	if !found || len(communities.Communities) != 2 {
		t.Fatalf("large communities not found: %#v", path.Pattrs)
	}
	if communities.Communities[1].LocalData1 != 7 {
		t.Fatalf("unexpected communities: %#v", communities.Communities)
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
		(1, 'test', 'dual-stack', '203.0.113.0/24'),
		(1, 'test', 'dual-stack', '2001:db8::/32')`); err != nil {
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
	if _, ok := manager.installed["203.0.113.0/24"]; !ok {
		t.Fatalf("IPv4 path was not installed: %#v", manager.installed)
	}
	if _, ok := manager.installed["2001:db8::/32"]; ok {
		t.Fatalf("IPv6 path installed without a local IPv6 address: %#v", manager.installed)
	}
}
