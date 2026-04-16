package engine

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/php-workx/fabrikk/internal/agentcli"
	"github.com/php-workx/fabrikk/internal/state"
)

const (
	prepareTestConsentSource     = "test"
	prepareTestNormalizationGate = "normalization_review"
)

func TestDefaultPrepareOptionsCoversNormalizationControls(t *testing.T) {
	opts := DefaultPrepareOptions()

	if opts.NormalizeMode != state.NormalizationAuto {
		t.Fatalf("NormalizeMode = %q, want auto", opts.NormalizeMode)
	}
	if opts.LLMConsent {
		t.Fatal("LLMConsent default = true, want false")
	}
	if opts.FallbackDeterministic {
		t.Fatal("FallbackDeterministic default = true, want false")
	}
	if opts.ConverterBackendName != agentcli.BackendClaude || opts.VerifierBackendName != agentcli.BackendClaude {
		t.Fatalf("backend defaults = %q/%q, want claude/claude", opts.ConverterBackendName, opts.VerifierBackendName)
	}
	if opts.ConverterTimeoutSeconds <= 0 || opts.VerifierTimeoutSeconds <= 0 || opts.MaxInputBytes <= 0 || opts.MaxOutputBytes <= 0 {
		t.Fatalf("timeouts and byte limits must be positive: %+v", opts)
	}
}

func TestPrepareWithOptionsReturnsReadyResultForExplicitSpecs(t *testing.T) {
	dir := t.TempDir()
	specPath := writeSpecInput(t, dir, "spec.md", "- **AT-FR-001**: The system must import tasks.\n")
	eng := New(state.NewRunDir(dir, "placeholder"), dir)

	result, err := eng.PrepareWithOptions(context.Background(), []string{specPath}, DefaultPrepareOptions())
	if err != nil {
		t.Fatalf("PrepareWithOptions: %v", err)
	}

	if result.Status != PrepareStatusReady {
		t.Fatalf("status = %q, want ready", result.Status)
	}
	if result.RunID == "" || result.Artifact == nil || result.Artifact.RunID != result.RunID {
		t.Fatalf("result identity is incomplete: %+v", result)
	}
	if result.RunDir == nil || result.RunDir.Root != eng.RunDir.Root {
		t.Fatalf("result run dir = %+v, engine run dir = %+v", result.RunDir, eng.RunDir)
	}
	if result.Blocking {
		t.Fatal("deterministic explicit prepare should not be blocking")
	}
	if !strings.Contains(result.NextAction, "review") {
		t.Fatalf("next action = %q, want review guidance", result.NextAction)
	}

	artifact, err := eng.Prepare(context.Background(), []string{specPath})
	if err != nil {
		t.Fatalf("Prepare compatibility wrapper: %v", err)
	}
	if artifact.RunID == "" || len(artifact.Requirements) != 1 {
		t.Fatalf("compatibility artifact = %+v", artifact)
	}
}

func TestPrepareResultCanRepresentVerifierOutcomesWithoutError(t *testing.T) {
	failResult := &PrepareResult{
		RunID: "run-123",
		NormalizationReview: &state.SpecNormalizationReview{
			Status: state.ReviewFail,
		},
		Status:     PrepareStatusBlocked,
		Blocking:   true,
		NextAction: "Inspect normalization review findings.",
	}
	revisionResult := &PrepareResult{
		RunID: "run-123",
		NormalizationReview: &state.SpecNormalizationReview{
			Status: state.ReviewNeedsRevision,
		},
		Status:     PrepareStatusAwaitingUserInput,
		Blocking:   true,
		NextAction: "Decide whether to revise or continue.",
	}

	if failResult.NormalizationReview.Status != state.ReviewFail || failResult.Status != PrepareStatusBlocked {
		t.Fatalf("fail result not representable: %+v", failResult)
	}
	if revisionResult.NormalizationReview.Status != state.ReviewNeedsRevision || revisionResult.Status != PrepareStatusAwaitingUserInput {
		t.Fatalf("needs_revision result not representable: %+v", revisionResult)
	}
}

