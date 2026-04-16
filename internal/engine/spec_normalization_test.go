package engine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/php-workx/fabrikk/internal/agentcli"
	"github.com/php-workx/fabrikk/internal/state"
)

func TestConvertSpecsToArtifactSuccessPersistsPromptRawAndCandidate(t *testing.T) {
	runDir := newSpecNormalizationRunDir(t)
	bundle := newSpecNormalizationBundle(t, "Users can import tasks.")
	raw := `preface
` + specNormalizationArtifactJSON(t, "run-123", bundle.Manifest.Sources[0].Path, bundle.Manifest.Sources[0].Fingerprint) + `
trailing text`

	var gotPrompt string
	var gotBackend string
	var gotTimeout int
	stubAgentInvoke(t, func(ctx context.Context, backend *agentcli.CLIBackend, prompt string, timeoutSec int) (string, error) {
		gotPrompt = prompt
		gotBackend = backend.Command
		gotTimeout = timeoutSec
		return raw, nil
	})

	artifact, err := (&Engine{RunDir: runDir}).convertSpecsToArtifact(context.Background(), "run-123", bundle, specNormalizationConverterOptions{
		BackendName:    agentcli.BackendClaude,
		TimeoutSeconds: 17,
		MaxOutputBytes: 4096,
	})
	if err != nil {
		t.Fatalf("convertSpecsToArtifact: %v", err)
	}

	if artifact.RunID != "run-123" {
		t.Fatalf("artifact run id = %q, want run-123", artifact.RunID)
	}
	if len(artifact.Requirements) != 1 || artifact.Requirements[0].ID != "AT-FR-001" {
		t.Fatalf("requirements = %+v, want AT-FR-001", artifact.Requirements)
	}
	if gotBackend == "" {
		t.Fatal("backend was not passed to agent invocation")
	}
	if gotTimeout != 17 {
		t.Fatalf("timeout = %d, want 17", gotTimeout)
	}
	for _, want := range []string{
		"spec normalization converter",
		"Run ID: run-123",
		"Source manifest hash: " + bundle.ManifestHash,
		"1 | Users can import tasks.",
	} {
		if !strings.Contains(gotPrompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, gotPrompt)
		}
	}

	assertFileContains(t, runDir.SpecNormalizationConverterPrompt(), "Source manifest hash: "+bundle.ManifestHash)
	assertFileContains(t, runDir.SpecNormalizationConverterRaw(), raw)
	candidate, err := runDir.ReadNormalizedArtifactCandidate()
	if err != nil {
		t.Fatalf("read normalized artifact candidate: %v", err)
	}
	if candidate.RunID != "run-123" || candidate.Requirements[0].SourceRefs[0].Path == "" {
		t.Fatalf("candidate = %+v, want persisted artifact with source evidence", candidate)
	}
}

func TestConvertSpecsToArtifactMalformedJSONPersistsRaw(t *testing.T) {
	runDir := newSpecNormalizationRunDir(t)
	bundle := newSpecNormalizationBundle(t, "Malformed response case.")
	raw := `{"schema_version":`
	stubAgentInvoke(t, func(ctx context.Context, backend *agentcli.CLIBackend, prompt string, timeoutSec int) (string, error) {
		return raw, nil
	})

	_, err := (&Engine{RunDir: runDir}).convertSpecsToArtifact(context.Background(), "run-123", bundle, specNormalizationConverterOptions{MaxOutputBytes: 4096})
	if err == nil {
		t.Fatal("expected malformed JSON error")
	}
	if !strings.Contains(err.Error(), "JSON") {
		t.Fatalf("error = %q, want JSON context", err.Error())
	}
	assertFileContains(t, runDir.SpecNormalizationConverterRaw(), raw)
}

