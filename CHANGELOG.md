# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).


## [Unreleased]

### Fixed
- **BGP peers could get stuck with a small fraction of their routes while the session stayed healthy** (observed live: 2380 desired routes, router holding 10). Three send-path defects compounded: route UPDATEs were written with no write deadline of their own, inheriting the read deadline `mainLoop` sets on the shared connection — so announcing a large set to a slow peer could fail mid-stream when that deadline expired; the failure was then swallowed (`sendRoutes` logged and returned void), so the manager recorded the full set as announced and every later reconcile skipped the peer — freezing it at whatever subset got through; and keepalives, notifications, and route updates were written from three goroutines with no shared lock, able to interleave partial writes and corrupt the BGP byte stream. All session writes now go through one per-connection mutex with a per-message write deadline (and `mainLoop` arms only the read deadline, so inbound traffic can't extend a blocked write); route delivery is a peer-owned atomic resync — the peer tracks what the remote actually holds, computes withdrawals itself, and advances that state only after a fully successful pass — with failures propagating all the way up so the manager never records a partial announcement as success and both the session-level retry and the next reconcile re-attempt.

### Changed
- **Route announcements are batched**: routes sharing an attribute set (next hop + communities) are packed into common UPDATE messages (up to 100 NLRI each) instead of one message per route — a typical multi-thousand-prefix announcement now takes dozens of writes rather than thousands, shrinking the slow-peer window that triggered the freeze above.

## [0.17.3-alpha] — 2026-07-10

### Changed
- **Release images now carry an OpenVEX attestation** (`.vex/openvex.vex.json`, attached by the deploy pipeline via `docker scout attestation add`) recording vulnerability-applicability verdicts as a portable, repo-versioned record for OpenVEX-aware scanners. First entry: GO-2026-5932 (`golang.org/x/crypto/openpgp` is unmaintained) — it matches every version of `x/crypto` with no fix ever coming, while the shipped binary links only `x/crypto/bcrypt`; verified via govulncheck symbol analysis and `go list -deps`. To reproduce the suppression locally: `docker scout cves <image> --vex-location .vex --vex-author andr.vk@gmail.com` (Scout's CLI trusts only `@docker.com` authors by default). Note the Scout platform (Docker Hub badge, dashboard) currently does not ingest attestation-based VEX for multi-arch images — a long-standing upstream issue (forums.docker.com/t/143422), verified against this repo's images — so the Hub badge is covered by an org-level Scout dashboard exception until Docker fixes ingestion; the attestation then takes over automatically. A statement must be removed if the flagged package ever becomes reachable — CI's govulncheck would start failing on its own in that case, which serves as the safety net.

## [0.17.2-alpha] — 2026-07-09

### Added
- **Configurable BGP hold time** (`WDBGP_BGP_HOLD_TIME` / `bgp_hold_time`, default 90s, range 3–65535). The speaker's RFC 4271 hold-time negotiation (min of local and remote) was fully implemented but hardwired to propose 90s — the knob existed with no way to turn it. Restart-required like the other BGP identity settings (raises the "apply now" banner), editable from the admin BGP section. 0 (infinite hold time) is deliberately rejected: on flaky links it turns silent peer death into a permanently wedged session.
- **Live feed-sync status and asynchronous manual sync.** `GET /api/admin/feeds` items now carry `syncing`/`sync_started_at` from the syncer's in-flight tracking, and the manual sync endpoints (`POST /feeds/{id}/sync`, `/feeds/sync-all`) return `202 Accepted` immediately (or `409` if that sync is already running) instead of holding the HTTP request for the whole sync — on a 100k-CIDR feed that was minutes of a hung request and a frozen UI. The feeds page shows a per-feed spinner, polls while anything is running (including scheduled background syncs already in flight when the page opens), and reports success/failure per feed as it finishes. Errors still land in the feed's `last_error` exactly as scheduled syncs record them.

### Security
- Docker build image and CI toolchain bumped to Go 1.26.5 (GO-2026-5856: Encrypted Client Hello privacy leak in `crypto/tls`, reachable through the HTTPS listener); CI's setup-go now uses `check-latest` so future Go security patch releases are picked up automatically.

## [0.17.1-alpha] — 2026-07-08

### Changed
- **Dynamic-peer MD5 is now fail-closed and its NFQUEUE rule lifecycle is managed by the BGP speaker.** Previously the nftables redirect rule was installed once at process startup, before the queue consumer existed, and a consumer startup failure logged "falling back to ASN-only" while leaving the rule in place — with no consumer attached, that rule silently dropped **every** inbound BGP SYN, fixed peers included, and in a host network namespace it even survived process restarts after the feature was disabled. Now `Speaker.Start` installs the rule together with the consumer and refuses to start BGP if either can't run (the admin asked for MD5 verification; running without it would mean silently accepting unauthenticated dynamic peers), `Speaker.Stop` removes them together, and startup clears any leftover rule from a previous run when the feature is off. Enabling/disabling the feature or changing the queue number from `/admin/settings` now correctly raises the "BGP settings changed — apply now" banner, and "Apply BGP" actually applies it. The nftables table name is scoped per instance (`wdbgp_dynamic_md5_p<port>_q<queue>`), so multiple wdbgp instances sharing a network namespace on different BGP ports no longer delete each other's live redirect rule; the old fixed-name table is cleaned up on upgrade.
- **`security_headers` now defaults to on.** The CSP is tailored to the bundled SPA and HSTS is not among the headers sent, so plain-HTTP deployments are unaffected; disable it if a reverse proxy injects its own conflicting headers.
- Docker release images are published with build provenance and SBOM attestations; remaining GitHub Actions are pinned by commit SHA; CI now enforces `golangci-lint`, frontend ESLint (the `lint` npm script no longer auto-fixes — that moved to `lint:fix`), and runs Go tests under the race detector.

### Fixed
- **Changing a peer's `active_dial` (or its resolved local bind address) now takes effect immediately**: `Speaker.SetPeers` only restarted an existing peer when its ASN or password changed, so a per-user active-dial toggle silently kept the old passive/active behavior until an unrelated full speaker reload.
- **Feed-adapter SSRF filter now rejects all IANA non-global ranges**, not just the stdlib's private/loopback/link-local set: CGNAT (`100.64.0.0/10`), benchmarking (`198.18.0.0/15`), documentation (TEST-NET-1/2/3, `2001:db8::/32`, `3fff::/20`), reserved (`240.0.0.0/4`), `0.0.0.0/8`, protocol assignments (`192.0.0.0/24`), 6to4 (`192.88.99.0/24`, `2002::/16`), TEREDO/ORCHID/benchmarking (`2001::/23`), discard-only (`100::/64`), SRv6 SIDs (`5f00::/16`) and local-use NAT64 (`64:ff9b:1::/48`). IPv4-mapped IPv6 addresses are unmapped before checking, and addresses under the well-known NAT64 prefix (`64:ff9b::/96`) are judged by their embedded IPv4 address — legitimate DNS64 synthesis keeps working, but `64:ff9b::<internal-v4>` no longer reaches private IPv4 space through a NAT64 gateway.
- **HTTP request bodies are now bounded** (`http.MaxBytesReader` middleware sized to the adapter-source limit, ≥1 MiB) and the server sets `ReadTimeout`/`WriteTimeout` in addition to the existing header/idle timeouts, so slow or oversized request bodies can't pin handlers and memory indefinitely.

## [0.17.0-alpha] — 2026-07-07

### Added
- **Dynamic-peer BGP MD5 authentication via NFQUEUE signature matching**, opt-in (`WDBGP_DYNAMIC_PEER_MD5_MATCH`, default off). Dynamic peers (`0.0.0.0`/`::`) previously had no way to authenticate at all — the kernel's `TCP_MD5SIG` needs a specific remote address ahead of time, which a wildcard peer doesn't have, so identification was ASN-only. This adds a real cryptographic check for RouterOS 7.21+ containers on x86/ARM64 (ARM32 lacks the required kernel modules): an NFQUEUE consumer intercepts inbound SYNs to the BGP port, bruteforce-matches the RFC 2385 signature against configured dynamic-peer passwords, and either installs a real per-source-IP `TCP_MD5SIG` key on the listener (match) or drops the SYN outright (no match). Fixed-peer traffic bypasses this entirely — it's already protected by the existing kernel-level per-address key.
  - New setting `WDBGP_DYNAMIC_PEER_MD5_QUEUE_NUM` (default 0) selects the NFQUEUE number; a matching redirect rule for the BGP port must exist in the process's own network namespace. `EnsureDynamicMD5NFQueueRule` installs it automatically at startup, entirely in Go over netlink (`github.com/google/nftables`) — no `nft`/`iptables` binary or shell needed, so the container image doesn't need one either. RouterOS containers need `/container/set 0 user=0:0` (root) for this to work.
  - Off by default: existing dynamic-peer deployments keep today's ASN-only identification until explicitly opted in. If the feature is enabled but its NFQUEUE prerequisites aren't met on the host (unsupported kernel/arch), it logs and falls back to the same ASN-only behavior rather than failing to start.
  - Both settings are now editable from `/admin/settings` (BGP Peers section), not just via env var, and documented in [docs/dynamic-peer-md5.md](docs/dynamic-peer-md5.md) / [docs/dynamic-peer-md5.ru.md](docs/dynamic-peer-md5.ru.md) — requirements, systemd/plain-Docker setup outside a RouterOS container, and troubleshooting (including the `/proc/net/netfilter/nfnetlink_queue` check for a queue with no attached consumer, and the first-activation kernel-module-warmup quirk found while validating this against real RouterOS hardware).

### Changed
- **Settings page regrouped for consistency**: `default_web_auth` moved out of "Localization" (it's an access-control default, not a language setting) into "Security". The overloaded "BGP" section (10 mixed fields) split into "BGP" (identity/wiring: ASN, router ID, port, local addresses — all restart-required) and "BGP Peers" (connection policy: active dial, dynamic peers, dynamic-peer MD5 matching, shared-IP password requirement). "Database" and "Backup" merged into "Database & Backup" (same subsystem: the DB file and its own backup/restore), with feed-adapter source backups split into their own "Adapter Backup" section (a genuinely different subsystem that was previously lumped in under the same generic "Backup" label).

### Fixed
- **Fixed (non-loopback) BGP peers with a password could get their connection torn down right after a successful, correctly-authenticated TCP handshake**: `AcceptWithOpen` redundantly re-set the TCP MD5 key on the already-accepted socket, even though the kernel already verified the signature during the handshake using the key `applyListenerMD5` installed on the listener, and automatically carries that key over to the accepted socket. On some kernels this redundant `setsockopt` call itself fails (`invalid argument`), and the failure was treated as fatal — closing a connection that had already proven its signature was correct. Removed the redundant call; loopback peers are unaffected (TCP MD5 was never enforceable there, so they already relied solely on the OPEN message's password field, which still applies).
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
