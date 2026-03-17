// Package councilflow owns the council review pipeline for technical specs.
package councilflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// CouncilConfig controls the council review pipeline.
type CouncilConfig struct {
	Rounds          int        // number of review rounds (default: 2)
	Mode            ReviewMode // review tone and strictness (default: standard)
	SpecPath        string     // absolute path to spec file (reviewers read from file instead of inline)
	CodebaseContext string     // optional codebase context for reviewers
	DryRun          bool       // generate prompts without executing
	Force           bool       // re-run all reviewers even if cached results exist
	SkipJudge       bool       // skip judge consolidation (review-only mode)
	SkipDynPersonas bool       // skip dynamic persona generation (fixed personas only)
	SkipApproval    bool       // skip interactive persona approval
	StaggerDelay    int        // seconds between launching parallel reviewers/judges (default: 15)
}

// DefaultConfig returns a config with sensible defaults.
func DefaultConfig() CouncilConfig {
	return CouncilConfig{
		Rounds:       2,
		StaggerDelay: 15, // seconds between parallel launches to avoid rate limits
	}
}

// StageTiming records the duration of one pipeline stage.
type StageTiming struct {
	Stage    string        `json:"stage"`
	Duration time.Duration `json:"duration_ms"`
}

// CouncilResult holds the full pipeline output across all rounds.
type CouncilResult struct {
	Rounds         []RoundResult         `json:"rounds"`
	Consolidations []ConsolidationResult `json:"consolidations,omitempty"`
	OverallVerdict Verdict               `json:"overall_verdict"`
	FinalSpecPath  string                `json:"final_spec_path,omitempty"`
	FinalSpec      string                `json:"-"`
	Timings        []StageTiming         `json:"timings,omitempty"`
	TotalDuration  time.Duration         `json:"total_duration_ms"`
}

// RunCouncil executes the full council review pipeline for a technical spec.
func RunCouncil(ctx context.Context, spec, outputBaseDir string, cfg CouncilConfig) (*CouncilResult, error) {
	if cfg.Rounds < 1 {
		cfg.Rounds = 2
	}

	pipelineStart := time.Now()
	result := &CouncilResult{}
	var priorFindings []ReviewOutput
	currentSpec := spec

	for round := 1; round <= cfg.Rounds; round++ {
		fmt.Printf("\n=== Council Review Round %d/%d ===\n", round, cfg.Rounds)

		roundDir := filepath.Join(outputBaseDir, fmt.Sprintf("round-%d", round))

		updatedSpec, err := executeRound(ctx, round, roundDir, currentSpec, priorFindings, cfg, result)
		if err != nil {
			return nil, err
		}

		if len(result.Rounds) > 0 {
			latest := &result.Rounds[len(result.Rounds)-1]
			priorFindings = append(priorFindings, latest.Reviews...)
		}
		if updatedSpec != "" {
			currentSpec = updatedSpec
		}
	}

	// Overall verdict.
	if len(result.Rounds) > 0 {
		result.OverallVerdict = result.Rounds[len(result.Rounds)-1].Consensus
	}

	// Total timing.
	result.TotalDuration = time.Since(pipelineStart)

	// Print summary.
	printFinalSummary(result)

	return result, nil
}

func executeRound(ctx context.Context, round int, roundDir, spec string, priorFindings []ReviewOutput, cfg CouncilConfig, result *CouncilResult) (updatedSpec string, err error) {
	// Stage: persona generation.
	t0 := time.Now()
	personas, err := buildPersonaSet(ctx, spec, roundDir, cfg)
	if err != nil {
		return "", fmt.Errorf("round %d: %w", round, err)
	}
	personaDur := time.Since(t0)
	result.Timings = append(result.Timings, StageTiming{
		Stage: fmt.Sprintf("round-%d/personas", round), Duration: personaDur,
	})
	fmt.Printf("  [timing] persona generation: %s\n", personaDur.Round(time.Millisecond))

	// Stage: persona approval.
	t0 = time.Now()
	personas, err = maybeApprovePersonas(personas, cfg)
	if err != nil {
		return "", fmt.Errorf("round %d: %w", round, err)
	}
	approvalDur := time.Since(t0)
	if approvalDur > time.Second {
		result.Timings = append(result.Timings, StageTiming{
			Stage: fmt.Sprintf("round-%d/approval", round), Duration: approvalDur,
		})
	}

	runner := NewRunner(roundDir)
	runner.Force = cfg.Force
	runner.Mode = cfg.Mode
	runner.SpecPath = cfg.SpecPath
	runner.StaggerSec = cfg.StaggerDelay

	if cfg.DryRun {
		return "", writeDryRunPrompts(roundDir, spec, round, personas, priorFindings, cfg.CodebaseContext)
	}

	// Stage: reviews.
	t0 = time.Now()
	roundResult, err := runner.RunRound(ctx, spec, round, personas, priorFindings, cfg.CodebaseContext)
	if err != nil {
		return "", fmt.Errorf("round %d reviews: %w", round, err)
	}
	reviewDur := time.Since(t0)
	result.Timings = append(result.Timings, StageTiming{
		Stage: fmt.Sprintf("round-%d/reviews", round), Duration: reviewDur,
	})

	if len(roundResult.Reviews) == 0 {
		return "", fmt.Errorf("round %d: %w: all reviewers failed", round, ErrEmptyReview)
	}

	result.Rounds = append(result.Rounds, *roundResult)

	totalFindings := 0
	for i := range roundResult.Reviews {
		totalFindings += len(roundResult.Reviews[i].Findings)
	}
	fmt.Printf("  [timing] reviews: %s (%d findings from %d reviewers)\n",
		reviewDur.Round(time.Millisecond), totalFindings, len(roundResult.Reviews))

	// Stage: judge consolidation.
	if cfg.SkipJudge || totalFindings == 0 {
		return "", nil
	}

	t0 = time.Now()
	judgeCfg := DefaultJudgeConfig()
	judgeCfg.Mode = cfg.Mode
	judgeCfg.StaggerSec = cfg.StaggerDelay
	consolidation, judgeErr := RunJudge(ctx, spec, round, roundResult.Reviews, roundDir, judgeCfg)
	if judgeErr != nil {
		return "", fmt.Errorf("round %d judge: %w", round, judgeErr)
	}
	judgeDur := time.Since(t0)
	result.Timings = append(result.Timings, StageTiming{
		Stage: fmt.Sprintf("round-%d/judge", round), Duration: judgeDur,
	})
	fmt.Printf("  [timing] judge: %s (%d applied, %d rejected, %d failed)\n",
		judgeDur.Round(time.Millisecond), consolidation.AppliedCount, consolidation.RejectedCount, len(consolidation.FailedEdits))

	// Drift detection.
	consolidation.DriftWarnings = DetectDrift(spec, consolidation.UpdatedSpec)
	if len(consolidation.DriftWarnings) > 0 {
		fmt.Printf("  [round %d] drift warnings:\n", round)
		for _, dw := range consolidation.DriftWarnings {
			fmt.Printf("    - %s\n", dw)
		}
	}

	result.Consolidations = append(result.Consolidations, *consolidation)

	// Write versioned spec.
	specPath := filepath.Join(filepath.Dir(roundDir), fmt.Sprintf("technical-spec-v%d.md", round))
	if writeErr := os.WriteFile(specPath, []byte(consolidation.UpdatedSpec), 0o644); writeErr != nil {
		return "", fmt.Errorf("write spec v%d: %w", round, writeErr)
	}
	result.FinalSpecPath = specPath
	result.FinalSpec = consolidation.UpdatedSpec

	return consolidation.UpdatedSpec, nil
}

