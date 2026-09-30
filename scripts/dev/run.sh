#!/usr/bin/env bash
# Development instance: builds relay and runs `ptyd` + `serve` against an
# isolated RELAY_HOME on a loopback port. Never touches ports 80/443.
#   scripts/dev/run.sh            # port 47700, home ~/.relay-dev
#   PORT=47710 NAME=alt scripts/dev/run.sh
set -euo pipefail
cd "$(dirname "$0")/../.."
PORT="${PORT:-47700}"
NAME="${NAME:-dev}"
export RELAY_HOME="${RELAY_HOME:-$HOME/.relay-$NAME}"
export RELAY_LISTEN="127.0.0.1:$PORT"
export RELAY_DEV=1
mkdir -p bin
scripts/dev/safe go build -o "bin/relay-$NAME" ./cmd/relay
"bin/relay-$NAME" ptyd >"$RELAY_HOME.ptyd.log" 2>&1 &
PTYD=$!
trap 'kill $PTYD 2>/dev/null || true' EXIT
sleep 0.3
echo "relay dev on http://127.0.0.1:$PORT  (RELAY_HOME=$RELAY_HOME)"
exec "bin/relay-$NAME" serve --debug
