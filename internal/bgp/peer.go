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
	p.setState(StateConnect)
	p.connAttempt.Store(0)

	// Connect to remote peer
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
	p.conn = conn
	defer conn.Close()

	// Deadlines for read/write
	conn.SetDeadline(time.Now().Add(30 * time.Second))

	// Step 1: Send OPEN
	bgpID := p.spk.RouterID.As4()
	openOut := &OpenMessage{
		Version:  4,
		MyASN32:  p.spk.ASN,
		HoldTime: 90,
		BGPID:    bgpID,
		Password: p.cfg.Password, // fallback for loopback where TCP MD5 is not enforced
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

	// Negotiate hold time: use the lower of local (90s) and remote
	if remoteHold := time.Duration(openIn.HoldTime) * time.Second; remoteHold > 0 && remoteHold < 90*time.Second {
		p.holdTime = remoteHold
	} else {
		p.holdTime = 90 * time.Second
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
	err = p.mainLoop(conn)
	p.setState(StateIdle)
	return err
}

// mainLoop handles incoming messages, periodic keepalives, and route updates
// for an established BGP session.
func (p *Peer) mainLoop(conn net.Conn) error {
	ka := &KeepaliveMessage{}
	holdTime := p.holdTime
	if holdTime <= 0 {
		holdTime = 90 * time.Second
	}
	ticker := time.NewTicker(holdTime / 3) // KEEPALIVE interval is holdTime / 3
	defer ticker.Stop()

	for !p.stopping.Load() {
		conn.SetDeadline(time.Now().Add(holdTime)) // hold time

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
		var attrs []PathAttribute
		var update *UpdateMessage

		if r.Prefix.Addr().Is6() {
			// IPv6: NLRI goes inside MP_REACH per RFC 4760.
			// UPDATE.NLRI must be empty.
			attrs = []PathAttribute{
				OriginAttribute(OriginIGP),
				&ASPathAttribute{ASN: p.spk.ASN},
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
				&ASPathAttribute{ASN: p.spk.ASN},
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

	// Validate ASN — use four-octet ASN capability (RFC 6793) if present
	remoteASN := openIn.MyASN32
	if remoteASN == 0 {
		remoteASN = uint32(openIn.MyASN)
	}
	if remoteASN != p.cfg.ASN {
		p.sendNotification(conn, 2, 2, nil)
		conn.Close()
		return
	}

	// Password authentication:
	//   - TCP MD5 (RFC 2385) is the primary mechanism, set on the socket.
	//   - If TCP MD5 is not available (e.g., loopback), validate via the
	//     Password field in the OPEN message as a fallback.
	if p.cfg.Password != "" {
		remoteAddr, _ := netip.ParseAddrPort(conn.RemoteAddr().String())
		remoteIP := remoteAddr.Addr()
		if err := setTCPMD5OnConn(conn, remoteIP, p.cfg.Password); err != nil {
			p.logger.Error("accept: tcp md5 set failed", "error", err)
			conn.Close()
			return
		}
		// Fallback: validate password from OPEN message when TCP MD5
		// is skipped (e.g., on loopback where MD5 is not enforced).
		if remoteIP.IsLoopback() && openIn.Password != p.cfg.Password {
			p.sendNotification(conn, 5, 0, nil)
			conn.Close()
			return
		}
	}

	// Send OPEN
	bgpID := p.spk.RouterID.As4()
	var id [4]byte
	copy(id[:], bgpID[:])
	openOut := &OpenMessage{Version: 4, MyASN32: p.spk.ASN, HoldTime: 90, BGPID: id, Password: p.cfg.Password}
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
	p.setState(StateIdle)
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
