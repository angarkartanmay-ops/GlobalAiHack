#!/usr/bin/env bash
# Run Heimdall and a Bifrost gateway side by side in simulator mode.
#
#   BIFROST_BIN=/path/to/bifrost-http scripts/dev-sim.sh
#
# BIFROST_BIN defaults to `npx -y @maximhq/bifrost`. State goes to ./.dev.
set -euo pipefail
cd "$(dirname "$0")/.."

HEIMDALL_PORT="${HEIMDALL_PORT:-8787}"
GATEWAY_PORT="${GATEWAY_PORT:-8080}"
DEV_DIR="${DEV_DIR:-$PWD/.dev}"
mkdir -p "$DEV_DIR/gateway" "$DEV_DIR/heimdall" bin

go build -o bin/heimdall ./cmd/heimdall

eval "$(bin/heimdall bootstrap --mode "${HEIMDALL_MODE:-sim}" --gateway-dir "$DEV_DIR/gateway" \
  --heimdall-url "http://127.0.0.1:$HEIMDALL_PORT" --gateway-url "http://127.0.0.1:$GATEWAY_PORT")"
export PORT="$HEIMDALL_PORT" HEIMDALL_DATA_DIR="$DEV_DIR/heimdall"

pids=()
trap 'kill "${pids[@]}" 2>/dev/null || true' EXIT

bin/heimdall serve > "$DEV_DIR/heimdall.log" 2>&1 &
pids+=($!)

if [ -n "${BIFROST_BIN:-}" ]; then
  "$BIFROST_BIN" -app-dir "$DEV_DIR/gateway" -port "$GATEWAY_PORT" -host 127.0.0.1 -log-style pretty > "$DEV_DIR/gateway.log" 2>&1 &
else
  npx -y @maximhq/bifrost -app-dir "$DEV_DIR/gateway" -port "$GATEWAY_PORT" -host 127.0.0.1 > "$DEV_DIR/gateway.log" 2>&1 &
fi
pids+=($!)

echo "Heimdall  http://127.0.0.1:$HEIMDALL_PORT   (log: $DEV_DIR/heimdall.log)"
echo "Bifrost   http://127.0.0.1:$GATEWAY_PORT   (log: $DEV_DIR/gateway.log)"
wait
