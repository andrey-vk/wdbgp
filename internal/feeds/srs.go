package feeds

import (
	"bytes"
	"compress/zlib"
	"context"
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
	srsItemPackageNameRegex        uint8 = 23
	srsItemFinal                   uint8 = 0xFF
)

// maxIPSetRangeCount limits allocations when reading untrusted count values.
const maxIPSetRangeCount = 10_000_000

// maxDecompressedSRS limits the total decompressed size of SRS rule data.
const maxDecompressedSRS = 64 << 20 // 64 MiB

// maxSRSRecursionDepth limits recursion depth in logical rule traversal.
const maxSRSRecursionDepth = 100

// ParseSRSConfig controls what data to extract from SRS files.
type ParseSRSConfig struct {
	CIDRs bool `json:"cidrs"` // extract ip_cidr and source_ip_cidr items
}

// ParseSRS parses raw sing-box rule-set binary (.srs) data and returns
// canonical entries. cfgJSON controls what data to extract, e.g. {"cidrs":true}.
// When cfgJSON is empty, CIDRs are extracted by default.
// Logical rules (type 1) are recursively traversed to extract CIDRs.
// Inverted rules are skipped — their semantics (everything EXCEPT) are opposite of what we want.
// Supports SRS format versions 1 through 5.
// maxEntries limits the total number of CIDRs across all entries; 0 means no limit.
// This is enforced during parsing (not just at the end) because ParseSRS runs
// synchronously and goja's timeout/interrupt cannot interrupt it.
func ParseSRS(ctx context.Context, data []byte, cfgJSON string, maxEntries int) ([]canonicalEntry, error) {
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
	lr := io.LimitReader(zr, maxDecompressedSRS)

	ruleCount, err := binary.ReadUvarint(&byteReader{r: lr})
	if err != nil {
		return nil, fmt.Errorf("srs: read rule count: %w", err)
	}

	var entries []canonicalEntry

	for i := uint64(0); i < ruleCount; i++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		ruleType, err := readByte(lr)
		if err != nil {
			return nil, fmt.Errorf("srs: rule[%d] type: %w", i, err)
		}
		switch ruleType {
		case 0: // default rule
			cidrs, err := parseDefaultRule(ctx, lr, &cfg)
			if err != nil {
				return nil, fmt.Errorf("srs: rule[%d]: %w", i, err)
			}
			if len(cidrs) > 0 {
				entries = append(entries, canonicalEntry{CIDRs: cidrs})
			}
		case 1: // logical rule
			cidrs, err := parseLogicalRule(ctx, lr, &cfg, 0)
			if err != nil {
				return nil, fmt.Errorf("srs: rule[%d] logical: %w", i, err)
			}
			if len(cidrs) > 0 {
				entries = append(entries, canonicalEntry{CIDRs: cidrs})
			}
		default:
			return nil, fmt.Errorf("srs: rule[%d] unknown type %d", i, ruleType)
		}
	}

	// Enforce maxEntries limit on total CIDRs across all entries.
	totalCIDRs := 0
	for _, e := range entries {
		totalCIDRs += len(e.CIDRs)
	}
	if maxEntries > 0 && totalCIDRs > maxEntries {
		return nil, fmt.Errorf("srs: %d CIDRs exceeds limit of %d", totalCIDRs, maxEntries)
	}

	// Drain remaining zlib stream to validate checksum and detect truncation
	if _, err := io.Copy(io.Discard, lr); err != nil {
		return nil, fmt.Errorf("srs: zlib stream error: %w", err)
	}

	return entries, nil
}

