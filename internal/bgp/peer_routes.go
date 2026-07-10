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

// resetSent forgets what the remote side holds. Called when a session
// (re-)establishes: a fresh session starts with no routes from us, so the
// next resync must announce everything and withdraw nothing.
func (p *Peer) resetSent() {
	p.sendMu.Lock()
	p.lastSent = nil
	p.sendMu.Unlock()
}

// resyncRoutes brings the established session in line with the current
// desired set: it withdraws prefixes the remote holds that are no longer
// desired (lastSent minus desired — the peer owns this diff, the manager
// never sends withdrawal deltas) and announces the desired set, packing
// routes that share an attribute set (next hop + communities) into common
// UPDATE messages.
//
// sendMu makes a whole resync atomic with respect to other resyncs — a
// retry from mainLoop and a concurrent reconcile must not interleave at
// message granularity, or a stale snapshot's chunks could re-advertise
// prefixes a newer resync just withdrew. Individual messages still take
// writeMu, so keepalives stay timely between them. lastSent is only
// advanced after a fully successful resync: a failed one re-arms
// needsUpdate and leaves lastSent alone, so the retry re-sends the full
// set — duplicate announcements are idempotent and withdrawing a prefix
// the remote never got is a legal no-op (RFC 4271 §3.1).
func (p *Peer) resyncRoutes(conn net.Conn) error {
	p.sendMu.Lock()
	defer p.sendMu.Unlock()

	p.mu.Lock()
	routes := p.routes
	p.mu.Unlock()

	fail := func(what string, err error) error {
		p.needsUpdate.Store(true)
		p.logger.Error("route resync failed", "stage", what, "error", err)
		return fmt.Errorf("%s: %w", what, err)
	}

	desired := make(map[netip.Prefix]bool, len(routes))
	for _, r := range routes {
		desired[r.Prefix] = true
	}
	var withdrawV4, withdrawV6 []netip.Prefix
	for _, pf := range p.lastSent {
		if !desired[pf] {
			if pf.Addr().Is6() {
				withdrawV6 = append(withdrawV6, pf)
			} else {
				withdrawV4 = append(withdrawV4, pf)
			}
		}
	}
	for _, chunk := range chunkPrefixes(withdrawV4) {
		update := &UpdateMessage{WithdrawnRoutes: chunk}
		if err := p.writeConn(conn, update.Serialize(), routeWriteTimeout); err != nil {
			return fail("send v4 withdrawal", err)
		}
	}
	for _, chunk := range chunkPrefixes(withdrawV6) {
		update := &UpdateMessage{PathAttributes: []PathAttribute{&MpUnreachNLRIAttribute{NLRI: chunk}}}
		if err := p.writeConn(conn, update.Serialize(), routeWriteTimeout); err != nil {
			return fail("send v6 withdrawal", err)
		}
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
	sentPrefixes := make([]netip.Prefix, 0, len(routes))
	for _, r := range routes {
		is6 := r.Prefix.Addr().Is6()
		if is6 && !p.hasIPv6Cap {
			// Skip IPv6 routes if remote peer didn't advertise IPv6 unicast
			// capability. Not recorded in lastSent: the remote never holds
			// them, so there is nothing to withdraw later.
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
		sentPrefixes = append(sentPrefixes, r.Prefix)
	}
	if skipped > 0 {
		p.logger.Debug("skipping IPv6 routes to IPv4-only peer", "count", skipped)
	}

	sent := 0
	messages := 0
	for _, k := range order {
		g := groups[k]
		for _, chunk := range chunkPrefixes(g.nlri) {
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
				return fail(fmt.Sprintf("send update after %d/%d routes", sent, len(sentPrefixes)), err)
			}
			sent += len(chunk)
			messages++
		}
	}

	p.lastSent = sentPrefixes
	p.logger.Debug("resynced routes",
		"announced", sent, "withdrawn", len(withdrawV4)+len(withdrawV6), "updates", messages)
	return nil
}

// chunkPrefixes splits prefixes into maxPrefixesPerUpdate-sized slices.
func chunkPrefixes(prefixes []netip.Prefix) [][]netip.Prefix {
	var chunks [][]netip.Prefix
	for start := 0; start < len(prefixes); start += maxPrefixesPerUpdate {
		end := start + maxPrefixesPerUpdate
		if end > len(prefixes) {
			end = len(prefixes)
		}
		chunks = append(chunks, prefixes[start:end])
	}
	return chunks
}

// AnnounceRoutes stores the desired route set and, if the session is
// established, synchronously resyncs it to the peer. A delivery failure is
// returned so callers don't record the set as announced; the resync has
// already re-armed needsUpdate for the FSM-level retry. A session that is
// not established yet is not an error — the establish path sends the set.
func (p *Peer) AnnounceRoutes(routes []Route) error {
	p.mu.Lock()
	p.routes = routes
	conn := p.conn
	state := p.state
	p.mu.Unlock()

	if conn != nil && state == StateEstablished {
		return p.resyncRoutes(conn)
	}
	p.needsUpdate.Store(true)
	return nil
}

// WithdrawRoutes removes the given prefixes from the desired set and, if the
// session is established, resyncs — the removed prefixes are withdrawn via
// the lastSent diff. Same error semantics as AnnounceRoutes.
func (p *Peer) WithdrawRoutes(prefixes []netip.Prefix) error {
	drop := make(map[netip.Prefix]bool, len(prefixes))
	for _, pf := range prefixes {
		drop[pf] = true
	}
	p.mu.Lock()
	kept := p.routes[:0:0]
	for _, r := range p.routes {
		if !drop[r.Prefix] {
			kept = append(kept, r)
		}
	}
	p.routes = kept
	conn := p.conn
	state := p.state
	p.mu.Unlock()

	if conn != nil && state == StateEstablished {
		return p.resyncRoutes(conn)
	}
	p.needsUpdate.Store(true)
	return nil
}
