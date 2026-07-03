package bgp

import (
	"net"
	"testing"
	"time"
)

// =============================================================================
// TestKeepaliveLoopSurvivesStaleDeadline — keepaliveLoop's Write shares the
// same net.Conn as mainLoop's blocking Read, and SetDeadline affects both
// directions at once. If the read side's deadline from a prior mainLoop
// iteration has already expired (e.g. right as the hold timer trips) by the
// time keepaliveLoop's ticker fires, a Write with no deadline of its own
// inherits that already-past deadline and fails instantly — even though the
// connection itself is perfectly healthy. This test pre-expires the conn's
// shared deadline before starting keepaliveLoop and confirms a keepalive
// still reaches the peer.
// =============================================================================

func TestKeepaliveLoopSurvivesStaleDeadline(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	defer serverSide.Close() //nolint:errcheck // test cleanup
	defer clientSide.Close() //nolint:errcheck // test cleanup

	// Simulate a stale deadline left over from a previous mainLoop
	// iteration that has already passed.
	if err := serverSide.SetDeadline(time.Now().Add(-1 * time.Second)); err != nil {
		t.Fatalf("SetDeadline: %v", err)
	}

	p := &Peer{}
	stop := make(chan struct{})
	defer close(stop)
	go p.keepaliveLoop(serverSide, 30*time.Millisecond, stop)

	if err := clientSide.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	msg, err := ReadMessage(clientSide)
	if err != nil {
		t.Fatalf("expected a KEEPALIVE despite the stale deadline, got read error: %v", err)
	}
	if _, ok := msg.(*KeepaliveMessage); !ok {
		t.Fatalf("expected *KeepaliveMessage, got %T", msg)
	}
}
