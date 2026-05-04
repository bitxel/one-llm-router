// @hey-api/openapi-ts configuration — regenerates the admin-API
// TypeScript client (types + SDK + fetch runtime bundle) from
// `openapi/admin.yaml`. Invoked by `scripts/codegen-frontend.sh`
// (T-007) and by the CI freshness gate (T-008).
//
// Mirrors the Go side (`openapi/codegen-types.yaml` +
// `openapi/codegen-server.yaml` + `scripts/codegen-go.sh`). The two
// halves of the pipeline stay byte-identical in terms of the schemas
// they consume — every `components.schemas` entry in `admin.yaml`
// emits a Go struct AND a TS interface AND (where relevant) a
// discriminated union on both sides.
//
// Pin strategy (AGENTS.md §API Contract rule #3 — "Generated code is
// committed; CI verifies freshness via `git diff --exit-code`"):
//
//   - `@hey-api/openapi-ts` is exact-pinned in `package.json`
//     (installed with `pnpm add -E`). CI runs the script and fails
//     on any unexpected diff, so a floating minor bump that
//     reshapes the emitter is caught the same day.
//   - Starting with `@hey-api/openapi-ts` v0.73.0 the fetch client
//     bundle ships inside the main package; there is no separate
//     `@hey-api/client-fetch` devDependency to track. The plugin
//     key `@hey-api/client-fetch` below is a stable identifier,
//     not a package name.
//
// See:
//   - `scripts/codegen-frontend.sh` (orchestrator)
//   - `frontend/src/lib/openapi-runtime.ts` (runtimeConfigPath target)
//   - `frontend/src/lib/router-api.ts` (hand-authored `callAdmin`
//     wrapper that unwraps the `{code,msg,data}` envelope and
//     throws `RouterApiError` on non-zero codes)
//   - `openapi/README.md` §Codegen pipeline

import { defineConfig } from '@hey-api/openapi-ts'

export default defineConfig({
  // `input` is resolved from the frontend/ directory (the pnpm
  // invocation's CWD). The spec lives at repository root, so we walk
  // one level up — keeping the single-source-of-truth file out of
  // any one sub-project.
  input: {
    path: '../openapi/admin.yaml',
  },
  output: {
    path: 'src/generated/openapi',
    // Disable post-processing (prettier / eslint / biome). The
    // Go side runs `gofmt -w` in `codegen-go.sh` because Go is our
    // primary lint target and shipping unformatted go files breaks
    // `golangci-lint`. On the TS side, `src/generated/openapi/**`
    // is excluded from `biome.json` (see §files.includes), so
    // emitting whatever @hey-api/openapi-ts' ts-morph printer
    // produces keeps the pipeline hermetic: no prettier dependency
    // to version-pin, no formatter-induced drift between emitter
    // patches, and the freshness `git diff --exit-code` gate still
    // detects any real schema change. `tsc --noEmit` enforces
    // type correctness of generated code at CI gate regardless of
    // whitespace.
    postProcess: [],
  },
  plugins: [
    {
      name: '@hey-api/client-fetch',
      // Runtime config injection — `openapi-runtime.ts` exports
      // `createClientConfig` which sets the same-origin baseUrl and
      // overrides `fetch` to attach `X-Request-Id` per FR-010.
      //
      // The generator embeds `runtimeConfigPath` VERBATIM into the
      // import statement emitted at `client.gen.ts` (see the
      // `external` branch in `init-*.mjs` → `symbolCreateClientConfig`).
      // The generated file lives at
      // `frontend/src/generated/openapi/client.gen.ts`, so the
      // correct relative specifier to `frontend/src/lib/openapi-runtime.ts`
      // is `../../lib/openapi-runtime`. NOTE the .ts suffix is
      // OMITTED — TypeScript's bundler resolver (see
      // `tsconfig.json` → `moduleResolution: "Bundler"`) handles the
      // extension, and Vite/Rollup rewrite it at build time. Adding
      // `.ts` would break `pnpm build` on platforms with strict
      // module resolution.
      //
      // Keeping the source of truth outside `src/generated/`
      // preserves the "generated/ never hand-edit" invariant
      // (mirrors the Go-side split between `*.gen.go` and
      // `strict_handler.go` under `internal/generated/adminapi/`).
      runtimeConfigPath: '../../lib/openapi-runtime',
    },
    {
      name: '@hey-api/sdk',
      // Flat tree-shakeable functions (one export per operation)
      // — matches how TanStack Query hooks will consume them:
      // `useQuery({ queryKey: [...], queryFn: () => oauthFlowStatus({...}) })`.
      // The alternative `single` (instance class) strategy loses
      // tree-shaking and produces a monolithic `Sdk` class that
      // our Biome + Vite bundle pipeline cannot dead-code-eliminate.
      operations: {
        strategy: 'flat',
      },
      // Do NOT enable `auth` — the 003 spec's `security:
      // [adminAuth]` block is declarative-only (see
      // `openapi/admin.yaml` §x-mvp-enforcement). Turning on
      // auth handling here would produce an `Authorization` header
      // that the MVP mux silently ignores, surfacing a phantom
      // dependency that the admin-auth plugin (feature 004+) will
      // need to un-wire.
    },
    {
      name: '@hey-api/typescript',
      // Emit every component schema as an exported TS type. The
      // handler package consumes every DTO directly, same as the
      // Go side's `skip-prune: true` in `openapi/codegen-types.yaml`.
      exportInlineEnums: true,
    },
  ],
})
