package setup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/user/one-llm-router/internal/api/errcode"
	"github.com/user/one-llm-router/internal/config"
	"github.com/user/one-llm-router/internal/domain"
)

// Serialiser is the in-process mutex that guarantees commit and
// settings-update operations never race when both try to write
// config.json. plan.md §Risk R-4 ("no two goroutines may hold the
// config writer lock concurrently") requires a single package-scoped
// mutex; the same lock is imported by internal/api/admin for the
// settings-update path.
//
// Using a Mutex (not atomic.Value) is intentional: WriteAtomic is a
// multi-step operation (marshal → open → write → fsync → rename) and
// the caller is blocked on a file-system round-trip, not a cheap CPU
// op. A Mutex also makes the "first-writer-wins" semantics explicit
// to reviewers.
var Serialiser sync.Mutex

// MigratorFactory is the narrow dependency the Commit path pulls in
// to run migrations against a freshly-validated DSN. Using an
// interface keeps tests hermetic (fake factory returns a stubbed
// Migrator) and keeps internal/store's golang-migrate import from
// bleeding into internal/setup (AGENTS.md Boundary: migration
// orchestration stays in internal/store).
type MigratorFactory interface {
	// Open constructs a Migrator for (driver, url). The caller
	// MUST Close the returned Migrator on success AND error paths.
	Open(ctx context.Context, driver, url string) (MigratorHandle, error)
}

// MigratorHandle is the subset of *store.Migrator that Commit touches.
// Narrowing the interface keeps internal/setup free of golang-migrate
// imports.
type MigratorHandle interface {
	Up(ctx context.Context) error
	Close() error
}

// AccountCreator is the narrow dependency the Commit path uses to
// insert the operator's first upstream account. Takes a driver+dsn so
// the setup path can open its OWN xorm engine — reusing the migrator's
// sql.DB would either require a second abstraction layer or force
// internal/setup to import xorm. The hand-off between migrator close
// and account-insert open is intentional: each phase has a dedicated
// connection.
type AccountCreator interface {
	CreateAccount(ctx context.Context, driver, url string, account *domain.UpstreamAccount) error
}

// PostCommitReloader is invoked AFTER config.json has been written.
// It reloads config into the live publisher and returns any error the
// loader produced. BuildApp passes the real reloader; tests pass a
// no-op.
//
// This is the control-plane reload path described in spec.md FR-007:
// a committed wizard must be observable to subsequent requests
// WITHOUT a process restart.
type PostCommitReloader func(ctx context.Context) error

// CommitResult is the structured outcome returned to the HTTP handler.
// AccountID is the freshly-inserted upstream_accounts.id; the wizard
// UI uses it to seed the settings page and the /admin/accounts/:id
// route.
type CommitResult struct {
	ConfigVersion int
	AccountID     int64
	APIKeyFP      string // short SHA-256 fingerprint, for logs only
}

// CommitFailure wraps a non-validation commit error with the errcode
// the handler should surface. Split from ValidationError because the
// Commit flow produces system-class failures (2900, 2901, 2902, 2903)
// that map onto different HTTP statuses (500 vs 200).
type CommitFailure struct {
	Code   int
	Msg    string
	Err    error
	SysErr bool // true → WriteSysErr, false → WriteBizErr
	Hint   string
}

// Error implements the error interface. Wraps the underlying cause
// so errors.Is / errors.As traverse the chain.
func (cf *CommitFailure) Error() string {
	if cf.Err == nil {
		return cf.Msg
	}
	return cf.Msg + ": " + cf.Err.Error()
}

// Unwrap exposes the underlying cause for errors.Is.
func (cf *CommitFailure) Unwrap() error { return cf.Err }

