// Package councilflow owns the council review pipeline for technical specs.
package councilflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// CouncilConfig controls the council review pipeline.
type CouncilConfig struct {
	Rounds          int        // number of review rounds (default: 2)
	Mode            ReviewMode // review tone and strictness (default: standard)
	CodebaseContext string     // optional codebase context for reviewers
	DryRun          bool       // generate prompts without executing
	Force           bool       // re-run all reviewers even if cached results exist
	SkipJudge       bool       // skip judge consolidation (review-only mode)
	SkipDynPersonas bool       // skip dynamic persona generation (fixed personas only)
	SkipApproval    bool       // skip interactive persona approval
}

// DefaultConfig returns a config with sensible defaults.
func DefaultConfig() CouncilConfig {
	return CouncilConfig{
		Rounds: 2,
	}
}

// CouncilResult holds the full pipeline output across all rounds.
type CouncilResult struct {
	Rounds         []RoundResult         `json:"rounds"`
	Consolidations []ConsolidationResult `json:"consolidations,omitempty"`
	OverallVerdict Verdict               `json:"overall_verdict"`
	FinalSpecPath  string                `json:"final_spec_path,omitempty"`
	FinalSpec      string                `json:"-"` // updated spec text (not serialized)
}

// RunCouncil executes the full council review pipeline for a technical spec.
func RunCouncil(ctx context.Context, spec, outputBaseDir string, cfg CouncilConfig) (*CouncilResult, error) {
	if cfg.Rounds < 1 {
		cfg.Rounds = 2
	}

	result := &CouncilResult{}
	var priorFindings []ReviewOutput
	currentSpec := spec

	for round := 1; round <= cfg.Rounds; round++ {
		fmt.Printf("\n=== Council Review Round %d/%d ===\n", round, cfg.Rounds)

		roundDir := filepath.Join(outputBaseDir, fmt.Sprintf("round-%d", round))

		personas, err := buildPersonaSet(ctx, currentSpec, roundDir, cfg)
		if err != nil {
			return nil, fmt.Errorf("round %d: %w", round, err)
		}

		personas, err = maybeApprovePersonas(personas, cfg)
		if err != nil {
			return nil, fmt.Errorf("round %d: %w", round, err)
		}

		runner := NewRunner(roundDir)
		runner.Force = cfg.Force
		runner.Mode = cfg.Mode

		if cfg.DryRun {
			if err := writeDryRunPrompts(roundDir, currentSpec, round, personas, priorFindings, cfg.CodebaseContext); err != nil {
				return nil, err
			}
			continue
		}

		roundResult, err := runner.RunRound(ctx, currentSpec, round, personas, priorFindings, cfg.CodebaseContext)
		if err != nil {
			return nil, fmt.Errorf("round %d reviews: %w", round, err)
		}

		if len(roundResult.Reviews) == 0 {
			return nil, fmt.Errorf("round %d: %w: all reviewers failed", round, ErrEmptyReview)
		}

		result.Rounds = append(result.Rounds, *roundResult)
		priorFindings = append(priorFindings, roundResult.Reviews...)

		totalFindings := 0
		for i := range roundResult.Reviews {
			totalFindings += len(roundResult.Reviews[i].Findings)
		}
		fmt.Printf("  Round %d reviews: %s (%d findings from %d reviewers)\n",
			round, roundResult.Consensus, totalFindings, len(roundResult.Reviews))

		// Judge consolidation.
		if cfg.SkipJudge || totalFindings == 0 {
			continue
		}

		judgeCfg := DefaultJudgeConfig()
		judgeCfg.Mode = cfg.Mode
		consolidation, judgeErr := RunJudge(ctx, currentSpec, round, roundResult.Reviews, roundDir, judgeCfg)
		if judgeErr != nil {
			return nil, fmt.Errorf("round %d judge: %w", round, judgeErr)
		}

		// Drift detection.
		consolidation.DriftWarnings = DetectDrift(currentSpec, consolidation.UpdatedSpec)
		if len(consolidation.DriftWarnings) > 0 {
			fmt.Printf("  [round %d] drift warnings:\n", round)
			for _, w := range consolidation.DriftWarnings {
				fmt.Printf("    - %s\n", w)
			}
		}

		result.Consolidations = append(result.Consolidations, *consolidation)

		// Write versioned spec.
		specPath := filepath.Join(outputBaseDir, fmt.Sprintf("technical-spec-v%d.md", round))
		if err := os.WriteFile(specPath, []byte(consolidation.UpdatedSpec), 0o644); err != nil {
			return nil, fmt.Errorf("write spec v%d: %w", round, err)
		}
		result.FinalSpecPath = specPath
		result.FinalSpec = consolidation.UpdatedSpec
		currentSpec = consolidation.UpdatedSpec

		fmt.Printf("  Round %d consolidated: %d applied, %d rejected → %s\n",
			round, consolidation.AppliedCount, consolidation.RejectedCount, specPath)
	}

	// Overall verdict is the consensus of the last round.
	if len(result.Rounds) > 0 {
		result.OverallVerdict = result.Rounds[len(result.Rounds)-1].Consensus
	}

	return result, nil
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
