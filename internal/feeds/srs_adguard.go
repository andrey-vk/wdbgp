package feeds

import (
	"encoding/binary"
	"fmt"
	"io"
)

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