// parseDefaultRule loops items until srsItemFinal.
func parseDefaultRule(ctx context.Context, r io.Reader, cfg *ParseSRSConfig) ([]string, error) {
	var allCIDRs []string
	var hasConstraint bool
	for {
		itemType, err := readByte(r)
		if err != nil {
			return nil, err
		}
		switch itemType {
		case srsItemIPCIDR:
			if !cfg.CIDRs {
		if err := skipIPSet(ctx, r); err != nil {
				return nil, err
			}
			continue
		}
		cidrs, err := readIPSetAsCIDRs(ctx, r)
			if err != nil {
				return nil, err
			}
			allCIDRs = append(allCIDRs, cidrs...)
		case srsItemSourceIPCIDR:
			if err := skipIPSet(ctx, r); err != nil {
				return nil, err
			}
		case srsItemDomain:
			if err := skipDomainMatcher(r); err != nil {
				return nil, err
			}
		case srsItemDomainKeyword, srsItemDomainRegex:
			if err := skipStringArray(ctx, r); err != nil {
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
			hasConstraint = true
		case srsItemNetwork,
			srsItemSourcePortRange, srsItemPortRange,
			srsItemProcessName, srsItemProcessPath, srsItemProcessPathRegex,
			srsItemPackageName, srsItemPackageNameRegex, srsItemWIFISSID, srsItemWIFIBSSID:
			if err := skipStringArray(ctx, r); err != nil {
				return nil, err
			}
			hasConstraint = true
		case srsItemNetworkType:
			if err := skipUint8Array(r); err != nil {
				return nil, err
			}
			hasConstraint = true
		case srsItemNetworkIsExpensive, srsItemNetworkIsConstrained:
			// no data, just the type byte
			hasConstraint = true
		case srsItemNetworkInterfaceAddress:
			if err := skipNetworkInterfaceAddress(r); err != nil {
				return nil, err
			}
			hasConstraint = true
		case srsItemDefaultInterfaceAddress:
			if err := skipPrefixArray(ctx, r); err != nil {
				return nil, err
			}
			hasConstraint = true
		case srsItemFinal:
			var invert uint8
			if err := binary.Read(r, binary.BigEndian, &invert); err != nil {
				return nil, err
			}
			if invert != 0 || hasConstraint {
				return nil, nil // skip inverted or constrained rules
			}
			return allCIDRs, nil
		default:
			return nil, fmt.Errorf("unknown item type %d", itemType)
		}
	}
}

// readIPSetAsCIDRs reads an IP set from an SRS rule and returns CIDR strings.
func readIPSetAsCIDRs(ctx context.Context, r io.Reader) ([]string, error) {
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
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
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

func skipIPSet(ctx context.Context, r io.Reader) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
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
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
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

func skipStringArray(ctx context.Context, r io.Reader) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	br := &byteReader{r: r}
	count, err := binary.ReadUvarint(br)
	if err != nil {
		return err
	}
	if count > maxIPSetRangeCount {
		return fmt.Errorf("string array: count %d exceeds max %d", count, maxIPSetRangeCount)
	}
	for i := uint64(0); i < count; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
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
	return fmt.Errorf("srs: domain items are not supported")
}

func skipAdGuardMatcher(r io.Reader) error {
	return fmt.Errorf("srs: adguard domain items are not supported")
}

func skipPrefixArray(ctx context.Context, r io.Reader) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	br := &byteReader{r: r}
	count, err := binary.ReadUvarint(br)
	if err != nil {
		return err
	}
	if count > maxIPSetRangeCount {
		return fmt.Errorf("prefix array: count %d exceeds limit", count)
	}
	for i := uint64(0); i < count; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
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

func parseLogicalRule(ctx context.Context, r io.Reader, cfg *ParseSRSConfig, depth int) ([]string, error) {
	if depth > maxSRSRecursionDepth {
		return nil, fmt.Errorf("srs: logical rule recursion depth %d exceeds limit", depth)
	}
	mode, err := readByte(r)
	if err != nil {
		return nil, err
	}

	// Validate mode before reading sub-rules so we can skip them correctly.
	if mode != 0 && mode != 1 {
		br := &byteReader{r: r}
		subCount, err := binary.ReadUvarint(br)
		if err != nil {
			return nil, err
		}
		skipCfg := &ParseSRSConfig{}
		for i := uint64(0); i < subCount; i++ {
			rt, err := readByte(r)
			if err != nil {
				return nil, err
			}
			switch rt {
			case 0:
				if _, err := parseDefaultRule(ctx, r, skipCfg); err != nil {
					return nil, err
				}
			case 1:
				if _, err := parseLogicalRule(ctx, r, skipCfg, depth+1); err != nil {
					return nil, err
				}
			default:
				return nil, fmt.Errorf("srs: unknown sub-rule type %d", rt)
			}
		}
		var invert uint8
		if err := binary.Read(r, binary.BigEndian, &invert); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("srs: unknown logical rule mode %d", mode)
	}

	br := &byteReader{r: r}
	subCount, err := binary.ReadUvarint(br)
	if err != nil {
		return nil, err
	}

	// AND mode (0): intersection of CIDR sets across sub-rules is not
	// safely representable as a simple union. Skip all sub-rules and return empty.
	if mode == 0 {
		skipCfg := &ParseSRSConfig{} // CIDRs=false, skip extraction
		for i := uint64(0); i < subCount; i++ {
			rt, err := readByte(r)
			if err != nil {
				return nil, err
			}
			switch rt {
			case 0:
				if _, err := parseDefaultRule(ctx, r, skipCfg); err != nil {
					return nil, err
				}
			case 1:
				if _, err := parseLogicalRule(ctx, r, skipCfg, depth+1); err != nil {
					return nil, err
				}
			default:
				return nil, fmt.Errorf("srs: unknown sub-rule type %d", rt)
			}
		}
		var invert uint8
		if err := binary.Read(r, binary.BigEndian, &invert); err != nil {
			return nil, err
		}
		_ = invert // AND mode: cannot represent intersection, inverted or not
		return nil, nil
	}

	// OR mode (1): collect CIDRs from all sub-rules (union).
	var allCIDRs []string
	for i := uint64(0); i < subCount; i++ {
		rt, err := readByte(r)
		if err != nil {
			return nil, err
		}
		switch rt {
		case 0:
			cidrs, err := parseDefaultRule(ctx, r, cfg)
			if err != nil {
				return nil, err
			}
			allCIDRs = append(allCIDRs, cidrs...)
		case 1:
			cidrs, err := parseLogicalRule(ctx, r, cfg, depth+1)
			if err != nil {
				return nil, err
			}
			allCIDRs = append(allCIDRs, cidrs...)
		default:
			return nil, fmt.Errorf("srs: unknown sub-rule type %d", rt)
		}
	}
	var invert uint8
	if err := binary.Read(r, binary.BigEndian, &invert); err != nil {
		return nil, err
	}
	if invert != 0 {
		return nil, nil // skip inverted logical rules
	}
	return allCIDRs, nil
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
