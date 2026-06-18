package bgp

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
)

// Speaker is the BGP speaker — listens on TCP, accepts connections, manages peers.
type Speaker struct {
	mu          sync.Mutex
	cfg         SpeakerConfig
	logger      *slog.Logger
	listener    net.Listener
	peers       map[netip.Addr]*Peer
	peerConfigs []PeerConfig // configured peers (from DB)
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
		peers:  make(map[netip.Addr]*Peer),
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
	s.peers = make(map[netip.Addr]*Peer)
	s.logger.Info("BGP speaker stopped")
	return nil
}

// SetPeers updates the configured peer list. New peers are started,
// removed peers are stopped, existing peers are updated if changed.
func (s *Speaker) SetPeers(peers []PeerConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()

	newSet := make(map[netip.Addr]PeerConfig, len(peers))
	for _, pc := range peers {
		newSet[pc.Address] = pc
	}

	// Stop peers that were removed
	for addr, p := range s.peers {
		if _, ok := newSet[addr]; !ok {
			p.Stop()
			delete(s.peers, addr)
		}
	}

	// Start new peers or update existing
	for addr, pc := range newSet {
		if existing, ok := s.peers[addr]; ok {
			// Peer exists — check if config changed
			if existing.PeerConfig().ASN != pc.ASN || existing.PeerConfig().Password != pc.Password {
				existing.Stop()
				delete(s.peers, addr)
			} else {
				continue // no change
			}
		}
		// Start new peer
		p := NewPeer(pc, s.cfg, s.logger)
		go p.Run()
		s.peers[addr] = p
	}

	s.peerConfigs = peers
}

// PeerStates returns the BGP state for each configured peer.
func (s *Speaker) PeerStates() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	states := make(map[string]string, len(s.peers))
	for addr, p := range s.peers {
		states[addr.String()] = p.State()
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

// Announce pushes routes to a specific peer, identified by its address.
func (s *Speaker) Announce(addr netip.Addr, routes []Route) error {
	s.mu.Lock()
	p, ok := s.peers[addr]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("peer %s not found", addr)
	}
	p.AnnounceRoutes(routes)
	return nil
}

// Withdraw removes prefixes from a specific peer.
func (s *Speaker) Withdraw(addr netip.Addr, prefixes []netip.Prefix) error {
	s.mu.Lock()
	p, ok := s.peers[addr]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("peer %s not found", addr)
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

	s.mu.Lock()
	peer, ok := s.peers[addr]
	s.mu.Unlock()

	if !ok {
		s.logger.Warn("connection from unknown peer", "addr", addr)
		conn.Close()
		return
	}

	peer.Accept(conn)
}
