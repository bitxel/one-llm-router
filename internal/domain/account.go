package domain

import (
	"fmt"
	"time"
)

// Account status values. Stored as TEXT with a domain-enforced enum.
// Status has no DB-level CHECK in any dialect — Go's domain layer is
// the single source of truth. auth_method DOES get a CHECK in Postgres
// and MySQL 8.0.16+ (defence-in-depth); SQLite uses plain TEXT because
// the 003 migration already relies on PRAGMA writable_schema to relax
// api_key NOT NULL. See data-model.md §Row-level invariants rule 2.
const (
	AccountStatusActive   = "active"
	AccountStatusDisabled = "disabled"
	AccountStatusDeleted  = "deleted"

	ProviderOpenAI    = "openai"
	ProviderAnthropic = "anthropic"
)

var ProviderDefaultURLs = map[string]string{
	ProviderOpenAI:    "https://api.openai.com",
	ProviderAnthropic: "https://api.anthropic.com",
}

// AuthMethod is the credential-shape discriminator added in 003. Kept
// as a named string (not an int enum) because (a) the DB column is
// TEXT, so a string trip-through avoids a mapping table, and (b) every
// consumer that crosses a JSON / log boundary needs the symbolic form
// anyway. The valid set is closed — unknown values flunk Validate().
type AuthMethod string

const (
	AuthMethodAPIKey       AuthMethod = "api_key"
	AuthMethodOAuthBrowser AuthMethod = "oauth_browser"
	AuthMethodOAuthDevice  AuthMethod = "oauth_device"
	AuthMethodOAuthImport  AuthMethod = "oauth_import"
)

// UpstreamAccount is the primary credential row. Existing 002 fields
// (id, name, provider, api_key, base_url, status, created_at,
// updated_at) retain their names and DB mapping exactly. The 8 new
// fields are nullable in the DB and hence pointers in Go (for the
// metadata) or length-check-able []byte slices (for the three token
// bodies — BYTEA/BLOB/VARBINARY round-trip as []byte with xorm, and
// `len == 0` means "column is NULL" for our write helpers).
//
// TOKEN FIELDS ARE NEVER SERIALIZED:
//
//   - AccessToken / RefreshToken / IDToken carry json:"-" so they
//     stay off every JSON boundary by default. The ONE legal read
//     path that fetches them is store.GetForExport (T-022), invoked
//     by the dedicated export endpoint whose response schema is
//     explicitly a file download and not this struct.
//   - APIKey keeps its 002 `json:"-"` tag for the same reason.
//   - String() redacts all five secret fields so an accidental
//     %v / %+v / %s in a log line does not leak bytes. Tests assert
//     the redaction in domain/account_test.go.
type UpstreamAccount struct {
	ID        int64     `xorm:"pk autoincr 'id'" json:"id"`
	Name      string    `xorm:"not null 'name'" json:"name"`
	Provider  string    `xorm:"not null default('openai') 'provider'" json:"provider"`
	APIKey    string    `xorm:"'api_key'" json:"-"`
	BaseURL   *string   `xorm:"'base_url'" json:"base_url,omitempty"`
	Status    string    `xorm:"not null default('active') 'status'" json:"status"`
	CreatedAt time.Time `xorm:"created not null 'created_at'" json:"created_at"`
	UpdatedAt time.Time `xorm:"updated not null 'updated_at'" json:"updated_at"`

	// Feature 003 — credential-shape discriminator + OAuth fields.
	AuthMethod       AuthMethod `xorm:"not null default('api_key') 'auth_method'" json:"auth_method"`
	AccessToken      []byte     `xorm:"'access_token'" json:"-"`
	RefreshToken     []byte     `xorm:"'refresh_token'" json:"-"`
	IDToken          []byte     `xorm:"'id_token'" json:"-"`
	LastRefresh      *time.Time `xorm:"'last_refresh'" json:"last_refresh,omitempty"`
	AccessExpiresAt  *time.Time `xorm:"'access_expires_at'" json:"access_expires_at,omitempty"`
	Email            *string    `xorm:"'email'" json:"email,omitempty"`
	PlanType         *string    `xorm:"'plan_type'" json:"plan_type,omitempty"`
	ChatGPTAccountID *string    `xorm:"'chatgpt_account_id'" json:"chatgpt_account_id,omitempty"`
}

func (a UpstreamAccount) TableName() string {
	return "upstream_accounts"
}

// String redacts every secret-bearing field for %s/%v/%+v logging.
// JSON emission is separately protected via `json:"-"` on the five
// secret fields.
//
// KNOWN LIMITATION: %#v (Go-syntax form) bypasses Stringer by design
// and will leak secrets — accepted risk per the 003 review. Never
// use %#v on an UpstreamAccount. The regression guard
// TestUpstreamAccount_SharpV_KnownLeak asserts the leak still
// happens; when a future change (e.g. opaque RedactedBytes types)
// closes it, delete this note and the test.
func (a UpstreamAccount) String() string {
	return fmt.Sprintf(
		"UpstreamAccount{id=%d name=%q provider=%q auth_method=%q status=%q api_key=<redacted> access_token=<redacted len=%d> refresh_token=<redacted len=%d> id_token=<redacted len=%d>}",
		a.ID, a.Name, a.Provider, a.AuthMethod, a.Status,
		len(a.AccessToken), len(a.RefreshToken), len(a.IDToken),
	)
}

