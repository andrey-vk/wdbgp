package bgp

import (
	"crypto/md5" //nolint:gosec // RFC 2385 mandates MD5; this is not used for general-purpose hashing
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"net/netip"
)

const (
	tcpOptKindEOL  = 0
	tcpOptKindNOP  = 1
	tcpOptKindMD5  = 19
	tcpOptLenMD5   = 18 // kind(1) + len(1) + digest(16)
	tcpHeaderMinSz = 20

	tcpFlagFIN = 0x01
	tcpFlagSYN = 0x02
	tcpFlagRST = 0x04
	tcpFlagACK = 0x10
)

// tcpSegment holds the fields of a captured IPv4/IPv6+TCP segment needed to
// recompute (and match) its RFC 2385 MD5 signature, without needing the
// original packet bytes again.
type tcpSegment struct {
	SrcAddr  netip.Addr
	DstAddr  netip.Addr
	SrcPort  uint16
	DstPort  uint16
	Flags    uint8
	MD5      []byte // 16-byte digest carried in the TCP MD5 option, nil if absent
	pseudo   []byte // pseudo-header, family-specific
	header20 [tcpHeaderMinSz]byte
	payload  []byte
}

func (s *tcpSegment) IsSYN() bool  { return s.Flags&tcpFlagSYN != 0 && s.Flags&tcpFlagACK == 0 }
func (s *tcpSegment) HasMD5() bool { return s.MD5 != nil }

