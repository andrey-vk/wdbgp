package store

import (
	"fmt"
	"net/netip"
)

// The refactored schema stores IP data in binary form: an address is a
// 4- or 16-byte BLOB (family implicit in the length), a prefix is such a
// BLOB plus an integer bit count. Prefixes are always stored masked so
// that (ip, bits) is canonical and UNIQUE constraints deduplicate
// correctly.

// EncodeAddr converts an address to its stored BLOB form.
func EncodeAddr(addr netip.Addr) []byte {
	return addr.AsSlice()
}

// DecodeAddr converts a stored BLOB back to an address.
func DecodeAddr(ip []byte) (netip.Addr, error) {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return netip.Addr{}, fmt.Errorf("invalid stored address: %d bytes", len(ip))
	}
	return addr, nil
}

// EncodePrefix converts a prefix to its stored (ip, bits) form, masking it
// so the encoding is canonical.
func EncodePrefix(prefix netip.Prefix) (ip []byte, bits int) {
	masked := prefix.Masked()
	return masked.Addr().AsSlice(), masked.Bits()
}

// DecodePrefix converts a stored (ip, bits) pair back to a prefix.
func DecodePrefix(ip []byte, bits int) (netip.Prefix, error) {
	addr, err := DecodeAddr(ip)
	if err != nil {
		return netip.Prefix{}, err
	}
	prefix := netip.PrefixFrom(addr, bits)
	if !prefix.IsValid() {
		return netip.Prefix{}, fmt.Errorf("invalid stored prefix: /%d for %d-byte address", bits, len(ip))
	}
	return prefix, nil
}

// EncodeAddrString parses and encodes a textual address in one step, for
// use as a query argument against BLOB address columns.
func EncodeAddrString(value string) ([]byte, error) {
	addr, err := netip.ParseAddr(value)
	if err != nil {
		return nil, err
	}
	return addr.AsSlice(), nil
}

// EncodePrefixString parses and encodes a textual CIDR in one step.
func EncodePrefixString(cidr string) (ip []byte, bits int, err error) {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		return nil, 0, err
	}
	ip, bits = EncodePrefix(prefix)
	return ip, bits, nil
}
