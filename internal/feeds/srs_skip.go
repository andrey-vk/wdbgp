package feeds

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
)

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
		if err := skipBytes(r, int64(fl)); err != nil { //nolint:gosec // binary.ReadUvarint returns small values
			return fmt.Errorf("ipset from: %w", err)
		}
		tl, err := binary.ReadUvarint(br)
		if err != nil {
			return fmt.Errorf("ipset toLen: %w", err)
		}
		if err := skipBytes(r, int64(tl)); err != nil { //nolint:gosec // binary.ReadUvarint returns small values
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
		if err := skipBytes(r, int64(slen)); err != nil { //nolint:gosec // binary.ReadUvarint returns small values
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
