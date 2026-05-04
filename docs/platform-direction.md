# Platform Direction and Roadmap

**Updated**: 2026-04-26

## Product Direction

one-llm-router is a shared routing and control plane for AI coding clients. The product goal is not to clone every feature from `codex-lb`; it is to create a smaller platform that proves three outcomes first:

1. Internal developers can use Codex through one managed endpoint.
2. Operators can pool and control multiple upstream accounts without distributing those credentials.
3. The platform can expand to additional client protocols later without re-platforming.

## Capability Principles

### Keep the launch small
- Start with Codex only.
- Start with one region and one instance.
- Start with a small operator surface before broad admin workflows.
- Add only the minimum data needed to route, investigate, and control traffic.
- No authentication in MVP (internal network).

### Separate the right responsibilities
- **Ingress protocol adapters** define how clients talk to the router.
- **Provider adapters** define how the router talks to upstream services.
- **Core routing** owns account selection, session continuity, request logging, and policy enforcement.
- **Control plane** owns accounts, history queries, and (later) usage summaries.

### Prefer additive evolution
- New protocols should be additive launches.
- Existing Codex behavior should remain stable when new protocols are added.
- Operational surfaces should grow from observed demand, not from speculation.

## Ingress Plan

### P0: Data Plane (MVP)
- Provider-compatible `/v1/*` data-plane facade for the explicitly supported Feature 006 operation matrix
- API-key accounts forward supported operations transparently to the OpenAI Platform-compatible upstream base URL
- ChatGPT OAuth accounts use explicit ChatGPT Codex backend mappings for Responses traffic, Codex Responses WebSocket, the Feature 006 Chat Completions adapter, selected Codex-native `/backend-api/*` compatibility paths, and codex-lb-shaped usage surfaces
- SSE response parsing for token usage extraction

### P0: Control Plane (MVP)
- Upstream account management (add/enable/disable/delete)
- Request history search
- Session key resolver (consistent hash lookup, not stored mappings)
- Health endpoint

### Deferred
- Client API key issuance and validation
- Admin API authentication and RBAC
- Quota and billing summary aggregation
- OpenAI Realtime WebSocket/WebRTC/SIP ingress
- OpenAI-compatible operations outside the Feature 006 matrix
- Claude Code ingress
- Internal bridge ingress for multi-instance routing

## What to Copy From `codex-lb`

### Copy now (simplified for MVP)
- Multi-account pooling → round-robin load balancing
- Session stickiness for follow-up requests → consistent hashing (stateless, deterministic)
- Searchable request logs → database (SQLite/PostgreSQL) with 30-day retention

### Delay
- Client API keys
- Usage-weighted load balancing
- Account health and quarantine logic (automatic)
- Retry policies
- Browser dashboard
- TOTP and bootstrap flows
- Firewall feature set
- Quota prediction and depletion forecasting
- Multi-instance leader election
- OpenAI `/v1/audio/transcriptions` and broader media workflows
- Broad OpenAI compatibility layers outside the Feature 006 matrix

## Roadmap

### Phase 1: Codex Router MVP (current)

Deliver a complete, deployable Codex routing proxy with multi-account support.

- Provider-compatible `/v1/*` facade for Codex clients
- API-key transport: OpenAI Platform-compatible `/v1/*`
- OAuth transport: ChatGPT Codex backend for `/v1/responses`, `WS /v1/responses`, `/v1/responses/compact`, `/v1/chat/completions`, OAuth `GET /v1/models` facade, selected `/backend-api/*` compatibility paths, and codex-lb-shaped usage surfaces. `/v1/audio/transcriptions` is deferred from 006.
- Multi-account pooling with round-robin selection
- Session continuity via consistent hashing (stateless, deterministic)
- Operator account management (add/enable/disable/delete via Admin API)
- Request history with 30-day retention (SQLite default, PostgreSQL for scale)
- SSE parsing for token usage extraction (input, output, cached, reasoning)
- Configurable request/response body logging
- Single-port deployment with path-prefix routing
- Single-node Go binary + SQLite (default) or PostgreSQL

**Exit gate**: at least 3 internal teams can route Codex traffic through the platform consistently, operators can disable one bad account without breaking new traffic.

### Phase 2: Client Management & Security

- Client API key issuance and validation
- Admin API authentication (shared secret or RBAC)
- Rate limiting per client key
- Upstream API key encryption at rest

**Exit gate**: the platform authenticates both client and operator traffic and can limit per-client usage.

### Phase 3: Production Hardening

- Account health auto-detection and quarantine (consecutive failure → auto-disable)
- Configurable retry policies for pre-response failures
- Usage summary aggregation views
- Usage-weighted load balancing
- Additional streaming transport hardening beyond the 006 Codex Responses WebSocket relay
- prompt_cache_key-based affinity
- Readiness/liveness probes and operational monitoring

**Exit gate**: the platform can survive routine account failures automatically and operators can review aggregated usage.

### Phase 4: Compatibility Expansion
- Expand the OpenAI-compatible operation matrix if client demand justifies it
- Add a protocol capability registry for additive launches

**Exit gate**: a second ingress can launch without breaking existing Codex clients

### Phase 5: Claude Code
- Add Claude Code as a separately scoped feature
- Reuse control plane, routing policy, and observability where possible
- Avoid rewriting Codex behavior to make Claude Code fit

**Exit gate**: Claude Code support ships as an additive feature, not a platform rewrite

## Decision Filters

Before adding any feature, ask:

1. Does it improve routing reliability, operator control, or client adoption in the next 90 days?
2. Can the feature be postponed until observed usage proves the need?
3. Does the feature preserve additive compatibility for future protocols?
4. Can the feature be rolled back safely?

If the answer to the first question is no, the feature should not enter the active roadmap.

## Immediate Follow-Up

- Use `specs/006-openai-api-gateway/spec.md` as the source of truth for the active data-plane compatibility scope.
- Keep unsupported/deferred OpenAI families explicit in the operation matrix until they receive their own spec, logging policy, and tests.
- Do not open Claude Code work until the Codex/OpenAI-compatible gateway path proves real usage.
