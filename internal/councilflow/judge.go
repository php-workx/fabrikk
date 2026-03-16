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
	FailedEdits    []string     `json:"failed_edits,omitempty"`
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
		TimeoutSec: 1800, // 30 minutes — judge processes large specs with many findings
	}
}

// judgeDecision holds one judge instance's accept/reject decisions and edit snippets.
type judgeDecision struct {
	personaID  string
	edits      []SpecEdit
	rejections []Rejection
	err        error
}

// RunJudge executes the judge/editor consolidation for one round.
// Phase 1: Parallel Opus instances judge each reviewer's findings independently.
// Phase 2: Apply all accepted edits to the spec (pure string ops, no LLM).
func RunJudge(ctx context.Context, spec string, round int, reviews []ReviewOutput, outputDir string, cfg JudgeConfig) (*ConsolidationResult, error) {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}

	// Filter to reviews with findings worth judging.
	var judgeable []ReviewOutput
	for i := range reviews {
		if len(reviews[i].Findings) == 0 {
			fmt.Printf("  [round %d] judge: skip %s (no findings)\n", round, reviews[i].PersonaID)
			continue
		}
		// Skip reviews with only minor findings.
		hasNonMinor := false
		for j := range reviews[i].Findings {
			if reviews[i].Findings[j].Severity != "minor" {
				hasNonMinor = true
				break
			}
		}
		if !hasNonMinor {
			fmt.Printf("  [round %d] judge: skip %s (minor only)\n", round, reviews[i].PersonaID)
			continue
		}
		judgeable = append(judgeable, reviews[i])
	}

	fmt.Printf("  [round %d] judge: %d reviewers to process (parallel) ...\n", round, len(judgeable))

	// Phase 1: Parallel judgment — each reviewer's findings judged independently.
	ch := make(chan judgeDecision, len(judgeable))
	for i := range judgeable {
		go func(review ReviewOutput) {
			decision := judgeOneReview(ctx, spec, round, &review, outputDir, &cfg)
			ch <- decision
		}(judgeable[i])
	}

	// Collect decisions.
	var allEdits []SpecEdit
	var allRejections []Rejection
	var allFailed []string
	for range judgeable {
		decision := <-ch
		if decision.err != nil {
			fmt.Printf("  [round %d] judge: %s FAILED (%v)\n", round, decision.personaID, decision.err)
			continue
		}
		allEdits = append(allEdits, decision.edits...)
		allRejections = append(allRejections, decision.rejections...)
		fmt.Printf("  [round %d] judge: %s — %d edits, %d rejected\n",
			round, decision.personaID, len(decision.edits), len(decision.rejections))
	}

	// Phase 2: Apply all accepted edits to the spec (no LLM, pure string ops).
	updatedSpec := spec
	appliedCount := 0
	for i := range allEdits {
		edit := &allEdits[i]
		if edit.Find == "" {
			allFailed = append(allFailed, fmt.Sprintf("%s: empty find field", edit.FindingID))
			continue
		}
		if edit.Replace == "" {
			allFailed = append(allFailed, fmt.Sprintf("%s: empty replace field", edit.FindingID))
			continue
		}
		if !strings.Contains(updatedSpec, edit.Find) {
			allFailed = append(allFailed, fmt.Sprintf("%s: find text not found in spec", edit.FindingID))
			continue
		}
		updatedSpec = strings.Replace(updatedSpec, edit.Find, edit.Replace, 1)
		appliedCount++
	}

	if strings.TrimSpace(updatedSpec) == "" {
		return nil, fmt.Errorf("judge edits produced an empty spec — aborting to prevent data loss")
	}

	// Phase 3: Conflict resolution — use Opus to apply failed edits to the updated spec.
	if len(allFailed) > 0 && len(conflictEdits(allEdits, allFailed)) > 0 {
		resolved, resolveErr := resolveConflicts(ctx, updatedSpec, conflictEdits(allEdits, allFailed), outputDir, &cfg)
		if resolveErr != nil {
			fmt.Printf("  [round %d] judge: conflict resolution failed (%v)\n", round, resolveErr)
		} else {
			updatedSpec = resolved.spec
			appliedCount += resolved.applied
			allFailed = resolved.stillFailed
			fmt.Printf("  [round %d] judge: conflict resolution — %d more applied, %d still failed\n",
				round, resolved.applied, len(resolved.stillFailed))
		}
	}

	combined := &ConsolidationResult{
		Round:       round,
		UpdatedSpec: updatedSpec,
		RejectionLog: RejectionLog{
			Round:      round,
			Rejections: allRejections,
		},
		AppliedCount:   appliedCount,
		RejectedCount:  len(allRejections),
		FailedEdits:    allFailed,
		ConsolidatedAt: time.Now(),
	}

	if err := writeJSON(filepath.Join(outputDir, "consolidation.json"), combined); err != nil {
		return nil, fmt.Errorf("write consolidation: %w", err)
	}
	if err := writeJSON(filepath.Join(outputDir, "rejection-log.json"), combined.RejectionLog); err != nil {
		return nil, fmt.Errorf("write rejection log: %w", err)
	}

	fmt.Printf("  [round %d] judge total: %d applied, %d rejected, %d failed\n",
		round, appliedCount, len(allRejections), len(allFailed))

	return combined, nil
}

