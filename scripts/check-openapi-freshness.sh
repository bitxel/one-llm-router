#!/usr/bin/env bash
# scripts/check-openapi-freshness.sh — CI "OpenAPI freshness" gate for
# feature 003+ (T-008).
#
# What this enforces
# ------------------
#
# Per AGENTS.md §"API Contract" rule #2, every API change must
# regenerate BOTH the Go server stubs (internal/generated/adminapi/)
# AND the TS client (frontend/src/generated/openapi/) in the SAME PR.
# Rule #3 mandates the CI gate implements this via
# `git diff --exit-code` post-regeneration.
#
# This script is the single, authoritative mechanical gate. It runs
# on every PR (via .github/workflows/ci.yaml job `openapi-freshness`)
# AND is runnable locally before push to catch drift early:
#
#     $ bash scripts/check-openapi-freshness.sh
#
# Steps (in order — each must pass before the next runs):
#
#   1. `redocly lint openapi/admin.yaml --config openapi/redocly.yaml`
#      Validates the spec itself is shape-correct. If the spec is
#      broken, there is no point in running codegen — it would emit
#      garbage or crash. `redocly` is pinned as a workspace-local
#      devDependency (frontend/package.json:
#      @redocly/cli@1.34.10 exact) so the lint is reproducible and
#      offline-friendly; no `npx` registry fetch per run.
#
#   2. `bash scripts/codegen-go.sh` — regenerates
#      internal/generated/adminapi/{types,server}.gen.go via the
#      pinned oapi-codegen version from tools/oapi-codegen.go.
#
#   3. `bash scripts/codegen-frontend.sh` — regenerates
#      frontend/src/generated/openapi/** via the pinned
#      @hey-api/openapi-ts version from frontend/package.json.
#
#   4. `git diff --exit-code` scoped to JUST the two generated
#      trees. Non-zero exit ⇒ either (a) the author touched
#      admin.yaml without regenerating, or (b) one of the pinned
#      codegens produced output that differs from what is committed.
#      Either way the PR needs a regen + commit before it can land.
#
# Scope of the diff check
# -----------------------
#
# We deliberately do NOT run `git diff --exit-code` with no path
# filter — a dirty working tree (say the author has WIP edits in
# application code) would cause false positives that are not this
# gate's concern. We check ONLY:
#
#   - internal/generated/adminapi/
#   - frontend/src/generated/openapi/
#
# Other drift is caught by the separate lint/test/typecheck gates.
#
# Local-dev ergonomics
# --------------------
#
# The script is idempotent. If a dev runs it locally on a branch
# where admin.yaml was edited without regenerating, the script:
#
#   - Regenerates both trees in place (modifying the working tree).
#   - Fails with `git diff` showing the stale vs fresh delta.
#   - Leaves the fresh output on disk so the dev can just
#     `git add -A && git commit --amend` to land the fix.
#
# On a clean checkout with everything up-to-date, the script is a
# no-op (redocly passes, regenerators produce byte-identical output,
# `git diff` is empty, exit 0).
#
# Exit codes
# ----------
#
#   0 — spec lints, both generators ran, working tree has no
#       unexpected drift in the two generated subtrees.
#   1 — something in the pipeline failed. Stderr includes a
#       human-friendly "what went wrong and how to fix it" message
#       AND the offending `git diff` when relevant.

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

SPEC="openapi/admin.yaml"
REDOCLY_CONFIG="openapi/redocly.yaml"
GO_GENERATED_DIR="internal/generated/adminapi"
FE_GENERATED_DIR="frontend/src/generated/openapi"

log()  { printf '[openapi-freshness] %s\n' "$*"; }
# Errors go to stderr so CI log aggregators classify them correctly
# and so downstream `| tee` invocations keep the right stream.
err()  { printf '[openapi-freshness][FAIL] %s\n' "$*" >&2; }

# --- Pre-flight ---
[ -f "$SPEC" ]            || { err "spec not found: $SPEC"; exit 1; }
[ -f "$REDOCLY_CONFIG" ]  || { err "redocly config not found: $REDOCLY_CONFIG"; exit 1; }

