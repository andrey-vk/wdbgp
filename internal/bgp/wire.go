package bgp

import (
	"encoding/binary"
	"fmt"
	"io"
	"net/netip"
)

// BGP message types
const (
	MsgOpen         = 1
	MsgUpdate       = 2
	MsgNotification = 3
	MsgKeepalive    = 4
)

// Path attribute type codes
const (
	AttrOrigin           = 1
	AttrASPath           = 2
	AttrNextHop          = 3
	AttrMpReachNLRI      = 14
	AttrLargeCommunities = 32
)

// Origin values
const (
	OriginIGP        = 0
	OriginEGP        = 1
	OriginIncomplete = 2
)

// Path attribute flags
const (
	attrFlagOptional       = 0x80
	attrFlagTransitive     = 0x40
	attrFlagPartial        = 0x20
	attrFlagExtendedLength = 0x10
)

// HeaderLen is the size of a BGP message header (marker + length + type).
const HeaderLen = 19

// BGP message header (19 bytes)
type Header struct {
	Marker [16]byte
	Length uint16
	Type   uint8
}

// OPEN message (RFC 4271 section 4.2). Password authentication is done
// at the TCP level via TCP MD5 (RFC 2385), not in the OPEN message.
type OpenMessage struct {
	Version    uint8
	MyASN      uint16 // 2-byte ASN field (set to AS_TRANS=23456 when using 4-octet ASN capability)
	MyASN32    uint32 // 4-byte local ASN for encoding in Four-octet ASN capability
	HoldTime   uint16
	BGPID      [4]byte
	OptParmLen uint8
}

// UPDATE message
type UpdateMessage struct {
	WithdrawnRoutes []netip.Prefix
	PathAttributes  []PathAttribute
	NLRI            []netip.Prefix
}

// KEEPALIVE message (no body, just header)
type KeepaliveMessage struct{}

// PathAttribute interface
type PathAttribute interface {
	TypeCode() uint8
	Serialize() []byte
}

// ORIGIN attribute
type OriginAttribute uint8

// NEXT_HOP attribute
type NextHopAttribute struct {
	NextHop netip.Addr
}

// MpReachNLRIAttribute is the MP_REACH_NLRI path attribute (type 14, RFC 4760).
type MpReachNLRIAttribute struct {
	NextHop netip.Addr
}

// ASPathAttribute is the AS_PATH path attribute (type 2, well-known mandatory).
type ASPathAttribute struct {
	ASN uint32 // local ASN to include
}

// LargeCommunity
type LargeCommunity struct {
	GlobalAdmin uint32
	LocalData1  uint32
	LocalData2  uint32
}

// LargeCommunities attribute
type LargeCommunitiesAttribute struct {
	Communities []LargeCommunity
}

// NOTIFICATION message
type NotificationMessage struct {
	ErrorCode    uint8
	ErrorSubcode uint8
	Data         []byte
}

// encodeHeader serializes a BGP message header to wire format.
func encodeHeader(h *Header) []byte {
	buf := make([]byte, HeaderLen)
	copy(buf[0:16], h.Marker[:])
	binary.BigEndian.PutUint16(buf[16:18], h.Length)
	buf[18] = h.Type
	return buf
}

// decodeHeader parses a BGP message header from wire format.
func decodeHeader(data []byte) (*Header, error) {
	if len(data) < HeaderLen {
		return nil, fmt.Errorf("bgp: header too short: %d bytes, need %d", len(data), HeaderLen)
	}
	h := &Header{}
	copy(h.Marker[:], data[0:16])
	h.Length = binary.BigEndian.Uint16(data[16:18])
	h.Type = data[18]

	// Validate marker (must be all 0xFF)
	for i, b := range h.Marker {
		if b != 0xFF {
			return nil, fmt.Errorf("bgp: invalid marker byte at position %d: 0x%02x", i, b)
		}
	}

	if h.Length < HeaderLen {
		return nil, fmt.Errorf("bgp: header length too small: %d", h.Length)
	}

	return h, nil
}

