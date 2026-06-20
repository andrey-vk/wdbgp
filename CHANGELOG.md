# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.14.0-alpha] — 2026-06-20

### Added
- **Custom BGP speaker** replacing GoBGP entirely. Full BGP-4 wire protocol: OPEN, UPDATE, KEEPALIVE, NOTIFICATION. TCP FSM with active/passive modes, reconnection with exponential backoff, per-peer route distribution with large community attributes.
- **4-octet ASN support** (RFC 6793): AS4 capability (code 65), AS_TRANS (23456), 4-byte AS_PATH encoding.
- **IPv6 unicast support** (RFC 4760): MP_REACH_NLRI, MP_UNREACH_NLRI, IPv6 capability negotiation.
- **Same-IP different-ASN peers**: speaker maps by `addr:asn` key, OPEN message ASN extraction.
- **Dynamic peers** (`0.0.0.0` / `::`): passive-only, globally unique ASN, configurable via `WDBGP_ALLOW_DYNAMIC_PEERS`.
- **TCP MD5 authentication** (RFC 2385) on listener and active dial sockets.
- **Catalog modes v2**: M:M feed assignments via `catalog_mode_feeds` junction table, admin CRUD for modes, per-mode community keys.
- **Database backup before migration**: automatic SQLite backup with `catalog_entries` stripped, configurable via `WDBGP_BACKUP_ENABLED` / `WDBGP_BACKUP_DIR`.
- **Auto-restore from backup**: when DB version is newer than server, scan backups for matching version and restore. Degraded web mode if restore fails. Configurable via `WDBGP_AUTO_RESTORE_ENABLED`.
- **Sing-box SRS binary format support**: built-in adapter for `.srs` files, geoip example feed.
- New environment variables: `WDBGP_SECURITY_HEADERS`, `WDBGP_RATE_LIMIT_LOGIN`, `WDBGP_RATE_LIMIT_ADMIN`, `WDBGP_SESSION_MAX_AGE`, `WDBGP_LOG_LEVEL`, `WDBGP_LOG_FORMAT`, `WDBGP_TRUST_PROXY_HEADERS`, `WDBGP_BACKUP_ENABLED`, `WDBGP_BACKUP_DIR`, `WDBGP_AUTO_RESTORE_ENABLED`, `WDBGP_ALLOW_DYNAMIC_PEERS`.
- `/status` endpoint for operational visibility (JSON).
- `golangci-lint` as dev-only tool with `.golangci.yml` config.
- 30+ new tests: BGP session communication (IPv4+IPv6), dynamic peers, backup/restore, auto-restore, degraded mode, AS4 encoding.

### Changed
- **GoBGP dependency completely removed**. Custom BGP speaker handles everything.
- `UNIQUE(peer_ip, peer_asn)` constraint on users table (was `peer_ip UNIQUE`).
- Dynamic peer UI: checkbox `readonly` when disabled, password `disabled` for wildcard IPs, same-IP password matching enforced with tooltip hints.
- Security headers, rate limiting, session management, and logging refactored.
- Config validation improved with helpful error messages and value constraints.

### Fixed
- BGP password never rendered in DOM `value` attribute.
- Dynamic IP form submits `0.0.0.0` via `readonly` (not `disabled`).
- Logout cookie now includes `Secure` flag and `SameSite: StrictMode`.
- CSRF context key changed from string to typed `csrfCtxKey struct{}`.
- 10 dead code items removed (~300 lines).
- Prefix decoder now validates `IsValid()` on decoded prefixes.
- MD5 listener skips unspecified addresses in setsockopt loop.
- AS_PATH always uses segment type 2 (AS_SEQUENCE) for both 2-byte and 4-byte.
- Dynamic peer duplicate check covers `::` and password mismatch.
- Hold time 0 properly disables hold timer.
- `feedsList` error handling, `validateWebAuthMode` error message, `strconv.Atoi` error checks.

### Removed
- GoBGP library (`github.com/osrg/gobgp/v4`).
- Python and BIRD dependencies (no longer referenced in docs).
- 10 dead functions/fields (~300 lines).

## [0.12.6-alpha] — prior release

### Added
- IPRanges feed adapter.
- Adapter backup directory with configurable max backups.
- JavaScript feed adapter with timeout, resource limits, and URL validation.
- CIDR debug tool for prefix analysis.
- Route filter override per user.
- `WDBGP_DEFAULT_WEB_AUTH` config.

### Changed
- Web auth mode: `any` added as valid option (network OR login).
- Feed sync improvements with circuit breaker and retry logic.
- `WDBGP_ADAPTER_BACKUP_DIR` and `WDBGP_ADAPTER_BACKUP_MAX` env vars.

### Fixed
- Various feed sync edge cases and adapter compatibility.
- Community generation for multi-mode feeds.
- Session cookie security improvements.
