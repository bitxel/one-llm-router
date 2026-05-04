package buildinfo

import (
	"testing"
)

// Note: these tests assume the package globals are at their link-time
// default values. The test binary is built without -ldflags -X
// injection, so defaults apply. Tests that mutate the globals MUST
// restore them via t.Cleanup to avoid cross-test leakage.

func TestDefaults_AreDevSentinels(t *testing.T) {
	t.Parallel()
	if Version != "dev" {
		t.Errorf("Version default = %q, want %q — did -ldflags accidentally land on the test binary?", Version, "dev")
	}
	if GitSHA != "unknown" {
		t.Errorf("GitSHA default = %q, want %q", GitSHA, "unknown")
	}
	if BuiltAt != "unknown" {
		t.Errorf("BuiltAt default = %q, want %q", BuiltAt, "unknown")
	}
}

func TestGet_ReflectsCurrentValues(t *testing.T) {
	// Not Parallel: mutates package globals.
	restore := swap(t, "v0.2.0", "abc1234", "2026-04-16T12:34:56Z")
	defer restore()

	snap := Get()
	if snap.Version != "v0.2.0" {
		t.Errorf("Snapshot.Version = %q, want v0.2.0", snap.Version)
	}
	if snap.GitSHA != "abc1234" {
		t.Errorf("Snapshot.GitSHA = %q, want abc1234", snap.GitSHA)
	}
	if snap.BuiltAt != "2026-04-16T12:34:56Z" {
		t.Errorf("Snapshot.BuiltAt = %q, want ISO 8601 UTC", snap.BuiltAt)
	}
}

func TestMap_MatchesWireContract(t *testing.T) {
	// Not Parallel: mutates package globals.
	restore := swap(t, "v0.3.1", "deadbee", "2026-04-16T12:34:56Z")
	defer restore()

	m := Map()
	wantKeys := map[string]string{
		"router_version":  "v0.3.1",
		"router_git_sha":  "deadbee",
		"router_built_at": "2026-04-16T12:34:56Z",
	}
	if len(m) != len(wantKeys) {
		t.Errorf("Map() has %d keys, want %d", len(m), len(wantKeys))
	}
	for k, want := range wantKeys {
		if got, ok := m[k]; !ok {
			t.Errorf("Map() missing key %q", k)
		} else if got != want {
			t.Errorf("Map()[%q] = %q, want %q", k, got, want)
		}
	}
}

func TestMap_IsFreshCopyEachCall(t *testing.T) {
	// Not Parallel (indirectly: Map() reads globals; but we don't
	// mutate them here).
	a := Map()
	b := Map()
	a["router_version"] = "mutated"
	if b["router_version"] == "mutated" {
		t.Error("Map() returned the same underlying map on two calls")
	}
}

func TestIsDev_TrueWhenAllDefaults(t *testing.T) {
	t.Parallel()
	if !IsDev() {
		t.Errorf("IsDev() = false, want true (test binary has no ldflags)")
	}
}

func TestIsDev_FalseWhenAnyInjected(t *testing.T) {
	// Not Parallel: mutates package globals.
	cases := []struct {
		name    string
		v, s, b string
	}{
		{"version_only", "v0.2.0", "unknown", "unknown"},
		{"sha_only", "dev", "abc1234", "unknown"},
		{"builtat_only", "dev", "unknown", "2026-04-16T12:34:56Z"},
		{"all_three", "v0.2.0", "abc1234", "2026-04-16T12:34:56Z"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			restore := swap(t, tc.v, tc.s, tc.b)
			defer restore()
			if IsDev() {
				t.Errorf("IsDev() = true, want false for case %s", tc.name)
			}
		})
	}
}

// swap mutates the three package globals, returning a restore closure
// callers invoke via defer. Tests calling swap MUST NOT t.Parallel().
func swap(t *testing.T, version, sha, builtAt string) func() {
	t.Helper()
	ov, os, ob := Version, GitSHA, BuiltAt
	Version = version
	GitSHA = sha
	BuiltAt = builtAt
	return func() {
		Version = ov
		GitSHA = os
		BuiltAt = ob
	}
}
