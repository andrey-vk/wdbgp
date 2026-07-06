# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).


## [Unreleased]

### Fixed
- **Startup crash on a stale/invalid rate-limit setting**: a DB-stored value that parses fine but fails validation (e.g. `rate_limit_login=0`, left over from before `validateRateLimit` required a positive value) aborted `settings.New()` entirely, crashing the whole app on every future restart with no way to fix it short of editing the database file directly. Such a value now falls back to the setting's default, the same treatment a DB value that fails to even parse already got. An out-of-range env var still fails startup loudly, since that's an operator error happening right now, not a stale row from an old version.

## [0.16.0-alpha] — 2026-07-06

### Changed
- **Database schema refactor: normalized dictionaries, binary IP storage, numeric keys** (migrations 32–35). A 100k-entry catalog database shrinks ~3.7× (15.1 MB → 4.1 MB):
  - New `categories`, `services`, and `prefixes` dictionary tables. `catalog_entries` becomes `(feed_id, service_id, prefix_id)` WITHOUT ROWID — category/service/CIDR text is no longer duplicated on every row and across three indexes. CIDRs are stored once as masked 4/16-byte binary addresses, shared across feeds; orphaned prefixes are pruned after each sync.
  - User selections and communities are remapped to numeric ids and survive the upgrade. Catalog entry data itself is dropped by the migration and repopulated by the startup feed sync (announced routes reappear once the first sync completes).
  - `users.peer_ip`/`next_hop` stored as binary addresses; `filter_mode`/`web_auth` as integer enums; the legacy `filter_override_enabled` column is gone (it always mirrored `filter_mode`). `user_networks` and `user_route_filters` store binary prefixes with an integer allow/deny action.
  - `feeds.last_success` and `app_settings.updated_at` are Unix epoch integers now; the JSON API still serves RFC3339 strings.
  - `feed_snapshots.prefixes` JSON replaced by a `feed_snapshot_counts` child table — no JSON encode/decode on the metrics path.
  - The database file is VACUUMed after migrations run, so the space freed by the rebuilds is actually returned.
- **`catalog_modes.key` column removed** (same rationale as `feed_adapters.key` in 0.15.0): modes are identified by id, `name` is already unique. The mode create API no longer accepts a `key` and mode JSON no longer carries one.

## [0.15.0-alpha] — 2026-07-05

### Added
- **Vue 3 SPA** with PrimeVue v4 + Tailwind CSS: multi-page Vite build serving admin (`/admin`) and user (`/`) interfaces.
- **Admin pages**: Dashboard with metrics charts (user/feed history), Users, Modes, Feeds, Adapters, Settings, Debug with CIDR diagnostics.
- **User-facing page** with catalog selection and web authentication (network, login, both, any modes).
- **Metrics history**: user and feed snapshots with stacked area charts, configurable retention period in Settings.
- **Batch peer-status endpoint** (`/api/admin/users/statuses`) with 5-second frontend polling for live BGP state.
- **Partial update APIs**: users and modes endpoints using pointer fields (`*string`, `*bool`) — nil means "not provided".
- **Credential management**: add, reset password, and delete via Dialog modals in the admin Users page.
- `ForkAdapter` store method with auto-naming for adapter forks.

### Changed
- **Vue 3 SPA replaces legacy Go HTML templates** (~3800 lines of server-rendered pages removed).
- `AddFeedAdapter` returns full `FeedAdapter` struct via SQLite `RETURNING` clause — no separate lookup after insert.
- **Snapshots use Unix epoch INTEGER** timestamps instead of RFC3339 TEXT — eliminates `time.Parse` overhead on constrained hardware.
- **Migration renumbering** (23–27) for compatibility with old production databases. Migration 25 is idempotent (checks column existence before use).
- `seedBuiltInAdapters` checks column existence at startup for databases that predate fork-column migrations.
- Admin rate limiter excludes `/api/admin/me` and `/api/admin/users/statuses` — lightweight auth and polling endpoints.
- **Static analysis**: zero `golangci-lint` warnings (43 issues resolved), zero ESLint warnings. All error paths checked, logged, or returned.
- **`feed_adapters.key` column removed** (migration 31): adapters are now identified by `name` + `is_builtin` instead of a separate slug column. Adapter backup filenames switched from `{key}_...` to `{id}_...`.

### Fixed
- Admin session cookie `Expires` incorrectly set to current time when `SessionMaxAge=0`, causing cookie to be deleted immediately.
- Route filter deny list: broader prefix (e.g. `/19`) covering narrower prefix (`/24`) with different base address was not properly excluded.
- BGP `UpdatePeer` now re-adds the peer when it is not found in the speaker (fixes enable/disable toggle leaving stale state).
- Settings with no database key (env-only settings like `Port`, `Host`, `DBPath`) could be silently written to a shared empty-key slot; `Set`/`Reset` now reject this explicitly.
- Debounced prefix-count fetching (user and admin selection pages) could show stale counts when an older request's response arrived after a newer one.
- Admin pages (Users, Feeds, Adapters, Modes, Debug, Communities) now show a reload prompt instead of an infinite spinner, or a misleading "nothing configured" empty state, when the initial page load fails.
- Any admin API call returning 401 now redirects to the login page immediately, instead of only some endpoints handling session expiry.
- Migration 31 (drop `feed_adapters.key`) could leave the database permanently unable to start if the process crashed mid-migration.
- Adapter backup file rotation could miss backups written before the key-to-id filename switch, or (in a narrow edge case) delete an unrelated adapter's backups.
- **Slow feed sync blocking the whole app** (#24): large feeds (e.g. `ipranges`, 100k+ CIDRs) inserted catalog entries one row per round-trip inside a single transaction, and the main database allowed only one connection total — so a multi-minute sync queued every other request (dashboard, login, any DB-touching page) behind it. Catalog inserts are now batched (500 rows per statement), and the connection pool raised from 1 to 4 so WAL mode's reader/writer concurrency is actually usable.

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


## [0.14.1-alpha] — 2026-06-22

### Fixed
- Added IPv4 unicast capability (AFI=1, SAFI=1) to BGP OPEN message for MikroTik RouterOS compatibility. MikroTik requires explicit IPv4 unicast advertisement to accept IPv4 routes.

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
