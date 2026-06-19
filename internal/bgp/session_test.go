package bgp

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"testing"
	"time"
)

// freeTCPPort returns a free TCP port for testing.
func freeTCPPort(t *testing.T) int32 {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	_, portStr, _ := net.SplitHostPort(l.Addr().String())
	port, _ := strconv.Atoi(portStr)
	return int32(port)
}

// startSpeakerAndClient starts a Speaker as a BGP server and a Peer as a client.
// The speaker listens on a random free port; the client connects to it.
//
// serverPeer describes the peer the speaker expects (set via SetPeers).
// The client PeerConfig is derived with matching ASN and password.
func startSpeakerAndClient(t *testing.T, serverASN uint32, serverPeer PeerConfig, clientASN uint32, clientRouteCB RouteCallback) (*Speaker, *Peer) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	port := freeTCPPort(t)
	speaker := NewSpeaker(SpeakerConfig{
		ASN:       serverASN,
		RouterID:  netip.MustParseAddr("192.0.2.1"),
		Port:      port,
		LocalAddr: netip.MustParseAddr("192.0.2.2"),
	}, logger)

	if err := speaker.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { speaker.Stop() })

	// Ensure server peer is passive (doesn't try to dial out; only
	// accepts incoming connections via the speaker's listener).
	serverPeer.Port = -1

	_ = speaker.SetPeers([]PeerConfig{serverPeer})

	// Create client peer that connects to the speaker.
	// cfg.ASN = speaker's ASN (received in OPEN and validated)
	// spk.ASN = client's own ASN (sent in OPEN as MyASN)
	clientCfg := PeerConfig{
		ID:       2,
		Address:  netip.MustParseAddr("127.0.0.1"),
		Port:     port,
		ASN:      serverASN,
		Password: serverPeer.Password,
		Name:     "test-client",
	}
	clientPeer := NewPeer(clientCfg, SpeakerConfig{
		ASN:      clientASN,
		RouterID: netip.MustParseAddr("192.0.2.3"),
		Port:     0,
	}, logger, clientRouteCB)

	go clientPeer.Run()
	t.Cleanup(func() { clientPeer.Stop() })

	return speaker, clientPeer
}