func TestPrepareWithOptionsReturnsOperationalErrors(t *testing.T) {
	dir := t.TempDir()
	eng := New(state.NewRunDir(dir, "placeholder"), dir)

	result, err := eng.PrepareWithOptions(context.Background(), []string{dir + "/missing.md"}, DefaultPrepareOptions())
	if err == nil {
		t.Fatal("expected invalid path error")
	}
	if result != nil {
		t.Fatalf("result = %+v, want nil on operational failure", result)
	}
}

func TestPrepareRoutingExplicitIDsAutoDoesNotCallLLM(t *testing.T) {
	dir := t.TempDir()
	specPath := writeSpecInput(t, dir, "spec.md", "- **AT-FR-001**: The system must import tasks.\n")
	eng := New(state.NewRunDir(dir, "placeholder"), dir)
	stubAgentInvoke(t, func(ctx context.Context, backend *agentcli.CLIBackend, prompt string, timeoutSec int) (string, error) {
		t.Fatalf("LLM should not be called for explicit IDs in auto mode")
		return "", nil
	})

	result, err := eng.PrepareWithOptions(context.Background(), []string{specPath}, DefaultPrepareOptions())
	if err != nil {
		t.Fatalf("PrepareWithOptions: %v", err)
	}
	if result.Status != PrepareStatusReady || result.Artifact == nil || len(result.Artifact.Requirements) != 1 {
		t.Fatalf("result = %+v, want ready deterministic artifact", result)
	}
	if !result.Artifact.Normalization.UsedDeterministic || result.Artifact.Normalization.UsedLLM {
		t.Fatalf("normalization metadata = %+v, want deterministic only", result.Artifact.Normalization)
	}
}

func TestPrepareRoutingFreeFormAutoWithoutConsentAwaitsUserInput(t *testing.T) {
	dir := t.TempDir()
	specPath := writeSpecInput(t, dir, "spec.md", "The system should import tasks from Markdown sentences.\n")
	eng := New(state.NewRunDir(dir, "placeholder"), dir)
	stubAgentInvoke(t, func(ctx context.Context, backend *agentcli.CLIBackend, prompt string, timeoutSec int) (string, error) {
		t.Fatalf("LLM should not be called without consent")
		return "", nil
	})

	result, err := eng.PrepareWithOptions(context.Background(), []string{specPath}, DefaultPrepareOptions())
	if err != nil {
		t.Fatalf("PrepareWithOptions: %v", err)
	}
	assertAwaitingNormalizationConsent(t, result)
}

func TestPrepareRoutingAlwaysWithoutConsentAwaitsUserInput(t *testing.T) {
	dir := t.TempDir()
	specPath := writeSpecInput(t, dir, "spec.md", "The system should import tasks from Markdown sentences.\n")
	eng := New(state.NewRunDir(dir, "placeholder"), dir)
	opts := DefaultPrepareOptions()
	opts.NormalizeMode = state.NormalizationAlways
	stubAgentInvoke(t, func(ctx context.Context, backend *agentcli.CLIBackend, prompt string, timeoutSec int) (string, error) {
		t.Fatalf("LLM should not be called without consent")
		return "", nil
	})

	result, err := eng.PrepareWithOptions(context.Background(), []string{specPath}, opts)
	if err != nil {
		t.Fatalf("PrepareWithOptions: %v", err)
	}
	assertAwaitingNormalizationConsent(t, result)
}

