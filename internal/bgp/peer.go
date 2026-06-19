package bgp

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const (
	StateIdle        = "IDLE"
	StateConnect     = "CONNECT"
	StateOpenSent    = "OPENSENT"
	StateOpenConfirm = "OPENCONFIRM"
	StateEstablished = "ESTABLISHED"
)

// RouteCallback is called when the peer receives an UPDATE message.
// The callback receives the NLRI prefixes from the received UPDATE.
type RouteCallback func(prefixes []netip.Prefix)

type Peer struct {
	mu          sync.Mutex
	cfg         PeerConfig
	spk         SpeakerConfig
	logger      *slog.Logger
	conn        net.Conn
	routes      []Route          // current announced routes
	state       string
	holdTime    time.Duration
	remoteID    netip.Addr
	stopping    atomic.Bool
	needsUpdate atomic.Bool
	connAttempt atomic.Int64
	routeCB     RouteCallback
}

func NewPeer(cfg PeerConfig, spk SpeakerConfig, logger *slog.Logger, routeCB RouteCallback) *Peer {
	return &Peer{
		cfg:     cfg,
		spk:     spk,
		logger:  logger.With("peer", cfg.Address.String(), "asn", cfg.ASN),
		state:   StateIdle,
		routeCB: routeCB,
	}
}

func (p *Peer) PeerConfig() PeerConfig { return p.cfg }

func (p *Peer) State() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}

func (p *Peer) TriggerUpdate() {
	p.needsUpdate.Store(true)
}

// Run connects to the peer and runs the BGP FSM.
func (p *Peer) Run() {
	for !p.stopping.Load() {
		err := p.connectAndRun()
		if p.stopping.Load() {
			return
		}
		backoff := time.Duration(p.connAttempt.Add(1)) * time.Second
		if backoff > 60*time.Second {
			backoff = 60 * time.Second
		}
		p.logger.Error("connection failed, reconnecting", "backoff", backoff, "error", err)
		time.Sleep(backoff)
	}
}

func (p *Peer) connectAndRun() error {
	if err := validateASN(p.spk.ASN); err != nil {
		return err
	}
	p.setState(StateConnect)
	p.connAttempt.Store(0)

	// Connect to remote peer
	dialer := net.Dialer{Timeout: 10 * time.Second}
	port := int(p.cfg.Port)
	if port < 0 {
		// Port -1 means passive only (no active dialing — used for
		// server-side peers that accept incoming connections).
		for !p.stopping.Load() {
			time.Sleep(1 * time.Second)
		}
		return fmt.Errorf("passive peer stopped")
	}
	if port == 0 {
		port = 179
	}
	addr := net.JoinHostPort(p.cfg.Address.String(), strconv.Itoa(port))
	// Bind to local address only if it's a loopback address (used for tests
	// that need clients to appear from different 127.x.y.z addresses).
	if p.spk.LocalAddr.IsValid() && p.spk.LocalAddr.IsLoopback() {
		dialer.LocalAddr = &net.TCPAddr{IP: net.IP(p.spk.LocalAddr.AsSlice())}
	}
	conn, err := dialer.Dial("tcp", addr)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	p.conn = conn
	defer conn.Close()

	// Deadlines for read/write
	conn.SetDeadline(time.Now().Add(30 * time.Second))

	// Step 1: Send OPEN
	bgpID := p.spk.RouterID.As4()
	openOut := &OpenMessage{
		Version:    4,
		MyASN:      uint16(p.spk.ASN),
		HoldTime:   90,
		BGPID:      bgpID,
		OptParmLen: 0,
		Password:   p.cfg.Password,
	}
	if _, err := conn.Write(openOut.Serialize()); err != nil {
		return fmt.Errorf("write open: %w", err)
	}
	p.setState(StateOpenSent)

	// Step 2: Receive OPEN
	conn.SetDeadline(time.Now().Add(30 * time.Second))
	msg, err := ReadMessage(conn)
	if err != nil {
		return fmt.Errorf("read open: %w", err)
	}
	openIn, ok := msg.(*OpenMessage)
	if !ok {
		p.sendNotification(conn, 2, 4, nil) // bad message type
		return fmt.Errorf("expected OPEN, got %T", msg)
	}
	if uint32(openIn.MyASN) != p.cfg.ASN {
		p.sendNotification(conn, 2, 2, nil) // bad peer AS
		return fmt.Errorf("peer ASN mismatch: got %d, want %d", openIn.MyASN, p.cfg.ASN)
	}

	// Validate remote password if client expects one
	if p.cfg.Password != "" && openIn.Password != p.cfg.Password {
		p.sendNotification(conn, 5, 0, nil)
		return fmt.Errorf("peer password mismatch")
	}

	// Step 3: Send KEEPALIVE
	ka := &KeepaliveMessage{}
	if _, err := conn.Write(ka.Serialize()); err != nil {
		return fmt.Errorf("write keepalive: %w", err)
	}
	p.setState(StateOpenConfirm)

	// Step 4: Receive KEEPALIVE
	conn.SetDeadline(time.Now().Add(30 * time.Second))
	msg, err = ReadMessage(conn)
	if err != nil {
		return fmt.Errorf("read keepalive: %w", err)
	}

	switch msg.(type) {
	case *KeepaliveMessage:
		// All good, established
	case *NotificationMessage:
		nm := msg.(*NotificationMessage)
		return fmt.Errorf("received notification: code=%d sub=%d", nm.ErrorCode, nm.ErrorSubcode)
	default:
		p.sendNotification(conn, 5, 0, nil) // FSM error
		return fmt.Errorf("expected KEEPALIVE, got %T", msg)
	}

	p.setState(StateEstablished)
	p.logger.Info("BGP session established")

	// Step 5: Send initial routes
	p.sendRoutes(conn)

	// Step 6: Main loop
	return p.mainLoop(conn)
}