func TestConvertSpecsToArtifactMissingJSONPersistsRaw(t *testing.T) {
	runDir := newSpecNormalizationRunDir(t)
	bundle := newSpecNormalizationBundle(t, "Missing JSON case.")
	raw := `I cannot complete that.`
	stubAgentInvoke(t, func(ctx context.Context, backend *agentcli.CLIBackend, prompt string, timeoutSec int) (string, error) {
		return raw, nil
	})

	_, err := (&Engine{RunDir: runDir}).convertSpecsToArtifact(context.Background(), "run-123", bundle, specNormalizationConverterOptions{MaxOutputBytes: 4096})
	if err == nil {
		t.Fatal("expected missing JSON error")
	}
	if !strings.Contains(err.Error(), "no JSON object") {
		t.Fatalf("error = %q, want missing JSON context", err.Error())
	}
	assertFileContains(t, runDir.SpecNormalizationConverterRaw(), raw)
}

func TestConvertSpecsToArtifactBackendFailurePersistsRaw(t *testing.T) {
	runDir := newSpecNormalizationRunDir(t)
	bundle := newSpecNormalizationBundle(t, "Backend failure case.")
	raw := "partial converter output"
	stubAgentInvoke(t, func(ctx context.Context, backend *agentcli.CLIBackend, prompt string, timeoutSec int) (string, error) {
		return raw, errors.New("backend unavailable")
	})

	_, err := (&Engine{RunDir: runDir}).convertSpecsToArtifact(context.Background(), "run-123", bundle, specNormalizationConverterOptions{
		BackendName:    agentcli.BackendClaude,
		TimeoutSeconds: 10,
		MaxOutputBytes: 4096,
	})
	if err == nil {
		t.Fatal("expected backend failure")
	}
	for _, want := range []string{"converter backend", agentcli.BackendClaude, "backend unavailable"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want %q", err.Error(), want)
		}
	}
	assertFileContains(t, runDir.SpecNormalizationConverterRaw(), raw)
}

func TestConvertSpecsToArtifactTimeoutPersistsRaw(t *testing.T) {
	runDir := newSpecNormalizationRunDir(t)
	bundle := newSpecNormalizationBundle(t, "Timeout case.")
	raw := "partial timeout output"
	stubAgentInvoke(t, func(ctx context.Context, backend *agentcli.CLIBackend, prompt string, timeoutSec int) (string, error) {
		return raw, context.DeadlineExceeded
	})

	_, err := (&Engine{RunDir: runDir}).convertSpecsToArtifact(context.Background(), "run-123", bundle, specNormalizationConverterOptions{
		BackendName:    agentcli.BackendClaude,
		TimeoutSeconds: 1,
		MaxOutputBytes: 4096,
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error = %q, want timeout context", err.Error())
	}
	assertFileContains(t, runDir.SpecNormalizationConverterRaw(), raw)
}

func TestConvertSpecsToArtifactRejectsOversizedOutputPersistsRaw(t *testing.T) {
	runDir := newSpecNormalizationRunDir(t)
	bundle := newSpecNormalizationBundle(t, "Oversized output case.")
	raw := strings.Repeat("x", 32)
	stubAgentInvoke(t, func(ctx context.Context, backend *agentcli.CLIBackend, prompt string, timeoutSec int) (string, error) {
		return raw, nil
	})

	_, err := (&Engine{RunDir: runDir}).convertSpecsToArtifact(context.Background(), "run-123", bundle, specNormalizationConverterOptions{MaxOutputBytes: 8})
	if err == nil {
		t.Fatal("expected oversized output error")
	}
	for _, want := range []string{"converter output", "limit"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want %q", err.Error(), want)
		}
	}
	assertFileContains(t, runDir.SpecNormalizationConverterRaw(), raw)
}

