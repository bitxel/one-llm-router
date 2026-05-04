package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// tmpSuffixPrefix is the literal separator inserted between the target
// path and the writer's PID. Exported only as an internal constant so
// tests can assert filenames without duplicating the magic string.
const tmpSuffixPrefix = ".tmp."

// scrubbedPathPlaceholder is the opaque token we substitute into
// *os.PathError.Path before surfacing it through fmt.Errorf (%w) or
// slog. plan.md §362 forbids the real config.json path appearing at
// INFO+ sinks; stdlib's syscall errors embed that path in their
// Error() output, so the helper below rewrites it while keeping the
// Op/Err parts intact for operator debugging.
const scrubbedPathPlaceholder = "<config>"

// scrubLinkError rewrites *os.LinkError.Old / .New to the placeholder.
// os.Rename returns this variant rather than *os.PathError, so
// ScrubPath alone does not cover it. Kept unexported — callers that
// need to scrub LinkError should be routed through here explicitly;
// the set of stdlib functions returning LinkError is small
// (os.Rename, os.Symlink, os.Link) and deliberately scoped.
func scrubLinkError(err error) error {
	if err == nil {
		return nil
	}
	var le *os.LinkError
	if !errors.As(err, &le) {
		// May still be a *os.PathError (e.g. Rename on a
		// directory stat failure) — fall back to ScrubPath so the
		// single-path case is still covered.
		return ScrubPath(err)
	}
	return &os.LinkError{
		Op:  le.Op,
		Old: scrubbedPathPlaceholder,
		New: scrubbedPathPlaceholder,
		Err: le.Err,
	}
}

// ScrubPath returns a copy of err in which any leaf *os.PathError has
// its Path replaced by scrubbedPathPlaceholder. Op (e.g. "open",
// "stat", "rename") and the wrapped Err (fs.ErrNotExist, EACCES, …)
// are preserved so callers can still errors.Is / errors.As against
// syscall sentinels.
//
// Callers MUST invoke ScrubPath on the raw OS error BEFORE wrapping
// with fmt.Errorf %w; our call sites always hold the raw syscall
// error at the scrub point, so this helper does not attempt to rewrite
// an already-wrapped chain (which would either require message
// surgery or risk re-anchoring the chain head).
//
// Exported because internal/setup/brownfield.go also wraps
// os.Stat/os.WriteFile errors that must be scrubbed before leaving
// the package.
func ScrubPath(err error) error {
	if err == nil {
		return nil
	}
	var pe *os.PathError
	if !errors.As(err, &pe) || pe.Path == "" {
		return err
	}
	// Return a fresh PathError rather than mutating the one in the
	// chain — mutation would leak through into any Defer'd log line
	// or test assertion that captured err before we reached this
	// call site. Callers that had a wrapping prefix should call
	// ScrubPath BEFORE their fmt.Errorf; see writer.go / loader.go /
	// brownfield.go for the expected pattern.
	return &os.PathError{
		Op:   pe.Op,
		Path: scrubbedPathPlaceholder,
		Err:  pe.Err,
	}
}

