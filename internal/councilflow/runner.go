package councilflow

import (
	"context"
	"crypto/sha256"
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
	Command    string   // e.g., "claude", "codex", "gemini"
	Args       []string // base args before the prompt
	PromptFlag string   // if set, prompt is passed as --flag=value instead of positional
}

// KnownBackends maps backend names to their default CLI invocation config.
// Commands are resolved to absolute paths at init time to work regardless
// of the subprocess PATH environment.
var KnownBackends = map[string]CLIBackend{
	BackendClaude: {Command: resolveCommand("claude"), Args: []string{"-p", "--model", "opus"}},
	BackendCodex:  {Command: resolveCommand("codex"), Args: []string{"exec", "-m", "gpt-5.4", "-c", "reasoning_effort=high"}},
	BackendGemini: {Command: resolveCommand("gemini"), Args: []string{"-m", "gemini-3-pro-preview"}, PromptFlag: "-p"},
}

func resolveCommand(name string) string {
	// Try standard PATH first.
	if path, err := exec.LookPath(name); err == nil {
		return path
	}
	// Search common tool directories not always in PATH.
	home, _ := os.UserHomeDir()
	if home != "" {
		candidates := []string{
			filepath.Join(home, ".local", "bin", name),
			filepath.Join(home, "go", "bin", name),
		}
		// Check nvm node directories.
		nvmDir := filepath.Join(home, ".nvm", "versions", "node")
		if entries, err := os.ReadDir(nvmDir); err == nil {
			for _, e := range entries {
				candidates = append(candidates, filepath.Join(nvmDir, e.Name(), "bin", name))
			}
		}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				return c
			}
		}
	}
	return name
}

// BackendFor returns the CLI backend for a persona.
// If the persona specifies a model preference, it overrides the default model arg.
func BackendFor(persona *Persona) CLIBackend {
	base, ok := KnownBackends[persona.Backend]
	if !ok {
		base = KnownBackends[BackendClaude]
	}
	if persona.ModelPref == "" {
		return base
	}

	// Override the model in the args.
	args := make([]string, len(base.Args))
	copy(args, base.Args)
	args = overrideModelArg(persona.Backend, args, persona.ModelPref)
	return CLIBackend{Command: base.Command, Args: args, PromptFlag: base.PromptFlag}
}

// overrideModelArg replaces the model argument in a CLI args slice.
func overrideModelArg(backend string, args []string, model string) []string {
	switch backend {
	case BackendClaude:
		return replaceArgValue(args, "--model", model)
	case BackendCodex:
		return replaceArgValue(args, "-m", model)
	case BackendGemini:
		return replaceArgValue(args, "-m", model)
	default:
		return args
	}
}

func replaceArgValue(args []string, flag, value string) []string {
	for i, arg := range args {
		if arg == flag && i+1 < len(args) {
			args[i+1] = value
			return args
		}
	}
	return append(args, flag, value)
}

// InvokeFn is the function signature for invoking a CLI backend.
// Tests replace InvokeFunc with a stub that returns predetermined responses.
type InvokeFn func(ctx context.Context, backend *CLIBackend, prompt string, timeoutSec int) (string, error)

// InvokeFunc is the package-level backend invocation function.
// Replace in tests with a stub to avoid real CLI calls.
var InvokeFunc InvokeFn = invokeBackend

// Runner executes council reviews for a technical spec.
type Runner struct {
	OutputDir   string     // directory to write review artifacts
	TimeoutSec  int        // per-reviewer timeout in seconds
	Mode        ReviewMode // review tone and strictness
	SpecPath    string     // absolute path to spec file (if set, reviewers read from file instead of inline)
	StaggerSec  int        // seconds between launching parallel reviewers (0 = no stagger)
	Force       bool       // re-run all reviewers even if cached results exist
	EnableNudge bool       // enable nudge pass (disabled by default — needs session resume for quality)
}

// NewRunner creates a runner with defaults.
func NewRunner(outputDir string) *Runner {
	return &Runner{
		OutputDir:  outputDir,
		TimeoutSec: 600, // 10 minutes per reviewer
	}
}