func (a UpstreamAccount) EffectiveBaseURL() string {
	if a.BaseURL != nil && *a.BaseURL != "" {
		return *a.BaseURL
	}
	if u, ok := ProviderDefaultURLs[a.Provider]; ok {
		return u
	}
	return ProviderDefaultURLs[ProviderOpenAI]
}

// IsOAuth reports whether the row's credential shape is one of the
// OAuth variants. Equivalent to `a.AuthMethod != AuthMethodAPIKey`
// for valid rows, but phrased as the positive assertion to avoid the
// "what about an unknown method?" dead-zone.
func (a UpstreamAccount) IsOAuth() bool {
	switch a.AuthMethod {
	case AuthMethodOAuthBrowser, AuthMethodOAuthDevice, AuthMethodOAuthImport:
		return true
	default:
		return false
	}
}

// Validate enforces the per-shape invariants from data-model.md
// §Invariants. Every write path (store.InsertUpstreamAccount,
// store.UpdateCredentials) MUST call Validate on the final in-memory
// shape before the DB hit. The validator has three jobs:
//
//  1. Reject unknown AuthMethod values with ErrUnknownAuthMethod.
//
//  2. For auth_method="api_key": require APIKey != "", require EVERY
//     003 OAuth field to be zero/nil.
//
//  3. For auth_method="oauth_*": require APIKey == "" (the store
//     writes NULL via xorm Omit("api_key") on OAuth INSERTs so the
//     column is NULL on disk even though the Go field is a non-
//     pointer string), require the five mandatory OAuth fields
//     (AccessToken, RefreshToken, IDToken, LastRefresh,
//     AccessExpiresAt) to be set.
//     Email/PlanType/ChatGPTAccountID are tolerated-nil per data-
//     model.md ("partial metadata, not partial credentials").
//
// The error messages list EVERY offending field in a stable order so
// a vendor-review test that asserts on error text has a deterministic
// needle; test TestUpstreamAccount_Validate_ShapeErrorText locks this.
func (a *UpstreamAccount) Validate() error {
	if a == nil {
		return fmt.Errorf("%w: nil receiver", ErrInvalidAccountShape)
	}

	switch a.AuthMethod {
	case AuthMethodAPIKey:
		return a.validateAPIKeyShape()
	case AuthMethodOAuthBrowser, AuthMethodOAuthDevice, AuthMethodOAuthImport:
		return a.validateOAuthShape()
	default:
		return fmt.Errorf("%w: %q (valid: api_key|oauth_browser|oauth_device|oauth_import)",
			ErrUnknownAuthMethod, string(a.AuthMethod))
	}
}

func (a *UpstreamAccount) validateAPIKeyShape() error {
	// data-model.md §Invariants rule 1 requires OAuth byte columns to
	// be NULL on api_key rows — that's a strict NULL, not "either
	// NULL or a zero-length slice". A non-nil but empty []byte (e.g.
	// []byte{}) still marshals to xorm as the zero-length blob, not
	// NULL, which breaks rule 1 in exactly the same invisible way as
	// a full token would. So we test the slice for nil, not len > 0.
	var offenders []string
	if a.APIKey == "" {
		offenders = append(offenders, "api_key=empty")
	}
	if a.AccessToken != nil {
		offenders = append(offenders, "access_token=non-nil")
	}
	if a.RefreshToken != nil {
		offenders = append(offenders, "refresh_token=non-nil")
	}
	if a.IDToken != nil {
		offenders = append(offenders, "id_token=non-nil")
	}
	if a.LastRefresh != nil {
		offenders = append(offenders, "last_refresh=non-nil")
	}
	if a.AccessExpiresAt != nil {
		offenders = append(offenders, "access_expires_at=non-nil")
	}
	if a.Email != nil {
		offenders = append(offenders, "email=non-nil")
	}
	if a.PlanType != nil {
		offenders = append(offenders, "plan_type=non-nil")
	}
	if a.ChatGPTAccountID != nil {
		offenders = append(offenders, "chatgpt_account_id=non-nil")
	}
	if len(offenders) == 0 {
		return nil
	}
	return fmt.Errorf("%w: auth_method=api_key requires api_key set + all OAuth fields nil; offenders=%v",
		ErrInvalidAccountShape, offenders)
}

func (a *UpstreamAccount) validateOAuthShape() error {
	var offenders []string
	if a.APIKey != "" {
		offenders = append(offenders, "api_key=non-empty")
	}
	if len(a.AccessToken) == 0 {
		offenders = append(offenders, "access_token=empty")
	}
	if len(a.RefreshToken) == 0 {
		offenders = append(offenders, "refresh_token=empty")
	}
	if len(a.IDToken) == 0 {
		offenders = append(offenders, "id_token=empty")
	}
	if a.LastRefresh == nil {
		offenders = append(offenders, "last_refresh=nil")
	}
	if a.AccessExpiresAt == nil {
		offenders = append(offenders, "access_expires_at=nil")
	}
	if len(offenders) == 0 {
		return nil
	}
	return fmt.Errorf("%w: auth_method=%s requires api_key nil + {access_token,refresh_token,id_token,last_refresh,access_expires_at} set; offenders=%v",
		ErrInvalidAccountShape, a.AuthMethod, offenders)
}
