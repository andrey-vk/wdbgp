package bgp

import (
	"bytes"
	"encoding/binary"
	"net/netip"
	"testing"
)

func TestEncodeDecodeOpen(t *testing.T) {
	open := &OpenMessage{
		Version:  4,
		MyASN32:  64512,
		HoldTime: 90,
		BGPID:    [4]byte{192, 0, 2, 1},
	}

	data := open.Serialize()
	msg, err := ReadMessage(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}

	decoded, ok := msg.(*OpenMessage)
	if !ok {
		t.Fatalf("expected *OpenMessage, got %T", msg)
	}

	if decoded.Version != 4 {
		t.Fatalf("version = %d, want 4", decoded.Version)
	}
	// MyASN is set to actual ASN when it fits in 16 bits (AS_TRANS=23456 only for >65535)
	if decoded.MyASN != 64512 {
		t.Fatalf("my_asn = %d, want 64512", decoded.MyASN)
	}
	if decoded.HoldTime != 90 {
		t.Fatalf("hold_time = %d, want 90", decoded.HoldTime)
	}
	if decoded.BGPID != [4]byte{192, 0, 2, 1} {
		t.Fatalf("bgp_id = %v, want {192, 0, 2, 1}", decoded.BGPID)
	}
	// OptParmLen reflects IPv6 unicast + AS4 capabilities (14 bytes: 2 header + 12 TLV data)
	if decoded.OptParmLen != 14 {
		t.Fatalf("opt_parm_len = %d, want 14", decoded.OptParmLen)
	}
}

