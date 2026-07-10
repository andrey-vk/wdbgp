package bgp

import (
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"
)

// countingRecorder counts callback invocations (≈ received UPDATE messages
// carrying NLRI) in addition to collecting the prefixes.
type countingRecorder struct {
	nlriRecorder
	mu2   sync.Mutex
	calls int
}

func (r *countingRecorder) callback(prefixes []netip.Prefix) {
	r.mu2.Lock()
	r.calls++
	r.mu2.Unlock()
	r.nlriRecorder.callback(prefixes)
}

func (r *countingRecorder) callCount() int {
	r.mu2.Lock()
	defer r.mu2.Unlock()
	return r.calls
}

// TestRouteAnnouncementBatched — routes sharing an attribute set (next hop +
// communities) must be packed into common UPDATE messages, not sent one
// message per route, and every announced prefix must arrive intact.
func TestRouteAnnouncementBatched(t *testing.T) {
	serverPeer := PeerConfig{
		ID:      1,
		Address: netip.MustParseAddr("127.0.0.1"),
		ASN:     65001,
	}
	recorder := &countingRecorder{}
	speaker, client := startSpeakerAndClient(t, 64512, serverPeer, 65001, recorder.callback)

	waitForPeerState(t, client, StateEstablished, 10*time.Second)
	waitForSpeakerPeerState(t, speaker, stateKey("127.0.0.1", 65001), StateEstablished, 10*time.Second)

	// 250 routes in two attribute groups: 200 share one community set (→ 2
	// chunks of maxPrefixesPerUpdate) and 50 share another (→ 1 chunk).
	comA := []LargeCommunity{{GlobalAdmin: 64512, LocalData1: 1, LocalData2: 0}}
	comB := []LargeCommunity{{GlobalAdmin: 64512, LocalData1: 2, LocalData2: 7}}
	nextHop := netip.MustParseAddr("192.0.2.2")
	var routes []Route
	for i := 0; i < 200; i++ {
		routes = append(routes, Route{
			Prefix:      netip.MustParsePrefix(fmt.Sprintf("10.%d.%d.0/24", i/250, i%250)),
			NextHop:     nextHop,
			Communities: comA,
		})
	}
	for i := 0; i < 50; i++ {
		routes = append(routes, Route{
			Prefix:      netip.MustParsePrefix(fmt.Sprintf("172.16.%d.0/24", i)),
			NextHop:     nextHop,
			Communities: comB,
		})
	}

	if err := speaker.Announce(serverPeer.Address, serverPeer.ASN, routes); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for recorder.len() < len(routes) {
		if time.Now().After(deadline) {
			t.Fatalf("received %d/%d prefixes before timeout", recorder.len(), len(routes))
		}
		time.Sleep(20 * time.Millisecond)
	}

	got := map[string]bool{}
	for _, p := range recorder.snapshot() {
		got[p.String()] = true
	}
	for _, r := range routes {
		if !got[r.Prefix.String()] {
			t.Fatalf("prefix %s never received", r.Prefix)
		}
	}

	// 250 routes must arrive in ~3 UPDATEs (2 chunks + 1), not 250. Allow
	// slack for how the client surfaces them, but far below one-per-route.
	if calls := recorder.callCount(); calls > 10 {
		t.Fatalf("received %d UPDATE messages for %d routes, want batched (≤10)", calls, len(routes))
	}
}

// TestSendRoutesFailureRearmsNeedsUpdate — a write failure mid-announcement
// must re-arm needsUpdate and surface an error, never report success: the
// manager records the full set as announced, so a swallowed error would
// freeze the peer at a partial route set forever (live-deployment bug:
// desired 2380 routes, router stuck at 10).
func TestSendRoutesFailureRearmsNeedsUpdate(t *testing.T) {
	server, client := net.Pipe()
	client.Close()       //nolint:errcheck,gosec // every write into the pipe must now fail
	defer server.Close() //nolint:errcheck // test cleanup

	p := NewPeer(
		PeerConfig{Address: netip.MustParseAddr("192.0.2.1"), ASN: 65001},
		SpeakerConfig{ASN: 64512},
		slog.New(slog.NewTextHandler(nil2Writer{}, nil)),
		nil,
	)
	p.routes = []Route{{
		Prefix:  netip.MustParsePrefix("10.0.0.0/8"),
		NextHop: netip.MustParseAddr("192.0.2.2"),
	}}

	if err := p.sendRoutes(server); err == nil {
		t.Fatal("sendRoutes returned nil on a dead connection")
	}
	if !p.needsUpdate.Load() {
		t.Fatal("needsUpdate not re-armed after failed send")
	}
}

// nil2Writer discards log output.
type nil2Writer struct{}

func (nil2Writer) Write(p []byte) (int, error) { return len(p), nil }
