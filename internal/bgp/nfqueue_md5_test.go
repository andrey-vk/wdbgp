package bgp

import (
	"io"
	"log/slog"
	"net"
	"net/netip"
	"testing"
	"time"

	nfqueue "github.com/florianl/go-nfqueue/v2"
)

// newTestDynamicMD5Queue builds a dynamicMD5Queue around a real loopback
// listener, without opening an actual NFQUEUE socket (which needs root/
// CAP_NET_ADMIN and isn't available in a normal test environment). Loopback
// addresses make addListenerMD5Key/removeListenerMD5Key no-ops (see
// setTCPMD5OnFd/clearTCPMD5OnFd), so this exercises the queue's matching and
// bookkeeping logic without touching the kernel's TCP_MD5SIG table.
func newTestDynamicMD5Queue(t *testing.T) *dynamicMD5Queue {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() }) //nolint:errcheck // test cleanup, best-effort

	return &dynamicMD5Queue{
		listener:   l,
		bgpPort:    179,
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		fixedAddrs: map[netip.Addr]bool{},
		installed:  map[netip.Addr]*installedMD5Key{},
	}
}

func TestDynamicMD5QueueDecideNonSYNAccepted(t *testing.T) {
	q := newTestDynamicMD5Queue(t)
	q.SetPeers([]PeerConfig{{Address: netip.IPv4Unspecified(), Password: "secret"}})

	srcIP := [4]byte{192, 0, 2, 1}
	dstIP := [4]byte{192, 0, 2, 2}
	tcp := buildSignedTCPHeader(1234, 179, tcpFlagSYN|tcpFlagACK, nil, srcIP[:], dstIP[:], nil, "wrong-or-irrelevant")
	pkt := buildIPv4Packet(srcIP, dstIP, tcp, nil)

	if got := q.decide(pkt); got != nfqueue.NfAccept {
		t.Fatalf("decide() = %d, want NfAccept for a non-SYN segment", got)
	}
}

func TestDynamicMD5QueueDecideWrongPortAccepted(t *testing.T) {
	q := newTestDynamicMD5Queue(t)
	q.SetPeers([]PeerConfig{{Address: netip.IPv4Unspecified(), Password: "secret"}})

	srcIP := [4]byte{192, 0, 2, 1}
	dstIP := [4]byte{192, 0, 2, 2}
	tcp, _ := buildTCPHeader(1234, 8080, tcpFlagSYN, nil) // not the BGP port
	pkt := buildIPv4Packet(srcIP, dstIP, tcp, nil)

	if got := q.decide(pkt); got != nfqueue.NfAccept {
		t.Fatalf("decide() = %d, want NfAccept for a non-BGP-port SYN", got)
	}
}

func TestDynamicMD5QueueDecideFixedPeerBypassesMatch(t *testing.T) {
	q := newTestDynamicMD5Queue(t)
	srcIP := [4]byte{192, 0, 2, 1}
	dstIP := [4]byte{192, 0, 2, 2}
	q.SetPeers([]PeerConfig{{Address: netip.AddrFrom4(srcIP), Password: "fixed-peer-secret"}}) //nolint:gosec // test fixture, not a real credential

	// No MD5 option at all — a fixed peer's traffic is authenticated by the
	// kernel's own per-address key, already installed via applyListenerMD5,
	// so this queue must not judge it.
	tcp, _ := buildTCPHeader(1234, 179, tcpFlagSYN, nil)
	pkt := buildIPv4Packet(srcIP, dstIP, tcp, nil)

	if got := q.decide(pkt); got != nfqueue.NfAccept {
		t.Fatalf("decide() = %d, want NfAccept for a known fixed-peer address", got)
	}
	if len(q.installed) != 0 {
		t.Fatalf("expected no key installed for a fixed-peer bypass, got %v", q.installed)
	}
}

