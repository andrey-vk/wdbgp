package bgp

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"
)

// Speaker is the BGP speaker — listens on TCP, accepts connections, manages peers.
type Speaker struct {
	mu          sync.Mutex
	cfg         SpeakerConfig
	logger      *slog.Logger
	listener    net.Listener
	peers       map[string]*Peer // keyed by "addr:asn" string
	peerConfigs []PeerConfig     // configured peers (from DB)
	started     atomic.Bool
	cancel      context.CancelFunc
	dynMD5      *dynamicMD5Queue // non-nil when dynamic-peer MD5 matching is enabled and running
}

// SpeakerConfig holds BGP speaker configuration.
type SpeakerConfig struct {
	ASN       uint32
	RouterID  netip.Addr // IPv4 router ID (e.g., 192.0.2.1)
	Port      int32
	LocalAddr netip.Addr // IPv4 local address for NEXT_HOP
	HoldTime  uint16     // proposed hold time in seconds (0 = default 90)

	// DynamicPeerMD5Match enables NFQUEUE-based RFC 2385 signature matching
	// for dynamic (0.0.0.0/::) peers (see nfqueue_md5.go). Speaker.Start
	// installs the NFQUEUE redirect rule and its consumer together, and
	// fails (fail-closed) if either can't run.
	DynamicPeerMD5Match    bool
	DynamicPeerMD5QueueNum uint16
}

// PeerConfig describes a configured BGP peer.
type PeerConfig struct {
	ID        int64
	Address   netip.Addr
	Port      int32 // destination port (0 = default 179)
	ASN       uint32
	Password  string     // MD5 password (empty = none)
	Name      string     // description
	LocalAddr netip.Addr // local bind address for dialing (per-peer, IPv4/IPv6 aware)
}

// Route is a single route to announce.
type Route struct {
	Prefix      netip.Prefix
	Communities []LargeCommunity
	NextHop     netip.Addr
}

func NewSpeaker(cfg SpeakerConfig, logger *slog.Logger) *Speaker {
	return &Speaker{
		cfg:    cfg,
		logger: logger,
		peers:  make(map[string]*Peer),
	}
}

// Start begins listening on the configured port.
// If Port is -1, no TCP listener is started (used for testing without BGP).
func (s *Speaker) Start(ctx context.Context) error {
	if s.cfg.Port >= 0 {
		// Dynamic-peer MD5 is fail-closed: the admin asked for MD5-verified
		// dynamic peers, so if the redirect rule or its queue consumer can't
		// run, refuse to run BGP at all rather than silently accepting
		// unauthenticated dynamic peers. The rule and the consumer live and
		// die together — the rule has no bypass flag, so a rule without a
		// consumer would drop every inbound BGP SYN, fixed peers included.
		//
		// The rule goes in BEFORE the listener exists: the kernel completes
		// handshakes into the listen backlog from the moment listen()
		// returns, so installing the rule any later leaves a window where a
		// dynamic peer's un-verified SYN is already accepted. With this
		// order a SYN either arrives pre-listener (refused, no connection)
		// or passes through the queue. Between rule install and consumer
		// start, SYNs are dropped — fail-closed, never fail-open.
		if s.cfg.DynamicPeerMD5Match {
			if err := EnsureDynamicMD5NFQueueRule(uint16(s.cfg.Port), s.cfg.DynamicPeerMD5QueueNum); err != nil { //nolint:gosec // BGP port fits uint16
				return fmt.Errorf("dynamic peer MD5 nfqueue rule: %w", err)
			}
		}

		addr := fmt.Sprintf(":%d", s.cfg.Port)
		var lc net.ListenConfig
		l, err := lc.Listen(ctx, "tcp", addr)
		if err != nil {
			s.removeDynamicMD5Rule()
			return fmt.Errorf("bgp listen %s: %w", addr, err)
		}
		s.listener = l

		// Set TCP MD5 on listener BEFORE accepting connections so the
		// kernel enforces MD5 during the TCP handshake (RFC 2385).
		if err := applyListenerMD5(s.listener, s.peerConfigs); err != nil {
			s.abortStart()
			return fmt.Errorf("tcp md5 on listener failed: %w", err)
		}

		ctx, s.cancel = context.WithCancel(ctx)

		// The consumer must be attached before the accept loop runs so no
		// connection is ever handed to the BGP FSM without its SYN having
		// been verified (or bypassed as a known fixed peer).
		if s.cfg.DynamicPeerMD5Match {
			dq, err := startDynamicMD5Queue(ctx, s.listener, s.cfg.DynamicPeerMD5QueueNum, uint16(s.cfg.Port), s.logger) //nolint:gosec // BGP port fits uint16
			if err != nil {
				s.abortStart()
				return err
			}
			dq.SetPeers(s.peerConfigs)
			s.dynMD5 = dq
		}

		go s.acceptLoop(ctx)
	}

	s.started.Store(true)
	s.logger.Info("BGP speaker started", "port", s.cfg.Port)
	return nil
}

