package setup_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/user/one-llm-router/internal/api/errcode"
	"github.com/user/one-llm-router/internal/domain"
	"github.com/user/one-llm-router/internal/setup"
)

// fakeMigratorFactory satisfies setup.MigratorFactory.
type fakeMigratorFactory struct {
	openErr error
	upErr   error
	opened  int
	closed  int
}

func (f *fakeMigratorFactory) Open(_ context.Context, _, _ string) (setup.MigratorHandle, error) {
	if f.openErr != nil {
		return nil, f.openErr
	}
	f.opened++
	return &fakeMigrator{parent: f, upErr: f.upErr}, nil
}

type fakeMigrator struct {
	parent *fakeMigratorFactory
	upErr  error
}

func (m *fakeMigrator) Up(_ context.Context) error {
	return m.upErr
}

func (m *fakeMigrator) Close() error {
	m.parent.closed++
	return nil
}

type fakeCreator struct {
	err       error
	account   *domain.UpstreamAccount
	createdID int64
}

func (c *fakeCreator) CreateAccount(_ context.Context, _, _ string, a *domain.UpstreamAccount) error {
	if c.err != nil {
		return c.err
	}
	c.createdID++
	a.ID = c.createdID
	c.account = a
	return nil
}

func validCommitReq(dsn string) *setup.CommitRequest {
	return &setup.CommitRequest{
		DB: setup.DBRequestBlock{Driver: "sqlite3", URL: dsn},
		FirstAccount: setup.AccountRequestBlock{
			Name: "prod-01", Provider: "openai", APIKey: "sk-test",
		},
		Plugins: setup.PluginsRequestBlock{},
	}
}

func TestCommit_HappyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	dsn := filepath.Join(dir, "router.db")

	factory := &fakeMigratorFactory{}
	creator := &fakeCreator{}

	reloaded := false
	reloader := func(_ context.Context) error {
		reloaded = true
		return nil
	}

	res, failure := setup.Commit(context.Background(), validCommitReq(dsn),
		cfgPath, factory, creator, reloader, nil)
	if failure != nil {
		t.Fatalf("Commit failure: %+v", failure)
	}
	if res.AccountID != 1 {
		t.Errorf("AccountID = %d, want 1", res.AccountID)
	}
	if res.APIKeyFP == "" {
		t.Errorf("APIKeyFP should be non-empty")
	}

	// config.json materialized?
	if _, err := os.Stat(cfgPath); err != nil {
		t.Fatalf("config.json not written: %v", err)
	}

	// Migrator opened and closed?
	if factory.opened != 1 || factory.closed != 1 {
		t.Errorf("migrator open/close = %d/%d, want 1/1", factory.opened, factory.closed)
	}

	// Account inserted?
	if creator.account == nil || creator.account.Name != "prod-01" {
		t.Errorf("account not inserted correctly: %+v", creator.account)
	}

	// Reloader invoked?
	if !reloaded {
		t.Errorf("reloader not invoked")
	}
}

// setup-api.md v2.4 — a wizard that omits first_account must still
// migrate the database, write config.json, and invoke the reloader.
// CreateAccount MUST NOT be called; CommitResult.AccountID MUST be 0.
func TestCommit_SkipsFirstAccount(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	dsn := filepath.Join(dir, "router.db")

	factory := &fakeMigratorFactory{}
	creator := &fakeCreator{}

	reloaded := false
	reloader := func(_ context.Context) error {
		reloaded = true
		return nil
	}

	req := &setup.CommitRequest{
		DB:           setup.DBRequestBlock{Driver: "sqlite3", URL: dsn},
		FirstAccount: setup.AccountRequestBlock{},
		Plugins:      setup.PluginsRequestBlock{},
	}
	res, failure := setup.Commit(context.Background(), req,
		cfgPath, factory, creator, reloader, nil)
	if failure != nil {
		t.Fatalf("Commit with empty first_account failed: %+v", failure)
	}
	if res.AccountID != 0 {
		t.Errorf("AccountID = %d, want 0 for skipped first_account", res.AccountID)
	}
	if res.APIKeyFP != "" {
		t.Errorf("APIKeyFP = %q, want empty string for skipped first_account", res.APIKeyFP)
	}

	if creator.createdID != 0 || creator.account != nil {
		t.Errorf("CreateAccount must not be called when first_account is empty; got %+v", creator.account)
	}
	if factory.opened != 1 || factory.closed != 1 {
		t.Errorf("migrator open/close = %d/%d, want 1/1", factory.opened, factory.closed)
	}
	if _, err := os.Stat(cfgPath); err != nil {
		t.Fatalf("config.json not written on skipped first_account: %v", err)
	}
	if !reloaded {
		t.Errorf("reloader not invoked on skipped first_account")
	}
}

