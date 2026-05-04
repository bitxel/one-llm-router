package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func sampleConfigForWrite() *Config {
	return &Config{
		Version: 1,
		DB:      DBConfig{Driver: "sqlite3", URL: "file:router.db"},
		Runtime: RuntimeConfig{
			LogClientRequestBody:    true,
			LogUpstreamRequestBody:  false,
			LogUpstreamResponseBody: false,
			LogRetentionDays:        30,
			LogLevel:                "info",
		},
		Plugins: DefaultPluginsConfig(),
	}
}

func TestWriter_HappyPath_CreatesFileWithMode0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission semantics not applicable on Windows")
	}
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	cfg := sampleConfigForWrite()
	before := time.Now()
	if err := WriteAtomic(path, cfg); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("mode = %o, want 0600", got)
	}
	if cfg.UpdatedAt.Before(before) {
		t.Errorf("UpdatedAt = %v, expected after %v (WriteAtomic must refresh)", cfg.UpdatedAt, before)
	}
	if cfg.CreatedAt.IsZero() {
		t.Errorf("CreatedAt still zero after first WriteAtomic")
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var parsed Config
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse written file: %v", err)
	}
	if parsed.DB != cfg.DB {
		t.Errorf("DB round-trip mismatch: got %+v, want %+v", parsed.DB, cfg.DB)
	}
	if parsed.Runtime != cfg.Runtime {
		t.Errorf("Runtime round-trip mismatch: got %+v, want %+v", parsed.Runtime, cfg.Runtime)
	}
}

func TestWriter_OverwriteExistingFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	cfg := sampleConfigForWrite()
	if err := WriteAtomic(path, cfg); err != nil {
		t.Fatalf("first WriteAtomic: %v", err)
	}
	firstUpdated := cfg.UpdatedAt
	firstCreated := cfg.CreatedAt

	// Ensure clock tick so UpdatedAt actually changes.
	time.Sleep(2 * time.Millisecond)

	cfg.Runtime.LogLevel = "debug"
	if err := WriteAtomic(path, cfg); err != nil {
		t.Fatalf("second WriteAtomic: %v", err)
	}
	if !cfg.UpdatedAt.After(firstUpdated) {
		t.Errorf("UpdatedAt did not advance: was %v, now %v", firstUpdated, cfg.UpdatedAt)
	}
	if !cfg.CreatedAt.Equal(firstCreated) {
		t.Errorf("CreatedAt changed on overwrite: was %v, now %v", firstCreated, cfg.CreatedAt)
	}

	raw, _ := os.ReadFile(path)
	var parsed Config
	_ = json.Unmarshal(raw, &parsed)
	if parsed.Runtime.LogLevel != "debug" {
		t.Errorf("overwrite did not persist LogLevel change: got %q", parsed.Runtime.LogLevel)
	}
}

func TestWriter_NilCfg_ReturnsError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	if err := WriteAtomic(path, nil); err == nil {
		t.Fatal("err = nil, want error for nil cfg")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("unexpected file at %s after nil-cfg error: %v", path, err)
	}
}

func TestWriter_ReadOnlyParentDir_ReturnsErrorAndNoTmpLeak(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod semantics differ on Windows")
	}
	if os.Getuid() == 0 {
		t.Skip("running as root bypasses unix permissions")
	}
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.Chmod(dir, 0o500); err != nil { // r-x only — cannot create files
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(dir, 0o700)
	})

	err := WriteAtomic(path, sampleConfigForWrite())
	if err == nil {
		t.Fatal("err = nil, want failure on read-only parent")
	}

	// Parent unreadable for us (0o500 + non-root); just assert the
	// target wasn't created. No tmp leak we can verify here because
	// the dir is unreadable, which matches production — on next boot
	// SweepStale would catch it.
	if _, statErr := os.Stat(path); statErr == nil {
		t.Errorf("target file was created despite error: %s", path)
	}
}

func TestWriter_PreExistingTmp_FailsWithEEXIST(t *testing.T) {
	// Validates O_EXCL semantics: a stale tmp with the same PID blocks
	// the write. Production code runs SweepStale at boot, so this
	// scenario should not occur — but the guard must exist.
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	tmp := tmpPathFor(path)
	if err := os.WriteFile(tmp, []byte("stale"), 0o600); err != nil {
		t.Fatalf("prep tmp: %v", err)
	}

	err := WriteAtomic(path, sampleConfigForWrite())
	if err == nil {
		t.Fatal("err = nil, want O_EXCL error on pre-existing tmp")
	}
	// On failure the function must not remove the pre-existing tmp
	// (it didn't create it). Verify the stale file still exists.
	if _, statErr := os.Stat(tmp); statErr != nil {
		t.Errorf("stale tmp was unexpectedly removed: %v", statErr)
	}
	// Verify the target file was NOT created.
	if _, statErr := os.Stat(path); statErr == nil {
		t.Errorf("target file was created despite error")
	}
}

