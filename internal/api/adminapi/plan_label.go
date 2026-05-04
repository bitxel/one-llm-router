package adminapi

import "github.com/user/one-llm-router/internal/presentation"

// PlanTypeLabel maps a raw OpenAI plan slug (as decoded from the
// id_token's `https://api.openai.com/auth.plan_type` claim) to the
// human-readable string every UI agrees on. FR-011a names three
// recognised values:
//
//	chatgpt-plus        → "ChatGPT Plus"
//	chatgpt-team        → "ChatGPT Team"
//	chatgpt-enterprise  → "ChatGPT Enterprise"
//
// Any other value — including a brand-new plan OpenAI ships before
// we update this table — is returned verbatim so the admin UI does
// not silently strip it. That "passthrough fallback" is the
// invariant; the mapping itself is politeness on top.
//
// Case sensitivity: the raw slugs come from provider tokens and are
// stable lowercase, so we match exactly. A differently-cased value
// from another provider falls through to passthrough; if/when that
// happens we can add canonicalisation — today, strict matching is
// safer than premature normalisation.
//
// Layering: this label function lives under `internal/api/adminapi`
// (the UI-facing adapter) rather than `internal/domain` because
// it is a presentation concern, not a business invariant — the
// domain knows `PlanType` the slug, the API layer knows how to
// render it for humans.
func PlanTypeLabel(raw string) string {
	return presentation.PlanTypeLabel(raw)
}
