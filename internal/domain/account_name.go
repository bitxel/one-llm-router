package domain

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	AccountNameMinLen = 1
	AccountNameMaxLen = 64
)

// ValidAccountName reports whether value is acceptable as an operator
// account name: non-empty after trimming surrounding whitespace, at
// most AccountNameMaxLen characters (counted as runes), and free of
// control characters (unicode.IsControl, which also covers the C1
// range U+0080–U+009F).
//
// The character set is deliberately unrestricted. Account names are
// display labels only — they never reach URL paths (routes use numeric
// ids), HTTP headers, file paths, or auth.json — and OAuth rows already
// carry email-derived names containing ".", "@", and spaces. The shared
// helper keeps the setup wizard, the admin create/edit surface, and the
// frontend bound to one rule.
//
// Length note: the frontend applies .max(64) on the JS string length
// (UTF-16 code units), which is stricter than this rune bound for
// astral-plane characters (e.g. an emoji counts as 1 rune but 2 UTF-16
// units). That direction is safe — a name the frontend accepts always
// fits the rune bound too, so the server never rejects a client-valid
// name on length.
func ValidAccountName(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}
	if utf8.RuneCountInString(trimmed) > AccountNameMaxLen {
		return false
	}
	for _, r := range trimmed {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