func TestPrepareRoutingAlwaysWithConsentUsesLLM(t *testing.T) {
	dir := t.TempDir()
	specPath := writeSpecInput(t, dir, "spec.md", "The system should import tasks from Markdown sentences.\n")
	eng := New(state.NewRunDir(dir, "placeholder"), dir)
	opts := DefaultPrepareOptions()
	opts.NormalizeMode = state.NormalizationAlways
	opts.LLMConsent = true
	opts.LLMConsentSource = prepareTestConsentSource
	stubSpecNormalizationLLM(t)

	result, err := eng.PrepareWithOptions(context.Background(), []string{specPath}, opts)
	if err != nil {
		t.Fatalf("PrepareWithOptions: %v", err)
	}
	if result.Status != PrepareStatusAwaitingArtifactApproval {
		t.Fatalf("status = %q, want awaiting artifact approval", result.Status)
	}
	if result.Artifact == nil || len(result.Artifact.Requirements) != 1 || !result.Artifact.Normalization.UsedLLM {
		t.Fatalf("artifact = %+v, want LLM-normalized candidate", result.Artifact)
	}
	if result.NormalizationReview == nil || result.NormalizationReview.Status != state.ReviewPass {
		t.Fatalf("review = %+v, want pass", result.NormalizationReview)
	}
	if _, err := eng.RunDir.ReadNormalizedArtifactCandidate(); err != nil {
		t.Fatalf("read normalized candidate: %v", err)
	}
	if _, err := eng.RunDir.ReadSpecNormalizationReview(); err != nil {
		t.Fatalf("read normalization review: %v", err)
	}
}

func TestPrepareRoutingNormalizeNeverForcesOfflineDeterministic(t *testing.T) {
	dir := t.TempDir()
	specPath := writeSpecInput(t, dir, "spec.md", "- Import tasks from Markdown bullets.\n")
	eng := New(state.NewRunDir(dir, "placeholder"), dir)
	opts := DefaultPrepareOptions()
	opts.NormalizeMode = state.NormalizationNever
	stubAgentInvoke(t, func(ctx context.Context, backend *agentcli.CLIBackend, prompt string, timeoutSec int) (string, error) {
		t.Fatalf("LLM should not be called in normalize never mode")
		return "", nil
	})

	result, err := eng.PrepareWithOptions(context.Background(), []string{specPath}, opts)
	if err == nil {
		t.Fatal("expected deterministic no requirements error")
	}
	if result != nil {
		t.Fatalf("result = %+v, want nil on deterministic failure", result)
	}
	if !strings.Contains(err.Error(), "no requirements found") {
		t.Fatalf("error = %q, want no requirements guidance", err.Error())
	}
}

func TestPrepareRoutingFallbackDeterministicAfterLLMFailure(t *testing.T) {
	dir := t.TempDir()
	specPath := writeSpecInput(t, dir, "spec.md", "- The system should import tasks from Markdown bullets.\n")
	eng := New(state.NewRunDir(dir, "placeholder"), dir)
	opts := DefaultPrepareOptions()
	opts.NormalizeMode = state.NormalizationAlways
	opts.LLMConsent = true
	opts.FallbackDeterministic = true
	stubAgentInvoke(t, func(ctx context.Context, backend *agentcli.CLIBackend, prompt string, timeoutSec int) (string, error) {
		return "partial output", fmt.Errorf("converter unavailable")
	})

	result, err := eng.PrepareWithOptions(context.Background(), []string{specPath}, opts)
	if err != nil {
		t.Fatalf("PrepareWithOptions: %v", err)
	}
	if result.Status != PrepareStatusReady || result.Artifact == nil || len(result.Artifact.Requirements) != 1 {
		t.Fatalf("result = %+v, want deterministic fallback artifact", result)
	}
	if !result.Artifact.Normalization.FallbackDeterministic || !result.Artifact.Normalization.UsedDeterministic {
		t.Fatalf("normalization metadata = %+v, want deterministic fallback", result.Artifact.Normalization)
	}
}

