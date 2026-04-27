package llmcli

import (
	"context"
	"fmt"

	"github.com/php-workx/fabrikk/llmclient"
)

// BackendPreference identifies a backend family and controls the default
// priority ordering when multiple backends are available. Lower values indicate
// higher priority.
type BackendPreference int

const (
	// PreferAuto lets SelectBackend choose the best available backend using
	// the built-in default priority order.
	PreferAuto BackendPreference = iota

	// PreferClaude prefers the Claude Code stream-json backend.
	PreferClaude

	// PreferCodex prefers a Codex backend (app-server if available, exec
	// otherwise).
	PreferCodex

	// PreferOpenCode prefers an OpenCode backend (serve if available, run
	// otherwise).
	PreferOpenCode

	// PreferOmp prefers an omp backend (RPC if available, print otherwise).
	// omp is intended for internal fabrikk use only.
	PreferOmp
)

// Requirements describes the features a caller needs from a backend. Backends
// that cannot statically satisfy all non-zero requirements are filtered out
// before preference ordering applies. SelectBackend never silently downgrades a
// required capability.
type Requirements struct {
	// NeedsToolEvents requires the backend to emit structured toolcall_start /
	// toolcall_end events, even if the CLI executes built-in tools internally.
	NeedsToolEvents bool

	// NeedsHostToolDefs requires the backend to accept host-defined tool
	// schemas injected by the caller.
	NeedsHostToolDefs bool

	// NeedsHostToolApproval requires the host to be able to approve or reject
	// tool calls before the CLI executes them.
	NeedsHostToolApproval bool

	// NeedsToolResults requires the host to be able to send tool results back
	// to the model mid-turn.
	NeedsToolResults bool

	// NeedsMultiTurn requires the backend to support session continuity
	// (e.g. claude --resume).
	NeedsMultiTurn bool

	// MinStreaming sets the minimum streaming fidelity the caller accepts.
	// Backends below this level are filtered out.
	MinStreaming llmclient.StreamingFidelity

	// NeedsThinking requires the backend to surface model thinking blocks.
	NeedsThinking bool

	// NeedsUsage requires the backend to report token usage on done events.
	NeedsUsage bool

	// Model is an optional model hint. Backends are filtered by this value only
	// when their static capabilities enumerate supported models.
	Model string

	// MinContextTokens is an optional minimum context window size in tokens.
	// Backends that statically report a smaller window are filtered out. A
	// value of 0 means no requirement. Backends that do not report their
	// context window (MaxContextTokens == 0) are not filtered out.
	MinContextTokens int

	// NeedsOllama requires the backend to support routing through an Ollama
	// instance.
	NeedsOllama bool

	// StrictProbe, when true, runs Available() on each candidate before
	// returning a backend. This catches backends whose binary has disappeared
	// between detection and selection.
	StrictProbe bool

	// PreferredOrder is an optional ordered list of backend families to try.
	// The first family with an available, requirements-satisfying backend wins.
	// An empty slice or a single PreferAuto entry uses the default priority
	// ordering.
	PreferredOrder []BackendPreference
}

// candidate pairs a registered factory with the CliInfo detected for its
// binary. The slice of candidates produced by [filterByStaticRequirements] is
// already in factory priority order.
type candidate struct {
	factory backendFactory
	info    CliInfo
}

// SelectBackend returns the highest-priority backend that satisfies req.
// It never silently downgrades a required capability. Returns
// [ErrNoBackendAvailable] if no installed CLI meets all requirements.
func SelectBackend(ctx context.Context, req Requirements) (llmclient.Backend, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	detected := DetectAvailableContext(ctx)
	candidates := filterByStaticRequirements(detected, req)
	if req.StrictProbe {
		candidates = filterByProbe(ctx, candidates)
	}
	return pickByPreference(candidates, req.PreferredOrder)
}

// MustSelectBackend calls [SelectBackend] and panics if no backend is
// available. Intended for tests that require a specific backend to be present.
func MustSelectBackend(ctx context.Context, req Requirements) llmclient.Backend {
	b, err := SelectBackend(ctx, req)
	if err != nil {
		panic(fmt.Sprintf("llmcli: MustSelectBackend: %v", err))
	}
	return b
}

// SelectBackendByName returns the backend registered under name, instantiated
// with the currently detected CliInfo for its binary. Returns a
// [*BackendNotFoundError] (which wraps [ErrUnknownBackend]) if name is not
// registered or its binary is not on PATH.
func SelectBackendByName(name string) (llmclient.Backend, error) {
	f, ok := factoryByName(name)
	if !ok {
		return nil, &BackendNotFoundError{Name: name}
	}
	detected := DetectAvailable()
	for _, info := range detected {
		if info.Binary == f.Binary {
			return f.New(info), nil
		}
	}
	return nil, &BackendNotFoundError{Name: name}
}

// NewBackendByName constructs the backend registered under name using the
// caller-provided CliInfo. Unlike SelectBackendByName, it does not inspect PATH
// or run detection; callers are responsible for providing a usable executable
// path in info.
func NewBackendByName(name string, info CliInfo) (llmclient.Backend, error) {
	f, ok := factoryByName(name)
	if !ok {
		return nil, &BackendNotFoundError{Name: name}
	}
	return f.New(info), nil
}