// ReadMessage reads and decodes a single BGP message from a reader.
func ReadMessage(r io.Reader) (interface{}, error) {
	// Read header
	headerBuf := make([]byte, HeaderLen)
	if _, err := io.ReadFull(r, headerBuf); err != nil {
		return nil, fmt.Errorf("bgp: read header: %w", err)
	}

	h, err := decodeHeader(headerBuf)
	if err != nil {
		return nil, err
	}

	// Read body
	bodyLen := int(h.Length) - HeaderLen
	body := make([]byte, bodyLen)
	if bodyLen > 0 {
		if _, err := io.ReadFull(r, body); err != nil {
			return nil, fmt.Errorf("bgp: read body: %w", err)
		}
	}

	switch h.Type {
	case MsgOpen:
		return decodeOpen(body)
	case MsgUpdate:
		return decodeUpdate(body)
	case MsgKeepalive:
		if bodyLen != 0 {
			return nil, fmt.Errorf("bgp: keepalive has non-zero body length: %d", bodyLen)
		}
		return &KeepaliveMessage{}, nil
	case MsgNotification:
		return decodeNotification(body)
	default:
		return nil, fmt.Errorf("bgp: unknown message type: %d", h.Type)
	}
}

// decodeOpen parses an OPEN message body.
func decodeOpen(data []byte) (*OpenMessage, error) {
	if len(data) < 10 {
		return nil, fmt.Errorf("bgp: open body too short: %d bytes, need at least 10", len(data))
	}
	o := &OpenMessage{
		Version:    data[0],
		MyASN:      binary.BigEndian.Uint16(data[1:3]),
		HoldTime:   binary.BigEndian.Uint16(data[3:5]),
		OptParmLen: data[9],
	}
	copy(o.BGPID[:], data[5:9])

	// Parse optional parameters for Four-octet ASN Capability (RFC 6793).
	if o.OptParmLen > 0 && len(data) >= 10+int(o.OptParmLen) {
		opts := data[10 : 10+o.OptParmLen]
		for len(opts) >= 2 {
			paramType := opts[0]
			paramLen := int(opts[1])
			if 2+paramLen > len(opts) {
				break // malformed, stop parsing
			}
			paramData := opts[2 : 2+paramLen]
			// Capability parameter (type 2)
			if paramType == 2 && len(paramData) >= 2 {
				capCode := paramData[0]
				capLen := int(paramData[1])
				if capCode == 65 && capLen == 4 && len(paramData) >= 2+capLen {
					// Four-octet ASN Capability
					o.MyASN32 = binary.BigEndian.Uint32(paramData[2 : 2+capLen])
				}
			}
			opts = opts[2+paramLen:]
		}
	}

	return o, nil
}

// decodeUpdate parses an UPDATE message body.
func decodeUpdate(data []byte) (*UpdateMessage, error) {
	u := &UpdateMessage{}

	if len(data) < 2 {
		return nil, fmt.Errorf("bgp: update body too short for withdrawn length")
	}

	withdrawnLen := binary.BigEndian.Uint16(data[0:2])
	data = data[2:]

	if int(withdrawnLen) > len(data) {
		return nil, fmt.Errorf("bgp: update withdrawn length %d exceeds body", withdrawnLen)
	}

	var err error
	u.WithdrawnRoutes, err = decodePrefixes(data[:withdrawnLen])
	if err != nil {
		return nil, fmt.Errorf("bgp: decode withdrawn routes: %w", err)
	}
	data = data[withdrawnLen:]

	if len(data) < 2 {
		return nil, fmt.Errorf("bgp: update body too short for path attr length")
	}

	pathAttrLen := binary.BigEndian.Uint16(data[0:2])
	data = data[2:]

	if int(pathAttrLen) > len(data) {
		return nil, fmt.Errorf("bgp: update path attr length %d exceeds body", pathAttrLen)
	}

	u.PathAttributes, err = decodePathAttributes(data[:pathAttrLen])
	if err != nil {
		return nil, fmt.Errorf("bgp: decode path attributes: %w", err)
	}
	data = data[pathAttrLen:]

	u.NLRI, err = decodePrefixes(data)
	if err != nil {
		return nil, fmt.Errorf("bgp: decode nlri: %w", err)
	}

	return u, nil
}