// RunRound executes a single review round: generates prompts, runs each persona, collects findings.
// Skips personas that already have a valid cached review file unless Force is set.
func (r *Runner) RunRound(ctx context.Context, spec string, round int, personas []Persona, priorFindings []ReviewOutput, codebaseCtx string) (*RoundResult, error) {
	if err := os.MkdirAll(r.OutputDir, 0o755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}

	// Validate persona uniqueness.
	if err := validatePersonaUniqueness(personas); err != nil {
		return nil, err
	}

	if err := writeJSON(filepath.Join(r.OutputDir, "personas.json"), personas); err != nil {
		return nil, fmt.Errorf("write personas: %w", err)
	}

	// Cache key includes spec content + review mode — changing either invalidates cache.
	cacheInput := spec + "\n__mode__:" + string(r.Mode)
	specHash := fmt.Sprintf("%x", sha256.Sum256([]byte(cacheInput)))
	specHashPath := filepath.Join(r.OutputDir, "spec-hash.txt")
	cacheValid := !r.Force && matchesSpecHash(specHashPath, specHash)
	if err := os.WriteFile(specHashPath, []byte(specHash+"\n"), 0o644); err != nil {
		return nil, fmt.Errorf("write spec hash: %w", err)
	}

	result := &RoundResult{
		Round:    round,
		Personas: personas,
	}

	// Separate cached from pending reviews.
	var pending []pendingReview

	for i := range personas {
		persona := &personas[i]
		reviewPath := filepath.Join(r.OutputDir, fmt.Sprintf("review-%s.json", persona.PersonaID))

		if cacheValid {
			if cached, err := loadCachedReview(reviewPath); err == nil {
				result.Reviews = append(result.Reviews, *cached)
				fmt.Printf("  [round %d] %s: cached %s (%d findings)\n", round, persona.DisplayName, cached.Verdict, len(cached.Findings))
				continue
			}
		}
		pending = append(pending, pendingReview{index: i, persona: persona})
	}

	// Run pending reviews in parallel.
	if len(pending) > 0 {
		reviews := runParallelReviews(ctx, r, spec, round, pending, priorFindings, codebaseCtx)
		result.Reviews = append(result.Reviews, reviews...)
	}

	result.Consensus = ComputeConsensus(result.Reviews)
	fmt.Printf("  [round %d] consensus: %s\n", round, result.Consensus)

	return result, nil
}

type pendingReview struct {
	index   int
	persona *Persona
}

type reviewResult struct {
	review  *ReviewOutput
	err     error
	persona *Persona
}

func runParallelReviews(ctx context.Context, r *Runner, spec string, round int, pending []pendingReview, priorFindings []ReviewOutput, codebaseCtx string) []ReviewOutput {
	ch := make(chan reviewResult, len(pending))

	for i, p := range pending {
		if i > 0 && r.StaggerSec > 0 {
			fmt.Printf("  [round %d] stagger: waiting %ds before next reviewer ...\n", round, r.StaggerSec)
			time.Sleep(time.Duration(r.StaggerSec) * time.Second)
		}
		go func(pr pendingReview) {
			backend := BackendFor(pr.persona)
			fmt.Printf("  [round %d] reviewing: %s (via %s) ...\n", round, pr.persona.DisplayName, backend.Command)

			review, err := r.runSingleReview(ctx, spec, round, pr.persona, &backend, priorFindings, codebaseCtx)
			ch <- reviewResult{review: review, err: err, persona: pr.persona}
		}(p)
	}

	var reviews []ReviewOutput
	for range pending {
		res := <-ch
		if res.err != nil {
			fmt.Printf("  [round %d] %s: FAILED (%v)\n", round, res.persona.DisplayName, res.err)
			continue
		}

		reviewPath := filepath.Join(r.OutputDir, fmt.Sprintf("review-%s.json", res.persona.PersonaID))
		if err := writeJSON(reviewPath, res.review); err != nil {
			fmt.Printf("  [round %d] %s: write failed (%v)\n", round, res.persona.DisplayName, err)
			continue
		}

		reviews = append(reviews, *res.review)
		fmt.Printf("  [round %d] %s: %s (%d findings)\n", round, res.persona.DisplayName, res.review.Verdict, len(res.review.Findings))
	}

	return reviews
}

func (r *Runner) runSingleReview(ctx context.Context, spec string, round int, persona *Persona, backend *CLIBackend, priorFindings []ReviewOutput, codebaseCtx string) (*ReviewOutput, error) {
	prompt := BuildReviewPrompt(&PromptContext{
		Spec:            spec,
		SpecPath:        r.SpecPath,
		Persona:         *persona,
		Round:           round,
		Mode:            r.Mode,
		PriorFindings:   priorFindings,
		CodebaseContext: codebaseCtx,
	})

	// Initial review.
	output, err := InvokeFunc(ctx, backend, prompt, r.TimeoutSec)
	if err != nil {
		return nil, fmt.Errorf("initial review: %w", err)
	}

	review, err := parseReviewOutput(output)
	if err != nil {
		return nil, fmt.Errorf("parse initial review: %w", err)
	}

	if !r.EnableNudge {
		return review, nil
	}

	// Nudge pass — send follow-up to go deeper.
	nudgePrompt := prompt + "\n\n---\n\nPrevious output:\n```json\n" + output + "\n```\n\n" + BuildNudgePrompt(persona)
	nudgeOutput, nudgeErr := InvokeFunc(ctx, backend, nudgePrompt, r.TimeoutSec)
	if nudgeErr != nil {
		return review, nil //nolint:nilerr // nudge is best-effort; initial review is valid
	}

	nudgeReview, nudgeParseErr := parseReviewOutput(nudgeOutput)
	if nudgeParseErr != nil {
		return review, nil //nolint:nilerr // nudge parse failure; initial review is valid
	}

	// Merge initial + nudge findings. The nudge may return the complete list,
	// only new findings, or a deduplicated subset — we can't assume which.
	// Deduplicate by finding_id, preferring nudge version for conflicts.
	review.Findings = mergeFindings(review.Findings, nudgeReview.Findings)

	// Use the nudge verdict/confidence if it's stricter (higher severity).
	if verdictSeverity(nudgeReview.Verdict) > verdictSeverity(review.Verdict) {
		review.Verdict = nudgeReview.Verdict
	}
	if nudgeReview.Confidence != "" {
		review.Confidence = nudgeReview.Confidence
	}

	return review, nil
}