type conflictResult struct {
	spec        string
	applied     int
	stillFailed []string
}

// conflictEdits extracts the SpecEdit objects that correspond to failed edit IDs.
func conflictEdits(allEdits []SpecEdit, failedIDs []string) []SpecEdit {
	failed := make(map[string]bool, len(failedIDs))
	for _, f := range failedIDs {
		// Extract finding ID from "sec-004: find text not found in spec"
		id := f
		if idx := strings.Index(f, ":"); idx > 0 {
			id = f[:idx]
		}
		failed[id] = true
	}
	var edits []SpecEdit
	for i := range allEdits {
		if failed[allEdits[i].FindingID] {
			edits = append(edits, allEdits[i])
		}
	}
	return edits
}

// resolveConflicts sends failed edits to Opus with the current (already-edited) spec
// so it can apply them against the updated text.
func resolveConflicts(ctx context.Context, spec string, edits []SpecEdit, outputDir string, cfg *JudgeConfig) (*conflictResult, error) {
	var b strings.Builder
	b.WriteString("# Conflict Resolution\n\n")
	b.WriteString("The following edits could not be applied because the target text was already modified by other edits.\n")
	b.WriteString("The spec below is the CURRENT version after other edits were applied.\n\n")
	b.WriteString("For each edit, find the closest matching text in the current spec and produce an updated find/replace pair.\n")
	b.WriteString("If the edit's intent is already addressed by existing changes, reject it.\n\n")
	b.WriteString("## Failed Edits\n\n```json\n")
	data, _ := json.MarshalIndent(edits, "", "  ")
	b.Write(data)
	b.WriteString("\n```\n\n## Current Specification\n\n")
	b.WriteString(spec)
	b.WriteString("\n\n## Output Format\n\nRespond with ONLY a JSON object:\n```json\n")
	b.WriteString(`{"edits": [{"finding_id": "...", "persona_id": "...", "action": "...", "section": "...", "find": "...", "replace": "..."}], "rejected": [{"finding_id": "...", "persona_id": "...", "severity": "...", "description": "...", "rejection_reason": "already addressed by prior edit"}]}`)
	b.WriteString("\n```\n")

	promptPath := filepath.Join(outputDir, "judge-conflict-resolution.md")
	_ = os.WriteFile(promptPath, []byte(b.String()), 0o644)

	fmt.Printf("  [judge] resolving %d conflicts ...\n", len(edits))
	output, err := invokeBackend(ctx, &cfg.Backend, b.String(), cfg.TimeoutSec)
	if err != nil {
		return nil, err
	}

	parsed, err := parseJudgeRawOutput(output)
	if err != nil {
		return nil, err
	}

	// Apply resolved edits.
	updatedSpec := spec
	applied := 0
	var stillFailed []string
	for i := range parsed.Edits {
		edit := &parsed.Edits[i]
		if edit.Find == "" || edit.Replace == "" {
			stillFailed = append(stillFailed, fmt.Sprintf("%s: empty find/replace after resolution", edit.FindingID))
			continue
		}
		if !strings.Contains(updatedSpec, edit.Find) {
			stillFailed = append(stillFailed, fmt.Sprintf("%s: still not found after resolution", edit.FindingID))
			continue
		}
		updatedSpec = strings.Replace(updatedSpec, edit.Find, edit.Replace, 1)
		applied++
	}

	return &conflictResult{spec: updatedSpec, applied: applied, stillFailed: stillFailed}, nil
}

