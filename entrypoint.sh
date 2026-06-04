#!/bin/sh
set -eu

python -m wdbgp init
bird -p -c "${WDBGP_BIRD_CONFIG:-/etc/bird.conf}"
bird -c "${WDBGP_BIRD_CONFIG:-/etc/bird.conf}" -s "${WDBGP_BIRD_SOCKET:-/run/bird/bird.ctl}"
exec python -m wdbgp serve
