package bgp

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"time"

	nfqueue "github.com/florianl/go-nfqueue/v2"
)

const (
	dynamicMD5KeyIdleTimeout   = 10 * time.Minute
	dynamicMD5KeyEvictInterval = time.Minute
	dynamicMD5MaxPacketLen     = 256 // IPv6 header + TCP header + options, no BGP payload needed
)

// dynamicMD5Queue bruteforce-matches TCP MD5 signatures (RFC 2385) on
// inbound SYNs to the BGP port against configured dynamic-peer (0.0.0.0/::)
// passwords, before the connection is accepted. SYNs from known fixed-peer
// addresses pass through untouched — the kernel's own per-address
// TCP_MD5SIG key (installed via applyListenerMD5) already authenticates
// those at the handshake level.
//
// A match installs a real per-source-IP TCP_MD5SIG key on the listener, so
// the kernel authenticates every packet in the session from that point on
// (see md5.go). No match, or no MD5 option at all, drops the SYN outright.
//
// This requires an NFQUEUE redirect rule for the BGP port in this process's
// own network namespace — see the container entrypoint, which installs it
// when this feature is enabled.
type dynamicMD5Queue struct {
	nf       *nfqueue.Nfqueue
	listener net.Listener
	bgpPort  uint16
	logger   *slog.Logger

	mu          sync.Mutex
	fixedAddrs  map[netip.Addr]bool
	dynamicPass []string
	installed   map[netip.Addr]*installedMD5Key
}

type installedMD5Key struct {
	password string
	lastSeen time.Time
}

// startDynamicMD5Queue opens the NFQUEUE consumer and registers the packet
// handler. The queue keeps running until ctx is cancelled.
func startDynamicMD5Queue(ctx context.Context, listener net.Listener, queueNum uint16, bgpPort uint16, logger *slog.Logger) (*dynamicMD5Queue, error) {
	nf, err := nfqueue.Open(&nfqueue.Config{
		NfQueue:      queueNum,
		MaxPacketLen: dynamicMD5MaxPacketLen,
		MaxQueueLen:  1024,
		Copymode:     nfqueue.NfQnlCopyPacket,
		WriteTimeout: 100 * time.Millisecond,
		// AfFamily left at its zero value (NFPROTO_UNSPEC) so a single queue
		// number can serve redirect rules for both IPv4 and IPv6.
	})
	if err != nil {
		return nil, fmt.Errorf("dynamic md5 queue: open nfqueue %d: %w", queueNum, err)
	}

	q := &dynamicMD5Queue{
		nf:         nf,
		listener:   listener,
		bgpPort:    bgpPort,
		logger:     logger,
		fixedAddrs: map[netip.Addr]bool{},
		installed:  map[netip.Addr]*installedMD5Key{},
	}

	if err := nf.RegisterWithErrorFunc(ctx, q.handle, q.handleError); err != nil {
		if cerr := nf.Close(); cerr != nil {
			logger.Debug("dynamic md5 queue: close after failed register", "error", cerr)
		}
		return nil, fmt.Errorf("dynamic md5 queue: register: %w", err)
	}

	go q.evictLoop(ctx)
	return q, nil
}

func (q *dynamicMD5Queue) Close() error {
	return q.nf.Close()
}

// SetPeers updates the fixed-peer addresses to bypass and the dynamic-peer
// passwords to bruteforce against. Called whenever Speaker.SetPeers runs.
//
// Any already-installed key whose password is no longer configured (peer
// deleted, or its password changed) is revoked immediately rather than left
// to idle-evict — an admin removing/rotating a dynamic peer's password is
// revoking access, which shouldn't stay valid for up to
// dynamicMD5KeyIdleTimeout on a connection that happens to still be open.
func (q *dynamicMD5Queue) SetPeers(peers []PeerConfig) {
	fixed := make(map[netip.Addr]bool)
	var dynamic []string
	for _, pc := range peers {
		if pc.Address.IsUnspecified() {
			if pc.Password != "" {
				dynamic = append(dynamic, pc.Password)
			}
			continue
		}
		fixed[pc.Address] = true
	}
	validPass := make(map[string]bool, len(dynamic))
	for _, pw := range dynamic {
		validPass[pw] = true
	}

	q.mu.Lock()
	q.fixedAddrs = fixed
	q.dynamicPass = dynamic
	var revoke []netip.Addr
	for addr, k := range q.installed {
		if !validPass[k.password] {
			revoke = append(revoke, addr)
			delete(q.installed, addr)
		}
	}
	q.mu.Unlock()

	for _, addr := range revoke {
		if err := removeListenerMD5Key(q.listener, addr); err != nil {
			q.logger.Warn("dynamic md5: revoke key failed", "addr", addr, "error", err)
		}
	}
}

