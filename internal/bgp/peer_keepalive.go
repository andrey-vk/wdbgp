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
			if _, err := conn.Write(ka.Serialize()); err != nil {
				return
			}
		case <-stop:
			return
		}
	}
}
