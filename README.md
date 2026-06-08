# wdbgp

[![Tests](https://github.com/andrey-vk/wdbgp/actions/workflows/tests.yml/badge.svg)](https://github.com/andrey-vk/wdbgp/actions/workflows/tests.yml)
[![Publish Docker Image](https://github.com/andrey-vk/wdbgp/actions/workflows/deploy.yml/badge.svg)](https://github.com/andrey-vk/wdbgp/actions/workflows/deploy.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Docker Alpha Version](https://img.shields.io/docker/v/wh1ted/wdbgp/alpha?label=docker%20alpha)](https://hub.docker.com/r/wh1ted/wdbgp/tags)
[![Docker Pulls](https://img.shields.io/docker/pulls/wh1ted/wdbgp)](https://hub.docker.com/r/wh1ted/wdbgp)
![Go](https://img.shields.io/badge/Go-1.26-00ADD8)
![Alpine](https://img.shields.io/badge/Alpine-3.23-0d597f)
![GoBGP](https://img.shields.io/badge/GoBGP-3.x-green)
![RouterOS](https://img.shields.io/badge/RouterOS-container-blue)
![Dual Stack](https://img.shields.io/badge/IP-IPv4%20%2B%20IPv6-blueviolet)

[Русская версия](README.ru.md)

`wdbgp` downloads categorized IPv4/IPv6 CIDR feeds, builds a service catalog,
and announces prefixes selected by each user to that user's router over BGP.

It is a single statically linked Go binary containing the HTTP server, SQLite
storage, and GoBGP. Python and BIRD are no longer required. A unique prefix is
installed in the in-memory GoBGP RIB once; per-peer export policies determine
which clients receive it.

## Network model

The container is an independent BGP speaker with its own `veth` address. It
does not modify RouterOS through an API.

- User CIDRs identify web requests; the most specific matching network wins.
- BGP peer IP and ASN identify the router receiving exported routes.
- The advertised next hop must be reachable from that router through the
  intended VPN path.

Do not expose HTTP or TCP/179 to untrusted networks.

## Feeds

Main and beta OpenCCK feeds for IPv4 and IPv6 are inserted on first start. The
first synchronization starts immediately; later runs use
`WDBGP_SYNC_INTERVAL`. A failed download does not replace the last successful
snapshot.

Canonical custom feed format:

```json
{
  "entries": [
    {
      "category": "ai",
      "service": "openai",
      "cidrs": ["104.18.0.0/16", "172.64.0.0/13"]
    }
  ]
}
```

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

`WDBGP_ADMIN_PASSWORD` and `WDBGP_SESSION_SECRET` are required by `serve`.
The old `WDBGP_BIRD_LOCAL_ADDRESS` and `WDBGP_BIRD_LOCAL_ADDRESS_V6` names are
temporarily accepted as compatibility aliases.
When `WDBGP_BGP_LOCAL_ADDRESS_V6` is empty, IPv6 selections remain stored but
only IPv4 prefixes are announced.

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

- OpenCCK is the only built-in adapter for a non-canonical feed format.
- Editing BGP peer settings restarts the embedded BGP server; changing a route
  selection is applied without a restart.