func printFinalSummary(result *CouncilResult) {
	fmt.Printf("\n=== Review Summary ===\n\n")

	// Output files.
	if result.FinalSpecPath != "" {
		fmt.Printf("  Updated spec: %s\n", result.FinalSpecPath)
	}

	// Changelog — what was changed and why.
	totalApplied := 0
	totalRejected := 0
	for i := range result.Consolidations {
		c := &result.Consolidations[i]
		totalApplied += c.AppliedCount
		totalRejected += c.RejectedCount

		if len(c.AppliedEdits) > 0 {
			fmt.Printf("\n  Changes applied (round %d):\n", c.Round)
			for j := range c.AppliedEdits {
				edit := &c.AppliedEdits[j]
				section := edit.Section
				if section == "" {
					section = "(unspecified)"
				}
				fmt.Printf("    %d. [%s] %s — %s\n", j+1, edit.FindingID, section, edit.Action)
			}
		}
		if c.RejectedCount > 0 {
			fmt.Printf("\n  Rejected findings (round %d): %d\n", c.Round, c.RejectedCount)
			for j := range c.RejectionLog.Rejections {
				r := &c.RejectionLog.Rejections[j]
				fmt.Printf("    - [%s] %s\n", r.FindingID, r.RejectionReason)
			}
		}
		if len(c.FailedEdits) > 0 {
			fmt.Printf("\n  Failed edits (round %d): %d\n", c.Round, len(c.FailedEdits))
		}
	}

	// Totals.
	fmt.Printf("\n  Totals: %d applied, %d rejected, verdict: %s\n", totalApplied, totalRejected, result.OverallVerdict)

	// Timing.
	fmt.Printf("\n  Timing:\n")
	for _, t := range result.Timings {
		fmt.Printf("    %-28s %s\n", t.Stage, t.Duration.Round(time.Millisecond))
	}
	fmt.Printf("    %-28s %s\n", "TOTAL", result.TotalDuration.Round(time.Millisecond))
	fmt.Println()
}

func maybeApprovePersonas(personas []Persona, cfg CouncilConfig) ([]Persona, error) {
	if cfg.SkipApproval || cfg.DryRun {
		return personas, nil
	}
	return ApprovePersonas(personas, nil, nil)
}

func buildPersonaSet(ctx context.Context, spec, roundDir string, cfg CouncilConfig) ([]Persona, error) {
	personas := FixedPersonas()
	if cfg.SkipDynPersonas || cfg.DryRun {
		return personas, nil
	}
	dynPersonas, err := GeneratePersonas(ctx, spec, roundDir, cfg.Mode)
	if err != nil {
		return nil, fmt.Errorf("dynamic persona generation: %w", err)
	}
	return append(personas, dynPersonas...), nil
}

func writeDryRunPrompts(roundDir, spec string, round int, personas []Persona, priorFindings []ReviewOutput, codebaseCtx string) error {
	fmt.Printf("  [dry-run] would review with %d personas\n", len(personas))
	for i := range personas {
		prompt := BuildReviewPrompt(&PromptContext{
			Spec:            spec,
			Persona:         personas[i],
			Round:           round,
			PriorFindings:   priorFindings,
			CodebaseContext: codebaseCtx,
		})
		promptPath := filepath.Join(roundDir, fmt.Sprintf("prompt-%s.md", personas[i].PersonaID))
		if err := writeJSON(promptPath, map[string]string{"prompt": prompt}); err != nil {
			return fmt.Errorf("write dry-run prompt: %w", err)
		}
		fmt.Printf("  [dry-run] prompt written: %s (%s via %s)\n",
			promptPath, personas[i].DisplayName, BackendFor(&personas[i]).Command)
	}
	return nil
}