func (q *dynamicMD5Queue) handle(a nfqueue.Attribute) int {
	if a.PacketID == nil {
		return 0
	}
	id := *a.PacketID
	verdict := nfqueue.NfAccept
	if a.Payload != nil {
		verdict = q.decide(*a.Payload)
	}
	if err := q.nf.SetVerdict(id, verdict); err != nil {
		q.logger.Warn("dynamic md5: set verdict failed", "error", err)
	}
	return 0
}

func (q *dynamicMD5Queue) handleError(e error) int {
	q.logger.Warn("dynamic md5 queue error", "error", e)
	return 0
}

// decide inspects a captured packet and returns the verdict to apply.
// Anything other than a fresh SYN to our BGP port is accepted untouched —
// this queue only ever needs to judge connection attempts.
func (q *dynamicMD5Queue) decide(payload []byte) int {
	seg, err := parseIPTCPSegment(payload)
	if err != nil {
		return nfqueue.NfAccept
	}
	if seg.DstPort != q.bgpPort || !seg.IsSYN() {
		return nfqueue.NfAccept
	}

	q.mu.Lock()
	isFixed := q.fixedAddrs[seg.SrcAddr]
	passwords := q.dynamicPass
	q.mu.Unlock()

	if isFixed {
		// Already protected by the kernel's per-address key on the listener.
		return nfqueue.NfAccept
	}

	for _, pw := range passwords {
		if seg.matchesPassword(pw) {
			if err := q.install(seg.SrcAddr, pw); err != nil {
				q.logger.Error("dynamic md5: install key failed", "addr", seg.SrcAddr, "error", err)
				return nfqueue.NfDrop
			}
			q.logger.Info("dynamic peer MD5 signature matched", "addr", seg.SrcAddr)
			return nfqueue.NfAccept
		}
	}
	q.logger.Warn("dynamic peer SYN did not match any configured MD5 password, dropping", "addr", seg.SrcAddr)
	return nfqueue.NfDrop
}

func (q *dynamicMD5Queue) install(addr netip.Addr, password string) error {
	q.mu.Lock()
	if k, ok := q.installed[addr]; ok && k.password == password {
		k.lastSeen = time.Now()
		q.mu.Unlock()
		return nil
	}
	q.mu.Unlock()

	if err := addListenerMD5Key(q.listener, addr, password); err != nil {
		return err
	}
	q.mu.Lock()
	q.installed[addr] = &installedMD5Key{password: password, lastSeen: time.Now()}
	q.mu.Unlock()
	return nil
}

func (q *dynamicMD5Queue) evictLoop(ctx context.Context) {
	ticker := time.NewTicker(dynamicMD5KeyEvictInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			q.evictIdle()
		}
	}
}

func (q *dynamicMD5Queue) evictIdle() {
	cutoff := time.Now().Add(-dynamicMD5KeyIdleTimeout)
	q.mu.Lock()
	var toClear []netip.Addr
	for addr, k := range q.installed {
		if k.lastSeen.Before(cutoff) {
			toClear = append(toClear, addr)
			delete(q.installed, addr)
		}
	}
	q.mu.Unlock()
	for _, addr := range toClear {
		if err := removeListenerMD5Key(q.listener, addr); err != nil {
			q.logger.Warn("dynamic md5: evict key failed", "addr", addr, "error", err)
		}
	}
}