func TestCommit_ValidationError_BlocksDB(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	factory := &fakeMigratorFactory{}
	creator := &fakeCreator{}

	badReq := validCommitReq("router.db")
	badReq.DB.Driver = "invalid"

	_, failure := setup.Commit(context.Background(), badReq, cfgPath,
		factory, creator, nil, nil)
	if failure == nil {
		t.Fatal("expected validation failure")
	}
	if failure.Code != errcode.InvalidDriver {
		t.Errorf("code = %d, want %d", failure.Code, errcode.InvalidDriver)
	}
	if failure.SysErr {
		t.Error("validation failure should be biz error, not sys error")
	}
	if factory.opened != 0 || creator.createdID != 0 {
		t.Error("DB should not be touched on validation failure")
	}
	if _, err := os.Stat(cfgPath); err == nil {
		t.Error("config.json should not be written on validation failure")
	}
}

func TestCommit_ProbeFailure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	// Non-existent parent dir for sqlite → probe fails.
	dsn := filepath.Join(dir, "no-such-subdir", "router.db")

	factory := &fakeMigratorFactory{}
	creator := &fakeCreator{}

	_, failure := setup.Commit(context.Background(), validCommitReq(dsn),
		cfgPath, factory, creator, nil, nil)
	if failure == nil {
		t.Fatal("expected probe failure")
	}
	// Commit-time probe failure mirrors probe-dsn's mapping and is
	// a business error (HTTP 200 + code=2003) per setup-api.md
	// "as probe | same". It must NOT be a sys error — the operator
	// can fix the DSN and retry without a router restart.
	if failure.Code != errcode.InvalidDSN {
		t.Errorf("code = %d, want %d (invalid_dsn)", failure.Code, errcode.InvalidDSN)
	}
	if failure.SysErr {
		t.Error("probe failure should be a business error (SysErr=false), not system")
	}
	if failure.Hint == "" {
		t.Error("probe failure should carry a driver-family hint")
	}
}

func TestCommit_MigrateFailure_DoesNotWriteConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	dsn := filepath.Join(dir, "router.db")

	factory := &fakeMigratorFactory{upErr: errors.New("schema conflict")}
	creator := &fakeCreator{}

	_, failure := setup.Commit(context.Background(), validCommitReq(dsn),
		cfgPath, factory, creator, nil, nil)
	if failure == nil {
		t.Fatal("expected migrate failure")
	}
	if failure.Code != errcode.MigrateFailed {
		t.Errorf("code = %d, want %d", failure.Code, errcode.MigrateFailed)
	}
	if _, err := os.Stat(cfgPath); err == nil {
		t.Error("config.json should not be written on migrate failure")
	}
	if creator.createdID != 0 {
		t.Error("account should not be inserted on migrate failure")
	}
}

func TestCommit_CreateAccountFailure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	dsn := filepath.Join(dir, "router.db")

	factory := &fakeMigratorFactory{}
	creator := &fakeCreator{err: errors.New("unique constraint")}

	_, failure := setup.Commit(context.Background(), validCommitReq(dsn),
		cfgPath, factory, creator, nil, nil)
	if failure == nil {
		t.Fatal("expected account creation failure")
	}
	if failure.Code != errcode.CommitTxFailed {
		t.Errorf("code = %d, want %d", failure.Code, errcode.CommitTxFailed)
	}
	if _, err := os.Stat(cfgPath); err == nil {
		t.Error("config.json should not be written on account insert failure")
	}
}

func TestCommit_ReloadFailure_StillWritesConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	dsn := filepath.Join(dir, "router.db")

	factory := &fakeMigratorFactory{}
	creator := &fakeCreator{}
	reloader := func(_ context.Context) error {
		return errors.New("live config invalid")
	}

	_, failure := setup.Commit(context.Background(), validCommitReq(dsn),
		cfgPath, factory, creator, reloader, nil)
	if failure == nil {
		t.Fatal("expected reload failure")
	}
	if _, err := os.Stat(cfgPath); err != nil {
		t.Error("config.json should have been written even if reload fails")
	}
}

func TestCommit_ContextCancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, failure := setup.Commit(ctx, validCommitReq("x.db"), "/tmp/config.json",
		&fakeMigratorFactory{}, &fakeCreator{}, nil, nil)
	if failure == nil {
		t.Fatal("expected failure from cancelled ctx")
	}
}

