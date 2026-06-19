package bgp

import (
	"fmt"
	"net"
	"net/netip"
	"syscall"

	"golang.org/x/sys/unix"
)

// setTCPMD5OnConn sets the TCP MD5 signature on an already-connected socket.
// Used for passive (accepted) connections.
// Skips loopback addresses because many kernels (including WSL2) do not
// support TCP MD5 on loopback.
//
// NOTE: Prefer applyListenerMD5 for passive connections — it sets MD5 on the
// listener socket before the TCP handshake, which is the correct timing per
// RFC 2385. Setting MD5 on an already-accepted socket is too late for the
// kernel to enforce during the handshake.
func setTCPMD5OnConn(conn net.Conn, addr netip.Addr, password string) error {
	if password == "" || addr.IsLoopback() {
		return nil
	}

	sc, ok := conn.(syscall.Conn)
	if !ok {
		return fmt.Errorf("tcp md5: conn is not syscall.Conn")
	}
	rawConn, err := sc.SyscallConn()
	if err != nil {
		return fmt.Errorf("tcp md5: syscall conn: %w", err)
	}

	var setErr error
	ctrlErr := rawConn.Control(func(fd uintptr) {
		setErr = setTCPMD5OnFd(int(fd), addr, password)
	})
	if ctrlErr != nil {
		return fmt.Errorf("tcp md5: control: %w", ctrlErr)
	}
	return setErr
}

// applyListenerMD5 sets TCP MD5 keys on the listener socket for all configured
// peers that have passwords. This must be called before accepting connections
// so the kernel enforces MD5 during the TCP handshake (RFC 2385).
//
// The kernel uses one key per remote address. Two peers at the same IP with
// different passwords is unsupported and will return an error.
func applyListenerMD5(listener net.Listener, peers []PeerConfig) error {
	sc, ok := listener.(syscall.Conn)
	if !ok {
		return fmt.Errorf("tcp md5: listener is not syscall.Conn")
	}
	rawConn, err := sc.SyscallConn()
	if err != nil {
		return fmt.Errorf("tcp md5: listener syscall conn: %w", err)
	}

	// Detect same-IP peers with different passwords and return error.
	// The kernel uses one key per remote address — different passwords
	// at the same IP would leave one peer unable to authenticate.
	addrPasswords := make(map[netip.Addr]string)
		for _, pc := range peers {
			if pc.Password == "" || pc.Address.IsLoopback() || pc.Address.IsUnspecified() {
				continue
			}
		if prev, ok := addrPasswords[pc.Address]; ok && prev != pc.Password {
			return fmt.Errorf("tcp md5: peers at %s have different passwords; kernel uses one key per remote address", pc.Address)
		}
		addrPasswords[pc.Address] = pc.Password
	}

	var firstErr error
	ctrlErr := rawConn.Control(func(fd uintptr) {
		fdInt := int(fd)
		for _, pc := range peers {
			if pc.Password == "" || pc.Address.IsLoopback() {
				continue
			}
			if err := setTCPMD5OnFd(fdInt, pc.Address, pc.Password); err != nil {
				firstErr = fmt.Errorf("tcp md5 set on %s: %w", pc.Address, err)
				return
			}
		}
	})
	if ctrlErr != nil {
		return fmt.Errorf("tcp md5: listener control: %w", ctrlErr)
	}
	return firstErr
}

// clearTCPMD5OnFd removes a TCP MD5 signature for the given address from a
// socket. On Linux, calling SetsockoptTCPMD5Sig with a zero-length key
// deletes the MD5 entry from the kernel's key database.
func clearTCPMD5OnFd(fd int, addr netip.Addr) error {
	if addr.IsLoopback() {
		return nil
	}
	af, err := getsocketFamily(fd)
	if err != nil {
		return fmt.Errorf("tcp md5 clear: getsockname: %w", err)
	}
	sa := buildSockaddrStorage(addr, af)
	sig := unix.TCPMD5Sig{
		Addr:   sa,
		Keylen: 0,
	}
	return unix.SetsockoptTCPMD5Sig(fd, unix.IPPROTO_TCP, unix.TCP_MD5SIG, &sig)
}