// removeDynamicMD5Rule removes the NFQUEUE redirect rule if this speaker's
// config would have installed one, logging rather than failing — it runs on
// unwind paths where a more important error is already being returned.
func (s *Speaker) removeDynamicMD5Rule() {
	if !s.cfg.DynamicPeerMD5Match {
		return
	}
	if err := RemoveDynamicMD5NFQueueRule(uint16(s.cfg.Port)); err != nil { //nolint:gosec // BGP port fits uint16
		s.logger.Error("remove dynamic MD5 nfqueue rule", "error", err)
	}
}

// abortStart unwinds a partially-started speaker (rule installed, listener
// open, accept loop not yet running) when a later Start step fails.
func (s *Speaker) abortStart() {
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	if s.listener != nil {
		if err := s.listener.Close(); err != nil {
			log.Printf("DEBUG: close listener: %v", err)
		}
		s.listener = nil
	}
	s.removeDynamicMD5Rule()
}

// Stop shuts down the speaker.
//
// Teardown mirrors Start's fail-closed ordering in reverse: the listener
// closes first, while the rule and consumer are still verifying SYNs — if
// the rule went first, a SYN arriving before the listener closes would be
// accepted from the backlog without MD5 verification. With the listener
// gone, remaining SYNs are either dropped by the consumer-less rule
// (fail-closed) or refused once the rule is removed too.
func (s *Speaker) Stop() error {
	s.started.Store(false)
	if s.cancel != nil {
		s.cancel()
	}
	if s.listener != nil {
		if err := s.listener.Close(); err != nil {
			log.Printf("DEBUG: close listener: %v", err)
		}
		s.listener = nil
	}
	if s.dynMD5 != nil {
		if err := s.dynMD5.Close(); err != nil {
			log.Printf("DEBUG: close dynamic md5 queue: %v", err)
		}
		s.dynMD5 = nil
		// The redirect rule must not outlive its consumer — without one it
		// drops every inbound BGP SYN (no bypass flag, deliberately).
		s.removeDynamicMD5Rule()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.peers {
		p.Stop()
	}
	s.peers = make(map[string]*Peer)
	s.logger.Info("BGP speaker stopped")
	return nil
}

// SetPeers updates the configured peer list. New peers are started,
// removed peers are stopped, existing peers are updated if changed.
func (s *Speaker) SetPeers(peers []PeerConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	oldConfigs := s.peerConfigs

	newSet := make(map[string]PeerConfig, len(peers))
	for _, pc := range peers {
		key := fmt.Sprintf("%s:%d", pc.Address.String(), pc.ASN)
		newSet[key] = pc
	}

	// Validate and apply MD5 BEFORE mutating peers.
	if s.listener != nil {
		if err := applyListenerMD5(s.listener, peers); err != nil {
			return fmt.Errorf("tcp md5 refresh: %w", err)
		}
	}

	// Stop peers that were removed
	for key, p := range s.peers {
		if _, ok := newSet[key]; !ok {
			p.Stop()
			delete(s.peers, key)
		}
	}

	// Start new peers or update existing
	for key, pc := range newSet {
		if existing, ok := s.peers[key]; ok {
			// Peer exists — restart it if any connection-relevant field
			// changed. Port encodes active-dial vs passive-only (see
			// peerPort), so a per-user active_dial toggle lands here too.
			old := existing.PeerConfig()
			if old.ASN != pc.ASN || old.Password != pc.Password ||
				old.Port != pc.Port || old.LocalAddr != pc.LocalAddr {
				existing.Stop()
				delete(s.peers, key)
			} else {
				continue // no change
			}
		}
		// Start new peer
		p := NewPeer(pc, s.cfg, s.logger, nil)
		go p.Run()
		s.peers[key] = p
	}

	s.peerConfigs = peers

	if s.dynMD5 != nil {
		s.dynMD5.SetPeers(peers)
	}

	// Clear old MD5 keys only for addresses no longer in the new set.
	// Keys for surviving peers were already re-applied above.
	if s.listener != nil {
		newAddrSet := make(map[netip.Addr]bool)
		for _, pc := range peers {
			newAddrSet[pc.Address] = true
		}
		var toClear []PeerConfig
		for _, oc := range oldConfigs {
			if !newAddrSet[oc.Address] {
				toClear = append(toClear, oc)
			}
		}
		if len(toClear) > 0 {
			_ = clearListenerMD5(s.listener, toClear) //nolint:errcheck // connection is dying, Close is best-effort cleanup
		}
	}
	return nil
}

// PeerStates returns the BGP state for each configured peer.
func (s *Speaker) PeerStates() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	states := make(map[string]string, len(s.peers))
	for key, p := range s.peers {
		states[key] = p.State()
	}
	return states
}