// WriteAtomic serialises cfg to a tmp file next to path, fsyncs, then
// renames into place. The rename is the SINGLE observable moment the
// file becomes visible to readers — crashes before the rename leave
// only a tmp file (cleaned up on next boot by SweepStale).
//
// Contract (T-016, data-model.md §Write path):
//   - tmp filename is <path>.tmp.<pid>; PID suffixing ensures two
//     *different* processes' aborted writes never collide.
//   - flags are O_CREATE|O_EXCL|O_WRONLY with mode 0600 at creation
//     (NOT post-chmod). Pre-existing stale tmp with same PID causes
//     EEXIST and the write fails — SweepStale MUST run before the first
//     WriteAtomic on any given boot. See also the Commit flow which
//     composes SweepStale → WriteAtomic.
//   - cfg.UpdatedAt is refreshed to time.Now().UTC() BEFORE marshal.
//     cfg.CreatedAt is left untouched unless it's the zero value (first
//     write from wizard commit), in which case it equals UpdatedAt.
//   - on ANY error after tmp-open succeeds, the tmp is best-effort
//     removed; we do NOT leak 0-byte tmp files.
//
// Concurrency: callers MUST serialise WriteAtomic per-path. Two
// goroutines in the SAME process calling WriteAtomic(same path, ...)
// simultaneously will conflict on the tmp filename (same PID). 002's
// call sites (setup.Commit, settings.Update, brownfield materializer)
// are each guarded by their own mutex so concurrent writes are not a
// live concern.
func WriteAtomic(path string, cfg *Config) error {
	if cfg == nil {
		return errors.New("config.WriteAtomic: cfg is nil")
	}

	now := time.Now().UTC()
	cfg.UpdatedAt = now
	if cfg.CreatedAt.IsZero() {
		cfg.CreatedAt = now
	}

	payload, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("config.WriteAtomic: marshal: %w", err)
	}

	tmp := tmpPathFor(path)

	// O_EXCL: refuse to clobber an existing tmp. If the same PID is
	// mid-write (impossible under our serialisation contract) or a
	// stale tmp survived a crash, surface the error so the caller can
	// invoke SweepStale explicitly.
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		// plan.md §362: the tmp path embeds the config.json path and
		// must stay out of INFO+ log sinks. ScrubPath rewrites the
		// *os.PathError.Path so %w wrapping does not leak it.
		return fmt.Errorf("config.WriteAtomic: open tmp: %w", ScrubPath(err))
	}

	// From this point any error must remove the tmp file.
	success := false
	defer func() {
		if !success {
			if rmErr := os.Remove(tmp); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
				// plan.md §362 keeps config.json paths out of INFO+.
				// os.Remove returns *os.PathError, whose Error()
				// embeds the tmp path — scrub it before WARN logging
				// so ops alerts do not leak the config location.
				// DEBUG carries the full path for operators.
				slog.Warn("config.WriteAtomic: failed to clean up tmp after error", "error", ScrubPath(rmErr))
				slog.Debug("config.WriteAtomic tmp cleanup failed path", "tmp", tmp)
			}
		}
	}()

	if _, err := f.Write(payload); err != nil {
		_ = f.Close()
		return fmt.Errorf("config.WriteAtomic: write tmp: %w", ScrubPath(err))
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("config.WriteAtomic: fsync tmp: %w", ScrubPath(err))
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("config.WriteAtomic: close tmp: %w", ScrubPath(err))
	}

	if err := os.Rename(tmp, path); err != nil {
		// plan.md §362: neither tmp nor final path enters the
		// returned error — os.Rename returns *os.LinkError which
		// carries both; ScrubPath rewrites the wrapped PathError and
		// we drop the LinkError variant here (see also loader.go /
		// brownfield.go for the matching pattern on other syscalls).
		return fmt.Errorf("config.WriteAtomic: rename tmp into place: %w", scrubLinkError(err))
	}
	// Persist the rename itself by fsyncing the parent directory.
	// Without this, a power loss after os.Rename returns can leave
	// the filesystem in a state where the new inode exists but the
	// directory entry does not. The fsync is best-effort: if the OS
	// returns EINVAL (e.g. some container-backed filesystems) we
	// downgrade to WARN rather than fail the whole write — the
	// operator's call to action in that case is to use a durable
	// volume, not to retry the commit.
	parent := filepath.Dir(path)
	if err := fsyncDir(parent); err != nil {
		// plan.md §362: keep the directory path out of INFO+ logs —
		// the parent of config.json is effectively the secret path
		// itself. WARN carries only the underlying error kind; the
		// full path is DEBUG-only so operators can still correlate
		// when they turn DEBUG on.
		slog.Warn("config.WriteAtomic: dir fsync failed — rename may not survive power loss",
			"error", err,
		)
		slog.Debug("config.WriteAtomic dir fsync failure path", "dir", parent)
	}
	success = true
	return nil
}

// fsyncDir opens dir O_RDONLY and calls Sync. Errors intentionally do
// NOT embed the directory path — callers that need the path for
// debugging emit it at DEBUG level alongside the WARN (see the
// WriteAtomic call site) to stay compliant with plan.md §362.
//
// ScrubPath is applied BEFORE %w wrapping because os.Open returns
// *os.PathError and fsync on an opened file returns a plain errno
// (no path leak there) — both paths through the function therefore
// terminate in a redacted chain.
func fsyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open config parent dir: %w", ScrubPath(err))
	}
	defer func() { _ = d.Close() }()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("fsync config parent dir: %w", ScrubPath(err))
	}
	return nil
}

