package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/php-workx/fabrikk/internal/state"
)

// ApproveRunArtifact records explicit approval for a normalized run artifact candidate.
// This is separate from Approve, which remains the final execution-plan approval.
func (e *Engine) ApproveRunArtifact(ctx context.Context, approvedBy string) (*state.RunArtifactApproval, error) {
	_ = ctx

	if e == nil || e.RunDir == nil {
		return nil, fmt.Errorf("approve run artifact: missing run directory")
	}
	runID := filepathBase(e.RunDir.Root)

	candidate, err := e.RunDir.ReadNormalizedArtifactCandidate()
	if err != nil {
		return nil, fmt.Errorf("read normalized artifact candidate: %w", err)
	}
	manifest, err := e.RunDir.ReadSpecNormalizationSourceManifest()
	if err != nil {
		return nil, fmt.Errorf("read source manifest: %w", err)
	}
	review, err := e.RunDir.ReadSpecNormalizationReview()
	if err != nil {
		return nil, fmt.Errorf("read normalization review: %w", err)
	}
	if review.Status != state.ReviewPass {
		return nil, fmt.Errorf("normalization review is not passing")
	}

	artifactHash, err := hashNormalizedArtifactCandidate(candidate)
	if err != nil {
		return nil, err
	}
	if review.NormalizedArtifactHash != artifactHash {
		return nil, fmt.Errorf("normalization review normalized_artifact_hash = %q, current normalized_artifact_hash = %q", review.NormalizedArtifactHash, artifactHash)
	}

	manifestHash, err := hashSpecNormalizationSourceManifest(manifest)
	if err != nil {
		return nil, err
	}
	if review.SourceManifestHash != manifestHash {
		return nil, fmt.Errorf("normalization review source_manifest_hash = %q, current source_manifest_hash = %q", review.SourceManifestHash, manifestHash)
	}

	reviewedInputHash, err := hashSpecNormalizationReviewedInput(artifactHash, manifestHash)
	if err != nil {
		return nil, err
	}
	if review.ReviewedInputHash != reviewedInputHash {
		return nil, fmt.Errorf("normalization review reviewed_input_hash = %q, current reviewed_input_hash = %q", review.ReviewedInputHash, reviewedInputHash)
	}

	now := time.Now()
	approval := &state.RunArtifactApproval{
		SchemaVersion:          "0.1",
		RunID:                  runID,
		ArtifactType:           state.ArtifactTypeRunArtifactApproval,
		NormalizedArtifactHash: artifactHash,
		SourceManifestHash:     manifestHash,
		ReviewedInputHash:      reviewedInputHash,
		ApprovedAt:             now,
	}
	if err := e.RunDir.WriteRunArtifactApproval(approval); err != nil {
		return nil, fmt.Errorf("write run artifact approval: %w", err)
	}

	candidate.Normalization.ApprovedAt = &now
	candidate.Normalization.ReviewedInputHash = reviewedInputHash
	if err := e.RunDir.WriteArtifact(candidate); err != nil {
		return nil, fmt.Errorf("write approved normalized run artifact: %w", err)
	}
	if err := e.writePrepareStatus(runID, state.RunAwaitingApproval, "technical_spec", nil, "draft or review the technical specification"); err != nil {
		return nil, err
	}

	_ = e.RunDir.AppendEvent(state.Event{
		Timestamp: now,
		Type:      "run_artifact_approved",
		RunID:     runID,
		Detail:    reviewedInputHash,
	})

	return approval, nil
}
