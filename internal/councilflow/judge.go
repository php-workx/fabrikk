package councilflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ConsolidationResult holds the judge's output for one round.
type ConsolidationResult struct {
	Round          int          `json:"round"`
	UpdatedSpec    string       `json:"updated_spec"`
	RejectionLog   RejectionLog `json:"rejection_log"`
	AppliedCount   int          `json:"applied_count"`
	RejectedCount  int          `json:"rejected_count"`
	DriftWarnings  []string     `json:"drift_warnings,omitempty"`
	ConsolidatedAt time.Time    `json:"consolidated_at"`
}

// JudgeConfig controls the judge/editor behavior.
type JudgeConfig struct {
	Backend    CLIBackend
	TimeoutSec int
}

// DefaultJudgeConfig returns a config targeting Claude Opus with extended thinking.
func DefaultJudgeConfig() JudgeConfig {
	return JudgeConfig{
		Backend: CLIBackend{
			Command: "claude",
			Args:    []string{"-p", "--model", "opus"},
		},
		TimeoutSec: 900, // 15 minutes — judge has more work
	}
}

// RunJudge executes the judge/editor consolidation for one round.
func RunJudge(ctx context.Context, spec string, round int, reviews []ReviewOutput, outputDir string, cfg JudgeConfig) (*ConsolidationResult, error) {
	prompt := buildJudgePrompt(spec, round, reviews)

	// Write the prompt for debugging/audit.
	promptPath := filepath.Join(outputDir, "judge-prompt.md")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}
	if err := os.WriteFile(promptPath, []byte(prompt), 0o644); err != nil {
		return nil, fmt.Errorf("write judge prompt: %w", err)
	}

	fmt.Printf("  [round %d] judge consolidating %d reviews ...\n", round, len(reviews))

	output, err := invokeBackend(ctx, &cfg.Backend, prompt, cfg.TimeoutSec)
	if err != nil {
		return nil, fmt.Errorf("invoke judge: %w", err)
	}

	result, err := parseJudgeOutput(output, round)
	if err != nil {
		return nil, fmt.Errorf("parse judge output: %w", err)
	}

	// Write outputs.
	if err := writeJSON(filepath.Join(outputDir, "consolidation.json"), result); err != nil {
		return nil, fmt.Errorf("write consolidation: %w", err)
	}
	if err := writeJSON(filepath.Join(outputDir, "rejection-log.json"), result.RejectionLog); err != nil {
		return nil, fmt.Errorf("write rejection log: %w", err)
	}

	fmt.Printf("  [round %d] judge: %d applied, %d rejected\n", round, result.AppliedCount, result.RejectedCount)

	return result, nil
}

