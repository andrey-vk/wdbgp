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
	"syscall"
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
	routes      []Route // current announced routes
	state       string
	holdTime    time.Duration
	stopping    atomic.Bool
	needsUpdate atomic.Bool
	connAttempt atomic.Int64
	routeCB     RouteCallback
	hasIPv6Cap  bool // remote peer advertised IPv6 unicast capability
	hasAS4Cap   bool // remote peer advertised Four-octet ASN capability (RFC 6793)
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

// hasEstablishedConn reports whether the peer has any BGP session in progress
// (CONNECT/OPENSENT/OPENCONFIRM/ESTABLISHED). Used to prevent duplicate
// sessions when both sides initiate connections simultaneously.
func (p *Peer) hasEstablishedConn() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.conn != nil
}

// Run connects to the peer and runs the BGP FSM.
func (p *Peer) Run() {
	p.connAttempt.Store(0)
	for !p.stopping.Load() {
		err := p.connectAndRun()
		if p.stopping.Load() {
			return
		}
		if err == nil {
			// Graceful session end — reset backoff and reconnect immediately.
			p.connAttempt.Store(0)
			continue
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
	p.setState(StateConnect)

	// If a session is already active from an inbound accept, don't dial.
	if p.hasEstablishedConn() {
		for !p.stopping.Load() {
			time.Sleep(1 * time.Second)
		}
		return fmt.Errorf("active session from inbound accept")
	}

	// Connect to remote peer
	port := int(p.cfg.Port)
	if port < 0 {
		// Port -1 means passive only (no active dialing — used for
		// server-side peers that accept incoming connections).
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for !p.stopping.Load() {
			<-ticker.C
		}
		return fmt.Errorf("passive peer stopped")
	}
	if port == 0 {
		port = 179
	}
	addr := net.JoinHostPort(p.cfg.Address.String(), strconv.Itoa(port))
	dialer := net.Dialer{Timeout: 10 * time.Second}
	// Bind to per-peer local address if configured (IPv4/IPv6 aware).
	// Falls back to speaker-global LocalAddr for backward compatibility.
	localAddr := p.cfg.LocalAddr
	if !localAddr.IsValid() {
		localAddr = p.spk.LocalAddr
	}
	if localAddr.IsValid() {
		dialer.LocalAddr = &net.TCPAddr{IP: net.IP(localAddr.AsSlice())}
	}
	// Set TCP MD5 before connect so the initial SYN carries the signature.
	// Without this, routers that enforce MD5 will drop our SYN before we
	// get a chance to set the option on the connected socket. RFC 2385.
	if p.cfg.Password != "" {
		dialer.Control = func(network, address string, c syscall.RawConn) error {
			var md5Err error
			ctrlErr := c.Control(func(fd uintptr) {
				md5Err = setTCPMD5OnFd(int(fd), p.cfg.Address, p.cfg.Password)
			})
			if ctrlErr != nil {
				return ctrlErr
			}
			return md5Err
		}
	}
	conn, err := dialer.Dial("tcp", addr)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	p.mu.Lock()
	p.conn = conn
	p.mu.Unlock()
	defer func() { p.mu.Lock(); p.conn = nil; p.mu.Unlock() }()
	defer func() {
		if err := conn.Close(); err != nil {
			p.logger.Debug("close connection", "error", err)
		}
	}()

	// Deadlines for read/write
	conn.SetDeadline(time.Now().Add(30 * time.Second)) //nolint:errcheck,gosec // deadline is advisory, session dying if Write fails

	// Step 1: Send OPEN
	bgpID := p.spk.RouterID.As4()
	openOut := &OpenMessage{
		Version:  4,
		MyASN32:  p.spk.ASN,
		HoldTime: 90,
		BGPID:    bgpID,
	}
	// Only include Password in OPEN for loopback connections where TCP MD5
	// is not enforced by the kernel. Real routers may reject unknown
	// optional parameters, so this fallback is limited to loopback.
	if p.cfg.Address.IsLoopback() {
		openOut.Password = p.cfg.Password
	}
	if _, err := conn.Write(openOut.Serialize()); err != nil {
		return fmt.Errorf("write open: %w", err)
	}
	p.setState(StateOpenSent)

	// Step 2: Receive OPEN
	conn.SetDeadline(time.Now().Add(30 * time.Second)) //nolint:errcheck,gosec // deadline is advisory, session dying if Write fails
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
		// Check four-octet ASN capability (RFC 6793) — the 2-byte field
		// may be AS_TRANS (23456) while the real ASN is in the capability.
		remoteASN := openIn.MyASN32
		if remoteASN == 0 {
			remoteASN = uint32(openIn.MyASN)
		}
		if remoteASN != p.cfg.ASN {
			p.sendNotification(conn, 2, 2, nil) // bad peer AS
			return fmt.Errorf("peer ASN mismatch: got %d, want %d", remoteASN, p.cfg.ASN)
		}
	}
	// Validate remote password when loopback (TCP MD5 not enforced).
	// On non-loopback, TCP MD5 handles authentication.
	if p.cfg.Address.IsLoopback() && p.cfg.Password != "" {
		if openIn.Password != p.cfg.Password {
			p.sendNotification(conn, 5, 0, nil)
			return fmt.Errorf("peer password mismatch")
		}
	}

	// Negotiate hold time per RFC 4271: use min(local, remote), 0 means disabled.
	remoteHold := time.Duration(openIn.HoldTime) * time.Second
	if openIn.HoldTime == 0 { //nolint:gocritic // switch doesn't improve clarity with time.Duration comparisons
		p.holdTime = 0 // no hold timer
	} else if remoteHold < 90*time.Second {
		p.holdTime = remoteHold
	} else {
		p.holdTime = 90 * time.Second
	}

	// Track remote IPv6 unicast capability so we skip IPv6 routes
	// to IPv4-only peers.
	p.hasIPv6Cap = openIn.HasIPv6Unicast
	p.hasAS4Cap = openIn.HasAS4Cap

	// Step 3: Send KEEPALIVE
	ka := &KeepaliveMessage{}
	if _, err := conn.Write(ka.Serialize()); err != nil {
		return fmt.Errorf("write keepalive: %w", err)
	}
	p.setState(StateOpenConfirm)

	// Step 4: Receive KEEPALIVE
	conn.SetDeadline(time.Now().Add(30 * time.Second)) //nolint:errcheck,gosec // deadline is advisory, session dying if Write fails
	msg, err = ReadMessage(conn)
	if err != nil {
		return fmt.Errorf("read keepalive: %w", err)
	}

	switch msg.(type) { //nolint:gocritic,staticcheck // 1 case benefits from type switch with assignment but explicit cast is clearer
	case *KeepaliveMessage:
		// All good, established
	case *NotificationMessage:
		nm := msg.(*NotificationMessage) //nolint:errcheck,staticcheck // guarded by type switch
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
	err = p.mainLoop(conn)
	p.setState(StateIdle)
	return err
}

// mainLoop handles incoming messages, periodic keepalives, and route updates
// for an established BGP session.
func (p *Peer) mainLoop(conn net.Conn) error {
	holdTime := p.holdTime
	if holdTime <= 0 {
		holdTime = 0 // disabled, no deadline (RFC 4271: 0 means no hold timer)
	}

	// Dedicated keepalive sender goroutine — writes keepalives at holdTime/3
	// independently of the blocking read in the main goroutine.
	kaStop := make(chan struct{})
	defer close(kaStop)
	if holdTime > 0 {
		go p.keepaliveLoop(conn, holdTime, kaStop)
	} else {
		// Clear stale handshake deadline from connectAndRun/AcceptWithOpen.
		conn.SetDeadline(time.Time{}) //nolint:errcheck,gosec // deadline is advisory, session dying if Write fails
	}

	for !p.stopping.Load() {
		if holdTime > 0 {
			conn.SetDeadline(time.Now().Add(holdTime)) //nolint:errcheck,gosec // deadline is advisory, session dying if Write fails
		}

		// Check for pending route updates
		if p.needsUpdate.Swap(false) {
			p.sendRoutes(conn)
		}

		// Read message (blocking with deadline)
		msg, err := ReadMessage(conn)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				// Hold timer expired — tear down session with NOTIFICATION.
				p.sendNotification(conn, 4, 0, nil) // Hold Timer Expired
				return fmt.Errorf("hold timer expired")
			}
			if err == io.EOF {
				return fmt.Errorf("peer closed connection")
			}
			return fmt.Errorf("read: %w", err)
		}

		switch msg.(type) { //nolint:gocritic,staticcheck // single-case type switch is intentional for extensibility
		case *KeepaliveMessage:
			// Received keepalive — reset hold timer
		case *UpdateMessage:
			// Invoke route callback if set
			if p.routeCB != nil {
				upd := msg.(*UpdateMessage) //nolint:errcheck // guarded by type switch
				nlri := upd.NLRI
				// Extract NLRI from MP_REACH attributes for IPv6 (RFC 4760).
				for _, attr := range upd.PathAttributes {
					if mp, ok := attr.(*MpReachNLRIAttribute); ok {
						nlri = append(nlri, mp.NLRI...)
					}
				}
				p.routeCB(nlri)
			}
		case *NotificationMessage:
			nm := msg.(*NotificationMessage) //nolint:errcheck,staticcheck // guarded by type switch
			return fmt.Errorf("received notification: code=%d sub=%d", nm.ErrorCode, nm.ErrorSubcode)
		}
	}
	return nil
}

