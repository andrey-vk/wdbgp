# wdbgp

[![Tests](https://github.com/andrey-vk/wdbgp/actions/workflows/tests.yml/badge.svg)](https://github.com/andrey-vk/wdbgp/actions/workflows/tests.yml)
[![Publish Docker Image](https://github.com/andrey-vk/wdbgp/actions/workflows/deploy.yml/badge.svg)](https://github.com/andrey-vk/wdbgp/actions/workflows/deploy.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Docker Image Version](https://img.shields.io/docker/v/wh1ted/wdbgp/latest?label=docker)](https://hub.docker.com/r/wh1ted/wdbgp/tags)
[![Docker Pulls](https://img.shields.io/docker/pulls/wh1ted/wdbgp)](https://hub.docker.com/r/wh1ted/wdbgp)
![Go](https://img.shields.io/badge/Go-1.26-00ADD8)
![Alpine](https://img.shields.io/badge/Alpine-3.23-0d597f)
![Custom BGP](https://img.shields.io/badge/BGP-Custom%20Speaker-blue)
![RouterOS](https://img.shields.io/badge/RouterOS-container-blue)
![Dual Stack](https://img.shields.io/badge/IP-IPv4%20%2B%20IPv6-blueviolet)
![Vue 3](https://img.shields.io/badge/Vue-3-4FC08D)
![TypeScript](https://img.shields.io/badge/TypeScript-6-3178C6)

[Русская версия](README.ru.md)

`wdbgp` downloads categorized IPv4/IPv6 CIDR feeds, builds a service catalog,
and announces prefixes selected by each user to that user's router over BGP.

It is a single statically linked Go binary containing a Vue 3 SPA admin interface
with PrimeVue v4 + Tailwind CSS, an HTTP API server, SQLite
storage, and a custom BGP speaker. The admin UI provides Dashboard, Users, Modes,
Feeds, Adapters, Settings, and Debug pages. A user-facing page enables catalog
selection with web authentication. Routes are announced per-peer directly from the
in-memory route table.

## Catalog modes

The built-in modes are `OpenCCK`, for broad service coverage based largely on
ASN and shared infrastructure ranges, and `IPRanges`, for provider, platform,
CDN, network, and privacy-service ranges from
[antonme/ipranges](https://github.com/antonme/ipranges).

Administrators can enable or disable modes and assign each feed to a mode. Each
user has one active mode and independent category/service selections retained
for every mode. BGP announcements use only the active mode. Existing databases
are migrated to `OpenCCK` without changing their selections.

Users cannot change modes unless the administrator explicitly grants that
permission. Disabled modes retain downloaded data and selections but do not
contribute routes. CIDR diagnostics inspect one selected mode and show only
users whose active mode matches it.

The built-in IPRanges adapter downloads the upstream merged IPv4/IPv6 lists and
maps them into separate catalog services. Upstream combines public provider
data, ASN-derived ranges, and DNS-resolved service addresses, so list scope
varies by service. The mode is initially disabled; enable it and run a feed
sync before configuring users.

The `sing-box SRS` mode provides support for sing-box rule-set binary format
(`.srs` files). These files contain IP CIDR ranges compiled from geoip or
custom rule-set sources. The distribution includes a built-in adapter that
downloads and decompresses SRS files, extracting all CIDRs. A default disabled
feed for Russia geoip (`geoip-ru.srs`) is included as an example.

## Network model

The container is an independent BGP speaker with its own `veth` address. It
does not modify RouterOS through an API.

- User CIDRs identify web requests; the most specific matching network wins.
- BGP peer IP and ASN identify the router receiving exported routes.
- The advertised next hop must be reachable from that router through the
  intended VPN path.

Do not expose HTTP or TCP/179 to untrusted networks.

## Web authentication

Each user's web authentication mode controls access to the selection page:

- **network** — source IP matching their CIDRs
- **login** — authenticate with login/password
- **both** — IP match AND credentials required
- **any** — IP match OR credentials (whichever passes)

Credentials (login + bcrypt-hashed password) are managed per-user in the admin UI.
A `/login` page serves credential-based authentication. `WDBGP_DEFAULT_WEB_AUTH`
sets the default mode for new users (default: `network`).

## Feeds

Main and beta OpenCCK feeds for IPv4 and IPv6 are inserted on first start. The
first synchronization starts immediately; later runs use
`WDBGP_SYNC_INTERVAL`. A failed download does not replace the last successful
snapshot.

See [Feed Adapters](docs/adapters.md) for adapter API, built-in adapters, and
feed format documentation.

A single entry object and a top-level entry array are also accepted. Prefixes
are normalized and deduplicated. Selecting a category also includes services
added to it in future feed updates.

Feeds can be added, edited, enabled, disabled, and deleted from the admin UI.
Disabling a feed keeps its last downloaded snapshot and user selections in the
database, but excludes its services and prefixes from the catalog and BGP
announcements. Re-enabling it restores that snapshot until the next sync.
Changing a feed URL clears the old snapshot; deleting a feed removes it.

The admin UI also includes CIDR diagnostics. Enter an IP address or subnet to
see full and partial service coverage, combined coverage across services, and
coverage from each enabled user's selected categories and services before and
after their effective route filters.

## Route filters

The administrator configures global allow and deny CIDR lists. An empty allow
list permits every selected feed prefix; deny entries are subtracted from the
result. Subtraction is exact: denying `1.1.1.1/32` from a selected `1.0.0.0/8`
splits the `/8` into CIDRs that no longer cover `1.1.1.1`.

Each user can inherit the global lists, extend them with per-user lists, or use
a complete per-user override. In extend mode, allow and deny lists are merged
with the global lists before filtering. The administrator controls whether that
user may edit the mode and lists from the user interface. Feed-provided default
routes are always discarded, and route expansion is limited to prevent
accidental prefix explosions.

The route-filter migration initializes the global deny list with common private,
loopback, link-local, documentation, benchmark, multicast, and reserved networks.

## Settings

All application settings are stored in the database and editable at
`/admin/settings`. Environment variables always override stored values — ENV-controlled
settings are grayed out and show a tooltip. Non-ENV settings take effect
immediately where possible; BGP and network settings require a restart.

The global route allow/deny filters are on the same page.

## Dynamic peer MD5 authentication

Dynamic peers (`peer_ip` = `0.0.0.0`/`::`) are normally identified by ASN
alone — kernel `TCP_MD5SIG` needs a specific address ahead of time, which a
wildcard peer doesn't have. `WDBGP_DYNAMIC_PEER_MD5_MATCH` (default `false`)
closes that gap: an NFQUEUE consumer bruteforce-matches a real TCP MD5
(RFC 2385) signature on inbound SYNs against configured dynamic-peer
passwords, authenticating them cryptographically instead of by ASN alone.
Requires `CAP_NET_ADMIN` and a Linux kernel with `nfnetlink_queue` support;
no `nft`/`iptables` binary is needed anywhere — the redirect rule is
installed automatically, entirely over netlink.

See [Dynamic Peer BGP MD5 Authentication](docs/dynamic-peer-md5.md) for
requirements, systemd/Docker examples for running outside a RouterOS
container, and troubleshooting.

## Run

```sh
docker run --rm \
  -p 8080:8080 \
  -p 179:179 \
  -v wdbgp-data:/data \
  -e WDBGP_ADMIN_PASSWORD=change-me \
  -e WDBGP_SESSION_SECRET=a-long-random-secret \
  -e WDBGP_LOCAL_ASN=64512 \
  -e WDBGP_ROUTER_ID=172.31.255.2 \
  -e WDBGP_BGP_LOCAL_ADDRESS=172.31.255.2 \
  -e WDBGP_BGP_LOCAL_ADDRESS_V6=fd00:31:255::2 \
  wh1ted/wdbgp:alpha
```

Open `/admin` to add users and edit their selections. `/` identifies a user by
source IP. Enable `WDBGP_TRUST_PROXY_HEADERS=true` only behind a trusted reverse
proxy.

The container runs as root: the process must bind the BGP port (179, below
1024) inside the container, and the optional
[dynamic-peer MD5 feature](docs/dynamic-peer-md5.md) additionally needs
`CAP_NET_ADMIN` for its nftables/NFQUEUE setup. To run as a non-root user
instead, move the BGP port above 1024 (`WDBGP_BGP_PORT`) or grant the binary
`CAP_NET_BIND_SERVICE`, and keep the MD5 feature off (or grant
`CAP_NET_ADMIN` explicitly — see the systemd example in the MD5 doc).

The web interface is available in English and Russian. It follows the browser's
`Accept-Language` preference and stores an explicit `EN`/`RU` selection in a
cookie. `WDBGP_DEFAULT_LANGUAGE` controls the fallback language and defaults to
`en`.

Admin login cookies use `WDBGP_ADMIN_COOKIE_SECURE=auto` by default. Cookies are
marked `Secure` for direct HTTPS requests and for trusted
`X-Forwarded-Proto: https` requests when `WDBGP_TRUST_PROXY_HEADERS=true`.
If the admin web UI is accessed without HTTPS, set
`WDBGP_ADMIN_COOKIE_SECURE=false`; otherwise browsers can reject or ignore the
admin session cookie and redirect back to the login page after a successful
password check. Force `true` only when the admin UI is always served over HTTPS.

### Environment

| Variable | Default |
| --- | --- |
| `WDBGP_DB` | `/data/wdbgp.sqlite3` |
| `WDBGP_HOST` / `WDBGP_PORT` | `0.0.0.0` / `8080` |
| `WDBGP_BGP_PORT` | `179` |
| `WDBGP_BGP_HOLD_TIME` | `90` seconds |
| `WDBGP_LOCAL_ASN` | `64512` |
| `WDBGP_ROUTER_ID` | `192.0.2.1` |
| `WDBGP_BGP_LOCAL_ADDRESS` | `192.0.2.2` |
| `WDBGP_BGP_LOCAL_ADDRESS_V6` | empty |
| `WDBGP_SYNC_INTERVAL` | `3600` seconds |
| `WDBGP_ADMIN_COOKIE_SECURE` | `auto` |
| `WDBGP_DEFAULT_LANGUAGE` | `en` |
| `WDBGP_SECURITY_HEADERS` | `true` |
| `WDBGP_RATE_LIMIT_LOGIN` | `5` |
| `WDBGP_RATE_LIMIT_ADMIN` | `30` |
| `WDBGP_SESSION_MAX_AGE` | `28800` |
| `WDBGP_LOG_LEVEL` | `INFO` |
| `WDBGP_LOG_FORMAT` | `text` |
| `WDBGP_TRUST_PROXY_HEADERS` | `false` |
| `WDBGP_STATUS_ALLOWED` | empty (no IPs allowed) |
| `WDBGP_STATUS_TOKEN` | empty (no token) |
| `WDBGP_DEFAULT_WEB_AUTH` | `network` |
| `WDBGP_JS_TIMEOUT` | `120` seconds |
| `WDBGP_JS_MAX_SOURCE` | `1048576` (1 MiB) |
| `WDBGP_JS_MAX_RESPONSE` | `16777216` (16 MiB) |
| `WDBGP_JS_MAX_TOTAL` | `67108864` (64 MiB) |
| `WDBGP_JS_MAX_ENTRIES` | `1000000` |
| `WDBGP_JS_MAX_REQUESTS` | `200` |
| `WDBGP_JS_MAX_CALL_STACK` | `1000` |
| `WDBGP_ADAPTER_BACKUP_DIR` | `<db_dir>/backup/adapters` |
| `WDBGP_ADAPTER_BACKUP_MAX` | `10` |
| `WDBGP_BACKUP_ENABLED` | `true` |
| `WDBGP_BACKUP_DIR` | `<db_dir>` |
| `WDBGP_AUTO_RESTORE_ENABLED` | `false` |
| `WDBGP_ALLOW_DYNAMIC_PEERS` | `false` |
| `WDBGP_DYNAMIC_PEER_MD5_MATCH` | `false` |
| `WDBGP_DYNAMIC_PEER_MD5_QUEUE_NUM` | `0` |

`WDBGP_ADMIN_PASSWORD` and `WDBGP_SESSION_SECRET` are required by `serve`.
When `WDBGP_BGP_LOCAL_ADDRESS_V6` is empty, IPv6 selections remain stored but
only IPv4 prefixes are announced.

### Database backup and auto-restore

Before running pending schema migrations, the server creates a copy of the
current database file in `WDBGP_BACKUP_DIR`. The backup excludes cached feed
data (`catalog_entries`), which can be regenerated by a feed sync. Disable
backups with `WDBGP_BACKUP_ENABLED=false`.

When a database has been created by a newer version of the software, startup
normally enters **degraded mode**: the web interface displays a version mismatch
page (EN/RU), BGP and feed sync are not started.

Enabling `WDBGP_AUTO_RESTORE_ENABLED=true` changes this behavior: the server
scans `WDBGP_BACKUP_DIR` for a backup matching the current server version and
restores it. The incompatible database is saved with a `.incompatible-v<N>.sqlite3`
suffix for manual inspection. If no matching backup is found, the server still
enters degraded mode with a descriptive error.

The `/status` endpoint provides operational visibility in JSON format. Access requires
either a client IP matching `WDBGP_STATUS_ALLOWED` (comma-separated CIDRs) or an
`Authorization: Bearer <WDBGP_STATUS_TOKEN>` header. When neither is configured, `/status` returns 403.

### BGP Communities

Each category and service is assigned a BGP Large Community (`ASN:0:Number`).
Communities are auto-generated with a human-readable scheme (groups: 10000, 20000, 30000…;
services: group+1, group+2…) and can be edited by the administrator at `/admin/communities`.
These communities are attached to every announced BGP prefix, allowing per-category
and per-service traffic engineering on the router side.

Community numbers are assigned **per catalog mode**: the same category can hold a
different number in a different mode, so a user moved between modes changes the
meaning of every community-matching rule on their router.

The end-user selection page also shows each category's and service's community number
next to it, so a user configuring their own router's filtering doesn't need to ask an
administrator for the numbers.

#### Community export

`GET /api/communities` returns the complete community map as JSON, for generating
downstream router policies instead of copying numbers by hand. It is authorized the
same way as `/status` (a client IP in `WDBGP_STATUS_ALLOWED`, or
`Authorization: Bearer <WDBGP_STATUS_TOKEN>`); an admin session is also accepted, so
the Communities page can offer the same document as a download.

Every mode is in one document, because a flat community→name map cannot express
per-mode numbering. Each category and service carries its number, the rendered wire
form (`<asn>:0:<number>`), and its current IPv4/IPv6 prefix counts — a category whose
count collapses is an early signal that a feed broke.

A feed sync publishes its catalog and generates communities for it as two separate
transactions; the export reads the catalog, assignments, and prefix counts as one
consistent snapshot — generating any missing assignments inside that same
transaction — rather than ever returning a service with no assignment yet. That
snapshot spans every mode in the document, not just each mode on its own: a feed
shared by several modes publishes its update to all of them, so reading each mode
independently could otherwise show the update applied to one mode but not yet to
another. The ASN and configured-ASN values used to render every `large_community`
string and `asn_configured` are likewise read after all of that database work finishes,
not before — and both rechecked again right after rendering, redoing the render if
either moved (an ordinary settings save changes the configured value with no BGP
restart at all), so a change completing while the export was still assembling data (or
even during the render itself) can never leave the response describing values that
already stopped matching reality. If every attempt in the retry budget still sees one of
them move, the endpoint answers `503 Service Unavailable` rather than publish a
document already known to be stale — poll again once the config or restart settles.

```console
$ curl -sH "Authorization: Bearer $WDBGP_STATUS_TOKEN" http://wdbgp:8080/api/communities
{
  "schema_version": 1,
  "generated_at": "2026-09-30T18:33:53Z",
  "asn": 64512,
  "bgp_running": true,
  "modes": [
    {
      "mode_id": 1,
      "mode_name": "OpenCCK",
      "enabled": true,
      "categories": [
        {
          "name": "adobe",
          "community": 10000,
          "large_community": "64512:0:10000",
          "prefix_count_v4": 352,
          "prefix_count_v6": 33,
          "services": [
            { "name": "adobe.com", "community": 10001, "large_community": "64512:0:10001",
              "prefix_count_v4": 143, "prefix_count_v6": 13 }
          ]
        }
      ]
    }
  ]
}
```

`asn` is the value the running speaker actually stamps onto routes, which is its
start-time snapshot rather than the live `WDBGP_LOCAL_ASN` setting. If the setting has
been changed without restarting BGP, the new value appears separately as
`asn_configured` — so a generated policy always matches what is on the wire. When no
speaker is running, `bgp_running` is `false` and `asn` reports the configured value.

This document reflects the database's intended state. BGP delivery to peers is
asynchronous and best-effort (a community edit, reset, or feed sync commits first and
reconciles afterward), and this endpoint has no way to tie a specific exported value to
confirmation that it actually reached peers — doing that precisely would need a revision
tracked through every write path that can affect announced routes, which is a larger
change than this endpoint attempts. `/api/admin/bgp/status` and the per-peer state on the
Users page are the existing way to check the BGP session itself is healthy.

For polling, the response carries an `ETag` that covers the document's content but not
`generated_at`, so an unchanged map answers `304 Not Modified`. It's a weak validator
(`W/"…"`) rather than a strong one — two responses sharing it are semantically
equivalent, not byte-for-byte identical (`generated_at` differs), which is exactly what
weak comparison is for. `If-None-Match` is matched per RFC 7232 §2.3: weakly (ignoring
any `W/` prefix on either side), against every validator in a comma-separated list, and
`*` always matches — not just the single exact string this endpoint itself emits:

```console
$ curl -sD- -o/dev/null -H "Authorization: Bearer $TOKEN" \
    -H 'If-None-Match: W/"b178345bbd…"' http://wdbgp:8080/api/communities
HTTP/1.1 304 Not Modified
```

`schema_version` changes only if the document's shape changes incompatibly.

#### Renumbering

"Regenerate missing" only fills gaps — existing assignments are never moved, so it is
safe to run after a feed sync adds services. "Reset to defaults" is different: it
discards every assignment and renumbers from scratch, which silently invalidates any
downstream policy matching the old values, and the resulting breakage looks like a
network fault rather than a config change. It therefore asks first and shows exactly
which values would change; on an instance whose communities were generated
incrementally across several feed syncs, that is typically *most* of them.

The preview's response carries a `digest` alongside the change list, fingerprinting the
exact state it was computed from — including the mode's own ID, so two modes that
happen to share identical community assignments (common when they share feeds) never
share a digest; a preview for one can't be used to authorize a reset on the other.
Applying it (`{"confirm": true, "digest": "..."}`) must echo that digest back; if the
mode changed in the meantime — a feed sync regenerated communities, or another admin
edited one — the digest no longer matches and the reset is refused (`409`) with a fresh
preview instead of silently renumbering something nobody actually reviewed.

### Address lookup

The end-user selection page has an address lookup tool answering "why is this IP
in/not in my tunnel": enter a CIDR prefix or an address, and it reports every catalog
category/service covering it (regardless of whether the user has selected it), whether
the user has it selected, and whether a route filter would remove it before it reaches
the wire. A CIDR block only partly delivered (e.g. a /24 with one selected /25 inside it)
is reported as partial rather than collapsed into the same verdict as a fully-delivered
query. It's a single-user, read-only view of the same coverage logic behind the admin's
CIDR debug tool (`/admin/debug`), scoped so a caller can only ever see their own
selections and filters — there is no mode or user parameter to request anyone else's.

`GET /api/user/debug?cidr=` powers it; mode and identity always come from the
authenticated session.

### Filters in effect

Global and per-user route filters can silently remove prefixes the user would otherwise
expect to see announced. The end-user selection page has a read-only "filters in effect"
section, visible to every user regardless of whether they're allowed to edit their own
filters, showing: their `filter_mode` (`global`, `extend`, or `override`) in plain language,
and the resulting effective allow/deny lists. In `extend` mode it also breaks the effective
lists down into their global and per-user origin, since a merged list alone can't show which
side contributed which entry.

`GET /api/user/route-filters` powers it, deriving everything from the authenticated session —
there is no parameter to request anyone else's filters.

### Audit log

An append-only log of who changed what, scoped to the actions most likely to cause the
kind of "what changed in the last 24h" question that's otherwise unanswerable: community
assignments (manual edits, reset, generate), route filters (the global settings and any
per-user override), feed enable/disable, moving a user between catalog modes, and selection
changes (self-service and admin-direct). Each entry records an actor, action, object type
and ID, a timestamp, and a before/after snapshot of just the fields that changed — nothing
is recorded when a request doesn't actually change anything (e.g. re-submitting the same
filter value, or a feed update that doesn't touch `enabled`).

There is no multi-admin identity in this codebase today — the admin session is a single
shared password/token — so the actor for an admin-triggered change is `admin:<ip>`; a
self-service change made by an end user is `user:<id>`.

`GET /api/admin/audit-log` lists entries with `actor`/`action`/`object_type`/`object_id`/
`since`/`until` filters and `limit`/`offset` pagination (surfaced on the admin "Audit Log"
page). Entries are retained for `audit_log_retention_days` (default 30) and purged hourly
alongside the metrics snapshot purge.

### Feed sync changes

Every sync that changes a feed's entries records what it added and removed: services and
prefixes, with the added services broken down by category. The Feeds page lists a feed's
last 20 changing syncs (`GET /api/admin/feeds/{id}/sync-changes`). The first import is not
recorded, since there is nothing to compare against, and only the 50 most recent changing
syncs per feed are kept.

Selecting a category includes all of its services, so a sync can add services to categories a user selected without any action on their side. The user's own page shows a dismissible note of the services a sync added, per category, for the categories they have selected in a mode that includes the feed. The note reports services, not routes: a service may already be covered by another feed, so it may add no route. The note covers the last 14 days, or since they last dismissed it, whichever is more recent. `GET /api/user/feed-changes` returns it, and `POST /api/user/feed-changes/ack` acknowledges up to the newest change shown, per mode.

### User change log

The user's page shows a change history for the last 30 days, or the audit retention if that is shorter. It lists every change to the user's own selection, route filters, and mode, and every feed sync that added or removed a service the user had selected. Each entry names who made the change: the user (`self`), an administrator (`admin`, with no address shown), or a feed sync, with the feed's name. Self and admin changes come from the audit log and are kept for `audit_log_retention_days`. Feed entries come from `feed_sync_change_services`, which is pruned along with the 50-sync feed history. `GET /api/user/change-log` returns the entries newest first, at most 200 per user. Each list (categories, services, routes) is capped at 100, and anything beyond that is counted in `omitted`.

A feed sync is placed by the user's history, not by their current selection. It shows up if the user was in the feed's mode at that moment and had the service's category or the service itself selected then. The selection and mode at a sync come from the audit trail. Each audited change records the state before it, so the state at a sync is the state before the first change after it, or the current state if none followed. Changing a selection later does not rewrite what an earlier sync meant for the user. The modes a sync reaches are recorded when it runs, so later edits to a feed's mode assignments don't rewrite them either. A sync records the audit row it came after, so a change in the same second is ordered by commit, not by timestamp.

Deleting a feed keeps its earlier syncs in the log, and records the services it took away as a removal. The selections that lose their last service are audited as administrator changes. Those rows are dropped once they are older than audit retention.

The history of an account starts when the account is created, and for accounts that existed at the upgrade, at the upgrade. So an ID that a deleted account once held never shows that account's history.

Reconstruction reads the newest 2000 audit rows of an account. A sync older than the oldest of those rows is left out rather than placed from a guess.

Two limits follow from this. Selection changes recorded before the name payload existed hold only counts, so a sync that falls before one of them can't be placed and is left out. And the history reaches back only as far as the audit window.

### Blast-radius preview

Four admin edits change which prefixes a user actually receives: the global route
filters, a user's own route filter override, a mode's feed membership, and moving a
user to a different catalog mode. Each one now previews its impact before saving —
a dialog lists every affected user's IPv4/IPv6 prefix count before and after, flags
anyone who would lose routes, and shows the aggregate change — with the save itself
gated behind an explicit Apply. If nobody's counts would actually move, the save goes
through directly; the dialog only interrupts when there's something to review.

Generalizes the same technique "Reset to defaults" (above) already used for its own
preview: run the real mutation inside a database transaction, measure the result
against every affected user, then always roll back — so the preview can never drift
from what the mutation would actually do, since it IS the mutation, just discarded
afterward. Unlike the reset preview, there is no digest/staleness contract here —
applying afterward is just the existing save action (the settings form, the user-edit
dialog, the mode feed editor), called normally.

`POST /api/admin/settings/preview-filters` and `POST /api/admin/modes/{id}/feeds/preview`
power the global-filter and mode-feed dialogs, each taking the same body its
corresponding save endpoint does. The admin user-edit dialog's filter_mode,
filter_override, route filters, and catalog_mode_id all save together in one PUT, so
they're previewed together too, by one `POST /api/admin/users/{id}/preview` simulating
the full target state in a single trial — simulating each field in isolation against
the original state could miss (or wrongly report) an impact only the combination
actually produces. All three endpoints are read-only; the trial transaction never
commits.

### Validation and constraints

All values are validated on startup with helpful error messages. If not specified, defaults apply.

| Variable | Constraints |
| --- | --- |
| `WDBGP_PORT` / `WDBGP_BGP_PORT` | Integer 1–65535 |
| `WDBGP_BGP_HOLD_TIME` | Integer 3–65535 (seconds); proposed in OPEN, sessions negotiate min(local, remote) |
| `WDBGP_LOCAL_ASN` | Integer 1–4294967295 |
| `WDBGP_SYNC_INTERVAL` | Integer ≥1 (seconds) |
| `WDBGP_ROUTER_ID` | Valid IPv4 address |
| `WDBGP_BGP_LOCAL_ADDRESS` | Valid IPv4 address |
| `WDBGP_BGP_LOCAL_ADDRESS_V6` | Valid IPv6 address (or empty to disable IPv6 announcements) |
| `WDBGP_SECURITY_HEADERS` | Boolean; enables HTTP security headers (CSP, X-Frame-Options, etc. — no HSTS, so plain-HTTP setups are unaffected). On by default; turn off if a reverse proxy injects its own |
| `WDBGP_RATE_LIMIT_LOGIN` | Integer 1–1000; login requests per minute (default 5) |
| `WDBGP_RATE_LIMIT_ADMIN` | Integer 1–1000; admin API requests per minute (default 30) |
| `WDBGP_SESSION_MAX_AGE` | Integer 60–31536000; session cookie max-age in seconds (default 28800 = 8 hours) |
| `WDBGP_LOG_LEVEL` | DEBUG, INFO, WARN, ERROR, FATAL, PANIC (default INFO) |
| `WDBGP_LOG_FORMAT` | text or json (default text) |
| `WDBGP_TRUST_PROXY_HEADERS` | Boolean; trust X-Forwarded-Proto header for cookie security detection |
| `WDBGP_DEFAULT_WEB_AUTH` | network, login, both, or any |
| `WDBGP_JS_TIMEOUT` | Integer ≥1; adapter execution timeout in seconds (default 120) |
| `WDBGP_JS_MAX_SOURCE` | Integer ≥1; max adapter source code size in bytes (default 1 MiB) |
| `WDBGP_JS_MAX_RESPONSE` | Integer ≥1; max HTTP response bytes per request (default 16 MiB) |
| `WDBGP_JS_MAX_TOTAL` | Integer ≥1; max total HTTP response bytes per adapter run (default 64 MiB) |
| `WDBGP_JS_MAX_ENTRIES` | Integer ≥1; max CIDR entries an adapter can produce (default 1 000 000) |
| `WDBGP_JS_MAX_REQUESTS` | Integer ≥1; max HTTP requests per adapter run (default 200) |
| `WDBGP_JS_MAX_CALL_STACK` | Integer ≥1; max JavaScript call stack depth (default 1000) |
| `WDBGP_DYNAMIC_PEER_MD5_MATCH` | Boolean; enables NFQUEUE-based MD5 signature matching for dynamic peers (default false, see [docs/dynamic-peer-md5.md](docs/dynamic-peer-md5.md)) |
| `WDBGP_DYNAMIC_PEER_MD5_QUEUE_NUM` | Integer 0–65535; NFQUEUE number (default 0) |

The application provides a `/status` endpoint for operational visibility, returning basic health and version information in JSON format.

## Database migrations

Transactional SQLite migrations run automatically before every command. An
existing database from the Python version is upgraded in place without changing
its user, feed, catalog, or selection data. Applied versions are stored in
`schema_migrations`.

The application refuses to open an unknown newer schema. Stop the container and
back up the persistent `/data` volume before major upgrades.

```sh
docker run --rm -v wdbgp-data:/data wh1ted/wdbgp:latest migrate
docker run --rm -v wdbgp-data:/data wh1ted/wdbgp:latest stats
docker run --rm -v wdbgp-data:/data wh1ted/wdbgp:latest sync
```

## Development

The Go build embeds the built frontend (`webgui/dist`) via `go:embed`, so the
frontend must be built first — `go build`/`go vet`/`go test ./...` fail on a
fresh checkout otherwise, since `webgui/dist` doesn't exist until it has.

```sh
cd webgui && npm install && npm run build && cd ..
go test ./...
go vet ./...
go build ./cmd/wdbgp
docker build -t wdbgp:latest .
```

Local HTTP debug run:

```sh
WDBGP_DB=/tmp/wdbgp-dev.sqlite3 \
WDBGP_HOST=127.0.0.1 \
WDBGP_PORT=8080 \
WDBGP_BGP_PORT=1179 \
WDBGP_ADMIN_PASSWORD=admin \
WDBGP_SESSION_SECRET=dev-only-long-random-secret \
WDBGP_LOCAL_ASN=64512 \
WDBGP_ROUTER_ID=192.0.2.1 \
WDBGP_BGP_LOCAL_ADDRESS=192.0.2.2 \
WDBGP_ADMIN_COOKIE_SECURE=false \
go run ./cmd/wdbgp serve
```

Then open `http://127.0.0.1:8080/admin` and log in with password `admin`.

## MikroTik outline

This example uses `172.31.255.2` for the container and `172.31.255.1` for
RouterOS:

```routeros
/interface/veth/add name=veth-wdbgp address=172.31.255.2/30 gateway=172.31.255.1
/interface/bridge/add name=br-containers
/interface/bridge/port/add bridge=br-containers interface=veth-wdbgp
/ip/address/add address=172.31.255.1/30 interface=br-containers

/container/envs/add list=wdbgp key=WDBGP_ADMIN_PASSWORD value="change-me"
/container/envs/add list=wdbgp key=WDBGP_SESSION_SECRET value="replace-with-a-long-random-secret"
/container/envs/add list=wdbgp key=WDBGP_LOCAL_ASN value="64512"
/container/envs/add list=wdbgp key=WDBGP_ROUTER_ID value="172.31.255.2"
/container/envs/add list=wdbgp key=WDBGP_BGP_LOCAL_ADDRESS value="172.31.255.2"

/container/mounts/add name=wdbgp-data src=disk1/wdbgp-data dst=/data
/container/add remote-image=wh1ted/wdbgp:latest interface=veth-wdbgp \
  root-dir=disk1/images/wdbgp mounts=wdbgp-data envlist=wdbgp \
  start-on-boot=yes logging=yes
```

RouterOS 7.22+ has a known, currently-unresolved bug where the container
runtime fails to read `ENTRYPOINT`/`CMD` from some image manifests (including
this one, which is built `FROM scratch`), and the container fails to start
with `start failed: no command specified, set cmd or entrypoint` (see
[MikroTik forum thread](https://forum.mikrotik.com/t/hap-ax3-after-update-ros-from-7-19-2-to-7-23-1-all-containers-fail-to-start/271024)).
Until MikroTik fixes this, work around it by setting `entrypoint` and `cmd`
explicitly on the last `/container/add` line:

```routeros
/container/add remote-image=wh1ted/wdbgp:latest interface=veth-wdbgp \
  root-dir=disk1/images/wdbgp mounts=wdbgp-data envlist=wdbgp \
  entrypoint=/usr/local/bin/wdbgp cmd=serve \
  start-on-boot=yes logging=yes
```

Allow HTTP port 8080 from user networks, TCP/179 between the container and BGP
peers, and forwarding for the received destination prefixes. Add a container
IPv6 address and `WDBGP_BGP_LOCAL_ADDRESS_V6` when using IPv6.

For cryptographic authentication of dynamic peers on RouterOS 7.21+ (x86/ARM64)
containers, add `/container/envs/add list=wdbgp key=WDBGP_DYNAMIC_PEER_MD5_MATCH value="1"`
and set `/container/set [find name=wdbgp] user=0:0` — the container manages
its own NFQUEUE/nftables setup, but needs to run as root to do so. See
[Dynamic Peer BGP MD5 Authentication](docs/dynamic-peer-md5.md).

## Limitations

- Changing a route selection is applied without a restart; enabling/disabling a
  peer updates the BGP speaker dynamically.