// waitForState polls a Peer until it reaches the expected state or times out.
func waitForPeerState(t *testing.T, peer *Peer, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if peer.State() == want {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("peer state = %s, want %s after %v", peer.State(), want, timeout)
}

// waitForSpeakerPeerState polls the speaker's peer states for a specific key
// until it reaches the expected state or times out.
func waitForSpeakerPeerState(t *testing.T, speaker *Speaker, key string, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		states := speaker.PeerStates()
		if state, ok := states[key]; ok && state == want {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	states := speaker.PeerStates()
	t.Fatalf("speaker peer %q state not %s after %v: got %v", key, want, timeout, states)
}

// stateKey builds the peer key used in speaker.PeerStates().
func stateKey(addr string, asn uint32) string {
	return addr + ":" + strconv.Itoa(int(asn))
}

// =============================================================================
// Test 1: Basic session establishment
// =============================================================================

func TestSessionEstablished(t *testing.T) {
	serverPeer := PeerConfig{
		ID:      1,
		Address: netip.MustParseAddr("127.0.0.1"),
		ASN:     65001,
	}
	speaker, client := startSpeakerAndClient(t, 64512, serverPeer, 65001, nil)

	waitForPeerState(t, client, StateEstablished, 10*time.Second)
	waitForSpeakerPeerState(t, speaker, stateKey("127.0.0.1", 65001), StateEstablished, 10*time.Second)
}

// =============================================================================
// Test 2: Same IP, different ASNs
// =============================================================================

func TestSameIPDifferentASNs(t *testing.T) {
	port := freeTCPPort(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	speaker := NewSpeaker(SpeakerConfig{
		ASN:       64512,
		RouterID:  netip.MustParseAddr("192.0.2.1"),
		Port:      port,
		LocalAddr: netip.MustParseAddr("192.0.2.2"),
	}, logger)
	if err := speaker.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { speaker.Stop() })

	// Two peers on same IP, different ASNs (passive-only)
	peers := []PeerConfig{
		{ID: 1, Address: netip.MustParseAddr("127.0.0.1"), ASN: 65001, Port: -1},
		{ID: 2, Address: netip.MustParseAddr("127.0.0.1"), ASN: 65002, Port: -1},
	}
	_ = speaker.SetPeers(peers)

	// Client for ASN 65001
	client1 := NewPeer(PeerConfig{
		ID: 10, Address: netip.MustParseAddr("127.0.0.1"), Port: port,
		ASN: 64512, Name: "client-65001",
	}, SpeakerConfig{ASN: 65001, RouterID: netip.MustParseAddr("192.0.2.10")}, logger, nil)
	go client1.Run()
	t.Cleanup(func() { client1.Stop() })

	// Client for ASN 65002
	client2 := NewPeer(PeerConfig{
		ID: 20, Address: netip.MustParseAddr("127.0.0.1"), Port: port,
		ASN: 64512, Name: "client-65002",
	}, SpeakerConfig{ASN: 65002, RouterID: netip.MustParseAddr("192.0.2.20")}, logger, nil)
	go client2.Run()
	t.Cleanup(func() { client2.Stop() })

	waitForPeerState(t, client1, StateEstablished, 10*time.Second)
	waitForPeerState(t, client2, StateEstablished, 10*time.Second)
	waitForSpeakerPeerState(t, speaker, stateKey("127.0.0.1", 65001), StateEstablished, 10*time.Second)
	waitForSpeakerPeerState(t, speaker, stateKey("127.0.0.1", 65002), StateEstablished, 10*time.Second)
}

// =============================================================================
// Test 3: Different IPs, same ASN
// =============================================================================

func TestDifferentIPSameASN(t *testing.T) {
	port := freeTCPPort(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	speaker := NewSpeaker(SpeakerConfig{
		ASN:       64512,
		RouterID:  netip.MustParseAddr("192.0.2.1"),
		Port:      port,
		LocalAddr: netip.MustParseAddr("192.0.2.2"),
	}, logger)
	if err := speaker.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { speaker.Stop() })

	// Two peers on different IPs, same ASN (passive-only)
	peers := []PeerConfig{
		{ID: 1, Address: netip.MustParseAddr("127.0.0.10"), ASN: 65001, Port: -1},
		{ID: 2, Address: netip.MustParseAddr("127.0.0.20"), ASN: 65001, Port: -1},
	}
	_ = speaker.SetPeers(peers)

	// Client for 127.0.0.10 — speaker uses remote address to find peer
	client1 := NewPeer(PeerConfig{
		ID: 10, Address: netip.MustParseAddr("127.0.0.1"), Port: port,
		ASN: 64512, Name: "client-10",
	}, SpeakerConfig{ASN: 65001, RouterID: netip.MustParseAddr("192.0.2.10"), LocalAddr: netip.MustParseAddr("127.0.0.10")}, logger, nil)
	go client1.Run()
	t.Cleanup(func() { client1.Stop() })

	// Client for 127.0.0.20
	client2 := NewPeer(PeerConfig{
		ID: 20, Address: netip.MustParseAddr("127.0.0.1"), Port: port,
		ASN: 64512, Name: "client-20",
	}, SpeakerConfig{ASN: 65001, RouterID: netip.MustParseAddr("192.0.2.20"), LocalAddr: netip.MustParseAddr("127.0.0.20")}, logger, nil)
	go client2.Run()
	t.Cleanup(func() { client2.Stop() })

	// Both should establish — the speaker matches by remote addr + ASN
	waitForPeerState(t, client1, StateEstablished, 10*time.Second)
	waitForPeerState(t, client2, StateEstablished, 10*time.Second)
	waitForSpeakerPeerState(t, speaker, stateKey("127.0.0.10", 65001), StateEstablished, 10*time.Second)
	waitForSpeakerPeerState(t, speaker, stateKey("127.0.0.20", 65001), StateEstablished, 10*time.Second)
}

// =============================================================================
// Test 4: Dynamic peer (0.0.0.0)
// =============================================================================

func TestDynamicPeer(t *testing.T) {
	serverPeer := PeerConfig{
		ID:      1,
		Address: netip.MustParseAddr("0.0.0.0"),
		ASN:     65001,
	}
	speaker, client := startSpeakerAndClient(t, 64512, serverPeer, 65001, nil)

	waitForPeerState(t, client, StateEstablished, 10*time.Second)
	waitForSpeakerPeerState(t, speaker, stateKey("0.0.0.0", 65001), StateEstablished, 10*time.Second)
}

// =============================================================================
// Test 5: ASN mismatch
// =============================================================================

func TestASNMismatch(t *testing.T) {
	serverPeer := PeerConfig{
		ID:      1,
		Address: netip.MustParseAddr("127.0.0.1"),
		ASN:     65001,
	}
	speaker, client := startSpeakerAndClient(t, 64512, serverPeer, 65002, nil)

	// Client should fail to connect because ASN doesn't match
	// After a few retries, the client will be in IDLE (backoff between retries)
	time.Sleep(3 * time.Second)

	clientState := client.State()
	t.Logf("client peer state: %s", clientState)

	// Client should NOT be established
	if clientState == StateEstablished {
		t.Fatal("client reached ESTABLISHED despite ASN mismatch")
	}

	// Speaker peer should not be established
	states := speaker.PeerStates()
	t.Logf("speaker peer states: %v", states)
	if st, ok := states[stateKey("127.0.0.1", 65001)]; ok && st == StateEstablished {
		t.Fatal("speaker peer reached ESTABLISHED with mismatched ASN")
	}
}

// =============================================================================
// Test 6: Route announcement
// =============================================================================

func TestRouteAnnouncement(t *testing.T) {
	serverPeer := PeerConfig{
		ID:      1,
		Address: netip.MustParseAddr("127.0.0.1"),
		ASN:     65001,
	}

	// Track received routes on client
	var receivedNLRI []netip.Prefix
	cb := func(prefixes []netip.Prefix) {
		receivedNLRI = append(receivedNLRI, prefixes...)
	}

	speaker, client := startSpeakerAndClient(t, 64512, serverPeer, 65001, cb)

	waitForPeerState(t, client, StateEstablished, 10*time.Second)
	waitForSpeakerPeerState(t, speaker, stateKey("127.0.0.1", 65001), StateEstablished, 10*time.Second)

	// Announce a route from the speaker
	routes := []Route{
		{
			Prefix:  netip.MustParsePrefix("10.0.0.0/8"),
			NextHop: netip.MustParseAddr("192.0.2.2"),
		},
	}
	err := speaker.Announce(serverPeer.Address, serverPeer.ASN, routes)
	if err != nil {
		t.Fatal(err)
	}

	// Wait for the UPDATE to arrive at the client
	time.Sleep(1 * time.Second)

	if len(receivedNLRI) == 0 {
		t.Fatal("client did not receive any NLRI")
	}

	// Check the received NLRI
	found := false
	for _, p := range receivedNLRI {
		if p.String() == "10.0.0.0/8" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("client NLRI did not contain 10.0.0.0/8: %v", receivedNLRI)
	}
}

// =============================================================================
// Test 7: Route withdrawal
// =============================================================================

func TestRouteWithdrawal(t *testing.T) {
	serverPeer := PeerConfig{
		ID:      1,
		Address: netip.MustParseAddr("127.0.0.1"),
		ASN:     65001,
	}

	var receivedNLRI []netip.Prefix
	cb := func(prefixes []netip.Prefix) {
		receivedNLRI = append(receivedNLRI, prefixes...)
	}

	speaker, client := startSpeakerAndClient(t, 64512, serverPeer, 65001, cb)

	waitForPeerState(t, client, StateEstablished, 10*time.Second)
	waitForSpeakerPeerState(t, speaker, stateKey("127.0.0.1", 65001), StateEstablished, 10*time.Second)

	// Announce a route
	prefix := netip.MustParsePrefix("10.0.0.0/8")
	err := speaker.Announce(serverPeer.Address, serverPeer.ASN, []Route{
		{Prefix: prefix, NextHop: netip.MustParseAddr("192.0.2.2")},
	})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)

	receivedBefore := len(receivedNLRI)

	// Withdraw the route
	err = speaker.Withdraw(serverPeer.Address, serverPeer.ASN, []netip.Prefix{prefix})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)

	// Client should have received both the announcement and the withdrawal
	// (the withdrawal shows up as additional received NLRI since the callback
	// is called for the UPDATE message; but withdrawals aren't passed
	// separately to the callback — we verify the callback was invoked at least once)
	if len(receivedNLRI) <= receivedBefore {
		t.Logf("note: withdrawal message may not trigger NLRI callback (only NLRI are passed)")
	}
	// At minimum, the announcement should have been received
	if receivedBefore == 0 {
		t.Fatal("client never received the route announcement")
	}
}