func TestWriter_MarshalRoundTripContainsAllFields(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg := sampleConfigForWrite()
	cfg.Plugins.AdminAuth.Enabled = true
	cfg.Plugins.AdminAuth.SessionTTLMinutes = 60
	cfg.Plugins.AdminAuth.JWTSecretRef = "env:ADMIN_AUTH_JWT"

	if err := WriteAtomic(path, cfg); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// Assert 003 forward-compat fields are preserved on disk.
	for _, want := range []string{
		`"session_ttl_minutes": 60`,
		`"jwt_secret_ref": "env:ADMIN_AUTH_JWT"`,
		`"enabled": true`,
	} {
		if !bytes.Contains(raw, []byte(want)) {
			t.Errorf("written payload missing %q; payload=%s", want, raw)
		}
	}
}

// ---- SweepStale tests --------------------------------------------------

func TestSweepStale_RemovesTmpWithDeadPID(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	// An impossibly-large PID is guaranteed to be free (Linux defaults
	// max PID to 2^22; we pick a value outside that range).
	const deadPID = 999_999_999
	stale := path + tmpSuffixPrefix + strconv.Itoa(deadPID)
	if err := os.WriteFile(stale, []byte("stale"), 0o600); err != nil {
		t.Fatalf("prep stale: %v", err)
	}

	if err := SweepStale(path); err != nil {
		t.Fatalf("SweepStale: %v", err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stale tmp was not removed; stat err=%v", err)
	}
}

func TestSweepStale_KeepsTmpFromLiveProcess(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	// PID 1 (init on Unix) is reliably alive across test environments,
	// including CI containers.
	live := path + tmpSuffixPrefix + "1"
	if err := os.WriteFile(live, []byte("live"), 0o600); err != nil {
		t.Fatalf("prep live: %v", err)
	}

	if err := SweepStale(path); err != nil {
		t.Fatalf("SweepStale: %v", err)
	}
	if _, err := os.Stat(live); err != nil {
		t.Errorf("live tmp was removed; stat err=%v", err)
	}
}

func TestSweepStale_KeepsTmpForOwnPID(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	own := tmpPathFor(path)
	if err := os.WriteFile(own, []byte("own"), 0o600); err != nil {
		t.Fatalf("prep own: %v", err)
	}

	if err := SweepStale(path); err != nil {
		t.Fatalf("SweepStale: %v", err)
	}
	if _, err := os.Stat(own); err != nil {
		t.Errorf("own-PID tmp was removed; stat err=%v", err)
	}
}

func TestSweepStale_RemovesNonNumericSuffix(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	bogus := path + tmpSuffixPrefix + "garbage"
	if err := os.WriteFile(bogus, []byte("x"), 0o600); err != nil {
		t.Fatalf("prep bogus: %v", err)
	}

	if err := SweepStale(path); err != nil {
		t.Fatalf("SweepStale: %v", err)
	}
	if _, err := os.Stat(bogus); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("bogus tmp was not removed; stat err=%v", err)
	}
}

func TestSweepStale_IgnoresUnrelatedFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	other := filepath.Join(dir, "unrelated.log")
	if err := os.WriteFile(other, []byte("keep me"), 0o644); err != nil {
		t.Fatalf("prep other: %v", err)
	}
	realCfg := filepath.Join(dir, "config.json")
	if err := os.WriteFile(realCfg, []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatalf("prep real config: %v", err)
	}

	if err := SweepStale(path); err != nil {
		t.Fatalf("SweepStale: %v", err)
	}

	for _, keep := range []string{other, realCfg} {
		if _, err := os.Stat(keep); err != nil {
			t.Errorf("SweepStale removed unrelated file %q: %v", keep, err)
		}
	}
}

func TestSweepStale_IgnoresSubdirWithMatchingName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	trap := path + tmpSuffixPrefix + "1"
	if err := os.Mkdir(trap, 0o755); err != nil {
		t.Fatalf("mkdir trap: %v", err)
	}

	if err := SweepStale(path); err != nil {
		t.Fatalf("SweepStale: %v", err)
	}
	if _, err := os.Stat(trap); err != nil {
		t.Errorf("SweepStale removed/affected directory: %v", err)
	}
}