// decodeNotification parses a NOTIFICATION message body.
func decodeNotification(data []byte) (*NotificationMessage, error) {
	if len(data) < 2 {
		return nil, fmt.Errorf("bgp: notification body too short: %d bytes, need at least 2", len(data))
	}
	return &NotificationMessage{
		ErrorCode:    data[0],
		ErrorSubcode: data[1],
		Data:         data[2:],
	}, nil
}

// Serialize encodes the OPEN message to wire format including header.
// Includes the Four-octet ASN Capability (RFC 6793) to match the 4-byte
// AS_PATH encoding used in UPDATE messages.
func (o *OpenMessage) Serialize() []byte {
	// Build capability parameter for Four-octet ASN (RFC 6793)
	// Parameter type: 2 (Capability)
	// Parameter length: 6
	//   Capability code: 65 (Four-octet ASN Capability)
	//   Capability length: 4
	//   Capability value: local ASN as 4 bytes
	capParam := make([]byte, 8)
	capParam[0] = 2    // Parameter type: Capability
	capParam[1] = 6    // Parameter length: 2 + 4
	capParam[2] = 65   // Capability code: Four-octet ASN
	capParam[3] = 4    // Capability length: 4
	binary.BigEndian.PutUint32(capParam[4:8], o.MyASN32)

	// 2-byte ASN field set to AS_TRANS (23456) per RFC 6793
	// when four-octet ASN capability is used.
	o.OptParmLen = uint8(len(capParam))
	o.MyASN = 23456

	body := make([]byte, 10+o.OptParmLen)
	body[0] = o.Version
	binary.BigEndian.PutUint16(body[1:3], o.MyASN)
	binary.BigEndian.PutUint16(body[3:5], o.HoldTime)
	copy(body[5:9], o.BGPID[:])
	body[9] = o.OptParmLen
	copy(body[10:], capParam)

	return wrapMessage(MsgOpen, body)
}

// Serialize encodes the UPDATE message to wire format including header.
func (u *UpdateMessage) Serialize() []byte {
	withdrawnBytes := encodePrefixes(u.WithdrawnRoutes)
	pathAttrBytes := encodePathAttributes(u.PathAttributes)
	nlriBytes := encodePrefixes(u.NLRI)

	bodyLen := 2 + len(withdrawnBytes) + 2 + len(pathAttrBytes) + len(nlriBytes)
	body := make([]byte, bodyLen)

	offset := 0
	binary.BigEndian.PutUint16(body[offset:offset+2], uint16(len(withdrawnBytes)))
	offset += 2
	copy(body[offset:], withdrawnBytes)
	offset += len(withdrawnBytes)

	binary.BigEndian.PutUint16(body[offset:offset+2], uint16(len(pathAttrBytes)))
	offset += 2
	copy(body[offset:], pathAttrBytes)
	offset += len(pathAttrBytes)

	copy(body[offset:], nlriBytes)

	return wrapMessage(MsgUpdate, body)
}

// Serialize encodes the KEEPALIVE message to wire format (just header).
func (k *KeepaliveMessage) Serialize() []byte {
	return wrapMessage(MsgKeepalive, nil)
}

// Serialize encodes the NOTIFICATION message to wire format including header.
func (n *NotificationMessage) Serialize() []byte {
	body := make([]byte, 2+len(n.Data))
	body[0] = n.ErrorCode
	body[1] = n.ErrorSubcode
	if n.Data != nil {
		copy(body[2:], n.Data)
	}
	return wrapMessage(MsgNotification, body)
}

