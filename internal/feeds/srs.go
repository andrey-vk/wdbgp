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
// maxEntries limits the total number of CIDRs; 0 means no limit.
// Limit is enforced in two stages:
//  1. Early range-count check: if the IPSet range count exceeds maxEntries,
//     parsing aborts before building the IPSet, avoiding the largest allocations.
//  2. Post-expansion check: after Prefixes() materialises all prefixes, a final
//     cap is applied. This catches edge cases where few ranges explode into many
//     prefixes. Full streaming would require a streaming Prefixes() method on
//     netipx.IPSet which does not exist; as a practical bound, the 64 MiB
//     decompressed input limit keeps worst-case memory under control.
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
	cr := &countReader{r: zr, limit: maxDecompressedSRS}

	ruleCount, err := binary.ReadUvarint(&byteReader{r: cr})
	if err != nil {
		return nil, fmt.Errorf("srs: read rule count: %w", err)
	}

	var entries []canonicalEntry
	var totalCIDRs int

	for i := uint64(0); i < ruleCount; i++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		ruleType, err := readByte(cr)
		if err != nil {
			return nil, fmt.Errorf("srs: rule[%d] type: %w", i, err)
		}
		switch ruleType {
		case 0: // default rule
			cidrs, _, err := parseDefaultRule(ctx, cr, &cfg, maxEntries)
			if err != nil {
				return nil, fmt.Errorf("srs: rule[%d]: %w", i, err)
			}
			if len(cidrs) > 0 {
				entries = append(entries, canonicalEntry{CIDRs: cidrs})
				totalCIDRs += len(cidrs)
			}
		case 1: // logical rule
			cidrs, _, err := parseLogicalRule(ctx, cr, &cfg, 0, maxEntries)
			if err != nil {
				return nil, fmt.Errorf("srs: rule[%d] logical: %w", i, err)
			}
			if len(cidrs) > 0 {
				entries = append(entries, canonicalEntry{CIDRs: cidrs})
				totalCIDRs += len(cidrs)
			}
		default:
			return nil, fmt.Errorf("srs: rule[%d] unknown type %d", i, ruleType)
		}
		// Abort early if total CIDRs across all rules exceeds the limit.
		if maxEntries > 0 && totalCIDRs > maxEntries {
			return nil, fmt.Errorf("srs: %d CIDRs exceeds limit of %d", totalCIDRs, maxEntries)
		}
	}

	// Drain remaining zlib stream to validate checksum and detect truncation
	if _, err := io.Copy(io.Discard, cr); err != nil {
		return nil, fmt.Errorf("srs: zlib stream error: %w", err)
	}

	return entries, nil
}

// parseDefaultRule loops items until srsItemFinal.
func parseDefaultRule(ctx context.Context, r io.Reader, cfg *ParseSRSConfig, maxEntries int) ([]string, bool, error) {
	var allCIDRs []string
	var hasConstraint bool
	for {
		select {
		case <-ctx.Done():
			return nil, false, ctx.Err()
		default:
		}
		itemType, err := readByte(r)
		if err != nil {
			return nil, false, err
		}
		switch itemType {
		case srsItemIPCIDR:
			if !cfg.CIDRs {
		if err := skipIPSet(ctx, r); err != nil {
				return nil, false, err
			}
			continue
		}
		cidrs, err := readIPSetAsCIDRs(ctx, r, maxEntries)
			if err != nil {
				return nil, false, err
			}
			allCIDRs = append(allCIDRs, cidrs...)
		case srsItemSourceIPCIDR:
			if err := skipIPSet(ctx, r); err != nil {
				return nil, false, err
			}
			hasConstraint = true
		case srsItemDomain:
			// Domain matchers use a compact binary format. Skip the
			// matcher payload, mark the rule constrained and fast-forward
			// to the end to keep the stream clean.
			if err := skipDomainMatcher(r); err != nil {
				return nil, false, err
			}
			hasConstraint = true
			if err := skipRemainingItems(r); err != nil {
				return nil, false, err
			}
			return nil, false, nil
		case srsItemDomainKeyword, srsItemDomainRegex:
			if err := skipStringArray(ctx, r); err != nil {
				return nil, false, err
			}
			hasConstraint = true
		case srsItemAdGuardDomain:
			// Same as domain — compact binary format. Skip matcher payload,
			// mark constrained, and fast-forward.
			if err := skipAdGuardMatcher(r); err != nil {
				return nil, false, err
			}
			hasConstraint = true
			if err := skipRemainingItems(r); err != nil {
				return nil, false, err
			}
			return nil, false, nil
		case srsItemQueryType, srsItemSourcePort, srsItemPort:
			if err := skipUint16Array(r); err != nil {
				return nil, false, err
			}
			hasConstraint = true
		case srsItemNetwork,
			srsItemSourcePortRange, srsItemPortRange,
			srsItemProcessName, srsItemProcessPath, srsItemProcessPathRegex,
			srsItemPackageName, srsItemPackageNameRegex, srsItemWIFISSID, srsItemWIFIBSSID:
			if err := skipStringArray(ctx, r); err != nil {
				return nil, false, err
			}
			hasConstraint = true
		case srsItemNetworkType:
			if err := skipUint8Array(r); err != nil {
				return nil, false, err
			}
			hasConstraint = true
		case srsItemNetworkIsExpensive, srsItemNetworkIsConstrained:
			// no data, just the type byte
			hasConstraint = true
		case srsItemNetworkInterfaceAddress:
			if err := skipNetworkInterfaceAddress(ctx, r); err != nil {
				return nil, false, err
			}
			hasConstraint = true
		case srsItemDefaultInterfaceAddress:
			if err := skipPrefixArray(ctx, r); err != nil {
				return nil, false, err
			}
			hasConstraint = true
		case srsItemFinal:
			var invert uint8
			if err := binary.Read(r, binary.BigEndian, &invert); err != nil {
				return nil, false, err
			}
			if invert != 0 || hasConstraint {
				return nil, hasConstraint, nil // skip inverted or constrained rules
			}
			return allCIDRs, false, nil
		default:
			return nil, false, fmt.Errorf("unknown item type %d", itemType)
		}
	}
}

