package feeds

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"

	"go4.org/netipx"
)

// SRS binary format constants.
const (
	srsItemQueryType              uint8 = 0
	srsItemNetwork                uint8 = 1
	srsItemDomain                 uint8 = 2
	srsItemDomainKeyword          uint8 = 3
	srsItemDomainRegex            uint8 = 4
	srsItemSourceIPCIDR           uint8 = 5
	srsItemIPCIDR                 uint8 = 6
	srsItemSourcePort             uint8 = 7
	srsItemSourcePortRange        uint8 = 8
	srsItemPort                   uint8 = 9
	srsItemPortRange              uint8 = 10
	srsItemProcessName            uint8 = 11
	srsItemProcessPath            uint8 = 12
	srsItemPackageName            uint8 = 13
	srsItemWIFISSID               uint8 = 14
	srsItemWIFIBSSID              uint8 = 15
	srsItemAdGuardDomain          uint8 = 16
	srsItemProcessPathRegex       uint8 = 17
	srsItemNetworkType            uint8 = 18
	srsItemNetworkIsExpensive     uint8 = 19
	srsItemNetworkIsConstrained   uint8 = 20
	srsItemNetworkInterfaceAddress uint8 = 21
	srsItemDefaultInterfaceAddress uint8 = 22
	srsItemFinal                  uint8 = 0xFF
)

// maxIPSetRangeCount limits allocations when reading untrusted count values.
const maxIPSetRangeCount = 10_000_000

// ParseSRSConfig controls what data to extract from SRS files.
type ParseSRSConfig struct {
	CIDRs bool `json:"cidrs"` // extract ip_cidr and source_ip_cidr items
}

// ParseSRS parses raw sing-box rule-set binary (.srs) data and returns
// canonical entries. cfgJSON controls what data to extract, e.g. {"cidrs":true}.
// When cfgJSON is empty, CIDRs are extracted by default.
// Logical rules (type 1) are skipped — only default rules (type 0) are processed.
// Supports SRS format versions 1 through 5.
func ParseSRS(data []byte, cfgJSON string) ([]canonicalEntry, error) {
	var cfg ParseSRSConfig
	if cfgJSON == "" {
		cfg.CIDRs = true
	}
	if cfgJSON != "" {
		if err := json.Unmarshal([]byte(cfgJSON), &cfg); err != nil {
			return nil, fmt.Errorf("parse srs config: %w", err)
		}
	}

	if len(data) < 4 {
		return nil, fmt.Errorf("srs: too short")
	}
	if data[0] != 0x53 || data[1] != 0x52 || data[2] != 0x53 {
		return nil, fmt.Errorf("srs: invalid magic, expected SRS")
	}
	version := data[3]
	if version < 1 || version > 5 {
		return nil, fmt.Errorf("srs: unsupported version %d", version)
	}

	zr, err := zlib.NewReader(bytes.NewReader(data[4:]))
	if err != nil {
		return nil, fmt.Errorf("srs: zlib decompress: %w", err)
	}
	defer zr.Close()

	ruleCount, err := binary.ReadUvarint(&byteReader{r: zr})
	if err != nil {
		return nil, fmt.Errorf("srs: read rule count: %w", err)
	}

	var entries []canonicalEntry

	for i := uint64(0); i < ruleCount; i++ {
		ruleType, err := readByte(zr)
		if err != nil {
			return nil, fmt.Errorf("srs: rule[%d] type: %w", i, err)
		}
		switch ruleType {
		case 0: // default rule
			cidrs, err := parseDefaultRule(zr, &cfg)
			if err != nil {
				return nil, fmt.Errorf("srs: rule[%d]: %w", i, err)
			}
			if len(cidrs) > 0 {
				entries = append(entries, canonicalEntry{CIDRs: cidrs})
			}
		case 1: // logical rule
			if err := skipLogicalRule(zr); err != nil {
				return nil, fmt.Errorf("srs: rule[%d] logical: %w", i, err)
			}
		default:
			return nil, fmt.Errorf("srs: rule[%d] unknown type %d", i, ruleType)
		}
	}

	return entries, nil
}

