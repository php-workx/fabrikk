package engine

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/php-workx/fabrikk/internal/state"
)

func TestApproveRunArtifactSuccessWritesApprovalAndMetadata(t *testing.T) {
	eng := preparePassingLLMRun(t)

	approval, err := eng.ApproveRunArtifact(context.Background(), "user")
	if err != nil {
		t.Fatalf("ApproveRunArtifact: %v", err)
	}
	review, err := eng.RunDir.ReadSpecNormalizationReview()
	if err != nil {
		t.Fatalf("read review: %v", err)
	}
	if approval.ArtifactType != state.ArtifactTypeRunArtifactApproval {
		t.Fatalf("artifact type = %q, want run artifact approval", approval.ArtifactType)
	}
	if approval.NormalizedArtifactHash != review.NormalizedArtifactHash ||
		approval.SourceManifestHash != review.SourceManifestHash ||
		approval.ReviewedInputHash != review.ReviewedInputHash {
		t.Fatalf("approval hashes = %+v, review = %+v", approval, review)
	}
	if approval.ApprovedAt.IsZero() {
		t.Fatal("approval timestamp is zero")
	}

	persisted, err := eng.RunDir.ReadRunArtifactApproval()
	if err != nil {
		t.Fatalf("read run artifact approval: %v", err)
	}
	if persisted.ReviewedInputHash != review.ReviewedInputHash {
		t.Fatalf("persisted approval = %+v, want reviewed input hash %q", persisted, review.ReviewedInputHash)
	}
	artifact, err := eng.RunDir.ReadArtifact()
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	if artifact.Normalization.ApprovedAt == nil || artifact.Normalization.ReviewedInputHash != review.ReviewedInputHash {
		t.Fatalf("artifact normalization metadata = %+v", artifact.Normalization)
	}
	status, err := eng.RunDir.ReadStatus()
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status.CurrentGate != "technical_spec" {
		t.Fatalf("status = %+v, want technical_spec gate", status)
	}
}

func TestApproveRunArtifactRejectsMissingReview(t *testing.T) {
	eng := preparePassingLLMRun(t)
	if err := os.Remove(eng.RunDir.SpecNormalizationReview()); err != nil {
		t.Fatalf("remove review: %v", err)
	}

	_, err := eng.ApproveRunArtifact(context.Background(), "user")
	if err == nil {
		t.Fatal("expected missing review error")
	}
	if !strings.Contains(err.Error(), "normalization review") {
		t.Fatalf("error = %q, want normalization review context", err.Error())
	}
}

func TestApproveRunArtifactRejectsFailingReview(t *testing.T) {
	eng := prepareLLMRunWithReviewStatus(t, state.ReviewFail)

	_, err := eng.ApproveRunArtifact(context.Background(), "user")
	if err == nil {
		t.Fatal("expected failing review rejection")
	}
	if !strings.Contains(err.Error(), "not passing") {
		t.Fatalf("error = %q, want not passing context", err.Error())
	}
}

func TestApproveRunArtifactRejectsStaleSourceManifest(t *testing.T) {
	eng := preparePassingLLMRun(t)
	manifest, err := eng.RunDir.ReadSpecNormalizationSourceManifest()
	if err != nil {
		t.Fatalf("read source manifest: %v", err)
	}
	manifest.Sources[0].LineNumberedText = "1 | Changed source"
	if err := eng.RunDir.WriteSpecNormalizationSourceManifest(manifest); err != nil {
		t.Fatalf("write changed source manifest: %v", err)
	}

	_, err = eng.ApproveRunArtifact(context.Background(), "user")
	if err == nil {
		t.Fatal("expected stale source manifest rejection")
	}
	if !strings.Contains(err.Error(), "source_manifest_hash") {
		t.Fatalf("error = %q, want source_manifest_hash context", err.Error())
	}
}

func TestApproveRunArtifactRejectsChangedCandidate(t *testing.T) {
	eng := preparePassingLLMRun(t)
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
		t.Fatal("expected changed candidate rejection")
	}
	if !strings.Contains(err.Error(), "normalized_artifact_hash") {
		t.Fatalf("error = %q, want normalized_artifact_hash context", err.Error())
	}
}

func TestFinalApproveSemanticsUnchanged(t *testing.T) {
	dir := t.TempDir()
	specPath := writeSpecInput(t, dir, "spec.md", "- **AT-FR-001**: The system must import tasks.\n")
	eng := New(state.NewRunDir(dir, "placeholder"), dir)
	if _, err := eng.Prepare(context.Background(), []string{specPath}); err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	err := eng.Approve(context.Background())
	if err == nil {
		t.Fatal("expected final Approve to still require an approved execution plan")
	}
	if !strings.Contains(err.Error(), "approved execution plan required") {
		t.Fatalf("error = %q, want existing execution plan approval requirement", err.Error())
	}
}

func preparePassingLLMRun(t *testing.T) *Engine {
	t.Helper()
	return prepareLLMRunWithReviewStatus(t, state.ReviewPass)
}

func prepareLLMRunWithReviewStatus(t *testing.T, reviewStatus state.ReviewStatus) *Engine {
	t.Helper()

	dir := t.TempDir()
	specPath := writeSpecInput(t, dir, "spec.md", "The system should import tasks from Markdown sentences.\n")
	eng := New(state.NewRunDir(dir, "placeholder"), dir)
	stubSpecNormalizationLLMWithReviewStatus(t, reviewStatus)
	if _, err := eng.PrepareWithOptions(context.Background(), []string{specPath}, llmPrepareOptionsForTest()); err != nil {
		t.Fatalf("PrepareWithOptions: %v", err)
	}
	return eng
}
