package bgp

import (
	"context"
	"fmt"
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
}

// SpeakerConfig holds BGP speaker configuration.
type SpeakerConfig struct {
	ASN       uint32
	RouterID  netip.Addr // IPv4 router ID (e.g., 192.0.2.1)
	Port      int32
	LocalAddr netip.Addr // IPv4 local address for NEXT_HOP
}

// PeerConfig describes a configured BGP peer.
type PeerConfig struct {
	ID       int64
	Address  netip.Addr
	Port     int32  // destination port (0 = default 179)
	ASN      uint32
	Password string // MD5 password (empty = none)
	Name     string // description
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
		addr := fmt.Sprintf(":%d", s.cfg.Port)
		var lc net.ListenConfig
		l, err := lc.Listen(ctx, "tcp", addr)
		if err != nil {
			return fmt.Errorf("bgp listen %s: %w", addr, err)
		}
		s.listener = l

		ctx, s.cancel = context.WithCancel(ctx)
		go s.acceptLoop(ctx)
	}

	s.started.Store(true)
	s.logger.Info("BGP speaker started", "port", s.cfg.Port)
	return nil
}

// Stop shuts down the speaker.
func (s *Speaker) Stop() error {
	s.started.Store(false)
	if s.cancel != nil {
		s.cancel()
	}
	if s.listener != nil {
		s.listener.Close()
		s.listener = nil
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
func (s *Speaker) SetPeers(peers []PeerConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()

	newSet := make(map[string]PeerConfig, len(peers))
	for _, pc := range peers {
		key := fmt.Sprintf("%s:%d", pc.Address.String(), pc.ASN)
		newSet[key] = pc
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
			// Peer exists — check if config changed
			if existing.PeerConfig().ASN != pc.ASN || existing.PeerConfig().Password != pc.Password {
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
	p.AnnounceRoutes(routes)
	return nil
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
	p.WithdrawRoutes(prefixes)
	return nil
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
	remoteAddr, _ := netip.ParseAddrPort(conn.RemoteAddr().String())
	addr := remoteAddr.Addr()

	// Set TCP MD5 on the accepted socket as early as possible (before any
	// data exchange). Look up the password by remote address.
	if pw := s.passwordForAddr(addr); pw != "" {
		if err := setTCPMD5OnConn(conn, addr, pw); err != nil {
			s.logger.Error("tcp md5 set failed on accept", "addr", addr, "error", err)
			conn.Close()
			return
		}
	}

	// Read OPEN message to get the remote ASN
	conn.SetDeadline(time.Now().Add(30 * time.Second))
	msg, err := ReadMessage(conn)
	if err != nil {
		conn.Close()
		return
	}
	open, ok := msg.(*OpenMessage)
	if !ok {
		conn.Close()
		return
	}
	remoteASN := uint32(open.MyASN)

	// Find peer by address + ASN
	s.mu.Lock()
	key := fmt.Sprintf("%s:%d", addr.String(), remoteASN)
	peer, ok := s.peers[key]
	if !ok {
		// Fallback: try 0.0.0.0 dynamic peer
		key = fmt.Sprintf("%s:%d", "0.0.0.0", remoteASN)
		peer, ok = s.peers[key]
	}
	s.mu.Unlock()

	if !ok {
		s.logger.Warn("unknown peer", "addr", addr, "asn", remoteASN)
		conn.Close()
		return
	}

	// Hand over connection with pre-read OPEN message
	peer.AcceptWithOpen(conn, open)
}

// passwordForAddr returns the MD5 password for a given remote address, or ""
// if no configured peer with a password matches this address.
func (s *Speaker) passwordForAddr(addr netip.Addr) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	// First check for exact address match
	for _, p := range s.peers {
		cfg := p.PeerConfig()
		if cfg.Address == addr && cfg.Password != "" {
			return cfg.Password
		}
	}

	// Fallback: dynamic peer (0.0.0.0) password
	for _, p := range s.peers {
		cfg := p.PeerConfig()
		if cfg.Address.String() == "0.0.0.0" && cfg.Password != "" {
			return cfg.Password
		}
	}

	return ""
}
