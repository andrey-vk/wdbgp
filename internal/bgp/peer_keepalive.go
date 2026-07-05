package bgp

import (
	"net"
	"time"
)

// keepaliveLoop sends periodic keepalive messages on conn at the configured
// interval (holdTime/3). Exits when the stop channel is closed or a write
// fails (connection is down).
func (p *Peer) keepaliveLoop(conn net.Conn, holdTime time.Duration, stop <-chan struct{}) {
	ticker := time.NewTicker(holdTime / 3)
	defer ticker.Stop()
	ka := &KeepaliveMessage{}
	for {
		select {
		case <-ticker.C:
			// Give the write its own fresh deadline: this conn's deadline is
			// shared with mainLoop's blocking Read, and SetDeadline sets
			// both directions at once — if the read side's deadline from a
			// prior mainLoop iteration has already expired (e.g. right as
			// the hold timer trips), a Write with no deadline of its own
			// would inherit that stale, already-past deadline and fail
			// instantly even though the connection is otherwise healthy.
			conn.SetWriteDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck,gosec // deadline is advisory, session dying if Write fails
			if _, err := conn.Write(ka.Serialize()); err != nil {
				return
			}
		case <-stop:
			return
		}
	}
}
