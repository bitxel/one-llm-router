#!/usr/bin/env bash
set -euo pipefail

case "${1:-}" in
  -h|--help)
    cat <<'EOF'
Usage: LIVE_UPSTREAM_SMOKE=1 LIVE_OPENAI_API_KEY=... LIVE_OPENAI_BASE_URL=... LIVE_OPENAI_MODEL=... bash scripts/run-openai-sdk-compat-smoke.sh

Runs an opt-in OpenAI Python SDK compatibility smoke against a temporary
one-llm-router process. Default CI does not run this target and does not
require live upstream credentials.

Required environment:
  LIVE_UPSTREAM_SMOKE=1
  LIVE_OPENAI_API_KEY
  LIVE_OPENAI_BASE_URL   Upstream origin, not a /v1 path
  LIVE_OPENAI_MODEL      Responses API model; also used for Chat Completions unless overridden

Optional environment:
  LIVE_OPENAI_CHAT_MODEL Chat Completions model override; defaults to LIVE_OPENAI_MODEL
  OPENAI_PYTHON_VERSION  Python SDK version, default 2.32.0
  ROUTER_BINARY          Router binary path, default ./one-llm-router
  PYTHON                 Python executable, default python3
EOF
    exit 0
    ;;
esac

sdk_version="${OPENAI_PYTHON_VERSION:-2.32.0}"
python_bin="${PYTHON:-python3}"
tmpdir="$(mktemp -d "${TMPDIR:-/tmp}/one-llm-router-openai-sdk.XXXXXX")"
trap 'rm -rf "${tmpdir}"' EXIT

test "${LIVE_UPSTREAM_SMOKE:-}" = "1" || {
  echo "LIVE_UPSTREAM_SMOKE=1 is required" >&2
  exit 1
}
test -n "${LIVE_OPENAI_API_KEY:-}" || {
  echo "LIVE_OPENAI_API_KEY is required" >&2
  exit 1
}
test -n "${LIVE_OPENAI_BASE_URL:-}" || {
  echo "LIVE_OPENAI_BASE_URL is required" >&2
  exit 1
}
test -n "${LIVE_OPENAI_MODEL:-}" || {
  echo "LIVE_OPENAI_MODEL is required" >&2
  exit 1
}

"${python_bin}" -m venv "${tmpdir}/venv"
# shellcheck source=/dev/null
source "${tmpdir}/venv/bin/activate"
python -m pip install --disable-pip-version-check --quiet --upgrade pip
python -m pip install --disable-pip-version-check --quiet "openai==${sdk_version}"
python scripts/openai-sdk-compat-smoke.py
