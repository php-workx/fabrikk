package engine

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/php-workx/fabrikk/internal/state"
)

// RunArtifactApprovalOptions controls exceptional normalized artifact approvals.
type RunArtifactApprovalOptions struct {
	AcceptNeedsRevision bool
}

type runArtifactApprovalInputs struct {
	Candidate             *state.RunArtifact
	Review                *state.SpecNormalizationReview
	ArtifactHash          string
	ManifestHash          string
	ReviewedInputHash     string
	AcceptedNeedsRevision bool
}

// ApproveRunArtifact records explicit approval for a normalized run artifact candidate.
// This is separate from Approve, which remains the final execution-plan approval.
func (e *Engine) ApproveRunArtifact(ctx context.Context, approvedBy string) (*state.RunArtifactApproval, error) {
	return e.ApproveRunArtifactWithOptions(ctx, approvedBy, RunArtifactApprovalOptions{})
}

// ApproveRunArtifactWithOptions records explicit approval for a normalized run artifact candidate.
func (e *Engine) ApproveRunArtifactWithOptions(ctx context.Context, approvedBy string, opts RunArtifactApprovalOptions) (*state.RunArtifactApproval, error) {
	_ = ctx

	if e == nil || e.RunDir == nil {
		return nil, fmt.Errorf("approve run artifact: missing run directory")
	}
	runID := filepathBase(e.RunDir.Root)
	inputs, err := e.loadRunArtifactApprovalInputs(opts)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	approval := &state.RunArtifactApproval{
		SchemaVersion:          "0.1",
		RunID:                  runID,
		ArtifactType:           state.ArtifactTypeRunArtifactApproval,
		NormalizedArtifactHash: inputs.ArtifactHash,
		SourceManifestHash:     inputs.ManifestHash,
		ReviewedInputHash:      inputs.ReviewedInputHash,
		ReviewStatus:           inputs.Review.Status,
		AcceptedNeedsRevision:  inputs.AcceptedNeedsRevision,
		ApprovedBy:             approvedBy,
		ApprovedAt:             now,
	}
	if err := e.RunDir.WriteRunArtifactApproval(approval); err != nil {
		return nil, fmt.Errorf("write run artifact approval: %w", err)
	}

	inputs.Candidate.Normalization.ApprovedAt = &now
	inputs.Candidate.Normalization.ReviewedAt = &inputs.Review.ReviewedAt
	inputs.Candidate.Normalization.ReviewedInputHash = inputs.ReviewedInputHash
	if err := e.RunDir.WriteArtifact(inputs.Candidate); err != nil {
		return nil, fmt.Errorf("write approved normalized run artifact: %w", err)
	}
	if err := e.writePrepareStatus(runID, state.RunAwaitingApproval, "technical_spec", nil, "draft or review the technical specification"); err != nil {
		return nil, err
	}

	_ = e.RunDir.AppendEvent(state.Event{
		Timestamp: now,
		Type:      "run_artifact_approved",
		RunID:     runID,
		Detail:    inputs.ReviewedInputHash,
	})

	return approval, nil
}

func (e *Engine) loadRunArtifactApprovalInputs(opts RunArtifactApprovalOptions) (*runArtifactApprovalInputs, error) {
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
	if err := validateRunArtifactReviewStatus(review.Status, opts); err != nil {
		return nil, err
	}
	if err := validateSpecNormalizationSourcesCurrent(manifest); err != nil {
		return nil, err
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

	converterPromptHash, err := hashSpecNormalizationPromptFile(e.RunDir.SpecNormalizationConverterPrompt())
	if err != nil {
		return nil, err
	}
	verifierPromptHash, err := hashSpecNormalizationPromptFile(e.RunDir.SpecNormalizationVerifierPrompt())
	if err != nil {
		return nil, err
	}
	if review.ConverterPromptHash != "" && review.ConverterPromptHash != converterPromptHash {
		return nil, fmt.Errorf("normalization review converter_prompt_hash = %q, current converter_prompt_hash = %q", review.ConverterPromptHash, converterPromptHash)
	}
	if review.VerifierPromptHash != "" && review.VerifierPromptHash != verifierPromptHash {
		return nil, fmt.Errorf("normalization review verifier_prompt_hash = %q, current verifier_prompt_hash = %q", review.VerifierPromptHash, verifierPromptHash)
	}
	reviewedInputHash, err := hashSpecNormalizationReviewedInput(state.SpecNormalizationReviewedInput{
		SourceManifestHash:     manifestHash,
		NormalizedArtifactHash: artifactHash,
		ConverterPromptHash:    converterPromptHash,
		VerifierPromptHash:     verifierPromptHash,
	})
	if err != nil {
		return nil, err
	}
	if review.ReviewedInputHash != reviewedInputHash {
		return nil, fmt.Errorf("normalization review reviewed_input_hash = %q, current reviewed_input_hash = %q", review.ReviewedInputHash, reviewedInputHash)
	}

	return &runArtifactApprovalInputs{
		Candidate:             candidate,
		Review:                review,
		ArtifactHash:          artifactHash,
		ManifestHash:          manifestHash,
		ReviewedInputHash:     reviewedInputHash,
		AcceptedNeedsRevision: review.Status == state.ReviewNeedsRevision,
	}, nil
}

func validateRunArtifactReviewStatus(status state.ReviewStatus, opts RunArtifactApprovalOptions) error {
	switch status {
	case state.ReviewPass:
		return nil
	case state.ReviewNeedsRevision:
		if opts.AcceptNeedsRevision {
			return nil
		}
		return fmt.Errorf("normalization review needs revision; revise the source spec or rerun artifact approval with --accept-needs-revision to approve intentionally")
	case state.ReviewFail:
		return fmt.Errorf("normalization review is not passing")
	default:
		return fmt.Errorf("normalization review status %q is not approvable", status)
	}
}

func hashSpecNormalizationPromptFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read spec normalization prompt %s: %w", path, err)
	}
	return hashSpecNormalizationPromptText(string(data)), nil
}

func validateSpecNormalizationSourcesCurrent(manifest *state.SpecNormalizationSourceManifest) error {
	if manifest == nil {
		return fmt.Errorf("source changed: missing source manifest")
	}
	for _, source := range manifest.Sources {
		snapshot, err := readSpecNormalizationSourceSnapshot(source.Path)
		if err != nil {
			return fmt.Errorf("source changed: read %s: %w", source.Path, err)
		}
		if snapshot.Fingerprint != source.Fingerprint {
			return fmt.Errorf("source changed: %s fingerprint = %q, reviewed fingerprint = %q", source.Path, snapshot.Fingerprint, source.Fingerprint)
		}
		if snapshot.ByteSize != source.ByteSize {
			return fmt.Errorf("source changed: %s byte_size = %d, reviewed byte_size = %d", source.Path, snapshot.ByteSize, source.ByteSize)
		}
		if snapshot.LineCount != source.LineCount {
			return fmt.Errorf("source changed: %s line_count = %d, reviewed line_count = %d", source.Path, snapshot.LineCount, source.LineCount)
		}
		if source.LineNumberedText != "" && snapshot.LineNumberedText != source.LineNumberedText {
			return fmt.Errorf("source changed: %s line-numbered content differs from reviewed source manifest", source.Path)
		}
	}
	return nil
}
