// Package setup hosts the 002 setup-wizard domain: the read-only
// setup-completion marker (this file), the request gate middleware
// (gate.go), the brownfield auto-materializer (brownfield.go), and
// the DSN probe (probe.go). All four collaborate to encode the
// invariant spelled out in specs/002-.../data-model.md:
//
//	"config.json presence == setup done"
//
// Everything in this package is synchronous, stateless, and safe to
// call from boot-time goroutines and per-request handlers alike.
package setup

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/user/one-llm-router/internal/config"
)

// StateReader reads the on-disk setup-completion marker. It holds no
// configuration of its own on purpose — callers pass the path on each
// call so the same reader instance can serve both production
// (ROUTER_CONFIG_PATH) and tests (temp directories) without
// re-initialisation.
//
// All methods are goroutine-safe; the type contains no mutable state.
type StateReader struct{}

// NewStateReader returns a zero-value reader. The constructor exists
// purely so callers can wire a mock through an interface during tests
// (see gate_test.go) without touching a global.
func NewStateReader() StateReader { return StateReader{} }

// IsDone reports whether `config.json` exists at path. It intentionally
// does NOT parse the file — the caller's config.Load pipeline owns
// validation. IsDone's only job is to answer the question the setup
// gate asks on every request: "is this server in setup-pending mode?"
//
// Semantics (matching specs/002-.../data-model.md §Overview):
//
//   - path refers to an existing regular file     → (true,  nil)
//   - path refers to an existing symlink to file  → (true,  nil)   — os.Stat follows symlinks
//   - path refers to a directory                  → (false, error) — corruption; fail-closed
//   - path does not exist                         → (false, nil)   — setup-pending, the happy US-1 branch
//   - path exists but is unreadable (EACCES, …)   → (false, error) — surface to operator
//   - path is an empty string                     → (false, error) — programmer bug
//
// The empty-string rejection matters: os.Stat("") returns a driver-
// dependent error on different platforms; normalising here gives the
// gate a consistent signal.
func (StateReader) IsDone(path string) (bool, error) {
	if path == "" {
		return false, errors.New("setup.IsDone: path is empty")
	}

	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		// Wrap non-ENOENT errors — permission denied, I/O failure, stale
		// NFS handle, etc. The raw stat error embeds the absolute path
		// in its Error() output (*os.PathError.Path). plan.md §362
		// forbids that path appearing at INFO+ log sinks, and the gate
		// WARN-logs this error verbatim on stat failure, so we scrub
		// the leaf *os.PathError before wrapping. Operators trace
		// which config.json we meant via DEBUG logs or the CLI flag.
		return false, fmt.Errorf("setup.IsDone: stat: %w", config.ScrubPath(err))
	}

	// A directory at the marker path means something is very wrong —
	// possibly a volume mount gone sideways, possibly an operator error
	// (`mkdir config.json` instead of a write). Refusing to treat this
	// as "setup done" is the fail-closed posture: the gate will keep
	// redirecting to the wizard, and the operator sees the error in
	// `/api/admin/health`'s diagnostics rather than silently mis-routing
	// traffic. The path itself stays out of the message per §362 — the
	// operator already knows which path they configured.
	if info.IsDir() {
		return false, errors.New("setup.IsDone: config path is a directory, not a regular file")
	}

	return true, nil
}
