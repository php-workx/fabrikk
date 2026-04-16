package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/php-workx/fabrikk/internal/agentcli"
	"github.com/php-workx/fabrikk/internal/state"
)

func TestPrepareNormalizationSmokePassAndVerifierOutcomes(t *testing.T) {
	for _, status := range []state.ReviewStatus{state.ReviewPass, state.ReviewFail, state.ReviewNeedsRevision} {
		t.Run(string(status), func(t *testing.T) {
			dir := t.TempDir()
			eng := New(state.NewRunDir(dir, "placeholder"), dir)
			stubSpecNormalizationLLMWithReviewStatus(t, status)

			result, err := eng.PrepareWithOptions(context.Background(), []string{fixtureSpecPath("prose.md")}, llmPrepareOptionsForTest())
			if err != nil {
				t.Fatalf("PrepareWithOptions: %v", err)
			}
			if result.NormalizationReview == nil || result.NormalizationReview.Status != status {
				t.Fatalf("review = %+v, want %s", result.NormalizationReview, status)
			}
			switch status {
			case state.ReviewPass:
				if result.Status != PrepareStatusAwaitingArtifactApproval {
					t.Fatalf("result status = %q, want awaiting artifact approval", result.Status)
				}
			case state.ReviewFail:
				if result.Status != PrepareStatusBlocked {
					t.Fatalf("result status = %q, want blocked", result.Status)
				}
			case state.ReviewNeedsRevision:
				if result.Status != PrepareStatusAwaitingUserInput {
					t.Fatalf("result status = %q, want awaiting user input", result.Status)
				}
			}
		})
	}
}

func TestPrepareNormalizationSmokeRejectsMissingSourceEvidence(t *testing.T) {
	dir := t.TempDir()
	eng := New(state.NewRunDir(dir, "placeholder"), dir)
	stubConverterMissingSourceRefs(t)

	_, err := eng.PrepareWithOptions(context.Background(), []string{fixtureSpecPath("prose.md")}, llmPrepareOptionsForTest())
	if err == nil {
		t.Fatal("expected missing source_refs error")
	}
	if !strings.Contains(err.Error(), "source_refs") {
		t.Fatalf("error = %q, want source_refs context", err.Error())
	}
}

func TestPrepareNormalizationSmokeVerifierUnsupportedAddition(t *testing.T) {
	dir := t.TempDir()
	eng := New(state.NewRunDir(dir, "placeholder"), dir)
	stubVerifierFindingCategory(t, state.ReviewFail, "unsupported_addition")

	result, err := eng.PrepareWithOptions(context.Background(), []string{fixtureSpecPath("ambiguous-and-unsupported.md")}, llmPrepareOptionsForTest())
	if err != nil {
		t.Fatalf("PrepareWithOptions: %v", err)
	}
	if result.Status != PrepareStatusBlocked {
		t.Fatalf("result status = %q, want blocked", result.Status)
	}
	if !hasFindingCategory(result.NormalizationReview.BlockingFindings, "unsupported_addition") {
		t.Fatalf("review findings = %+v, want unsupported_addition", result.NormalizationReview.BlockingFindings)
	}
}

func TestPrepareNormalizationSmokeOversizedInputSkipsAgent(t *testing.T) {
	dir := t.TempDir()
	eng := New(state.NewRunDir(dir, "placeholder"), dir)
	opts := llmPrepareOptionsForTest()
	opts.MaxInputBytes = 8
	stubAgentInvoke(t, func(ctx context.Context, backend *agentcli.CLIBackend, prompt string, timeoutSec int) (string, error) {
		t.Fatalf("agent should not be invoked for oversized input")
		return "", nil
	})

	_, err := eng.PrepareWithOptions(context.Background(), []string{fixtureSpecPath("prose.md")}, opts)
	if err == nil {
		t.Fatal("expected oversized input error")
	}
	if !strings.Contains(err.Error(), "source bundle") {
		t.Fatalf("error = %q, want source bundle context", err.Error())
	}
}