func TestPrepareLLMPassPersistsRunArtifactAndArtifactApprovalGate(t *testing.T) {
	dir := t.TempDir()
	specPath := writeSpecInput(t, dir, "spec.md", "The system should import tasks from Markdown sentences.\n")
	eng := New(state.NewRunDir(dir, "placeholder"), dir)
	opts := llmPrepareOptionsForTest()
	stubSpecNormalizationLLMWithReviewStatus(t, state.ReviewPass)

	result, err := eng.PrepareWithOptions(context.Background(), []string{specPath}, opts)
	if err != nil {
		t.Fatalf("PrepareWithOptions: %v", err)
	}
	if result.Status != PrepareStatusAwaitingArtifactApproval {
		t.Fatalf("result status = %q, want awaiting artifact approval", result.Status)
	}
	status, err := eng.RunDir.ReadStatus()
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status.State != state.RunAwaitingArtifactApproval || status.CurrentGate != "awaiting_artifact_approval" {
		t.Fatalf("status = %+v, want awaiting artifact approval gate", status)
	}
	if _, err := eng.RunDir.ReadArtifact(); err != nil {
		t.Fatalf("read run artifact draft: %v", err)
	}
}

func TestPrepareLLMVerifierFailPersistsArtifactsAndBlocksReviewGate(t *testing.T) {
	dir := t.TempDir()
	specPath := writeSpecInput(t, dir, "spec.md", "The system should import tasks from Markdown sentences.\n")
	eng := New(state.NewRunDir(dir, "placeholder"), dir)
	opts := llmPrepareOptionsForTest()
	stubSpecNormalizationLLMWithReviewStatus(t, state.ReviewFail)

	result, err := eng.PrepareWithOptions(context.Background(), []string{specPath}, opts)
	if err != nil {
		t.Fatalf("PrepareWithOptions: %v", err)
	}
	if result.Status != PrepareStatusBlocked || !result.Blocking {
		t.Fatalf("result = %+v, want blocked verifier result", result)
	}
	status, err := eng.RunDir.ReadStatus()
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status.State != state.RunBlocked || status.CurrentGate != prepareTestNormalizationGate {
		t.Fatalf("status = %+v, want blocked normalization_review gate", status)
	}
	if _, err := eng.RunDir.ReadArtifact(); err != nil {
		t.Fatalf("read run artifact draft: %v", err)
	}
	if review, err := eng.RunDir.ReadSpecNormalizationReview(); err != nil || review.Status != state.ReviewFail {
		t.Fatalf("review = %+v, err = %v; want persisted fail review", review, err)
	}
}

func TestPrepareLLMNeedsRevisionPersistsDraftAndClarificationGate(t *testing.T) {
	dir := t.TempDir()
	specPath := writeSpecInput(t, dir, "spec.md", "The system should import tasks from Markdown sentences.\n")
	eng := New(state.NewRunDir(dir, "placeholder"), dir)
	opts := llmPrepareOptionsForTest()
	stubSpecNormalizationLLMWithReviewStatus(t, state.ReviewNeedsRevision)

	result, err := eng.PrepareWithOptions(context.Background(), []string{specPath}, opts)
	if err != nil {
		t.Fatalf("PrepareWithOptions: %v", err)
	}
	if result.Status != PrepareStatusAwaitingUserInput || !result.Blocking {
		t.Fatalf("result = %+v, want awaiting user input for needs_revision", result)
	}
	if !strings.Contains(result.NextAction, "revise") || !strings.Contains(result.NextAction, "approve") {
		t.Fatalf("next action = %q, want revise/approve guidance", result.NextAction)
	}
	status, err := eng.RunDir.ReadStatus()
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status.State != state.RunAwaitingClarification || status.CurrentGate != prepareTestNormalizationGate {
		t.Fatalf("status = %+v, want awaiting clarification normalization_review gate", status)
	}
	if _, err := eng.RunDir.ReadArtifact(); err != nil {
		t.Fatalf("read run artifact draft: %v", err)
	}
}