// clearListenerMD5 removes TCP MD5 keys from the listener socket for the
// given peers (those with passwords set). Used when peer configs change to
// remove keys for peers that are being removed or whose passwords changed.
func clearListenerMD5(listener net.Listener, peers []PeerConfig) error {
	sc, ok := listener.(syscall.Conn)
	if !ok {
		return nil // not a syscall.Conn, nothing to clear
	}
	rawConn, err := sc.SyscallConn()
	if err != nil {
		return err
	}
	var firstErr error
	ctrlErr := rawConn.Control(func(fd uintptr) {
		fdInt := int(fd)
		for _, pc := range peers {
			if pc.Password == "" || pc.Address.IsLoopback() {
				continue
			}
			if err := clearTCPMD5OnFd(fdInt, pc.Address); err != nil {
				firstErr = err
				return
			}
		}
	})
	if ctrlErr != nil {
		return ctrlErr
	}
	return firstErr
}

// setTCPMD5OnFd sets the TCP MD5 signature option on a raw file descriptor.
// Skips loopback addresses (see setTCPMD5OnConn).
func setTCPMD5OnFd(fd int, addr netip.Addr, password string) error {
	if password == "" || addr.IsLoopback() {
		return nil
	}
	const maxMD5KeyLen = 80
	if len(password) > maxMD5KeyLen {
		return fmt.Errorf("tcp md5: password too long (%d bytes > %d)", len(password), maxMD5KeyLen)
	}
	af, err := getsocketFamily(fd)
	if err != nil {
		return fmt.Errorf("tcp md5: getsockname: %w", err)
	}

	sa := buildSockaddrStorage(addr, af)

	sig := unix.TCPMD5Sig{
		Addr:  sa,
		Keylen: uint16(len(password)),
	}
	copy(sig.Key[:], password)

	return unix.SetsockoptTCPMD5Sig(fd, unix.IPPROTO_TCP, unix.TCP_MD5SIG, &sig)
}

// getsocketFamily returns the address family (AF_INET or AF_INET6) of the socket.
func getsocketFamily(fd int) (int, error) {
	sa, err := unix.Getsockname(fd)
	if err != nil {
		return 0, err
	}
	switch sa.(type) {
	case *unix.SockaddrInet4:
		return unix.AF_INET, nil
	case *unix.SockaddrInet6:
		return unix.AF_INET6, nil
	default:
		return 0, fmt.Errorf("tcp md5: unknown socket family from %T", sa)
	}
}

// buildSockaddrStorage builds a SockaddrStorage for the given address, using
// the specified socket address family (AF_INET or AF_INET6). Dual-stack IPv6
// sockets that accept IPv4 connections need AF_INET6 with an IPv4-mapped IPv6
// address (::ffff:a.b.c.d), not AF_INET.
func buildSockaddrStorage(addr netip.Addr, af int) unix.SockaddrStorage {
	var sa unix.SockaddrStorage

	if addr.Is4() {
		if af == unix.AF_INET6 {
			// IPv4-mapped IPv6 address (::ffff:a.b.c.d) for AF_INET6 sockets
			sa.Family = unix.AF_INET6
			ip4 := addr.As4()
			// IPv4-mapped IPv6 prefix is: 0:0:0:0:0:ffff + IPv4 bytes
			sa.Data[0] = 0 // sin6_port high byte
			sa.Data[1] = 0 // sin6_port low byte
			// sin6_flowinfo = 0 (Data[2:6] already zero)
			// Prefix bytes 0-9 = 0, bytes 10-11 = 0xff 0xff, bytes 12-15 = IPv4
			sa.Data[16] = 0xff // byte 10 of addr
			sa.Data[17] = 0xff // byte 11 of addr
			copy(sa.Data[18:22], ip4[:]) // bytes 12-15 of addr
		} else {
			// Pure IPv4 (AF_INET)
			sa.Family = unix.AF_INET
			ip4 := addr.As4()
			// sockaddr_in layout inside Data:
			// Data[0:2] = sin_port (0)
			// Data[2:6] = sin_addr (4 bytes)
			copy(sa.Data[2:6], ip4[:])
		}
	} else if addr.Is6() {
		sa.Family = unix.AF_INET6
		ip6 := addr.As16()
		// sockaddr_in6 layout inside Data:
		// Data[0:2] = sin6_port (0)
		// Data[2:6] = sin6_flowinfo (0, already zero)
		// Data[6:22] = sin6_addr (16 bytes)
		// Data[22:26] = sin6_scope_id (0)
		copy(sa.Data[6:22], ip6[:])
	}
	// For unsupported address families, leave sa as zero — will fail at setsockopt

	return sa
}