// readIPSetAsCIDRs reads an IP set from an SRS rule and returns CIDR strings.
// maxEntries enforces a limit in three stages:
//  1. Range-count abort: if the raw range count exceeds maxEntries, abort immediately.
//  2. Conservative estimate: if count*30 > maxEntries, abort before building the IPSet.
//  3. Post-expansion check: after Prefixes() materialises all prefixes, abort if the
//     actual count exceeds maxEntries (before allocating the string slice).
func readIPSetAsCIDRs(ctx context.Context, r io.Reader, maxEntries int) ([]string, error) {
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
	if maxEntries > 0 && count > uint64(maxEntries) {
		return nil, fmt.Errorf("srs: %d IP ranges exceeds limit of %d", count, maxEntries)
	}
	if count > maxIPSetRangeCount {
		return nil, fmt.Errorf("ipset: count %d exceeds max %d", count, maxIPSetRangeCount)
	}
	// Conservative overflow estimate: each IP range can produce at most ~30
	// prefixes (worst-case for a non-aligned IPv4 range). If the estimate
	// exceeds the limit, the actual expansion almost certainly does too.
	// Abort early to avoid building and materializing an oversized IPSet.
	const maxPrefixesPerRange = 30
	if maxEntries > 0 && int(count)*maxPrefixesPerRange > maxEntries {
		return nil, fmt.Errorf("srs: %d IP ranges likely expand to >%d prefixes (limit %d)", count, maxEntries, maxEntries)
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
	// All prefixes are materialised at this point. A true streaming limit would
	// require a streaming Prefixes() from netipx.IPSet which isn't available.
	//
	// Three-stage limit enforcement bounds worst-case allocations:
	//  1. Range-count early abort (above) — skips entire loop.
	//  2. Conservative estimate (above) — skips IPSet build for likely overflow.
	//  3. Exact post-expansion check (here) — catches edge cases that slip
	//     through the estimate, while still aborting before the string slice
	//     allocation below.
	prefixes := ipSet.Prefixes()
	if maxEntries > 0 && len(prefixes) > maxEntries {
		return nil, fmt.Errorf("srs: %d expanded prefixes exceeds limit of %d", len(prefixes), maxEntries)
	}
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
	version, err := readByte(r)
	if err != nil {
		return err
	}
	br := &byteReader{r: r}

	// Domain matcher uses sing-box's succinct-set binary format:
	//   version byte + two arrays of big-endian uint64 values + one byte array.
	// Each array is length-prefixed with a uvarint count.
	// The version byte is read above; version is unused here (only for skipping).

	// First uint64 array.
	count, err := binary.ReadUvarint(br)
	if err != nil {
		return fmt.Errorf("domain matcher: read first array count: %w", err)
	}
	if count > maxIPSetRangeCount {
		return fmt.Errorf("domain matcher: first array count %d exceeds limit", count)
	}
	if err := skipBytes(r, int64(count*8)); err != nil {
		return fmt.Errorf("domain matcher: skip first array: %w", err)
	}

	// Second uint64 array.
	count, err = binary.ReadUvarint(br)
	if err != nil {
		return fmt.Errorf("domain matcher: read second array count: %w", err)
	}
	if count > maxIPSetRangeCount {
		return fmt.Errorf("domain matcher: second array count %d exceeds limit", count)
	}
	if err := skipBytes(r, int64(count*8)); err != nil {
		return fmt.Errorf("domain matcher: skip second array: %w", err)
	}

	// Byte array (present in all versions).
	_ = version
	byteLen, err := binary.ReadUvarint(br)
	if err != nil {
		return fmt.Errorf("domain matcher: read byte array length: %w", err)
	}
	if byteLen > uint64(maxDecompressedSRS) {
		return fmt.Errorf("domain matcher: byte array length %d exceeds limit", byteLen)
	}
	if err := skipBytes(r, int64(byteLen)); err != nil {
		return fmt.Errorf("domain matcher: skip byte array: %w", err)
	}

	return nil
}

func skipAdGuardMatcher(r io.Reader) error {
	version, err := readByte(r)
	if err != nil {
		return err
	}
	br := &byteReader{r: r}

	// Same succinct-set binary format as the domain matcher:
	//   version byte + two arrays of big-endian uint64 values + one byte array.
	// Each array is length-prefixed with a uvarint count.

	// First uint64 array.
	count, err := binary.ReadUvarint(br)
	if err != nil {
		return fmt.Errorf("adguard matcher: read first array count: %w", err)
	}
	if count > maxIPSetRangeCount {
		return fmt.Errorf("adguard matcher: first array count %d exceeds limit", count)
	}
	if err := skipBytes(r, int64(count*8)); err != nil {
		return fmt.Errorf("adguard matcher: skip first array: %w", err)
	}

	// Second uint64 array.
	count, err = binary.ReadUvarint(br)
	if err != nil {
		return fmt.Errorf("adguard matcher: read second array count: %w", err)
	}
	if count > maxIPSetRangeCount {
		return fmt.Errorf("adguard matcher: second array count %d exceeds limit", count)
	}
	if err := skipBytes(r, int64(count*8)); err != nil {
		return fmt.Errorf("adguard matcher: skip second array: %w", err)
	}

	// Byte array.
	_ = version
	byteLen, err := binary.ReadUvarint(br)
	if err != nil {
		return fmt.Errorf("adguard matcher: read byte array length: %w", err)
	}
	if byteLen > uint64(maxDecompressedSRS) {
		return fmt.Errorf("adguard matcher: byte array length %d exceeds limit", byteLen)
	}
	if err := skipBytes(r, int64(byteLen)); err != nil {
		return fmt.Errorf("adguard matcher: skip byte array: %w", err)
	}

	return nil
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

func skipNetworkInterfaceAddress(ctx context.Context, r io.Reader) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
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
				return fmt.Errorf("network interface: invalid addr length %d", addrLen)
			}
			if err := skipBytes(r, int64(addrLen+1)); err != nil {
				return err
			} // addr + prefix byte
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
	return nil
}

