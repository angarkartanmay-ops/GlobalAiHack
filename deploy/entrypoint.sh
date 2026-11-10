#!/bin/sh
# Runs a stock Bifrost gateway and Heimdall side by side in one container.
#
# With no provider key in the environment this starts in simulator mode: the
# real gateway, with simulated providers and a simulated web behind it. Set a
# provider key (OPENAI_API_KEY, ANTHROPIC_API_KEY, GEMINI_API_KEY, ...) and
# TAVILY_API_KEY, restart, and the same container runs live.
set -eu

PORT="${PORT:-7860}"
GATEWAY_PORT="${GATEWAY_PORT:-8080}"
HOME_DIR="${HEIMDALL_HOME:-/app/data}"
BIFROST_BIN="${BIFROST_BIN:-/app/main}"
HEIMDALL_BIN="${HEIMDALL_BIN:-heimdall}"

mkdir -p "$HOME_DIR/gateway" "$HOME_DIR/heimdall"

# Writes the gateway's config.json for this environment and prints the
# variables both processes need. Secrets stay in the environment; the file
# only references them.
eval "$("$HEIMDALL_BIN" bootstrap --gateway-dir "$HOME_DIR/gateway" \
  --heimdall-url "http://127.0.0.1:$PORT" --gateway-url "http://127.0.0.1:$GATEWAY_PORT")"

export PORT HEIMDALL_DATA_DIR="$HOME_DIR/heimdall"

"$HEIMDALL_BIN" serve &
HEIMDALL_PID=$!

# The gateway lists the simulated providers' models when it starts, so
# Heimdall has to be answering first.
i=0
until wget -q -O /dev/null "http://127.0.0.1:$PORT/healthz" 2>/dev/null; do
  i=$((i + 1))
  if [ "$i" -gt 100 ] || ! kill -0 "$HEIMDALL_PID" 2>/dev/null; then
    echo "heimdall did not start" >&2
    exit 1
  fi
  sleep 0.2
done

GATEWAY_HOST="127.0.0.1"
set -- -app-dir "$HOME_DIR/gateway" -port "$GATEWAY_PORT" -host "$GATEWAY_HOST" \
  -log-level "${LOG_LEVEL:-info}" -log-style "${LOG_STYLE:-json}"
if [ "${HEIMDALL_EXPOSE_GATEWAY:-}" = "true" ]; then
  set -- "$@" -base-path /gateway
fi
"$BIFROST_BIN" "$@" &
GATEWAY_PID=$!

trap 'kill "$HEIMDALL_PID" "$GATEWAY_PID" 2>/dev/null; exit 0' TERM INT

# Stop the container if either process stops, so the platform restarts it.
while kill -0 "$HEIMDALL_PID" 2>/dev/null && kill -0 "$GATEWAY_PID" 2>/dev/null; do
  sleep 2
done
echo "a process exited; stopping" >&2
kill "$HEIMDALL_PID" "$GATEWAY_PID" 2>/dev/null || true
exit 1
