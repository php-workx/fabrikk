package llmcli

import (
	"fmt"
	"sort"
	"sync"

	"github.com/php-workx/fabrikk/llmclient"
)

// backendFactory describes a registered CLI backend. Each backend
// implementation registers itself via [registerBackendFactory] from an init()
// function in its own disjoint *_registry.go file.
//
// Backend implementation tickets must NOT edit registry.go or select.go; they
// add only their own *_registry.go file.
type backendFactory struct {
	// Name is the stable backend identifier, e.g. "claude", "codex-exec",
	// "codex-appserver", "opencode-run", "opencode-serve", "omp", "omp-rpc".
	Name string

	// Binary is the CLI binary name, e.g. "claude", "codex". Multiple factories
	// may share the same Binary (e.g. codex-exec and codex-appserver both use
	// "codex"). [DetectAvailable] finds a CliInfo by Binary; [SelectBackend]
	// then matches it against registered factories.
	Binary string

	// Preference is the backend family this factory belongs to. It controls the
	// default priority ordering: lower value = higher priority. Ties are broken
	// by registration order (earlier = higher priority) thanks to the stable
	// sort in [registeredBackendFactories].
	Preference BackendPreference

	// Capabilities describes the static (pre-probe) capabilities of this
	// backend. [filterByStaticRequirements] uses these to discard backends that
	// cannot satisfy the caller's [Requirements] without spawning any process.
	Capabilities llmclient.Capabilities

	// New constructs a concrete backend for the given detected CLI. Called by
	// [SelectBackend], [SelectBackendByName], and [filterByProbe]. Callers must
	// close the returned backend when done.
	New func(CliInfo) llmclient.Backend
}

var (
	factoryMu sync.RWMutex
	factories []backendFactory // ordered by registration time
)

// registerBackendFactory adds f to the global registry. It is intended to be
// called from init() functions in per-backend *_registry.go files. Panics if a
// factory with the same Name is already registered to catch copy-paste errors
// at startup.
func registerBackendFactory(f backendFactory) {
	factoryMu.Lock()
	defer factoryMu.Unlock()
	for i := range factories {
		if factories[i].Name == f.Name {
			panic(fmt.Sprintf("llmcli: duplicate backend factory name %q", f.Name))
		}
	}
	factories = append(factories, f)
}

// registeredBackendFactories returns a stable, priority-sorted copy of the
// registered factories. The sort is by Preference (ascending), with ties
// broken by registration order (earlier = higher priority) because
// sort.SliceStable preserves the original relative order for equal keys.
func registeredBackendFactories() []backendFactory {
	factoryMu.RLock()
	cp := make([]backendFactory, len(factories))
	copy(cp, factories)
	factoryMu.RUnlock()

	sort.SliceStable(cp, func(i, j int) bool {
		return cp[i].Preference < cp[j].Preference
	})
	return cp
}

// factoryByName returns the factory registered under name, or (zero, false) if
// no such factory exists.
func factoryByName(name string) (backendFactory, bool) {
	factoryMu.RLock()
	defer factoryMu.RUnlock()
	for i := range factories {
		if factories[i].Name == name {
			return factories[i], true
		}
	}
	return backendFactory{}, false
}

// resetBackendFactoriesForTest atomically replaces the registry with the
// provided factories for the duration of a test. It returns a restore function
// that callers must invoke (typically via defer) to put the original registry
// back. The replacement slice is shallow-copied so the caller's slice is not
// mutated.
//
// Example:
//
//	restore := resetBackendFactoriesForTest(fakeA, fakeB)
//	defer restore()
func resetBackendFactoriesForTest(facs ...backendFactory) func() {
	factoryMu.Lock()
	saved := factories
	replacement := make([]backendFactory, len(facs))
	copy(replacement, facs)
	factories = replacement
	factoryMu.Unlock()

	return func() {
		factoryMu.Lock()
		factories = saved
		factoryMu.Unlock()
	}
}