// parseDefaultRule loops items until srsItemFinal.
func parseDefaultRule(r io.Reader, cfg *ParseSRSConfig) ([]string, error) {
	var allCIDRs []string
	for {
		itemType, err := readByte(r)
		if err != nil {
			return nil, err
		}
		switch itemType {
		case srsItemIPCIDR, srsItemSourceIPCIDR:
			if !cfg.CIDRs {
				if err := skipIPSet(r); err != nil {
					return nil, err
				}
				continue
			}
			cidrs, err := readIPSetAsCIDRs(r)
			if err != nil {
				return nil, err
			}
			allCIDRs = append(allCIDRs, cidrs...)
		case srsItemDomain:
			if err := skipDomainMatcher(r); err != nil {
				return nil, err
			}
		case srsItemAdGuardDomain:
			if err := skipAdGuardMatcher(r); err != nil {
				return nil, err
			}
		case srsItemQueryType, srsItemSourcePort, srsItemPort:
			if err := skipUint16Array(r); err != nil {
				return nil, err
			}
		case srsItemNetwork, srsItemDomainKeyword, srsItemDomainRegex,
			srsItemSourcePortRange, srsItemPortRange,
			srsItemProcessName, srsItemProcessPath, srsItemProcessPathRegex,
			srsItemPackageName, srsItemWIFISSID, srsItemWIFIBSSID:
			if err := skipStringArray(r); err != nil {
				return nil, err
			}
		case srsItemNetworkType:
			if err := skipUint8Array(r); err != nil {
				return nil, err
			}
		case srsItemNetworkIsExpensive, srsItemNetworkIsConstrained:
			// no data, just the type byte
		case srsItemNetworkInterfaceAddress:
			if err := skipNetworkInterfaceAddress(r); err != nil {
				return nil, err
			}
		case srsItemDefaultInterfaceAddress:
			if err := skipPrefixArray(r); err != nil {
				return nil, err
			}
		case srsItemFinal:
			var invert uint8
			if err := binary.Read(r, binary.BigEndian, &invert); err != nil {
				return nil, err
			}
			_ = invert
			return allCIDRs, nil
		default:
			return nil, fmt.Errorf("unknown item type %d", itemType)
		}
	}
}

// readIPSetAsCIDRs reads an IP set from an SRS rule and returns CIDR strings.
func readIPSetAsCIDRs(r io.Reader) ([]string, error) {
	ver, err := readByte(r)
	if err != nil {
		return nil, fmt.Errorf("ipset version: %w", err)
	}
	if ver != 1 {
		return nil, fmt.Errorf("ipset: unsupported version %d", ver)
	}
	var count uint64
	if err := binary.Read(r, binary.BigEndian, &count); err != nil {
		return nil, fmt.Errorf("ipset count: %w", err)
	}
	if count > maxIPSetRangeCount {
		return nil, fmt.Errorf("ipset: count %d exceeds max %d", count, maxIPSetRangeCount)
	}
	var builder netipx.IPSetBuilder
	for i := uint64(0); i < count; i++ {
		fromLen, err := binary.ReadUvarint(&byteReader{r: r})
		if err != nil {
			return nil, fmt.Errorf("ipset fromLen: %w", err)
		}
		if fromLen > 16 || (fromLen != 4 && fromLen != 16) {
			return nil, fmt.Errorf("ipset: invalid fromLen %d", fromLen)
		}
		from := make([]byte, fromLen)
		if _, err := io.ReadFull(r, from); err != nil {
			return nil, fmt.Errorf("ipset from: %w", err)
		}
		toLen, err := binary.ReadUvarint(&byteReader{r: r})
		if err != nil {
			return nil, fmt.Errorf("ipset toLen: %w", err)
		}
		if toLen > 16 || (toLen != 4 && toLen != 16) {
			return nil, fmt.Errorf("ipset: invalid toLen %d", toLen)
		}
		if fromLen != toLen {
			return nil, fmt.Errorf("ipset: from/to address length mismatch: %d != %d", fromLen, toLen)
		}
		to := make([]byte, toLen)
		if _, err := io.ReadFull(r, to); err != nil {
			return nil, fmt.Errorf("ipset to: %w", err)
		}
		fromAddr, ok := netip.AddrFromSlice(from)
		if !ok {
			return nil, fmt.Errorf("ipset: invalid from addr len %d", fromLen)
		}
		toAddr, ok := netip.AddrFromSlice(to)
		if !ok {
			return nil, fmt.Errorf("ipset: invalid to addr len %d", toLen)
		}
		builder.AddRange(netipx.IPRangeFrom(fromAddr, toAddr))
	}
	ipSet, err := builder.IPSet()
	if err != nil {
		return nil, fmt.Errorf("ipset build: %w", err)
	}
	prefixes := ipSet.Prefixes()
	result := make([]string, len(prefixes))
	for i, p := range prefixes {
		result[i] = p.String()
	}
	return result, nil
}

