// Package buildinfo carries the immutable build metadata injected by
// the linker at release time. Three fields are tracked, matching the
// 002 /api/admin/settings system block (data-model.md §system):
//
//   - Version — semantic-version string, typically the git tag
//     (e.g. "v0.2.0"). Defaults to "dev" for un-ldflagged builds.
//   - GitSHA  — 7-character short commit hash at build time. Defaults
//     to "unknown" so a mis-linked binary cannot masquerade as a real
//     build.
//   - BuiltAt — ISO 8601 UTC timestamp with second precision
//     (e.g. "2026-04-16T12:34:56Z"). Defaults to "unknown".
//
// Release builds override these via linker -X flags:
//
//	go build -ldflags "-X github.com/user/one-llm-router/internal/app/buildinfo.Version=v0.2.0 \
//	  -X github.com/user/one-llm-router/internal/app/buildinfo.GitSHA=abcdef1 \
//	  -X github.com/user/one-llm-router/internal/app/buildinfo.BuiltAt=2026-04-16T12:34:56Z" \
//	  ./cmd/one-llm-router
//
// Consumers MUST NOT mutate these variables at runtime. The values are
// exposed as package-level vars rather than constants solely so the
// linker can inject replacements — treat them as read-only. Any
// downstream code that needs a structured view of the triple should
// call Map() or Snapshot() rather than reading the vars directly, so
// the centralised dev-mode sentinel stays authoritative.
package buildinfo

// Version, GitSHA, and BuiltAt are the three fields the linker may
// override. They are package-level vars (not consts) because Go's
// -ldflags -X only supports vars.
//
// The default values below are intentionally human-readable — an
// operator who sees "dev / unknown / unknown" in /api/admin/settings
// immediately knows the binary was not built by CI.
var (
	Version = "dev"
	GitSHA  = "unknown"
	BuiltAt = "unknown"
)

// Snapshot is a read-only view of the three build-metadata values
// taken at call time. The returned struct is by-value so callers
// cannot accidentally mutate the package globals through it.
type Snapshot struct {
	Version string
	GitSHA  string
	BuiltAt string
}

// Get returns the current Snapshot. Safe to call from any goroutine
// (the package vars are written once at link time and never after).
func Get() Snapshot {
	return Snapshot{
		Version: Version,
		GitSHA:  GitSHA,
		BuiltAt: BuiltAt,
	}
}

// Map returns the build metadata formatted for JSON embedding into
// the /api/admin/settings system block. Keys match the wire contract
// defined in contracts/admin-api.md §GET /api/admin/settings:
//   - "router_version"
//   - "router_git_sha"
//   - "router_built_at"
//
// The function returns a fresh map per call so handlers can mutate
// their own copies (e.g. to add transient fields like "uptime_seconds")
// without corrupting the shared package state.
func Map() map[string]string {
	return map[string]string{
		"router_version":  Version,
		"router_git_sha":  GitSHA,
		"router_built_at": BuiltAt,
	}
}

// IsDev reports whether the binary was built without linker
// injection — i.e. it's running with all three defaults. Used by
// boot-time logging and optional health surfacing so the operator can
// detect "someone shipped an un-stamped binary".
func IsDev() bool {
	return Version == "dev" && GitSHA == "unknown" && BuiltAt == "unknown"
}