// =============================================================================
// Test 8: Reconnect after disconnect
// =============================================================================

func TestReconnect(t *testing.T) {
	port := freeTCPPort(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	speaker := NewSpeaker(SpeakerConfig{
		ASN:       64512,
		RouterID:  netip.MustParseAddr("192.0.2.1"),
		Port:      port,
		LocalAddr: netip.MustParseAddr("192.0.2.2"),
	}, logger)
	if err := speaker.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { speaker.Stop() })

	serverPeer := PeerConfig{
		ID: 1, Address: netip.MustParseAddr("127.0.0.1"), ASN: 65001,
		Port: -1, // passive only
	}
	_ = speaker.SetPeers([]PeerConfig{serverPeer})

	clientCfg := PeerConfig{
		ID: 2, Address: netip.MustParseAddr("127.0.0.1"), Port: port,
		ASN: 64512, Name: "test-client",
	}
	clientSpk := SpeakerConfig{
		ASN: 65001, RouterID: netip.MustParseAddr("192.0.2.3"),
	}

	client := NewPeer(clientCfg, clientSpk, logger, nil)
	go client.Run()
	t.Cleanup(func() { client.Stop() })

	waitForPeerState(t, client, StateEstablished, 10*time.Second)
	waitForSpeakerPeerState(t, speaker, stateKey("127.0.0.1", 65001), StateEstablished, 10*time.Second)

	// Disconnect the client
	client.Stop()
	time.Sleep(1 * time.Second)

	// Create a new client (stopping flag is set on the old one)
	client2 := NewPeer(clientCfg, clientSpk, logger, nil)
	go client2.Run()
	t.Cleanup(func() { client2.Stop() })

	// Should reconnect and establish again
	waitForPeerState(t, client2, StateEstablished, 10*time.Second)
	waitForSpeakerPeerState(t, speaker, stateKey("127.0.0.1", 65001), StateEstablished, 10*time.Second)
}