func TestConvertSpecsToArtifactRejectsApprovalFields(t *testing.T) {
	runDir := newSpecNormalizationRunDir(t)
	bundle := newSpecNormalizationBundle(t, "Approval field case.")
	var artifact map[string]any
	if err := json.Unmarshal([]byte(specNormalizationArtifactJSON(t, "run-123", bundle.Manifest.Sources[0].Path, bundle.Manifest.Sources[0].Fingerprint)), &artifact); err != nil {
		t.Fatal(err)
	}
	artifact["approved_by"] = "user"
	data, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	stubAgentInvoke(t, func(ctx context.Context, backend *agentcli.CLIBackend, prompt string, timeoutSec int) (string, error) {
		return string(data), nil
	})

	_, err = (&Engine{RunDir: runDir}).convertSpecsToArtifact(context.Background(), "run-123", bundle, specNormalizationConverterOptions{MaxOutputBytes: 4096})
	if err == nil {
		t.Fatal("expected approval field rejection")
	}
	if !strings.Contains(err.Error(), "approval fields") {
		t.Fatalf("error = %q, want approval field context", err.Error())
	}
}

func TestValidateNormalizedArtifactCandidatePassesAndPersists(t *testing.T) {
	runDir := newSpecNormalizationRunDir(t)
	bundle := newSpecNormalizationBundle(t, "Users can import tasks.")
	artifact := baseSpecNormalizationArtifact("run-123", bundle.Manifest.Sources[0].Path, bundle.Manifest.Sources[0].Fingerprint)

	findings, err := (&Engine{RunDir: runDir}).validateNormalizedArtifactCandidate(&artifact, bundle.Manifest)
	if err != nil {
		t.Fatalf("validateNormalizedArtifactCandidate: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("findings = %+v, want none", findings)
	}
	persisted, err := runDir.ReadSpecNormalizationValidation()
	if err != nil {
		t.Fatalf("read persisted validation findings: %v", err)
	}
	if len(persisted) != 0 {
		t.Fatalf("persisted findings = %+v, want none", persisted)
	}
}

func TestValidateNormalizedArtifactCandidateFailureClasses(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(*state.RunArtifact, *state.SpecNormalizationSourceManifest)
		category string
	}{
		{
			name: "empty requirements",
			mutate: func(a *state.RunArtifact, m *state.SpecNormalizationSourceManifest) {
				a.Requirements = nil
			},
			category: "no_requirements",
		},
		{
			name: "missing requirement id",
			mutate: func(a *state.RunArtifact, m *state.SpecNormalizationSourceManifest) {
				a.Requirements[0].ID = ""
			},
			category: "missing_requirement_field",
		},
		{
			name: "missing requirement text",
			mutate: func(a *state.RunArtifact, m *state.SpecNormalizationSourceManifest) {
				a.Requirements[0].Text = ""
			},
			category: "missing_requirement_field",
		},
		{
			name: "missing source refs",
			mutate: func(a *state.RunArtifact, m *state.SpecNormalizationSourceManifest) {
				a.Requirements[0].SourceRefs = nil
			},
			category: "missing_requirement_field",
		},
		{
			name: "invalid requirement id",
			mutate: func(a *state.RunArtifact, m *state.SpecNormalizationSourceManifest) {
				a.Requirements[0].ID = "REQ-001"
			},
			category: "invalid_requirement_id",
		},
		{
			name: "duplicate requirement id",
			mutate: func(a *state.RunArtifact, m *state.SpecNormalizationSourceManifest) {
				a.Requirements = append(a.Requirements, a.Requirements[0])
			},
			category: "duplicate_requirement_id",
		},
		{
			name: "unknown source ref path",
			mutate: func(a *state.RunArtifact, m *state.SpecNormalizationSourceManifest) {
				a.Requirements[0].SourceRefs[0].Path = "missing.md"
			},
			category: "unknown_source_ref",
		},
		{
			name: "invalid source ref range",
			mutate: func(a *state.RunArtifact, m *state.SpecNormalizationSourceManifest) {
				a.Requirements[0].SourceRefs[0].LineEnd = 12
			},
			category: "invalid_source_ref_range",
		},
		{
			name: "excerpt mismatch",
			mutate: func(a *state.RunArtifact, m *state.SpecNormalizationSourceManifest) {
				a.Requirements[0].SourceRefs[0].Excerpt = "Unrelated text"
			},
			category: "source_excerpt_mismatch",
		},
		{
			name: "unknown dependency",
			mutate: func(a *state.RunArtifact, m *state.SpecNormalizationSourceManifest) {
				a.Dependencies = []string{"AT-FR-999"}
			},
			category: "unknown_dependency",
		},
		{
			name: "unnormalized boundaries",
			mutate: func(a *state.RunArtifact, m *state.SpecNormalizationSourceManifest) {
				a.Boundaries.Always = []string{"  Preserve source evidence for each requirement.  ", "Preserve source evidence for each requirement."}
			},
			category: "unnormalized_boundaries",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runDir := newSpecNormalizationRunDir(t)
			bundle := newSpecNormalizationBundle(t, "Users can import tasks.")
			artifact := baseSpecNormalizationArtifact("run-123", bundle.Manifest.Sources[0].Path, bundle.Manifest.Sources[0].Fingerprint)
			tt.mutate(&artifact, bundle.Manifest)

			findings, err := (&Engine{RunDir: runDir}).validateNormalizedArtifactCandidate(&artifact, bundle.Manifest)
			if err != nil {
				t.Fatalf("validateNormalizedArtifactCandidate: %v", err)
			}
			if !hasFindingCategory(findings, tt.category) {
				t.Fatalf("findings = %+v, want category %q", findings, tt.category)
			}
			persisted, err := runDir.ReadSpecNormalizationValidation()
			if err != nil {
				t.Fatalf("read persisted validation findings: %v", err)
			}
			if !hasFindingCategory(persisted, tt.category) {
				t.Fatalf("persisted findings = %+v, want category %q", persisted, tt.category)
			}
		})
	}
}