// mergeFindings combines two finding lists, deduplicating by finding_id.
// For duplicate IDs, the nudge version wins (it's the more considered response).
func mergeFindings(initial, nudge []Finding) []Finding {
	seen := make(map[string]int, len(initial))
	merged := make([]Finding, len(initial))
	copy(merged, initial)
	for i := range merged {
		seen[merged[i].FindingID] = i
	}
	for i := range nudge {
		if idx, exists := seen[nudge[i].FindingID]; exists {
			merged[idx] = nudge[i] // nudge version replaces initial
		} else {
			merged = append(merged, nudge[i])
			seen[nudge[i].FindingID] = len(merged) - 1
		}
	}
	return merged
}

func verdictSeverity(v Verdict) int {
	switch v {
	case VerdictFail:
		return 3
	case VerdictWarn:
		return 2
	case VerdictPass:
		return 1
	default:
		return 0
	}
}

func validatePersonaUniqueness(personas []Persona) error {
	seen := make(map[string]bool, len(personas))
	for i := range personas {
		if seen[personas[i].PersonaID] {
			return fmt.Errorf("%w: duplicate persona_id %q", ErrInvalidPersonaConfig, personas[i].PersonaID)
		}
		seen[personas[i].PersonaID] = true
	}
	return nil
}

func matchesSpecHash(path, currentHash string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(data)) == currentHash
}

func loadCachedReview(path string) (*ReviewOutput, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var review ReviewOutput
	if err := json.Unmarshal(data, &review); err != nil {
		return nil, err
	}
	// A cached review is valid if it has a verdict — persona_id may be empty
	// if the model omitted it from its response.
	if review.Verdict == "" {
		return nil, fmt.Errorf("cached review missing verdict")
	}
	return &review, nil
}

func invokeBackend(ctx context.Context, backend *CLIBackend, prompt string, timeoutSec int) (string, error) {
	timeout := time.Duration(timeoutSec) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := append([]string(nil), backend.Args...)

	// For large prompts, pipe via stdin to avoid OS arg size limits.
	// Claude and Gemini read from stdin when prompt is "-" or piped.
	// Codex reads from stdin when no positional prompt is given.
	useStdin := len(prompt) > 32000

	if !useStdin {
		if backend.PromptFlag != "" {
			args = append(args, backend.PromptFlag, prompt)
		} else {
			args = append(args, prompt)
		}
	}

	cmd := exec.CommandContext(ctx, backend.Command, args...) //nolint:gosec // command is from trusted CLIBackend config, not user input
	cmd.Stderr = os.Stderr

	if useStdin {
		cmd.Stdin = strings.NewReader(prompt)
	}

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
		return nil, fmt.Errorf("%w: %v (raw: %s)", ErrInvalidReviewJSON, err, truncateForError(raw, 200))
	}
	if err := ValidateReviewOutput(&review); err != nil {
		return nil, err
	}
	review.ReviewedAt = time.Now()
	return &review, nil
}

func extractJSON(s string) string {
	// Try code fence extraction, but validate the result parses as JSON.
	// Code fences can break when JSON values contain backticks (e.g., spec markdown).
	if candidate := extractFromCodeFence(s); candidate != "" {
		if json.Valid([]byte(candidate)) {
			return candidate
		}
	}
	// String-aware bracket matching for JSON objects and arrays.
	objIdx := strings.Index(s, "{")
	arrIdx := strings.Index(s, "[")
	idx := objIdx
	if arrIdx >= 0 && (idx < 0 || arrIdx < idx) {
		idx = arrIdx
	}
	if idx >= 0 {
		if result := extractJSONBlock(s, idx); result != "" {
			return result
		}
	}
	return strings.TrimSpace(s)
}

func extractFromCodeFence(s string) string {
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
	return ""
}

func extractJSONBlock(s string, idx int) string {
	open := s[idx]
	closeCh := byte('}')
	if open == '[' {
		closeCh = ']'
	}
	depth := 0
	inStr := false
	esc := false
	for i := idx; i < len(s); i++ {
		ch := s[i]
		if esc {
			esc = false
			continue
		}
		if ch == '\\' && inStr {
			esc = true
			continue
		}
		if ch == '"' {
			inStr = !inStr
			continue
		}
		if inStr {
			continue
		}
		switch ch {
		case open:
			depth++
		case closeCh:
			depth--
			if depth == 0 {
				return s[idx : i+1]
			}
		}
	}
	return ""
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
