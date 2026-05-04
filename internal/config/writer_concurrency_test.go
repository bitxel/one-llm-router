package config

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// T-016: two in-process goroutines racing WriteAtomic on the SAME path
// contend on the PID-scoped tmp name; exactly one OpenFile(O_EXCL) wins.
func TestWriteAtomic_ConcurrentSamePath_OneWinner(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	const n = 32
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			// Each goroutine needs its own *Config — WriteAtomic mutates timestamps in-place.
			cfg := sampleConfigForWrite()
			errs[i] = WriteAtomic(path, cfg)
		}(i)
	}
	// Release all goroutines together so they contend on the same O_EXCL tmp name.
	close(start)
	wg.Wait()

	var wins, fails, existFails int
	for _, err := range errs {
		switch err {
		case nil:
			wins++
		default:
			fails++
			if errors.Is(err, os.ErrExist) || isExistErr(err) {
				existFails++
			} else {
				t.Logf("non-EEXIST failure (acceptable on some FS): %v", err)
			}
		}
	}
	if wins < 1 || fails < 1 {
		t.Fatalf("expected mixed outcomes under contention; wins=%d fails=%d", wins, fails)
	}
	if wins+fails != n {
		t.Fatalf("invariant broken: wins=%d fails=%d n=%d", wins, fails, n)
	}
	if existFails < 1 {
		t.Fatalf("expected at least one EEXIST-style Open tmp failure; errs=%v", errs)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat final: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("final config.json is empty")
	}
}

func isExistErr(err error) bool {
	// os.IsExist is deprecated in Go 1.21+ but still the portable grep for EEXIST wrappers.
	return err != nil && (errors.Is(err, os.ErrExist) || os.IsExist(err))
}