func TestVerifyNormalizedArtifactCandidatePassPersistsPromptRawAndReview(t *testing.T) {
	runDir := newSpecNormalizationRunDir(t)
	bundle := newSpecNormalizationBundle(t, "Users can import tasks.")
	artifact := baseSpecNormalizationArtifact("run-123", bundle.Manifest.Sources[0].Path, bundle.Manifest.Sources[0].Fingerprint)
	artifactHash := testNormalizedArtifactHash(t, &artifact)
	reviewedInputHash := testReviewedInputHash(t, artifactHash, bundle.ManifestHash)
	raw := specNormalizationReviewJSON(t, "run-123", state.ReviewPass, artifactHash, bundle.ManifestHash, reviewedInputHash, nil)

	var gotPrompt string
	stubAgentInvoke(t, func(ctx context.Context, backend *agentcli.CLIBackend, prompt string, timeoutSec int) (string, error) {
		gotPrompt = prompt
		return raw, nil
	})

	review, err := (&Engine{RunDir: runDir}).verifyNormalizedArtifactCandidate(context.Background(), "run-123", bundle, &artifact, specNormalizationVerifierOptions{
		BackendName:    agentcli.BackendClaude,
		TimeoutSeconds: 19,
		MaxOutputBytes: 4096,
	})
	if err != nil {
		t.Fatalf("verifyNormalizedArtifactCandidate: %v", err)
	}
	if review.Status != state.ReviewPass {
		t.Fatalf("review status = %q, want pass", review.Status)
	}
	for _, want := range []string{
		"independent verifier",
		"1 | Users can import tasks.",
		artifactHash,
		bundle.ManifestHash,
		`"requirements"`,
	} {
		if !strings.Contains(gotPrompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, gotPrompt)
		}
	}
	assertFileContains(t, runDir.SpecNormalizationVerifierPrompt(), artifactHash)
	assertFileContains(t, runDir.SpecNormalizationVerifierRaw(), raw)
	persisted, err := runDir.ReadSpecNormalizationReview()
	if err != nil {
		t.Fatalf("read persisted review: %v", err)
	}
	if persisted.ReviewedInputHash != reviewedInputHash {
		t.Fatalf("persisted reviewed input hash = %q, want %q", persisted.ReviewedInputHash, reviewedInputHash)
	}
}