// mainLoop handles incoming messages, periodic keepalives, and route updates
// for an established BGP session.
func (p *Peer) mainLoop(conn net.Conn) error {
	ka := &KeepaliveMessage{}
	ticker := time.NewTicker(30 * time.Second) // KEEPALIVE interval
	defer ticker.Stop()

	for !p.stopping.Load() {
		conn.SetDeadline(time.Now().Add(60 * time.Second)) // hold time

		// Non-blocking check for keepalive tick
		select {
		case <-ticker.C:
			if _, err := conn.Write(ka.Serialize()); err != nil {
				return fmt.Errorf("write keepalive: %w", err)
			}
		default:
		}

		// Check for pending route updates
		if p.needsUpdate.Swap(false) {
			p.sendRoutes(conn)
		}

		// Read message (blocking with deadline)
		msg, err := ReadMessage(conn)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue // deadline hit, loop again
			}
			if err == io.EOF {
				return fmt.Errorf("peer closed connection")
			}
			return fmt.Errorf("read: %w", err)
		}

		switch msg.(type) {
		case *KeepaliveMessage:
			// Received keepalive — reset hold timer
		case *UpdateMessage:
			// Invoke route callback if set
			if p.routeCB != nil {
				upd := msg.(*UpdateMessage)
				p.routeCB(upd.NLRI)
			}
		case *NotificationMessage:
			nm := msg.(*NotificationMessage)
			return fmt.Errorf("received notification: code=%d sub=%d", nm.ErrorCode, nm.ErrorSubcode)
		}
	}
	return nil
}

