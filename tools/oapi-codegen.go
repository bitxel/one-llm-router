//go:build tools

// Package tools is a dependency-parking placeholder. Every import here
// is a build-time-only tool whose binary we invoke via `go run` and
// whose version we want pinned into `go.mod` / `go.sum`.
//
// Why the `tools` build tag:
//
//   - The file is EXCLUDED from `go build ./...` so the tool binaries
//     never land in the production router image.
//   - `go mod tidy` still walks the imports under the `tools` tag so the
//     module stays pinned; without the blank import, `go mod tidy`
//     would evict the tool from go.sum and the codegen script would
//     silently upgrade on every CI run.
//
// Why `github.com/oapi-codegen/oapi-codegen/v2` (not `deepmap/...`):
//
//	The task text (specs/003-multi-mode-codex-auth/tasks.md T-006) was
//	drafted against the legacy import path
//	`github.com/deepmap/oapi-codegen/v2`. That repository moved to the
//	`oapi-codegen` GitHub organisation at version **v2.3.0** (see
//	https://github.com/oapi-codegen/oapi-codegen/discussions/1605).
//	Anything past v2.2.0 at the old path is stale — pulling in the
//	legacy coordinates would freeze us on a 2024-era release and miss
//	every subsequent bug fix. We deliberately use the new canonical
//	path; `scripts/codegen-go.sh` documents this in its header.
//
// Invocation: see scripts/codegen-go.sh and openapi/README.md
// §Codegen pipeline. Do NOT call `go install` on a floating `@latest`
// — that would bypass the pin we set here.
package tools

import (
	_ "github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen"
)