// TypeCode returns the path attribute type code for ORIGIN (1).
func (o OriginAttribute) TypeCode() uint8 {
	return AttrOrigin
}

// Serialize encodes the ORIGIN path attribute value.
func (o OriginAttribute) Serialize() []byte {
	return encodePathAttribute(attrFlagTransitive, AttrOrigin, []byte{byte(o)})
}

// TypeCode returns the path attribute type code for NEXT_HOP (3).
func (n *NextHopAttribute) TypeCode() uint8 {
	return AttrNextHop
}

// Serialize encodes the NEXT_HOP path attribute value (4 bytes, IPv4 only).
func (n *NextHopAttribute) Serialize() []byte {
	ip := n.NextHop.As4()
	return encodePathAttribute(attrFlagTransitive, AttrNextHop, ip[:])
}

// TypeCode returns the path attribute type code for MP_REACH_NLRI (14).
func (a *MpReachNLRIAttribute) TypeCode() uint8 {
	return AttrMpReachNLRI
}

// Serialize encodes the MP_REACH_NLRI path attribute value (RFC 4760).
func (a *MpReachNLRIAttribute) Serialize() []byte {
	// Two-byte AFI (2=IPv6) + 1-byte SAFI (1=unicast) + 1-byte NH len + NH + 1-byte SNP
	afi := uint16(2) // IPv6
	safi := uint8(1)
	nh := a.NextHop.AsSlice()
	data := []byte{byte(afi >> 8), byte(afi), safi, byte(len(nh))}
	data = append(data, nh...)
	data = append(data, 0) // SNP = 0 (no subsequent address family info)
	return encodePathAttribute(attrFlagOptional|attrFlagTransitive, AttrMpReachNLRI, data)
}

// TypeCode returns the path attribute type code for AS_PATH (2).
func (a *ASPathAttribute) TypeCode() uint8 {
	return AttrASPath
}

// Serialize encodes the AS_PATH path attribute value.
// Uses 4-octet ASN format (AS_SEQUENCE with AS_TRANS = false).
func (a *ASPathAttribute) Serialize() []byte {
	// AS_PATH segment: type=2 (AS_SEQUENCE), length=1, ASN as 4 bytes
	val := make([]byte, 6)
	val[0] = 2 // AS_SEQUENCE
	val[1] = 1 // one AS in segment
	binary.BigEndian.PutUint32(val[2:6], a.ASN)
	return encodePathAttribute(attrFlagTransitive, AttrASPath, val)
}

// TypeCode returns the path attribute type code for Large Communities (32).
func (l *LargeCommunitiesAttribute) TypeCode() uint8 {
	return AttrLargeCommunities
}

// Serialize encodes the Large Communities path attribute value (RFC 8092).
func (l *LargeCommunitiesAttribute) Serialize() []byte {
	val := make([]byte, 12*len(l.Communities))
	for i, c := range l.Communities {
		off := i * 12
		binary.BigEndian.PutUint32(val[off:off+4], c.GlobalAdmin)
		binary.BigEndian.PutUint32(val[off+4:off+8], c.LocalData1)
		binary.BigEndian.PutUint32(val[off+8:off+12], c.LocalData2)
	}
	flags := uint8(attrFlagOptional | attrFlagTransitive)
	return encodePathAttribute(flags, AttrLargeCommunities, val)
}

// encodePathAttribute encodes a single path attribute in TLV format.
func encodePathAttribute(flags, typeCode uint8, value []byte) []byte {
	// Determine if extended length is needed
	if len(value) > 255 {
		flags |= attrFlagExtendedLength
		buf := make([]byte, 4+len(value))
		buf[0] = flags
		buf[1] = typeCode
		binary.BigEndian.PutUint16(buf[2:4], uint16(len(value)))
		copy(buf[4:], value)
		return buf
	}
	buf := make([]byte, 3+len(value))
	buf[0] = flags
	buf[1] = typeCode
	buf[2] = byte(len(value))
	copy(buf[3:], value)
	return buf
}

