# one-llm-router Constitution

## 元原则：第一性原理思考

请使用第一性原理思考。你不能总是假设我非常清楚自己想要什么和该怎么得到。请保持审慎，从原始需求和问题出发，如果动机和目标不清晰，停下来和我讨论。如果目标清晰但是路径不是最短，告诉我，并且建议更好的办法。

此原则高于便利性。为了"赶进度"而跳过澄清，属于违宪行为。

## Core Principles

### 1. Spec-First
Specifications are the source of truth. Code serves specifications.
All feature work starts from a spec, never directly from code.
Updating a feature means updating its spec first, then regenerating affected artifacts.

### 2. Simplicity Gate
- Maximum 3 sub-projects for initial implementation
- No future-proofing or speculative features
- Use framework features directly; avoid unnecessary abstraction layers
- If a gate violation is necessary, document justification in the plan's Complexity Tracking section

### 3. Test-First
Tests define behavior before implementation.
Execution order: Contract tests → Integration tests → Unit tests.
No implementation code before tests exist and fail (Red phase).

### 4. Clarification Mandate
Uncertainty MUST be marked with `[NEEDS CLARIFICATION: specific question]`.
Never guess on decisions that impact: scope, security, or user experience.
Maximum 3 clarification markers per spec — make informed defaults for everything else.

### 5. Traceability
Every technical decision traces back to a specific requirement in the spec.
Every requirement traces forward to implementation in code.
Orphaned code and orphaned specs are defects.

### 6. Reversibility
Prefer designs that can be rolled back safely.
Irreversible changes require:
- Explicit justification in the plan
- User approval before implementation

### 7. Integration-First Testing
Prefer real environments over mocks when feasible.
Contract tests are mandatory before implementation begins.
Use mocks only for external services outside your control.

## Phase Gates

| Phase | Entry Gate |
|------|-----------|
| SPECIFY | Project initialized (AGENTS.md + constitution exist) |
| PLAN | Spec has zero `[NEEDS CLARIFICATION]` markers |
| TASKS | Plan passes all Constitution checks |
| IMPLEMENT | Tasks list exists with dependencies resolved |
| TEST | Implementation complete for target scope |
| VERIFY | All tests pass |
| REVIEW | Spec-code consistency verified |
| DELIVER | Review approved, no blocking issues |

## Governance

Modifications to this constitution require:
- Documented rationale for the change
- Review and approval by project maintainers
- Assessment of impact on existing specs and plans

**Version**: 1.0 | **Created**: 2026-04-15