// tmpPathFor returns the per-PID tmp filename paired with path.
// Split into a function so tests and SweepStale can reason about the
// naming convention without re-encoding string concatenation.
func tmpPathFor(path string) string {
	return path + tmpSuffixPrefix + strconv.Itoa(os.Getpid())
}

// SweepStale removes tmp files next to configPath that belong to
// processes no longer running. It is intended to run once at boot
// (inside BuildApp) so that O_EXCL writes later in the session do not
// fail on a PID-collision with a crashed predecessor.
//
// Matching rule: files in filepath.Dir(configPath) whose name is
// <basename>.tmp.<suffix> where <basename> == filepath.Base(configPath).
// Three sub-cases:
//
//   - <suffix> is a valid integer AND the PID is still alive       → keep.
//   - <suffix> is a valid integer AND the PID is dead or ours-alive → see
//     below. "Ours" (os.Getpid()) is always kept — a live writer in
//     this same process may have an open tmp.
//   - <suffix> is not a valid integer                               → remove
//     (garbage tmp file that does not match the PID convention).
//
// Returns the first error encountered while listing the directory; per-
// file remove errors are logged via slog.Warn but do not halt the sweep
// (a sweep failure MUST NOT block boot — worst case is a leftover file
// that the next sweep catches).
func SweepStale(configPath string) error {
	dir := filepath.Dir(configPath)
	basename := filepath.Base(configPath)
	prefix := basename + tmpSuffixPrefix
	selfPID := os.Getpid()

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("config.SweepStale: read config dir: %w", ScrubPath(err))
	}

	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		suffix := strings.TrimPrefix(name, prefix)
		// Refuse to treat sub-directories as sweep targets — paranoid
		// guard against future layout changes.
		if entry.IsDir() {
			continue
		}

		full := filepath.Join(dir, name)

		pid, err := strconv.Atoi(suffix)
		if err != nil {
			// plan.md §362 keeps config.json paths out of INFO+; emit
			// the full path at DEBUG only and log a path-less INFO
			// summary operators can grep.
			slog.Info("config.SweepStale: removing tmp file with non-numeric suffix")
			slog.Debug("config.SweepStale tmp candidate", "path", full)
			if rmErr := os.Remove(full); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
				// plan.md §362: os.Remove returns *os.PathError with
				// the full candidate path embedded in Error(); scrub
				// before WARN to keep config directory out of INFO+.
				slog.Warn("config.SweepStale: remove failed", "error", ScrubPath(rmErr))
			}
			continue
		}

		if pid == selfPID {
			// Live writer in this process — never sweep.
			continue
		}
		if processAlive(pid) {
			continue
		}

		slog.Info("config.SweepStale: removing stale tmp file", "pid", pid)
		slog.Debug("config.SweepStale stale tmp path", "path", full, "pid", pid)
		if rmErr := os.Remove(full); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			// plan.md §362: scrub *os.PathError path before WARN.
			slog.Warn("config.SweepStale: remove failed", "pid", pid, "error", ScrubPath(rmErr))
		}
	}
	return nil
}

// processAlive reports whether a PID is currently a live process on
// this host. Uses kill(pid, 0): ESRCH means the PID is free; EPERM
// means the process exists but we can't signal it (still "alive" for
// sweep purposes). Any other error is conservatively treated as alive
// so we never delete an unclear tmp file.
//
// Non-positive PIDs are treated as dead (kill(0, 0) / kill(-1, 0) have
// special semantics on POSIX that we don't want to accidentally invoke).
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	if errors.Is(err, syscall.EPERM) {
		return true
	}
	if errors.Is(err, syscall.ESRCH) {
		return false
	}
	// Unknown error — assume alive so we do not delete an ambiguous
	// tmp.  slog.Debug keeps the audit trail without spamming INFO.
	slog.Debug("config.processAlive: unexpected kill error, treating PID as alive",
		"pid", pid,
		"error", err,
	)
	return true
}
