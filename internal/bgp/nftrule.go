package bgp

import (
	"errors"
	"fmt"

	"github.com/google/nftables"
	"github.com/google/nftables/binaryutil"
	"github.com/google/nftables/expr"
	"golang.org/x/sys/unix"
)

// nftDynamicMD5TablePrefix names the nftables tables this process manages
// for dynamic-peer MD5 matching. Namespaced so they can be safely dropped
// and recreated without touching any rules the operator (or RouterOS
// itself) manages independently in the same network namespace.
const nftDynamicMD5TablePrefix = "wdbgp_dynamic_md5"

// nftDynamicMD5LegacyTable is the fixed table name used before the name
// became port/queue-scoped; cleaned up alongside the scoped name so an
// upgrade never leaves the old table's consumer-less rule behind.
const nftDynamicMD5LegacyTable = nftDynamicMD5TablePrefix

// dynamicMD5TableName scopes the table to this instance's BGP port and
// queue number, so two wdbgp instances sharing a network namespace (e.g.
// host networking) on different ports manage disjoint tables — with one
// shared name, either instance's cleanup deleted the other's live rule,
// silently disabling that instance's MD5 verification (fail-open).
// The cost: changing port/queue between runs strands the old name, which
// exact-name cleanup won't find (a prefix sweep would reintroduce the
// cross-instance deletion). That stale rule only affects the old,
// no-longer-used port, and startup logs the manual fix.
func dynamicMD5TableName(bgpPort, queueNum uint16) string {
	return fmt.Sprintf("%s_p%d_q%d", nftDynamicMD5TablePrefix, bgpPort, queueNum)
}

// deleteDynamicMD5Table removes one table in its own netlink batch, so a
// missing table (ENOENT, the common case) can't abort a sibling deletion
// batched alongside it.
func deleteDynamicMD5Table(name string) error {
	conn, err := nftables.New()
	if err != nil {
		return fmt.Errorf("nftables: connect: %w", err)
	}
	conn.DelTable(&nftables.Table{Family: nftables.TableFamilyINet, Name: name})
	if err := conn.Flush(); err != nil && !errors.Is(err, unix.ENOENT) {
		return fmt.Errorf("nftables: delete table %s: %w", name, err)
	}
	return nil
}

// EnsureDynamicMD5NFQueueRule installs the nftables rule that redirects
// inbound BGP-port SYNs to the NFQUEUE consumed by dynamicMD5Queue (see
// nfqueue_md5.go), in this process's own network namespace. It talks
// directly to the kernel over netlink (via github.com/google/nftables) —
// no nft/iptables binary or shell needed, so the container image doesn't
// need to bundle one either.
//
// The rule has no bypass flag, so while it is installed every matched SYN
// with no queue consumer attached is dropped by the kernel — fail-closed.
// Speaker.Start/Stop pair the rule's lifetime with the consumer's for
// exactly that reason; never leave this rule behind without one.
//
// Idempotent: any previous incarnation of the table is dropped first, so
// calling this again (e.g. on speaker reload) never accumulates duplicate
// rules.
func EnsureDynamicMD5NFQueueRule(bgpPort, queueNum uint16) error {
	// Best-effort cleanup of a previous run's tables — this instance's own
	// scoped name and the pre-scoping legacy name (upgrade path). The
	// common error here is "no such table", which is expected.
	name := dynamicMD5TableName(bgpPort, queueNum)
	_ = deleteDynamicMD5Table(name)                     //nolint:errcheck // best-effort cleanup before recreate
	_ = deleteDynamicMD5Table(nftDynamicMD5LegacyTable) //nolint:errcheck // best-effort legacy cleanup

	conn, err := nftables.New()
	if err != nil {
		return fmt.Errorf("nftables: connect: %w", err)
	}
	table := conn.AddTable(&nftables.Table{
		Family: nftables.TableFamilyINet, // matches both IPv4 and IPv6
		Name:   name,
	})
	chain := conn.AddChain(&nftables.Chain{
		Name:     "input",
		Table:    table,
		Type:     nftables.ChainTypeFilter,
		Hooknum:  nftables.ChainHookInput,
		Priority: nftables.ChainPriorityFilter,
	})
	conn.AddRule(&nftables.Rule{
		Table: table,
		Chain: chain,
		Exprs: []expr.Any{
			// meta l4proto == tcp
			&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{unix.IPPROTO_TCP}},
			// tcp dport == bgpPort
			&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: 2, Len: 2},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: binaryutil.BigEndian.PutUint16(bgpPort)},
			// tcp flags & (SYN|ACK) == SYN — initial connection attempts
			// only; an established flow's later segments skip the queue.
			&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: 13, Len: 1},
			&expr.Bitwise{SourceRegister: 1, DestRegister: 1, Len: 1, Mask: []byte{tcpFlagSYN | tcpFlagACK}, Xor: []byte{0}},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{tcpFlagSYN}},
			&expr.Queue{Num: queueNum},
		},
	})

	if err := conn.Flush(); err != nil {
		return fmt.Errorf("nftables: flush: %w", err)
	}
	return nil
}

// RemoveDynamicMD5NFQueueRule drops this instance's dynamic-MD5 nftables
// table (and with it the NFQUEUE redirect rule), plus the pre-scoping
// legacy table. A missing table is not an error — the common case is
// cleaning up when the feature is disabled and nothing was ever installed.
// In a host network namespace the table survives process death, so this
// must run whenever the queue consumer goes away: a leftover no-consumer
// rule black-holes every inbound SYN on its port.
func RemoveDynamicMD5NFQueueRule(bgpPort, queueNum uint16) error {
	return errors.Join(
		deleteDynamicMD5Table(dynamicMD5TableName(bgpPort, queueNum)),
		deleteDynamicMD5Table(nftDynamicMD5LegacyTable),
	)
}
