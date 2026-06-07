package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"xorm.io/xorm"

	"github.com/user/one-llm-router/internal/domain"
)

type AccountRepo struct {
	engine *xorm.Engine
}

func NewAccountRepo(engine *xorm.Engine) *AccountRepo {
	return &AccountRepo{engine: engine}
}

// AccountListItem is the token-free projection returned by
// ListForAdminAPI. The admin API handler (T-051) wraps these in an
// envelope; this type is the server-authoritative shape from
// data-model.md §AccountListItem.
//
// Pointer fields for the 5 OAuth-only metadata columns keep the
// absent-vs-null distinction that FR-011a requires: the final JSON
// marshal on the handler side applies omitempty so api_key rows
// carry ONLY the 002 fields (no `email: null` clutter).
type AccountListItem struct {
	ID         int64             `json:"id"`
	Name       string            `json:"name"`
	Provider   string            `json:"provider"`
	AuthMethod domain.AuthMethod `json:"auth_method"`
	Status     string            `json:"status"`
	BaseURL    *string           `json:"base_url,omitempty"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`

	// OAuth-only: set on oauth_* rows, nil on api_key rows. The
	// handler layer relies on `json:"…,omitempty"` + pointer nil to
	// drop these keys for api_key rows, matching the contract in
	// specs/003-multi-mode-codex-auth/contracts/accounts-api.md.
	Email            *string    `json:"email,omitempty"`
	PlanType         *string    `json:"plan_type,omitempty"`
	ChatGPTAccountID *string    `json:"chatgpt_account_id,omitempty"`
	LastRefresh      *time.Time `json:"last_refresh,omitempty"`
	AccessExpiresAt  *time.Time `json:"access_expires_at,omitempty"`

	Capabilities []string `json:"capabilities,omitempty"`

	PrimaryUsedPercent   *float64   `json:"-"`
	SecondaryUsedPercent *float64   `json:"-"`
	UsageUpdatedAt       *time.Time `json:"-"`

	// PlanTypeLabel is DELIBERATELY absent from the store DTO. It is
	// a UI-only presentation of PlanType — the handler in
	// internal/api/adminapi fills it via adminapi.PlanTypeLabel when
	// it serialises the wire response. Keeping the store DTO free of
	// presentation concerns satisfies the layering review (D7) while
	// still letting the admin list use this shape as its wire body
	// per plan.md §API↔Store shortcut.
}

// ExportPayload is the token-bearing shape returned by GetForExport.
// THE ONLY legal code path that fetches token bytes from the DB goes
// through this struct; every other read path MUST use
// ListForAdminAPI (token-free projection). The export endpoint
// (T-080/T-081) streams these bytes back to the operator as an
// audited file download; they must NEVER be JSON-marshalled into an
// admin list response.
type ExportPayload struct {
	AccountID        int64
	AccessToken      []byte
	RefreshToken     []byte
	IDToken          []byte
	LastRefresh      *time.Time
	ChatGPTAccountID *string
}

// CredentialPatch is the narrow update set accepted by
// UpdateCredentials. Exactly ONE of the two shapes MUST be populated:
// the api_key mode carries only APIKey, and the OAuth mode carries
// the 5-field token/expiry bundle plus optional metadata. Mixing
// shapes is a programmer bug — the store helper rejects it
// defensively before touching the DB.
//
// LastRefresh + AccessExpiresAt are REQUIRED together for OAuth
// updates: they define the single unit-of-staleness that
// RefreshIfStale reasons over, and splitting them across calls would
// leave a momentary window where a concurrent read sees a fresh
// access_expires_at but stale access_token. The store commits both
// in the same UPDATE.
type CredentialPatch struct {
	AuthMethod domain.AuthMethod

	// api_key mode — required on AuthMethodAPIKey, MUST be empty on
	// OAuth modes.
	APIKey string

	// OAuth mode — all six fields MUST be set on oauth_* modes,
	// MUST be empty/nil on api_key mode. Email/PlanType/
	// ChatGPTAccountID are tolerated-nil even on OAuth mode
	// (matches data-model.md's "partial metadata, not partial
	// credentials" rule).
	AccessToken  []byte
	RefreshToken []byte
	IDToken      []byte
	LastRefresh  *time.Time
	// ExpectedLastRefresh adds an optional optimistic-lock precondition
	// for OAuth updates. When set, UpdateCredentials only succeeds if
	// the stored row still has this last_refresh value. Used by the
	// request-time refresh path to avoid clobbering a concurrent credential update.
	ExpectedLastRefresh *time.Time
	AccessExpiresAt     *time.Time
	Email               *string
	PlanType            *string
	ChatGPTAccountID    *string
}

