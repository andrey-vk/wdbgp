package feeds

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
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
// The 64 MiB decompressed input cap (maxDecompressedSRS) is the only size limit.
func ParseSRS(ctx context.Context, data []byte, cfgJSON string) ([]canonicalEntry, error) {
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
	defer func() {
		if err := zr.Close(); err != nil {
			log.Printf("DEBUG: close zlib reader: %v", err)
		}
	}()
	cr := &countReader{r: zr, limit: maxDecompressedSRS}

	ruleCount, err := binary.ReadUvarint(&byteReader{r: cr})
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
		ruleType, err := readByte(cr)
		if err != nil {
			return nil, fmt.Errorf("srs: rule[%d] type: %w", i, err)
		}
		switch ruleType {
		case 0: // default rule
			cidrs, _, err := parseDefaultRule(ctx, cr, &cfg)
			if err != nil {
				return nil, fmt.Errorf("srs: rule[%d]: %w", i, err)
			}
			if len(cidrs) > 0 {
				entries = append(entries, canonicalEntry{CIDRs: cidrs})
			}
		case 1: // logical rule
			cidrs, _, err := parseLogicalRule(ctx, cr, &cfg, 0)
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

	// Drain remaining zlib stream to validate checksum and detect truncation
	if _, err := io.Copy(io.Discard, cr); err != nil {
		return nil, fmt.Errorf("srs: zlib stream error: %w", err)
	}

	return entries, nil
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

func skipBytes(r io.Reader, n int64) error {
	_, err := io.CopyN(io.Discard, r, n)
	return err
}
