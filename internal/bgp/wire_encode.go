package bgp

import (
	"encoding/binary"
	"log"
	"net/netip"
)

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

// Serialize encodes the OPEN message to wire format including header.
// Advertises IPv6 unicast (RFC 4760) and Four-octet ASN (RFC 6793)
// capabilities. Uses either 2-byte or 4-byte AS_PATH encoding depending
// on the local ASN value (AS_TRANS=23456 for >65535 in the 2-byte field).
// If Password is set, it is included as parameter type 1 (fallback auth
// for loopback connections where TCP MD5 is not enforced).
func (o *OpenMessage) Serialize() []byte {
	// Build capability parameter with three capabilities:
	//   1) IPv6 unicast (RFC 4760): code 1, len 4 (AFI=2, SAFI=1)
	//   2) IPv4 unicast (RFC 4760): code 1, len 4 (AFI=1, SAFI=1)
	//   3) Four-octet ASN (RFC 6793): code 65, len 4 (MyASN32 in network byte order)
	// Parameter type: 2 (Capability)
	capParam := make([]byte, 20)
	capParam[0] = 2     // Parameter type: Capability
	capParam[1] = 18    // Parameter length: three capabilities, 6 bytes each = 18
	// Capability 1: Multiprotocol Extension for IPv6 unicast
	capParam[2] = 1     // Capability code: Multiprotocol Extension
	capParam[3] = 4     // Capability length: 4
	capParam[4] = 0x00  // AFI high byte (IPv6=2)
	capParam[5] = 0x02  // AFI low byte
	capParam[6] = 0x00  // Reserved
	capParam[7] = 0x01  // SAFI=1 (unicast)
	// Capability 2: Multiprotocol Extension for IPv4 unicast
	capParam[8] = 1     // Capability code: Multiprotocol Extension
	capParam[9] = 4     // Capability length: 4
	capParam[10] = 0x00 // AFI high byte (IPv4=1)
	capParam[11] = 0x01 // AFI low byte
	capParam[12] = 0x00 // Reserved
	capParam[13] = 0x01 // SAFI=1 (unicast)
	// Capability 3: Four-octet ASN (RFC 6793)
	capParam[14] = 65   // Capability code: Four-octet ASN
	capParam[15] = 4    // Capability length: 4
	binary.BigEndian.PutUint32(capParam[16:20], o.MyASN32)

	// Build password parameter (type 1) if set and fits in OPEN message.
	// Max password length is 233 bytes: OptParmLen (255) - capParam (20) - pwParam header (2).
	var pwParam []byte
	if o.Password != "" {
		if len(o.Password) > 233 {
			log.Printf("WARNING: BGP password too long (%d bytes > 233), omitting from OPEN", len(o.Password))
		} else {
			pwParam = make([]byte, 2+len(o.Password))
			pwParam[0] = 1 // Parameter type: password
			pwParam[1] = uint8(len(o.Password))
			copy(pwParam[2:], o.Password)
		}
	}

	// 2-byte ASN field: use AS_TRANS (23456) for ASNs > 65535 (RFC 6793).
	o.OptParmLen = uint8(len(capParam) + len(pwParam))
	if o.MyASN32 > 65535 {
		o.MyASN = 23456 // AS_TRANS — real ASN in Four-octet ASN capability
	} else {
		o.MyASN = uint16(o.MyASN32)
	}

	body := make([]byte, 10+o.OptParmLen)
	body[0] = o.Version
	binary.BigEndian.PutUint16(body[1:3], o.MyASN)
	binary.BigEndian.PutUint16(body[3:5], o.HoldTime)
	copy(body[5:9], o.BGPID[:])
	body[9] = o.OptParmLen
	copy(body[10:], capParam)
	if len(pwParam) > 0 {
		copy(body[10+len(capParam):], pwParam)
	}

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

// Serialize encodes the ORIGIN path attribute value.
func (o OriginAttribute) Serialize() []byte {
	return encodePathAttribute(attrFlagTransitive, AttrOrigin, []byte{byte(o)})
}

// Serialize encodes the NEXT_HOP path attribute value (4 bytes, IPv4 only).
func (n *NextHopAttribute) Serialize() []byte {
	ip := n.NextHop.As4()
	return encodePathAttribute(attrFlagTransitive, AttrNextHop, ip[:])
}

// Serialize encodes the MP_REACH_NLRI path attribute value (RFC 4760).
func (a *MpReachNLRIAttribute) Serialize() []byte {
	// Two-byte AFI (2=IPv6) + 1-byte SAFI (1=unicast) + 1-byte NH len + NH + 1-byte SNP + NLRI
	afi := uint16(2) // IPv6
	safi := uint8(1)
	nh := a.NextHop.AsSlice()
	data := []byte{byte(afi >> 8), byte(afi), safi, byte(len(nh))}
	data = append(data, nh...)
	data = append(data, 0) // SNP = 0 (no subsequent address family info)
	data = append(data, encodePrefixes(a.NLRI)...)
	return encodePathAttribute(attrFlagOptional, AttrMpReachNLRI, data)
}

// Serialize encodes the MP_UNREACH_NLRI path attribute value (RFC 4760).
// Format: AFI(2) + SAFI(1) + NLRI — no next hop, no SNP.
func (a *MpUnreachNLRIAttribute) Serialize() []byte {
	afi := uint16(2) // IPv6
	safi := uint8(1)
	data := []byte{byte(afi >> 8), byte(afi), safi}
	data = append(data, encodePrefixes(a.NLRI)...)
	return encodePathAttribute(attrFlagOptional, AttrMpUnreachNLRI, data)
}

// Serialize encodes the AS_PATH path attribute value.
// Uses AS_SEQUENCE (type code 2) for both 2-byte and 4-byte ASNs.
func (a *ASPathAttribute) Serialize() []byte {
	if a.ASN > 65535 || a.FourOctet {
		// 4-octet AS_SEQUENCE: segment type=2, length=1 (in ASNs), value=4 bytes
		val := make([]byte, 6)
		val[0] = 2 // AS_SEQUENCE
		val[1] = 1 // one AS in segment
		binary.BigEndian.PutUint32(val[2:6], a.ASN)
		return encodePathAttribute(attrFlagTransitive, AttrASPath, val)
	}
	// 2-octet AS_SEQUENCE: segment type=2, length=1 (in ASNs), value=2 bytes
	val := make([]byte, 4)
	val[0] = 2 // AS_SEQUENCE
	val[1] = 1 // one AS in segment
	binary.BigEndian.PutUint16(val[2:4], uint16(a.ASN))
	return encodePathAttribute(attrFlagTransitive, AttrASPath, val)
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