// Commit executes the canonical wizard commit sequence described in
// specs/002-.../spec.md §US-1 AC-2 and setup-api.md §Commit flow:
//
//  1. Validate inputs (internal/setup/validator.go).
//  2. Probe the DSN (probe.go).
//  3. Run migrations against the DSN (via the injected MigratorFactory).
//  4. Insert the first upstream_account (AccountCreator).
//  5. Sweep stale tmp files next to cfgPath (SweepStale).
//  6. WriteAtomic the new config.json.
//  7. Invoke the post-commit reloader to publish the new live config.
//
// If ANY step between 3 and 6 fails, the function returns a
// CommitFailure and — critically — leaves the database in its post-
// migrate / post-insert state. That asymmetry matches data-model.md
// §Commit transaction ordering: "file last, db first; a crash between
// step 4 and 6 leaves a populated DB but still-pending config.json,
// and the next boot lands in brownfield mode which picks up where we
// left off."
//
// Concurrency: the whole function runs under Serialiser. Two parallel
// wizards will serialise behind the mutex; the second one sees
// config.json already present and returns SetupAlreadyDone at the
// state check that the caller (handler) performs BEFORE calling into
// Commit.
//
// Parameters:
//   - ctx          — request-scoped; migrations honour cancellation
//     where golang-migrate permits.
//   - req          — validated CommitRequest. Validator.Commit MUST
//     have returned nil for this request before Commit is called.
//   - cfgPath      — absolute path to config.json (same value the
//     loader / gate use).
//   - factory      — migrator factory (BuildApp injects a real one;
//     tests pass a fake).
//   - creator      — account creator (same pattern).
//   - reloader     — config reloader (same pattern).
//   - logger       — scoped slog logger; MUST be non-nil in
//     production wiring.
func Commit(
	ctx context.Context,
	req *CommitRequest,
	cfgPath string,
	factory MigratorFactory,
	creator AccountCreator,
	reloader PostCommitReloader,
	logger *slog.Logger,
) (*CommitResult, *CommitFailure) {
	if logger == nil {
		logger = slog.Default()
	}
	if cfgPath == "" {
		return nil, &CommitFailure{
			Code: errcode.InternalError, Msg: "cfgPath is empty",
			Err: errors.New("setup.Commit: cfgPath empty"), SysErr: true,
		}
	}
	if factory == nil {
		return nil, &CommitFailure{
			Code: errcode.InternalError, Msg: "migrator factory is nil",
			Err: errors.New("setup.Commit: factory nil"), SysErr: true,
		}
	}
	if creator == nil {
		return nil, &CommitFailure{
			Code: errcode.InternalError, Msg: "account creator is nil",
			Err: errors.New("setup.Commit: creator nil"), SysErr: true,
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, &CommitFailure{
			Code: errcode.InternalError, Msg: "context cancelled",
			Err: err, SysErr: true,
		}
	}

	Serialiser.Lock()
	defer Serialiser.Unlock()

	// Step 0 — re-check config.json presence UNDER the lock. A racing
	// commit that won the serialiser ahead of us may already have
	// written config.json while we were waiting on Lock; the handler's
	// pre-lock ProbeState may have read the filesystem BEFORE that win.
	// Returning setup_already_done here prevents two wizards from both
	// racing the migrate→account→write sequence (US-1 Edge-2).
	if done, stErr := NewStateReader().IsDone(cfgPath); stErr != nil {
		return nil, &CommitFailure{
			Code: errcode.InternalError, Msg: "config_path_unreadable",
			Err: stErr, SysErr: true,
		}
	} else if done {
		return nil, &CommitFailure{
			Code: errcode.SetupAlreadyDone, Msg: errcode.Symbol(errcode.SetupAlreadyDone),
			Err:    errors.New("setup.Commit: config.json already present under lock"),
			SysErr: false,
		}
	}

	// Step 1 — validate.
	if vErr := NewValidator().Commit(req); vErr != nil {
		return nil, &CommitFailure{
			Code: vErr.Code, Msg: vErr.Msg, Err: vErr,
			SysErr: false,
			Hint:   vErr.Field,
		}
	}

	// Step 2 — probe DSN.
	//
	// Mapping rule (setup-api.md §Response errors, "as probe | same"):
	// a commit-time probe failure is the same operator mistake
	// probe-dsn reports (unreachable host, bad credentials, invalid
	// filename) and MUST surface as business error
	// `2003 invalid_dsn` — HTTP 200. Only the much rarer "driver
	// rejected the DSN at commit time" catastrophes (driver absent
	// from the binary, malformed DSN the validator let through) are
	// `2900 db_connect_failed` / HTTP 500, which in 002 cannot happen
	// because the validator + probe guard every driver name and the
	// same binary runs probe and commit.
	latency, version, err := ProbeDSN(ctx, req.DB.Driver, req.DB.URL)
	if err != nil {
		return nil, &CommitFailure{
			Code: errcode.InvalidDSN, Msg: errcode.Symbol(errcode.InvalidDSN),
			Err: err, SysErr: false,
			Hint: driverHint(req.DB.Driver),
		}
	}
	logger.Debug("setup commit probe ok",
		"driver", req.DB.Driver,
		"latency_ms", latency,
		"server_version", version,
	)

	// Step 3 — migrations.
	mig, err := factory.Open(ctx, req.DB.Driver, req.DB.URL)
	if err != nil {
		return nil, &CommitFailure{
			Code: errcode.MigrateFailed, Msg: "migrate_failed",
			Err: err, SysErr: true,
		}
	}
	defer func() {
		if cErr := mig.Close(); cErr != nil {
			logger.Warn("setup commit migrator close failed", "error", cErr)
		}
	}()

	if err := mig.Up(ctx); err != nil {
		return nil, &CommitFailure{
			Code: errcode.MigrateFailed, Msg: "migrate_failed",
			Err: err, SysErr: true,
		}
	}

	// Step 4 — insert first account (optional since 2026-04-15 —
	// setup-api.md v2.4). When the operator omits first_account the
	// commit still publishes config.json + runs migrations; the
	// upstream_accounts table stays empty and /api/admin/health
	// reports `degraded` so the admin portal's "no healthy accounts"
	// banner (V-001) keeps the state observable.
	now := time.Now().UTC()
	var account *domain.UpstreamAccount
	if !req.FirstAccount.IsEmpty() {
		var baseURL *string
		if req.FirstAccount.BaseURL != nil && *req.FirstAccount.BaseURL != "" {
			v := *req.FirstAccount.BaseURL
			baseURL = &v
		}
		account = &domain.UpstreamAccount{
			Name:         strings.TrimSpace(req.FirstAccount.Name),
			Provider:     req.FirstAccount.Provider,
			APIKey:       req.FirstAccount.APIKey,
			BaseURL:      baseURL,
			Status:       domain.AccountStatusActive,
			CreatedAt:    now,
			UpdatedAt:    now,
			Capabilities: []string{"op.openai.chat_completions", "op.openai.responses"},
		}
		if err := creator.CreateAccount(ctx, req.DB.Driver, req.DB.URL, account); err != nil {
			return nil, &CommitFailure{
				Code: errcode.CommitTxFailed, Msg: "commit_tx_failed",
				Err: err, SysErr: true,
			}
		}
	}

	// Step 5 — sweep stale tmp files.
	if err := config.SweepStale(cfgPath); err != nil {
		// Sweep failure is recoverable — the next WriteAtomic would
		// fail with EEXIST and we'd surface THAT error; but we log
		// this so operators can correlate.
		logger.Warn("setup commit sweep stale failed", "error", err)
	}

	// Step 6 — WriteAtomic.
	cfg := &config.Config{
		Version: config.SupportedVersion,
		DB: config.DBConfig{
			Driver: req.DB.Driver,
			URL:    req.DB.URL,
		},
		Runtime: ApplyRuntimeDefaults(req.Runtime),
		Plugins: config.PluginsConfig{
			AdminAuth:  config.AdminAuthPluginConfig{Enabled: req.Plugins.AdminAuth.Enabled},
			ClientKeys: config.ClientKeysPluginConfig{Enabled: req.Plugins.ClientKeys.Enabled},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := config.WriteAtomic(cfgPath, cfg); err != nil {
		return nil, &CommitFailure{
			Code: errcode.ConfigWriteFailed, Msg: "config_write_failed",
			Err: err, SysErr: true,
		}
	}

	// Step 7 — reload.
	if reloader != nil {
		if err := reloader(ctx); err != nil {
			// Reload failure is serious but non-fatal: the file is
			// on disk so the NEXT request will hit the loader once
			// the operator retries or restarts. Surface as a
			// system error so the wizard shows remediation, but do
			// not attempt to delete config.json.
			return nil, &CommitFailure{
				Code: errcode.ConfigWriteFailed, Msg: "config_reload_failed",
				Err: err, SysErr: true,
				Hint: "config.json was written but the live reload failed; retry or restart the router",
			}
		}
	}

	// Setup-api.md §Side-effects requires EXACTLY ONE `setup_committed`
	// INFO event; the canonical emit lives in the HTTP handler (it
	// carries the request_id and plugin flags that are not available
	// down here). This checkpoint stays at DEBUG so operators can
	// still see a commit-layer "success" marker when verbose logging
	// is on, without creating a duplicate audit line.
	var accountID int64
	var accountName string
	if account != nil {
		accountID = account.ID
		accountName = account.Name
	}
	logger.Debug("setup commit complete",
		"driver", req.DB.Driver,
		"account_id", accountID,
		"account_name", accountName,
		"account_seeded", account != nil,
	)

	return &CommitResult{
		ConfigVersion: cfg.Version,
		AccountID:     accountID,
		APIKeyFP:      fingerprintAPIKey(req.FirstAccount.APIKey),
	}, nil
}

// fingerprintAPIKey returns the first 8 hex chars of SHA-256(key).
// Used in structured logs so operators can correlate audit trails
// without echoing the raw key. NEVER logged to INFO by itself — always
// paired with the account_id / request_id.
func fingerprintAPIKey(key string) string {
	if key == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:4])
}

// driverHint returns a short remediation string keyed off the driver.
// Surfaced in the wizard UI's "Could not reach the database" banner.
// Kept short; the full playbook is in docs/setup-troubleshooting.md.
func driverHint(driver string) string {
	switch driver {
	case "sqlite3":
		return "verify the sqlite file path exists and is writable"
	case "postgres":
		return "verify host/port/credentials and that the server accepts connections"
	case "mysql":
		return "verify host/port/credentials and that mysql is listening on the expected port"
	default:
		return fmt.Sprintf("verify %s is reachable and credentials are correct", driver)
	}
}
