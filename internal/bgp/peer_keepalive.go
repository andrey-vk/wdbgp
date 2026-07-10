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
			// writeConn serializes against concurrent route-update writes
			// and gives the keepalive its own fresh deadline (the conn's
			// deadline is shared with mainLoop's blocking Read — a Write
			// with no deadline of its own could inherit a stale,
			// already-past one and fail instantly on a healthy session).
			if err := p.writeConn(conn, ka.Serialize(), 5*time.Second); err != nil {
				return
			}
		case <-stop:
			return
		}
	}
}