func TestPrepareNormalizationSmokeApprovalRejectsStaleHashes(t *testing.T) {
	dir := t.TempDir()
	eng := New(state.NewRunDir(dir, "placeholder"), dir)
	stubSpecNormalizationLLMWithReviewStatus(t, state.ReviewPass)
	if _, err := eng.PrepareWithOptions(context.Background(), []string{fixtureSpecPath("prose.md")}, llmPrepareOptionsForTest()); err != nil {
		t.Fatalf("PrepareWithOptions: %v", err)
	}

	candidate, err := eng.RunDir.ReadNormalizedArtifactCandidate()
	if err != nil {
		t.Fatalf("read candidate: %v", err)
	}
	candidate.Requirements[0].Text = "Changed requirement."
	if err := eng.RunDir.WriteNormalizedArtifactCandidate(candidate); err != nil {
		t.Fatalf("write changed candidate: %v", err)
	}

	_, err = eng.ApproveRunArtifact(context.Background(), "user")
	if err == nil {
		t.Fatal("expected stale candidate hash error")
	}
	if !strings.Contains(err.Error(), "normalized_artifact_hash") {
		t.Fatalf("error = %q, want normalized artifact hash context", err.Error())
	}
}

func fixtureSpecPath(name string) string {
	return "testdata/spec_normalization/" + name
}

func stubConverterMissingSourceRefs(t *testing.T) {
	t.Helper()

	stubAgentInvoke(t, func(ctx context.Context, backend *agentcli.CLIBackend, prompt string, timeoutSec int) (string, error) {
		if !strings.Contains(prompt, "# spec normalization converter") {
			t.Fatalf("unexpected verifier call after invalid converter output:\n%s", prompt)
		}
		runID := promptLineValue(prompt, "Run ID: ")
		sourcePath := promptLineValue(prompt, "## Source: ")
		fingerprint := promptLineValue(prompt, "Fingerprint: ")
		var artifact state.RunArtifact
		if err := json.Unmarshal([]byte(specNormalizationArtifactJSONWithText(t, runID, sourcePath, fingerprint, "The importer should read Markdown task files and preserve their title, body, and current status.")), &artifact); err != nil {
			t.Fatal(err)
		}
		artifact.Requirements[0].SourceRefs = nil
		data, err := json.Marshal(artifact)
		if err != nil {
			t.Fatal(err)
		}
		return string(data), nil
	})
}

func stubVerifierFindingCategory(t *testing.T, status state.ReviewStatus, category string) {
	t.Helper()

	stubAgentInvoke(t, func(ctx context.Context, backend *agentcli.CLIBackend, prompt string, timeoutSec int) (string, error) {
		runID := promptLineValue(prompt, "Run ID: ")
		switch {
		case strings.Contains(prompt, "# spec normalization converter"):
			sourcePath := promptLineValue(prompt, "## Source: ")
			fingerprint := promptLineValue(prompt, "Fingerprint: ")
			sourceText := promptLineValue(prompt, "1 | ")
			return specNormalizationArtifactJSONWithText(t, runID, sourcePath, fingerprint, sourceText), nil
		case strings.Contains(prompt, "# spec normalization verifier"):
			findings := []state.ReviewFinding{{
				FindingID:       "snr-001",
				Severity:        "high",
				Category:        category,
				Summary:         "Verifier found unsupported scope.",
				SuggestedRepair: "Remove unsupported scope.",
			}}
			return specNormalizationReviewJSON(t, runID, status, promptLineValue(prompt, "Normalized artifact hash: "), promptLineValue(prompt, "Source manifest hash: "), promptLineValue(prompt, "Reviewed input hash: "), findings), nil
		default:
			t.Fatalf("unexpected prompt:\n%s", prompt)
			return "", nil
		}
	})
}
