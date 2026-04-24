package llmcli

import (
	"context"
	"errors"
	"testing"

	"github.com/php-workx/fabrikk/llmclient"
)

// structuredCaps returns Capabilities that satisfy structured streaming,
// multi-turn, thinking, and usage requirements (a "full-featured" backend).
func structuredCaps(name string) llmclient.Capabilities {
	return llmclient.Capabilities{
		Backend:    name,
		Streaming:  llmclient.StreamingStructured,
		ToolEvents: true,
		MultiTurn:  true,
		Thinking:   true,
		Usage:      true,
	}
}

// textOnlyCaps returns Capabilities for a text-only backend (no structured
// streaming, no tool events, no thinking or usage reporting).
func textOnlyCaps(name string) llmclient.Capabilities {
	return llmclient.Capabilities{
		Backend:   name,
		Streaming: llmclient.StreamingBufferedOnly,
	}
}

// setupSelectTest registers the given factories and returns a CliInfo slice
// pretending those binaries are detected. The caller must call the returned
// restore function to reset the registry.
//
// detectOverride returns the provided CliInfo slice when used with
// filterByStaticRequirements so that tests don't need real binaries on PATH.
func setupSelectTest(t *testing.T, facs []backendFactory) func() {
	t.Helper()
	return resetBackendFactoriesForTest(facs...)
}

// TestSelectBackend_FiltersTextOnlyWhenStructuredRequired verifies that
// backends with text-only streaming capabilities are excluded when
// MinStreaming=StreamingStructured.
func TestSelectBackend_FiltersTextOnlyWhenStructuredRequired(t *testing.T) {
	fClaude := makeFactory("claude", "claude", PreferClaude, structuredCaps("claude"), true)
	fCodex := makeFactory("codex-exec", "codex", PreferCodex, textOnlyCaps("codex-exec"), true)

	restore := setupSelectTest(t, []backendFactory{fClaude, fCodex})
	defer restore()

	// Simulate both binaries being present.
	detected := []CliInfo{
		{Name: "claude", Binary: "claude", Path: "/usr/bin/claude"},
		{Name: "codex", Binary: "codex", Path: "/usr/bin/codex"},
	}

	req := Requirements{MinStreaming: llmclient.StreamingStructured}
	candidates := filterByStaticRequirements(detected, req)

	if len(candidates) != 1 {
		t.Fatalf("filterByStaticRequirements: got %d candidates, want 1", len(candidates))
	}
	if candidates[0].factory.Name != claudeBackendName {
		t.Errorf("surviving candidate: got %q, want %q", candidates[0].factory.Name, claudeBackendName)
	}
}

// TestSelectBackend_RespectsPreferredOrder verifies that PreferredOrder
// overrides the default priority ordering.
func TestSelectBackend_RespectsPreferredOrder(t *testing.T) {
	fClaude := makeFactory("claude", "claude", PreferClaude, structuredCaps("claude"), true)
	fCodex := makeFactory("codex-exec", "codex", PreferCodex, structuredCaps("codex-exec"), true)

	restore := setupSelectTest(t, []backendFactory{fClaude, fCodex})
	defer restore()

	detected := []CliInfo{
		{Name: "claude", Binary: "claude", Path: "/usr/bin/claude"},
		{Name: "codex", Binary: "codex", Path: "/usr/bin/codex"},
	}

	// With no preferred order, claude (PreferClaude=1) should win.
	candidates := filterByStaticRequirements(detected, Requirements{})
	b, err := pickByPreference(candidates, nil)
	if err != nil {
		t.Fatalf("pickByPreference(nil): unexpected error: %v", err)
	}
	if b.Name() != claudeBackendName {
		t.Errorf("default order: got %q, want %q", b.Name(), claudeBackendName)
	}

	// With PreferCodex first, codex should win despite lower default priority.
	b2, err := pickByPreference(candidates, []BackendPreference{PreferCodex, PreferClaude})
	if err != nil {
		t.Fatalf("pickByPreference([PreferCodex, PreferClaude]): unexpected error: %v", err)
	}
	if b2.Name() != "codex-exec" {
		t.Errorf("PreferCodex order: got %q, want %q", b2.Name(), "codex-exec")
	}
}

// TestSelectBackend_NeverDowngradesRequiredCapabilities verifies that a
// backend that does NOT satisfy NeedsToolEvents is never returned when that
// requirement is set, even if it is the only detected backend.
func TestSelectBackend_NeverDowngradesRequiredCapabilities(t *testing.T) {
	fTextOnly := makeFactory("codex-exec", "codex", PreferCodex, textOnlyCaps("codex-exec"), true)

	restore := setupSelectTest(t, []backendFactory{fTextOnly})
	defer restore()

	detected := []CliInfo{
		{Name: "codex", Binary: "codex", Path: "/usr/bin/codex"},
	}

	// NeedsToolEvents=true must exclude the text-only backend.
	req := Requirements{NeedsToolEvents: true}
	candidates := filterByStaticRequirements(detected, req)
	if len(candidates) != 0 {
		t.Errorf("NeedsToolEvents should filter text-only backend; got %d candidates", len(candidates))
	}

	// NeedsMultiTurn=true must also exclude it.
	req2 := Requirements{NeedsMultiTurn: true}
	candidates2 := filterByStaticRequirements(detected, req2)
	if len(candidates2) != 0 {
		t.Errorf("NeedsMultiTurn should filter text-only backend; got %d candidates", len(candidates2))
	}

	// NeedsThinking=true must also exclude it.
	req3 := Requirements{NeedsThinking: true}
	candidates3 := filterByStaticRequirements(detected, req3)
	if len(candidates3) != 0 {
		t.Errorf("NeedsThinking should filter text-only backend; got %d candidates", len(candidates3))
	}
}

