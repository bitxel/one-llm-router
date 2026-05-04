package setup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/user/one-llm-router/internal/store"
)

// ProbeTimeout is the hard deadline every ProbeDSN call enforces end-
// to-end. Matches spec FR-004 ("wizard must not wait more than 5s on
// an unreachable DSN") and the quickstart perf budget.
const ProbeTimeout = 5 * time.Second

// ProbeDSN opens a throwaway database connection to (driver, url),
// runs a round-trip (`SELECT 1`), captures a driver-specific version
// string, and closes the connection. Used by the wizard's DSN
// validation step.
//
// Returns:
//   - latencyMs      — wall-clock milliseconds from dial start to the
//     completion of the SELECT 1 round-trip. Useful
//     for the wizard's "probe ok, round-trip: 3 ms"
//     banner. Only meaningful when err == nil.
//   - serverVersion  — non-authoritative string reported by the DB
//     server (SQLite's `sqlite_version()`, Postgres'
//     `version()`, MySQL's `@@version`). Empty on
//     sqlite3 files that have never been written to;
//     never contains secrets. Only meaningful when
//     err == nil.
//   - err            — a wrapped driver error on any failure. The
//     caller (setup-api handler) is responsible for
//     mapping the error to the envelope code 2003
//     invalid_dsn and for attaching a driver-family-
//     specific remediation hint; this function is
//     deliberately hint-free so its responsibilities
//     stay narrow.
//
// The function hard-caps wall-clock time at ProbeTimeout by deriving
// a WithTimeout context from ctx; callers may pass a shorter ctx to
// tighten the bound further but not loosen it.
func ProbeDSN(ctx context.Context, driver, url string) (int64, string, error) {
	if driver == "" {
		return 0, "", errors.New("probe: driver is empty")
	}
	if url == "" {
		return 0, "", errors.New("probe: url is empty")
	}
	if err := ctx.Err(); err != nil {
		return 0, "", err
	}

	dialect, err := store.LookupDialect(driver)
	if err != nil {
		// LookupDialect already wraps with the supported-drivers list,
		// so a bare return preserves the useful message.
		return 0, "", fmt.Errorf("probe: %w", err)
	}
	dsn := dialect.NormalizeDSN(url)

	// Hard deadline. context.WithTimeout on top of the caller ctx
	// means cancellation propagates both ways: caller-cancelled
	// wizards abort early, and a hung driver dial cannot outlast the
	// cap.
	probeCtx, cancel := context.WithTimeout(ctx, ProbeTimeout)
	defer cancel()

	// start is captured once at the beginning so EVERY return path —
	// success and failure — can report the wall-clock elapsed time.
	// setup-api.md §POST /api/setup/probe-dsn mandates `data.latency_ms`
	// on unreachable responses so the wizard UI can distinguish
	// "timed out after 5000 ms" from "refused in 3 ms" (both are
	// 2003 invalid_dsn but have very different remediation).
	start := time.Now()
	elapsedMs := func() int64 { return time.Since(start).Milliseconds() }

	// sql.Open does not actually connect; PingContext / QueryContext
	// do. Cap connection count to 1 so we do not leak extra
	// background connections while the probe runs.
	db, err := sql.Open(dialect.XormDriverName(), dsn)
	if err != nil {
		return elapsedMs(), "", fmt.Errorf("probe: open: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(0)
	defer func() { _ = db.Close() }()

	if err := db.PingContext(probeCtx); err != nil {
		return elapsedMs(), "", classifyProbeError(probeCtx, err)
	}

	// SELECT 1 — canonical liveness check. Portable across every
	// driver in 002's supported matrix.
	if _, err := db.ExecContext(probeCtx, "SELECT 1"); err != nil {
		return elapsedMs(), "", classifyProbeError(probeCtx, err)
	}

	latencyMs := elapsedMs()

	// Best-effort version lookup. The spec requires a string; on a
	// driver whose version query fails we return "" rather than
	// failing the whole probe — the operator cares that the DB
	// accepted `SELECT 1`, not that we know the patch version.
	version := queryServerVersion(probeCtx, db, driver)

	return latencyMs, version, nil
}

// classifyProbeError maps raw sql.* errors into something the caller
// can grep/group. Specifically it translates context.DeadlineExceeded
// into a wrapped error whose message mentions the 5-second cap so the
// wizard UI can say "the DB did not respond within 5 s" without
// re-inventing the deadline.
func classifyProbeError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("probe: deadline exceeded after %s: %w", ProbeTimeout, err)
	}
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return fmt.Errorf("probe: canceled: %w", err)
	}
	return fmt.Errorf("probe: %w", err)
}

// queryServerVersion picks the correct per-driver interrogation
// statement. Keeps the knowledge co-located with the probe rather
// than forcing each dialect in internal/store to grow a method the
// migrator does not need. If the query fails we swallow the error
// and return "" — see function header above for why.
//
// Statements chosen to match the shapes every MVP deployment sees:
//
//	sqlite3  : SELECT sqlite_version()     → "3.43.2"
//	postgres : SHOW server_version         → "16.2 (Debian 16.2-1.pgdg120+2)"
//	mysql    : SELECT @@version            → "8.0.35" or "10.11.2-MariaDB"
//
// Anything else: "" (unsupported driver will have been caught by
// LookupDialect above; this path is defensive).
func queryServerVersion(ctx context.Context, db *sql.DB, driver string) string {
	var stmt string
	switch driver {
	case "sqlite3":
		stmt = "SELECT sqlite_version()"
	case "postgres":
		stmt = "SHOW server_version"
	case "mysql":
		stmt = "SELECT @@version"
	default:
		return ""
	}
	var v string
	row := db.QueryRowContext(ctx, stmt)
	if err := row.Scan(&v); err != nil {
		return ""
	}
	return v
}