# Fail loudly rather than silently skip — we want the gate to be
# non-conditional. Either you have the required toolchain or you
# cannot run the gate.
command -v git >/dev/null 2>&1 || { err "git not on PATH"; exit 1; }

# Locate the pnpm-installed redocly binary. We could call
# `pnpm --dir frontend exec redocly` but spawning pnpm shells adds
# ~1s of overhead per invocation; invoking the binary directly is
# equally reproducible (the binary was installed by pnpm from the
# lockfile) and faster.
REDOCLY_BIN="$ROOT_DIR/frontend/node_modules/.bin/redocly"
if ! [ -x "$REDOCLY_BIN" ]; then
  err "@redocly/cli is not installed; run \`pnpm --dir frontend install\` first"
  err "(pinned version lives in frontend/package.json devDependencies)"
  exit 1
fi

# --- Step 1: lint the spec ---
#
# Redocly lint classifies findings as ERROR or WARNING; the CLI exits
# non-zero ONLY on ERROR (warnings are informational — see
# https://redocly.com/docs/cli/v1/commands/lint#exit-codes ). The
# committed `openapi/redocly.yaml` extends `recommended` and turns off
# `operation-4xx-response` (the envelope policy intentionally forbids
# 4xx on /api/admin/*). Warnings in the current spec (private-server
# URL, unused APIKeyRotateRequest schema deferred to 004) are
# acknowledged and do NOT block CI; if the spec ever emits a lint
# ERROR, this gate blocks the PR until the author either fixes it or
# downgrades the rule in `redocly.yaml` with justification.
#
# We do not pass `--max-problems` or `--format=stylish` — the defaults
# print human-readable output that is ideal for CI log inspection.
log "redocly lint $SPEC (config: $REDOCLY_CONFIG)"
if ! "$REDOCLY_BIN" lint "$SPEC" --config "$REDOCLY_CONFIG"; then
  err "redocly lint failed — fix the issues above before regenerating"
  exit 1
fi

# --- Step 2: regenerate Go artefacts ---
log "regenerating $GO_GENERATED_DIR/ via scripts/codegen-go.sh"
if ! bash scripts/codegen-go.sh; then
  err "Go codegen failed (scripts/codegen-go.sh) — see error above"
  exit 1
fi

# --- Step 3: regenerate TS artefacts ---
log "regenerating $FE_GENERATED_DIR/ via scripts/codegen-frontend.sh"
if ! bash scripts/codegen-frontend.sh; then
  err "Frontend codegen failed (scripts/codegen-frontend.sh) — see error above"
  exit 1
fi

# --- Step 4: check generated trees are unchanged ---
#
# `git diff --exit-code -- <paths>` exits 0 iff there is no diff in
# the specified paths. Using `--` is mandatory — it disambiguates
# paths from revisions (otherwise a path like `HEAD` would be mis-
# parsed). We pass `-U1` so the printed diff is compact but still
# readable for the eventual CI log inspector.
log "checking $GO_GENERATED_DIR/ + $FE_GENERATED_DIR/ for drift"
if ! git diff --exit-code -U1 -- "$GO_GENERATED_DIR" "$FE_GENERATED_DIR"; then
  err ""
  err "Generated artefacts are stale relative to $SPEC."
  err ""
  err "What to do:"
  err "  1. Review the diff printed above."
  err "  2. Commit the regenerated files:"
  err "       git add $GO_GENERATED_DIR $FE_GENERATED_DIR"
  err "       git commit --amend --no-edit   # or a fresh commit"
  err "  3. Re-run this script locally to confirm clean exit."
  err ""
  err "Root cause is usually one of:"
  err "  - You edited $SPEC without running 'make codegen' (or the"
  err "    two codegen-*.sh scripts)."
  err "  - A codegen tool version was bumped; the diff reflects the"
  err "    re-emission under the new version."
  err ""
  exit 1
fi

log "OK — spec lints clean, both codegens byte-stable, no drift."