// digest recomputes the RFC 2385 MD5 signature this segment would carry if
// signed with the given password: MD5(pseudo-header || TCP header with
// checksum zeroed and options excluded || segment data || password).
func (s *tcpSegment) digest(password string) [16]byte {
	h := md5.New() //nolint:gosec // RFC 2385 mandates MD5
	h.Write(s.pseudo)
	h.Write(s.header20[:])
	h.Write(s.payload)
	// RFC 2385 mandates MD5 for the TCP signature itself; this reproduces
	// the wire protocol's required digest to compare against, not a
	// password storage/verification hash.
	h.Write([]byte(password)) // codeql[go/weak-sensitive-data-hashing]
	var sum [16]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

// matchesPassword reports whether this segment's carried MD5 option is a
// valid RFC 2385 signature for the given password. Comparison is constant
// time to avoid leaking timing information about password candidates.
func (s *tcpSegment) matchesPassword(password string) bool {
	if len(s.MD5) != 16 {
		return false
	}
	want := s.digest(password)
	return subtle.ConstantTimeCompare(s.MD5, want[:]) == 1
}

var (
	errPacketTooShort   = errors.New("tcp md5: packet too short")
	errUnsupportedIPVer = errors.New("tcp md5: unsupported IP version")
	errNotTCP           = errors.New("tcp md5: not a TCP segment")
)

// parseIPTCPSegment parses a raw IPv4 or IPv6 packet (as delivered by
// NFQUEUE, no link-layer header) into a tcpSegment. Only the base IPv6
// header is understood — packets with IPv6 extension headers before TCP are
// rejected rather than misparsed.
func parseIPTCPSegment(pkt []byte) (*tcpSegment, error) {
	if len(pkt) < 1 {
		return nil, errPacketTooShort
	}
	switch pkt[0] >> 4 {
	case 4:
		return parseIPv4TCPSegment(pkt)
	case 6:
		return parseIPv6TCPSegment(pkt)
	default:
		return nil, errUnsupportedIPVer
	}
}

func parseIPv4TCPSegment(pkt []byte) (*tcpSegment, error) {
	if len(pkt) < 20 {
		return nil, errPacketTooShort
	}
	ihl := int(pkt[0]&0x0f) * 4
	if ihl < 20 || len(pkt) < ihl {
		return nil, errPacketTooShort
	}
	protocol := pkt[9]
	if protocol != 6 {
		return nil, errNotTCP
	}
	totalLen := int(binary.BigEndian.Uint16(pkt[2:4]))
	if totalLen < ihl || len(pkt) < totalLen {
		totalLen = len(pkt) // tolerate truncated captures / NFQUEUE MaxPacketLen
	}
	srcAddr := netip.AddrFrom4([4]byte(pkt[12:16]))
	dstAddr := netip.AddrFrom4([4]byte(pkt[16:20]))

	tcpSegLen := totalLen - ihl
	seg, err := parseTCP(pkt[ihl:totalLen], tcpSegLen)
	if err != nil {
		return nil, err
	}
	seg.SrcAddr = srcAddr
	seg.DstAddr = dstAddr

	// RFC 793 pseudo-header: src(4) + dst(4) + zero(1) + protocol(1) + length(2)
	pseudo := make([]byte, 12)
	copy(pseudo[0:4], pkt[12:16])
	copy(pseudo[4:8], pkt[16:20])
	pseudo[8] = 0
	pseudo[9] = protocol
	binary.BigEndian.PutUint16(pseudo[10:12], uint16(tcpSegLen)) //nolint:gosec // segment length fits uint16 for any real BGP packet
	seg.pseudo = pseudo
	return seg, nil
}

func parseIPv6TCPSegment(pkt []byte) (*tcpSegment, error) {
	const ipv6HeaderSz = 40
	if len(pkt) < ipv6HeaderSz {
		return nil, errPacketTooShort
	}
	nextHeader := pkt[6]
	if nextHeader != 6 {
		return nil, errNotTCP
	}
	payloadLen := int(binary.BigEndian.Uint16(pkt[4:6]))
	end := ipv6HeaderSz + payloadLen
	if payloadLen == 0 || len(pkt) < end {
		end = len(pkt) // tolerate truncated captures / NFQUEUE MaxPacketLen
	}
	srcAddr := netip.AddrFrom16([16]byte(pkt[8:24]))
	dstAddr := netip.AddrFrom16([16]byte(pkt[24:40]))

	tcpSegLen := end - ipv6HeaderSz
	seg, err := parseTCP(pkt[ipv6HeaderSz:end], tcpSegLen)
	if err != nil {
		return nil, err
	}
	seg.SrcAddr = srcAddr
	seg.DstAddr = dstAddr

	// RFC 2460 pseudo-header: src(16) + dst(16) + upper-layer length(4) + zero(3) + next header(1)
	pseudo := make([]byte, 40)
	copy(pseudo[0:16], pkt[8:24])
	copy(pseudo[16:32], pkt[24:40])
	binary.BigEndian.PutUint32(pseudo[32:36], uint32(tcpSegLen)) //nolint:gosec // segment length fits uint32
	pseudo[39] = nextHeader
	seg.pseudo = pseudo
	return seg, nil
}

// parseTCP parses the TCP header and options starting at tcp[0], given the
// total TCP segment length (header+options+data) reported by the IP layer.
func parseTCP(tcp []byte, segLen int) (*tcpSegment, error) {
	if len(tcp) < tcpHeaderMinSz || segLen < tcpHeaderMinSz {
		return nil, errPacketTooShort
	}
	dataOffset := int(tcp[12]>>4) * 4
	if dataOffset < tcpHeaderMinSz || len(tcp) < dataOffset || segLen < dataOffset {
		return nil, errPacketTooShort
	}

	seg := &tcpSegment{
		SrcPort: binary.BigEndian.Uint16(tcp[0:2]),
		DstPort: binary.BigEndian.Uint16(tcp[2:4]),
		Flags:   tcp[13],
	}

	// RFC 2385 §2.1: hash the fixed 20-byte header with checksum zeroed,
	// options excluded.
	copy(seg.header20[:], tcp[:tcpHeaderMinSz])
	seg.header20[16] = 0
	seg.header20[17] = 0

	if segLen > dataOffset {
		seg.payload = append([]byte(nil), tcp[dataOffset:segLen]...)
	}

	seg.MD5 = findMD5Option(tcp[tcpHeaderMinSz:dataOffset])
	return seg, nil
}

// findMD5Option scans TCP options for kind 19 (MD5 signature, RFC 2385) and
// returns its 16-byte digest, or nil if absent or malformed.
func findMD5Option(opts []byte) []byte {
	i := 0
	for i < len(opts) {
		kind := opts[i]
		switch kind {
		case tcpOptKindEOL:
			return nil
		case tcpOptKindNOP:
			i++
			continue
		}
		if i+1 >= len(opts) {
			return nil
		}
		optLen := int(opts[i+1])
		if optLen < 2 || i+optLen > len(opts) {
			return nil
		}
		if kind == tcpOptKindMD5 && optLen == tcpOptLenMD5 {
			digest := make([]byte, 16)
			copy(digest, opts[i+2:i+optLen])
			return digest
		}
		i += optLen
	}
	return nil
}