// --- skip helpers for items we don't need ---

func skipIPSet(r io.Reader) error {
	ver, err := readByte(r)
	if err != nil {
		return fmt.Errorf("ipset version: %w", err)
	}
	if ver != 1 {
		return fmt.Errorf("ipset: unsupported version %d", ver)
	}
	var count uint64
	if err := binary.Read(r, binary.BigEndian, &count); err != nil {
		return fmt.Errorf("ipset count: %w", err)
	}
	if count > maxIPSetRangeCount {
		return fmt.Errorf("ipset: count %d exceeds max %d", count, maxIPSetRangeCount)
	}
	br := &byteReader{r: r}
	for i := uint64(0); i < count; i++ {
		fl, err := binary.ReadUvarint(br)
		if err != nil {
			return fmt.Errorf("ipset fromLen: %w", err)
		}
		if err := skipBytes(r, int64(fl)); err != nil {
			return fmt.Errorf("ipset from: %w", err)
		}
		tl, err := binary.ReadUvarint(br)
		if err != nil {
			return fmt.Errorf("ipset toLen: %w", err)
		}
		if err := skipBytes(r, int64(tl)); err != nil {
			return fmt.Errorf("ipset to: %w", err)
		}
	}
	return nil
}

func skipStringArray(r io.Reader) error {
	br := &byteReader{r: r}
	count, err := binary.ReadUvarint(br)
	if err != nil {
		return err
	}
	if count > maxIPSetRangeCount {
		return fmt.Errorf("string array: count %d exceeds max %d", count, maxIPSetRangeCount)
	}
	for i := uint64(0); i < count; i++ {
		slen, err := binary.ReadUvarint(br)
		if err != nil {
			return err
		}
		if err := skipBytes(r, int64(slen)); err != nil {
			return err
		}
	}
	return nil
}

func skipUint16Array(r io.Reader) error {
	br := &byteReader{r: r}
	count, err := binary.ReadUvarint(br)
	if err != nil {
		return err
	}
	if count > maxIPSetRangeCount {
		return fmt.Errorf("uint16 array: count %d exceeds max %d", count, maxIPSetRangeCount)
	}
	return skipBytes(r, int64(count*2))
}

func skipUint8Array(r io.Reader) error {
	br := &byteReader{r: r}
	count, err := binary.ReadUvarint(br)
	if err != nil {
		return err
	}
	if count > maxIPSetRangeCount {
		return fmt.Errorf("uint8 array: count %d exceeds max %d", count, maxIPSetRangeCount)
	}
	return skipBytes(r, int64(count))
}

func skipDomainMatcher(r io.Reader) error {
	version, err := readByte(r)
	if err != nil {
		return err
	}
	br := &byteReader{r: r}

	// prefix list
	pc, err := binary.ReadUvarint(br)
	if err != nil {
		return err
	}
	for i := uint64(0); i < pc; i++ {
		l, err := binary.ReadUvarint(br)
		if err != nil {
			return err
		}
		if err := skipBytes(r, int64(l)); err != nil {
			return err
		}
	}
	// suffix list
	sc, err := binary.ReadUvarint(br)
	if err != nil {
		return err
	}
	for i := uint64(0); i < sc; i++ {
		l, err := binary.ReadUvarint(br)
		if err != nil {
			return err
		}
		if err := skipBytes(r, int64(l)); err != nil {
			return err
		}
	}
	// v2+ has fallback domain list
	if version >= 2 {
		fc, err := binary.ReadUvarint(br)
		if err != nil {
			return err
		}
		for i := uint64(0); i < fc; i++ {
			l, err := binary.ReadUvarint(br)
			if err != nil {
				return err
			}
			if err := skipBytes(r, int64(l)); err != nil {
				return err
			}
		}
	}
	return nil
}

