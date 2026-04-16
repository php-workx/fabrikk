package engine

import (
	"github.com/php-workx/fabrikk/internal/agentcli"
	"github.com/php-workx/fabrikk/internal/state"
)

// PrepareStatus summarizes the prepare outcome for callers that need to inspect
// normalization and approval gates without treating review outcomes as I/O errors.
type PrepareStatus string

const (
	// PrepareStatusReady means prepare produced a deterministic artifact ready for the existing review flow.
	PrepareStatusReady PrepareStatus = "ready"
	// PrepareStatusAwaitingArtifactApproval means a normalized artifact needs explicit approval.
	PrepareStatusAwaitingArtifactApproval PrepareStatus = "awaiting_artifact_approval"
	// PrepareStatusAwaitingUserInput means prepare needs consent, clarification, or an explicit user decision.
	PrepareStatusAwaitingUserInput PrepareStatus = "awaiting_user_input"
	// PrepareStatusBlocked means prepare produced inspectable artifacts but cannot proceed safely.
	PrepareStatusBlocked PrepareStatus = "blocked"
)

// PrepareOptions controls deterministic and LLM-assisted spec ingestion.
type PrepareOptions struct {
	NormalizeMode           state.NormalizationMode
	LLMConsent              bool
	LLMConsentSource        string
	FallbackDeterministic   bool
	ConverterBackendName    string
	VerifierBackendName     string
	ConverterTimeoutSeconds int
	VerifierTimeoutSeconds  int
	MaxInputBytes           int64
	MaxOutputBytes          int
}

// DefaultPrepareOptions returns conservative defaults: explicit specs are parsed
// deterministically and LLM normalization requires explicit consent.
func DefaultPrepareOptions() PrepareOptions {
	return PrepareOptions{
		NormalizeMode:           state.NormalizationAuto,
		LLMConsent:              false,
		FallbackDeterministic:   false,
		ConverterBackendName:    agentcli.BackendClaude,
		VerifierBackendName:     agentcli.BackendClaude,
		ConverterTimeoutSeconds: defaultSpecNormalizationConverterTimeoutSec,
		VerifierTimeoutSeconds:  defaultSpecNormalizationConverterTimeoutSec,
		MaxInputBytes:           1 << 20,
		MaxOutputBytes:          defaultSpecNormalizationMaxOutputBytes,
	}
}

func (o PrepareOptions) withDefaults() PrepareOptions {
	defaults := DefaultPrepareOptions()
	if o.NormalizeMode == "" {
		o.NormalizeMode = defaults.NormalizeMode
	}
	if o.ConverterBackendName == "" {
		o.ConverterBackendName = defaults.ConverterBackendName
	}
	if o.VerifierBackendName == "" {
		o.VerifierBackendName = defaults.VerifierBackendName
	}
	if o.ConverterTimeoutSeconds <= 0 {
		o.ConverterTimeoutSeconds = defaults.ConverterTimeoutSeconds
	}
	if o.VerifierTimeoutSeconds <= 0 {
		o.VerifierTimeoutSeconds = defaults.VerifierTimeoutSeconds
	}
	if o.MaxInputBytes <= 0 {
		o.MaxInputBytes = defaults.MaxInputBytes
	}
	if o.MaxOutputBytes <= 0 {
		o.MaxOutputBytes = defaults.MaxOutputBytes
	}
	return o
}

// PrepareResult is the inspectable outcome of a prepare attempt.
type PrepareResult struct {
	RunID               string
	Artifact            *state.RunArtifact
	NormalizationReview *state.SpecNormalizationReview
	Status              PrepareStatus
	RunDir              *state.RunDir
	Blocking            bool
	NextAction          string
	OperationalError    string
}