func TestVerifyNormalizedArtifactCandidateStatusesAndFindings(t *testing.T) {
	for _, status := range []state.ReviewStatus{state.ReviewFail, state.ReviewNeedsRevision} {
		t.Run(string(status), func(t *testing.T) {
			runDir := newSpecNormalizationRunDir(t)
			bundle := newSpecNormalizationBundle(t, "Users can import tasks.")
			artifact := baseSpecNormalizationArtifact("run-123", bundle.Manifest.Sources[0].Path, bundle.Manifest.Sources[0].Fingerprint)
			artifactHash := testNormalizedArtifactHash(t, &artifact)
			reviewedInputHash := testReviewedInputHash(t, artifactHash, bundle.ManifestHash)
			findings := []state.ReviewFinding{
				{FindingID: "snr-001", Severity: "high", Category: "lost_information", Summary: "Lost scope."},
				{FindingID: "snr-002", Severity: "high", Category: "unsupported_addition", Summary: "Added scope."},
				{FindingID: "snr-003", Severity: "high", Category: "scope_change", Summary: "Changed scope."},
				{FindingID: "snr-004", Severity: "high", Category: "ambiguous_requirement", Summary: "Needs input."},
				{FindingID: "snr-005", Severity: "high", Category: "missing_source_evidence", Summary: "Missing evidence."},
				{FindingID: "snr-006", Severity: "high", Category: "non_goal_promoted", Summary: "Promoted non-goal."},
			}
			raw := specNormalizationReviewJSON(t, "run-123", status, artifactHash, bundle.ManifestHash, reviewedInputHash, findings)
			stubAgentInvoke(t, func(ctx context.Context, backend *agentcli.CLIBackend, prompt string, timeoutSec int) (string, error) {
				return raw, nil
			})

			review, err := (&Engine{RunDir: runDir}).verifyNormalizedArtifactCandidate(context.Background(), "run-123", bundle, &artifact, specNormalizationVerifierOptions{MaxOutputBytes: 4096})
			if err != nil {
				t.Fatalf("verifyNormalizedArtifactCandidate: %v", err)
			}
			if review.Status != status {
				t.Fatalf("review status = %q, want %q", review.Status, status)
			}
			for _, finding := range findings {
				if !hasFindingCategory(review.BlockingFindings, finding.Category) {
					t.Fatalf("review findings = %+v, want category %q", review.BlockingFindings, finding.Category)
				}
			}
		})
	}
}

func TestVerifyNormalizedArtifactCandidateMalformedReviewJSON(t *testing.T) {
	runDir := newSpecNormalizationRunDir(t)
	bundle := newSpecNormalizationBundle(t, "Users can import tasks.")
	artifact := baseSpecNormalizationArtifact("run-123", bundle.Manifest.Sources[0].Path, bundle.Manifest.Sources[0].Fingerprint)
	raw := `{"schema_version":`
	stubAgentInvoke(t, func(ctx context.Context, backend *agentcli.CLIBackend, prompt string, timeoutSec int) (string, error) {
		return raw, nil
	})

	_, err := (&Engine{RunDir: runDir}).verifyNormalizedArtifactCandidate(context.Background(), "run-123", bundle, &artifact, specNormalizationVerifierOptions{MaxOutputBytes: 4096})
	if err == nil {
		t.Fatal("expected malformed verifier JSON error")
	}
	if !strings.Contains(err.Error(), "JSON") {
		t.Fatalf("error = %q, want JSON context", err.Error())
	}
	assertFileContains(t, runDir.SpecNormalizationVerifierRaw(), raw)
}