// TestSelectBackend_ReturnsErrNoBackendAvailable verifies that SelectBackend
// returns ErrNoBackendAvailable when no registered backend can satisfy the
// requirements.
func TestSelectBackend_ReturnsErrNoBackendAvailable(t *testing.T) {
	// Empty registry: no factories registered.
	restore := resetBackendFactoriesForTest()
	defer restore()

	ctx := context.Background()
	_, err := SelectBackend(ctx, Requirements{})
	if !errors.Is(err, ErrNoBackendAvailable) {
		t.Errorf("SelectBackend with empty registry: got %v, want ErrNoBackendAvailable", err)
	}
}

// TestSelectBackendByName_ReturnsErrUnknownBackend verifies that
// SelectBackendByName returns a *BackendNotFoundError for an unregistered name.
func TestSelectBackendByName_ReturnsErrUnknownBackend(t *testing.T) {
	restore := resetBackendFactoriesForTest()
	defer restore()

	_, err := SelectBackendByName("nonexistent")
	if !errors.Is(err, ErrUnknownBackend) {
		t.Errorf("SelectBackendByName(nonexistent): got %v, want ErrUnknownBackend", err)
	}
	var bne *BackendNotFoundError
	if !errors.As(err, &bne) {
		t.Errorf("SelectBackendByName(nonexistent): error should be *BackendNotFoundError, got %T", err)
	}
}

// TestSelectBackend_StreamingFidelityLevels verifies the streaming fidelity
// ordering: BufferedOnly < TextChunk < StructuredUnknown < Structured.
func TestSelectBackend_StreamingFidelityLevels(t *testing.T) {
	cases := []struct {
		have      llmclient.StreamingFidelity
		need      llmclient.StreamingFidelity
		wantMatch bool
	}{
		{llmclient.StreamingStructured, llmclient.StreamingStructured, true},
		{llmclient.StreamingStructured, llmclient.StreamingTextChunk, true},
		{llmclient.StreamingStructured, llmclient.StreamingBufferedOnly, true},
		{llmclient.StreamingStructuredUnknown, llmclient.StreamingStructured, false},
		{llmclient.StreamingStructuredUnknown, llmclient.StreamingTextChunk, true},
		{llmclient.StreamingTextChunk, llmclient.StreamingStructured, false},
		{llmclient.StreamingTextChunk, llmclient.StreamingTextChunk, true},
		{llmclient.StreamingBufferedOnly, llmclient.StreamingTextChunk, false},
		{llmclient.StreamingBufferedOnly, llmclient.StreamingBufferedOnly, true},
	}
	for _, tc := range cases {
		got := streamingFidelityMeets(tc.have, tc.need)
		if got != tc.wantMatch {
			t.Errorf("streamingFidelityMeets(%q, %q) = %v, want %v",
				tc.have, tc.need, got, tc.wantMatch)
		}
	}
}

// TestSelectBackend_MinContextTokensFilter verifies that a backend is excluded
// when its MaxContextTokens is less than the required MinContextTokens, and
// that backends with MaxContextTokens==0 (unknown) are not excluded.
func TestSelectBackend_MinContextTokensFilter(t *testing.T) {
	smallCaps := llmclient.Capabilities{
		Backend:          "small",
		Streaming:        llmclient.StreamingStructured,
		MaxContextTokens: 4096,
	}
	largeCaps := llmclient.Capabilities{
		Backend:          "large",
		Streaming:        llmclient.StreamingStructured,
		MaxContextTokens: 200_000,
	}
	unknownCaps := llmclient.Capabilities{
		Backend:          "unknown",
		Streaming:        llmclient.StreamingStructured,
		MaxContextTokens: 0, // not reported
	}

	fSmall := makeFactory("small", "small", PreferClaude, smallCaps, true)
	fLarge := makeFactory("large", "large", PreferCodex, largeCaps, true)
	fUnknown := makeFactory("unknown", "unknown", PreferOpenCode, unknownCaps, true)

	restore := resetBackendFactoriesForTest(fSmall, fLarge, fUnknown)
	defer restore()

	detected := []CliInfo{
		{Name: "small", Binary: "small", Path: "/bin/small"},
		{Name: "large", Binary: "large", Path: "/bin/large"},
		{Name: "unknown", Binary: "unknown", Path: "/bin/unknown"},
	}

	req := Requirements{MinContextTokens: 100_000}
	candidates := filterByStaticRequirements(detected, req)

	names := make(map[string]bool)
	for _, c := range candidates {
		names[c.factory.Name] = true
	}

	if names["small"] {
		t.Error("small backend (4096 tokens) should be filtered out for MinContextTokens=100000")
	}
	if !names["large"] {
		t.Error("large backend (200000 tokens) should pass MinContextTokens=100000")
	}
	if !names["unknown"] {
		t.Error("unknown-context backend (0) should not be filtered out (unknown ≠ insufficient)")
	}
}

