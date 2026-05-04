#!/usr/bin/env bash
# scripts/smoke-brownfield.sh — CI smoke test for quickstart.md Path B.
#
# Scenario: an MVP (001) operator has a running SQLite DB with at
# least one upstream_accounts row and their DB driver/URL in env,
# but no config.json. The 002 binary must boot, auto-materialize
# config.json, and leave the router answering /api/admin/health.
#
# This script intentionally uses ONLY POSIX shell + sqlite3 + curl
# so CI runners with a bare Go toolchain can execute it without a
# full dev setup.
#
# Exit codes:
#   0 — smoke passed; workspace left clean.
#   1 — smoke failed; see diagnostic output; cleanup still runs.
set -euo pipefail

cd "$(dirname "$0")/.."

ROOT="$(pwd)"
TMPDIR="$(mktemp -d -t router-smoke.XXXXXX)"
CFG_PATH="${TMPDIR}/config.json"
DB_PATH="${TMPDIR}/router.db"
PORT="${ROUTER_SMOKE_PORT:-18080}"
PID=""

log() { printf '[smoke] %s\n' "$*"; }
fail() { printf '[smoke][FAIL] %s\n' "$*" >&2; exit 1; }

cleanup() {
  if [ -n "${PID}" ] && kill -0 "${PID}" 2>/dev/null; then
    kill "${PID}" 2>/dev/null || true
    wait "${PID}" 2>/dev/null || true
  fi
  rm -rf "${TMPDIR}"
}
trap cleanup EXIT INT TERM

command -v sqlite3 >/dev/null || fail "sqlite3 is required"
command -v curl   >/dev/null || fail "curl is required"
command -v go     >/dev/null || fail "go is required"

log "workspace: ${TMPDIR}"

# 1. Pre-seed DB with one upstream_accounts row matching the
#    001/002 schema. golang-migrate will own the schema for real;
#    for the smoke we bypass it with an equivalent CREATE TABLE so
#    the first 002 boot sees the brownfield signal (Count>0).
log "seeding brownfield DB"
sqlite3 "${DB_PATH}" <<'SQL'
CREATE TABLE IF NOT EXISTS upstream_accounts (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  name        TEXT    NOT NULL,
  provider    TEXT    NOT NULL DEFAULT 'openai',
  api_key     TEXT    NOT NULL,
  base_url    TEXT,
  status      TEXT    NOT NULL DEFAULT 'active',
  created_at  DATETIME NOT NULL,
  updated_at  DATETIME NOT NULL
);
INSERT INTO upstream_accounts
  (name, provider, api_key, base_url, status, created_at, updated_at)
VALUES
  ('brownfield-acc', 'openai', 'sk-smoke-KEEP-SECRET', NULL, 'active',
   datetime('now'), datetime('now'));
SQL

# 2. Boot the router pointing at the seeded DB. No config.json;
#    brownfield is the only path that can land steady-state.
log "starting router on :${PORT}"
ROUTER_CONFIG_PATH="${CFG_PATH}" \
ROUTER_DB_DRIVER="sqlite3" \
ROUTER_DB_URL="${DB_PATH}" \
ROUTER_LISTEN_ADDR=":${PORT}" \
  go run ./cmd/one-llm-router >"${TMPDIR}/router.log" 2>&1 &
PID=$!

# 3. Wait up to 20s for the health endpoint to come up.
for _ in $(seq 1 40); do
  if curl -fsS "http://127.0.0.1:${PORT}/api/admin/health" \
       -o "${TMPDIR}/health.json" 2>/dev/null; then
    break
  fi
  sleep 0.5
done
[ -s "${TMPDIR}/health.json" ] || {
  cat "${TMPDIR}/router.log" >&2
  fail "router did not answer /api/admin/health in 20s"
}

log "health response: $(cat "${TMPDIR}/health.json")"

# 4. Assert envelope shape (code==0) and setup_state=="done".
python3 - "${TMPDIR}/health.json" <<'PY' || fail "health payload assertion failed"
import json, sys
j = json.load(open(sys.argv[1]))
assert j.get("code") == 0, f"code={j.get('code')}"
data = j.get("data", {})
assert data.get("setup_state") == "done", f"setup_state={data.get('setup_state')}"
PY

# 5. Assert config.json was materialized.
[ -f "${CFG_PATH}" ] || fail "expected ${CFG_PATH} to be materialized"
log "materialized config present"

# 6. Assert mode 0600 (POSIX stat; BSD vs GNU compatibility kludge).
MODE=$(stat -f '%Op' "${CFG_PATH}" 2>/dev/null || stat -c '%a' "${CFG_PATH}")
case "${MODE}" in
  *600) ;;
  *) fail "expected mode 0600, got ${MODE}" ;;
esac

# 7. Assert that /v1/* is wired in brownfield steady-state (US-2 AC-1:
#    existing Codex clients MUST continue to reach /v1/responses after
#    the 002 binary boots over an 001 dataset). We cannot exercise a
#    real upstream because CI has no OpenAI credentials; instead we
#    verify the router does NOT 404 on /v1/responses — any non-404
#    response (typically 502 router_error because the dummy api_key
#    fails against the real upstream, or a connection refused mapped
#    to an envelope-less 001 native error) proves the proxy handler
#    is registered. A 404 here would mean the /v1/* pattern never
#    reached the mux (the regression F-001 addressed).
log "probing /v1/* wiring (expect non-404)"
# Note: intentionally NOT using curl -f; we WANT the HTTP code even
# when the upstream call fails (which is the expected outcome against
# a fake api_key).
V1_CODE=$(curl -sS -o "${TMPDIR}/v1.body" -w '%{http_code}' \
  -X POST \
  -H "content-type: application/json" \
  -H "authorization: Bearer sk-smoke-KEEP-SECRET" \
  --data '{"model":"gpt-5","messages":[{"role":"user","content":"hi"}]}' \
  "http://127.0.0.1:${PORT}/v1/responses" 2>/dev/null || echo "000")

# curl -f would drop the body on 4xx/5xx; the `|| echo` branch
# captures the HTTP code even when curl exits non-zero. We accept
# any code EXCEPT 404 (proxy not registered) and 000 (total network
# failure — the router process should still be alive).
case "${V1_CODE}" in
  000|404)
    printf '[smoke] /v1/* response body: %s\n' "$(cat "${TMPDIR}/v1.body" 2>/dev/null || true)" >&2
    fail "/v1/responses returned ${V1_CODE}; proxy handler not wired"
    ;;
  *)
    log "/v1/* reachable (http ${V1_CODE}, body passthrough OK)"
    ;;
esac

log "PASS"