// decodePathAttributes decodes path attributes from wire format.
func decodePathAttributes(data []byte) ([]PathAttribute, error) {
	var attrs []PathAttribute
	for len(data) > 0 {
		if len(data) < 3 {
			return nil, fmt.Errorf("bgp: path attribute too short: %d bytes remaining", len(data))
		}
		flags := data[0]
		typeCode := data[1]

		var attrLen int
		var hdrLen int
		if flags&attrFlagExtendedLength != 0 {
			if len(data) < 4 {
				return nil, fmt.Errorf("bgp: path attribute with extended length too short")
			}
			attrLen = int(binary.BigEndian.Uint16(data[2:4]))
			hdrLen = 4
		} else {
			attrLen = int(data[2])
			hdrLen = 3
		}

		if len(data) < hdrLen+attrLen {
			return nil, fmt.Errorf("bgp: path attribute value truncated: need %d, have %d",
				hdrLen+attrLen, len(data))
		}

		value := data[hdrLen : hdrLen+attrLen]
		data = data[hdrLen+attrLen:]

		attr, err := decodePathAttribute(flags, typeCode, value)
		if err != nil {
			return nil, err
		}
		if attr != nil {
			attrs = append(attrs, attr)
		}
	}
	return attrs, nil
}

// decodePathAttribute decodes a single path attribute value by type.
// Returns (nil, nil) for unknown optional attributes, which are skipped.
func decodePathAttribute(flags, typeCode uint8, value []byte) (PathAttribute, error) {
	switch typeCode {
	case AttrOrigin:
		if len(value) != 1 {
			return nil, fmt.Errorf("bgp: origin attribute bad length: %d", len(value))
		}
		return OriginAttribute(value[0]), nil

	case AttrASPath:
		return decodeASPath(value)

	case AttrNextHop:
		if len(value) != 4 {
			return nil, fmt.Errorf("bgp: next_hop attribute bad length: %d", len(value))
		}
		var ip [4]byte
		copy(ip[:], value)
		return &NextHopAttribute{NextHop: netip.AddrFrom4(ip)}, nil

	case AttrMpReachNLRI:
		return decodeMpReachNLRI(value)

	case AttrLargeCommunities:
		if len(value)%12 != 0 {
			return nil, fmt.Errorf("bgp: large communities attribute bad length: %d (must be multiple of 12)", len(value))
		}
		num := len(value) / 12
		comms := make([]LargeCommunity, num)
		for i := 0; i < num; i++ {
			off := i * 12
			comms[i] = LargeCommunity{
				GlobalAdmin: binary.BigEndian.Uint32(value[off : off+4]),
				LocalData1:  binary.BigEndian.Uint32(value[off+4 : off+8]),
				LocalData2:  binary.BigEndian.Uint32(value[off+8 : off+12]),
			}
		}
		return &LargeCommunitiesAttribute{Communities: comms}, nil

	default:
		// Unknown/unsupported attribute: skip it (return nil, nil).
		// Real routers send attributes we don't use (standard communities,
		// MED, LOCAL_PREF, etc.). Per RFC 4271, unrecognized optional
		// attributes should be ignored.
		return nil, nil
	}
}

// decodeMpReachNLRI decodes an MP_REACH_NLRI attribute value.
func decodeMpReachNLRI(value []byte) (PathAttribute, error) {
	if len(value) < 5 {
		return nil, fmt.Errorf("bgp: mp_reach_nlri too short: %d bytes", len(value))
	}
	// AFI (2 bytes) + SAFI (1 byte) + NH len (1 byte)
	nhLen := int(value[3])
	if len(value) < 4+nhLen {
		return nil, fmt.Errorf("bgp: mp_reach_nlri truncated: need next hop %d bytes, have %d", nhLen, len(value)-4)
	}
	nhBytes := value[4 : 4+nhLen]
	var nh netip.Addr
	switch nhLen {
	case 4:
		nh = netip.AddrFrom4([4]byte(nhBytes))
	case 16:
		nh = netip.AddrFrom16([16]byte(nhBytes))
	default:
		return nil, fmt.Errorf("bgp: mp_reach_nlri bad next hop length: %d", nhLen)
	}
	return &MpReachNLRIAttribute{NextHop: nh}, nil
}

