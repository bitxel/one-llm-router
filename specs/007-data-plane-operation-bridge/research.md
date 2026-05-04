# Research: Data-Plane Operation Bridge

**Feature**: 007-data-plane-operation-bridge
**Spec**: `specs/007-data-plane-operation-bridge/spec.md`
**Created**: 2026-04-27
**Status**: Resolved baseline

## Decision 1: Use OperationBridge instead of Capability

- **Decision**: Name the executable per-operation adapter `OperationBridge`.
- **Rationale**:
  - The component is not only declaring support; it actively decodes client input, builds upstream requests, and selects client response adaptation.
  - `Capability` sounds like passive provider metadata and risks drifting toward provider-wide configuration flags.
  - `Bridge` accurately describes connecting a client-facing operation contract to a selected upstream contract.

| Alternative | Pros | Cons | Why Rejected |
|---|---|---|---|
| `Capability` | Common term for "provider can do X" | Too abstract; suggests static capability declaration | This feature needs executable bridge behavior |
| `Adapter` | Familiar for conversion | Too broad; response adapter already has a specific role | Would blur request bridge and response adaptation |
| `OperationBridge` | Explicit operation-to-upstream bridge | Slightly longer | Chosen for clarity |

## Decision 2: Keep DecodeClientRequest and BuildUpstreamRequest separate

- **Decision**: The public bridge boundary has both `DecodeClientRequest` and `BuildUpstreamRequest`.
- **Rationale**:
  - Decode errors are client contract validation failures.
  - Build errors can mean the selected upstream cannot honestly support the decoded client intent.
  - Separate phases produce clearer tests and better router error mapping.

| Alternative | Pros | Cons | Why Rejected |
|---|---|---|---|
| Single `Build` method | Simpler interface | Blurs invalid-client vs unsupported-upstream errors and creates larger black-box tests | Current bridge conversions are high-risk enough to keep boundaries visible |
| Many fine-grained methods | Very testable | Overfits current providers and increases interface burden | Private helper methods can provide fine-grained tests where needed |

## Decision 3: Avoid provider-wide RequestPolicy configuration

- **Decision**: Do not model provider behavior as a large `RequestPolicy` struct with fields like body mode, field policy, tool policy, and stream policy.
- **Rationale**:
  - Provider differences are often operation-specific and conditional.
  - A generic policy struct would grow into a configuration DSL and still need provider-specific code.
  - Go code is clearer and more testable for bridge logic that includes stream collection, field normalization, tool normalization, multipart forwarding, and WebSocket behavior.

| Alternative | Pros | Cons | Why Rejected |
|---|---|---|---|
| Provider-wide profile flags | Easy to inspect | Incorrect for operation-specific behavior | `store`, `stream`, tools, and response adapters differ by operation |
| Fine-grained RequestPolicy DSL | Centralized metadata | Hard to express complex conversions and response reconstruction | More complexity than direct bridge implementations |
| Code-owned bridges | Localizes provider logic | Requires tests per bridge | Chosen because behavior is concrete and operation-specific |

## Decision 4: Use `Contract` with `ClientContract` and `UpstreamContract`

- **Decision**: Use a single `Contract` type and direction-specific bridge accessors/fields named `ClientContract` and `UpstreamContract`.
- **Rationale**:
  - `WireContract` is precise but too verbose for common code.
  - `ClientContract` and `UpstreamContract` make direction explicit without creating separate duplicate type systems.
  - Contract mismatch is the key trigger for explicit bridging.

| Alternative | Pros | Cons | Why Rejected |
|---|---|---|---|
| `WireContract` | Very precise | Verbose and less readable | User explicitly preferred shorter `Contract` |
| Separate `ClientContract`/`UpstreamContract` types | Stronger type distinction | Duplicates the same value space | Field/accessor names provide enough direction |
| Use only operation id | Short | Cannot express different upstream contracts for the same client operation | Operation and contract answer different questions |

## Decision 5: Preserve 006 external behavior

- **Decision**: Feature 007 is behavior-preserving and must not expand the data-plane operation matrix.
- **Rationale**:
  - The immediate problem is architecture clarity and maintainability, not endpoint coverage.
  - Adding provider/endpoint scope during this refactor would obscure regression risk.
  - Existing 006 compatibility tests already define the external behavior that must be preserved.

## Dependency Versions (Verified 2026-04-27)

No new dependency is introduced by Feature 007.

| Package / Tool | Verified Version | Source | Notes |
|---|---|---|---|
| Go | `1.25.0` | `go.mod` | Existing project toolchain |
| `github.com/coder/websocket` | `v1.8.14` | `go.mod` | Existing 006 dependency, reused for WebSocket tests/relay only |
| `xorm.io/xorm` | `v1.3.11` | `go.mod` | No schema change planned |
| React | `^19.1.0` | `frontend/package.json` | No frontend change planned |
| Vite | `^6.2.5` | `frontend/package.json` | No frontend change planned |
| TypeScript | `~5.8.2` | `frontend/package.json` | No frontend change planned |
| pnpm | `10.30.3` | `frontend/package.json` | No frontend change planned |

## Brownfield Code Findings

- `internal/api/proxy_routes.go` already has a pure classifier but lacks stable OpID/BridgeID/Contract identities.
- `internal/api/proxy.go` currently orchestrates route classification, WebSocket relay, body policy, account selection, provider forwarding, and request recording.
- `internal/provider/openai/forwarder.go` currently switches between direct API-key forwarding, Codex mapping, models facade, and Chat adapter using account type and route behavior.
- `internal/provider/openai/codex_request.go` already captures important Codex normalization rules and should be reused rather than rewritten.
- `internal/provider/openai/chat_adapter.go` already captures Chat Completions facade behavior and should be moved behind a bridge boundary without weakening validation.

## Implications

- The first implementation task should be red tests around operation and bridge identity, not code movement.
- Low-level HTTP send/header helpers should remain boring and reusable.
- Bridge metadata should be safe identifiers, not raw request headers or bodies.
- If a bridge migration changes external behavior, that is a bug unless the spec is explicitly updated first.
