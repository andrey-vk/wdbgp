package feeds

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"net/netip"
	"os"
	"testing"
)

// TestParseSRS_GeoIP_RU tests against a real SRS v1 file downloaded to runtime/.
// The runtime/ directory is gitignored; this test is skipped in CI.
// For CI coverage, see TestJavaScriptAdapterSRSGet in feeds_test.go.
func TestParseSRS_GeoIP_RU(t *testing.T) {
	data, err := os.ReadFile("../../runtime/geoip-ru.srs")
	if err != nil {
		t.Skipf("test file not found: %v", err)
	}

	entries, err := ParseSRS(context.Background(), data, `{"cidrs":true}`, 0)
	if err != nil {
		t.Fatalf("ParseSRS: %v", err)
	}

	if len(entries) == 0 {
		t.Fatal("expected at least one entry")
	}

	total := 0
	for _, e := range entries {
		total += len(e.CIDRs)
		for _, c := range e.CIDRs {
			if _, err := netip.ParsePrefix(c); err != nil {
				t.Errorf("invalid CIDR: %s: %v", c, err)
			}
		}
	}
	t.Logf("entries: %d, total CIDRs: %d", len(entries), total)
}

func TestParseSRS_EmptyConfigDefaultsCIDRs(t *testing.T) {
	// Build a minimal SRS v1 binary with one CIDR and verify
	// ParseSRS(data, "") extracts CIDRs (default behavior).

	var srsBuf bytes.Buffer
	srsBuf.Write([]byte{0x53, 0x52, 0x53, 0x01}) // SRS v1

	zw, _ := zlib.NewWriterLevel(&srsBuf, zlib.BestCompression)
	bw := bufio.NewWriter(zw)

	// rule_count = 1
	uvbuf := make([]byte, binary.MaxVarintLen64)
	n := binary.PutUvarint(uvbuf, 1)
	bw.Write(uvbuf[:n])

	// rule_type = 0
	bw.WriteByte(0)

	// item_type = 6 (ip_cidr)
	bw.WriteByte(6)

	// IPSet: version=1, range_count=1
	bw.WriteByte(1)
	binary.Write(bw, binary.BigEndian, uint64(1))

	// Range: 10.0.0.0 -> 10.255.255.255
	from := netip.MustParseAddr("10.0.0.0").AsSlice()
	to := netip.MustParseAddr("10.255.255.255").AsSlice()
	n1 := binary.PutUvarint(uvbuf, uint64(len(from)))
	bw.Write(uvbuf[:n1])
	bw.Write(from)
	n1 = binary.PutUvarint(uvbuf, uint64(len(to)))
	bw.Write(uvbuf[:n1])
	bw.Write(to)

	// final
	bw.WriteByte(0xFF)
	bw.WriteByte(0) // invert

	bw.Flush()
	zw.Close()

	entries, err := ParseSRS(context.Background(), srsBuf.Bytes(), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 || len(entries[0].CIDRs) == 0 {
		t.Fatal("expected CIDRs with default (empty) config")
	}
	if entries[0].CIDRs[0] != "10.0.0.0/8" {
		t.Errorf("expected 10.0.0.0/8, got %s", entries[0].CIDRs[0])
	}
}