func (p *Peer) sendRoutes(conn net.Conn) {
	p.mu.Lock()
	routes := p.routes
	p.mu.Unlock()

	if len(routes) == 0 {
		return
	}

	for _, r := range routes {
		var nhAttr PathAttribute
		if r.Prefix.Addr().Is6() {
			nhAttr = &MpReachNLRIAttribute{NextHop: r.NextHop}
		} else {
			nhAttr = &NextHopAttribute{NextHop: r.NextHop}
		}
		attrs := []PathAttribute{
			OriginAttribute(OriginIGP),
			&ASPathAttribute{ASN: p.spk.ASN},
			nhAttr,
			&LargeCommunitiesAttribute{Communities: r.Communities},
		}
		update := &UpdateMessage{
			PathAttributes: attrs,
			NLRI:           []netip.Prefix{r.Prefix},
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
		update := &UpdateMessage{
			WithdrawnRoutes: prefixes,
		}
		if _, err := conn.Write(update.Serialize()); err != nil {
			p.logger.Error("failed to send withdrawal", "error", err)
		}
		p.logger.Debug("withdrew routes", "count", len(prefixes))
	}
}

// Accept handles a passive BGP connection. Reads the OPEN, validates,
// then delegates to AcceptWithOpen.
func (p *Peer) Accept(conn net.Conn) {
	p.conn = conn
	defer conn.Close()

	// Receive OPEN first (passive side)
	conn.SetDeadline(time.Now().Add(30 * time.Second))
	msg, err := ReadMessage(conn)
	if err != nil {
		p.logger.Error("accept: read open", "error", err)
		return
	}
	openIn, ok := msg.(*OpenMessage)
	if !ok {
		p.sendNotification(conn, 2, 4, nil)
		return
	}

	p.AcceptWithOpen(conn, openIn)
}

// AcceptWithOpen handles a passive connection with a pre-read OPEN message.
func (p *Peer) AcceptWithOpen(conn net.Conn, openIn *OpenMessage) {
	p.conn = conn

	// Validate our own ASN before sending OPEN
	if err := validateASN(p.spk.ASN); err != nil {
		p.logger.Error("accept: invalid speaker ASN", "error", err)
		conn.Close()
		return
	}

	// Validate ASN
	if uint32(openIn.MyASN) != p.cfg.ASN {
		p.sendNotification(conn, 2, 2, nil)
		conn.Close()
		return
	}

	// Validate password if configured
	if p.cfg.Password != "" && openIn.Password != p.cfg.Password {
		p.sendNotification(conn, 5, 0, nil)
		conn.Close()
		return
	}

	// Send OPEN
	bgpID := p.spk.RouterID.As4()
	var id [4]byte
	copy(id[:], bgpID[:])
	openOut := &OpenMessage{Version: 4, MyASN: uint16(p.spk.ASN), HoldTime: 90, BGPID: id, Password: p.cfg.Password}
	if _, err := conn.Write(openOut.Serialize()); err != nil {
		p.logger.Error("accept: write open", "error", err)
		conn.Close()
		return
	}
	p.setState(StateOpenSent)

	// Send KEEPALIVE
	ka := &KeepaliveMessage{}
	if _, err := conn.Write(ka.Serialize()); err != nil {
		p.logger.Error("accept: write keepalive", "error", err)
		conn.Close()
		return
	}
	p.setState(StateOpenConfirm)

	// Receive KEEPALIVE
	conn.SetDeadline(time.Now().Add(30 * time.Second))
	msg, err := ReadMessage(conn)
	if err != nil {
		p.logger.Error("accept: read keepalive", "error", err)
		conn.Close()
		return
	}
	switch msg.(type) {
	case *KeepaliveMessage:
		// established
	case *NotificationMessage:
		nm := msg.(*NotificationMessage)
		p.logger.Error("accept: received notification", "code", nm.ErrorCode, "sub", nm.ErrorSubcode)
		conn.Close()
		return
	default:
		p.sendNotification(conn, 5, 0, nil)
		conn.Close()
		return
	}

	p.setState(StateEstablished)
	p.logger.Info("BGP session established (passive)")

	// Initial routes
	p.sendRoutes(conn)

	// Main loop
	if err := p.mainLoop(conn); err != nil {
		p.logger.Error("main loop error", "error", err)
	}
	conn.Close()
}

// sendNotification sends a BGP NOTIFICATION message.
func (p *Peer) sendNotification(conn net.Conn, code, subcode uint8, data []byte) {
	notif := &NotificationMessage{
		ErrorCode:    code,
		ErrorSubcode: subcode,
		Data:         data,
	}
	conn.Write(notif.Serialize())
}

func (p *Peer) Stop() {
	p.stopping.Store(true)
	if p.conn != nil {
		p.conn.Close()
	}
}

func (p *Peer) setState(state string) {
	p.mu.Lock()
	p.state = state
	p.mu.Unlock()
	p.logger.Debug("state change", "new", state)
}

// validateASN returns an error if the ASN exceeds the 16-bit limit of the OPEN message.
func validateASN(asn uint32) error {
	if asn > 65535 {
		return fmt.Errorf("ASN %d exceeds 16-bit limit, 4-byte ASN not yet supported", asn)
	}
	return nil
}
