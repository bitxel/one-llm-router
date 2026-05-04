package errcode_test

// Go ↔ TS errcode parity (C-008 / T-010).
//
// The frontend and backend both ship a copy of the envelope code
// registry (Go: internal/api/errcode/codes.go, TS:
// frontend/src/lib/errcode.ts). Any drift silently breaks operator
// UX — the banner says one thing, the server logs another. This test
// parses the TS source as text and asserts every entry in
// `CodeSymbols` maps to the same symbol the Go Symbol() returns.
//
// The parser is intentionally minimal: we don't run a JS engine,
// we just scan for the literal lines that matter. Anyone rewriting
// the TS file in a way that breaks the regex will see a very clear
// failure naming the file and the pattern they must preserve.
//
// Running: covered by `go test ./internal/api/errcode/...`.

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/user/one-llm-router/internal/api/errcode"
)

// locateErrcodeTS walks up from the test's working directory looking
// for `frontend/src/lib/errcode.ts`. Allows the test to run from any
// sub-package without hard-coding a relative path.
func locateErrcodeTS(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 10; i++ {
		candidate := filepath.Join(dir, "frontend", "src", "lib", "errcode.ts")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("could not locate frontend/src/lib/errcode.ts from cwd; is the repo root reachable?")
	return ""
}

// constLine matches: `export const Identifier = 1234`
var constLine = regexp.MustCompile(`(?m)^export const (\w+) = (-?\d+)\b`)

// symbolLine matches a CodeSymbols entry `  [Identifier]: 'symbol',`.
var symbolLine = regexp.MustCompile(`(?m)^\s*\[(\w+)\]:\s*'([a-z0-9_]+)'`)

func TestErrcode_TSParity(t *testing.T) {
	path := locateErrcodeTS(t)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	src := string(raw)

	// Step 1: build identifier → integer code from `export const` lines.
	nameToCode := map[string]int{}
	for _, m := range constLine.FindAllStringSubmatch(src, -1) {
		name := m[1]
		code, err := strconv.Atoi(m[2])
		if err != nil {
			t.Fatalf("cannot parse int for %s: %v", name, err)
		}
		nameToCode[name] = code
	}
	if len(nameToCode) < 20 {
		t.Fatalf("only parsed %d TS codes; parser regex likely drifted", len(nameToCode))
	}

	// Step 2: for each CodeSymbols entry, look up the int via the
	// previous step and confirm the Go side agrees on the symbol.
	seen := 0
	for _, m := range symbolLine.FindAllStringSubmatch(src, -1) {
		name, sym := m[1], m[2]
		code, ok := nameToCode[name]
		if !ok {
			t.Errorf("CodeSymbols references unknown identifier %q", name)
			continue
		}
		got := errcode.Symbol(code)
		if got != sym {
			t.Errorf("code %d (%s): TS says %q, Go Symbol says %q — fix one of the registries",
				code, name, sym, got)
		}
		seen++
	}
	if seen < 20 {
		t.Fatalf("only matched %d CodeSymbols entries; parser regex likely drifted", seen)
	}

	// Step 3: every code that Go registers MUST also appear in the
	// TS file. Catch the "Go added a symbol, TS forgot to mirror"
	// direction of drift.
	goCodes := []int{
		errcode.OK, errcode.Unknown,
		errcode.AccountNotFound, errcode.AccountNameConflict,
		errcode.InvalidAccountPayload, errcode.AccountAlreadyInState,
		errcode.RequestRecordNotFound, errcode.InvalidRequestFilter,
		errcode.SessionNotFound, errcode.InvalidPagination,
		errcode.NoAvailableAccount,
		errcode.DBUnavailable, errcode.InternalError,
		errcode.SetupAlreadyDone, errcode.InvalidDriver, errcode.InvalidDSN,
		errcode.InvalidAccountName, errcode.InvalidAPIKey,
		errcode.InvalidPluginFlag, errcode.InvalidRetention,
		errcode.MalformedBody, errcode.RequestBodyTooLarge,
		errcode.SetupRequired, errcode.UnknownConfigKey,
		errcode.EnvOverrideReadonly, errcode.InvalidLogLevel,
		errcode.InvalidAccountProvider, errcode.InvalidBaseURL,
		errcode.DBConnectFailed, errcode.MigrateFailed,
		errcode.CommitTxFailed, errcode.ConfigWriteFailed,
		// Feature 003 — OAuth / auth.json / refresh.
		errcode.OAuthFlowInProgress, errcode.InvalidOAuthProvider,
		errcode.OAuthStateMismatch, errcode.NoFlowInProgress,
		errcode.OAuthAlreadyConsumed, errcode.OAuthFlowExpired,
		errcode.InvalidCallbackURL, errcode.OAuthFlowIDMismatch,
		errcode.OAuthInvalidGrant, errcode.InvalidAuthJSONStructure,
		errcode.InvalidAuthJSON,
		errcode.OAuthModeRequiresFlowEndpoint, errcode.NotOAuthAccount,
		errcode.DeviceAuthUnavailable, errcode.OAuthUpstreamError,
		errcode.OAuthInternalError, errcode.OAuthStoreFailed,
		errcode.OAuthExportReadFailed,
		// Feature 004 — Account Playground.
		errcode.InvalidPlaygroundRequest, errcode.PlaygroundNoActiveAccount,
		errcode.PlaygroundAccountUnavailable, errcode.PlaygroundUpstreamError,
		errcode.PlaygroundUpstreamTimeout, errcode.PlaygroundResponseMalformed,
		errcode.PlaygroundResponseTooLarge, errcode.PlaygroundInternalError,
		// Feature 005 — Observability.
		errcode.DashboardInvalidFilter, errcode.DashboardInternalError,
	}
	for _, c := range goCodes {
		sym := errcode.Symbol(c)
		if !strings.Contains(src, "'"+sym+"'") {
			t.Errorf("Go code %d (%s) is not mirrored in %s", c, sym, path)
		}
	}
}