func TestPrepareLLMConverterFailurePersistsInspectableFailureWithoutRunArtifact(t *testing.T) {
	dir := t.TempDir()
	specPath := writeSpecInput(t, dir, "spec.md", "The system should import tasks from Markdown sentences.\n")
	eng := New(state.NewRunDir(dir, "placeholder"), dir)
	opts := llmPrepareOptionsForTest()
	raw := "partial converter output"
	stubAgentInvoke(t, func(ctx context.Context, backend *agentcli.CLIBackend, prompt string, timeoutSec int) (string, error) {
		return raw, fmt.Errorf("converter unavailable")
	})

	_, err := eng.PrepareWithOptions(context.Background(), []string{specPath}, opts)
	if err == nil {
		t.Fatal("expected converter failure")
	}
	assertFileContains(t, eng.RunDir.SpecNormalizationConverterRaw(), raw)
	if _, readErr := eng.RunDir.ReadArtifact(); readErr == nil {
		t.Fatal("run-artifact.json should not be written when converter produced no candidate")
	}
	status, readErr := eng.RunDir.ReadStatus()
	if readErr != nil {
		t.Fatalf("read status: %v", readErr)
	}
	if status.State != state.RunBlocked || status.CurrentGate != "normalization_conversion" {
		t.Fatalf("status = %+v, want blocked normalization_conversion gate", status)
	}
}

func llmPrepareOptionsForTest() PrepareOptions {
	opts := DefaultPrepareOptions()
	opts.NormalizeMode = state.NormalizationAlways
	opts.LLMConsent = true
	opts.LLMConsentSource = prepareTestConsentSource
	return opts
}

func assertAwaitingNormalizationConsent(t *testing.T, result *PrepareResult) {
	t.Helper()

	if result == nil {
		t.Fatal("result is nil")
	}
	if result.Status != PrepareStatusAwaitingUserInput {
		t.Fatalf("status = %q, want awaiting user input", result.Status)
	}
	if !result.Blocking {
		t.Fatal("result should be blocking until consent is supplied")
	}
	for _, want := range []string{"--normalize always", "--allow-llm-normalization"} {
		if !strings.Contains(result.NextAction, want) {
			t.Fatalf("next action = %q, want %q", result.NextAction, want)
		}
	}
}

func stubSpecNormalizationLLM(t *testing.T) {
	t.Helper()
	stubSpecNormalizationLLMWithReviewStatus(t, state.ReviewPass)
}

func stubSpecNormalizationLLMWithReviewStatus(t *testing.T, status state.ReviewStatus) {
	t.Helper()

	stubAgentInvoke(t, func(ctx context.Context, backend *agentcli.CLIBackend, prompt string, timeoutSec int) (string, error) {
		runID := promptLineValue(prompt, "Run ID: ")
		switch {
		case strings.Contains(prompt, "# spec normalization converter"):
			sourcePath := promptLineValue(prompt, "## Source: ")
			fingerprint := promptLineValue(prompt, "Fingerprint: ")
			sourceText := strings.TrimPrefix(promptLineValue(prompt, "1 | "), "1 | ")
			return specNormalizationArtifactJSONWithText(t, runID, sourcePath, fingerprint, sourceText), nil
		case strings.Contains(prompt, "# spec normalization verifier"):
			artifactHash := promptLineValue(prompt, "Normalized artifact hash: ")
			manifestHash := promptLineValue(prompt, "Source manifest hash: ")
			reviewedInputHash := promptLineValue(prompt, "Reviewed input hash: ")
			findings := []state.ReviewFinding(nil)
			if status != state.ReviewPass {
				findings = []state.ReviewFinding{{
					FindingID:       "snr-001",
					Severity:        "high",
					Category:        "lost_information",
					Summary:         "Missing source requirement.",
					SuggestedRepair: "Restore the missing requirement.",
				}}
			}
			return specNormalizationReviewJSON(t, runID, status, artifactHash, manifestHash, reviewedInputHash, findings), nil
		default:
			t.Fatalf("unexpected prompt:\n%s", prompt)
			return "", nil
		}
	})
}

func promptLineValue(prompt, prefix string) string {
	for _, line := range strings.Split(prompt, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}
