# Spec Quality Checklist: Multi-mode Codex Authentication

**Spec**: specs/003-multi-mode-codex-auth/spec.md
**Created**: 2026-04-15
**Updated**: 2026-04-15
**Status**: PASS (0 `[NEEDS CLARIFICATION]` markers remain; 2 clarifications resolved this session)

## Content Quality

- [X] No implementation details (languages, frameworks, APIs)
- [X] Focused on user value and business needs
- [X] Written for non-technical stakeholders
- [X] All mandatory sections completed

## Requirement Completeness

- [X] No [NEEDS CLARIFICATION] markers remain
- [X] Requirements are testable and unambiguous
- [X] Success criteria are measurable and tech-agnostic
- [X] All acceptance scenarios defined with Given/When/Then
- [X] Edge cases identified for each User Story
- [X] Scope clearly bounded (In Scope + Out of Scope)
- [X] Assumptions documented

## Coverage Scan Results

| Category | Status |
|----------|--------|
| Functional Scope | Clear |
| Domain & Data | Clear |
| UX Flow | Clear |
| Non-Functional | Clear |
| Edge Cases | Clear |
| Constraints | Clear |

## Notes

- Reference implementation: `~/app/project/codex-lb` (app/modules/oauth + app/modules/accounts/auth_manager.py).
- Backward compatibility with 002 API-key rows is an explicit FR (FR-013 + US-2); 003 is purely additive.
- Non-OpenAI OAuth providers (Anthropic, Azure, local OIDC) are out-of-scope by design.
- **FR-003 decision (2026-04-15)**: tokens stored plaintext on disk in 003 (matches 002 API-key posture); off-host redaction mandatory; sealed-bytes-ready column shape reserved for the future key-vault plugin.
- **FR-014 decision (2026-04-15)**: Option B — explicit "Export auth.json" button per OAuth account; every export emits a `oauth_auth_json_exported` WARN audit event (without token bytes). No dedicated rate limit — shares the generic per-operator admin-API rate envelope (operator feedback 2026-04-15 rejected the 10-per-account-24h throttle). API-key accounts keep 002's "no plaintext read-back" policy.
- **FR-011a decision (2026-04-15)**: email + plan_type ONLY (human-readable label for `chatgpt-plus` / `chatgpt-team` / `chatgpt-enterprise`, raw fallback for unknown) MUST render on both the account list row and the detail panel for every OAuth account — no extra click. API-key rows keep 002 field set.
- 6 user stories now (US-6 added 2026-04-15 for auth.json import). 15 FRs / 9 NFRs after FR-011a addition and dedicated-rate-limit removal. Storage Layout section added to explicitly document what 003 writes to disk per account.
