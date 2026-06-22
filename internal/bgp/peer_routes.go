package bgp

import (
	"net"
	"net/netip"
)

func (p *Peer) TriggerUpdate() {
	p.needsUpdate.Store(true)
}

func (p *Peer) sendRoutes(conn net.Conn) {
	p.mu.Lock()
	routes := p.routes
	p.mu.Unlock()

	if len(routes) == 0 {
		return
	}

	for _, r := range routes {
		var attrs []PathAttribute
		var update *UpdateMessage

		if r.Prefix.Addr().Is6() {
			// Skip IPv6 routes if remote peer didn't advertise IPv6 unicast capability.
			if !p.hasIPv6Cap {
				p.logger.Debug("skipping IPv6 route to IPv4-only peer", "prefix", r.Prefix)
				continue
			}
			// IPv6: NLRI goes inside MP_REACH per RFC 4760.
			// UPDATE.NLRI must be empty.
			attrs = []PathAttribute{
				OriginAttribute(OriginIGP),
				&ASPathAttribute{ASN: p.spk.ASN, FourOctet: p.hasAS4Cap},
				&MpReachNLRIAttribute{
					NextHop: r.NextHop,
					NLRI:    []netip.Prefix{r.Prefix},
				},
				&LargeCommunitiesAttribute{Communities: r.Communities},
			}
			update = &UpdateMessage{
				PathAttributes: attrs,
			}
		} else {
			// IPv4: NLRI in UPDATE.NLRI field + NextHopAttribute (RFC 4271).
			attrs = []PathAttribute{
				OriginAttribute(OriginIGP),
				&ASPathAttribute{ASN: p.spk.ASN, FourOctet: p.hasAS4Cap},
				&NextHopAttribute{NextHop: r.NextHop},
				&LargeCommunitiesAttribute{Communities: r.Communities},
			}
			update = &UpdateMessage{
				PathAttributes: attrs,
				NLRI:           []netip.Prefix{r.Prefix},
			}
		}
		if _, err := conn.Write(update.Serialize()); err != nil {
			p.logger.Error("failed to send update", "error", err)
			return
		}
	}

	p.logger.Debug("announced routes", "count", len(routes))
}

// AnnounceRoutes stores routes and triggers an update if the session is established.
func (p *Peer) AnnounceRoutes(routes []Route) {
	p.mu.Lock()
	p.routes = routes
	conn := p.conn
	state := p.state
	p.mu.Unlock()

	if conn != nil && state == StateEstablished {
		p.sendRoutes(conn)
	} else {
		p.needsUpdate.Store(true)
	}
}

// WithdrawRoutes sends withdrawals for the given prefixes over an established session.
func (p *Peer) WithdrawRoutes(prefixes []netip.Prefix) {
	p.mu.Lock()
	p.routes = nil
	conn := p.conn
	state := p.state
	p.mu.Unlock()

	if conn != nil && state == StateEstablished {
		var v4prefixes, v6prefixes []netip.Prefix
		for _, pf := range prefixes {
			if pf.Addr().Is6() {
				v6prefixes = append(v6prefixes, pf)
			} else {
				v4prefixes = append(v4prefixes, pf)
			}
		}

		// Chunk v4 withdrawals to avoid oversized UPDATE messages.
		for i := 0; i < len(v4prefixes); i += 100 {
			end := i + 100
			if end > len(v4prefixes) {
				end = len(v4prefixes)
			}
			update := &UpdateMessage{WithdrawnRoutes: v4prefixes[i:end]}
			if _, err := conn.Write(update.Serialize()); err != nil {
				p.logger.Error("failed to send v4 withdrawal", "error", err)
				break
			}
		}
		// Chunk v6 withdrawals
		for i := 0; i < len(v6prefixes); i += 100 {
			end := i + 100
			if end > len(v6prefixes) {
				end = len(v6prefixes)
			}
			update := &UpdateMessage{PathAttributes: []PathAttribute{&MpUnreachNLRIAttribute{NLRI: v6prefixes[i:end]}}}
			if _, err := conn.Write(update.Serialize()); err != nil {
				p.logger.Error("failed to send v6 withdrawal", "error", err)
				break
			}
		}
		p.logger.Debug("withdrew routes", "count", len(prefixes))
	}
}
