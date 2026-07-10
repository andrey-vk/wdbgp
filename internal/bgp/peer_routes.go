package bgp

import (
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"
)

// maxPrefixesPerUpdate bounds NLRI (and withdrawal) counts per UPDATE so a
// message can never approach the RFC 4271 4096-byte limit: 100 prefixes at
// worst-case 5 (v4) / 17 (v6) bytes plus this speaker's fixed attribute set
// stays well under it.
const maxPrefixesPerUpdate = 100

// routeWriteTimeout is the per-message write deadline for route UPDATEs. A
// peer that cannot drain its receive buffer for this long is effectively
// wedged; failing the send (and letting the FSM retry or re-establish) beats
// blocking the reconcile goroutine forever.
const routeWriteTimeout = 10 * time.Second

func (p *Peer) TriggerUpdate() {
	p.needsUpdate.Store(true)
}

// sendRoutes announces the peer's current route set. Routes sharing an
// identical attribute set (next hop + communities) are packed into common
// UPDATE messages instead of one message per route — for a typical catalog
// selection that collapses thousands of writes into a few dozen.
//
// On a write failure it re-arms needsUpdate before returning the error, so
// the announcement is never silently half-delivered: either mainLoop retries
// the full set on a live session, or the session tears down and the
// establish path re-sends everything.
func (p *Peer) sendRoutes(conn net.Conn) error {
	p.mu.Lock()
	routes := p.routes
	p.mu.Unlock()

	if len(routes) == 0 {
		return nil
	}

	type attrGroup struct {
		nextHop netip.Addr
		comms   []LargeCommunity
		is6     bool
		nlri    []netip.Prefix
	}
	var order []string
	groups := map[string]*attrGroup{}
	skipped := 0
	for _, r := range routes {
		is6 := r.Prefix.Addr().Is6()
		if is6 && !p.hasIPv6Cap {
			// Skip IPv6 routes if remote peer didn't advertise IPv6 unicast capability.
			skipped++
			continue
		}
		var key strings.Builder
		if is6 {
			key.WriteString("6|")
		} else {
			key.WriteString("4|")
		}
		key.WriteString(r.NextHop.String())
		for _, c := range r.Communities {
			fmt.Fprintf(&key, "|%d:%d:%d", c.GlobalAdmin, c.LocalData1, c.LocalData2)
		}
		k := key.String()
		g, ok := groups[k]
		if !ok {
			g = &attrGroup{nextHop: r.NextHop, comms: r.Communities, is6: is6}
			groups[k] = g
			order = append(order, k)
		}
		g.nlri = append(g.nlri, r.Prefix)
	}
	if skipped > 0 {
		p.logger.Debug("skipping IPv6 routes to IPv4-only peer", "count", skipped)
	}

	sent := 0
	messages := 0
	for _, k := range order {
		g := groups[k]
		for start := 0; start < len(g.nlri); start += maxPrefixesPerUpdate {
			end := start + maxPrefixesPerUpdate
			if end > len(g.nlri) {
				end = len(g.nlri)
			}
			chunk := g.nlri[start:end]

			var update *UpdateMessage
			if g.is6 {
				// IPv6: NLRI goes inside MP_REACH per RFC 4760.
				// UPDATE.NLRI must be empty.
				update = &UpdateMessage{
					PathAttributes: []PathAttribute{
						OriginAttribute(OriginIGP),
						&ASPathAttribute{ASN: p.spk.ASN, FourOctet: p.hasAS4Cap},
						&MpReachNLRIAttribute{
							NextHop: g.nextHop,
							NLRI:    chunk,
						},
						&LargeCommunitiesAttribute{Communities: g.comms},
					},
				}
			} else {
				// IPv4: NLRI in UPDATE.NLRI field + NextHopAttribute (RFC 4271).
				update = &UpdateMessage{
					PathAttributes: []PathAttribute{
						OriginAttribute(OriginIGP),
						&ASPathAttribute{ASN: p.spk.ASN, FourOctet: p.hasAS4Cap},
						&NextHopAttribute{NextHop: g.nextHop},
						&LargeCommunitiesAttribute{Communities: g.comms},
					},
					NLRI: chunk,
				}
			}
			if err := p.writeConn(conn, update.Serialize(), routeWriteTimeout); err != nil {
				p.needsUpdate.Store(true)
				p.logger.Error("failed to send update",
					"error", err, "sent", sent, "total", len(routes)-skipped)
				return fmt.Errorf("send update after %d/%d routes: %w", sent, len(routes)-skipped, err)
			}
			sent += len(chunk)
			messages++
		}
	}

	p.logger.Debug("announced routes", "count", sent, "updates", messages)
	return nil
}

// AnnounceRoutes stores routes and triggers an update if the session is established.
func (p *Peer) AnnounceRoutes(routes []Route) {
	p.mu.Lock()
	p.routes = routes
	conn := p.conn
	state := p.state
	p.mu.Unlock()

	if conn != nil && state == StateEstablished {
		// A failed send has already re-armed needsUpdate: mainLoop retries
		// the full set on the live session, or the establish path re-sends
		// after the (likely dying) session is replaced. Nothing else to do.
		if err := p.sendRoutes(conn); err != nil {
			p.logger.Warn("route announcement incomplete, will retry", "error", err)
		}
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
		for i := 0; i < len(v4prefixes); i += maxPrefixesPerUpdate {
			end := i + maxPrefixesPerUpdate
			if end > len(v4prefixes) {
				end = len(v4prefixes)
			}
			update := &UpdateMessage{WithdrawnRoutes: v4prefixes[i:end]}
			if err := p.writeConn(conn, update.Serialize(), routeWriteTimeout); err != nil {
				p.logger.Error("failed to send v4 withdrawal", "error", err)
				break
			}
		}
		// Chunk v6 withdrawals
		for i := 0; i < len(v6prefixes); i += maxPrefixesPerUpdate {
			end := i + maxPrefixesPerUpdate
			if end > len(v6prefixes) {
				end = len(v6prefixes)
			}
			update := &UpdateMessage{PathAttributes: []PathAttribute{&MpUnreachNLRIAttribute{NLRI: v6prefixes[i:end]}}}
			if err := p.writeConn(conn, update.Serialize(), routeWriteTimeout); err != nil {
				p.logger.Error("failed to send v6 withdrawal", "error", err)
				break
			}
		}
		p.logger.Debug("withdrew routes", "count", len(prefixes))
	}
}