func TestDynamicMD5QueueDecideDynamicPeerMatch(t *testing.T) {
	q := newTestDynamicMD5Queue(t)
	// Loopback source: install() calls the real setsockopt(TCP_MD5SIG) via
	// addListenerMD5Key, which setTCPMD5OnFd no-ops for loopback addresses
	// (see md5.go) — real kernel TCP MD5 support is inconsistent across
	// environments (WSL2, CI runners), so a non-loopback address here would
	// make this test's outcome depend on host kernel support rather than
	// decide()'s own matching logic.
	srcIP := [4]byte{127, 0, 0, 2}
	dstIP := [4]byte{192, 0, 2, 2}
	password := "dyn-secret"
	q.SetPeers([]PeerConfig{{Address: netip.IPv4Unspecified(), Password: password}})

	tcp := buildSignedTCPHeader(1234, 179, tcpFlagSYN, nil, srcIP[:], dstIP[:], nil, password)
	pkt := buildIPv4Packet(srcIP, dstIP, tcp, nil)

	if got := q.decide(pkt); got != nfqueue.NfAccept {
		t.Fatalf("decide() = %d, want NfAccept for a matching signature", got)
	}
	addr := netip.AddrFrom4(srcIP)
	q.mu.Lock()
	k, ok := q.installed[addr]
	q.mu.Unlock()
	if !ok || k.password != password {
		t.Fatalf("expected installed key for %s with password %q, got %+v", addr, password, k)
	}
}

func TestDynamicMD5QueueDecideDynamicPeerNoMatchDropped(t *testing.T) {
	q := newTestDynamicMD5Queue(t)
	srcIP := [4]byte{198, 51, 100, 9}
	dstIP := [4]byte{192, 0, 2, 2}
	q.SetPeers([]PeerConfig{{Address: netip.IPv4Unspecified(), Password: "the-real-password"}})

	tcp := buildSignedTCPHeader(1234, 179, tcpFlagSYN, nil, srcIP[:], dstIP[:], nil, "attacker-guess")
	pkt := buildIPv4Packet(srcIP, dstIP, tcp, nil)

	if got := q.decide(pkt); got != nfqueue.NfDrop {
		t.Fatalf("decide() = %d, want NfDrop for a non-matching signature", got)
	}
}

func TestDynamicMD5QueueDecideNoMD5OptionDropped(t *testing.T) {
	q := newTestDynamicMD5Queue(t)
	srcIP := [4]byte{198, 51, 100, 9}
	dstIP := [4]byte{192, 0, 2, 2}
	q.SetPeers([]PeerConfig{{Address: netip.IPv4Unspecified(), Password: "the-real-password"}})

	tcp, _ := buildTCPHeader(1234, 179, tcpFlagSYN, nil) // no MD5 option
	pkt := buildIPv4Packet(srcIP, dstIP, tcp, nil)

	if got := q.decide(pkt); got != nfqueue.NfDrop {
		t.Fatalf("decide() = %d, want NfDrop for a SYN with no MD5 option at all", got)
	}
}

func TestDynamicMD5QueueSetPeersRevokesRemovedPassword(t *testing.T) {
	q := newTestDynamicMD5Queue(t)
	addr := netip.MustParseAddr("198.51.100.9")
	q.installed[addr] = &installedMD5Key{password: "old-password", lastSeen: time.Now()}

	// New peer list no longer includes "old-password" — SetPeers must
	// revoke the stale key immediately, not wait for idle eviction.
	q.SetPeers([]PeerConfig{{Address: netip.IPv4Unspecified(), Password: "new-password"}})

	q.mu.Lock()
	_, stillInstalled := q.installed[addr]
	q.mu.Unlock()
	if stillInstalled {
		t.Fatal("expected key with removed password to be revoked by SetPeers")
	}
}

func TestDynamicMD5QueueSetPeersKeepsStillValidPassword(t *testing.T) {
	q := newTestDynamicMD5Queue(t)
	addr := netip.MustParseAddr("198.51.100.9")
	q.installed[addr] = &installedMD5Key{password: "still-valid", lastSeen: time.Now()}

	q.SetPeers([]PeerConfig{{Address: netip.IPv4Unspecified(), Password: "still-valid"}})

	q.mu.Lock()
	_, stillInstalled := q.installed[addr]
	q.mu.Unlock()
	if !stillInstalled {
		t.Fatal("expected key with still-configured password to survive SetPeers")
	}
}

func TestDynamicMD5QueueEvictIdle(t *testing.T) {
	q := newTestDynamicMD5Queue(t)
	staleAddr := netip.MustParseAddr("198.51.100.9")
	freshAddr := netip.MustParseAddr("198.51.100.10")
	q.installed[staleAddr] = &installedMD5Key{password: "p1", lastSeen: time.Now().Add(-2 * dynamicMD5KeyIdleTimeout)}
	q.installed[freshAddr] = &installedMD5Key{password: "p2", lastSeen: time.Now()}

	q.evictIdle()

	q.mu.Lock()
	defer q.mu.Unlock()
	if _, ok := q.installed[staleAddr]; ok {
		t.Error("expected idle key to be evicted")
	}
	if _, ok := q.installed[freshAddr]; !ok {
		t.Error("expected fresh key to survive eviction")
	}
}