func judgeOneReview(ctx context.Context, spec string, round int, review *ReviewOutput, outputDir string, cfg *JudgeConfig) judgeDecision {
	prompt := buildJudgePrompt(spec, round, []ReviewOutput{*review})

	// Write per-reviewer prompt for audit.
	promptPath := filepath.Join(outputDir, fmt.Sprintf("judge-prompt-%s.md", review.PersonaID))
	_ = os.WriteFile(promptPath, []byte(prompt), 0o644)

	output, err := invokeBackend(ctx, &cfg.Backend, prompt, cfg.TimeoutSec)
	if err != nil {
		return judgeDecision{personaID: review.PersonaID, err: err}
	}

	parsed, err := parseJudgeRawOutput(output)
	if err != nil {
		return judgeDecision{personaID: review.PersonaID, err: err}
	}

	return judgeDecision{
		personaID:  review.PersonaID,
		edits:      parsed.Edits,
		rejections: parsed.Rejected,
	}
}

func parseJudgeRawOutput(raw string) (*judgeRawOutput, error) {
	jsonStr := extractJSON(raw)
	var parsed judgeRawOutput
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		return nil, fmt.Errorf("unmarshal judge output: %w (raw: %s)", err, truncateForError(raw, 200))
	}
	return &parsed, nil
}