// skipRemainingItems consumes all items in a default rule until srsItemFinal.
// Used when we encounter an unparseable item (like domain matchers) and need
// to fast-forward through the rest of the rule without corrupting the stream.
func skipRemainingItems(r io.Reader) error {
	for {
		itemType, err := readByte(r)
		if err != nil {
			return err
		}
		if itemType == srsItemFinal {
			var invert uint8
			return binary.Read(r, binary.BigEndian, &invert)
		}
		if err := skipItemPayload(r, itemType); err != nil {
			return err
		}
	}
}

// skipItemPayload skips the payload of a single SRS item by type.
func skipItemPayload(r io.Reader, itemType uint8) error {
	switch itemType {
	case srsItemIPCIDR, srsItemSourceIPCIDR:
		return skipIPSet(context.Background(), r)
	case srsItemDomain:
		return skipDomainMatcher(r)
	case srsItemAdGuardDomain:
		return skipAdGuardMatcher(r)
	case srsItemQueryType, srsItemSourcePort, srsItemPort:
		return skipUint16Array(r)
	case srsItemNetwork, srsItemDomainKeyword, srsItemDomainRegex,
		srsItemSourcePortRange, srsItemPortRange,
		srsItemProcessName, srsItemProcessPath, srsItemProcessPathRegex,
		srsItemPackageName, srsItemPackageNameRegex,
		srsItemWIFISSID, srsItemWIFIBSSID:
		return skipStringArray(context.Background(), r)
	case srsItemNetworkType:
		return skipUint8Array(r)
	case srsItemNetworkIsExpensive, srsItemNetworkIsConstrained:
		return nil // no data
	case srsItemNetworkInterfaceAddress:
		return skipNetworkInterfaceAddress(context.Background(), r)
	case srsItemDefaultInterfaceAddress:
		return skipPrefixArray(context.Background(), r)
	default:
		return fmt.Errorf("srs: unknown item type %d in skipItemPayload", itemType)
	}
}

