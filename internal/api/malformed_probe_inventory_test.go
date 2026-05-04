package api_test

import (
	"sort"
	"testing"

	"github.com/user/one-llm-router/internal/api/testutil"
)

func TestMalformedProbeInventoryMatchesOpenAPI(t *testing.T) {
	t.Parallel()

	ops := map[string]struct{}{}
	names := map[string]struct{}{}
	for _, probe := range testutil.OAuthMalformedProbes(8 * 1024) {
		ops[probe.OperationID] = struct{}{}
		names[probe.Name] = struct{}{}
	}
	for _, probe := range testutil.AdminMalformedProbes(
		16*1024,
		64*1024,
		[]byte(`{}`),
		"multipart/form-data; boundary=fixture-malformed",
		[]byte{},
		"multipart/form-data; boundary=fixture-empty",
		[]byte("fixture-part-too-large"),
		"multipart/form-data; boundary=fixture-part-too-large",
		[]byte("fixture-preamble-flood"),
		"multipart/form-data; boundary=fixture-preamble-flood",
		1,
	) {
		ops[probe.OperationID] = struct{}{}
		names[probe.Name] = struct{}{}
	}

	got := make([]string, 0, len(ops))
	for op := range ops {
		got = append(got, op)
	}
	sort.Strings(got)

	want := testutil.DeclaredAdminOperationIDs(t)
	if len(got) != len(want) {
		t.Fatalf("malformed probe operation inventory size=%d want=%d got=%v want=%v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("malformed probe operation inventory mismatch at %d: got=%v want=%v", i, got, want)
		}
	}

	var missingNames []string
	for _, name := range testutil.RequiredMalformedProbeNames() {
		if _, ok := names[name]; !ok {
			missingNames = append(missingNames, name)
		}
	}
	if len(missingNames) > 0 {
		sort.Strings(missingNames)
		t.Fatalf("required malformed probes missing from matrix: %v", missingNames)
	}
}
