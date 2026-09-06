package app

import (
	"context"
	"fmt"

	"github.com/user/one-llm-router/internal/config"
	"github.com/user/one-llm-router/internal/domain"
	"github.com/user/one-llm-router/internal/setup"
	"github.com/user/one-llm-router/internal/store"
)

// migratorFactory adapts store.NewMigrator onto setup.MigratorFactory.
// Lives in internal/app rather than internal/store because the bridge
// is a composition concern — internal/store stays ignorant of
// internal/setup.
type migratorFactory struct{}

// Open implements setup.MigratorFactory.
func (migratorFactory) Open(ctx context.Context, driver, url string) (setup.MigratorHandle, error) {
	db := &config.DBConfig{Driver: driver, URL: url}
	mig, err := store.NewMigrator(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("open migrator: %w", err)
	}
	return mig, nil
}

// accountCreator adapts a short-lived xorm connection onto
// setup.AccountCreator. The wizard commit uses its own store open/close
// cycle rather than the running-router's *store.Store because at the
// time Commit runs, the long-lived store doesn't exist yet — BuildApp
// only opens it after the reloader picks up the new config.json.
//
// BuildApp owns ONE instance of this struct and passes the pointer to
// both the setup handler and postCommitReloader, so seededAccountID
// written during CreateAccount is visible to the reloader's
// compensateWizardSeed (a value copy would strand the write on the
// handler's copy). Write and read happen on the same HTTP handler
// goroutine — the reloader runs inside setup.Commit before it returns —
// and the setup gate refuses any second commit, so a plain field needs
// no synchronization.
//
// Residual gap: if the process dies (or promotion fails) after the
// insert but before compensateWizardSeed fires, the seeded account
// ships without a model list; remediation is the manual
// POST /api/admin/accounts/{id}/models/refresh endpoint.
type accountCreator struct {
	seededAccountID int64
}

// CreateAccount implements setup.AccountCreator.
func (c *accountCreator) CreateAccount(ctx context.Context, driver, url string, account *domain.UpstreamAccount) error {
	st, err := store.New(driver, url, defaultMaxConns, defaultMinConns)
	if err != nil {
		return fmt.Errorf("open store for first account: %w", err)
	}
	defer func() { _ = st.Close() }()

	repo := store.NewAccountRepo(st.Engine())
	// Idempotent insert: plan.md R-1 requires the wizard commit to be
	// safely retryable after a post-DB-commit / pre-file-rename crash.
	// CreateIdempotentByName runs inside an explicit transaction and
	// reuses the previously-inserted row on name match so retrying the
	// wizard with the same inputs does not duplicate accounts.
	if err := repo.CreateIdempotentByName(ctx, account); err != nil {
		return fmt.Errorf("insert first account: %w", err)
	}
	c.seededAccountID = account.ID
	return nil
}

// postCommitReloader returns a setup.PostCommitReloader that drives
// the setup-pending → steady-state transition after WriteAtomic.
//
// Two cases:
//
//  1. BuildApp was invoked with cfg != nil (steady-state boot) and a
//     subsequent settings update triggered the reloader. In that case
//     a.promoteToSteadyState is a no-op (App.promoted is true) and
//     we only need to re-publish the reloaded config.
//  2. BuildApp booted in setup-pending mode (cfg == nil) and the
//     wizard commit just wrote config.json. In this case we MUST
//     open the store, run plugins, build the steady-state handler,
//     and swap it in atomically — otherwise US-2 AC-1 (`/v1/*` must
//     start working after commit) and US-1 AC-2 (admin portal must
//     become reachable) cannot be satisfied without a process
//     restart, violating FR-007.
//
// The function is called from setup.Commit while setup.Serialiser is
// still held (Commit defers its Unlock until after the reloader
// returns), so promoteToSteadyState — which only takes promoteMu —
// stays deadlock-free. NEVER acquire setup.Serialiser anywhere on
// this path.
func postCommitReloader(a *App) setup.PostCommitReloader {
	return func(ctx context.Context) error {
		cfg, _, err := config.Load(ctx, a.deps.ConfigPath, a.deps.Env)
		if err != nil {
			return fmt.Errorf("post-commit reload: %w", err)
		}
		config.Publisher.Store(cfg)
		if err := a.promoteToSteadyState(ctx); err != nil {
			return fmt.Errorf("post-commit promote: %w", err)
		}
		a.compensateWizardSeed(ctx)
		return nil
	}
}

// compensateWizardSeed fires the model refresh for the account the
// just-committed wizard seeded. The insert runs on the commit's
// short-lived store before any ModelRefresher exists, so unlike the
// admin/OAuth/import creation paths it cannot refresh inline — the
// reloader calls this after promoteToSteadyState has wired
// a.modelRefresher instead. Best-effort by design: a failure here only
// logs, and the manual POST /api/admin/accounts/{id}/models/refresh
// endpoint remains the operator's remediation (e.g. when the process
// died between insert and promotion).
func (a *App) compensateWizardSeed(ctx context.Context) {
	id := a.wizardAccounts.seededAccountID
	if id == 0 || a.modelRefresher == nil || a.store == nil {
		return
	}
	acct, err := store.NewAccountRepo(a.store.Engine()).GetByID(ctx, id)
	if err != nil {
		a.logger.Warn("setup_seeded_account_load_failed",
			"account_id", id,
			"error", err,
		)
		return
	}
	a.modelRefresher.TriggerAsync(acct, a.accountModelRepo)
}