func parseLogicalRule(ctx context.Context, r io.Reader, cfg *ParseSRSConfig, depth int, maxEntries int) ([]string, bool, error) {
	if depth > maxSRSRecursionDepth {
		return nil, false, fmt.Errorf("srs: logical rule recursion depth %d exceeds limit", depth)
	}
	mode, err := readByte(r)
	if err != nil {
		return nil, false, err
	}

	// Validate mode before reading sub-rules so we can skip them correctly.
	if mode != 0 && mode != 1 {
		br := &byteReader{r: r}
		subCount, err := binary.ReadUvarint(br)
		if err != nil {
			return nil, false, err
		}
		skipCfg := &ParseSRSConfig{}
		for i := uint64(0); i < subCount; i++ {
			rt, err := readByte(r)
			if err != nil {
				return nil, false, err
			}
			switch rt {
			case 0:
				if _, _, err := parseDefaultRule(ctx, r, skipCfg, 0); err != nil {
					return nil, false, err
				}
			case 1:
				if _, _, err := parseLogicalRule(ctx, r, skipCfg, depth+1, 0); err != nil {
					return nil, false, err
				}
			default:
				return nil, false, fmt.Errorf("srs: unknown sub-rule type %d", rt)
			}
		}
		var invert uint8
		if err := binary.Read(r, binary.BigEndian, &invert); err != nil {
			return nil, false, err
		}
		return nil, false, fmt.Errorf("srs: unknown logical rule mode %d", mode)
	}

	br := &byteReader{r: r}
	subCount, err := binary.ReadUvarint(br)
	if err != nil {
		return nil, false, err
	}

	// AND mode (0): compute intersection if all sub-rules are pure CIDR.
	if mode == 0 {
		var allGroups [][]string
		for i := uint64(0); i < subCount; i++ {
			rt, err := readByte(r)
			if err != nil {
				return nil, false, err
			}
			switch rt {
			case 0:
				cidrs, hasConstraint, err := parseDefaultRule(ctx, r, cfg, maxEntries)
				if err != nil {
					return nil, false, err
				}
				if hasConstraint || len(cidrs) == 0 {
					// Skip entire AND. Consume remaining sub-rules and invert byte.
					skipCfg := &ParseSRSConfig{}
					for j := i + 1; j < subCount; j++ {
						srt, skipErr := readByte(r)
						if skipErr != nil {
							return nil, false, skipErr
						}
						switch srt {
						case 0:
							if _, _, err := parseDefaultRule(ctx, r, skipCfg, 0); err != nil {
								return nil, false, err
							}
						case 1:
							if _, _, err := parseLogicalRule(ctx, r, skipCfg, depth+1, 0); err != nil {
								return nil, false, err
							}
						default:
							return nil, false, fmt.Errorf("srs: unknown sub-rule type %d", srt)
						}
					}
					var invert uint8
					if err := binary.Read(r, binary.BigEndian, &invert); err != nil {
						return nil, false, err
					}
					return nil, true, nil
				}
				allGroups = append(allGroups, cidrs)
			case 1:
				cidrs, hasConstraint, err := parseLogicalRule(ctx, r, cfg, depth+1, maxEntries)
				if err != nil {
					return nil, false, err
				}
				if hasConstraint || len(cidrs) == 0 {
					// Skip entire AND. Consume remaining sub-rules and invert byte.
					skipCfg := &ParseSRSConfig{}
					for j := i + 1; j < subCount; j++ {
						srt, skipErr := readByte(r)
						if skipErr != nil {
							return nil, false, skipErr
						}
						switch srt {
						case 0:
							if _, _, err := parseDefaultRule(ctx, r, skipCfg, 0); err != nil {
								return nil, false, err
							}
						case 1:
							if _, _, err := parseLogicalRule(ctx, r, skipCfg, depth+1, 0); err != nil {
								return nil, false, err
							}
						default:
							return nil, false, fmt.Errorf("srs: unknown sub-rule type %d", srt)
						}
					}
					var invert uint8
					if err := binary.Read(r, binary.BigEndian, &invert); err != nil {
						return nil, false, err
					}
					return nil, true, nil
				}
				allGroups = append(allGroups, cidrs)
			default:
				return nil, false, fmt.Errorf("srs: unknown sub-rule type %d", rt)
			}
		}
		var invert uint8
		if err := binary.Read(r, binary.BigEndian, &invert); err != nil {
			return nil, false, err
		}
		if invert != 0 {
			return nil, true, nil
		}
		if len(allGroups) == 0 {
			return nil, false, nil
		}
		result := intersectCIDRs(allGroups...)
		return result, false, nil
	}

	// OR mode (1): collect CIDRs from all sub-rules (union).
	var allCIDRs []string
	var hasConstraint bool
	for i := uint64(0); i < subCount; i++ {
		rt, err := readByte(r)
		if err != nil {
			return nil, false, err
		}
		switch rt {
		case 0:
			cidrs, subConstraint, err := parseDefaultRule(ctx, r, cfg, maxEntries)
			if err != nil {
				return nil, false, err
			}
			if maxEntries > 0 && len(allCIDRs)+len(cidrs) > maxEntries {
				return nil, false, fmt.Errorf("srs: %d CIDRs exceeds limit of %d", len(allCIDRs)+len(cidrs), maxEntries)
			}
			allCIDRs = append(allCIDRs, cidrs...)
			if subConstraint {
				hasConstraint = true
			}
		case 1:
			cidrs, subConstraint, err := parseLogicalRule(ctx, r, cfg, depth+1, maxEntries)
			if err != nil {
				return nil, false, err
			}
			if maxEntries > 0 && len(allCIDRs)+len(cidrs) > maxEntries {
				return nil, false, fmt.Errorf("srs: %d CIDRs exceeds limit of %d", len(allCIDRs)+len(cidrs), maxEntries)
			}
			allCIDRs = append(allCIDRs, cidrs...)
			if subConstraint {
				hasConstraint = true
			}
		default:
			return nil, false, fmt.Errorf("srs: unknown sub-rule type %d", rt)
		}
	}
	var invert uint8
	if err := binary.Read(r, binary.BigEndian, &invert); err != nil {
		return nil, false, err
	}
	if invert != 0 {
		return nil, true, nil // skip inverted logical rules
	}
	return allCIDRs, hasConstraint, nil
}

