package bgp

import (
	"encoding/binary"
	"fmt"
	"io"
	"net/netip"
)

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
	case MsgRouteRefresh:
		return decodeRouteRefresh(body)
	default:
		return nil, fmt.Errorf("bgp: unknown message type: %d", h.Type)
	}
}

// decodeRouteRefresh parses a ROUTE-REFRESH message body (RFC 2918):
// AFI (2 bytes), the RFC 7313 subtype byte (0 on a plain RFC 2918
// refresh), SAFI (1 byte).
func decodeRouteRefresh(data []byte) (*RouteRefreshMessage, error) {
	if len(data) != 4 {
		return nil, fmt.Errorf("bgp: route-refresh body length %d, want 4", len(data))
	}
	return &RouteRefreshMessage{
		AFI:     binary.BigEndian.Uint16(data[0:2]),
		Subtype: data[2],
		SAFI:    data[3],
	}, nil
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
	if o.Version != 4 {
		return nil, fmt.Errorf("bgp: unsupported version %d", o.Version)
	}
	copy(o.BGPID[:], data[5:9])

	// Parse optional parameters for Four-octet ASN Capability (RFC 6793)
	// and IPv6 unicast capability (RFC 4760).
	if o.OptParmLen > 0 && len(data) >= 10+int(o.OptParmLen) {
		opts := data[10 : 10+int(o.OptParmLen)]
		for len(opts) >= 2 {
			paramType := opts[0]
			paramLen := int(opts[1])
			if 2+paramLen > len(opts) {
				break // malformed, stop parsing
			}
			paramData := opts[2 : 2+paramLen]
			// Capability parameter (type 2) — may contain multiple TLVs
			if paramType == 2 {
				capData := paramData
				for len(capData) >= 2 {
					capCode := capData[0]
					capLen := int(capData[1])
					if 2+capLen > len(capData) {
						break // malformed, stop
					}
					if capCode == 65 && capLen == 4 {
						// Four-octet ASN Capability — use the LAST one found
						o.MyASN32 = binary.BigEndian.Uint32(capData[2 : 2+capLen])
						o.HasAS4Cap = true
					}
					if capCode == 1 && capLen == 4 {
						// Multiprotocol Extension — check AFI/SAFI for IPv6 unicast.
						// Value: AFI(2) + Reserved(1) + SAFI(1) = 4 bytes.
						// capData[2:4]=AFI, capData[4]=Reserved, capData[5]=SAFI.
						afi := binary.BigEndian.Uint16(capData[2:4])
						safi := capData[5]
						if afi == 2 && safi == 1 {
							o.HasIPv6Unicast = true
						}
					}
					capData = capData[2+capLen:]
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
	u.WithdrawnRoutes, err = decodePrefixes(data[:withdrawnLen], 1) // AFI=1 (IPv4)
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

	u.NLRI, err = decodePrefixes(data, 1) // AFI=1 (IPv4)
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

	case AttrMpUnreachNLRI:
		return decodeMpUnreachNLRI(value)

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
	afi := binary.BigEndian.Uint16(value[0:2])
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
	case 32:
		// IPv6 with link-local: use first 16 bytes (global next hop)
		nh = netip.AddrFrom16([16]byte(nhBytes[0:16]))
	default:
		return nil, fmt.Errorf("bgp: mp_reach_nlri bad next hop length: %d", nhLen)
	}

	// After next hop comes SNP (1 byte), then NLRI.
	nlriStart := 4 + nhLen + 1 // AFI(2) + SAFI(1) + NHLen(1) + NH(nhLen) + SNP(1)
	var nlri []netip.Prefix
	if len(value) > nlriStart {
		var err error
		nlri, err = decodePrefixes(value[nlriStart:], afi)
		if err != nil {
			return nil, fmt.Errorf("bgp: mp_reach_nlri decode NLRI: %w", err)
		}
	}

	return &MpReachNLRIAttribute{NextHop: nh, NLRI: nlri}, nil
}

// decodeMpUnreachNLRI decodes an MP_UNREACH_NLRI attribute value.
// Format: AFI(2) + SAFI(1) + NLRI (no next hop, no SNP).
func decodeMpUnreachNLRI(value []byte) (PathAttribute, error) {
	if len(value) < 3 {
		return nil, fmt.Errorf("bgp: mp_unreach_nlri too short: %d bytes", len(value))
	}
	// AFI (2 bytes) + SAFI (1 byte) — NLRI follows immediately
	afi := binary.BigEndian.Uint16(value[0:2])
	nlriData := value[3:]
	var nlri []netip.Prefix
	if len(nlriData) > 0 {
		var err error
		nlri, err = decodePrefixes(nlriData, afi)
		if err != nil {
			return nil, fmt.Errorf("bgp: mp_unreach_nlri decode NLRI: %w", err)
		}
	}
	return &MpUnreachNLRIAttribute{NLRI: nlri}, nil
}

// decodeASPath decodes an AS_PATH attribute value.
// Parses AS_SEQUENCE segments (type 1 = 2-byte, type 2 = 4-byte).
// Returns the first ASN from the first segment.
func decodeASPath(value []byte) (PathAttribute, error) {
	if len(value) < 2 {
		return nil, fmt.Errorf("bgp: as_path too short: %d bytes", len(value))
	}

	segType := value[0]
	segLen := int(value[1]) // number of ASNs in this segment

	if segType != 1 && segType != 2 {
		return nil, fmt.Errorf("bgp: unknown as_path segment type: %d", segType)
	}
	if segLen == 0 {
		return nil, fmt.Errorf("bgp: as_path segment has zero length")
	}

	// Determine ASN width from the total segment value length.
	// 2 bytes (header) + segLen * 2 (2-byte ASNs) or + segLen * 4 (4-byte ASNs).
	var asn uint32
	if len(value) >= 2+segLen*4 { //nolint:gocritic // if/else is clearer than switch here
		// 4-byte ASNs
		asn = binary.BigEndian.Uint32(value[2:6])
	} else if len(value) >= 2+segLen*2 {
		// 2-byte ASNs
		asn = uint32(binary.BigEndian.Uint16(value[2:4]))
	} else {
		return nil, fmt.Errorf("bgp: as_path segment truncated: need at least %d, have %d",
			2+segLen*2, len(value))
	}
	return &ASPathAttribute{ASN: asn}, nil
}

// decodePrefixes decodes NLRI/withdrawn prefixes from wire format.
// afi is the Address Family Identifier (1=IPv4, 2=IPv6).
func decodePrefixes(data []byte, afi uint16) ([]netip.Prefix, error) {
	var prefixes []netip.Prefix
	is6 := afi == 2
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
		if is6 {
			var ip [16]byte
			copy(ip[:], prefixBytes)
			addr = netip.AddrFrom16(ip)
		} else {
			// IPv4 (or default route, which is also IPv4)
			var ip [4]byte
			copy(ip[:], prefixBytes)
			addr = netip.AddrFrom4(ip)
		}

		prefix := netip.PrefixFrom(addr, prefixLen)
		if !prefix.IsValid() {
			return nil, fmt.Errorf("bgp: invalid prefix %s/%d", addr.String(), prefixLen)
		}
		prefixes = append(prefixes, prefix)
	}
	return prefixes, nil
}