// filterByStaticRequirements returns the candidates whose registered factory
// has a binary in detected and whose static Capabilities satisfy req. The
// result is in factory priority order (from [registeredBackendFactories]).
func filterByStaticRequirements(detected []CliInfo, req Requirements) []candidate {
	allFactories := registeredBackendFactories()

	detectedByBinary := make(map[string]CliInfo, len(detected))
	for _, info := range detected {
		// Use the first matching binary in case of duplicates (DetectAvailable
		// returns only one entry per binary).
		if _, already := detectedByBinary[info.Binary]; !already {
			detectedByBinary[info.Binary] = info
		}
	}

	out := make([]candidate, 0, len(allFactories))
	for i := range allFactories {
		f := &allFactories[i]
		info, found := detectedByBinary[f.Binary]
		if !found {
			continue
		}
		if satisfiesStaticRequirements(f.Capabilities, req) {
			out = append(out, candidate{factory: *f, info: info})
		}
	}
	return out
}

// satisfiesStaticRequirements reports whether the given Capabilities satisfy
// all non-zero fields in req without running any live probes. This is the
// "never silently downgrade" gate: a backend that cannot meet a stated
// requirement is excluded from selection regardless of preference.
func satisfiesStaticRequirements(caps llmclient.Capabilities, req Requirements) bool {
	if !satisfiesStaticFeatureRequirements(caps, req) {
		return false
	}
	if req.MinStreaming != "" && !streamingFidelityMeets(caps.Streaming, req.MinStreaming) {
		return false
	}
	if req.Model != "" && len(caps.Models) > 0 && !containsString(caps.Models, req.Model) {
		return false
	}
	// Only filter by context window when both sides are known.
	if req.MinContextTokens > 0 && caps.MaxContextTokens > 0 && caps.MaxContextTokens < req.MinContextTokens {
		return false
	}
	return true
}

func satisfiesStaticFeatureRequirements(caps llmclient.Capabilities, req Requirements) bool {
	if req.NeedsToolEvents && !caps.ToolEvents {
		return false
	}
	if req.NeedsHostToolDefs && !caps.HostToolDefs {
		return false
	}
	if req.NeedsHostToolApproval && !caps.HostToolApproval {
		return false
	}
	if req.NeedsToolResults && !caps.ToolResultInjection {
		return false
	}
	if req.NeedsMultiTurn && !caps.MultiTurn {
		return false
	}
	if req.NeedsThinking && !caps.Thinking {
		return false
	}
	if req.NeedsUsage && !caps.Usage {
		return false
	}
	if req.NeedsOllama && !caps.OllamaRouting {
		return false
	}
	return true
}

// streamingFidelityMeets reports whether have satisfies the need fidelity
// level. The ordering is:
//
//	BufferedOnly(0) < TextChunk(1) < StructuredUnknown(2) < Structured(3)
//
// StructuredUnknown does NOT satisfy a Structured requirement because its
// event schema is not yet pinned.
func streamingFidelityMeets(have, need llmclient.StreamingFidelity) bool {
	return fidelityRank(have) >= fidelityRank(need)
}

// fidelityRank maps a StreamingFidelity to a comparable integer.
func fidelityRank(f llmclient.StreamingFidelity) int {
	switch f {
	case llmclient.StreamingBufferedOnly:
		return 0
	case llmclient.StreamingTextChunk:
		return 1
	case llmclient.StreamingStructuredUnknown:
		return 2
	case llmclient.StreamingStructured:
		return 3
	default:
		return 0
	}
}

// filterByProbe removes candidates whose backend reports Available() == false.
// It instantiates each backend via its factory New function, calls Available(),
// and discards the instance. The selected backend is constructed fresh by
// [pickByPreference].
func filterByProbe(ctx context.Context, candidates []candidate) []candidate {
	out := candidates[:0:0]
	for i := range candidates {
		if ctx.Err() != nil {
			return out
		}
		b := candidates[i].factory.New(candidates[i].info)
		if b.Available() {
			out = append(out, candidates[i])
		}
	}
	return out
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

// pickByPreference selects the best candidate from the priority-ordered list.
//
// If preferredOrder is empty or contains only [PreferAuto], the first candidate
// (already in default priority order from [filterByStaticRequirements]) wins.
//
// Otherwise each preference in preferredOrder is tried in turn; the first
// family that has at least one candidate returns its highest-priority member.
// [PreferAuto] entries within a mixed list cause immediate selection of the
// current highest-priority remaining candidate.
//
// Returns [ErrNoBackendAvailable] if candidates is empty.
func pickByPreference(candidates []candidate, preferredOrder []BackendPreference) (llmclient.Backend, error) {
	if len(candidates) == 0 {
		return nil, ErrNoBackendAvailable
	}
	if len(preferredOrder) == 0 || (len(preferredOrder) == 1 && preferredOrder[0] == PreferAuto) {
		c := candidates[0]
		return c.factory.New(c.info), nil
	}
	for _, pref := range preferredOrder {
		if pref == PreferAuto {
			// Auto within a mixed list: use the best remaining candidate.
			c := candidates[0]
			return c.factory.New(c.info), nil
		}
		for i := range candidates {
			if candidates[i].factory.Preference == pref {
				return candidates[i].factory.New(candidates[i].info), nil
			}
		}
	}
	// None of the preferred families had a matching candidate; fall back to
	// the default highest-priority candidate rather than returning an error,
	// because PreferredOrder is a hint, not a hard requirement.
	c := candidates[0]
	return c.factory.New(c.info), nil
}
