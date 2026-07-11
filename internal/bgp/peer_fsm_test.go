package bgp

import (
	"errors"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"
)

// =============================================================================
// TestMainLoopSendsNotificationOnHoldTimerExpiry — RFC 4271 requires a
// NOTIFICATION (Cease, code 4: Hold Timer Expired) to be sent to the peer
// when its hold timer expires. ReadMessage always wraps the underlying read
// error (fmt.Errorf(...: %w, err)), so a plain `err.(net.Error)` type
// assertion on it never succeeds — only errors.As does. This test drives a
// real deadline-exceeded read through mainLoop and checks the peer on the
// other end of the pipe actually receives a NOTIFICATION instead of just
// seeing the connection go silent.
// =============================================================================

func TestMainLoopSendsNotificationOnHoldTimerExpiry(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	defer serverSide.Close() //nolint:errcheck // test cleanup
	defer clientSide.Close() //nolint:errcheck // test cleanup

	p := &Peer{
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		holdTime: 200 * time.Millisecond,
	}

	errCh := make(chan error, 1)
	go func() { errCh <- p.mainLoop(serverSide) }()

	// The simulated peer (clientSide) never writes anything, so the hold
	// timer must expire. Drain and discard any keepalives mainLoop sends
	// in the meantime, until a NOTIFICATION shows up or we time out.
	if err := clientSide.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	var notif *NotificationMessage
	for notif == nil {
		msg, err := ReadMessage(clientSide)
		if err != nil {
			t.Fatalf("expected a NOTIFICATION from the peer, got read error: %v", err)
		}
		if nm, ok := msg.(*NotificationMessage); ok {
			notif = nm
			break
		}
	}

	if notif.ErrorCode != 4 {
		t.Errorf("NotificationMessage.ErrorCode = %d, want 4 (Hold Timer Expired)", notif.ErrorCode)
	}

	select {
	case err := <-errCh:
		if err == nil || !strings.Contains(err.Error(), "hold timer expired") {
			t.Errorf("mainLoop error = %v, want it to mention hold timer expired", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("mainLoop did not return after sending the notification")
	}
}

// =============================================================================
// TestWriteConnPartialWriteClosesConn — a write that lands only part of a
// BGP message before failing leaves a truncated message on the wire; any
// retry on that connection appends a fresh header after the truncated body
// and corrupts the stream. writeConn must close the conn in that case so
// recovery goes through a clean re-establish — but must NOT close it when
// nothing was written, since then the stream is intact and a retry is safe.
// =============================================================================

func TestWriteConnPartialWriteClosesConn(t *testing.T) {
	p := &Peer{}

	partial := &stubWriteConn{n: 5, err: errors.New("i/o timeout")}
	if err := p.writeConn(partial, make([]byte, 100), time.Second); err == nil {
		t.Fatal("writeConn returned nil for a failed write")
	}
	if !partial.closed {
		t.Error("conn left open after a partial write — a retry would corrupt the stream")
	}

	clean := &stubWriteConn{n: 0, err: errors.New("i/o timeout")}
	if err := p.writeConn(clean, make([]byte, 100), time.Second); err == nil {
		t.Fatal("writeConn returned nil for a failed write")
	}
	if clean.closed {
		t.Error("conn closed although nothing was written — retrying on the intact stream was safe")
	}
}

// stubWriteConn fails every Write after reporting n bytes written and
// records whether Close was called. Methods writeConn doesn't touch are
// inherited from the nil embedded net.Conn and would panic if reached.
type stubWriteConn struct {
	net.Conn
	n      int
	err    error
	closed bool
}

func (c *stubWriteConn) Write([]byte) (int, error)        { return c.n, c.err }
func (c *stubWriteConn) Close() error                     { c.closed = true; return nil }
func (c *stubWriteConn) SetWriteDeadline(time.Time) error { return nil }

// =============================================================================
// TestMainLoopRouteRefreshTriggersFullResync — an incoming ROUTE-REFRESH
// (RFC 2918; RouterOS sends one on `refresh` and on input-filter changes)
// must NOT kill the session — it previously surfaced as "unknown message
// type: 5" and tore the connection down mid-table. The peer must instead
// re-announce its full route set, even though lastSent says the remote
// already holds it.
// =============================================================================

func TestMainLoopRouteRefreshTriggersFullResync(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	defer serverSide.Close() //nolint:errcheck // test cleanup
	defer clientSide.Close() //nolint:errcheck // test cleanup

	prefix := netip.MustParsePrefix("10.0.0.0/8")
	stale := netip.MustParsePrefix("172.16.0.0/12")
	p := &Peer{
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		holdTime: 5 * time.Second,
		spk:      SpeakerConfig{ASN: 64512},
		routes:   []Route{{Prefix: prefix, NextHop: netip.MustParseAddr("192.0.2.1")}},
	}
	// Simulate a synced session whose desired set has just shrunk: the
	// remote holds both prefixes, but only one is still desired. The
	// refresh handler must preserve lastSent — wiping it would lose the
	// pending withdrawal of the stale prefix (Codex P2 on PR #37).
	p.lastSent = []netip.Prefix{prefix, stale}

	errCh := make(chan error, 1)
	go func() { errCh <- p.mainLoop(serverSide) }()

	// Ask for a refresh (IPv4 unicast).
	if _, err := clientSide.Write(wrapMessage(MsgRouteRefresh, []byte{0x00, 0x01, 0x00, 0x01})); err != nil {
		t.Fatalf("write route refresh: %v", err)
	}

	// The peer must re-announce the prefix. Drain keepalives while waiting.
	if err := clientSide.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	announced := false
	withdrawn := false
	for !announced || !withdrawn {
		msg, err := ReadMessage(clientSide)
		if err != nil {
			t.Fatalf("expected UPDATEs (announce %v, withdraw %v so far), got read error: %v", announced, withdrawn, err)
		}
		u, ok := msg.(*UpdateMessage)
		if !ok {
			continue // keepalive
		}
		for _, pf := range u.NLRI {
			if pf == prefix {
				announced = true
			}
		}
		for _, pf := range u.WithdrawnRoutes {
			if pf == stale {
				withdrawn = true
			}
		}
	}

	// The session must still be alive — the refresh itself is not an error.
	select {
	case err := <-errCh:
		t.Fatalf("mainLoop exited on route refresh: %v", err)
	default:
	}

	// Cleanup: closing the pipe ends mainLoop.
	clientSide.Close() //nolint:errcheck,gosec // test cleanup
	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("mainLoop did not return after pipe close")
	}
}

// =============================================================================
// TestMainLoopIgnoresRouteRefreshWithSubtype — RFC 7313 subtypes 1 (BoRR)
// and 2 (EoRR) demarcate the peer's own re-advertisement toward us; they
// are not requests for our routes (and are only valid under the Enhanced
// Route Refresh capability, which we don't advertise). They must neither
// trigger a resync nor kill the session.
// =============================================================================

func TestMainLoopIgnoresRouteRefreshWithSubtype(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	defer serverSide.Close() //nolint:errcheck,gosec // test cleanup
	defer clientSide.Close() //nolint:errcheck,gosec // test cleanup

	prefix := netip.MustParsePrefix("10.0.0.0/8")
	p := &Peer{
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		holdTime: 5 * time.Second,
		spk:      SpeakerConfig{ASN: 64512},
		routes:   []Route{{Prefix: prefix, NextHop: netip.MustParseAddr("192.0.2.1")}},
	}
	p.lastSent = []netip.Prefix{prefix}

	errCh := make(chan error, 1)
	go func() { errCh <- p.mainLoop(serverSide) }()

	// BoRR (subtype 1) and EoRR (subtype 2) — neither is a request. The
	// write deadline turns a broken implementation (which reacts to the
	// first message by writing a resync UPDATE into the unbuffered pipe,
	// deadlocking against our second write) into a fast failure.
	if err := clientSide.SetWriteDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetWriteDeadline: %v", err)
	}
	for _, subtype := range []byte{1, 2} {
		if _, err := clientSide.Write(wrapMessage(MsgRouteRefresh, []byte{0x00, 0x01, subtype, 0x01})); err != nil {
			t.Fatalf("write route refresh subtype %d (resync UPDATE in the way?): %v", subtype, err)
		}
	}

	// No UPDATE may arrive — only keepalives. Read for a short window.
	if err := clientSide.SetReadDeadline(time.Now().Add(700 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	for {
		msg, err := ReadMessage(clientSide)
		if err != nil {
			break // window elapsed with no UPDATE
		}
		if _, ok := msg.(*UpdateMessage); ok {
			t.Fatal("BoRR/EoRR triggered a resync UPDATE; they must be ignored")
		}
	}

	// The session must still be alive.
	select {
	case err := <-errCh:
		t.Fatalf("mainLoop exited on BoRR/EoRR: %v", err)
	default:
	}

	clientSide.Close() //nolint:errcheck,gosec // test cleanup
	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("mainLoop did not return after pipe close")
	}
}

// =============================================================================
// TestMainLoopResyncLongerThanHoldTimeKeepsSession — mainLoop arms the read
// deadline at the top of each iteration, and a needsUpdate resync used to
// run between that arming and the read. A full-table resync to a slow peer
// that outlasted the hold time then made the read fail instantly — a live
// peer (whose keepalives sat unread) was torn down as "hold timer expired".
// After a successful resync the loop must re-arm the deadline first.
// net.Pipe is unbuffered, so delaying the client's reads makes the resync
// writes block for longer than the hold time.
// =============================================================================

func TestMainLoopResyncLongerThanHoldTimeKeepsSession(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	defer serverSide.Close() //nolint:errcheck,gosec // test cleanup
	defer clientSide.Close() //nolint:errcheck,gosec // test cleanup

	prefix := netip.MustParsePrefix("10.0.0.0/8")
	p := &Peer{
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		holdTime: 500 * time.Millisecond,
		spk:      SpeakerConfig{ASN: 64512},
		routes:   []Route{{Prefix: prefix, NextHop: netip.MustParseAddr("192.0.2.1")}},
	}
	p.needsUpdate.Store(true) // pending resync, as after a ROUTE-REFRESH

	errCh := make(chan error, 1)
	go func() { errCh <- p.mainLoop(serverSide) }()

	// Stall all reads past the hold time: the resync write (and keepalive
	// writes) block on the unbuffered pipe, so the resync completes only
	// after the hold-time-sized read deadline armed before it has passed.
	time.Sleep(700 * time.Millisecond)

	// Drain until the resync UPDATE arrives.
	if err := clientSide.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	sawUpdate := false
	for !sawUpdate {
		msg, err := ReadMessage(clientSide)
		if err != nil {
			t.Fatalf("expected the resync UPDATE, got read error: %v", err)
		}
		switch msg.(type) {
		case *UpdateMessage:
			sawUpdate = true
		case *NotificationMessage:
			t.Fatal("peer sent NOTIFICATION: a slow resync was misreported as hold-timer expiry")
		}
	}

	// Feed the (re-armed) read and confirm the session survived. The write
	// deadline keeps a broken implementation (mainLoop already torn down,
	// nobody reading the unbuffered pipe) from deadlocking the test.
	if err := clientSide.SetWriteDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetWriteDeadline: %v", err)
	}
	if _, err := clientSide.Write((&KeepaliveMessage{}).Serialize()); err != nil {
		t.Fatalf("write keepalive (session likely torn down): %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	select {
	case err := <-errCh:
		t.Fatalf("mainLoop exited after slow resync: %v", err)
	default:
	}

	clientSide.Close() //nolint:errcheck,gosec // test cleanup
	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("mainLoop did not return after pipe close")
	}
}
