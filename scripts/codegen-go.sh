#!/usr/bin/env bash
# scripts/codegen-go.sh — regenerate the Go admin-API types + server
# stubs from openapi/admin.yaml.
#
# This is one of two mirrors of the OpenAPI spec — the TypeScript
# client lives in scripts/codegen-frontend.sh (T-007) and the CI
# freshness gate (T-008) runs BOTH + `git diff --exit-code` to fail
# PRs that touch admin.yaml without regenerating.
#
# Why `go run` (not `go install` or `go tool`):
#   - `go install` would write a binary to $GOBIN/GOPATH/bin, which
#     is per-developer state that CI would have to pre-warm; `go run`
#     resolves the version straight from go.mod and stays hermetic.
#   - `go tool oapi-codegen` is the Go 1.24+ alternative, but would
#     require a second-level pin (`go get -tool`) that T-006 did not
#     specify. `go run` with a versioned import is the simplest
#     reproducible path. The version itself is pinned by the blank
#     import in tools/oapi-codegen.go + go.mod.
#
# Why the new `oapi-codegen/oapi-codegen` path (not `deepmap/`):
#   The oapi-codegen project moved organisations at v2.3.0 (see
#   https://github.com/oapi-codegen/oapi-codegen/discussions/1605).
#   tasks.md T-006 was drafted before the move; tools/oapi-codegen.go
#   documents this deliberate deviation.
#
# Exit codes:
#   0 — generation succeeded and the working tree reflects the spec.
#   1 — generation failed (spec has a schema issue, tool crashed, or
#       gofmt found a formatting drift we could not auto-fix).

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

CODEGEN_PKG="github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen"
SPEC="openapi/admin.yaml"
TYPES_CFG="openapi/codegen-types.yaml"
SERVER_CFG="openapi/codegen-server.yaml"
OUT_DIR="internal/generated/adminapi"

log() { printf '[codegen-go] %s\n' "$*"; }

[ -f "$SPEC" ]        || { printf '[codegen-go][FAIL] spec not found: %s\n' "$SPEC" >&2;        exit 1; }
[ -f "$TYPES_CFG" ]   || { printf '[codegen-go][FAIL] config not found: %s\n' "$TYPES_CFG" >&2;  exit 1; }
[ -f "$SERVER_CFG" ]  || { printf '[codegen-go][FAIL] config not found: %s\n' "$SERVER_CFG" >&2; exit 1; }

mkdir -p "$OUT_DIR"

# Stamp the pinned version in the script log so CI archives and
# local dev runs both carry a breadcrumb of which oapi-codegen emitted
# the current tree. When debugging "why did the diff flip?", grep this
# line first.
CODEGEN_VERSION="$(go list -m -f '{{.Version}}' github.com/oapi-codegen/oapi-codegen/v2 2>/dev/null || echo '(unknown)')"
log "oapi-codegen pinned at ${CODEGEN_VERSION}"

log "generating types → $OUT_DIR/types.gen.go"
go run "$CODEGEN_PKG" -config "$TYPES_CFG" "$SPEC"

log "generating server → $OUT_DIR/server.gen.go"
go run "$CODEGEN_PKG" -config "$SERVER_CFG" "$SPEC"

# Normalise formatting. oapi-codegen's emitted formatting can drift
# between releases (tab vs space in struct tags, trailing commas in
# slices, etc.); running gofmt guarantees the freshness gate stays
# idempotent across toolchain upgrades.
log "gofmt $OUT_DIR"
gofmt -w "$OUT_DIR"

log "done"
