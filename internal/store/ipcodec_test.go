package store

import (
	"bytes"
	"net/netip"
	"testing"
)

func TestEncodeDecodePrefixRoundTrip(t *testing.T) {
	for _, raw := range []string{
		"10.0.0.0/8",
		"192.168.1.0/24",
		"0.0.0.0/0",
		"203.0.113.7/32",
		"2001:db8::/32",
		"::/0",
		"2001:db8::1/128",
	} {
		prefix := netip.MustParsePrefix(raw)
		ip, bits := EncodePrefix(prefix)
		decoded, err := DecodePrefix(ip, bits)
		if err != nil {
			t.Fatalf("DecodePrefix(%s): %v", raw, err)
		}
		if decoded != prefix.Masked() {
			t.Errorf("round trip %s: got %s", raw, decoded)
		}
		wantLen := 4
		if prefix.Addr().Is6() {
			wantLen = 16
		}
		if len(ip) != wantLen {
			t.Errorf("%s: encoded to %d bytes, want %d", raw, len(ip), wantLen)
		}
	}
}

func TestEncodePrefixMasksHostBits(t *testing.T) {
	ip, bits := EncodePrefix(netip.MustParsePrefix("192.168.1.99/24"))
	maskedIP, maskedBits := EncodePrefix(netip.MustParsePrefix("192.168.1.0/24"))
	if !bytes.Equal(ip, maskedIP) || bits != maskedBits {
		t.Errorf("host bits not masked: got %v/%d, want %v/%d", ip, bits, maskedIP, maskedBits)
	}
}

func TestDecodePrefixRejectsInvalid(t *testing.T) {
	for _, tc := range []struct {
		name string
		ip   []byte
		bits int
	}{
		{"empty blob", nil, 24},
		{"wrong length", []byte{10, 0, 0}, 8},
		{"bits too large v4", []byte{10, 0, 0, 0}, 33},
		{"negative bits", []byte{10, 0, 0, 0}, -1},
		{"bits too large v6", bytes.Repeat([]byte{0}, 16), 129},
	} {
		if _, err := DecodePrefix(tc.ip, tc.bits); err == nil {
			t.Errorf("%s: expected error, got none", tc.name)
		}
	}
}

func TestEncodeDecodeAddrRoundTrip(t *testing.T) {
	for _, raw := range []string{"192.0.2.1", "2001:db8::1"} {
		addr := netip.MustParseAddr(raw)
		decoded, err := DecodeAddr(EncodeAddr(addr))
		if err != nil {
			t.Fatalf("DecodeAddr(%s): %v", raw, err)
		}
		if decoded != addr {
			t.Errorf("round trip %s: got %s", raw, decoded)
		}
	}
}

func TestParsePrefixOrAddr(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
	}{
		{"10.0.0.0/8", "10.0.0.0/8"},
		{"192.168.1.99/24", "192.168.1.99/24"},
		{"1.2.3.4", "1.2.3.4/32"},
		{"2001:db8::/32", "2001:db8::/32"},
		{"fd00::1", "fd00::1/128"},
	} {
		got, err := ParsePrefixOrAddr(tc.input)
		if err != nil {
			t.Errorf("ParsePrefixOrAddr(%q): %v", tc.input, err)
			continue
		}
		if got != netip.MustParsePrefix(tc.want) {
			t.Errorf("ParsePrefixOrAddr(%q) = %s, want %s", tc.input, got, tc.want)
		}
	}
	for _, input := range []string{"", "not-a-cidr", "10.0.0.0/33", "1.2.3.4/", "fd00::1/129", "1.2.3", "1.2.3.4 /32"} {
		if _, err := ParsePrefixOrAddr(input); err == nil {
			t.Errorf("ParsePrefixOrAddr(%q): expected error, got none", input)
		}
	}
}

func TestEncodePrefixString(t *testing.T) {
	ip, bits, err := EncodePrefixString("10.1.2.3/16")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ip, []byte{10, 1, 0, 0}) || bits != 16 {
		t.Errorf("got %v/%d, want [10 1 0 0]/16", ip, bits)
	}
	if _, _, err := EncodePrefixString("not-a-cidr"); err == nil {
		t.Error("expected error for invalid CIDR")
	}
}
