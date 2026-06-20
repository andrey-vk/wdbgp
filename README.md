# wdbgp

[![Tests](https://github.com/andrey-vk/wdbgp/actions/workflows/tests.yml/badge.svg)](https://github.com/andrey-vk/wdbgp/actions/workflows/tests.yml)
[![Publish Docker Image](https://github.com/andrey-vk/wdbgp/actions/workflows/deploy.yml/badge.svg)](https://github.com/andrey-vk/wdbgp/actions/workflows/deploy.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Docker Alpha Version](https://img.shields.io/docker/v/wh1ted/wdbgp/alpha?label=docker%20alpha)](https://hub.docker.com/r/wh1ted/wdbgp/tags)
[![Docker Pulls](https://img.shields.io/docker/pulls/wh1ted/wdbgp)](https://hub.docker.com/r/wh1ted/wdbgp)
![Go](https://img.shields.io/badge/Go-1.26-00ADD8)
![Alpine](https://img.shields.io/badge/Alpine-3.23-0d597f)
![Custom BGP](https://img.shields.io/badge/BGP-Custom%20Speaker-blue)
![RouterOS](https://img.shields.io/badge/RouterOS-container-blue)
![Dual Stack](https://img.shields.io/badge/IP-IPv4%20%2B%20IPv6-blueviolet)

[Русская версия](README.ru.md)

`wdbgp` downloads categorized IPv4/IPv6 CIDR feeds, builds a service catalog,
and announces prefixes selected by each user to that user's router over BGP.

It is a single statically linked Go binary containing the HTTP server, SQLite
storage, and a custom BGP speaker. Routes
are announced per-peer directly from the in-memory route table.

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
| `WDBGP_LOCAL_ASN` | `64512` |
| `WDBGP_ROUTER_ID` | `192.0.2.1` |
| `WDBGP_BGP_LOCAL_ADDRESS` | `192.0.2.2` |
| `WDBGP_BGP_LOCAL_ADDRESS_V6` | empty |
| `WDBGP_SYNC_INTERVAL` | `3600` seconds |
| `WDBGP_ADMIN_COOKIE_SECURE` | `auto` |
| `WDBGP_DEFAULT_LANGUAGE` | `en` |
| `WDBGP_SECURITY_HEADERS` | `false` |
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

`WDBGP_ADMIN_PASSWORD` and `WDBGP_SESSION_SECRET` are required by `serve`.
The old `WDBGP_BIRD_LOCAL_ADDRESS` and `WDBGP_BIRD_LOCAL_ADDRESS_V6` names are
temporarily accepted as compatibility aliases.
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

### Validation and constraints

All values are validated on startup with helpful error messages. If not specified, defaults apply.

| Variable | Constraints |
| --- | --- |
| `WDBGP_PORT` / `WDBGP_BGP_PORT` | Integer 1–65535 |
| `WDBGP_LOCAL_ASN` | Integer 1–4294967295 |
| `WDBGP_SYNC_INTERVAL` | Integer ≥1 (seconds) |
| `WDBGP_ROUTER_ID` | Valid IPv4 address |
| `WDBGP_BGP_LOCAL_ADDRESS` | Valid IPv4 address |
| `WDBGP_BGP_LOCAL_ADDRESS_V6` | Valid IPv6 address (or empty to disable IPv6 announcements) |
| `WDBGP_SECURITY_HEADERS` | Boolean; enables HTTP security headers (CSP, HSTS, X-Frame-Options, etc.) |
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

```sh
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

Allow HTTP port 8080 from user networks, TCP/179 between the container and BGP
peers, and forwarding for the received destination prefixes. Add a container
IPv6 address and `WDBGP_BGP_LOCAL_ADDRESS_V6` when using IPv6.

## Limitations

- Editing BGP peer settings restarts the embedded BGP server; changing a route
  selection is applied without a restart.
