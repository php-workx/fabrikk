package councilflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// CLIBackend describes how to invoke a reviewer via a CLI tool.
type CLIBackend struct {
	Command string   // e.g., "claude", "codex", "gemini"
	Args    []string // base args before the prompt
}

// KnownBackends maps backend names to their CLI invocation config.
var KnownBackends = map[string]CLIBackend{
	BackendClaude: {Command: "claude", Args: []string{"-p"}},
	BackendCodex:  {Command: "codex", Args: []string{"-q"}},
	BackendGemini: {Command: "gemini", Args: []string{"-p"}},
}

// BackendFor returns the CLI backend for a persona, falling back to claude.
func BackendFor(persona *Persona) CLIBackend {
	if b, ok := KnownBackends[persona.Backend]; ok {
		return b
	}
	return KnownBackends[BackendClaude]
}

// Runner executes council reviews for a technical spec.
type Runner struct {
	OutputDir  string // directory to write review artifacts
	TimeoutSec int    // per-reviewer timeout in seconds
}

// NewRunner creates a runner with defaults.
func NewRunner(outputDir string) *Runner {
	return &Runner{
		OutputDir:  outputDir,
		TimeoutSec: 600, // 10 minutes per reviewer
	}
}

// RunRound executes a single review round: generates prompts, runs each persona, collects findings.
func (r *Runner) RunRound(ctx context.Context, spec string, round int, personas []Persona, priorFindings []ReviewOutput, codebaseCtx string) (*RoundResult, error) {
	if err := os.MkdirAll(r.OutputDir, 0o755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}

	if err := writeJSON(filepath.Join(r.OutputDir, "personas.json"), personas); err != nil {
		return nil, fmt.Errorf("write personas: %w", err)
	}

	result := &RoundResult{
		Round:    round,
		Personas: personas,
	}

	for i := range personas {
		persona := &personas[i]
		backend := BackendFor(persona)
		fmt.Printf("  [round %d] reviewing: %s (via %s) ...\n", round, persona.DisplayName, backend.Command)

		review, err := r.runSingleReview(ctx, spec, round, persona, &backend, priorFindings, codebaseCtx)
		if err != nil {
			return nil, fmt.Errorf("review %s: %w", persona.PersonaID, err)
		}

		reviewPath := filepath.Join(r.OutputDir, fmt.Sprintf("review-%s.json", persona.PersonaID))
		if err := writeJSON(reviewPath, review); err != nil {
			return nil, fmt.Errorf("write review %s: %w", persona.PersonaID, err)
		}

		result.Reviews = append(result.Reviews, *review)
		fmt.Printf("  [round %d] %s: %s (%d findings)\n", round, persona.DisplayName, review.Verdict, len(review.Findings))
	}

	result.Consensus = ComputeConsensus(result.Reviews)
	fmt.Printf("  [round %d] consensus: %s\n", round, result.Consensus)

	return result, nil
}

func (r *Runner) runSingleReview(ctx context.Context, spec string, round int, persona *Persona, backend *CLIBackend, priorFindings []ReviewOutput, codebaseCtx string) (*ReviewOutput, error) {
	prompt := BuildReviewPrompt(&PromptContext{
		Spec:            spec,
		Persona:         *persona,
		Round:           round,
		PriorFindings:   priorFindings,
		CodebaseContext: codebaseCtx,
	})

	// Initial review.
	output, err := invokeBackend(ctx, backend, prompt, r.TimeoutSec)
	if err != nil {
		return nil, fmt.Errorf("initial review: %w", err)
	}

	review, err := parseReviewOutput(output)
	if err != nil {
		return nil, fmt.Errorf("parse initial review: %w", err)
	}

	// Nudge pass — send follow-up to go deeper.
	nudgePrompt := prompt + "\n\n---\n\nPrevious output:\n```json\n" + output + "\n```\n\n" + BuildNudgePrompt(persona)
	nudgeOutput, nudgeErr := invokeBackend(ctx, backend, nudgePrompt, r.TimeoutSec)
	if nudgeErr != nil {
		return review, nil //nolint:nilerr // nudge is best-effort; initial review is valid
	}

	nudgeReview, nudgeParseErr := parseReviewOutput(nudgeOutput)
	if nudgeParseErr != nil {
		return review, nil //nolint:nilerr // nudge parse failure; initial review is valid
	}

	// Use nudge output if it has more findings (it should be the complete list).
	if len(nudgeReview.Findings) > len(review.Findings) {
		return nudgeReview, nil
	}
	return review, nil
}

func invokeBackend(ctx context.Context, backend *CLIBackend, prompt string, timeoutSec int) (string, error) {
	timeout := time.Duration(timeoutSec) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := append([]string(nil), backend.Args...)
	args = append(args, prompt)

	cmd := exec.CommandContext(ctx, backend.Command, args...) //nolint:gosec // command is from trusted CLIBackend config, not user input
	cmd.Stderr = os.Stderr

	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("execute %s: %w", backend.Command, err)
	}

	return string(out), nil
}

func parseReviewOutput(raw string) (*ReviewOutput, error) {
	jsonStr := extractJSON(raw)

	var review ReviewOutput
	if err := json.Unmarshal([]byte(jsonStr), &review); err != nil {
		return nil, fmt.Errorf("unmarshal review output: %w (raw: %s)", err, truncateForError(raw, 200))
	}
	review.ReviewedAt = time.Now()
	return &review, nil
}

func extractJSON(s string) string {
	if idx := strings.Index(s, "```json"); idx >= 0 {
		start := idx + len("```json")
		if end := strings.Index(s[start:], "```"); end >= 0 {
			return strings.TrimSpace(s[start : start+end])
		}
	}
	if idx := strings.Index(s, "```"); idx >= 0 {
		start := idx + len("```")
		if end := strings.Index(s[start:], "```"); end >= 0 {
			return strings.TrimSpace(s[start : start+end])
		}
	}
	if idx := strings.Index(s, "{"); idx >= 0 {
		depth := 0
		for i := idx; i < len(s); i++ {
			switch s[i] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					return s[idx : i+1]
				}
			}
		}
	}
	return strings.TrimSpace(s)
}

func truncateForError(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func writeJSON(path string, v any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
