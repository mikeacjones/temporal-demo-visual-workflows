#!/usr/bin/env bash
# Runs the whole demo locally: Temporal dev server, sandbox, flow worker, app,
# and the Vite dev server for the UI. Ctrl-C stops everything.
#
# The worker runs in its own loop so the presenter can kill it
# (make crash-worker) and bring it back (make worker) without restarting the rest.
set -euo pipefail
cd "$(dirname "$0")/.."
mkdir -p bin .logs
go build -o bin/ ./cmd/...

# A fixed local secret keeps session cookies valid across app restarts, so your
# saved flows stay reachable. Hosted deployments supply their own.
export SESSION_SECRET="${SESSION_SECRET:-local-dev-only-session-secret}"

pids=()
cleanup() { kill "${pids[@]}" 2>/dev/null || true; }
trap cleanup EXIT INT TERM

# --disable-config-file keeps a personal CLI profile (maybe a Cloud namespace)
# out of the demo.
if ! nc -z localhost 7233 2>/dev/null; then
  temporal --disable-config-file server start-dev --ip 127.0.0.1 --ui-port 8233 --log-level warn > .logs/temporal.log 2>&1 &
  pids+=($!)
  until nc -z localhost 7233 2>/dev/null; do sleep 0.5; done
fi

./bin/sandbox > .logs/sandbox.log 2>&1 & pids+=($!)
./bin/app     > .logs/app.log 2>&1 & pids+=($!)
WORKER_SET_CURRENT_VERSION=true ./bin/worker > .logs/worker.log 2>&1 & pids+=($!)
(cd web && npm run dev -- --port 5173) > .logs/web.log 2>&1 & pids+=($!)

echo "Builder:     http://localhost:5173   (or the embedded build at http://localhost:8080)"
echo "Temporal UI: http://localhost:8233"
echo "Logs:        .logs/   (tail -f .logs/worker.log)"
wait