func TestVerifyNormalizedArtifactCandidateRejectsHashMismatch(t *testing.T) {
	runDir := newSpecNormalizationRunDir(t)
	bundle := newSpecNormalizationBundle(t, "Users can import tasks.")
	artifact := baseSpecNormalizationArtifact("run-123", bundle.Manifest.Sources[0].Path, bundle.Manifest.Sources[0].Fingerprint)
	artifactHash := testNormalizedArtifactHash(t, &artifact)
	reviewedInputHash := testReviewedInputHash(t, artifactHash, bundle.ManifestHash)
	raw := specNormalizationReviewJSON(t, "run-123", state.ReviewPass, "sha256:wrong", bundle.ManifestHash, reviewedInputHash, nil)
	stubAgentInvoke(t, func(ctx context.Context, backend *agentcli.CLIBackend, prompt string, timeoutSec int) (string, error) {
		return raw, nil
	})

	_, err := (&Engine{RunDir: runDir}).verifyNormalizedArtifactCandidate(context.Background(), "run-123", bundle, &artifact, specNormalizationVerifierOptions{MaxOutputBytes: 4096})
	if err == nil {
		t.Fatal("expected hash mismatch")
	}
	if !strings.Contains(err.Error(), "normalized_artifact_hash") {
		t.Fatalf("error = %q, want normalized_artifact_hash context", err.Error())
	}
}

func TestVerifyNormalizedArtifactCandidateRejectsArtifactMutationOutput(t *testing.T) {
	runDir := newSpecNormalizationRunDir(t)
	bundle := newSpecNormalizationBundle(t, "Users can import tasks.")
	artifact := baseSpecNormalizationArtifact("run-123", bundle.Manifest.Sources[0].Path, bundle.Manifest.Sources[0].Fingerprint)
	artifactHash := testNormalizedArtifactHash(t, &artifact)
	reviewedInputHash := testReviewedInputHash(t, artifactHash, bundle.ManifestHash)
	var payload map[string]any
	if err := json.Unmarshal([]byte(specNormalizationReviewJSON(t, "run-123", state.ReviewPass, artifactHash, bundle.ManifestHash, reviewedInputHash, nil)), &payload); err != nil {
		t.Fatal(err)
	}
	payload["requirements"] = []map[string]string{{"id": "AT-FR-999", "text": "Mutated requirement."}}
	rawData, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	raw := string(rawData)
	stubAgentInvoke(t, func(ctx context.Context, backend *agentcli.CLIBackend, prompt string, timeoutSec int) (string, error) {
		return raw, nil
	})

	_, err = (&Engine{RunDir: runDir}).verifyNormalizedArtifactCandidate(context.Background(), "run-123", bundle, &artifact, specNormalizationVerifierOptions{MaxOutputBytes: 4096})
	if err == nil {
		t.Fatal("expected artifact mutation output rejection")
	}
	if !strings.Contains(err.Error(), "must not include artifact fields") {
		t.Fatalf("error = %q, want artifact mutation context", err.Error())
	}
}

func newSpecNormalizationRunDir(t *testing.T) *state.RunDir {
	t.Helper()

	runDir := state.NewRunDir(t.TempDir(), "run-123")
	if err := runDir.Init(); err != nil {
		t.Fatalf("init run dir: %v", err)
	}
	return runDir
}

func newSpecNormalizationBundle(t *testing.T, content string) *specNormalizationSourceBundle {
	t.Helper()

	dir := t.TempDir()
	spec := writeSpecInput(t, dir, "spec.md", content)
	bundle, err := buildSpecNormalizationSourceBundle("run-123", []string{spec}, 1024)
	if err != nil {
		t.Fatalf("build source bundle: %v", err)
	}
	return bundle
}