// ReconcileRoutes triggers route re-announcement for all peers.
func (s *Speaker) ReconcileRoutes() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.peers {
		p.TriggerUpdate()
	}
}

// Announce pushes routes to a specific peer, identified by its address and ASN.
func (s *Speaker) Announce(addr netip.Addr, asn uint32, routes []Route) error {
	s.mu.Lock()
	key := fmt.Sprintf("%s:%d", addr.String(), asn)
	p, ok := s.peers[key]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("peer %s:%d not found", addr.String(), asn)
	}
	return p.AnnounceRoutes(routes)
}

// Withdraw removes prefixes from a specific peer.
func (s *Speaker) Withdraw(addr netip.Addr, asn uint32, prefixes []netip.Prefix) error {
	s.mu.Lock()
	key := fmt.Sprintf("%s:%d", addr.String(), asn)
	p, ok := s.peers[key]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("peer %s:%d not found", addr.String(), asn)
	}
	return p.WithdrawRoutes(prefixes)
}

// acceptLoop accepts incoming connections.
func (s *Speaker) acceptLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				s.logger.Error("accept error", "error", err)
				continue
			}
		}

		go s.handleConnection(conn)
	}
}

// handleConnection processes a new incoming TCP connection.
func (s *Speaker) handleConnection(conn net.Conn) {
	remoteAddr, _ := netip.ParseAddrPort(conn.RemoteAddr().String()) //nolint:errcheck // always valid from kernel accept()
	addr := remoteAddr.Addr()

	// Read OPEN message to get the remote ASN
	conn.SetDeadline(time.Now().Add(30 * time.Second)) //nolint:errcheck,gosec // deadline is advisory, session dying if Write fails
	msg, err := ReadMessage(conn)
	if err != nil {
		if cerr := conn.Close(); cerr != nil {
			log.Printf("DEBUG: close: %v", cerr)
		}
		return
	}
	open, ok := msg.(*OpenMessage)
	if !ok {
		if cerr := conn.Close(); cerr != nil {
			log.Printf("DEBUG: close: %v", cerr)
		}
		return
	}
	remoteASN := open.MyASN32
	if remoteASN == 0 {
		remoteASN = uint32(open.MyASN)
	}

	// Find peer by address + ASN
	s.mu.Lock()
	key := fmt.Sprintf("%s:%d", addr.String(), remoteASN)
	peer, ok := s.peers[key]
	if !ok {
		// Fallback: try 0.0.0.0 dynamic peer (IPv4)
		key = fmt.Sprintf("%s:%d", "0.0.0.0", remoteASN)
		peer, ok = s.peers[key]
	}
	if !ok {
		// Fallback: try :: dynamic peer (IPv6 wildcard)
		key = fmt.Sprintf("%s:%d", "::", remoteASN)
		peer, ok = s.peers[key]
	}
	s.mu.Unlock()

	if !ok {
		s.logger.Warn("unknown peer", "addr", addr, "asn", remoteASN)
		if cerr := conn.Close(); cerr != nil {
			log.Printf("DEBUG: close: %v", cerr)
		}
		return
	}

	// Prevent duplicate sessions: if this peer already has a connection
	// in progress (e.g., both sides initiated simultaneously), keep the
	// existing session and close the new one.
	if peer.hasEstablishedConn() {
		s.logger.Warn("duplicate connection rejected, peer session in progress",
			"addr", addr, "asn", remoteASN)
		if cerr := conn.Close(); cerr != nil {
			log.Printf("DEBUG: close: %v", cerr)
		}
		return
	}

	// Hand over connection with pre-read OPEN message.
	// TCP MD5 authentication is handled by the kernel on the listener
	// socket (RFC 2385), set via applyListenerMD5 before accepting.
	peer.AcceptWithOpen(conn, open)
}
