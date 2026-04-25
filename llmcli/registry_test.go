package llmcli

import (
	"context"
	"testing"

	"github.com/php-workx/fabrikk/llmclient"
)

// fakeBackend is a minimal llmclient.Backend implementation used in tests.
// It reports the name and availability passed to it at construction time.
type fakeBackend struct {
	name      string
	available bool
}

func (f *fakeBackend) Name() string    { return f.name }
func (f *fakeBackend) Available() bool { return f.available }
func (f *fakeBackend) Close() error    { return nil }
func (f *fakeBackend) Stream(_ context.Context, _ *llmclient.Context, _ ...llmclient.Option) (<-chan llmclient.Event, error) {
	ch := make(chan llmclient.Event)
	close(ch)
	return ch, nil
}

// makeFactory returns a backendFactory that always creates a fakeBackend with
// the given availability.
func makeFactory(name, binary string, pref BackendPreference, caps llmclient.Capabilities, available bool) backendFactory {
	return backendFactory{
		Name:         name,
		Binary:       binary,
		Preference:   pref,
		Capabilities: caps,
		New: func(info CliInfo) llmclient.Backend {
			return &fakeBackend{name: name, available: available}
		},
	}
}

// TestRegistry_ReturnsStablePriorityOrder verifies that
// registeredBackendFactories returns factories sorted by Preference (ascending)
// and that the order is stable across repeated calls.
func TestRegistry_ReturnsStablePriorityOrder(t *testing.T) {
	// Register in reverse priority order to prove the sort works.
	fOmp := makeFactory("omp", "omp", PreferOmp, llmclient.Capabilities{}, true)
	fCodex := makeFactory("codex-exec", "codex", PreferCodex, llmclient.Capabilities{}, true)
	fClaude := makeFactory("claude", "claude", PreferClaude, llmclient.Capabilities{}, true)

	restore := resetBackendFactoriesForTest(fOmp, fCodex, fClaude)
	defer restore()

	got := registeredBackendFactories()

	if len(got) != 3 {
		t.Fatalf("registeredBackendFactories: got %d entries, want 3", len(got))
	}
	wantNames := []string{"claude", "codex-exec", "omp"}
	for i, want := range wantNames {
		if got[i].Name != want {
			t.Errorf("position %d: got %q, want %q", i, got[i].Name, want)
		}
	}

	// Second call must return identical ordering.
	got2 := registeredBackendFactories()
	for i := range got {
		if got[i].Name != got2[i].Name {
			t.Errorf("order unstable at position %d: first=%q second=%q", i, got[i].Name, got2[i].Name)
		}
	}
}

// TestRegistry_ResetForTest verifies that resetBackendFactoriesForTest
// replaces the registry for the duration of the test and the restore function
// returns the original contents.
func TestRegistry_ResetForTest(t *testing.T) {
	// Record the original registry state.
	original := registeredBackendFactories()

	fA := makeFactory("fake-a", "fake", PreferClaude, llmclient.Capabilities{}, true)
	fB := makeFactory("fake-b", "fake", PreferCodex, llmclient.Capabilities{}, true)

	restore := resetBackendFactoriesForTest(fA, fB)

	mid := registeredBackendFactories()
	if len(mid) != 2 {
		t.Fatalf("mid-test registry length: got %d, want 2", len(mid))
	}
	if mid[0].Name != "fake-a" || mid[1].Name != "fake-b" {
		t.Errorf("mid-test registry names: got %q %q, want fake-a fake-b", mid[0].Name, mid[1].Name)
	}

	restore()

	after := registeredBackendFactories()
	if len(after) != len(original) {
		t.Errorf("after restore registry length: got %d, want %d", len(after), len(original))
	}
}

// TestRegistry_RejectsDuplicateName verifies that registerBackendFactory
// panics when a factory with the same name is registered twice.
func TestRegistry_RejectsDuplicateName(t *testing.T) {
	fA := makeFactory("dup", "dup", PreferClaude, llmclient.Capabilities{}, true)
	restore := resetBackendFactoriesForTest(fA)
	defer restore()

	defer func() {
		r := recover()
		if r == nil {
			t.Error("registerBackendFactory: expected panic for duplicate name, got none")
		}
	}()
	// Attempt to register a second factory with the same name.
	registerBackendFactory(makeFactory("dup", "other", PreferCodex, llmclient.Capabilities{}, true))
}

// TestRegistry_RegisteredNamesComplete verifies that the complete set of
// expected backend factories is registered by their init() functions. The
// expected set is the canonical source of truth: exactly these seven names must
// be registered — no more, no fewer.
func TestRegistry_RegisteredNamesComplete(t *testing.T) {
	want := map[string]bool{
		"claude":          true,
		"codex-exec":      true,
		"codex-appserver": true,
		"opencode-run":    true,
		"opencode-serve":  true,
		"omp":             true,
		"omp-rpc":         true,
	}

	got := registeredBackendFactories()

	gotNames := make(map[string]bool, len(got))
	for _, f := range got {
		gotNames[f.Name] = true
	}

	// Every expected name must be present.
	for name := range want {
		if !gotNames[name] {
			t.Errorf("expected backend %q is not registered; add a *_registry.go file for it", name)
		}
	}

	// No unexpected names must slip in.
	for name := range gotNames {
		if !want[name] {
			t.Errorf("unexpected backend %q is registered; update the expected set in this test", name)
		}
	}

	// The total count must match to catch silent duplicates or omissions.
	if len(got) != len(want) {
		t.Errorf("registered backend count = %d, want %d", len(got), len(want))
	}
}

// TestRegistry_RegistrationOrderBreaksTies verifies that when two factories
// share the same Preference value, the one registered first has higher priority
// in the sorted output.
func TestRegistry_RegistrationOrderBreaksTies(t *testing.T) {
	// Both factories have PreferCodex; first registered should win.
	fFirst := makeFactory("codex-appserver", "codex", PreferCodex, llmclient.Capabilities{}, true)
	fSecond := makeFactory("codex-exec", "codex", PreferCodex, llmclient.Capabilities{}, true)

	restore := resetBackendFactoriesForTest(fFirst, fSecond)
	defer restore()

	got := registeredBackendFactories()
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2", len(got))
	}
	if got[0].Name != "codex-appserver" {
		t.Errorf("position 0: got %q, want %q", got[0].Name, "codex-appserver")
	}
	if got[1].Name != "codex-exec" {
		t.Errorf("position 1: got %q, want %q", got[1].Name, "codex-exec")
	}
}
