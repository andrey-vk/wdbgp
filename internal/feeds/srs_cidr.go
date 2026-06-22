package feeds

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/netip"

	"go4.org/netipx"
)

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
		result, err = ib.IPSet()
		if err != nil {
			return nil
		}
	}

	prefixes := result.Prefixes()
	out := make([]string, len(prefixes))
	for i, p := range prefixes {
		out[i] = p.String()
	}
	return out
}
