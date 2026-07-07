package bgp

import "testing"

// The table name must be scoped per instance (port+queue): with a single
// shared name, two wdbgp instances in one network namespace deleted each
// other's live redirect rule, silently disabling the other instance's MD5
// verification.
func TestDynamicMD5TableNameScopedPerInstance(t *testing.T) {
	if got, want := dynamicMD5TableName(179, 0), "wdbgp_dynamic_md5_p179_q0"; got != want {
		t.Fatalf("dynamicMD5TableName(179, 0) = %q, want %q", got, want)
	}
	if dynamicMD5TableName(179, 0) == dynamicMD5TableName(1790, 0) ||
		dynamicMD5TableName(179, 0) == dynamicMD5TableName(179, 1) {
		t.Fatal("different port/queue configs must map to different table names")
	}
	// The scoped name must never collide with the legacy fixed name that
	// upgrade cleanup deletes unconditionally.
	if dynamicMD5TableName(179, 0) == nftDynamicMD5LegacyTable {
		t.Fatal("scoped name collides with the legacy table name")
	}
}
