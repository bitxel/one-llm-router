package setup

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	// Register the sqlite3 dialect (and its driver import) so
	// store.LookupDialect("sqlite3") resolves inside the probe.
	_ "github.com/user/one-llm-router/internal/store"
)

func TestProbeDSN_EmptyArgs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, driver, url string
	}{
		{"empty driver", "", "x"},
		{"empty url", "sqlite3", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := ProbeDSN(context.Background(), tc.driver, tc.url)
			if err == nil {
				t.Fatalf("err = nil, want non-nil for %s", tc.name)
			}
		})
	}
}

func TestProbeDSN_UnknownDriver(t *testing.T) {
	t.Parallel()
	_, _, err := ProbeDSN(context.Background(), "oracle", "irrelevant")
	if err == nil {
		t.Fatal("err = nil, want non-nil for unsupported driver")
	}
	if !strings.Contains(err.Error(), "oracle") {
		t.Errorf("err = %v, want mention of offending driver", err)
	}
}

func TestProbeDSN_CancelledCtx(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := ProbeDSN(ctx, "sqlite3", ":memory:")
	if err == nil {
		t.Fatal("err = nil, want context cancellation")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want Is(context.Canceled)", err)
	}
}

func TestProbeDSN_Sqlite_Memory_Happy(t *testing.T) {
	t.Parallel()
	latency, version, err := ProbeDSN(context.Background(), "sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if latency < 0 {
		t.Errorf("latency = %d, want >= 0", latency)
	}
	// 5-second hard cap, so anything reasonably bigger suggests a bug.
	if latency > int64(ProbeTimeout/time.Millisecond) {
		t.Errorf("latency = %d ms exceeds ProbeTimeout", latency)
	}
	if version == "" {
		t.Error("version = \"\", want non-empty sqlite_version()")
	}
	// The sqlite_version() return should look like "3.x.y".
	if !strings.HasPrefix(version, "3.") {
		t.Errorf("version = %q, want leading \"3.\" for sqlite_version()", version)
	}
}

func TestProbeDSN_Sqlite_FileBacked(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	url := filepath.Join(dir, "probe.db")

	latency, version, err := ProbeDSN(context.Background(), "sqlite3", url)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if version == "" {
		t.Error("version empty on file-backed sqlite probe")
	}
	if latency < 0 {
		t.Errorf("latency = %d, want >= 0", latency)
	}
}

func TestProbeDSN_Sqlite_InvalidDSN(t *testing.T) {
	t.Parallel()
	// A DSN pointing into a non-existent directory with explicit
	// file URI flags forces Open or Ping to fail. We use a path
	// that cannot be created to avoid flaking on actual file
	// semantics.
	dir := t.TempDir()
	bogus := "file:" + filepath.Join(dir, "nope", "sub", "db.sqlite") + "?mode=rw"

	_, _, err := ProbeDSN(context.Background(), "sqlite3", bogus)
	if err == nil {
		t.Fatal("err = nil, want non-nil on unreachable sqlite DSN")
	}
}

func TestProbeDSN_Sqlite_SelectFailure(t *testing.T) {
	// mattn/go-sqlite3 opens :memory: readily, but we can force an
	// Exec failure by pointing at a DSN with mode=ro and a file that
	// does not exist.
	t.Parallel()
	dir := t.TempDir()
	bogus := "file:" + filepath.Join(dir, "does-not-exist.db") + "?mode=ro"

	_, _, err := ProbeDSN(context.Background(), "sqlite3", bogus)
	if err == nil {
		t.Fatal("err = nil, want non-nil on read-only missing file")
	}
}

// fakeDialectMissing is used to assert LookupDialect wrapping.
func TestProbeDSN_UnknownDriver_ErrorMentionsSupported(t *testing.T) {
	t.Parallel()
	_, _, err := ProbeDSN(context.Background(), "cassandra", "x")
	if err == nil {
		t.Fatal("err = nil, want non-nil")
	}
	// LookupDialect already embeds the supported-drivers list; we
	// just make sure the probe wrapper preserves it.
	if !strings.Contains(err.Error(), "sqlite3") {
		t.Errorf("err = %v, want LookupDialect's supported-list to bubble up", err)
	}
}

func TestProbeDSN_RespectsShorterCallerCtx(t *testing.T) {
	// A caller-supplied ctx with a deadline MUCH shorter than
	// ProbeTimeout must win.
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	_, _, err := ProbeDSN(ctx, "sqlite3", ":memory:")
	if err == nil {
		t.Fatal("err = nil, want deadline exceeded / cancellation")
	}
	// Either context.DeadlineExceeded (if Ping hit the deadline) or
	// context.Canceled depending on scheduling. Both are acceptable.
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want Deadline/Cancel", err)
	}
}

func TestProbeDSN_LatencyMonotonic(t *testing.T) {
	// Latency should always be >= 0 and < ProbeTimeout on a
	// successful probe. This protects against future regressions
	// where someone swaps time.Since for a signed subtraction.
	t.Parallel()
	latency, _, err := ProbeDSN(context.Background(), "sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if latency < 0 {
		t.Errorf("latency = %d, must be non-negative", latency)
	}
	budgetMs := int64(ProbeTimeout / time.Millisecond)
	if latency > budgetMs {
		t.Errorf("latency = %d ms > ProbeTimeout %d ms", latency, budgetMs)
	}
}

func TestQueryServerVersion_UnknownDriverReturnsEmpty(t *testing.T) {
	// queryServerVersion is package-private; this test keeps its
	// default-case behaviour deterministic so future driver additions
	// don't accidentally fail silently.
	t.Parallel()
	got := queryServerVersion(context.Background(), nil, "oracle")
	if got != "" {
		t.Errorf("got %q, want \"\" for unknown driver", got)
	}
}
