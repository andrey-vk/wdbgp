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

// setTCPMD5OnFd sets the TCP MD5 signature option on a raw file descriptor.
// Skips loopback addresses (see setTCPMD5OnConn).
func setTCPMD5OnFd(fd int, addr netip.Addr, password string) error {
	if password == "" || addr.IsLoopback() {
		return nil
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
