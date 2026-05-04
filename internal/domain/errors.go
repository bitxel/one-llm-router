package domain

import "errors"

var (
	ErrNoCapacity            = errors.New("no active upstream accounts available")
	ErrAccountNotFound       = errors.New("upstream account not found")
	ErrRequestRecordNotFound = errors.New("request record not found")
	ErrAccountDeleted        = errors.New("upstream account is deleted")
	ErrInvalidTransition     = errors.New("invalid account status transition")

	// ErrAuthMethodMismatch — a credential update (rotation or
	// refresh) was asked to change the auth_method of an
	// existing row, which data-model.md §Invariants rule 3 forbids
	// ("auth_method is immutable within a row — moving between
	// shapes requires a new row"). store.UpdateCredentials wraps
	// this sentinel so callers can distinguish invariant violations
	// from missing rows without string-scraping.
	ErrAuthMethodMismatch = errors.New("domain: auth_method mismatch")

	// ErrInvalidAccountShape — an UpstreamAccount has a known
	// AuthMethod but the credential columns don't match the required
	// shape (e.g. an api_key row carrying a non-nil AccessToken, or
	// an oauth_browser row missing RefreshToken). Wrapped by
	// UpstreamAccount.Validate; the wrapped message pinpoints which
	// field(s) are wrong. Callers use errors.Is to distinguish this
	// "known-method shape violation" from ErrUnknownAuthMethod.
	ErrInvalidAccountShape = errors.New("domain: invalid account shape")

	// ErrUnknownAuthMethod — the AuthMethod value is not in the
	// closed set {api_key, oauth_browser, oauth_device, oauth_import}.
	// Categorically different from ErrInvalidAccountShape because
	// the *discriminator itself* is corrupt — we cannot even decide
	// which shape to enforce. Surfaces as envelope code 3011
	// unknown_auth_method.
	ErrUnknownAuthMethod = errors.New("domain: unknown auth method")
)

type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return e.Message
}

func IsValidationError(err error) bool {
	var ve *ValidationError
	return errors.As(err, &ve)
}
