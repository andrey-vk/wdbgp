# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- **sing-box SRS binary format support**: parse `.srs` files (v1-5), zlib decompression, CIDR extraction
- `api.srsGet(url, cfg)` — JS adapter API for downloading and parsing SRS files
- `api.log(msg)` — adapter debugging via wdbgp logger
- Feed `data` JSON field for adapter parameterization
- Built-in `singbox-srs` adapter + catalog mode (disabled by default)
- Default disabled Russia geoip feed example
- Force re-download button in feed list
- Feed data textarea with JSON validation
- Adapter source backup on edit/delete/reset (configurable dir + max copies)
- Adapter docs split to `docs/adapters.md` (EN/RU)

### Changed
- Removed redundant admin back-links (sidebar covers navigation)

## [v0.13.0-alpha] — Unreleased

### Added
- **Catalog modes v2**: admin CRUD for creating/editing/deleting modes
- Feeds belong to multiple modes via `catalog_mode_feeds` junction table
- Mode management UI with built-in badges and delete protection
- Feed edit: multi-select mode checkboxes
- Adapter upgrade safety: `builtin_version` + `is_customized` columns
- `seedBuiltInAdapters` preserves user customizations on upgrade

### Changed
- `feeds.mode_id` column dropped — migrated to junction table
- `peer_ip` UNIQUE constraint removed (fixes #17)
- Users can share same peer_ip with different auth methods

## [v0.12.6-alpha] — 2026-06-14

### Fixed
- Backup adapter source before reset to distribution version

## [v0.12.5-alpha] — 2026-06-14

### Added
- Adapter source backup on edit/delete with configurable dir and max copies

## [v0.12.4-alpha] — 2026-06-14

### Fixed
- Various bug fixes

## [v0.12.3-alpha] — 2026-06-14

### Added
- Status endpoint authentication (`WDBGP_STATUS_ALLOWED`, `WDBGP_STATUS_TOKEN`)

## [v0.12.2-alpha] — 2026-06-14

### Fixed
- Various bug fixes

## [v0.12.1-alpha] — 2026-06-14

### Fixed
- Security fix

## [v0.12.0-alpha] — 2026-06-14

### Added
- Web authentication modes: `network`, `login`, `both`, `any`
- User credentials: login + bcrypt-hashed password per user
- `/login` page for credential-based authentication
- `WDBGP_DEFAULT_WEB_AUTH` for default auth mode on new users
- Redesigned admin UI with sidebar navigation
- Settings page: all app settings editable from admin UI
- Configurable `WDBGP_SESSION_MAX_AGE`, rate limits, security headers
- Structured logging with configurable format/level

## [v0.11.3-alpha] — 2026-06-13

### Fixed
- Use uint32 for community IDs to prevent overflow

## [v0.11.2-alpha] — 2026-06-13

### Fixed
- 32-bit ARM build compatibility

## [v0.11.1-alpha] — 2026-06-13

### Fixed
- Various bug fixes

## [v0.11.0-alpha] — 2026-06-13

### Added
- BGP Large Communities (`ASN:0:Number`) per category and service
- Community editor in admin UI with auto-generation

## [v0.10.1-alpha] — 2026-06-13

### Added
- Docker Hub description sync on deploy

## [v0.10.0-alpha] — 2026-06-13

### Added
- Rate limiting for login and admin endpoints
- Configurable security headers (CSP, HSTS, X-Frame-Options)
- `/status` endpoint for operational visibility
- Configuration validation with helpful error messages
- Trust proxy header handling for secure cookie detection

### Changed
- Logging system refactored with structured output
- Enhanced error handling and codebase cleanup

## [v0.9.0-alpha] — 2026-06-09

### Added
- **Scriptable feed adapters**: JavaScript programs stored in SQLite, executed with goja runtime
- Built-in adapters: canonical JSON, OpenCCK, IPRanges
- Adapter editor page with syntax validation and test/preview
- Adapter reset to distribution version
- `api.httpGet(url)` for JS adapters with host allowlisting and size limits
- JS runtime limits: timeout, max source/response/total bytes, max entries, max requests
- Feed management: add, edit, enable, disable, delete from admin UI
- English + Russian localization (i18n) with browser language detection
- Route selection count details in user selection page

## [v0.8.0-alpha] — 2026-06-09

### Added
- Feed lifecycle management
- Disabling feed keeps snapshot, re-enabling restores it

## [v0.7.0-alpha] — 2026-06-09

### Added
- Route filter modes: global + per-user allow/deny CIDR lists
- Route filter subtraction (exact CIDR splitting)
- Route filter migration with default deny list

## [v0.6.0-alpha] — 2026-06-08

### Added
- CIDR diagnostics: IP/subnet coverage analysis per user and mode
- Admin diagnostic tools

## [v0.5.2-alpha] — 2026-06-05

### Fixed
- Per-peer BGP export policy

## [v0.5.1-alpha] — 2026-06-05

### Fixed
- Legacy database migration compatibility

## [v0.5.0-alpha] — 2026-06-05

### Added
- Route filter modes (inherit, extend, override)
- Multi-arch Docker build (amd64, 386, arm64, arm/v7, arm/v5)
- IPRanges feed support

## [v0.4.0-alpha] — 2026-06-04

### Added
- Catalog modes: independent selection per user
- Catalog mode migration from legacy Python database

## [v0.3.0-alpha] — 2026-06-03

### Added
- User management in admin UI
- Category/service selection per user
- BGP peer management

## [v0.2.0-alpha] — 2026-06-02

### Added
- OpenCCK CIDR feeds with IPv4/IPv6 main and beta feeds
- Feed synchronization with configurable interval
- BGP route announcements

## [v0.1.0-alpha] — 2026-06-01

### Added
- Initial release: single Go binary with HTTP server, SQLite, GoBGP
- Admin web UI
- Transactional SQLite migrations
- Python database upgrade path
- Docker image
- CLI subcommands: `serve`, `migrate`, `sync`, `stats`, `healthcheck`
