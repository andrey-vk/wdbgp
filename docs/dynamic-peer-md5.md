# Dynamic Peer BGP MD5 Authentication

Dynamic peers (`peer_ip` = `0.0.0.0` or `::`) accept a BGP session from any
source address and are normally identified by ASN alone: the kernel's
`TCP_MD5SIG` needs a specific remote address configured ahead of time, which a
wildcard peer doesn't have, so no cryptographic check is possible at the
handshake level. `WDBGP_DYNAMIC_PEER_MD5_MATCH` closes that gap by
authenticating dynamic peers with a real TCP MD5 (RFC 2385) signature,
verified via an NFQUEUE packet interception before the connection is even
accepted.

This document covers running that feature when `wdbgp` is **not** managed by
the RouterOS container feature — bare metal, a VM, a systemd service, or a
plain (non-RouterOS) Docker host. If you're running inside a RouterOS
container, see the main [README](../README.md#mikrotik-outline) — the
mechanism is identical, but RouterOS's own container capability model needs a
short callout of its own, covered there.

## How it works

1. An NFQUEUE consumer intercepts inbound SYNs to the BGP port.
2. SYNs from already-known fixed-peer addresses pass through untouched —
   they're already authenticated by the kernel's own per-address
   `TCP_MD5SIG` key (set on the listener before RouterOS/kernel accepts the
   connection).
3. SYNs from any other source are bruteforce-matched: the RFC 2385 digest is
   recomputed for every configured dynamic-peer password and compared against
   the signature the SYN actually carries.
4. A match installs a real, address-specific `TCP_MD5SIG` key on the
   listener (so the kernel authenticates the rest of that session normally)
   and accepts the packet. No match — or no MD5 option at all — drops the
   SYN outright, before it ever reaches `wdbgp`'s own TCP listener.

This means an unauthenticated or mismatched dynamic-peer connection attempt
never shows up as a rejected BGP session in the logs — it's dropped at the
packet level. See [Troubleshooting](#troubleshooting) below for how to check
what's actually happening.

## Requirements

- **Linux kernel with `nfnetlink_queue` support.** Present on effectively
  every modern distribution kernel, but it may be a loadable module
  (`nf_tables`, `nfnetlink_queue`) that isn't loaded yet on a freshly booted
  host. See the first-activation note under Troubleshooting.
- **`CAP_NET_ADMIN`** for the `wdbgp` process — it manages its own nftables
  table and binds an NFQUEUE consumer directly over netlink
  (`github.com/google/nftables` / `github.com/florianl/go-nfqueue`). No
  `nft`/`iptables` binary or shell is required in the image or on the host —
  this is pure Go, no external tool is ever exec'd.
- **x86 or ARM64.** Confirmed working on RouterOS 7.21+ containers on these
  architectures; ARM32 RouterOS targets lack the required kernel modules.
  Outside of RouterOS, this maps to "whatever your distro kernel actually
  ships" — check `zcat /proc/config.gz | grep -i nf_tables` or
  `modinfo nfnetlink_queue` if in doubt.

## Enabling it

Two settings control this feature (env var, or `/admin/settings` in the
BGP section — either way, changing them requires a restart to take effect):

| Variable | Default | Meaning |
| --- | --- | --- |
| `WDBGP_DYNAMIC_PEER_MD5_MATCH` | `false` | Enables the NFQUEUE consumer and signature matching. |
| `WDBGP_DYNAMIC_PEER_MD5_QUEUE_NUM` | `0` | The NFQUEUE number this process binds to. Only matters if something else on the same host also uses NFQUEUE and needs a different number to avoid colliding. |

Everything else — creating the nftables table/chain/rule that redirects BGP
SYNs into that queue — happens automatically: the BGP speaker installs the
rule together with the NFQUEUE consumer when it starts, and removes both
when it stops (including on "Apply BGP" from the admin UI). There is no
manual rule to write.

## Running as a systemd service (bare metal or VM)

Grant `CAP_NET_ADMIN` via the unit rather than running the whole process as
root:

```ini
[Service]
ExecStart=/usr/local/bin/wdbgp serve
AmbientCapabilities=CAP_NET_ADMIN
CapabilityBoundingSet=CAP_NET_ADMIN
Environment=WDBGP_DYNAMIC_PEER_MD5_MATCH=1
# ... the rest of your usual WDBGP_* environment
```

`AmbientCapabilities` is what actually matters here — it lets a non-root
service user keep `CAP_NET_ADMIN` across `execve`. Running as root works too,
but is unnecessary for this specific feature alone.

## Running under plain Docker (non-RouterOS)

The default Docker capability set does **not** include `CAP_NET_ADMIN` — add
it explicitly when this feature is enabled, on top of the normal `docker run`
invocation from the main README:

```sh
docker run --rm \
  -p 8080:8080 \
  -p 179:179 \
  -v wdbgp-data:/data \
  --cap-add=NET_ADMIN \
  -e WDBGP_ADMIN_PASSWORD=change-me \
  -e WDBGP_SESSION_SECRET=a-long-random-secret \
  -e WDBGP_LOCAL_ASN=64512 \
  -e WDBGP_ROUTER_ID=172.31.255.2 \
  -e WDBGP_BGP_LOCAL_ADDRESS=172.31.255.2 \
  -e WDBGP_DYNAMIC_PEER_MD5_MATCH=1 \
  wh1ted/wdbgp:alpha
```

Without `--cap-add=NET_ADMIN`, the nftables rule and the NFQUEUE consumer
can't start. This is fail-closed: since MD5 verification was requested but
can't be enforced, the BGP speaker refuses to start at all (the error shows
up in the log and in the admin UI's BGP status banner) rather than silently
accepting unauthenticated dynamic peers. The web UI stays reachable so the
setting can be fixed.

## Coexisting with your own firewall rules

`wdbgp` manages a single, self-contained nftables table named
`wdbgp_dynamic_md5` (`inet` family, so it covers both IPv4 and IPv6). It's
dropped and recreated fresh on every process start — safe to run alongside
whatever else you already manage with `iptables`, `nft`, `firewalld`, or
`ufw`, as long as nothing else on the host also tries to own a table with
that exact name. Nothing about this rule alters routing, NAT, or filtering
for any other traffic — it only redirects TCP SYNs on the configured BGP
port into the NFQUEUE.

## Troubleshooting

**Dynamic peer's SYN is sent (confirm with `tcpdump`) but no SYN-ACK ever
comes back, and nothing shows up in the logs at all:**

- Check the rule actually exists and looks right:
  ```sh
  nft list ruleset
  ```
  Expect a `wdbgp_dynamic_md5` table with a rule like
  `tcp dport <port> tcp flags & (syn|ack) == syn queue to <num>`.
- Check whether a consumer is actually bound to that queue number (requires
  root or `CAP_NET_ADMIN` to read):
  ```sh
  cat /proc/net/netfilter/nfnetlink_queue
  ```
  A non-zero port-id means something is attached. If the queue number your
  rule targets doesn't appear here at all, nothing is consuming it — by
  default, Linux silently **drops** every packet sent to a queue with no
  attached consumer. That produces exactly the symptom above: no
  application-level log at all, because the packet never reaches `wdbgp`'s
  own code.
- **First-ever activation on a host that has never loaded `nfnetlink_queue`**
  can hit this even when everything is configured correctly: loading that
  kernel module on demand needs a privilege level a namespaced/unprivileged
  process's own `request_module()` call typically doesn't have, so the very
  first `nfqueue.Open()` after a fresh host boot can fail silently before
  anything else on the host has ever triggered the module to load. Once the
  module is loaded — by any means — it stays loaded for the life of the
  running kernel, independent of restarting `wdbgp` or recreating its
  container. If this is the first time the feature has ever been enabled on
  this exact host, try one restart before assuming something is
  misconfigured.

**A no-match drop is logged (`"dynamic peer SYN did not match any configured
MD5 password, dropping"`) even though you're sure the password is right:**

- Confirm the connecting peer's password is genuinely configured with a
  *dynamic* (`0.0.0.0`/`::`) `PeerConfig` — fixed-peer addresses bypass this
  matcher entirely and rely on the kernel-level per-address key instead
  (`applyListenerMD5`), so a fixed-peer password is never bruteforce-matched
  here at all.
- Confirm the peer is actually signing with `TCP_MD5SIG` (`tcpdump -v` shows
  the MD5 option on the SYN) — a peer sending an unsigned SYN, or one signed
  under a stale/rotated password, is indistinguishable from a genuine
  mismatch from this side.
