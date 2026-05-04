package plugin

import (
	"fmt"
	"sort"
	"sync"
)

// Binding couples a plugin ID to its compile-time factory. Entries
// live in each plugin's own package and are added to the global
// registry via Register, mirroring internal/store/dialect.go.
type Binding struct {
	// ID is the plugin identifier, matching the Plugin.ID() returned
	// by the factory's output. Two bindings with the same ID is a
	// boot-time fatal (see Register).
	ID string

	// Factory constructs a fresh Plugin on demand. BuildApp calls it
	// exactly once per process, AFTER confirming the plugin is
	// enabled via config.Plugins.Enabled(id). Disabled plugins have
	// their Factory NEVER invoked — this is the invariant that
	// prevents disabled plugins from holding resources or side-
	// effecting at boot.
	Factory func() Plugin
}

var (
	registryMu sync.RWMutex
	// registry is keyed by ID for O(1) duplicate detection. Iteration
	// order is stabilised by Registry() sorting before returning.
	registry = map[string]Binding{}
)

// Register adds binding to the global plugin registry. It is intended
// to be called from each concrete plugin package's init() so that a
// blank import of the plugin's package is sufficient to activate it
// at compile time.
//
// Panics:
//   - ID empty — a silent "plugin" is worse than a loud bug.
//   - Factory nil — same reasoning.
//   - Duplicate ID — two plugins cannot share a config key; fail loudly
//     so the operator doesn't get a silently-overridden plugin.
//
// 002 ships zero concrete plugins, so 002 never triggers the panic
// paths in production. The panics exist so 003/004/005 misconfigurations
// surface at init rather than at first request.
func Register(b Binding) {
	if b.ID == "" {
		panic("plugin.Register: binding ID is empty")
	}
	if b.Factory == nil {
		panic(fmt.Sprintf("plugin.Register: binding %q has nil Factory", b.ID))
	}

	registryMu.Lock()
	defer registryMu.Unlock()
	if _, dup := registry[b.ID]; dup {
		panic(fmt.Sprintf("plugin.Register: duplicate plugin ID %q", b.ID))
	}
	registry[b.ID] = b
}

// Registry returns a snapshot of registered bindings, sorted
// ascending by ID. Safe for concurrent use; the returned slice is a
// fresh copy — callers may mutate it without affecting the global
// registry.
//
// BuildApp calls this exactly once at boot to enumerate plugins.
// Tests MAY call it multiple times; stability across calls is an
// invariant (see registry_test.go).
func Registry() []Binding {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]Binding, 0, len(registry))
	for _, b := range registry {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// resetRegistryForTests clears the global registry. Exported ONLY
// through the _test.go compilation unit (hence the naming convention);
// production code never calls this.
//
//nolint:unused // referenced exclusively from registry_test.go
func resetRegistryForTests() {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = map[string]Binding{}
}