// Accept handles a passive BGP connection. Reads the OPEN, validates,
// then delegates to AcceptWithOpen.
func (p *Peer) Accept(conn net.Conn) {
	p.mu.Lock()
	p.conn = conn
	p.mu.Unlock()
	defer func() { p.mu.Lock(); p.conn = nil; p.mu.Unlock() }()
	defer func() {
		if err := conn.Close(); err != nil {
			p.logger.Debug("close connection", "error", err)
		}
	}()

	// Receive OPEN first (passive side)
	conn.SetDeadline(time.Now().Add(30 * time.Second)) //nolint:errcheck,gosec // deadline is advisory, session dying if Write fails
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
	p.mu.Lock()
	p.conn = conn
	p.mu.Unlock()
	defer func() { p.mu.Lock(); p.conn = nil; p.mu.Unlock() }()

	// Validate ASN — use four-octet ASN capability (RFC 6793) if present
	remoteASN := openIn.MyASN32
	if remoteASN == 0 {
		remoteASN = uint32(openIn.MyASN)
	}
	if remoteASN != p.cfg.ASN {
		p.sendNotification(conn, 2, 2, nil)
		if err := conn.Close(); err != nil {
			p.logger.Debug("close connection", "error", err)
		}
		return
	}

	// Password authentication:
	//   - TCP MD5 (RFC 2385) is the primary mechanism, set on the socket.
	//   - If TCP MD5 is not available (e.g., loopback), validate via the
	//     Password field in the OPEN message as a fallback.
	//   - Dynamic peers (0.0.0.0) on non-loopback cannot use MD5 at the
	//     handshake level (listener can't preinstall keys for 0.0.0.0).
	remoteAddr, _ := netip.ParseAddrPort(conn.RemoteAddr().String()) //nolint:errcheck // always valid from kernel accept()
	remoteIP := remoteAddr.Addr()
	if p.cfg.Password != "" {
		if p.cfg.Address.IsUnspecified() && !remoteIP.IsLoopback() {
			// Dynamic peer on non-loopback: cannot authenticate.
			// TCP MD5 cannot be preinstalled for wildcard addresses,
			// and OPEN password is not validated. Accept silently.
		} else if err := setTCPMD5OnConn(conn, remoteIP, p.cfg.Password); err != nil {
			p.logger.Error("accept: tcp md5 set failed", "error", err)
			if err := conn.Close(); err != nil {
				p.logger.Debug("close connection", "error", err)
			}
			return
		}
		// Fallback: validate password from OPEN message when TCP MD5
		// is skipped (e.g., on loopback where MD5 is not enforced).
		if remoteIP.IsLoopback() && openIn.Password != p.cfg.Password {
			p.sendNotification(conn, 5, 0, nil)
			if err := conn.Close(); err != nil {
				p.logger.Debug("close connection", "error", err)
			}
			return
		}
	}

	// Send OPEN — hold time negotiation per RFC 4271
	holdTime := uint16(90)
	if openIn.HoldTime == 0 { //nolint:gocritic // switch doesn't improve clarity with time.Duration comparisons
		p.holdTime = 0
	} else if openIn.HoldTime < 90 {
		holdTime = openIn.HoldTime
		p.holdTime = time.Duration(openIn.HoldTime) * time.Second
	} else {
		p.holdTime = 90 * time.Second
	}
	bgpID := p.spk.RouterID.As4()
	var id [4]byte
	copy(id[:], bgpID[:])
	openOut := &OpenMessage{Version: 4, MyASN32: p.spk.ASN, HoldTime: holdTime, BGPID: id}
	// Only include Password in OPEN for loopback connections.
	if p.cfg.Password != "" {
		if remoteIP.IsLoopback() {
			openOut.Password = p.cfg.Password
		}
	}
	if _, err := conn.Write(openOut.Serialize()); err != nil {
		p.logger.Error("accept: write open", "error", err)
		if err := conn.Close(); err != nil {
			p.logger.Debug("close connection", "error", err)
		}
		return
	}
	p.setState(StateOpenSent)

	// Send KEEPALIVE
	ka := &KeepaliveMessage{}
	if _, err := conn.Write(ka.Serialize()); err != nil {
		p.logger.Error("accept: write keepalive", "error", err)
		if err := conn.Close(); err != nil {
			p.logger.Debug("close connection", "error", err)
		}
		return
	}
	p.setState(StateOpenConfirm)

	// Receive KEEPALIVE
	conn.SetDeadline(time.Now().Add(30 * time.Second)) //nolint:errcheck,gosec // deadline is advisory, session dying if Write fails
	msg, err := ReadMessage(conn)
	if err != nil {
		p.logger.Error("accept: read keepalive", "error", err)
		if err := conn.Close(); err != nil {
			p.logger.Debug("close connection", "error", err)
		}
		return
	}
	switch msg.(type) { //nolint:gocritic,staticcheck // 1 case benefits from type switch with assignment but explicit cast is clearer
	case *KeepaliveMessage:
		// established
	case *NotificationMessage:
		nm := msg.(*NotificationMessage) //nolint:errcheck,staticcheck // guarded by type switch
		p.logger.Error("accept: received notification", "code", nm.ErrorCode, "sub", nm.ErrorSubcode)
		if err := conn.Close(); err != nil {
			p.logger.Debug("close connection", "error", err)
		}
		return
	default:
		p.sendNotification(conn, 5, 0, nil)
		if err := conn.Close(); err != nil {
			p.logger.Debug("close connection", "error", err)
		}
		return
	}

	p.setState(StateEstablished)
	p.logger.Info("BGP session established (passive)")

	// Track remote IPv6 unicast capability so we skip IPv6 routes
	// to IPv4-only peers.
	p.hasIPv6Cap = openIn.HasIPv6Unicast
	p.hasAS4Cap = openIn.HasAS4Cap

	// Initial routes
	p.sendRoutes(conn)

	// Main loop
	if err := p.mainLoop(conn); err != nil {
		p.logger.Error("main loop error", "error", err)
	}
	p.setState(StateIdle)
	if err := conn.Close(); err != nil {
		p.logger.Debug("close connection", "error", err)
	}
}

// sendNotification sends a BGP NOTIFICATION message.
func (p *Peer) sendNotification(conn net.Conn, code, subcode uint8, data []byte) {
	notif := &NotificationMessage{
		ErrorCode:    code,
		ErrorSubcode: subcode,
		Data:         data,
	}
	if _, err := conn.Write(notif.Serialize()); err != nil {
		p.logger.Debug("send notification write", "error", err)
	}
	// Write error is intentionally ignored — the session is being torn down
	// and the connection may already be closed by the peer.
}

func (p *Peer) Stop() {
	p.stopping.Store(true)
	p.mu.Lock()
	if p.conn != nil {
		if err := p.conn.Close(); err != nil {
			p.logger.Debug("close connection", "error", err)
		}
		p.conn = nil
	}
	p.mu.Unlock()
}

func (p *Peer) setState(state string) {
	p.mu.Lock()
	p.state = state
	p.mu.Unlock()
	p.logger.Debug("state change", "new", state)
}
