package bgp

import (
	"crypto/md5" //nolint:gosec // RFC 2385 mandates MD5
	"encoding/binary"
	"testing"
)

// referenceDigest computes the RFC 2385 digest independently of tcpSegment.digest,
// directly from packet fields, so tests exercise the parser's field extraction
// rather than just round-tripping through the same helper being tested.
func referenceDigest(srcIP, dstIP []byte, protocol byte, tcpSegLen int, header20 [20]byte, payload []byte, password string) [16]byte {
	var pseudo []byte
	switch len(srcIP) {
	case 4:
		pseudo = make([]byte, 12)
		copy(pseudo[0:4], srcIP)
		copy(pseudo[4:8], dstIP)
		pseudo[9] = protocol
		binary.BigEndian.PutUint16(pseudo[10:12], uint16(tcpSegLen)) //nolint:gosec // test data
	case 16:
		pseudo = make([]byte, 40)
		copy(pseudo[0:16], srcIP)
		copy(pseudo[16:32], dstIP)
		binary.BigEndian.PutUint32(pseudo[32:36], uint32(tcpSegLen)) //nolint:gosec // test data
		pseudo[39] = protocol
	}
	h := md5.New() //nolint:gosec // RFC 2385 mandates MD5
	h.Write(pseudo)
	header20[16], header20[17] = 0, 0
	h.Write(header20[:])
	h.Write(payload)
	h.Write([]byte(password))
	var sum [16]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

// buildTCPHeader builds a 20-byte TCP header plus the given options, padded
// to a 4-byte boundary. Returns the full header+options bytes and the
// 20-byte header alone (for reference digest computation).
func buildTCPHeader(srcPort, dstPort uint16, flags uint8, options []byte) ([]byte, [20]byte) {
	for len(options)%4 != 0 {
		options = append(options, tcpOptKindNOP)
	}
	dataOffsetWords := (20 + len(options)) / 4
	hdr := make([]byte, 20+len(options))
	binary.BigEndian.PutUint16(hdr[0:2], srcPort)
	binary.BigEndian.PutUint16(hdr[2:4], dstPort)
	hdr[12] = byte(dataOffsetWords << 4) //nolint:gosec // test data, always small
	hdr[13] = flags
	copy(hdr[20:], options)

	var ref [20]byte
	copy(ref[:], hdr[:20])
	return hdr, ref
}

// buildSignedTCPHeader builds a TCP header carrying an RFC 2385 MD5 option
// signed with password, plus any extra options placed before it (e.g. NOPs).
func buildSignedTCPHeader(srcPort, dstPort uint16, flags uint8, extraOpts []byte, srcIP, dstIP []byte, payload []byte, password string) []byte {
	placeholder := append(append([]byte{}, extraOpts...), md5Option([16]byte{})...)
	hdr, ref := buildTCPHeader(srcPort, dstPort, flags, placeholder)
	segLen := len(hdr) + len(payload)
	digest := referenceDigest(srcIP, dstIP, 6, segLen, ref, payload, password)

	signedOpts := append(append([]byte{}, extraOpts...), md5Option(digest)...)
	hdr, _ = buildTCPHeader(srcPort, dstPort, flags, signedOpts)
	return hdr
}

func buildIPv4Packet(srcIP, dstIP [4]byte, tcpHeaderAndOpts, payload []byte) []byte {
	totalLen := 20 + len(tcpHeaderAndOpts) + len(payload)
	pkt := make([]byte, totalLen)
	pkt[0] = 0x45                                          // version 4, IHL 5
	binary.BigEndian.PutUint16(pkt[2:4], uint16(totalLen)) //nolint:gosec // test data
	pkt[9] = 6                                             // TCP
	copy(pkt[12:16], srcIP[:])
	copy(pkt[16:20], dstIP[:])
	copy(pkt[20:], tcpHeaderAndOpts)
	copy(pkt[20+len(tcpHeaderAndOpts):], payload)
	return pkt
}

func buildIPv6Packet(srcIP, dstIP [16]byte, tcpHeaderAndOpts, payload []byte) []byte {
	payloadLen := len(tcpHeaderAndOpts) + len(payload)
	pkt := make([]byte, 40+payloadLen)
	pkt[0] = 0x60                                            // version 6
	binary.BigEndian.PutUint16(pkt[4:6], uint16(payloadLen)) //nolint:gosec // test data
	pkt[6] = 6                                               // next header: TCP
	copy(pkt[8:24], srcIP[:])
	copy(pkt[24:40], dstIP[:])
	copy(pkt[40:], tcpHeaderAndOpts)
	copy(pkt[40+len(tcpHeaderAndOpts):], payload)
	return pkt
}

func md5Option(digest [16]byte) []byte {
	opt := make([]byte, 18)
	opt[0] = tcpOptKindMD5
	opt[1] = tcpOptLenMD5
	copy(opt[2:], digest[:])
	return opt
}

func TestParseIPv4TCPSegmentMD5Match(t *testing.T) {
	srcIP := [4]byte{198, 51, 100, 7}
	dstIP := [4]byte{203, 0, 113, 1}
	password := "s3cr3t"

	tcpFinal := buildSignedTCPHeader(23456, 179, tcpFlagSYN, nil, srcIP[:], dstIP[:], nil, password)
	pkt := buildIPv4Packet(srcIP, dstIP, tcpFinal, nil)

	seg, err := parseIPTCPSegment(pkt)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !seg.IsSYN() {
		t.Fatal("expected SYN flag set")
	}
	if seg.SrcPort != 23456 || seg.DstPort != 179 {
		t.Fatalf("ports: got %d->%d", seg.SrcPort, seg.DstPort)
	}
	if seg.SrcAddr.As4() != srcIP || seg.DstAddr.As4() != dstIP {
		t.Fatalf("addrs: got %s->%s", seg.SrcAddr, seg.DstAddr)
	}
	if !seg.HasMD5() {
		t.Fatal("expected MD5 option present")
	}
	if !seg.matchesPassword(password) {
		t.Fatal("expected password to match")
	}
	if seg.matchesPassword("wrong-password") {
		t.Fatal("expected wrong password not to match")
	}
}

func TestParseIPv6TCPSegmentMD5Match(t *testing.T) {
	srcIP := [16]byte{0x20, 0x01, 0x0d, 0xb8}
	dstIP := [16]byte{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	password := "another-secret"

	tcpFinal := buildSignedTCPHeader(179, 179, tcpFlagSYN, nil, srcIP[:], dstIP[:], nil, password)
	pkt := buildIPv6Packet(srcIP, dstIP, tcpFinal, nil)

	seg, err := parseIPTCPSegment(pkt)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if seg.SrcAddr.As16() != srcIP || seg.DstAddr.As16() != dstIP {
		t.Fatalf("addrs: got %s->%s", seg.SrcAddr, seg.DstAddr)
	}
	if !seg.matchesPassword(password) {
		t.Fatal("expected password to match")
	}
}

func TestParseTCPSegmentNoMD5Option(t *testing.T) {
	srcIP := [4]byte{10, 0, 0, 1}
	dstIP := [4]byte{10, 0, 0, 2}
	tcp, _ := buildTCPHeader(5000, 179, tcpFlagSYN, nil)
	pkt := buildIPv4Packet(srcIP, dstIP, tcp, nil)

	seg, err := parseIPTCPSegment(pkt)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if seg.HasMD5() {
		t.Fatal("expected no MD5 option")
	}
	if seg.matchesPassword("anything") {
		t.Fatal("expected match to fail with no MD5 option")
	}
}

func TestParseTCPSegmentNOPPaddedOptions(t *testing.T) {
	srcIP := [4]byte{10, 0, 0, 1}
	dstIP := [4]byte{10, 0, 0, 2}
	password := "padded"

	nopPrefix := []byte{tcpOptKindNOP, tcpOptKindNOP}
	tcpFinal := buildSignedTCPHeader(5000, 179, tcpFlagSYN, nopPrefix, srcIP[:], dstIP[:], nil, password)
	pkt := buildIPv4Packet(srcIP, dstIP, tcpFinal, nil)

	seg, err := parseIPTCPSegment(pkt)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !seg.matchesPassword(password) {
		t.Fatal("expected password to match through NOP padding")
	}
}

func TestParseTCPSegmentWithPayload(t *testing.T) {
	srcIP := [4]byte{10, 0, 0, 1}
	dstIP := [4]byte{10, 0, 0, 2}
	password := "payload-secret"
	payload := []byte("hello-bgp")

	tcpFinal := buildSignedTCPHeader(5000, 179, tcpFlagACK, nil, srcIP[:], dstIP[:], payload, password)
	pkt := buildIPv4Packet(srcIP, dstIP, tcpFinal, payload)

	seg, err := parseIPTCPSegment(pkt)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !seg.matchesPassword(password) {
		t.Fatal("expected password to match with non-empty payload")
	}
}

func TestParseIPTCPSegmentRejectsNonTCP(t *testing.T) {
	pkt := make([]byte, 20)
	pkt[0] = 0x45
	binary.BigEndian.PutUint16(pkt[2:4], 20)
	pkt[9] = 17 // UDP
	if _, err := parseIPTCPSegment(pkt); err == nil {
		t.Fatal("expected error for non-TCP protocol")
	}
}

func TestParseIPTCPSegmentTooShort(t *testing.T) {
	if _, err := parseIPTCPSegment([]byte{0x45, 0, 0}); err == nil {
		t.Fatal("expected error for too-short packet")
	}
	if _, err := parseIPTCPSegment(nil); err == nil {
		t.Fatal("expected error for empty packet")
	}
}

func TestParseIPTCPSegmentUnsupportedVersion(t *testing.T) {
	pkt := make([]byte, 20)
	pkt[0] = 0x55 // version 5
	if _, err := parseIPTCPSegment(pkt); err == nil {
		t.Fatal("expected error for unsupported IP version")
	}
}

func TestIsSYNExcludesSYNACK(t *testing.T) {
	seg := &tcpSegment{Flags: tcpFlagSYN | tcpFlagACK}
	if seg.IsSYN() {
		t.Fatal("SYN+ACK should not be classified as an initial SYN")
	}
}
