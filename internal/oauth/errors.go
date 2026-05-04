package oauth

import (
	"errors"
	"fmt"
)

var (
	// ErrFlowInProgress maps to `code:3001 oauth_flow_in_progress` in
	// contracts/oauth-flow-api.md when a new flow collides with the
	// process-wide in-flight slot.
	ErrFlowInProgress = errors.New("oauth: flow in progress")

	// ErrFlowNotFound maps to `code:3008 flow_id_mismatch` on `/cancel`
	// and `code:3004 no_flow_in_progress` on `/browser/manual-callback`
	// in contracts/oauth-flow-api.md.
	ErrFlowNotFound = errors.New("oauth: flow not found")

	// ErrStateMismatch maps to plaintext `400 Bad Request` on the
	// loopback rail and envelope `code:3003 oauth_state_mismatch` on the
	// manual-paste rail in contracts/oauth-flow-api.md.
	ErrStateMismatch = errors.New("oauth: state mismatch")

	// ErrAlreadyConsumed maps to a silent loopback success/cancel render
	// and manual-paste envelope `code:3005 already_consumed` in
	// contracts/oauth-flow-api.md.
	ErrAlreadyConsumed = errors.New("oauth: already consumed")

	// ErrFlowExpired maps to plaintext `410 Gone` on the loopback rail
	// and envelope `code:3006 flow_expired` on the manual-paste rail in
	// contracts/oauth-flow-api.md.
	ErrFlowExpired = errors.New("oauth: flow expired")

	// ErrMalformedIDToken maps broken JWT payloads to a stable sentinel
	// so callers can bucket malformed `id_token` claims without parsing
	// error strings.
	ErrMalformedIDToken = errors.New("oauth: malformed id_token")

	// errConcurrentRefreshConflict is an internal-only sentinel that
	// marks a refresh write losing an optimistic-lock race against a
	// concurrent credential update. It is not mapped
	// directly to a wire error; the caller decides whether to retry.
	errConcurrentRefreshConflict = errors.New("oauth: concurrent refresh conflict")
)

// StoreError wraps a write-side storage failure after OAuth already
// succeeded upstream (token exchange and decode completed) and the
// router was persisting the new credentials locally. Handlers map it to
// `code:3901 oauth_store_failed`.
type StoreError struct {
	Op  string
	Err error
}

func (e *StoreError) Error() string {
	if e == nil {
		return "oauth store failed"
	}
	if e.Op == "" {
		return fmt.Sprintf("oauth store failed: %v", e.Err)
	}
	return fmt.Sprintf("oauth store failed: %s: %v", e.Op, e.Err)
}

func (e *StoreError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}
