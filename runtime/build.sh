#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# Build frontend (Vue SPA → webgui/dist/)
echo "=== Building frontend ==="
cd "$ROOT/webgui"
npm ci --prefer-offline --silent
npx eslint .
npx vue-tsc -b --noEmit
npx vite build

echo ""
echo "=== Building Go backend ==="
cd "$ROOT"
go vet ./...
go build -o wdbgp ./cmd/wdbgp

echo ""
echo "=== Done ==="
echo "  Binary: $ROOT/wdbgp"
echo "  Static: $ROOT/webgui/dist/"