func TestCommit_NilFactory(t *testing.T) {
	t.Parallel()
	_, failure := setup.Commit(context.Background(), validCommitReq("x.db"),
		"/tmp/x", nil, &fakeCreator{}, nil, nil)
	if failure == nil || failure.Code != errcode.InternalError {
		t.Fatalf("want internal error for nil factory, got %+v", failure)
	}
}

// TestCommit_ConcurrentCommits_OneWinner exercises US-1 Edge-2:
// "if two operators post commit simultaneously, exactly one wins and
// the other sees setup_already_done". setup.Serialiser + the under-
// lock re-stat in Commit must jointly guarantee this invariant.
//
// The second caller MUST NOT be allowed to progress past the IsDone
// check into migrate/account-insert/WriteAtomic — F-003's fix
// (re-stat cfgPath after Serialiser.Lock) is precisely the guard
// under test here.
//
// Why an integration-flavoured test rather than a unit test over
// Serialiser alone: the contract we care about is behavioural
// ("one caller receives 2001 setup_already_done, one receives a
// success result, and config.json is written exactly once"), not
// "the mutex is acquired in this order". A timing-free test on two
// goroutines racing into Commit is the cheapest way to reach that
// assertion — t.Parallel is NOT used because we want the second
// commit to observe the filesystem state the first one leaves.
func TestCommit_ConcurrentCommits_OneWinner(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	dsn := filepath.Join(dir, "router.db")

	factory := &fakeMigratorFactory{}
	creator := &fakeCreator{}

	// Two goroutines race into Commit with the same cfgPath + dsn.
	// Serialiser guarantees they serialise; the second one MUST see
	// config.json already materialised by the first and return
	// 2001 setup_already_done without touching the DB again.
	type result struct {
		res     *setup.CommitResult
		failure *setup.CommitFailure
	}
	ch := make(chan result, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			res, failure := setup.Commit(
				context.Background(),
				validCommitReq(dsn),
				cfgPath,
				factory,
				creator,
				func(_ context.Context) error { return nil },
				nil,
			)
			ch <- result{res, failure}
		}()
	}
	close(start)

	results := []result{<-ch, <-ch}

	var successes, alreadyDone int
	for _, r := range results {
		switch {
		case r.failure == nil:
			successes++
		case r.failure.Code == errcode.SetupAlreadyDone && !r.failure.SysErr:
			alreadyDone++
		default:
			t.Fatalf("unexpected commit outcome: res=%+v failure=%+v", r.res, r.failure)
		}
	}

	if successes != 1 {
		t.Errorf("successes = %d, want 1", successes)
	}
	if alreadyDone != 1 {
		t.Errorf("already_done = %d, want 1", alreadyDone)
	}
	// Ensure the DB was only touched by the winner.
	if factory.opened != 1 {
		t.Errorf("migrator open count = %d, want 1 (second commit must not migrate)", factory.opened)
	}
	if creator.createdID != 1 {
		t.Errorf("accounts created = %d, want 1 (second commit must not insert)", creator.createdID)
	}
	if _, err := os.Stat(cfgPath); err != nil {
		t.Fatalf("config.json not written: %v", err)
	}
}

// TestCommit_AfterSetupAlreadyDone_ReturnsBizError is the sequential
// variant: a second commit issued AFTER the first one completed must
// reject with 2001 setup_already_done rather than re-running migrate/
// insert (which would otherwise violate uniqueness constraints on
// the upstream_accounts.name column).
func TestCommit_AfterSetupAlreadyDone_ReturnsBizError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	dsn := filepath.Join(dir, "router.db")

	factory := &fakeMigratorFactory{}
	creator := &fakeCreator{}
	reloader := func(_ context.Context) error { return nil }

	if _, failure := setup.Commit(context.Background(), validCommitReq(dsn),
		cfgPath, factory, creator, reloader, nil); failure != nil {
		t.Fatalf("first commit failed: %+v", failure)
	}

	// Second commit against the same cfgPath must be rejected by
	// the under-lock IsDone check WITHOUT re-opening the migrator.
	_, failure := setup.Commit(context.Background(), validCommitReq(dsn),
		cfgPath, factory, creator, reloader, nil)
	if failure == nil {
		t.Fatal("second commit should have returned setup_already_done")
	}
	if failure.Code != errcode.SetupAlreadyDone {
		t.Errorf("code = %d, want %d", failure.Code, errcode.SetupAlreadyDone)
	}
	if failure.SysErr {
		t.Error("setup_already_done must be a biz error, not sys error")
	}
	if factory.opened != 1 {
		t.Errorf("migrator opened %d times, want 1 (second commit must short-circuit)", factory.opened)
	}
	if creator.createdID != 1 {
		t.Errorf("accounts created %d, want 1 (second commit must short-circuit)", creator.createdID)
	}
}
