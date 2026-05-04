// Package presentation hosts shared UI-facing mapping helpers that sit below
// individual handler packages but above the domain model.
package presentation

// PlanTypeLabel maps a raw OpenAI plan slug (as decoded from the
// token's `https://api.openai.com/auth.chatgpt_plan_type` claim) to the
// human-readable string every UI agrees on.
//
// Any unrecognised value is returned verbatim so callers do not silently
// erase a provider value that the backend has not learned yet.
func PlanTypeLabel(raw string) string {
	switch raw {
	case "plus", "chatgpt-plus":
		return "ChatGPT Plus"
	case "pro", "prolite", "chatgpt-pro":
		return "ChatGPT Pro"
	case "business", "chatgpt-business":
		return "ChatGPT Business"
	case "team", "chatgpt-team":
		return "ChatGPT Team"
	case "enterprise", "chatgpt-enterprise":
		return "ChatGPT Enterprise"
	default:
		return raw
	}
}