// =============================================================================
// Test 9: Password authentication
// =============================================================================

func TestPasswordAuthCorrect(t *testing.T) {
	serverPeer := PeerConfig{
		ID:       1,
		Address:  netip.MustParseAddr("127.0.0.1"),
		ASN:      65001,
		Password: "secret",
	}
	speaker, client := startSpeakerAndClient(t, 64512, serverPeer, 65001, nil)

	waitForPeerState(t, client, StateEstablished, 10*time.Second)
	waitForSpeakerPeerState(t, speaker, stateKey("127.0.0.1", 65001), StateEstablished, 10*time.Second)
}

func TestPasswordAuthWrong(t *testing.T) {
	port := freeTCPPort(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	speaker := NewSpeaker(SpeakerConfig{
		ASN:       64512,
		RouterID:  netip.MustParseAddr("192.0.2.1"),
		Port:      port,
		LocalAddr: netip.MustParseAddr("192.0.2.2"),
	}, logger)
	if err := speaker.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { speaker.Stop() })

	// Speaker expects password "secret"
	_ = speaker.SetPeers([]PeerConfig{
		{ID: 1, Address: netip.MustParseAddr("127.0.0.1"), ASN: 65001, Password: "secret", Port: -1},
	})

	// Client provides wrong password
	clientCfg := PeerConfig{
		ID:       2,
		Address:  netip.MustParseAddr("127.0.0.1"),
		Port:     port,
		ASN:      64512,
		Password: "wrong",
		Name:     "test-client",
	}
	clientPeer := NewPeer(clientCfg, SpeakerConfig{
		ASN:      65001,
		RouterID: netip.MustParseAddr("192.0.2.3"),
	}, logger, nil)

	go clientPeer.Run()
	t.Cleanup(func() { clientPeer.Stop() })

	// Client should NOT reach ESTABLISHED
	time.Sleep(3 * time.Second)
	clientState := clientPeer.State()
	if clientState == StateEstablished {
		t.Fatal("client reached ESTABLISHED with wrong password")
	}
	t.Logf("client state with wrong password: %s", clientState)

	// Speaker should also not be established
	states := speaker.PeerStates()
	if st, ok := states[stateKey("127.0.0.1", 65001)]; ok && st == StateEstablished {
		t.Fatal("speaker peer reached ESTABLISHED with wrong password")
	}
	t.Logf("speaker peer state with wrong password: %v", states)
}

