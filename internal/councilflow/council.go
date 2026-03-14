// Package councilflow owns the council review pipeline for technical specs.
package councilflow

import (
	"context"
	"fmt"
	"path/filepath"
)

// CouncilConfig controls the council review pipeline.
type CouncilConfig struct {
	Rounds          int    // number of review rounds (default: 2)
	CodebaseContext string // optional codebase context for reviewers
	DryRun          bool   // generate prompts without executing
}

// DefaultConfig returns a config with sensible defaults.
func DefaultConfig() CouncilConfig {
	return CouncilConfig{
		Rounds: 2,
	}
}

// CouncilResult holds the full pipeline output across all rounds.
type CouncilResult struct {
	Rounds         []RoundResult `json:"rounds"`
	OverallVerdict Verdict       `json:"overall_verdict"`
}

// RunCouncil executes the full council review pipeline for a technical spec.
func RunCouncil(ctx context.Context, spec, outputBaseDir string, cfg CouncilConfig) (*CouncilResult, error) {
	if cfg.Rounds < 1 {
		cfg.Rounds = 2
	}

	personas := FixedPersonas()

	result := &CouncilResult{}
	var priorFindings []ReviewOutput

	for round := 1; round <= cfg.Rounds; round++ {
		fmt.Printf("\n=== Council Review Round %d/%d ===\n", round, cfg.Rounds)

		roundDir := filepath.Join(outputBaseDir, fmt.Sprintf("round-%d", round))
		runner := NewRunner(roundDir)

		if cfg.DryRun {
			if err := writeDryRunPrompts(roundDir, spec, round, personas, priorFindings, cfg.CodebaseContext); err != nil {
				return nil, err
			}
			continue
		}

		roundResult, err := runner.RunRound(ctx, spec, round, personas, priorFindings, cfg.CodebaseContext)
		if err != nil {
			return nil, fmt.Errorf("round %d: %w", round, err)
		}

		result.Rounds = append(result.Rounds, *roundResult)
		priorFindings = append(priorFindings, roundResult.Reviews...)

		totalFindings := 0
		for i := range roundResult.Reviews {
			totalFindings += len(roundResult.Reviews[i].Findings)
		}
		fmt.Printf("  Round %d complete: %s (%d findings from %d reviewers)\n",
			round, roundResult.Consensus, totalFindings, len(roundResult.Reviews))
	}

	// Overall verdict is the consensus of the last round.
	if len(result.Rounds) > 0 {
		result.OverallVerdict = result.Rounds[len(result.Rounds)-1].Consensus
	}

	return result, nil
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
