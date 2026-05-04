package config

import (
	"sync"
	"sync/atomic"
	"testing"
)

// resetLiveForTests returns the slot to its pre-BuildApp state. Call
// from every test in this file that writes to the global to avoid
// leaking state across test cases.
func resetLiveForTests() {
	Publisher.Store(nil)
}

func TestLive_LoadBeforeStore_ReturnsNil(t *testing.T) {
	// Not Parallel: writes to the package-global slot.
	resetLiveForTests()
	if got := Reader.Load(); got != nil {
		t.Errorf("Load() = %+v, want nil before any Store", got)
	}
}

func TestLive_RoundTrip(t *testing.T) {
	// Not Parallel.
	resetLiveForTests()
	cfg := &Config{Version: 1, Runtime: DefaultRuntimeConfig()}
	Publisher.Store(cfg)
	got := Reader.Load()
	if got == nil {
		t.Fatal("Load() = nil after Store, want non-nil")
	}
	if got != cfg {
		t.Errorf("Load() returned a different pointer (%p) than was stored (%p)", got, cfg)
	}
	if got.Runtime.LogLevel != "info" {
		t.Errorf("Load().Runtime.LogLevel = %q, want info", got.Runtime.LogLevel)
	}
}

func TestLive_SecondStoreReplacesFirst(t *testing.T) {
	// Not Parallel.
	resetLiveForTests()
	a := &Config{Version: 1, Runtime: RuntimeConfig{LogLevel: "info"}}
	b := &Config{Version: 1, Runtime: RuntimeConfig{LogLevel: "debug"}}
	Publisher.Store(a)
	Publisher.Store(b)
	got := Reader.Load()
	if got != b {
		t.Errorf("Load() = %p, want %p (second store)", got, b)
	}
	if got.Runtime.LogLevel != "debug" {
		t.Errorf("Runtime.LogLevel = %q, want debug", got.Runtime.LogLevel)
	}
}

func TestLive_StoreNilAllowed(t *testing.T) {
	// Not Parallel.
	resetLiveForTests()
	Publisher.Store(&Config{Version: 1})
	Publisher.Store(nil)
	if got := Reader.Load(); got != nil {
		t.Errorf("Load() after Store(nil) = %+v, want nil", got)
	}
}

// TestLive_NoRaceUnderConcurrency matches the tasks.md T-017 verify
// profile: 100 readers + 10 writers, run under `go test -race`.
// Each reader observes a complete *Config (log_level either old or
// new, never an interleaved partial state) because publishing swaps
// an entire pointer, never a field.
func TestLive_NoRaceUnderConcurrency(t *testing.T) {
	// Not Parallel: mutates the package-global slot.
	resetLiveForTests()

	const (
		readers = 100
		writers = 10
		itersR  = 500
		itersW  = 200
	)

	var (
		wg        sync.WaitGroup
		tornReads atomic.Int64
	)

	// Writers alternate between a pair of "known consistent" configs so
	// readers can detect torn state (a field from A mixed with a field
	// from B would be a bug).
	shapeA := &Config{
		Version: 1,
		Runtime: RuntimeConfig{LogLevel: "info", LogRetentionDays: 30, LogClientRequestBody: false, LogUpstreamRequestBody: false, LogUpstreamResponseBody: false},
	}
	shapeB := &Config{
		Version: 1,
		Runtime: RuntimeConfig{LogLevel: "debug", LogRetentionDays: 7, LogClientRequestBody: true, LogUpstreamRequestBody: true, LogUpstreamResponseBody: true},
	}
	// Pre-seed with shapeA so a reader that wins the scheduler race
	// before any writer goroutine runs still observes a known-valid
	// pointer (not "unknown"). Readers below tolerate either shape.
	Publisher.Store(shapeA)

	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < itersW; j++ {
				if j%2 == 0 {
					Publisher.Store(shapeA)
				} else {
					Publisher.Store(shapeB)
				}
			}
		}(i)
	}

	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < itersR; j++ {
				snap := Reader.Load()
				if snap == nil {
					t.Errorf("[reader %d] Load() = nil after initial Store", i)
					return
				}
				// Shape-A and Shape-B are internally consistent; any
				// mismatch means we read a torn snapshot.
				switch snap {
				case shapeA:
					if snap.Runtime.LogLevel != "info" ||
						snap.Runtime.LogRetentionDays != 30 ||
						snap.Runtime.LogClientRequestBody != false ||
						snap.Runtime.LogUpstreamRequestBody != false ||
						snap.Runtime.LogUpstreamResponseBody != false {
						tornReads.Add(1)
					}
				case shapeB:
					if snap.Runtime.LogLevel != "debug" ||
						snap.Runtime.LogRetentionDays != 7 ||
						snap.Runtime.LogClientRequestBody != true ||
						snap.Runtime.LogUpstreamRequestBody != true ||
						snap.Runtime.LogUpstreamResponseBody != true {
						tornReads.Add(1)
					}
				default:
					// Any other pointer came from a previous test and
					// is unexpected.
					t.Errorf("[reader %d] Load() returned unknown pointer %p", i, snap)
					return
				}
			}
		}(i)
	}

	wg.Wait()

	if n := tornReads.Load(); n != 0 {
		t.Errorf("observed %d torn reads — atomic.Pointer is not being used correctly", n)
	}
}

// TestLive_ReaderPublisherShareSlot sanity-checks that the two
// singletons are genuinely paired — a refactor that accidentally
// allocates two separate atomic.Pointer instances would be caught
// here.
func TestLive_ReaderPublisherShareSlot(t *testing.T) {
	// Not Parallel.
	resetLiveForTests()
	cfg := &Config{Version: 1}
	Publisher.Store(cfg)
	if got := Reader.Load(); got != cfg {
		t.Fatalf("Reader.Load() = %p, want %p — singletons are not paired", got, cfg)
	}
}
