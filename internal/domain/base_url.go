// Package domain hosts cross-feature validation helpers for the
// router's immutable domain model. Keeping a single home for the
// rules that govern entity fields — here, the upstream account's
// base_url — prevents drift between the 001 admin CRUD path and the
// 002 setup-wizard path: both enter through NormalizeBaseURL /
// ValidateBaseURL and therefore apply the same constraints.
//
// Why domain, not core or setup:
//   - internal/core owns composition (selection, recording); it is a
//     consumer of domain rules, not the authority on them.
//   - internal/setup is a peer consumer; having it depend on core
//     would invert the layering.
//   - Every rule in this file is a property of the stored model
//     (what the repository will accept), not of a particular caller.
//
// All helpers are pure and goroutine-safe.
package domain

import (
	"net/url"
	"strings"
)

// BaseURLMaxLen caps upstream account base_url length. 256 mirrors the
// upstream proxy's cap and is large enough for every real-world API
// base (e.g. `https://api.openai.com`). Kept as an exported constant
// so callers can surface the limit to operators in UI hints.
const BaseURLMaxLen = 256

// ValidateBaseURL enforces a minimal set of sanity checks against an
// operator-supplied upstream base URL. Shared by:
//
//   - internal/core.AccountService.Create (POST /api/admin/accounts)
//   - internal/setup.Validator.BaseURL   (POST /api/setup/commit)
//
// It is not a full SSRF control — this is an operator-only admin API
// on an internal network — but it catches typos and garbage (missing
// scheme, relative URLs, query strings) that would otherwise
// lead to confusing upstream failures.
//
// Returns nil on success; a *ValidationError with Field="base_url" on
// failure. The caller maps Field/Message onto its own envelope shape
// (001 returns HTTP 400; 002 returns envelope code 2016 invalid_base_url).
//
// Rules, in order checked:
//
//  1. Length must be ≤ BaseURLMaxLen. Rejects pathological inputs
//     before running url.Parse.
//  2. url.Parse must succeed.
//  3. URL must be absolute (IsAbs == true).
//  4. Scheme must be "http" or "https" (case-insensitive).
//  5. Host must be non-empty.
//  6. Path is allowed — the router strips the leading /v1 from client
//     paths when constructing upstream URLs, so base_url can include
//     a path segment (e.g. "/v1" or "/zen/v1") without doubling.
//  7. Query string and fragment must be empty.
//
// Empty / nil input is the caller's responsibility — this function
// assumes the caller has already decided whether base_url is
// optional in its context.
func ValidateBaseURL(raw string) error {
	if len(raw) > BaseURLMaxLen {
		return &ValidationError{
			Field:   "base_url",
			Message: "base_url must be ≤256 characters",
		}
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return &ValidationError{Field: "base_url", Message: "invalid URL: " + err.Error()}
	}
	if !parsed.IsAbs() {
		return &ValidationError{Field: "base_url", Message: "must be an absolute URL"}
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return &ValidationError{Field: "base_url", Message: "scheme must be http or https"}
	}
	if parsed.Host == "" {
		return &ValidationError{Field: "base_url", Message: "host is required"}
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return &ValidationError{Field: "base_url", Message: "base_url must not contain query string or fragment"}
	}
	return nil
}