func skipAdGuardMatcher(r io.Reader) error {
	br := &byteReader{r: r}
	count, err := binary.ReadUvarint(br)
	if err != nil {
		return err
	}
	for i := uint64(0); i < count; i++ {
		l, err := binary.ReadUvarint(br)
		if err != nil {
			return err
		}
		if err := skipBytes(r, int64(l)); err != nil {
			return err
		}
	}
	return nil
}

func skipPrefixArray(r io.Reader) error {
	br := &byteReader{r: r}
	count, err := binary.ReadUvarint(br)
	if err != nil {
		return err
	}
	if count > maxIPSetRangeCount {
		return fmt.Errorf("prefix array: count %d exceeds limit", count)
	}
	for i := uint64(0); i < count; i++ {
		addrLen, err := binary.ReadUvarint(br)
		if err != nil {
			return err
		}
		if addrLen > 16 {
			return fmt.Errorf("prefix: invalid addr length %d", addrLen)
		}
		if err := skipBytes(r, int64(addrLen+1)); err != nil {
			return err
		} // addr + prefix byte
	}
	return nil
}

func skipNetworkInterfaceAddress(r io.Reader) error {
	br := &byteReader{r: r}
	mapSize, err := binary.ReadUvarint(br)
	if err != nil {
		return err
	}
	if mapSize > maxIPSetRangeCount {
		return fmt.Errorf("network interface: map size %d exceeds limit", mapSize)
	}
	for i := uint64(0); i < mapSize; i++ {
		if err := skipBytes(r, 1); err != nil {
			return err
		} // key byte
		valCount, err := binary.ReadUvarint(br)
		if err != nil {
			return err
		}
		if valCount > maxIPSetRangeCount {
			return fmt.Errorf("network interface: prefix count %d exceeds limit", valCount)
		}
		for j := uint64(0); j < valCount; j++ {
			addrLen, err := binary.ReadUvarint(br)
			if err != nil {
				return err
			}
			if addrLen > 16 {
				return fmt.Errorf("network interface: invalid addr length %d", addrLen)
			}
			if err := skipBytes(r, int64(addrLen+1)); err != nil {
				return err
			} // addr + prefix byte
		}
	}
	return nil
}

func skipLogicalRule(r io.Reader) error {
	mode, err := readByte(r)
	if err != nil {
		return err
	}
	_ = mode // 0=and, 1=or
	br := &byteReader{r: r}
	subCount, err := binary.ReadUvarint(br)
	if err != nil {
		return err
	}
	for i := uint64(0); i < subCount; i++ {
		rt, err := readByte(r)
		if err != nil {
			return err
		}
		switch rt {
		case 0:
			if _, err := parseDefaultRule(r, &ParseSRSConfig{}); err != nil {
				return err
			}
		case 1:
			if err := skipLogicalRule(r); err != nil {
				return err
			}
		default:
			return fmt.Errorf("srs: unknown sub-rule type %d", rt)
		}
	}
	var invert uint8
	return binary.Read(r, binary.BigEndian, &invert)
}

func skipBytes(r io.Reader, n int64) error {
	_, err := io.CopyN(io.Discard, r, n)
	return err
}

// --- reader helpers ---

// byteReader adapts an io.Reader to io.ByteReader for binary.ReadUvarint.
type byteReader struct {
	r   io.Reader
	buf [1]byte
}

func (b *byteReader) ReadByte() (byte, error) {
	_, err := io.ReadFull(b.r, b.buf[:])
	return b.buf[0], err
}

func readByte(r io.Reader) (byte, error) {
	var buf [1]byte
	_, err := io.ReadFull(r, buf[:])
	return buf[0], err
}