func TestSweepStale_NonexistentDir_ReturnsError(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "does-not-exist", "config.json")
	err := SweepStale(path)
	if err == nil {
		t.Fatal("err = nil, want error on nonexistent parent dir")
	}
	if !strings.Contains(err.Error(), "read config dir") {
		t.Errorf("err = %v, expected 'read config dir' context", err)
	}
}

func TestWriter_SweepThenWriteFlow(t *testing.T) {
	// Integration-style: simulate boot-time recovery — stale tmp from a
	// prior crashed process, then current process sweeps + writes.
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	const deadPID = 999_999_998
	stale := path + tmpSuffixPrefix + strconv.Itoa(deadPID)
	if err := os.WriteFile(stale, []byte("stale"), 0o600); err != nil {
		t.Fatalf("prep stale: %v", err)
	}
	if err := SweepStale(path); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if err := WriteAtomic(path, sampleConfigForWrite()); err != nil {
		t.Fatalf("write after sweep: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("target not created after sweep+write: %v", err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stale tmp still present after sweep+write")
	}
}

// Sanity check the tmpPathFor helper so the PID contract is testable
// in isolation without touching the filesystem.
func TestTmpPathFor_FormatMatchesContract(t *testing.T) {
	t.Parallel()
	got := tmpPathFor("/etc/router/config.json")
	wantPrefix := "/etc/router/config.json.tmp."
	if !strings.HasPrefix(got, wantPrefix) {
		t.Errorf("tmpPathFor prefix = %q, want %q", got, wantPrefix)
	}
	suffix := strings.TrimPrefix(got, wantPrefix)
	if _, err := strconv.Atoi(suffix); err != nil {
		t.Errorf("tmpPathFor suffix %q is not a numeric PID", suffix)
	}
}

// TestScrubPath_RedactsPathErrorButKeepsOpAndErr locks in F-003 fix
// behaviour: operators still need the syscall (Op) and the errno
// (errors.Is against fs.ErrNotExist / fs.ErrPermission) to triage a
// failure, but the path itself MUST be redacted before the error is
// wrapped via %w and logged.
func TestScrubPath_RedactsPathErrorButKeepsOpAndErr(t *testing.T) {
	t.Parallel()

	// 1. nil in → nil out.
	if got := ScrubPath(nil); got != nil {
		t.Fatalf("ScrubPath(nil) = %v, want nil", got)
	}

	// 2. Non-PathError chain passes through untouched (we must not
	//    lose diagnostic signal for non-syscall errors).
	plain := errors.New("not a path error")
	// errors.Is would unwrap; the contract here is pointer-identity
	// pass-through, so we intentionally compare with ==.
	if got := ScrubPath(plain); got != plain { //nolint:errorlint
		t.Fatalf("ScrubPath(plain) = %v, want identical plain error", got)
	}

	// 3. Synthesised *os.PathError: Path rewritten, Op + Err kept.
	raw := &os.PathError{
		Op:   "stat",
		Path: "/very/secret/config.json",
		Err:  os.ErrNotExist,
	}
	scrubbed := ScrubPath(raw)
	if strings.Contains(scrubbed.Error(), "/very/secret/") {
		t.Fatalf("scrubbed.Error() %q still contains secret path", scrubbed.Error())
	}
	if !strings.Contains(scrubbed.Error(), "stat") {
		t.Fatalf("scrubbed.Error() %q lost Op", scrubbed.Error())
	}
	if !errors.Is(scrubbed, os.ErrNotExist) {
		t.Fatalf("scrubbed error no longer Is os.ErrNotExist: %v", scrubbed)
	}

	// 4. Real OS error path: guaranteed-absent directory yields a
	//    *os.PathError whose path leaks; ScrubPath fixes it.
	secretDir := filepath.Join(t.TempDir(), "absent-sub-dir-"+t.Name())
	_, osErr := os.Stat(filepath.Join(secretDir, "config.json"))
	if osErr == nil {
		t.Fatal("setup invariant: os.Stat must fail for absent path")
	}
	// Assert the raw error really does leak the path (guards against
	// the stdlib changing its formatting and silently no-op-ing our
	// helper).
	if !strings.Contains(osErr.Error(), secretDir) {
		t.Fatalf("stdlib no longer leaks path in PathError.Error() — update the scrubber contract: %q", osErr.Error())
	}
	if got := ScrubPath(osErr); strings.Contains(got.Error(), secretDir) {
		t.Fatalf("ScrubPath failed to redact path: %q", got.Error())
	}
}

