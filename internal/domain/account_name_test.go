package domain

import (
	"strings"
	"testing"
)

func TestValidAccountName(t *testing.T) {
	ok := []string{
		"a",
		"prod-01",
		"my_account",
		"has space",
		"中文账户",
		"alice@example.com",
		"  padded  ", // trimmed before evaluation
	}
	for _, s := range ok {
		if !ValidAccountName(s) {
			t.Errorf("ValidAccountName(%q) = false, want true", s)
		}
	}

	bad := []string{
		"",
		"   ",
		strings.Repeat("a", AccountNameMaxLen+1),
		strings.Repeat("中", AccountNameMaxLen+1),
		"ctrl\x01name",
		"tab\there",
		"new\nline",
	}
	for _, s := range bad {
		if ValidAccountName(s) {
			t.Errorf("ValidAccountName(%q) = true, want false", s)
		}
	}
}

func TestValidAccountName_RuneBoundMatchesFrontend(t *testing.T) {
	// 64 Chinese characters is exactly the JS string-length max; the
	// rune-count bound must accept it while 65 must be rejected.
	exactly := strings.Repeat("中", AccountNameMaxLen)
	if !ValidAccountName(exactly) {
		t.Errorf("ValidAccountName(64 runes) = false, want true")
	}
	if ValidAccountName(exactly + "中") {
		t.Errorf("ValidAccountName(65 runes) = true, want false")
	}
}