// TestSelectBackend_PreferAutoWithMultipleCandidates verifies that when
// PreferAuto is the only entry, the highest-priority (lowest Preference value)
// candidate is returned.
func TestSelectBackend_PreferAutoWithMultipleCandidates(t *testing.T) {
	fClaude := makeFactory("claude", "claude", PreferClaude, structuredCaps("claude"), true)
	fCodex := makeFactory("codex-exec", "codex", PreferCodex, structuredCaps("codex-exec"), true)
	fOmp := makeFactory("omp", "omp", PreferOmp, structuredCaps("omp"), true)

	restore := resetBackendFactoriesForTest(fClaude, fCodex, fOmp)
	defer restore()

	detected := []CliInfo{
		{Name: "claude", Binary: "claude", Path: "/bin/claude"},
		{Name: "codex", Binary: "codex", Path: "/bin/codex"},
		{Name: "omp", Binary: "omp", Path: "/bin/omp"},
	}

	candidates := filterByStaticRequirements(detected, Requirements{})

	b, err := pickByPreference(candidates, []BackendPreference{PreferAuto})
	if err != nil {
		t.Fatalf("pickByPreference([PreferAuto]): %v", err)
	}
	if b.Name() != "claude" {
		t.Errorf("PreferAuto: got %q, want %q", b.Name(), "claude")
	}
}

// TestSelectBackendByName_AllRegisteredBackends verifies that SelectBackendByName
// can address every registered backend by its canonical name. Each backend must
// be selectable by name: the call either returns a valid backend (when the
// binary is on PATH) or a typed *BackendNotFoundError (binary absent). Any
// other error type is a bug because the factory IS registered.
func TestSelectBackendByName_AllRegisteredBackends(t *testing.T) {
	// Canonical names that MUST be registered.
	expected := []string{
		"claude",
		"codex-exec",
		"codex-appserver",
		"opencode-run",
		"opencode-serve",
		"omp",
		"omp-rpc",
	}

	for _, name := range expected {
		name := name
		t.Run(name, func(t *testing.T) {
			// The factory MUST be registered regardless of binary availability.
			_, registered := factoryByName(name)
			if !registered {
				t.Errorf("backend %q: factory not registered", name)
				// Continue to test SelectBackendByName regardless.
			}

			b, err := SelectBackendByName(name)
			if err != nil {
				// Binary is not on PATH in this environment — that is OK.
				// The error MUST be a *BackendNotFoundError (ErrUnknownBackend),
				// not some other unexpected error type.
				if !errors.Is(err, ErrUnknownBackend) {
					t.Errorf("SelectBackendByName(%q): got %v, want ErrUnknownBackend or nil", name, err)
				}
				var bne *BackendNotFoundError
				if !errors.As(err, &bne) {
					t.Errorf("SelectBackendByName(%q): error type %T, want *BackendNotFoundError", name, err)
				} else if bne.Name != name {
					t.Errorf("BackendNotFoundError.Name = %q, want %q", bne.Name, name)
				}
				return
			}

			if b == nil {
				t.Errorf("SelectBackendByName(%q): returned nil backend without error", name)
				return
			}
			//nolint:errcheck // test cleanup
			defer b.Close()
			if b.Name() != name {
				t.Errorf("SelectBackendByName(%q): backend.Name() = %q, want %q", name, b.Name(), name)
			}
		})
	}
}

// TestSelectBackend_StrictProbeFiltersUnavailable verifies that filterByProbe
// removes backends whose Available() returns false.
func TestSelectBackend_StrictProbeFiltersUnavailable(t *testing.T) {
	fAvail := makeFactory("claude", "claude", PreferClaude, structuredCaps("claude"), true)
	fUnavail := makeFactory("codex-exec", "codex", PreferCodex, structuredCaps("codex-exec"), false)

	restore := resetBackendFactoriesForTest(fAvail, fUnavail)
	defer restore()

	candidates := []candidate{
		{factory: fAvail, info: CliInfo{Name: "claude", Binary: "claude", Path: "/bin/claude"}},
		{factory: fUnavail, info: CliInfo{Name: "codex", Binary: "codex", Path: "/bin/codex"}},
	}

	ctx := context.Background()
	filtered := filterByProbe(ctx, candidates)

	if len(filtered) != 1 {
		t.Fatalf("filterByProbe: got %d candidates, want 1", len(filtered))
	}
	if filtered[0].factory.Name != "claude" {
		t.Errorf("filterByProbe: surviving candidate %q, want %q", filtered[0].factory.Name, "claude")
	}
}
