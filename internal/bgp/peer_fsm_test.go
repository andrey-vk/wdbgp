package bgp

import (
	"io"
	"log/slog"
	"net"
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
