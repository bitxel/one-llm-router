package plugin

import (
	"sync"
	"testing"
)

// fakePlugin satisfies the Plugin interface for registry tests.
type fakePlugin struct{ id string }

func (f *fakePlugin) ID() string { return f.id }

// newBinding is a test helper — builds a Binding whose Factory returns
// a fresh fakePlugin bearing the given ID.
func newBinding(id string) Binding {
	return Binding{
		ID: id,
		Factory: func() Plugin {
			return &fakePlugin{id: id}
		},
	}
}

func TestRegistry_EmptyIsValid(t *testing.T) {
	// Not Parallel: global state.
	resetRegistryForTests()
	got := Registry()
	if len(got) != 0 {
		t.Errorf("Registry() = %v, want empty slice", got)
	}
	// A second call still returns empty (and a fresh slice).
	got2 := Registry()
	if len(got2) != 0 {
		t.Errorf("Second Registry() call = %v, want empty", got2)
	}
}

func TestRegistry_SingleRegistration(t *testing.T) {
	// Not Parallel: global state.
	resetRegistryForTests()
	Register(newBinding("admin_auth"))

	got := Registry()
	if len(got) != 1 {
		t.Fatalf("Registry() len = %d, want 1", len(got))
	}
	if got[0].ID != "admin_auth" {
		t.Errorf("Registry()[0].ID = %q, want admin_auth", got[0].ID)
	}
	// Factory is callable and returns a matching Plugin.
	p := got[0].Factory()
	if p.ID() != "admin_auth" {
		t.Errorf("Factory().ID() = %q, want admin_auth", p.ID())
	}
}

func TestRegistry_StableOrder_LexicalByID(t *testing.T) {
	// Not Parallel: global state.
	resetRegistryForTests()
	// Register in deliberately-scrambled order.
	Register(newBinding("prometheus"))
	Register(newBinding("admin_auth"))
	Register(newBinding("client_keys"))

	got := Registry()
	want := []string{"admin_auth", "client_keys", "prometheus"}
	if len(got) != len(want) {
		t.Fatalf("Registry() len = %d, want %d", len(got), len(want))
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Errorf("Registry()[%d].ID = %q, want %q", i, got[i].ID, id)
		}
	}

	// Stability: a second call returns the same order, on a fresh slice.
	got2 := Registry()
	for i := range got {
		if got[i].ID != got2[i].ID {
			t.Errorf("unstable order across calls at index %d: %q vs %q", i, got[i].ID, got2[i].ID)
		}
	}
	if &got[0] == &got2[0] {
		t.Error("Registry() returned the same underlying slice on two calls — must be a snapshot")
	}
}

func TestRegistry_SnapshotIsIndependent(t *testing.T) {
	// Not Parallel: global state.
	resetRegistryForTests()
	Register(newBinding("one"))
	Register(newBinding("two"))

	got := Registry()
	// Mutating the returned slice must not leak into the global.
	got[0] = Binding{ID: "tampered", Factory: func() Plugin { return nil }}

	fresh := Registry()
	if fresh[0].ID == "tampered" {
		t.Error("Registry() did not return an independent snapshot")
	}
}

func TestRegistry_DuplicateIDPanics(t *testing.T) {
	// Not Parallel: global state.
	resetRegistryForTests()
	Register(newBinding("admin_auth"))

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on duplicate ID, got none")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("panic payload type = %T, want string", r)
		}
		// Message must mention the offending ID so operators can diagnose.
		if !contains(msg, "admin_auth") {
			t.Errorf("panic message %q missing offending ID", msg)
		}
	}()
	Register(newBinding("admin_auth"))
}

func TestRegistry_EmptyIDPanics(t *testing.T) {
	// Not Parallel: global state.
	resetRegistryForTests()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on empty ID, got none")
		}
	}()
	Register(Binding{ID: "", Factory: func() Plugin { return nil }})
}

func TestRegistry_NilFactoryPanics(t *testing.T) {
	// Not Parallel: global state.
	resetRegistryForTests()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on nil Factory, got none")
		}
	}()
	Register(Binding{ID: "bogus", Factory: nil})
}

func TestRegistry_ConcurrentRegisterAndRead(t *testing.T) {
	// Not Parallel: global state.
	resetRegistryForTests()

	const writers = 20
	var wg sync.WaitGroup

	// Register distinct IDs concurrently — no panics, no races under -race.
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			Register(Binding{
				ID:      "plugin_" + string(rune('a'+i)),
				Factory: func() Plugin { return &fakePlugin{id: "x"} },
			})
		}(i)
	}

	// Readers interleaved with writers — Registry() must not panic or
	// return inconsistent data even during registration.
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = Registry()
		}()
	}

	wg.Wait()

	got := Registry()
	if len(got) != writers {
		t.Errorf("Registry() len = %d, want %d", len(got), writers)
	}
	// Verify sorted-by-ID invariant under concurrent registration.
	for i := 1; i < len(got); i++ {
		if got[i-1].ID >= got[i].ID {
			t.Errorf("not sorted at index %d: %q >= %q", i, got[i-1].ID, got[i].ID)
		}
	}
}

// contains is a tiny helper local to this test file.
func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
