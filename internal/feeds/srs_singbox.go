package feeds

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/netip"

	"go4.org/netipx"
)

// parseDefaultRule loops items until srsItemFinal.
func parseDefaultRule(ctx context.Context, r io.Reader, cfg *ParseSRSConfig) ([]string, bool, error) {
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
		cidrs, err := readIPSetAsCIDRs(ctx, r)
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
			// Domain matchers use a compact binary format. Domain is OR'd
			// with CIDRs, not AND'd — just skip the payload and continue.
			if err := skipDomainMatcher(r); err != nil {
				if err := skipRemainingItemsForConstraint(r); err != nil {
					return nil, false, err
				}
				return nil, false, nil
			}
		case srsItemDomainKeyword, srsItemDomainRegex:
			// DomainKeyword/DomainRegex are OR'd with CIDRs like srsItemDomain,
			// not AND'd — skip the payload without setting a constraint.
			if err := skipStringArray(ctx, r); err != nil {
				return nil, false, err
			}
		case srsItemAdGuardDomain:
			// Same as domain — compact binary format. Domain is OR'd
			// with CIDRs — just skip the payload and continue.
			if err := skipAdGuardMatcher(r); err != nil {
				if err := skipRemainingItemsForConstraint(r); err != nil {
					return nil, false, err
				}
				return nil, false, nil
			}
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
		if fromAddr.Compare(toAddr) > 0 {
			return nil, fmt.Errorf("srs: invalid IP range: %s > %s", fromAddr.String(), toAddr.String())
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

// skipRemainingItemsForConstraint consumes all items in a default rule until srsItemFinal.
// Used when we encounter an unparseable item (like domain matchers) and need
// to fast-forward through the rest of the rule without corrupting the stream.
func skipRemainingItemsForConstraint(r io.Reader) error {
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

func parseLogicalRule(ctx context.Context, r io.Reader, cfg *ParseSRSConfig, depth int) ([]string, bool, error) {
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
				if _, _, err := parseDefaultRule(ctx, r, skipCfg); err != nil {
					return nil, false, err
				}
			case 1:
				if _, _, err := parseLogicalRule(ctx, r, skipCfg, depth+1); err != nil {
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
				cidrs, hasConstraint, err := parseDefaultRule(ctx, r, cfg)
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
							if _, _, err := parseDefaultRule(ctx, r, skipCfg); err != nil {
								return nil, false, err
							}
						case 1:
							if _, _, err := parseLogicalRule(ctx, r, skipCfg, depth+1); err != nil {
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
				cidrs, hasConstraint, err := parseLogicalRule(ctx, r, cfg, depth+1)
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
							if _, _, err := parseDefaultRule(ctx, r, skipCfg); err != nil {
								return nil, false, err
							}
						case 1:
							if _, _, err := parseLogicalRule(ctx, r, skipCfg, depth+1); err != nil {
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

	// OR mode (1): collect CIDRs from unconstrained sub-rules (union).
	// OR is constrained only if ALL sub-rules are constrained.
	var allCIDRs []string
	allConstrained := true
	for i := uint64(0); i < subCount; i++ {
		rt, err := readByte(r)
		if err != nil {
			return nil, false, err
		}
		switch rt {
		case 0:
			cidrs, subConstraint, err := parseDefaultRule(ctx, r, cfg)
			if err != nil {
				return nil, false, err
			}
			if !subConstraint {
				allCIDRs = append(allCIDRs, cidrs...)
				allConstrained = false
			}
		case 1:
			cidrs, subConstraint, err := parseLogicalRule(ctx, r, cfg, depth+1)
			if err != nil {
				return nil, false, err
			}
			if !subConstraint {
				allCIDRs = append(allCIDRs, cidrs...)
				allConstrained = false
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
	return allCIDRs, allConstrained, nil
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
