package setup

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestStateReader_IsDone_FileMissing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	r := NewStateReader()

	done, err := r.IsDone(filepath.Join(dir, "nope.json"))
	if err != nil {
		t.Errorf("IsDone(missing) err = %v, want nil", err)
	}
	if done {
		t.Error("IsDone(missing) = true, want false")
	}
}

func TestStateReader_IsDone_FileExists(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	done, err := NewStateReader().IsDone(path)
	if err != nil {
		t.Errorf("IsDone(exists) err = %v, want nil", err)
	}
	if !done {
		t.Error("IsDone(exists) = false, want true")
	}
}

func TestStateReader_IsDone_EmptyFileStillCountsAsDone(t *testing.T) {
	// A zero-byte config.json is corrupt *to the loader*, but to the
	// gate it still means "the operator finished the wizard, so route
	// to /admin not /setup". config.Load then surfaces the parse error
	// at startup — that's the correct split of responsibility.
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	done, err := NewStateReader().IsDone(path)
	if err != nil {
		t.Errorf("IsDone(empty) err = %v, want nil", err)
	}
	if !done {
		t.Error("IsDone(empty) = false, want true — presence, not content, decides")
	}
}

func TestStateReader_IsDone_PathIsDirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Create a directory at the would-be config.json path.
	badPath := filepath.Join(dir, "config.json")
	if err := os.Mkdir(badPath, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	done, err := NewStateReader().IsDone(badPath)
	if err == nil {
		t.Fatal("IsDone(dir) err = nil, want non-nil")
	}
	if done {
		t.Error("IsDone(dir) = true, want false — directory must fail-closed")
	}
	if !strings.Contains(err.Error(), "directory") {
		t.Errorf("error %q missing 'directory' diagnostic", err.Error())
	}
}

func TestStateReader_IsDone_Symlink(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	realPath := filepath.Join(dir, "real.json")
	if err := os.WriteFile(realPath, []byte("{}"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	linkPath := filepath.Join(dir, "config.json")
	if err := os.Symlink(realPath, linkPath); err != nil {
		t.Skipf("symlink unsupported on this platform: %v", err)
	}

	done, err := NewStateReader().IsDone(linkPath)
	if err != nil {
		t.Errorf("IsDone(symlink) err = %v, want nil", err)
	}
	if !done {
		t.Error("IsDone(symlink) = false, want true — symlinks to regular files count as done")
	}
}

func TestStateReader_IsDone_DanglingSymlink(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	linkPath := filepath.Join(dir, "config.json")
	if err := os.Symlink(filepath.Join(dir, "nonexistent"), linkPath); err != nil {
		t.Skipf("symlink unsupported on this platform: %v", err)
	}

	done, err := NewStateReader().IsDone(linkPath)
	// A dangling symlink is ENOENT to os.Stat — treat as setup-pending,
	// not as an error. The operator-visible failure is that subsequent
	// writes will succeed (creating the target), leaving the symlink
	// resolved.
	if err != nil {
		t.Errorf("IsDone(dangling symlink) err = %v, want nil (ENOENT path)", err)
	}
	if done {
		t.Error("IsDone(dangling symlink) = true, want false")
	}
}

func TestStateReader_IsDone_EmptyPath(t *testing.T) {
	t.Parallel()
	done, err := NewStateReader().IsDone("")
	if err == nil {
		t.Fatal("IsDone(\"\") err = nil, want non-nil — empty path is a programmer bug")
	}
	if done {
		t.Error("IsDone(\"\") = true, want false")
	}
}

func TestStateReader_IsDone_PermissionDenied(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission semantics not applicable on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses permission checks")
	}
	t.Parallel()

	dir := t.TempDir()
	sub := filepath.Join(dir, "locked")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	target := filepath.Join(sub, "config.json")
	if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// Strip all perms on the parent dir so stat fails with EACCES.
	if err := os.Chmod(sub, 0o000); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(sub, 0o700) })

	done, err := NewStateReader().IsDone(target)
	if err == nil {
		t.Fatal("IsDone(no-perm) err = nil, want non-nil")
	}
	if done {
		t.Error("IsDone(no-perm) = true, want false")
	}
	if !strings.Contains(err.Error(), "stat") {
		t.Errorf("error %q missing 'stat' diagnostic", err.Error())
	}
	// Preserve wrapping: underlying error should still be reachable.
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) {
		t.Errorf("expected wrapped *os.PathError, got %T", err)
	}
	// plan.md §362 compliance — the real config path must not appear
	// in the error message when this error surfaces at WARN+ (the gate
	// logs it verbatim). config.ScrubPath rewrites Path to a
	// placeholder before we wrap, so the absolute path below should be
	// nowhere in the chain's string form.
	if strings.Contains(err.Error(), target) {
		t.Errorf("error %q leaks config path %q — plan.md §362 violation", err.Error(), target)
	}
}

func TestStateReader_IsDone_DirectoryDoesNotLeakPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bogus := filepath.Join(dir, "config.json")
	if err := os.Mkdir(bogus, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	_, err := NewStateReader().IsDone(bogus)
	if err == nil {
		t.Fatal("IsDone(dir) err = nil, want non-nil")
	}
	if strings.Contains(err.Error(), bogus) {
		t.Errorf("error %q leaks config path %q — plan.md §362 violation", err.Error(), bogus)
	}
}

func TestStateReader_IsDone_ConcurrentSafe(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	r := NewStateReader()
	const readers = 50
	var wg sync.WaitGroup
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			done, err := r.IsDone(path)
			if err != nil || !done {
				t.Errorf("concurrent IsDone = (%v, %v)", done, err)
			}
		}()
	}
	wg.Wait()
}

func TestStateReader_ZeroValueUsable(t *testing.T) {
	// Documented contract: the zero value is production-usable, no
	// constructor required. Matches how store.Dialect and similar
	// stateless services are exposed.
	t.Parallel()
	var r StateReader
	dir := t.TempDir()
	done, err := r.IsDone(filepath.Join(dir, "missing.json"))
	if err != nil || done {
		t.Errorf("zero-value reader = (%v, %v), want (false, nil)", done, err)
	}
}