func stubAgentInvoke(t *testing.T, fn agentcli.InvokeFn) {
	t.Helper()

	old := agentcli.InvokeFunc
	agentcli.InvokeFunc = fn
	t.Cleanup(func() {
		agentcli.InvokeFunc = old
	})
}

func assertFileContains(t *testing.T, path, want string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !strings.Contains(string(data), want) {
		t.Fatalf("%s missing %q:\n%s", path, want, string(data))
	}
}

func specNormalizationArtifactJSON(t *testing.T, runID, sourcePath, fingerprint string) string {
	t.Helper()

	artifact := baseSpecNormalizationArtifact(runID, sourcePath, fingerprint)
	data, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func specNormalizationArtifactJSONWithText(t *testing.T, runID, sourcePath, fingerprint, text string) string {
	t.Helper()

	artifact := baseSpecNormalizationArtifact(runID, sourcePath, fingerprint)
	artifact.Requirements[0].Text = text
	artifact.Requirements[0].SourceRefs[0].Excerpt = text
	data, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func baseSpecNormalizationArtifact(runID, sourcePath, fingerprint string) state.RunArtifact {
	return state.RunArtifact{
		SchemaVersion: "0.1",
		RunID:         runID,
		SourceSpecs: []state.SourceSpec{{
			Path:        sourcePath,
			Fingerprint: fingerprint,
		}},
		Requirements: []state.Requirement{{
			ID:         "AT-FR-001",
			Text:       "Users can import tasks.",
			SourceSpec: sourcePath,
			SourceRefs: []state.SourceRef{{
				Path:      sourcePath,
				LineStart: 1,
				LineEnd:   1,
				Excerpt:   "Users can import tasks.",
			}},
			Confidence: "high",
		}},
		Assumptions:    []string{"Existing task files remain valid."},
		Clarifications: []state.Clarification{{QuestionID: "Q-001", Text: "Which import format is highest priority?", Status: "pending"}},
		Dependencies:   []string{"AT-FR-001"},
		RiskProfile:    "standard",
		Boundaries: state.Boundaries{
			Always:   []string{"Preserve source evidence for each requirement."},
			AskFirst: []string{"Ask before dropping ambiguous scope."},
			Never:    []string{"Do not implement explicitly out-of-scope items."},
		},
		RoutingPolicy: state.RoutingPolicy{DefaultImplementer: "claude-sonnet"},
	}
}

func hasFindingCategory(findings []state.ReviewFinding, category string) bool {
	for _, finding := range findings {
		if finding.Category == category {
			return true
		}
	}
	return false
}

func specNormalizationReviewJSON(t *testing.T, runID string, status state.ReviewStatus, artifactHash, manifestHash, reviewedInputHash string, findings []state.ReviewFinding) string {
	t.Helper()

	review := state.SpecNormalizationReview{
		SchemaVersion:          "0.1",
		RunID:                  runID,
		ArtifactType:           state.ArtifactTypeSpecNormalizationReview,
		Status:                 status,
		Summary:                "Verifier summary.",
		NormalizedArtifactHash: artifactHash,
		SourceManifestHash:     manifestHash,
		ReviewedInputHash:      reviewedInputHash,
		ReviewedAt:             time.Unix(1, 0).UTC(),
		BlockingFindings:       findings,
	}
	data, err := json.Marshal(review)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func testNormalizedArtifactHash(t *testing.T, artifact *state.RunArtifact) string {
	t.Helper()

	data, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	return "sha256:" + state.SHA256Bytes(data)
}

func testReviewedInputHash(t *testing.T, artifactHash, manifestHash string) string {
	t.Helper()

	data, err := json.Marshal(struct {
		NormalizedArtifactHash string `json:"normalized_artifact_hash"`
		SourceManifestHash     string `json:"source_manifest_hash"`
	}{
		NormalizedArtifactHash: artifactHash,
		SourceManifestHash:     manifestHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	return "sha256:" + state.SHA256Bytes(data)
}
