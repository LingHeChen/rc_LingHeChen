#!/usr/bin/env bash
#
# Release gate: build the production release, actually START it, and verify it
# serves real traffic. This is the check that catches "compiles and every test
# is green, but the app crashes on boot" bugs — e.g. an invalid Oban plugin that
# only fails when the supervision tree starts (Oban disables plugins under
# `testing: :manual`, so ExUnit never exercises the real config).
#
# `mix release` alone does NOT catch this: it assembles the release but never
# boots it. Only starting the release and hitting /healthz proves the whole
# supervision tree (Repo + Oban + web server) comes up.
#
# Usage:
#   DATABASE_URL=postgres://notify:notify@localhost:5432/notify_smoke \
#   PORT=8092 scripts/release_smoke.sh
#
set -euo pipefail

export MIX_ENV=prod
PORT="${PORT:-8092}"
export PORT
: "${DATABASE_URL:?DATABASE_URL must be set}"

REL_BIN="_build/prod/rel/rc_notification/bin/rc_notification"
BASE="http://localhost:${PORT}"

cleanup() {
  if [ -n "${REL_BIN:-}" ] && [ -x "$REL_BIN" ]; then
    "$REL_BIN" stop >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

echo "==> Building production release"
mix deps.get --only prod
mix release --overwrite

echo "==> Running migrations via the release binary"
"$REL_BIN" eval 'RcNotification.Release.migrate()'

echo "==> Starting the release as a daemon (PORT=${PORT})"
"$REL_BIN" daemon

echo "==> Waiting for /healthz to report 200 (this is the real boot check)"
up=0
for i in $(seq 1 30); do
  code="$(curl -s -o /dev/null -w '%{http_code}' "${BASE}/healthz" 2>/dev/null || true)"
  if [ "$code" = "200" ]; then
    echo "    healthz OK after ${i}s"
    up=1
    break
  fi
  sleep 1
done
if [ "$up" -ne 1 ]; then
  echo "!! Release failed to boot: /healthz never returned 200" >&2
  "$REL_BIN" eval 'IO.puts("release process pid: " <> System.pid())' 2>/dev/null || true
  exit 1
fi

# Use a unique vendor name + idempotency key so the smoke test is re-runnable
# against a non-empty database (vendor names and idempotency keys are unique).
RUN_ID="$(date +%s)-$$"
VENDOR="smoke-${RUN_ID}"

echo "==> Smoke: register a vendor (${VENDOR})"
vendor_code="$(curl -s -o /dev/null -w '%{http_code}' -X POST "${BASE}/vendors" \
  -H 'Content-Type: application/json' \
  -d "{\"name\":\"${VENDOR}\",\"target_url\":\"https://httpbin.org/post\",\"headers\":{\"Content-Type\":\"application/json\"},\"body_tpl\":\"{\\\"id\\\":\\\"<%= @user_id %>\\\"}\"}")"
if [ "$vendor_code" != "201" ]; then
  echo "!! POST /vendors returned ${vendor_code}, expected 201" >&2
  exit 1
fi
echo "    vendor created (201)"

echo "==> Smoke: submit a notification"
notif_code="$(curl -s -o /dev/null -w '%{http_code}' -X POST "${BASE}/notifications/${VENDOR}" \
  -H 'Content-Type: application/json' \
  -H "Idempotency-Key: release-smoke-${RUN_ID}" \
  -d '{"body":{"user_id":"u1"}}')"
if [ "$notif_code" != "202" ]; then
  echo "!! POST /notifications/smoke returned ${notif_code}, expected 202" >&2
  exit 1
fi
echo "    notification accepted (202)"

echo "==> Release gate PASSED: app boots and serves traffic"