// intersectCIDRs computes the intersection of multiple CIDR groups.
// Each group is a list of CIDR prefixes. The result is the set of prefixes
// contained in all groups.
func intersectCIDRs(groups ...[]string) []string {
	if len(groups) == 0 {
		return nil
	}

	// Build first set.
	var result *netipx.IPSet
	{
		var builder netipx.IPSetBuilder
		for _, c := range groups[0] {
			p, err := netip.ParsePrefix(c)
			if err != nil {
				continue
			}
			builder.AddPrefix(p)
		}
		s, err := builder.IPSet()
		if err != nil {
			return nil
		}
		result = s
	}

	// Intersect with remaining groups.
	for i := 1; i < len(groups); i++ {
		var builder netipx.IPSetBuilder
		for _, c := range groups[i] {
			p, err := netip.ParsePrefix(c)
			if err != nil {
				continue
			}
			builder.AddPrefix(p)
		}
		s, err := builder.IPSet()
		if err != nil {
			return nil
		}
		var ib netipx.IPSetBuilder
		ib.AddSet(result)
		ib.Intersect(s)
		result, _ = ib.IPSet()
	}

	prefixes := result.Prefixes()
	out := make([]string, len(prefixes))
	for i, p := range prefixes {
		out[i] = p.String()
	}
	return out
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

// countReader wraps an io.Reader and tracks the total bytes read. It signals
// when the limit is exceeded via the exceeded field but continues reading to
// allow draining the underlying stream.
type countReader struct {
	r        io.Reader
	limit    int64
	n        int64
	exceeded bool
}

func (c *countReader) Read(p []byte) (int, error) {
	if c.exceeded {
		return 0, fmt.Errorf("decompressed size exceeded %d bytes", c.limit)
	}
	n, err := c.r.Read(p)
	c.n += int64(n)
	if c.n > c.limit {
		c.exceeded = true
		return n, fmt.Errorf("decompressed size exceeded %d bytes", c.limit)
	}
	return n, err
}
