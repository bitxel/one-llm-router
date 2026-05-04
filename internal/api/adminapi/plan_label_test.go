package adminapi

import "testing"

func TestPlanTypeLabel(t *testing.T) {
	t.Parallel()
	cases := []struct {
		raw  string
		want string
	}{
		// Current ChatGPT OAuth values from `chatgpt_plan_type`.
		{"plus", "ChatGPT Plus"},
		{"pro", "ChatGPT Pro"},
		{"prolite", "ChatGPT Pro"},
		{"business", "ChatGPT Business"},
		{"team", "ChatGPT Team"},
		{"enterprise", "ChatGPT Enterprise"},
		// Mapped — FR-011a "three known values".
		{"chatgpt-plus", "ChatGPT Plus"},
		{"chatgpt-team", "ChatGPT Team"},
		{"chatgpt-enterprise", "ChatGPT Enterprise"},
		// Passthrough — the "new plan not yet in our table" case.
		// FR-011a mandates we render the raw string rather than
		// drop it, otherwise the admin UI would silently lose info.
		{"chatgpt-edu", "chatgpt-edu"},
		{"", ""},
		// Canonicalisation is not part of the contract — a
		// differently-cased value passes through unchanged so a
		// future enhancement that adds case-folding shows up as a
		// failing test, not a silent behaviour change.
		{"ChatGPT-Plus", "ChatGPT-Plus"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.raw, func(t *testing.T) {
			t.Parallel()
			got := PlanTypeLabel(tc.raw)
			if got != tc.want {
				t.Errorf("PlanTypeLabel(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}
