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
	// MyASN is set to AS_TRANS (23456) by Serialize when 4-octet ASN capability is used
	if decoded.MyASN != 23456 {
		t.Fatalf("my_asn = %d, want 23456 (AS_TRANS)", decoded.MyASN)
	}
	if decoded.HoldTime != 90 {
		t.Fatalf("hold_time = %d, want 90", decoded.HoldTime)
	}
	if decoded.BGPID != [4]byte{192, 0, 2, 1} {
		t.Fatalf("bgp_id = %v, want {192, 0, 2, 1}", decoded.BGPID)
	}
	// OptParmLen now reflects capability parameter length
	if decoded.OptParmLen != 8 {
		t.Fatalf("opt_parm_len = %d, want 8", decoded.OptParmLen)
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

func TestOpenIncludesFourOctetASNCapability(t *testing.T) {
	open := &OpenMessage{
		Version:  4,
		MyASN:    23456, // AS_TRANS when using 4-octet ASN
		MyASN32:  64600, // 4-byte ASN for capability
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
		t.Fatal("OptParmLen is 0, expected four-octet ASN capability in optional parameters")
	}
	t.Logf("OptParmLen = %d", optParmLen)

	// Optional parameters start at body[10]
	opts := body[10:]

	// Capability parameter format:
	//   Param Type: 2 (Capability)
	//   Param Length: variable
	//   Capability Code: 65 (Four-octet ASN)
	//   Capability Length: 4
	//   Capability Value: ASN as 4 bytes

	if len(opts) < 2 {
		t.Fatal("optional parameters too short")
	}
	paramType := opts[0]
	if paramType != 2 {
		t.Fatalf("param type = %d, want 2 (Capability)", paramType)
	}
	paramLen := int(opts[1])
	if paramLen < 6 {
		t.Fatalf("param length = %d, want at least 6 (code + len + 4 bytes)", paramLen)
	}

	capData := opts[2 : 2+paramLen]
	capCode := capData[0]
	capLen := capData[1]

	if capCode != 65 {
		t.Fatalf("capability code = %d, want 65 (Four-octet ASN)", capCode)
	}
	if capLen != 4 {
		t.Fatalf("capability length = %d, want 4", capLen)
	}

	asn := binary.BigEndian.Uint32(capData[2:6])
	if asn != 64600 {
		t.Fatalf("four-octet ASN = %d, want 64600", asn)
	}

	t.Logf("Four-octet ASN capability verified: ASN=%d", asn)
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
	decoded, err := decodePrefixes(encoded)
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
	maxDecoded, err := decodePrefixes(maxEncoded)
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
	emptyDecoded, err := decodePrefixes(emptyEncoded)
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
	mixedDecoded, err := decodePrefixes(mixedEncoded)
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