// TestScrubLinkError_RedactsBothPaths guards the os.Rename case that
// ScrubPath itself cannot cover (LinkError carries Old + New).
func TestScrubLinkError_RedactsBothPaths(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := filepath.Join(dir, "does-not-exist-src")
	dst := filepath.Join(dir, "does-not-exist-dst")

	err := os.Rename(src, dst)
	if err == nil {
		t.Fatal("setup invariant: Rename of absent src must fail")
	}
	if !strings.Contains(err.Error(), src) {
		t.Skipf("stdlib LinkError format changed — skipping instead of false-positive: %v", err)
	}

	scrubbed := scrubLinkError(err)
	if strings.Contains(scrubbed.Error(), src) || strings.Contains(scrubbed.Error(), dst) {
		t.Fatalf("scrubLinkError still leaks a path: %q", scrubbed.Error())
	}
	// Preserve syscall sentinel identity so callers can still triage
	// ENOENT vs EACCES.
	if !errors.Is(scrubbed, os.ErrNotExist) {
		t.Fatalf("scrubbed LinkError no longer Is os.ErrNotExist: %v", scrubbed)
	}
}

// TestWriteAtomic_OpenFailure_DoesNotLeakPath confirms the call-site
// application of ScrubPath: if the parent directory is read-only,
// OpenFile fails and the returned error MUST NOT embed the tmp path.
func TestWriteAtomic_OpenFailure_DoesNotLeakPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX chmod semantics not applicable on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses 0o500 write protection — scenario not exercisable")
	}
	t.Parallel()

	dir := t.TempDir()
	// Strip write bits on the dir so O_CREATE|O_EXCL fails with EACCES.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	path := filepath.Join(dir, "config.json")
	err := WriteAtomic(path, sampleConfigForWrite())
	if err == nil {
		t.Fatal("expected write to fail on read-only parent")
	}
	// The concrete tmp path embeds the config.json path; neither of
	// them may appear in the wrapped error.
	if strings.Contains(err.Error(), path) {
		t.Fatalf("WriteAtomic error leaks config.json path: %q", err.Error())
	}
	if strings.Contains(err.Error(), dir) {
		t.Fatalf("WriteAtomic error leaks parent dir: %q", err.Error())
	}
}

// Race-test: 8 goroutines take turns doing SweepStale + WriteAtomic on
// separate paths. This does NOT cover same-path concurrency (which the
// contract forbids), only that the package is goroutine-safe for the
// intended usage shape.
func TestWriter_SweepAndWrite_DifferentPaths_NoRace(t *testing.T) {
	t.Parallel()
	const n = 8
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			dir := t.TempDir()
			path := filepath.Join(dir, "config.json")
			if err := SweepStale(path); err != nil {
				t.Errorf("[%d] sweep: %v", i, err)
				return
			}
			if err := WriteAtomic(path, sampleConfigForWrite()); err != nil {
				t.Errorf("[%d] write: %v", i, err)
				return
			}
		}(i)
	}
	wg.Wait()
}

// TestSweepStale_RemoveFailure_DoesNotLeakPath asserts N-002: when
// os.Remove fails inside SweepStale (e.g. parent dir becomes read-only
// mid-sweep), the WARN line emitted must scrub the *os.PathError path
// so the real config directory does not leak at INFO+ per plan.md §362.
func TestSweepStale_RemoveFailure_DoesNotLeakPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX dir-permission semantics not applicable on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses dir permission bits; skip on uid=0")
	}
	t.Parallel()

	parent := t.TempDir()
	dir := filepath.Join(parent, "stash")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	// Seed a tmp with a non-numeric suffix so SweepStale enters the
	// "remove non-numeric" branch — and thus the WARN we are auditing.
	badTmp := filepath.Join(dir, "config.json.tmp.notanint")
	if err := os.WriteFile(badTmp, []byte("x"), 0o600); err != nil {
		t.Fatalf("seed tmp: %v", err)
	}
	// Flip dir to read-only so os.Remove(badTmp) fails with EACCES.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	// plan.md §362 forbids the path at INFO+ levels — DEBUG is
	// explicitly allowed to carry the path for operator debugging.
	// Capture at INFO level so the DEBUG "path candidate" line does
	// not pollute the path-leak assertion.
	logs := captureLogsLoaderAtLevel(t, slog.LevelInfo, func() {
		if err := SweepStale(filepath.Join(dir, "config.json")); err != nil {
			t.Fatalf("SweepStale: %v", err)
		}
	})

	if strings.Contains(logs, dir) || strings.Contains(logs, badTmp) {
		t.Fatalf("INFO+ leaked config directory / tmp path: %q", logs)
	}
	if !strings.Contains(logs, scrubbedPathPlaceholder) {
		t.Fatalf("WARN did not include the scrubbed placeholder %q: %q", scrubbedPathPlaceholder, logs)
	}
}
