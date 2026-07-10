package bgp

import (
	"errors"
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

	// writeMu serializes all writes to the session socket once it can have
	// multiple writers: the keepalive goroutine, mainLoop (updates,
	// notifications) and the manager's reconcile goroutine (AnnounceRoutes/
	// WithdrawRoutes on an established session) would otherwise interleave
	// partial writes and corrupt the BGP byte stream mid-message.
	writeMu sync.Mutex
}

// writeConn sends one serialized BGP message under writeMu with its own
// write deadline. The deadline matters twice over: mainLoop's SetDeadline
// before each read covers BOTH directions, so an unadorned Write could
// inherit a stale, already-expired read deadline — or block indefinitely
// on a full socket buffer until that shared deadline kills it mid-stream.
func (p *Peer) writeConn(conn net.Conn, data []byte, timeout time.Duration) error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	conn.SetWriteDeadline(time.Now().Add(timeout)) //nolint:errcheck,gosec // deadline is advisory, session dying if Write fails
	_, err := conn.Write(data)
	return err
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
	p.setState(StateConnect)

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
	ht := p.spk.HoldTime
	if ht == 0 {
		ht = 90
	}
	openOut := &OpenMessage{
		Version:  4,
		MyASN32:  p.spk.ASN,
		HoldTime: ht,
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
	localHold := time.Duration(p.spk.HoldTime) * time.Second
	if localHold == 0 {
		localHold = 90 * time.Second
	}
	remoteHold := time.Duration(openIn.HoldTime) * time.Second
	if openIn.HoldTime == 0 { //nolint:gocritic // switch doesn't improve clarity with time.Duration comparisons
		p.holdTime = 0 // no hold timer
	} else if remoteHold < localHold {
		p.holdTime = remoteHold
	} else {
		p.holdTime = localHold
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
	if err := p.sendRoutes(conn); err != nil {
		p.setState(StateIdle)
		return fmt.Errorf("send initial routes: %w", err)
	}

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
			if err := p.sendRoutes(conn); err != nil {
				// needsUpdate is re-armed by sendRoutes; tear the session
				// down so the establish path re-sends the full set.
				return fmt.Errorf("send route update: %w", err)
			}
		}

		// Read message (blocking with deadline)
		msg, err := ReadMessage(conn)
		if err != nil {
			// ReadMessage always wraps the underlying error (fmt.Errorf
			// "...: %w"), so these must unwrap via errors.As/Is — a plain
			// type assertion or == comparison against the wrapped error
			// never matches, silently skipping both branches below.
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				// Hold timer expired — tear down session with NOTIFICATION.
				p.sendNotification(conn, 4, 0, nil) // Hold Timer Expired
				return fmt.Errorf("hold timer expired")
			}
			if errors.Is(err, io.EOF) {
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
	//   - TCP MD5 (RFC 2385) is the primary mechanism. For a non-loopback
	//     fixed peer, it's already enforced by the kernel during the
	//     handshake using the key applyListenerMD5 installed on the
	//     listener before Accept — the kernel copies that key into this
	//     accepted socket automatically as part of completing the
	//     handshake, so reaching this point already proves the signature
	//     matched. There is nothing left to set here; a prior attempt to
	//     redundantly re-set the same key on the already-accepted socket
	//     could itself fail (observed: setsockopt EINVAL on some kernels)
	//     and would needlessly tear down an already-authenticated
	//     connection.
	//   - TCP MD5 is not enforceable on loopback (many kernels, including
	//     WSL2, don't support it there), so loopback validates the
	//     Password field in the OPEN message instead.
	//   - Dynamic peers (0.0.0.0) on non-loopback: the listener can't
	//     preinstall a key for a wildcard address, so this accept is
	//     otherwise unauthenticated. When dynamic-peer MD5 matching is
	//     enabled (nfqueue_md5.go), enforcement already happened earlier:
	//     an NFQUEUE consumer bruteforce-matches the SYN's RFC 2385
	//     signature against configured dynamic-peer passwords and drops
	//     the packet outright on no match, so a connection only reaches
	//     this point at all if it already proved a password (or the
	//     feature is off / its NFQUEUE prerequisites aren't met on this
	//     host, in which case this remains ASN-only identification, same
	//     as before).
	remoteAddr, _ := netip.ParseAddrPort(conn.RemoteAddr().String()) //nolint:errcheck // always valid from kernel accept()
	remoteIP := remoteAddr.Addr()
	if p.cfg.Password != "" && remoteIP.IsLoopback() && openIn.Password != p.cfg.Password {
		p.sendNotification(conn, 5, 0, nil)
		if err := conn.Close(); err != nil {
			p.logger.Debug("close connection", "error", err)
		}
		return
	}

	// Send OPEN — hold time negotiation per RFC 4271
	localHold := p.spk.HoldTime
	if localHold == 0 {
		localHold = 90
	}
	holdTime := localHold
	if openIn.HoldTime == 0 { //nolint:gocritic // switch doesn't improve clarity with time.Duration comparisons
		p.holdTime = 0
	} else if openIn.HoldTime < localHold {
		holdTime = openIn.HoldTime
		p.holdTime = time.Duration(openIn.HoldTime) * time.Second
	} else {
		p.holdTime = time.Duration(localHold) * time.Second
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

	// Initial routes. On failure fall through to teardown — needsUpdate is
	// re-armed by sendRoutes, so the next session re-sends the full set.
	if err := p.sendRoutes(conn); err != nil {
		p.logger.Error("initial route send failed", "error", err)
	} else if err := p.mainLoop(conn); err != nil {
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
	// writeConn gives the write its own fresh deadline: this is called right
	// after a read-deadline expiry (e.g. hold timer), so the connection's
	// existing deadline has, by definition, already passed — without
	// resetting it, this Write would immediately fail with the same stale
	// timeout and the peer would never actually receive the NOTIFICATION.
	if err := p.writeConn(conn, notif.Serialize(), 5*time.Second); err != nil {
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
