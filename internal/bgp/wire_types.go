package bgp

import "net/netip"

// BGP message types
const (
	MsgOpen         = 1
	MsgUpdate       = 2
	MsgNotification = 3
	MsgKeepalive    = 4
	MsgRouteRefresh = 5 // RFC 2918
)

// Path attribute type codes
const (
	AttrOrigin           = 1
	AttrASPath           = 2
	AttrNextHop          = 3
	AttrMpReachNLRI      = 14
	AttrMpUnreachNLRI    = 15
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
// at the TCP level via TCP MD5 (RFC 2385). The Password field is a fallback
// for loopback connections where TCP MD5 is not enforced by the kernel.
type OpenMessage struct {
	Version        uint8
	MyASN          uint16 // 2-byte ASN field
	MyASN32        uint32 // 4-byte local ASN (advertised via Four-octet ASN capability, RFC 6793)
	HoldTime       uint16
	BGPID          [4]byte
	OptParmLen     uint8
	Password       string // fallback auth for loopback (empty = none)
	HasIPv6Unicast bool   // remote peer advertised IPv6 unicast capability (AFI=2, SAFI=1)
	HasAS4Cap      bool   // remote peer advertised Four-octet ASN capability (capCode 65, RFC 6793)
}

// UPDATE message
type UpdateMessage struct {
	WithdrawnRoutes []netip.Prefix
	PathAttributes  []PathAttribute
	NLRI            []netip.Prefix
}

// KEEPALIVE message (no body, just header)
type KeepaliveMessage struct{}

// ROUTE-REFRESH message (RFC 2918). The peer asks us to re-advertise our
// full Adj-RIB-Out for the given AFI/SAFI. RFC 7313 reuses the reserved
// byte between AFI and SAFI as a subtype; we accept and ignore it.
type RouteRefreshMessage struct {
	AFI  uint16
	SAFI uint8
}

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
	NLRI    []netip.Prefix
}

// MpUnreachNLRIAttribute is the MP_UNREACH_NLRI path attribute (type 15, RFC 4760).
// Carries withdrawn IPv6 prefixes in the path attributes section of an UPDATE.
type MpUnreachNLRIAttribute struct {
	NLRI []netip.Prefix
}

// ASPathAttribute is the AS_PATH path attribute (type 2, well-known mandatory).
type ASPathAttribute struct {
	ASN       uint32 // local ASN to include
	FourOctet bool   // if true, always use 4-byte AS_SEQUENCE regardless of ASN value
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

// TypeCode returns the path attribute type code for ORIGIN (1).
func (o OriginAttribute) TypeCode() uint8 {
	return AttrOrigin
}

// TypeCode returns the path attribute type code for NEXT_HOP (3).
func (n *NextHopAttribute) TypeCode() uint8 {
	return AttrNextHop
}

// TypeCode returns the path attribute type code for MP_REACH_NLRI (14).
func (a *MpReachNLRIAttribute) TypeCode() uint8 {
	return AttrMpReachNLRI
}

// TypeCode returns the path attribute type code for MP_UNREACH_NLRI (15).
func (a *MpUnreachNLRIAttribute) TypeCode() uint8 {
	return AttrMpUnreachNLRI
}

// TypeCode returns the path attribute type code for AS_PATH (2).
func (a *ASPathAttribute) TypeCode() uint8 {
	return AttrASPath
}

// TypeCode returns the path attribute type code for Large Communities (32).
func (l *LargeCommunitiesAttribute) TypeCode() uint8 {
	return AttrLargeCommunities
}
