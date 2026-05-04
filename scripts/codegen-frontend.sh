#!/usr/bin/env bash
# scripts/codegen-frontend.sh — regenerate the TypeScript admin-API
# client (types + SDK + bundled fetch runtime) from openapi/admin.yaml.
#
# This is the frontend mirror of scripts/codegen-go.sh. The CI
# freshness gate (T-008) runs BOTH scripts followed by
# `git diff --exit-code` to fail PRs that changed admin.yaml without
# regenerating BOTH Go and TS artefacts — keeping the contract
# bi-directionally tight.
#
# Why `pnpm exec` (and not `npx` / `pnpm dlx`):
#   - `pnpm exec` uses the workspace-local, exact-pinned binary from
#     `frontend/node_modules/.bin/openapi-ts`. That matches the
#     Go-side choice of `go run <versioned import>` — every codegen
#     invocation uses the version pinned in source control.
#   - `npx` / `pnpm dlx` would hit the registry and could resolve to
#     a newer emitter if the lockfile drifted, silently reshaping
#     the generated tree and breaking the freshness diff.
#
# Configuration lives in `frontend/openapi-ts.config.ts`
# (input path, plugins, output path). This script is deliberately
# thin — it only orchestrates the invocation and surfaces errors in
# a human-friendly CI log format.
#
# Exit codes:
#   0 — generation succeeded; working tree reflects the spec.
#   1 — generation failed (spec issue, tool crashed, or the pinned
#       openapi-ts version is not installed).

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FRONTEND_DIR="$ROOT_DIR/frontend"
SPEC="$ROOT_DIR/openapi/admin.yaml"
CONFIG="$FRONTEND_DIR/openapi-ts.config.ts"
OUT_DIR="$FRONTEND_DIR/src/generated/openapi"

log() { printf '[codegen-frontend] %s\n' "$*"; }

[ -f "$SPEC" ]   || { printf '[codegen-frontend][FAIL] spec not found: %s\n' "$SPEC" >&2;   exit 1; }
[ -f "$CONFIG" ] || { printf '[codegen-frontend][FAIL] config not found: %s\n' "$CONFIG" >&2; exit 1; }

cd "$FRONTEND_DIR"

# Stamp the pinned version in the log so CI archives carry a
# breadcrumb of which @hey-api/openapi-ts emitted the current tree.
# Matches the equivalent breadcrumb in codegen-go.sh; when debugging
# "why did the diff flip?", grep this line first.
CODEGEN_VERSION="$(node -e 'import("./node_modules/@hey-api/openapi-ts/package.json", { with: { type: "json" } }).then(m => process.stdout.write(m.default.version)).catch(() => process.stdout.write("(unknown)"))' 2>/dev/null || echo '(unknown)')"
log "@hey-api/openapi-ts pinned at ${CODEGEN_VERSION}"

if ! [ -x "$FRONTEND_DIR/node_modules/.bin/openapi-ts" ]; then
  printf '[codegen-frontend][FAIL] openapi-ts not installed; run `pnpm install` in %s first\n' "$FRONTEND_DIR" >&2
  exit 1
fi

mkdir -p "$OUT_DIR"

log "generating TS client → src/generated/openapi/ (via openapi-ts.config.ts)"
# `pnpm exec openapi-ts` picks up openapi-ts.config.ts in CWD
# (frontend/), which drives input/output/plugins. We pass no extra
# flags — any additional knobs belong in the config file so the
# Go-side `codegen-*.yaml` symmetry holds.
pnpm exec openapi-ts

log "done → $(cd "$OUT_DIR" && ls -1 | tr '\n' ' ')"
