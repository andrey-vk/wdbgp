package bgp

import (
	"fmt"

	"github.com/google/nftables"
	"github.com/google/nftables/binaryutil"
	"github.com/google/nftables/expr"
	"golang.org/x/sys/unix"
)

// nftDynamicMD5Table is the name of the nftables table this process manages
// for dynamic-peer MD5 matching. Namespaced so it can be safely dropped and
// recreated without touching any rules the operator (or RouterOS itself)
// manages independently in the same network namespace.
const nftDynamicMD5Table = "wdbgp_dynamic_md5"

// EnsureDynamicMD5NFQueueRule installs the nftables rule that redirects
// inbound BGP-port SYNs to the NFQUEUE consumed by dynamicMD5Queue (see
// nfqueue_md5.go), in this process's own network namespace. It talks
// directly to the kernel over netlink (via github.com/google/nftables) —
// no nft/iptables binary or shell needed, so the container image doesn't
// need to bundle one either.
//
// Idempotent: any previous incarnation of the table is dropped first, so
// calling this again (e.g. on process restart) never accumulates duplicate
// rules. Call once at process startup, not on every settings reload.
func EnsureDynamicMD5NFQueueRule(bgpPort, queueNum uint16) error {
	conn, err := nftables.New()
	if err != nil {
		return fmt.Errorf("nftables: connect: %w", err)
	}

	// Best-effort cleanup of a previous run's table. The common error here
	// is "no such file" on the very first startup, which is expected.
	conn.DelTable(&nftables.Table{Family: nftables.TableFamilyINet, Name: nftDynamicMD5Table})
	_ = conn.Flush() //nolint:errcheck // best-effort cleanup, "no such table" on first-ever startup is expected

	table := conn.AddTable(&nftables.Table{
		Family: nftables.TableFamilyINet, // matches both IPv4 and IPv6
		Name:   nftDynamicMD5Table,
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
