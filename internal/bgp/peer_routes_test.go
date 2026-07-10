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

// TestResyncFailureRearmsNeedsUpdate — a write failure mid-resync must
// re-arm needsUpdate and surface an error, never report success: the
// manager records the full set as announced, so a swallowed error would
// freeze the peer at a partial route set forever (live-deployment bug:
// desired 2380 routes, router stuck at 10). lastSent must also stay
// unadvanced so the retry re-sends everything.
func TestResyncFailureRearmsNeedsUpdate(t *testing.T) {
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

	if err := p.resyncRoutes(server); err == nil {
		t.Fatal("resyncRoutes returned nil on a dead connection")
	}
	if !p.needsUpdate.Load() {
		t.Fatal("needsUpdate not re-armed after failed send")
	}
	if len(p.lastSent) != 0 {
		t.Fatalf("lastSent advanced to %v after a failed resync", p.lastSent)
	}
}

// TestResyncWithdrawsRemovedPrefixes — shrinking the desired set must emit
// withdrawals for the removed prefixes, computed by the peer itself from
// what it previously delivered (the manager no longer sends withdrawal
// deltas, and a failed withdrawal write must propagate — Codex round-1 #1).
func TestResyncWithdrawsRemovedPrefixes(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close() //nolint:errcheck // test cleanup
	defer client.Close() //nolint:errcheck // test cleanup

	// Drain and decode everything the peer writes.
	type result struct {
		nlri      []netip.Prefix
		withdrawn []netip.Prefix
	}
	decoded := make(chan result, 16)
	go func() {
		for {
			msg, err := ReadMessage(client)
			if err != nil {
				close(decoded)
				return
			}
			if u, ok := msg.(*UpdateMessage); ok {
				decoded <- result{nlri: u.NLRI, withdrawn: u.WithdrawnRoutes}
			}
		}
	}()

	p := NewPeer(
		PeerConfig{Address: netip.MustParseAddr("192.0.2.1"), ASN: 65001},
		SpeakerConfig{ASN: 64512},
		slog.New(slog.NewTextHandler(nil2Writer{}, nil)),
		nil,
	)
	nextHop := netip.MustParseAddr("192.0.2.2")
	a := netip.MustParsePrefix("10.1.0.0/16")
	b := netip.MustParsePrefix("10.2.0.0/16")
	c := netip.MustParsePrefix("10.3.0.0/16")

	p.routes = []Route{
		{Prefix: a, NextHop: nextHop},
		{Prefix: b, NextHop: nextHop},
		{Prefix: c, NextHop: nextHop},
	}
	if err := p.resyncRoutes(server); err != nil {
		t.Fatalf("initial resync: %v", err)
	}
	first := <-decoded
	if len(first.nlri) != 3 || len(first.withdrawn) != 0 {
		t.Fatalf("initial resync: nlri=%v withdrawn=%v, want 3 NLRI in one batched UPDATE",
			first.nlri, first.withdrawn)
	}

	// Drop b — the resync must withdraw it before re-announcing a and c.
	p.routes = []Route{
		{Prefix: a, NextHop: nextHop},
		{Prefix: c, NextHop: nextHop},
	}
	if err := p.resyncRoutes(server); err != nil {
		t.Fatalf("second resync: %v", err)
	}
	withdrawal := <-decoded
	if len(withdrawal.withdrawn) != 1 || withdrawal.withdrawn[0] != b {
		t.Fatalf("second resync withdrawal = %v, want [%s]", withdrawal.withdrawn, b)
	}
	announce := <-decoded
	if len(announce.nlri) != 2 {
		t.Fatalf("second resync re-announce NLRI = %v, want [a c]", announce.nlri)
	}
	if got := p.lastSent; len(got) != 2 {
		t.Fatalf("lastSent = %v after successful resync, want the 2 delivered prefixes", got)
	}
}

// nil2Writer discards log output.
type nil2Writer struct{}

func (nil2Writer) Write(p []byte) (int, error) { return len(p), nil }