// ErrConditionalUpdateConflict reports that an optimistic-lock
// precondition on UpdateCredentials no longer matched the stored row.
// Request-time refresh maps this to an internal retryable conflict.
var ErrConditionalUpdateConflict = errors.New("store: conditional update conflict")

// InsertUpstreamAccount is the 003 INSERT path that covers both
// credential shapes. It runs domain.Validate() BEFORE the DB hit so
// a shape violation never reaches the wire. For OAuth rows the
// api_key column is explicitly left unset (xorm Omit) so it lands
// as SQL NULL rather than "" — the rollback invariance test in
// migration_003_test.go depends on this to identify oauth rows by
// `api_key IS NULL`.
//
// Returns the freshly-assigned ID on success. The input account is
// mutated: xorm fills ID + CreatedAt, and OAuth timestamps
// (LastRefresh, AccessExpiresAt) are canonicalised to UTC per
// data-model.md §Timestamp types. Callers that need a rollback-safe
// path should call this inside an explicit transaction.
func (r *AccountRepo) InsertUpstreamAccount(_ context.Context, account *domain.UpstreamAccount) (int64, error) {
	if err := account.Validate(); err != nil {
		return 0, fmt.Errorf("insert account: validate: %w", err)
	}
	// Normalise OAuth timestamps to UTC before handing to xorm.
	// data-model.md §Timestamp types requires every round-trip to
	// be UTC-stable across dialects (PG TIMESTAMPTZ, MySQL
	// TIMESTAMP(6), SQLite TEXT ISO-8601 with Z). Callers MAY pass
	// local-zone or UTC — we canonicalise here so there is exactly
	// one normalisation point instead of sprinkling `.UTC()` at
	// every call site.
	account.LastRefresh = normaliseUTC(account.LastRefresh)
	account.AccessExpiresAt = normaliseUTC(account.AccessExpiresAt)

	sess := r.engine.NewSession()
	defer func() { _ = sess.Close() }()

	if account.IsOAuth() {
		// Ensure the api_key column lands as NULL for OAuth rows.
		// xorm would otherwise write "" because the Go field is a
		// non-pointer string.
		sess = sess.Omit("api_key")
	}
	if _, err := sess.Insert(account); err != nil {
		return 0, fmt.Errorf("insert account: %w", err)
	}
	return account.ID, nil
}

