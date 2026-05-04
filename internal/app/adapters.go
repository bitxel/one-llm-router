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
type accountCreator struct{}

// CreateAccount implements setup.AccountCreator.
func (accountCreator) CreateAccount(ctx context.Context, driver, url string, account *domain.UpstreamAccount) error {
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
// The function is called OUTSIDE setup.Serialiser (commit releases
// the lock before invoking the reloader) so promoteToSteadyState is
// free to acquire its own mutex without risking a deadlock.
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
		return nil
	}
}
