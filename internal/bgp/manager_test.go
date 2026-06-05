package bgp

import (
	"context"
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
