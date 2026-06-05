package prefixfilter

import (
	"net/netip"
	"testing"
)

func TestApplySubtractsHostFromWidePrefix(t *testing.T) {
	got, err := Apply(prefixes("1.0.0.0/8"), Lists{
		Deny: prefixes("1.1.1.1/32"),
	}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 24 {
		t.Fatalf("fragment count = %d, want 24: %v", len(got), got)
	}
	for _, prefix := range got {
		if prefix.Contains(netip.MustParseAddr("1.1.1.1")) {
			t.Fatalf("denied address remains covered by %s", prefix)
		}
	}
	for _, address := range []string{"1.0.0.1", "1.1.1.0", "1.1.1.2", "1.255.255.254"} {
		if !contains(got, netip.MustParseAddr(address)) {
			t.Fatalf("allowed address %s was removed: %v", address, got)
		}
	}
}

func TestApplyUsesAllowListAsRestriction(t *testing.T) {
	got, err := Apply(prefixes("10.0.0.0/8"), Lists{
		Allow: prefixes("10.20.0.0/16"),
		Deny:  prefixes("10.20.30.0/24"),
	}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if contains(got, netip.MustParseAddr("10.19.0.1")) {
		t.Fatalf("address outside allow list remains covered: %v", got)
	}
	if contains(got, netip.MustParseAddr("10.20.30.1")) {
		t.Fatalf("denied address remains covered: %v", got)
	}
	if !contains(got, netip.MustParseAddr("10.20.29.1")) {
		t.Fatalf("allowed address was removed: %v", got)
	}
}

func TestApplyCollapsesOverlappingAllowEntries(t *testing.T) {
	got, err := Apply(prefixes("1.0.0.0/8"), Lists{
		Allow: prefixes("1.0.0.0/8", "1.1.0.0/16"),
	}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].String() != "1.0.0.0/8" {
		t.Fatalf("overlapping allow entries produced %v", got)
	}
}

func TestApplySupportsIPv6(t *testing.T) {
	got, err := Apply(prefixes("2001:db8::/32"), Lists{
		Deny: prefixes("2001:db8::1/128"),
	}, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 96 {
		t.Fatalf("fragment count = %d, want 96", len(got))
	}
	if contains(got, netip.MustParseAddr("2001:db8::1")) {
		t.Fatal("denied IPv6 address remains covered")
	}
}

func TestApplyEnforcesExpansionLimit(t *testing.T) {
	_, err := Apply(prefixes("::/0"), Lists{Deny: prefixes("2001:db8::1/128")}, 64)
	if err == nil {
		t.Fatal("Apply accepted a result exceeding the prefix limit")
	}
}

func prefixes(values ...string) []netip.Prefix {
	result := make([]netip.Prefix, len(values))
	for index, value := range values {
		result[index] = netip.MustParsePrefix(value)
	}
	return result
}

func contains(prefixes []netip.Prefix, address netip.Addr) bool {
	for _, prefix := range prefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}
