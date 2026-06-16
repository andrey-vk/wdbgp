# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Catalog modes v2: custom modes with CRUD, feeds belong to multiple modes via junction table
- Adapter upgrade safety: `builtin_version` + `is_customized` columns, auto-upgrade unmodified built-ins
- Mode management UI in admin sidebar with built-in badges and delete protection
- Feed edit: multi-select mode checkboxes

### Changed
- Feeds no longer locked to one mode — M:M via `catalog_mode_feeds`
- `peer_ip` UNIQUE constraint removed (users can share same IP)

### Removed
- `feeds.mode_id` column — migrated to junction table

## [1.4.0] — 2026-06-16

### Added
- **sing-box SRS binary format support**: parse `.srs` files (v1-5), zlib decompression, CIDR extraction
- `api.srsGet(url, cfg)` — JS adapter API for downloading and parsing SRS files
- `api.log(msg)` — adapter debugging via wdbgp logger
- Feed `data` JSON field for adapter parameterization (one adapter, many feeds)
- Built-in `singbox-srs` adapter + catalog mode (disabled by default)
- Default disabled Russia geoip feed example
- Force re-download button in feed list
- Feed data textarea with JSON validation
- Adapter source backup on edit/delete/reset (configurable dir + max copies)

### Changed
- Adapter docs split to `docs/adapters.md` (EN/RU)
- Removed redundant admin back-links (sidebar covers navigation)

## [1.3.0] — 2026-06-14

### Added
- **Scriptable feed adapters**: JavaScript programs stored in SQLite, executed with goja runtime
- Built-in adapters: canonical JSON, OpenCCK, IPRanges
- Adapter editor page with syntax validation and test/preview
- Adapter testing against feeds (preview 100 normalized CIDRs)
- Adapter reset to distribution version for built-in adapters
- `api.httpGet(url)` for JS adapters with host allowlisting and size limits
- JS runtime limits: timeout, max source/response/total bytes, max entries, max requests
- Feed management: add, edit, enable, disable, delete from admin UI
- English + Russian localization (i18n) with browser language detection
- Route selection count details in user selection page

### Changed
- Feed lifecycle: disabling keeps snapshot, re-enabling restores it
- Changing feed URL/adapter clears old snapshot

## [1.2.0] — 2026-06-12

### Added
- **Catalog modes**: `OpenCCK` and `IPRanges` with independent selection per user
- IPRanges feed: provider, platform, CDN, network, and privacy-service ranges
- BGP Large Communities (`ASN:0:Number`) per category and service
- Community editor in admin UI with auto-generation
- CIDR diagnostics: IP/subnet coverage analysis per user and mode
- Route filters: global + per-user allow/deny CIDR lists with subtraction
- Route filter migration: default deny list for private/loopback/link-local networks

### Changed
- Users have one active mode with independent category/service selections
- Disabled modes retain data and selections but don't contribute routes
- Database migrated: existing users assigned to `OpenCCK` mode

## [1.1.0] — 2026-06-10

### Added
- **Web authentication modes**: `network` (IP match), `login` (credentials), `both`, `any`
- User credentials: login + bcrypt-hashed password, managed per-user in admin UI
- `/login` page for credential-based authentication
- `WDBGP_DEFAULT_WEB_AUTH` for default auth mode on new users
- Selectable web authentication per user
- BGP peer settings: restart on change, route selection applies without restart

### Changed
- Redesigned admin UI with sidebar navigation
- Users identify by source IP (most specific matching network wins)

## [1.0.0] — 2026-06-08

### Added
- **Initial release**: single Go binary (HTTP server, SQLite, GoBGP)
- OpenCCK CIDR feeds: IPv4/IPv6 main and beta feeds auto-inserted on first start
- BGP announcements to routers with per-peer export policies
- In-memory GoBGP RIB with unique prefix installation
- Admin web UI: user management, category/service selection
- Status page: operational visibility in JSON format
- Transactional SQLite migrations (auto-run before every command)
- Python database upgrade path (existing databases migrated in place)
- Environment variable configuration (see README)
- Docker multi-arch image (linux/amd64, 386, arm64, arm/v7, arm/v5)
- `serve`, `migrate`, `sync`, `stats`, `healthcheck` CLI subcommands
- Logging with structured output and configurable format/level
- Session management with configurable max-age
- Rate limiting for login and admin endpoints
- Configurable security headers (CSP, HSTS, X-Frame-Options)
- BIRD compatibility layer (legacy Python config generation)
- MikroTik RouterOS container deployment guide