// normaliseUTC returns a copy of t in UTC, or nil if t is nil.
// Used by the 003 insert / update paths to guarantee OAuth timestamps
// are persisted as UTC regardless of caller zone — see data-model.md
// §Timestamp types.
func normaliseUTC(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

// UpdateCredentials writes ONLY the credential columns for the row
// with the given ID. Used by:
//   - T-054 (token refresh) — OAuth mode with fresh tokens +
//     last_refresh + access_expires_at.
//   - OAuth refresh — replacing stale credentials
//     wholesale.
//
// The update is atomic: every call goes through a single xorm
// Update with the explicit Cols() list, so a concurrent reader can
// never see access_token + refresh_token + id_token from different
// refresh rounds.
//
// CROSS-ROW SHAPE ENFORCEMENT (data-model.md §Invariants rule 3 —
// "auth_method is immutable within a row"):
// The function SELECTs the existing row's auth_method inside the
// same session and rejects any patch whose AuthMethod does not
// match. Without this check a caller could issue
// `{AuthMethod: api_key, APIKey: "sk-x"}` against an oauth_* row,
// which would UPDATE api_key to non-NULL while the tokens and
// auth_method stayed oauth_browser — violating invariants rule 1
// ("oauth row ⇒ api_key IS NULL"). The mismatch surfaces as a
// domain.ErrAuthMethodMismatch so callers can distinguish this invariant
// violation without string-scraping.
//
// Returns domain.ErrAccountNotFound when no row matches id and
// domain.ErrAuthMethodMismatch when the patch's AuthMethod disagrees
// with the stored row.
func (r *AccountRepo) UpdateCredentials(_ context.Context, id int64, p CredentialPatch) error {
	// Patch-internal shape check runs FIRST — a
	// malformed patch is a programmer bug and we don't want
	// to burn a SELECT round-trip before rejecting it.
	switch p.AuthMethod {
	case domain.AuthMethodAPIKey:
		if p.APIKey == "" {
			return fmt.Errorf("UpdateCredentials api_key mode: APIKey=empty")
		}
		if len(p.AccessToken) > 0 || len(p.RefreshToken) > 0 || len(p.IDToken) > 0 ||
			p.LastRefresh != nil || p.AccessExpiresAt != nil {
			return fmt.Errorf("UpdateCredentials api_key mode: OAuth fields must be zero")
		}
	case domain.AuthMethodOAuthBrowser, domain.AuthMethodOAuthDevice, domain.AuthMethodOAuthImport:
		if p.APIKey != "" {
			return fmt.Errorf("UpdateCredentials oauth mode: APIKey must be empty")
		}
		if len(p.AccessToken) == 0 || len(p.RefreshToken) == 0 || len(p.IDToken) == 0 ||
			p.LastRefresh == nil || p.AccessExpiresAt == nil {
			return fmt.Errorf("UpdateCredentials oauth mode: tokens + last_refresh + access_expires_at are required together")
		}
	default:
		return fmt.Errorf("UpdateCredentials: unknown AuthMethod %q", p.AuthMethod)
	}

	// Read the stored auth_method to guard invariants rule 3.
	// We intentionally SELECT only auth_method (and the primary
	// key via the implicit filter) so we never pull tokens or
	// api_key bytes into memory on the refresh path.
	existing := &domain.UpstreamAccount{}
	found, err := r.engine.ID(id).
		Cols("id", "auth_method", "status").
		Get(existing)
	if err != nil {
		return fmt.Errorf("read existing auth_method for %d: %w", id, err)
	}
	if !found {
		return domain.ErrAccountNotFound
	}
	if existing.Status == domain.AccountStatusDeleted {
		return domain.ErrAccountNotFound
	}

	storedMethod := existing.AuthMethod
	if storedMethod == "" {
		// Same defensive fallback as ListForAdminAPI: a row with
		// auth_method='' is impossible on a live DB (NOT NULL
		// DEFAULT 'api_key') but we treat it as api_key rather
		// than flunking the update — this matches how the admin
		// list surfaces the row.
		storedMethod = domain.AuthMethodAPIKey
	}
	if storedMethod != p.AuthMethod {
		return fmt.Errorf(
			"UpdateCredentials: patch auth_method=%q does not match stored auth_method=%q (invariant: auth_method is immutable within a row): %w",
			p.AuthMethod, storedMethod, domain.ErrAuthMethodMismatch,
		)
	}

	switch p.AuthMethod {
	case domain.AuthMethodAPIKey:
		affected, err := r.engine.
			Where("id = ? AND status != ?", id, domain.AccountStatusDeleted).
			Cols("api_key", "updated_at").
			Update(&domain.UpstreamAccount{APIKey: p.APIKey})
		if err != nil {
			return fmt.Errorf("update api_key for %d: %w", id, err)
		}
		if affected == 0 {
			return r.credentialUpdateMissReason(id, false)
		}
		return nil

	case domain.AuthMethodOAuthBrowser, domain.AuthMethodOAuthDevice, domain.AuthMethodOAuthImport:
		row := &domain.UpstreamAccount{
			AccessToken:      p.AccessToken,
			RefreshToken:     p.RefreshToken,
			IDToken:          p.IDToken,
			LastRefresh:      normaliseUTC(p.LastRefresh),
			AccessExpiresAt:  normaliseUTC(p.AccessExpiresAt),
			Email:            p.Email,
			PlanType:         p.PlanType,
			ChatGPTAccountID: p.ChatGPTAccountID,
		}
		update := r.engine.
			Cols("access_token", "refresh_token", "id_token",
				"last_refresh", "access_expires_at",
				"email", "plan_type", "chatgpt_account_id",
				"updated_at")
		if p.ExpectedLastRefresh != nil {
			expectedLastRefresh := normaliseUTC(p.ExpectedLastRefresh)
			update = update.Where("id = ? AND status != ? AND last_refresh = ?",
				id, domain.AccountStatusDeleted, r.conditionalLastRefreshArg(*expectedLastRefresh))
		} else {
			update = update.Where("id = ? AND status != ?", id, domain.AccountStatusDeleted)
		}
		affected, err := update.Update(row)
		if err != nil {
			return fmt.Errorf("update oauth credentials for %d: %w", id, err)
		}
		if affected == 0 {
			return r.credentialUpdateMissReason(id, p.ExpectedLastRefresh != nil)
		}
		return nil

	default:
		// Unreachable — the patch-internal switch above covers the
		// same set; left in place so a future AuthMethod addition
		// fails at two sites, not one.
		return fmt.Errorf("UpdateCredentials: unknown AuthMethod %q", p.AuthMethod)
	}
}

func (r *AccountRepo) conditionalLastRefreshArg(t time.Time) any {
	if r != nil && r.engine != nil && r.engine.DriverName() == "sqlite" {
		// xorm's sqlite3 path persists TIMESTAMP columns with second
		// precision, so optimistic-lock comparisons must use the same
		// representation the DB stores rather than the caller's original
		// nanosecond value.
		return t.UTC().Format("2006-01-02 15:04:05")
	}
	return t.UTC()
}

func (r *AccountRepo) credentialUpdateMissReason(id int64, conditional bool) error {
	row := &domain.UpstreamAccount{}
	exists, err := r.engine.ID(id).Cols("id", "status").Get(row)
	if err != nil {
		return fmt.Errorf("check credential update miss reason for %d: %w", id, err)
	}
	if !exists || row.Status == domain.AccountStatusDeleted {
		return domain.ErrAccountNotFound
	}
	if conditional {
		return ErrConditionalUpdateConflict
	}
	return domain.ErrAccountNotFound
}

// ListForAdminAPI returns the token-free projection for every
// non-deleted account in a single query. Token columns
// (`access_token`, `refresh_token`, `id_token`, `api_key`) are
// DELIBERATELY absent from the SELECT list — the xorm Cols() filter
// is belt-and-suspenders against a future "just use SELECT *"
// refactor leaking bytes.
//
// The returned slice applies pointer-nil for OAuth-only fields on
// api_key rows so the handler's omitempty marshalling drops them
// cleanly. The human-readable plan label (e.g. "ChatGPT Plus") is
// NOT set here — it is a presentation concern owned by the adminapi
// handler, which fills it from adminapi.PlanTypeLabel before writing
// the wire response. Store stays free of UI strings.
func (r *AccountRepo) ListForAdminAPI(_ context.Context) ([]AccountListItem, error) {
	var rows []domain.UpstreamAccount
	err := r.engine.
		Cols("id", "name", "provider", "auth_method", "status",
			"base_url", "created_at", "updated_at",
			"email", "plan_type", "chatgpt_account_id",
			"last_refresh", "access_expires_at",
			"primary_used_percent", "secondary_used_percent", "usage_updated_at",
			"capabilities").
		Where("status != ?", domain.AccountStatusDeleted).
		OrderBy("id ASC").
		Find(&rows)
	if err != nil {
		return nil, fmt.Errorf("list for admin api: %w", err)
	}

	items := make([]AccountListItem, 0, len(rows))
	for _, r := range rows {
		method := r.AuthMethod
		if method == "" {
			// Defence-in-depth: 002 rows written before the 003
			// migration ran carry auth_method='' because
			// `NOT NULL DEFAULT 'api_key'` only kicks in on INSERT.
			// Every live row should have the DEFAULT applied at
			// migration time, but if the column somehow came back
			// empty we tag it api_key rather than crash-loop the
			// admin list.
			method = domain.AuthMethodAPIKey
		}
		it := AccountListItem{
			ID:         r.ID,
			Name:       r.Name,
			Provider:   r.Provider,
			AuthMethod: method,
			Status:     r.Status,
			BaseURL:    r.BaseURL,
			CreatedAt:  r.CreatedAt,
			UpdatedAt:  r.UpdatedAt,
			Capabilities: r.Capabilities,
		}
		if method != domain.AuthMethodAPIKey {
			it.Email = r.Email
			it.PlanType = r.PlanType
			it.ChatGPTAccountID = r.ChatGPTAccountID
			it.LastRefresh = r.LastRefresh
			it.AccessExpiresAt = r.AccessExpiresAt
			it.PrimaryUsedPercent = r.PrimaryUsedPercent
			it.SecondaryUsedPercent = r.SecondaryUsedPercent
			it.UsageUpdatedAt = r.UsageUpdatedAt
		}
		items = append(items, it)
	}
	return items, nil
}

// GetForExport is the ONE legal read path that fetches token bytes.
// It exists solely to back POST /api/admin/accounts/{id}/export-
// auth-json (T-080/T-081). Every other read path MUST go through
// ListForAdminAPI or the selector-internal refresh read in
// internal/oauth/coordinator.go.
//
// Returns domain.ErrAccountNotFound when no row matches id, and
// domain.ErrInvalidAccountShape wrapped in a descriptive error when
// the row is api_key-shaped (export of api_key rows is forbidden by
// FR-014 — the button is hidden in the UI, but a direct API call
// must be rejected at the store layer too).
func (r *AccountRepo) GetForExport(_ context.Context, id int64) (*ExportPayload, error) {
	acct := &domain.UpstreamAccount{}
	found, err := r.engine.
		ID(id).
		Cols("id", "auth_method", "status",
			"access_token", "refresh_token", "id_token",
			"last_refresh", "access_expires_at", "chatgpt_account_id").
		Get(acct)
	if err != nil {
		return nil, fmt.Errorf("get for export %d: %w", id, err)
	}
	if !found {
		return nil, domain.ErrAccountNotFound
	}
	if acct.Status == domain.AccountStatusDeleted {
		return nil, domain.ErrAccountNotFound
	}
	// Reject anything that is NOT one of the three OAuth variants.
	// This catches api_key rows (the common case) AND the impossible-
	// but-defensive case of auth_method='' / a future unknown value,
	// neither of which should stream tokens to an operator.
	if !acct.IsOAuth() {
		return nil, fmt.Errorf("get for export %d (auth_method=%q): %w: only oauth_* rows are exportable",
			id, acct.AuthMethod, domain.ErrInvalidAccountShape)
	}
	// Shape-guard the exportable bundle: data-model.md §Invariants
	// rule 1 requires every OAuth row carry access_token +
	// refresh_token + id_token + last_refresh + access_expires_at.
	// access_expires_at is not written into auth.json itself, but
	// a NULL value means the refresh machinery below (see
	// RefreshIfStale in internal/oauth) cannot reason about
	// staleness — exporting such a row would ship a bundle the
	// router itself treats as broken, so we refuse at the store.
	var missing []string
	if len(acct.AccessToken) == 0 {
		missing = append(missing, "access_token")
	}
	if len(acct.RefreshToken) == 0 {
		missing = append(missing, "refresh_token")
	}
	if len(acct.IDToken) == 0 {
		missing = append(missing, "id_token")
	}
	if acct.LastRefresh == nil {
		missing = append(missing, "last_refresh")
	}
	if acct.AccessExpiresAt == nil {
		missing = append(missing, "access_expires_at")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("get for export %d: %w: oauth row has missing credentials %v (corrupted row)",
			id, domain.ErrInvalidAccountShape, missing)
	}
	return &ExportPayload{
		AccountID:        acct.ID,
		AccessToken:      acct.AccessToken,
		RefreshToken:     acct.RefreshToken,
		IDToken:          acct.IDToken,
		LastRefresh:      acct.LastRefresh,
		ChatGPTAccountID: acct.ChatGPTAccountID,
	}, nil
}

// Create is the API-key-only insert path for 001/002 callers. New
// 003 code should prefer InsertUpstreamAccount which is explicit
// about the shape and handles OAuth columns via xorm Omit.
//
// Two 003 guards:
//  1. normaliseAPIKeyShape defaults zero-value AuthMethod to api_key
//     (legacy writers predate the field; leaving it empty fails
//     PG/MySQL CHECK and silently corrupts SQLite).
//  2. Explicit api_key-only gate — Create does not Omit("api_key"),
//     so an OAuth row here would land api_key=” instead of NULL
//     and violate data-model.md rule 1.
func (r *AccountRepo) Create(_ context.Context, account *domain.UpstreamAccount) error {
	normaliseAPIKeyShape(account)
	if account != nil && account.AuthMethod != domain.AuthMethodAPIKey {
		return fmt.Errorf("insert account: %w: Create accepts api_key rows only (got %q) — use InsertUpstreamAccount for OAuth shapes",
			domain.ErrInvalidAccountShape, account.AuthMethod)
	}
	if err := account.Validate(); err != nil {
		return fmt.Errorf("insert account: validate: %w", err)
	}
	_, err := r.engine.Insert(account)
	if err != nil {
		return fmt.Errorf("insert account: %w", err)
	}
	return nil
}

// normaliseAPIKeyShape applies the 003 default so a 001/002 caller
// that constructed its UpstreamAccount without knowing about
// AuthMethod still produces a legal api_key row. Only touches
// AuthMethod — other fields are the caller's responsibility.
func normaliseAPIKeyShape(a *domain.UpstreamAccount) {
	if a == nil {
		return
	}
	if a.AuthMethod == "" {
		a.AuthMethod = domain.AuthMethodAPIKey
	}
}

// CreateIdempotentByName inserts account inside an explicit transaction
// IF and only if no row with the same name is already present. On hit
// it copies the existing row's id/created_at into the input account so
// the caller can treat the operation as idempotent.
//
// This is the wizard-commit path's guard for plan.md R-1: a post-DB /
// pre-file crash leaves the account row committed but config.json
// absent; the next boot's brownfield auto-materializer synthesises
// config.json without an operator round-trip, but an operator
// retry-through-the-wizard MUST NOT produce a duplicate account row
// (upstream_accounts has no UNIQUE(name) constraint in the 001 MVP
// schema, so we enforce idempotency in code inside a single tx).
//
// Semantics:
//   - returns nil when either (a) the insert succeeded, or (b) a row
//     with the same name already exists (the caller gets that row's
//     id / created_at mutated into the passed account).
//   - returns a non-nil error only for real DB failures (tx begin,
//     select, insert, commit) — those should bubble up as 2902
//     commit_tx_failed.
func (r *AccountRepo) CreateIdempotentByName(_ context.Context, account *domain.UpstreamAccount) error {
	// Same 003-invariant normalisation + api_key-only gate as
	// Create — the wizard-commit path is API-key only, and
	// accepting an OAuth shape here would bypass xorm's
	// Omit("api_key") contract and land api_key='' instead of
	// NULL.
	normaliseAPIKeyShape(account)
	if account != nil && account.AuthMethod != domain.AuthMethodAPIKey {
		return fmt.Errorf("account idempotent insert: %w: only api_key rows accepted (got %q) — use InsertUpstreamAccount for OAuth shapes",
			domain.ErrInvalidAccountShape, account.AuthMethod)
	}
	if err := account.Validate(); err != nil {
		return fmt.Errorf("account idempotent insert: validate: %w", err)
	}

	sess := r.engine.NewSession()
	defer func() { _ = sess.Close() }()

	if err := sess.Begin(); err != nil {
		return fmt.Errorf("account tx begin: %w", err)
	}

	existing := &domain.UpstreamAccount{}
	found, err := sess.Where("name = ?", account.Name).Get(existing)
	if err != nil {
		_ = sess.Rollback()
		return fmt.Errorf("account lookup by name: %w", err)
	}
	if found {
		account.ID = existing.ID
		account.CreatedAt = existing.CreatedAt
		if err := sess.Commit(); err != nil {
			return fmt.Errorf("account tx commit (idempotent hit): %w", err)
		}
		return nil
	}

	if _, err := sess.Insert(account); err != nil {
		_ = sess.Rollback()
		return fmt.Errorf("account tx insert: %w", err)
	}
	if err := sess.Commit(); err != nil {
		return fmt.Errorf("account tx commit: %w", err)
	}
	return nil
}

func (r *AccountRepo) GetByID(_ context.Context, id int64) (*domain.UpstreamAccount, error) {
	account := &domain.UpstreamAccount{}
	found, err := r.engine.ID(id).Get(account)
	if err != nil {
		return nil, fmt.Errorf("get account %d: %w", id, err)
	}
	if !found {
		return nil, domain.ErrAccountNotFound
	}
	return account, nil
}

// GetProjectionByID returns a token-free account row for OAuth flow
// completion and admin read paths. Secret columns are deliberately
// absent from the SELECT list so the caller cannot accidentally log or
// serialize token bytes while hydrating the success response.
func (r *AccountRepo) GetProjectionByID(_ context.Context, id int64) (*domain.UpstreamAccount, error) {
	account := &domain.UpstreamAccount{}
	found, err := r.engine.ID(id).
		Cols("id", "name", "provider", "base_url", "status",
			"created_at", "updated_at", "auth_method",
			"email", "plan_type", "chatgpt_account_id",
			"last_refresh", "access_expires_at",
			"primary_used_percent", "secondary_used_percent", "usage_updated_at",
			"capabilities").
		Get(account)
	if err != nil {
		return nil, fmt.Errorf("get account projection %d: %w", id, err)
	}
	if !found {
		return nil, domain.ErrAccountNotFound
	}
	return account, nil
}

func (r *AccountRepo) List(_ context.Context, statusFilter []string) ([]domain.UpstreamAccount, error) {
	var accounts []domain.UpstreamAccount
	sess := r.engine.NewSession()
	defer func() { _ = sess.Close() }()

	if len(statusFilter) > 0 {
		sess = sess.In("status", statusFilter)
	}
	sess = sess.OrderBy("id ASC")

	if err := sess.Find(&accounts); err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	return accounts, nil
}

func (r *AccountRepo) ListActive(_ context.Context) ([]domain.UpstreamAccount, error) {
	var accounts []domain.UpstreamAccount
	err := r.engine.Where("status = ?", domain.AccountStatusActive).
		OrderBy("id ASC").
		Find(&accounts)
	if err != nil {
		return nil, fmt.Errorf("list active accounts: %w", err)
	}
	return accounts, nil
}

func (r *AccountRepo) UpdateStatus(_ context.Context, id int64, status string) error {
	affected, err := r.engine.ID(id).
		Cols("status", "updated_at").
		Update(&domain.UpstreamAccount{Status: status})
	if err != nil {
		return fmt.Errorf("update account %d status: %w", id, err)
	}
	if affected == 0 {
		return domain.ErrAccountNotFound
	}
	return nil
}

func (r *AccountRepo) UpdateUsage(_ context.Context, id int64, primary, secondary *float64) error {
	now := time.Now().UTC()
	row := &domain.UpstreamAccount{
		PrimaryUsedPercent:   primary,
		SecondaryUsedPercent: secondary,
		UsageUpdatedAt:       &now,
	}
	_, err := r.engine.
		ID(id).
		Cols("primary_used_percent", "secondary_used_percent", "usage_updated_at", "updated_at").
		Update(row)
	if err != nil {
		return fmt.Errorf("update account %d usage: %w", id, err)
	}
	return nil
}

// UpdateStatusIfCurrent conditionally updates status only when the row
// still matches the caller's stale-detection snapshot. Used by the
// request-time refresh path so a permanent refresh failure cannot
// clobber a concurrent credential update or delete that already changed the row.
func (r *AccountRepo) UpdateStatusIfCurrent(_ context.Context, id int64, status string, expectedLastRefresh time.Time) error {
	affected, err := r.engine.
		Where("id = ? AND status = ? AND last_refresh = ?", id, domain.AccountStatusActive, r.conditionalLastRefreshArg(expectedLastRefresh)).
		Cols("status", "updated_at").
		Update(&domain.UpstreamAccount{Status: status})
	if err != nil {
		return fmt.Errorf("update account %d status conditionally: %w", id, err)
	}
	if affected == 0 {
		exists, err := r.engine.ID(id).Cols("id").Get(&domain.UpstreamAccount{})
		if err != nil {
			return fmt.Errorf("check conditional status update conflict for %d: %w", id, err)
		}
		if exists {
			return ErrConditionalUpdateConflict
		}
		return domain.ErrAccountNotFound
	}
	return nil
}