func buildJudgePrompt(spec string, round int, reviews []ReviewOutput) string {
	var b strings.Builder

	b.WriteString("# Judge / Editor Consolidation\n\n")
	b.WriteString("You are the **Judge/Editor** — the most critical role in the council review pipeline.\n\n")
	b.WriteString("Your job:\n")
	b.WriteString("1. Cross-validate each finding against the spec for factual accuracy.\n")
	b.WriteString("2. Reject findings that are wrong, already addressed, out of scope, or based on misunderstanding.\n")
	b.WriteString("3. Apply valid findings by editing the spec text.\n")
	b.WriteString("4. Preserve the spec's voice, structure, and existing content.\n")
	b.WriteString("5. Explain every rejection with a clear reason.\n\n")

	b.WriteString("## Rules\n\n")
	b.WriteString("- You MUST NOT dismiss a high-confidence finding without explicit counter-evidence.\n")
	b.WriteString("- You MUST NOT drop existing spec content unless a finding explicitly calls for its removal.\n")
	b.WriteString("- You MUST deduplicate findings that describe the same underlying issue.\n")
	b.WriteString("- You MUST preserve all section headings and structure.\n")
	b.WriteString("- Apply findings as minimal surgical edits, not wholesale rewrites.\n\n")

	b.WriteString("## Reviewer Findings\n\n")
	for i := range reviews {
		review := &reviews[i]
		fmt.Fprintf(&b, "### %s (Round %d) — %s (confidence: %s)\n\n",
			review.PersonaID, review.Round, review.Verdict, review.Confidence)
		if review.KeyInsight != "" {
			fmt.Fprintf(&b, "Key insight: %s\n\n", review.KeyInsight)
		}
		data, err := json.MarshalIndent(review.Findings, "", "  ")
		if err == nil {
			b.WriteString("```json\n")
			b.Write(data)
			b.WriteString("\n```\n\n")
		}
	}

	b.WriteString("## Current Technical Specification\n\n")
	b.WriteString(spec)
	b.WriteString("\n\n")

	fmt.Fprintf(&b, "## Output Format (Round %d)\n\n", round)
	b.WriteString("Respond with ONLY a JSON object with this schema:\n\n")
	b.WriteString("```json\n")
	b.WriteString(`{
  "updated_spec": "... the full updated technical specification markdown ...",
  "applied": [
    {
      "finding_id": "sec-001",
      "persona_id": "security-perf-engineer",
      "action": "Brief description of what was changed"
    }
  ],
  "rejected": [
    {
      "finding_id": "perf-003",
      "persona_id": "security-perf-engineer",
      "severity": "significant",
      "description": "The original finding text",
      "rejection_reason": "Why this was rejected",
      "dismissal_rationale": "Counter-evidence (required for high-confidence rejections)",
      "cross_reference": "Reference to spec section that addresses this"
    }
  ]
}`)
	b.WriteString("\n```\n\n")
	b.WriteString("IMPORTANT: The `updated_spec` field must contain the COMPLETE updated specification. Do not truncate or summarize.\n")

	return b.String()
}

type judgeRawOutput struct {
	UpdatedSpec string `json:"updated_spec"`
	Applied     []struct {
		FindingID string `json:"finding_id"`
		PersonaID string `json:"persona_id"`
		Action    string `json:"action"`
	} `json:"applied"`
	Rejected []Rejection `json:"rejected"`
}

func parseJudgeOutput(raw string, round int) (*ConsolidationResult, error) {
	jsonStr := extractJSON(raw)

	var parsed judgeRawOutput
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		return nil, fmt.Errorf("unmarshal judge output: %w (raw: %s)", err, truncateForError(raw, 200))
	}

	if parsed.UpdatedSpec == "" {
		return nil, fmt.Errorf("judge output missing updated_spec")
	}

	return &ConsolidationResult{
		Round:       round,
		UpdatedSpec: parsed.UpdatedSpec,
		RejectionLog: RejectionLog{
			Round:      round,
			Rejections: parsed.Rejected,
		},
		AppliedCount:   len(parsed.Applied),
		RejectedCount:  len(parsed.Rejected),
		ConsolidatedAt: time.Now(),
	}, nil
}

// DetectDrift compares two spec versions and flags removed sections or significant shrinkage.
func DetectDrift(before, after string) []string {
	var warnings []string

	beforeSections := extractHeadings(before)
	afterSections := extractHeadings(after)

	afterSet := make(map[string]bool, len(afterSections))
	for _, h := range afterSections {
		afterSet[h] = true
	}

	for _, h := range beforeSections {
		if !afterSet[h] {
			warnings = append(warnings, fmt.Sprintf("section removed: %s", h))
		}
	}

	// Flag significant shrinkage (>20% reduction).
	if before != "" {
		reduction := float64(len(before)-len(after)) / float64(len(before))
		if reduction > 0.2 {
			warnings = append(warnings, fmt.Sprintf("spec shrank by %.0f%% (%d → %d bytes)", reduction*100, len(before), len(after)))
		}
	}

	return warnings
}

func extractHeadings(markdown string) []string {
	var headings []string
	for _, line := range strings.Split(markdown, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") || strings.HasPrefix(trimmed, "### ") {
			headings = append(headings, trimmed)
		}
	}
	return headings
}
