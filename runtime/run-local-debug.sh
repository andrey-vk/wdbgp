#!/usr/bin/env sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
PROJECT_DIR=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)

export WDBGP_DB="${WDBGP_DB:-/tmp/wdbgp-dev.sqlite3}"
export WDBGP_HOST="${WDBGP_HOST:-0.0.0.0}"
export WDBGP_PORT="${WDBGP_PORT:-8080}"
export WDBGP_BGP_PORT="${WDBGP_BGP_PORT:-1179}"
export WDBGP_ADMIN_PASSWORD="${WDBGP_ADMIN_PASSWORD:-admin}"
export WDBGP_SESSION_SECRET="${WDBGP_SESSION_SECRET:-dev-only-long-random-secret}"
export WDBGP_LOCAL_ASN="${WDBGP_LOCAL_ASN:-64512}"
export WDBGP_ROUTER_ID="${WDBGP_ROUTER_ID:-192.168.20.15}"
export WDBGP_BGP_LOCAL_ADDRESS="${WDBGP_BGP_LOCAL_ADDRESS:-0.0.0.0}"
export WDBGP_ADMIN_COOKIE_SECURE="${WDBGP_ADMIN_COOKIE_SECURE:-false}"

GO_BIN="${GO_BIN:-go}"
if [ -x /tmp/wdbgp-go1.26.4/bin/go ] && [ "$GO_BIN" = "go" ]; then
	GO_BIN=/tmp/wdbgp-go1.26.4/bin/go
	export GOROOT="${GOROOT:-/tmp/wdbgp-go1.26.4}"
	export GOENV="${GOENV:-off}"
	export GOCACHE="${GOCACHE:-/tmp/wdbgp-gocache1264}"
	export GOPATH="${GOPATH:-/tmp/wdbgp-gopath}"
fi

printf 'Starting wdbgp admin UI on %s:%s\n' "$WDBGP_HOST" "$WDBGP_PORT"
printf 'Local URL: http://127.0.0.1:%s/\n' "$WDBGP_PORT"
printf 'LAN URL via WSL mirrored networking or Windows portproxy: http://<windows-lan-ip>:%s/legacy/admin\n' "$WDBGP_PORT"
printf 'Admin password: %s\n' "$WDBGP_ADMIN_PASSWORD"
printf 'Using DB: %s\n' "$WDBGP_DB"
printf 'Using BGP listen port: %s\n' "$WDBGP_BGP_PORT"
printf 'SPA URL: http://127.0.0.1:%s\n' "$WDBGP_PORT"

cd "$PROJECT_DIR"
exec "$GO_BIN" run ./cmd/wdbgp serve