// decodeASPath decodes an AS_PATH attribute value.
func decodeASPath(value []byte) (PathAttribute, error) {
	if len(value) < 2 {
		return nil, fmt.Errorf("bgp: as_path too short: %d bytes", len(value))
	}
	// We accept the attribute without fully decoding it — just validate.
	// Return a zero ASPathAttribute.
	return &ASPathAttribute{}, nil
}

// encodePathAttributes serializes a slice of path attributes into wire format.
func encodePathAttributes(attrs []PathAttribute) []byte {
	var total int
	serialized := make([][]byte, len(attrs))
	for i, attr := range attrs {
		serialized[i] = attr.Serialize()
		total += len(serialized[i])
	}

	buf := make([]byte, total)
	offset := 0
	for _, s := range serialized {
		copy(buf[offset:], s)
		offset += len(s)
	}
	return buf
}

// encodePrefixes serializes a slice of prefixes into wire format (NLRI / withdrawn).
func encodePrefixes(prefixes []netip.Prefix) []byte {
	// Calculate total size
	total := 0
	for _, p := range prefixes {
		bits := p.Bits()
		bytesNeeded := (bits + 7) / 8
		total += 1 + bytesNeeded // 1 byte length + address bytes
	}

	buf := make([]byte, total)
	offset := 0
	for _, p := range prefixes {
		bits := p.Bits()
		bytesNeeded := (bits + 7) / 8
		buf[offset] = uint8(bits)
		offset++

		// Get address bytes
		var addrBytes []byte
		if p.Addr().Is4() {
			a := p.Addr().As4()
			addrBytes = a[:]
		} else {
			a := p.Addr().As16()
			addrBytes = a[:]
		}
		copy(buf[offset:offset+bytesNeeded], addrBytes[:bytesNeeded])
		offset += bytesNeeded
	}
	return buf
}

// decodePrefixes decodes NLRI/withdrawn prefixes from wire format.
func decodePrefixes(data []byte) ([]netip.Prefix, error) {
	var prefixes []netip.Prefix
	for len(data) > 0 {
		prefixLen := int(data[0])
		data = data[1:]

		bytesNeeded := (prefixLen + 7) / 8
		if len(data) < bytesNeeded {
			return nil, fmt.Errorf("bgp: prefix truncated: need %d bytes, have %d", bytesNeeded, len(data))
		}

		prefixBytes := data[:bytesNeeded]
		data = data[bytesNeeded:]

		var addr netip.Addr
		if bytesNeeded <= 4 {
			// IPv4 (or default route, which is also IPv4)
			var ip [4]byte
			copy(ip[:], prefixBytes)
			addr = netip.AddrFrom4(ip)
		} else {
			var ip [16]byte
			copy(ip[:], prefixBytes)
			addr = netip.AddrFrom16(ip)
		}

		prefix, err := netip.ParsePrefix(fmt.Sprintf("%s/%d", addr.String(), prefixLen))
		if err != nil {
			return nil, fmt.Errorf("bgp: invalid prefix %s/%d: %w", addr.String(), prefixLen, err)
		}
		prefixes = append(prefixes, prefix)
	}
	return prefixes, nil
}

// wrapMessage creates a complete BGP message with header and body.
func wrapMessage(msgType uint8, body []byte) []byte {
	totalLen := HeaderLen + len(body)
	buf := make([]byte, totalLen)

	// Marker: 16 bytes of 0xFF
	for i := 0; i < 16; i++ {
		buf[i] = 0xFF
	}

	binary.BigEndian.PutUint16(buf[16:18], uint16(totalLen))
	buf[18] = msgType

	if body != nil {
		copy(buf[HeaderLen:], body)
	}
	return buf
}