func TestEncodeDecodeKeepalive(t *testing.T) {
	ka := &KeepaliveMessage{}
	data := ka.Serialize()

	msg, err := ReadMessage(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := msg.(*KeepaliveMessage); !ok {
		t.Fatalf("expected *KeepaliveMessage, got %T", msg)
	}
}

func TestEncodeDecodeUpdate(t *testing.T) {
	prefix := netip.MustParsePrefix("10.0.0.0/8")
	origin := OriginAttribute(OriginIGP)
	asPath := &ASPathAttribute{ASN: 64512}
	nextHop := &NextHopAttribute{NextHop: netip.MustParseAddr("192.0.2.1")}

	update := &UpdateMessage{
		WithdrawnRoutes: nil,
		PathAttributes:  []PathAttribute{origin, asPath, nextHop},
		NLRI:            []netip.Prefix{prefix},
	}

	data := update.Serialize()
	msg, err := ReadMessage(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}

	decoded, ok := msg.(*UpdateMessage)
	if !ok {
		t.Fatalf("expected *UpdateMessage, got %T", msg)
	}

	if len(decoded.NLRI) != 1 {
		t.Fatalf("nlri len = %d, want 1", len(decoded.NLRI))
	}
	if decoded.NLRI[0] != prefix {
		t.Fatalf("nlri[0] = %s, want %s", decoded.NLRI[0], prefix)
	}
	if len(decoded.PathAttributes) != 3 {
		t.Fatalf("path_attrs len = %d, want 3", len(decoded.PathAttributes))
	}

	// Verify ORIGIN attribute
	originAttr, ok := decoded.PathAttributes[0].(OriginAttribute)
	if !ok {
		t.Fatalf("first attr not OriginAttribute: %T", decoded.PathAttributes[0])
	}
	if originAttr != OriginIGP {
		t.Fatalf("origin = %d, want %d", originAttr, OriginIGP)
	}

	// Verify NEXT_HOP attribute
	nhAttr, ok := decoded.PathAttributes[2].(*NextHopAttribute)
	if !ok {
		t.Fatalf("third attr not *NextHopAttribute: %T", decoded.PathAttributes[2])
	}
	if nhAttr.NextHop.String() != "192.0.2.1" {
		t.Fatalf("next_hop = %s, want 192.0.2.1", nhAttr.NextHop)
	}
}

func TestEncodeDecodeNotification(t *testing.T) {
	notif := &NotificationMessage{
		ErrorCode:    6,
		ErrorSubcode: 5,
		Data:         []byte{0xAA, 0xBB},
	}

	data := notif.Serialize()
	msg, err := ReadMessage(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}

	decoded, ok := msg.(*NotificationMessage)
	if !ok {
		t.Fatalf("expected *NotificationMessage, got %T", msg)
	}

	if decoded.ErrorCode != 6 {
		t.Fatalf("error_code = %d, want 6", decoded.ErrorCode)
	}
	if decoded.ErrorSubcode != 5 {
		t.Fatalf("error_subcode = %d, want 5", decoded.ErrorSubcode)
	}
	if len(decoded.Data) != 2 || decoded.Data[0] != 0xAA || decoded.Data[1] != 0xBB {
		t.Fatalf("data = %v, want [0xAA 0xBB]", decoded.Data)
	}
}

func TestOpenIncludesIPv6UnicastCapability(t *testing.T) {
	open := &OpenMessage{
		Version:  4,
		MyASN:    64512,
		MyASN32:  64512,
		HoldTime: 90,
		BGPID:    [4]byte{192, 0, 2, 1},
	}

	data := open.Serialize()

	// BGP header is 19 bytes, OPEN body is 10 + opt parms
	if len(data) < 19+10 {
		t.Fatalf("serialized OPEN too short: %d bytes", len(data))
	}

	body := data[19:] // skip BGP header

	// OptParmLen at offset 9 of body
	optParmLen := body[9]
	if optParmLen == 0 {
		t.Fatal("OptParmLen is 0, expected IPv6 unicast capability in optional parameters")
	}
	t.Logf("OptParmLen = %d", optParmLen)

	// Optional parameters start at body[10]
	opts := body[10:]

	// Capability parameter format:
	//   Param Type: 2 (Capability)
	//   Param Length: variable
	//   Capability Code: 1 (Multiprotocol Extension)
	//   Capability Length: 4
	//   Capability Value: AFI(2 bytes) + Reserved(1) + SAFI(1)

	if len(opts) < 2 {
		t.Fatal("optional parameters too short")
	}
	paramType := opts[0]
	if paramType != 2 {
		t.Fatalf("param type = %d, want 2 (Capability)", paramType)
	}
	paramLen := int(opts[1])
	if paramLen < 12 {
		t.Fatalf("param length = %d, want at least 12 (IPv6 + AS4 capabilities)", paramLen)
	}

	capData := opts[2 : 2+paramLen]

	// Find IPv6 unicast capability (code 1) and AS4 capability (code 65)
	foundIPv6 := false
	foundAS4 := false
	for len(capData) >= 2 {
		capCode := capData[0]
		capLen := int(capData[1])
		if 2+capLen > len(capData) {
			break
		}
		if capCode == 1 && capLen == 4 {
			// Multiprotocol Extension — verify IPv6 unicast
			afi := binary.BigEndian.Uint16(capData[2:4])
			safi := capData[5]
			if afi == 2 && safi == 1 {
				foundIPv6 = true
				t.Logf("IPv6 unicast capability verified: AFI=%d SAFI=%d", afi, safi)
			}
		}
		if capCode == 65 && capLen == 4 {
			// Four-octet ASN capability — verify value
			as4 := binary.BigEndian.Uint32(capData[2:6])
			if as4 == 64512 {
				foundAS4 = true
				t.Logf("AS4 capability verified: ASN=%d", as4)
			} else {
				t.Errorf("AS4 capability has wrong ASN: %d, want 64512", as4)
			}
		}
		capData = capData[2+capLen:]
	}

	if !foundIPv6 {
		t.Error("IPv6 unicast capability not found in OPEN message")
	}
	if !foundAS4 {
		t.Error("Four-octet ASN capability not found in OPEN message")
	}
}

func TestOpenIncludesAS4Capability(t *testing.T) {
	open := &OpenMessage{
		Version:  4,
		MyASN:    23456, // AS_TRANS — the 2-byte field
		MyASN32:  196608, // 4-octet ASN (3 * 65536)
		HoldTime: 90,
		BGPID:    [4]byte{192, 0, 2, 1},
	}

	data := open.Serialize()

	// BGP header is 19 bytes, OPEN body is 10 + opt parms
	if len(data) < 19+10 {
		t.Fatalf("serialized OPEN too short: %d bytes", len(data))
	}

	body := data[19:] // skip BGP header

	// 2-byte ASN field at offset 1-2 of body should be AS_TRANS (23456)
	myasn2 := binary.BigEndian.Uint16(body[1:3])
	if myasn2 != 23456 {
		t.Fatalf("2-byte ASN field = %d, want 23456 (AS_TRANS)", myasn2)
	}
	t.Logf("2-byte ASN field is AS_TRANS: %d", myasn2)

	// OptParmLen at offset 9 of body
	optParmLen := body[9]
	if optParmLen == 0 {
		t.Fatal("OptParmLen is 0, expected AS4 capability in optional parameters")
	}

	// Optional parameters start at body[10]
	opts := body[10:]

	// Find AS4 capability (code 65)
	foundAS4 := false
	for len(opts) >= 2 {
		paramType := opts[0]
		paramLen := int(opts[1])
		if 2+paramLen > len(opts) {
			break
		}
		if paramType == 2 { // Capability parameter
			capData := opts[2 : 2+paramLen]
			for len(capData) >= 2 {
				capCode := capData[0]
				capLen := int(capData[1])
				if 2+capLen > len(capData) {
					break
				}
				if capCode == 65 && capLen == 4 {
					as4 := binary.BigEndian.Uint32(capData[2:6])
					if as4 == 196608 {
						foundAS4 = true
						t.Logf("AS4 capability with large ASN verified: %d", as4)
					} else {
						t.Errorf("AS4 capability has wrong ASN: %d, want 196608", as4)
					}
				}
				capData = capData[2+capLen:]
			}
		}
		opts = opts[2+paramLen:]
	}

	if !foundAS4 {
		t.Error("Four-octet ASN capability not found for large ASN")
	}

	// Verify round-trip via ReadMessage
	msg, err := ReadMessage(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("ReadMessage failed for AS4 OPEN: %v", err)
	}
	decoded, ok := msg.(*OpenMessage)
	if !ok {
		t.Fatalf("expected *OpenMessage, got %T", msg)
	}
	if decoded.MyASN != 23456 {
		t.Fatalf("decoded 2-byte ASN = %d, want 23456", decoded.MyASN)
	}
	if decoded.MyASN32 != 196608 {
		t.Fatalf("decoded MyASN32 = %d, want 196608", decoded.MyASN32)
	}
	t.Logf("AS4 OPEN round-trip verified: MyASN=%d, MyASN32=%d", decoded.MyASN, decoded.MyASN32)
}

func TestASPath4ByteEncoding(t *testing.T) {
	// Test that ASNs > 65535 produce 4-byte AS_SEQUENCE encoding
	attr := &ASPathAttribute{ASN: 196608}

	serialized := attr.Serialize()

	// Decode with decodePathAttributes
	attrs, err := decodePathAttributes(serialized)
	if err != nil {
		t.Fatal(err)
	}
	if len(attrs) != 1 {
		t.Fatalf("attrs len = %d, want 1", len(attrs))
	}

	// Verify the type code from the raw bytes
	// Path attribute format: flags(1) + type(1) + len(1|2) + value
	// The value is: segment_type(1) + segment_len(1) + ASN bytes
	// For 4-byte AS: segment_type=2, segment_len=1, value=4 bytes → total value=6
	// Verify raw bytes contain type 2 AS_SEQUENCE (4-byte)
	flags := serialized[0]
	t.Logf("path attr flags: 0x%02x", flags)

	// Calculate value offset and size
	var valOffset, valLen int
	if flags&attrFlagExtendedLength != 0 {
		valOffset = 4
		valLen = int(binary.BigEndian.Uint16(serialized[2:4]))
	} else {
		valOffset = 3
		valLen = int(serialized[2])
	}

	value := serialized[valOffset : valOffset+valLen]
	if len(value) != 6 {
		t.Fatalf("value length = %d, want 6 (4-byte AS_SEQUENCE)", len(value))
	}
	if value[0] != 2 {
		t.Fatalf("segment type = %d, want 2 (AS_SEQUENCE 4-byte)", value[0])
	}
	if value[1] != 1 {
		t.Fatalf("segment length = %d, want 1", value[1])
	}
	decodedASN := binary.BigEndian.Uint32(value[2:6])
	if decodedASN != 196608 {
		t.Fatalf("decoded ASN = %d, want 196608", decodedASN)
	}
	t.Logf("4-byte AS_PATH encoding verified for ASN %d", decodedASN)

	// Test that ASNs ≤ 65535 still produce 2-byte encoding
	smallAttr := &ASPathAttribute{ASN: 64512}
	smallSerialized := smallAttr.Serialize()
	smallFlags := smallSerialized[0]
	var smallValOffset, smallValLen int
	if smallFlags&attrFlagExtendedLength != 0 {
		smallValOffset = 4
		smallValLen = int(binary.BigEndian.Uint16(smallSerialized[2:4]))
	} else {
		smallValOffset = 3
		smallValLen = int(smallSerialized[2])
	}
	smallValue := smallSerialized[smallValOffset : smallValOffset+smallValLen]
	if len(smallValue) != 4 {
		t.Fatalf("value length = %d, want 4 (2-byte AS_SEQUENCE)", len(smallValue))
	}
	if smallValue[0] != 1 {
		t.Fatalf("segment type = %d, want 1 (AS_SEQUENCE 2-byte)", smallValue[0])
	}
	t.Logf("2-byte AS_PATH encoding verified for ASN 64512")
}

func TestDecodeASPath2Byte(t *testing.T) {
	attr := &ASPathAttribute{ASN: 64512}
	serialized := attr.Serialize()

	attrs, err := decodePathAttributes(serialized)
	if err != nil {
		t.Fatal(err)
	}
	if len(attrs) != 1 {
		t.Fatalf("attrs len = %d, want 1", len(attrs))
	}

	decoded, ok := attrs[0].(*ASPathAttribute)
	if !ok {
		t.Fatalf("expected *ASPathAttribute, got %T", attrs[0])
	}
	if decoded.ASN != 64512 {
		t.Fatalf("decoded ASN = %d, want 64512", decoded.ASN)
	}
	t.Logf("2-byte AS_PATH round-trip verified: ASN=%d", decoded.ASN)
}

func TestDecodeASPath4Byte(t *testing.T) {
	attr := &ASPathAttribute{ASN: 196608}
	serialized := attr.Serialize()

	attrs, err := decodePathAttributes(serialized)
	if err != nil {
		t.Fatal(err)
	}
	if len(attrs) != 1 {
		t.Fatalf("attrs len = %d, want 1", len(attrs))
	}

	decoded, ok := attrs[0].(*ASPathAttribute)
	if !ok {
		t.Fatalf("expected *ASPathAttribute, got %T", attrs[0])
	}
	if decoded.ASN != 196608 {
		t.Fatalf("decoded ASN = %d, want 196608", decoded.ASN)
	}
	t.Logf("4-byte AS_PATH round-trip verified: ASN=%d", decoded.ASN)
}

func TestLargeCommunitiesRoundTrip(t *testing.T) {
	comms := []LargeCommunity{
		{GlobalAdmin: 64512, LocalData1: 7, LocalData2: 0},
		{GlobalAdmin: 64512, LocalData1: 0, LocalData2: 10000},
		{GlobalAdmin: 64512, LocalData1: 0, LocalData2: 10001},
	}
	attr := &LargeCommunitiesAttribute{Communities: comms}

	// Test Serialize/Deserialize via path attribute encoding
	serialized := attr.Serialize()

	// Decode with decodePathAttributes
	attrs, err := decodePathAttributes(serialized)
	if err != nil {
		t.Fatal(err)
	}
	if len(attrs) != 1 {
		t.Fatalf("attrs len = %d, want 1", len(attrs))
	}

	decoded, ok := attrs[0].(*LargeCommunitiesAttribute)
	if !ok {
		t.Fatalf("expected *LargeCommunitiesAttribute, got %T", attrs[0])
	}

	if len(decoded.Communities) != 3 {
		t.Fatalf("communities len = %d, want 3", len(decoded.Communities))
	}
	for i, c := range comms {
		if decoded.Communities[i] != c {
			t.Fatalf("community[%d] = %+v, want %+v", i, decoded.Communities[i], c)
		}
	}

	// Empty communities round-trip
	emptyAttr := &LargeCommunitiesAttribute{}
	emptySerialized := emptyAttr.Serialize()
	emptyAttrs, err := decodePathAttributes(emptySerialized)
	if err != nil {
		t.Fatal(err)
	}
	if len(emptyAttrs) != 1 {
		t.Fatalf("empty attrs len = %d, want 1", len(emptyAttrs))
	}
	emptyDecoded, ok := emptyAttrs[0].(*LargeCommunitiesAttribute)
	if !ok {
		t.Fatalf("expected *LargeCommunitiesAttribute, got %T", emptyAttrs[0])
	}
	if len(emptyDecoded.Communities) != 0 {
		t.Fatalf("empty communities len = %d, want 0", len(emptyDecoded.Communities))
	}
}

func TestPrefixEncodeDecode(t *testing.T) {
	// IPv4 prefix
	prefixes := []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("192.168.1.0/24"),
	}

	encoded := encodePrefixes(prefixes)
	decoded, err := decodePrefixes(encoded, 1)
	if err != nil {
		t.Fatal(err)
	}

	if len(decoded) != len(prefixes) {
		t.Fatalf("len = %d, want %d", len(decoded), len(prefixes))
	}
	for i, p := range prefixes {
		if decoded[i] != p {
			t.Fatalf("prefix[%d] = %s, want %s", i, decoded[i], p)
		}
	}

	// IPv6 prefixes — encodePrefixes works for both address families,
	// but decodePrefixes is classic BGP-4 (IPv4-only) and treats short
	// prefixes as IPv4. Test encoding format directly for IPv6.
	ipv6Prefixes := []netip.Prefix{
		netip.MustParsePrefix("fd00::1/128"),
	}

	ipv6Encoded := encodePrefixes(ipv6Prefixes)
	// 17 bytes: 1 (prefix length 128) + 16 (full IPv6 address) = 17
	if len(ipv6Encoded) != 17 {
		t.Fatalf("ipv6 encoded len = %d, want 17", len(ipv6Encoded))
	}
	if ipv6Encoded[0] != 128 {
		t.Fatalf("ipv6 prefix len byte = %d, want 128", ipv6Encoded[0])
	}

	// /32 (max IPv4) round-trip
	maxPrefixes := []netip.Prefix{
		netip.MustParsePrefix("1.2.3.4/32"),
	}

	maxEncoded := encodePrefixes(maxPrefixes)
	maxDecoded, err := decodePrefixes(maxEncoded, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(maxDecoded) != 1 {
		t.Fatalf("max len = %d, want 1", len(maxDecoded))
	}
	if maxDecoded[0] != maxPrefixes[0] {
		t.Fatalf("max = %s, want %s", maxDecoded[0], maxPrefixes[0])
	}

	// Empty prefix list
	emptyEncoded := encodePrefixes(nil)
	emptyDecoded, err := decodePrefixes(emptyEncoded, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(emptyDecoded) != 0 {
		t.Fatalf("empty len = %d, want 0", len(emptyDecoded))
	}

	// Multiple prefixes (mix of lengths)
	mixedPrefixes := []netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/0"),
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("172.16.0.0/16"),
		netip.MustParsePrefix("192.168.1.1/32"),
	}

	mixedEncoded := encodePrefixes(mixedPrefixes)
	mixedDecoded, err := decodePrefixes(mixedEncoded, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(mixedDecoded) != 4 {
		t.Fatalf("mixed len = %d, want 4", len(mixedDecoded))
	}
	for i, p := range mixedPrefixes {
		if mixedDecoded[i] != p {
			t.Fatalf("mixed[%d] = %s, want %s", i, mixedDecoded[i], p)
		}
	}
}

func TestDecodePathAttributeSkipsUnknown(t *testing.T) {
	// Construct an UPDATE with an unknown path attribute (type 99, empty value)
	// Path attribute TLV: flags=0xC0 (Optional|Transitive), type=99, length=0
	pathAttr := []byte{0xC0, 99, 0}

	// UPDATE body: 2-byte withdrawn len (0) + 2-byte path attr len + attrs + NLRI (empty)
	body := []byte{
		0, 0, // withdrawn routes length = 0
		byte(len(pathAttr) >> 8), byte(len(pathAttr)), // path attr length = 3
	}
	body = append(body, pathAttr...)

	// BGP header: marker (16 bytes of 0xFF) + length + type
	totalLen := HeaderLen + len(body)
	header := make([]byte, HeaderLen)
	for i := 0; i < 16; i++ {
		header[i] = 0xFF
	}
	header[16] = byte(totalLen >> 8)
	header[17] = byte(totalLen)
	header[18] = MsgUpdate

	data := append(header, body...)

	msg, err := ReadMessage(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("ReadMessage with unknown path attribute should NOT error, got: %v", err)
	}

	update, ok := msg.(*UpdateMessage)
	if !ok {
		t.Fatalf("expected *UpdateMessage, got %T", msg)
	}

	// Unknown attribute should be skipped, so PathAttributes should be empty
	if len(update.PathAttributes) != 0 {
		t.Fatalf("PathAttributes len = %d, want 0 (unknown attrs skipped)", len(update.PathAttributes))
	}

	t.Log("Unknown path attribute (type 99) successfully skipped")
}

func TestReadMessageErrors(t *testing.T) {
	// Invalid marker (all zeros instead of 0xFF)
	invalidMarker := make([]byte, HeaderLen)
	// marker is already zeros, set length and type
	invalidMarker[16] = 0
	invalidMarker[17] = byte(HeaderLen)
	invalidMarker[18] = MsgKeepalive

	_, err := ReadMessage(bytes.NewReader(invalidMarker))
	if err == nil {
		t.Fatal("expected error for invalid marker, got nil")
	}
	t.Logf("invalid marker error: %v", err)

	// Short header (fewer than 19 bytes)
	_, err = ReadMessage(bytes.NewReader([]byte{1, 2, 3}))
	if err == nil {
		t.Fatal("expected error for short header, got nil")
	}
	t.Logf("short header error: %v", err)

	// Unknown message type
	unknownHeader := make([]byte, HeaderLen)
	for i := 0; i < 16; i++ {
		unknownHeader[i] = 0xFF
	}
	unknownHeader[16] = 0
	unknownHeader[17] = byte(HeaderLen)
	unknownHeader[18] = 99 // invalid type

	_, err = ReadMessage(bytes.NewReader(unknownHeader))
	if err == nil {
		t.Fatal("expected error for unknown message type, got nil")
	}
	t.Logf("unknown type error: %v", err)

	// Truncated body (header says length > header size, but no body follows)
	truncHeader := make([]byte, HeaderLen)
	for i := 0; i < 16; i++ {
		truncHeader[i] = 0xFF
	}
	// Length = header + 10 bytes of body, but we only provide header
	truncHeader[16] = 0
	truncHeader[17] = byte(HeaderLen + 10)
	truncHeader[18] = MsgUpdate

	_, err = ReadMessage(bytes.NewReader(truncHeader))
	if err == nil {
		t.Fatal("expected error for truncated body, got nil")
	}
	t.Logf("truncated body error: %v", err)

	// Header length too small (< 19)
	smallLenHeader := make([]byte, HeaderLen)
	for i := 0; i < 16; i++ {
		smallLenHeader[i] = 0xFF
	}
	smallLenHeader[16] = 0
	smallLenHeader[17] = 10 // length 10 < HeaderLen (19)
	smallLenHeader[18] = MsgKeepalive

	_, err = ReadMessage(bytes.NewReader(smallLenHeader))
	if err == nil {
		t.Fatal("expected error for header length < 19, got nil")
	}
	t.Logf("small length error: %v", err)

	// Notification with too short body
	notifHeader := make([]byte, HeaderLen)
	for i := 0; i < 16; i++ {
		notifHeader[i] = 0xFF
	}
	notifHeader[16] = 0
	notifHeader[17] = byte(HeaderLen + 1) // only 1 body byte, need at least 2
	notifHeader[18] = MsgNotification
	// Append 1-byte body
	notifWithBody := append(notifHeader, 0x00)

	_, err = ReadMessage(bytes.NewReader(notifWithBody))
	if err == nil {
		t.Fatal("expected error for notification with short body, got nil")
	}
	t.Logf("notification short body error: %v", err)
}

func TestIPv6UpdateWithMpReachNLRI(t *testing.T) {
	// Build an UPDATE with IPv6 NLRI inside MP_REACH (RFC 4760).
	// Legacy UPDATE.NLRI must be empty for IPv6.
	ipv6Prefix := netip.MustParsePrefix("fd00::1/128")
	ipv6NextHop := netip.MustParseAddr("2001:db8::1")

	mpReach := &MpReachNLRIAttribute{
		NextHop: ipv6NextHop,
		NLRI:    []netip.Prefix{ipv6Prefix},
	}

	origin := OriginAttribute(OriginIGP)
	asPath := &ASPathAttribute{ASN: 64512}

	update := &UpdateMessage{
		PathAttributes: []PathAttribute{origin, asPath, mpReach},
		// NLRI is empty — IPv6 prefixes go inside MP_REACH per RFC 4760.
	}

	data := update.Serialize()
	msg, err := ReadMessage(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("ReadMessage failed: %v", err)
	}

	decoded, ok := msg.(*UpdateMessage)
	if !ok {
		t.Fatalf("expected *UpdateMessage, got %T", msg)
	}

	// UPDATE.NLRI must be empty for IPv6 routes.
	if len(decoded.NLRI) != 0 {
		t.Fatalf("legacy NLRI len = %d, want 0 (IPv6 NLRI goes inside MP_REACH)", len(decoded.NLRI))
	}

	// Find the MP_REACH attribute and check its NLRI.
	var foundMpReach *MpReachNLRIAttribute
	for _, attr := range decoded.PathAttributes {
		if mp, ok := attr.(*MpReachNLRIAttribute); ok {
			foundMpReach = mp
			break
		}
	}
	if foundMpReach == nil {
		t.Fatal("MP_REACH attribute not found in decoded update")
	}

	if foundMpReach.NextHop != ipv6NextHop {
		t.Fatalf("next_hop = %s, want %s", foundMpReach.NextHop, ipv6NextHop)
	}
	if len(foundMpReach.NLRI) != 1 {
		t.Fatalf("mp_reach NLRI len = %d, want 1", len(foundMpReach.NLRI))
	}
	if foundMpReach.NLRI[0] != ipv6Prefix {
		t.Fatalf("mp_reach NLRI[0] = %s, want %s", foundMpReach.NLRI[0], ipv6Prefix)
	}

	t.Logf("IPv6 MP_REACH round-trip verified: next_hop=%s NLRI=%v", foundMpReach.NextHop, foundMpReach.NLRI)
}

// decodeNLRIFromAttr extracts NLRI from path attributes, collecting
// both legacy UPDATE.NLRI and NLRI from MP_REACH attributes (RFC 4760).
func decodeNLRIFromAttr(update *UpdateMessage) []netip.Prefix {
	nlri := append([]netip.Prefix(nil), update.NLRI...)
	for _, attr := range update.PathAttributes {
		if mp, ok := attr.(*MpReachNLRIAttribute); ok {
			nlri = append(nlri, mp.NLRI...)
		}
	}
	return nlri
}

func TestDecodeNLRIFromAttr(t *testing.T) {
	ipv4 := netip.MustParsePrefix("10.0.0.0/8")
	ipv6 := netip.MustParsePrefix("fd00::/64")

	update := &UpdateMessage{
		NLRI: []netip.Prefix{ipv4},
		PathAttributes: []PathAttribute{
			&MpReachNLRIAttribute{
				NextHop: netip.MustParseAddr("2001:db8::1"),
				NLRI:    []netip.Prefix{ipv6},
			},
		},
	}

	nlri := decodeNLRIFromAttr(update)
	if len(nlri) != 2 {
		t.Fatalf("nlri len = %d, want 2", len(nlri))
	}
	if nlri[0] != ipv4 {
		t.Fatalf("nlri[0] = %s, want %s", nlri[0], ipv4)
	}
	if nlri[1] != ipv6 {
		t.Fatalf("nlri[1] = %s, want %s", nlri[1], ipv6)
	}
	t.Logf("decodeNLRIFromAttr: %v", nlri)
}