func buildJudgePrompt(spec string, round int, reviews []ReviewOutput) string {
	var b strings.Builder

	b.WriteString("# Judge / Editor — Finding-by-Finding Validation\n\n")
	b.WriteString("You are the **Judge/Editor** — the most critical role in the council review pipeline.\n\n")
	b.WriteString("## Process\n\n")
	b.WriteString("Process each finding INDIVIDUALLY in order. For each finding:\n\n")
	b.WriteString("1. Read the finding's description and recommendation.\n")
	b.WriteString("2. Check: Is this factually correct against the current spec? Does the spec already address it?\n")
	b.WriteString("3. Decide: APPLY (edit the spec) or REJECT (record why).\n")
	b.WriteString("4. If applying: make a minimal, surgical edit to the spec. Do NOT rewrite surrounding text.\n")
	b.WriteString("5. Move to the next finding.\n\n")
	b.WriteString("DO NOT synthesize, summarize, or merge findings before processing them. ")
	b.WriteString("Each finding must be validated on its own merits against the spec text. ")
	b.WriteString("The only exception is exact duplicates from different reviewers — deduplicate those and apply once.\n\n")

	b.WriteString("## Rules\n\n")
	b.WriteString("- You MUST NOT dismiss a high-confidence finding without explicit counter-evidence.\n")
	b.WriteString("- You MUST NOT drop existing spec content unless a finding explicitly calls for its removal.\n")
	b.WriteString("- You MUST preserve all section headings and structure.\n")
	b.WriteString("- Every finding must appear in either `applied` or `rejected` — none may be silently skipped.\n")
	b.WriteString("- Apply findings as minimal surgical edits, not wholesale rewrites.\n")
	b.WriteString("- When two findings conflict, apply the higher-severity one and reject the other with an explanation.\n\n")

	b.WriteString("## Security Finding Triage\n\n")
	b.WriteString("Security reviewers often flag issues that are real but not relevant to the current implementation phase. ")
	b.WriteString("For each security finding, evaluate:\n\n")
	b.WriteString("1. Is this exploitable in the CURRENT phase (single-user CLI, local execution, no network exposure)?\n")
	b.WriteString("2. Or is this only relevant for FUTURE phases (multi-user, detached engine, network-exposed APIs)?\n\n")
	b.WriteString("APPLY current-phase findings as spec edits. ")
	b.WriteString("For future-phase findings: APPLY them as clearly marked future-phase requirements ")
	b.WriteString("(e.g., 'When multi-user mode is introduced, this must be addressed by...'). ")
	b.WriteString("REJECT only findings that are factually wrong or already covered.\n\n")

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
  "edits": [
    {
      "finding_id": "sec-001",
      "persona_id": "security-perf-engineer",
      "action": "Brief description of what was changed",
      "section": "## 2. Architecture",
      "find": "exact text to find in the spec (copy-paste from the spec above)",
      "replace": "replacement text with the finding addressed"
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
	b.WriteString("IMPORTANT:\n")
	b.WriteString("- Each edit's `find` field must be an EXACT substring of the current spec text (copy-paste, do not paraphrase).\n")
	b.WriteString("- The `replace` field contains the replacement text with the finding addressed.\n")
	b.WriteString("- Keep edits minimal and surgical. Do NOT include the entire spec — only the changed portions.\n")
	b.WriteString("- If multiple findings affect the same paragraph, combine them into one edit.\n")
	b.WriteString("- Every finding must appear in either `edits` or `rejected` — none may be silently skipped.\n")

	return b.String()
}

// SpecEdit is a single find-replace edit to apply to the spec.
type SpecEdit struct {
	FindingID string `json:"finding_id"`
	PersonaID string `json:"persona_id"`
	Action    string `json:"action"`
	Section   string `json:"section"`
	Find      string `json:"find"`
	Replace   string `json:"replace"`
}

type judgeRawOutput struct {
	Edits    []SpecEdit  `json:"edits"`
	Rejected []Rejection `json:"rejected"`
}

func parseJudgeOutput(raw string, round int, currentSpec string) (*ConsolidationResult, error) {
	jsonStr := extractJSON(raw)

	var parsed judgeRawOutput
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		return nil, fmt.Errorf("unmarshal judge output: %w (raw: %s)", err, truncateForError(raw, 200))
	}

	// Apply edits to the spec.
	updatedSpec := currentSpec
	appliedCount := 0
	var failedEdits []string
	for i := range parsed.Edits {
		edit := &parsed.Edits[i]
		if edit.Find == "" {
			failedEdits = append(failedEdits, fmt.Sprintf("%s: empty find field", edit.FindingID))
			continue
		}
		if edit.Replace == "" {
			failedEdits = append(failedEdits, fmt.Sprintf("%s: empty replace field (would delete content)", edit.FindingID))
			continue
		}
		if !strings.Contains(updatedSpec, edit.Find) {
			failedEdits = append(failedEdits, fmt.Sprintf("%s: find text not found in spec", edit.FindingID))
			continue
		}
		updatedSpec = strings.Replace(updatedSpec, edit.Find, edit.Replace, 1)
		appliedCount++
	}

	// Guard: reject if edits wiped the spec.
	if strings.TrimSpace(updatedSpec) == "" {
		return nil, fmt.Errorf("judge edits produced an empty spec — aborting to prevent data loss")
	}

	if len(failedEdits) > 0 {
		fmt.Printf("  [judge] %d edits could not be applied:\n", len(failedEdits))
		for _, msg := range failedEdits {
			fmt.Printf("    - %s\n", msg)
		}
	}

	return &ConsolidationResult{
		Round:       round,
		UpdatedSpec: updatedSpec,
		RejectionLog: RejectionLog{
			Round:      round,
			Rejections: parsed.Rejected,
		},
		AppliedCount:   appliedCount,
		RejectedCount:  len(parsed.Rejected),
		FailedEdits:    failedEdits,
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
