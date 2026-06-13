# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- New environment variables for security and observability:
  - `WDBGP_SECURITY_HEADERS` (boolean, default false): Enable HTTP security headers (Content-Security-Policy, X-Frame-Options, etc.)
  - `WDBGP_RATE_LIMIT_LOGIN` (integer 1-1000, default 5): Login requests per minute limit
  - `WDBGP_RATE_LIMIT_ADMIN` (integer 1-1000, default 30): Admin API requests per minute limit
  - `WDBGP_SESSION_MAX_AGE` (integer 60-31536000, default 28800): Session cookie max-age in seconds (8 hours)
  - `WDBGP_LOG_LEVEL` (DEBUG, INFO, WARN, ERROR, FATAL, PANIC, default INFO): Logging verbosity
  - `WDBGP_LOG_FORMAT` (text, json, default text): Log output format
  - `WDBGP_TRUST_PROXY_HEADERS` (boolean, default false): Trust X-Forwarded-Proto header for cookie security detection
  
- New `/status` endpoint for operational visibility, returning health and version info in JSON format
  
- Improved configuration validation with helpful error messages and value constraints
  - Rate limits validated to 1-1000 requests per minute
  - Session max-age validated to 60-31536000 seconds (1 minute to 1 year)
  - Log level and format validated against allowed values
  - All environment variables validated on startup

### Changed
- Enhanced security headers implementation (when `WDBGP_SECURITY_HEADERS=true`)
- Rate limiting now applies to login attempts and admin API endpoints
- Session management improvements with configurable max-age
- Logging system refactored with structured output and configurable format/level
- Codebase cleanup: removed debug statements, improved error handling

### Fixed
- Configuration validation edge cases
- Session cookie security detection with proxy headers
- Log formatting consistency

### Security
- Added configurable security headers for CSP, X-Frame-Options, etc.
- Rate limiting for authentication and admin endpoints
- Session timeout configuration
- Trust proxy header handling for secure cookie detection