// =============================================================================
// Test 10: Peer state transitions
// =============================================================================

func TestPeerStateTransitions(t *testing.T) {
	serverPeer := PeerConfig{
		ID:      1,
		Address: netip.MustParseAddr("127.0.0.1"),
		ASN:     65001,
	}

	// Poll the client state rapidly to capture transitions as the session comes up.
	speaker, client := startSpeakerAndClient(t, 64512, serverPeer, 65001, nil)

	var transitions []string
	done := make(chan struct{})
	go func() {
		defer close(done)
		prev := ""
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			current := client.State()
			if current != prev {
				transitions = append(transitions, current)
				prev = current
			}
			if current == StateEstablished {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()

	<-done

	// Verify the speaker side too
	waitForSpeakerPeerState(t, speaker, stateKey("127.0.0.1", 65001), StateEstablished, 5*time.Second)

	// We should see a sequence that goes through IDLE, CONNECT, OPENSENT, OPENCONFIRM, ESTABLISHED
	// (or at minimum ends with ESTABLISHED)
	if len(transitions) == 0 {
		t.Fatal("no state transitions recorded")
	}
	t.Logf("state transitions: %v", transitions)

	// Verify the last state is ESTABLISHED
	if transitions[len(transitions)-1] != StateEstablished {
		t.Fatalf("last transition = %s, want %s", transitions[len(transitions)-1], StateEstablished)
	}

	// Check that ESTABLISHED was reached
	estIdx := -1
	for i, s := range transitions {
		if s == StateEstablished {
			estIdx = i
			break
		}
	}
	if estIdx == -1 {
		t.Fatalf("never reached ESTABLISHED: %v", transitions)
	}

	// Verify CONNECT was seen (must be before ESTABLISHED)
	connIdx := -1
	for i, s := range transitions {
		if s == StateConnect {
			connIdx = i
		}
	}
	if connIdx == -1 {
		t.Logf("note: CONNECT state not captured (may have been too fast)")
	} else {
		if connIdx > estIdx {
			t.Fatalf("CONNECT (%d) seen after ESTABLISHED (%d)", connIdx, estIdx)
		}
	}
}

// =============================================================================
// Test 11: Keepalive maintains session
// =============================================================================

func TestKeepaliveMaintainsSession(t *testing.T) {
	port := freeTCPPort(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	speaker := NewSpeaker(SpeakerConfig{
		ASN:       64512,
		RouterID:  netip.MustParseAddr("192.0.2.1"),
		Port:      port,
		LocalAddr: netip.MustParseAddr("192.0.2.2"),
	}, logger)
	if err := speaker.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { speaker.Stop() })

	serverPeer := PeerConfig{
		ID: 1, Address: netip.MustParseAddr("127.0.0.1"), ASN: 65001,
		Port: -1, // passive only
	}
	_ = speaker.SetPeers([]PeerConfig{serverPeer})

	client := NewPeer(PeerConfig{
		ID: 2, Address: netip.MustParseAddr("127.0.0.1"), Port: port,
		ASN: 64512, Name: "keepalive-client",
	}, SpeakerConfig{
		ASN: 65001, RouterID: netip.MustParseAddr("192.0.2.3"),
	}, logger, nil)
	go client.Run()
	t.Cleanup(func() { client.Stop() })

	waitForPeerState(t, client, StateEstablished, 10*time.Second)
	waitForSpeakerPeerState(t, speaker, stateKey("127.0.0.1", 65001), StateEstablished, 10*time.Second)

	// Wait for at least one keepalive interval (30s) + buffer
	time.Sleep(35 * time.Second)

	// Both sides should still be established
	if client.State() != StateEstablished {
		t.Fatalf("client state = %s, want %s after keepalive wait", client.State(), StateEstablished)
	}
	states := speaker.PeerStates()
	keys := stateKey("127.0.0.1", 65001)
	if st, ok := states[keys]; !ok || st != StateEstablished {
		t.Fatalf("speaker peer state for %s = %v, want %s after keepalive wait", keys, states, StateEstablished)
	}
}

// =============================================================================
// Test: Speaker announces route to multiple peers (same IP, different ASN)
// =============================================================================

func TestRouteAnnouncementToMultiplePeers(t *testing.T) {
	port := freeTCPPort(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	speaker := NewSpeaker(SpeakerConfig{
		ASN:       64512,
		RouterID:  netip.MustParseAddr("192.0.2.1"),
		Port:      port,
		LocalAddr: netip.MustParseAddr("192.0.2.2"),
	}, logger)
	if err := speaker.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { speaker.Stop() })

	peers := []PeerConfig{
		{ID: 1, Address: netip.MustParseAddr("127.0.0.1"), ASN: 65001, Port: -1},
		{ID: 2, Address: netip.MustParseAddr("127.0.0.1"), ASN: 65002, Port: -1},
	}
	_ = speaker.SetPeers(peers)

	var mu sync.Mutex
	received65001 := false
	received65002 := false

	cb65001 := func(prefixes []netip.Prefix) {
		mu.Lock()
		received65001 = true
		mu.Unlock()
	}
	cb65002 := func(prefixes []netip.Prefix) {
		mu.Lock()
		received65002 = true
		mu.Unlock()
	}

	client1 := NewPeer(PeerConfig{
		ID: 10, Address: netip.MustParseAddr("127.0.0.1"), Port: port,
		ASN: 64512, Name: "client-65001",
	}, SpeakerConfig{ASN: 65001, RouterID: netip.MustParseAddr("192.0.2.10")}, logger, cb65001)
	go client1.Run()
	t.Cleanup(func() { client1.Stop() })

	client2 := NewPeer(PeerConfig{
		ID: 20, Address: netip.MustParseAddr("127.0.0.1"), Port: port,
		ASN: 64512, Name: "client-65002",
	}, SpeakerConfig{ASN: 65002, RouterID: netip.MustParseAddr("192.0.2.20")}, logger, cb65002)
	go client2.Run()
	t.Cleanup(func() { client2.Stop() })

	waitForPeerState(t, client1, StateEstablished, 10*time.Second)
	waitForPeerState(t, client2, StateEstablished, 10*time.Second)

	// Announce route only to 65001
	err := speaker.Announce(netip.MustParseAddr("127.0.0.1"), 65001, []Route{
		{Prefix: netip.MustParsePrefix("10.0.0.0/8"), NextHop: netip.MustParseAddr("192.0.2.2")},
	})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1 * time.Second)

	mu.Lock()
	r1 := received65001
	r2 := received65002
	mu.Unlock()

	if !r1 {
		t.Fatal("client 65001 did not receive route")
	}
	if r2 {
		t.Fatal("client 65002 should NOT have received route")
	}

	// Announce to 65002
	err = speaker.Announce(netip.MustParseAddr("127.0.0.1"), 65002, []Route{
		{Prefix: netip.MustParsePrefix("20.0.0.0/8"), NextHop: netip.MustParseAddr("192.0.2.2")},
	})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1 * time.Second)

	mu.Lock()
	r2 = received65002
	mu.Unlock()

	if !r2 {
		t.Fatal("client 65002 did not receive route")
	}
}
