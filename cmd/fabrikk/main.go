package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/php-workx/fabrikk/internal/agentcli"
	"github.com/php-workx/fabrikk/internal/councilflow"
	"github.com/php-workx/fabrikk/internal/engine"
	"github.com/php-workx/fabrikk/internal/learning"
	"github.com/php-workx/fabrikk/internal/state"
	"gopkg.in/yaml.v3"
)

// Build-time variables set via ldflags by goreleaser / justfile.
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
)

const (
	commandDraft      = "draft"
	commandReview     = "review"
	commandApprove    = "approve"
	commandBoundaries = "boundaries"
	consentSourceCLI  = "cli"

	fabrikDir            = ".fabrikk"
	flagFrom             = "--from"
	flagFromTechSpec     = "--from-tech-spec"
	completionReportFile = "completion-report.json"
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stderr))
}

func run(ctx context.Context, args []string, stderr io.Writer) int {
	if len(args) < 1 {
		usage(stderr)
		return 0
	}

	if handleRunMetaCommands(args[0], stderr) {
		return 0
	}

	err := dispatchRunCommand(ctx, args)
	if err == nil {
		return 0
	}

	var humanErr *engine.HumanInputRequiredError
	if errors.As(err, &humanErr) {
		writeHumanInputRequired(stderr, args, humanErr)
		return 1
	}
	if strings.HasPrefix(err.Error(), "unknown command:") {
		_, _ = fmt.Fprintf(stderr, "%v\n", err)
		usage(stderr)
		return 1
	}
	_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
	return 1
}

func handleRunMetaCommands(command string, stderr io.Writer) bool {
	switch command {
	case "help", "--help", "-h":
		usage(stderr)
		return true
	case "version", "--version", "-v":
		_, _ = fmt.Fprintf(stderr, "fabrikk %s (%s, %s)\n", Version, GitCommit, BuildDate)
		return true
	default:
		return false
	}
}

func dispatchRunCommand(ctx context.Context, args []string) error {
	handlers := map[string]func([]string) error{
		"prepare":         func(rest []string) error { return cmdPrepare(ctx, rest) },
		commandReview:     func(rest []string) error { return cmdReview(ctx, rest) },
		"artifact":        func(rest []string) error { return cmdArtifact(ctx, rest) },
		"tech-spec":       func(rest []string) error { return cmdTechSpec(ctx, rest) },
		"plan":            func(rest []string) error { return cmdPlan(ctx, rest) },
		commandBoundaries: cmdBoundaries,
		commandApprove:    func(rest []string) error { return cmdApprove(ctx, rest) },
		"status":          func(rest []string) error { return cmdStatus(ctx, rest) },
		"report":          cmdReport,
		"verify":          func(rest []string) error { return cmdVerify(ctx, rest) },
		"retry":           cmdRetry,
		"tasks":           cmdTasks,
		"ready":           cmdReady,
		"blocked":         cmdBlocked,
		"next":            cmdNext,
		"progress":        cmdProgress,
		"learn":           cmdLearn,
		"context":         cmdContext,
	}
	handler, ok := handlers[args[0]]
	if !ok {
		return fmt.Errorf("unknown command: %s", args[0])
	}
	return handler(args[1:])
}

// usage is defined in help.go — renders grouped command help with lipgloss.

// newEngine creates an engine with the TaskStore from taskStoreForRun.
// Single backend-selection rule — both engine and CLI commands use the same store.
func newEngine(wd, runID string) *engine.Engine {
	runDir := state.NewRunDir(wd, runID)
	eng := engine.New(runDir, wd)
	eng.TaskStore = taskStoreForRun(wd, runID)
	eng.LearningEnricher = newLearningStore(wd)
	return eng
}

func newLearningStore(wd string) *learning.Store {
	sharedDir := filepath.Join(wd, fabrikDir, "learnings")
	localDir := resolveLocalLearningDir(wd)
	if localDir != "" {
		return learning.NewStoreWithLocalDir(sharedDir, localDir)
	}
	return learning.NewStore(sharedDir)
}

func resolveLocalLearningDir(wd string) string {
	cmd := exec.Command("git", "rev-parse", "--git-common-dir")
	cmd.Dir = wd
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	gitCommonDir := strings.TrimSpace(string(out))
	if !filepath.IsAbs(gitCommonDir) {
		gitCommonDir = filepath.Join(wd, gitCommonDir)
	}
	return filepath.Join(gitCommonDir, "fabrikk", "learnings")
}

func workDir() (string, error) {
	return os.Getwd()
}

func cmdPrepare(ctx context.Context, args []string) error {
	flags, err := parsePrepareFlags(args)
	if err != nil {
		return err
	}

	wd, err := workDir()
	if err != nil {
		return err
	}
	if err := applyTrustedPrepareConfig(wd, &flags); err != nil {
		return err
	}
	if err := validatePrepareFlags(flags); err != nil {
		return err
	}

	runDir := state.NewRunDir(wd, "placeholder")
	eng := engine.New(runDir, wd)

	result, err := eng.PrepareWithOptions(ctx, flags.specPaths, flags.options)
	if err != nil {
		return err
	}
	if result == nil || result.Artifact == nil {
		if result != nil && result.NextAction != "" {
			printPreparePending(result)
			return nil
		}
		return fmt.Errorf("prepare did not produce a run artifact")
	}
	artifact := result.Artifact

	fmt.Printf("Run prepared: %s\n", artifact.RunID)
	fmt.Printf("Requirements: %d\n", len(artifact.Requirements))
	fmt.Printf("Source specs: %d\n", len(artifact.SourceSpecs))
	printPrepareNormalizationSummary(result)
	if result.NormalizationReview != nil {
		fmt.Printf("Normalization review: %s\n", result.NormalizationReview.Status)
		fmt.Printf("Blocking findings: %d\n", len(result.NormalizationReview.BlockingFindings))
	}
	if artifact.QualityGate != nil {
		fmt.Printf("Quality gate: %s\n", artifact.QualityGate.Command)
	}
	fmt.Printf("\nRun directory: .fabrikk/runs/%s/\n", artifact.RunID)
	fmt.Printf("Next: %s\n", prepareNextAction(result))

	return nil
}

func printPreparePending(result *engine.PrepareResult) {
	fmt.Println("Prepare paused.")
	fmt.Printf("Status: %s\n", result.Status)
	if result.Blocking {
		fmt.Println("Blocking: true")
	}
	fmt.Printf("Next: %s\n", result.NextAction)
}

func printPrepareNormalizationSummary(result *engine.PrepareResult) {
	if result == nil || result.Artifact == nil {
		return
	}
	metadata := result.Artifact.Normalization
	switch {
	case metadata.FallbackDeterministic:
		fmt.Printf("Normalization: deterministic fallback mode=%s\n", metadata.Mode)
		fmt.Println("Warning: deterministic fallback was used")
	case metadata.UsedLLM:
		fmt.Printf("Normalization: llm mode=%s converter=%s verifier=%s\n", metadata.Mode, metadata.ConverterBackend, metadata.VerifierBackend)
	case metadata.UsedDeterministic:
		fmt.Printf("Normalization: deterministic mode=%s\n", metadata.Mode)
	}
}

func prepareNextAction(result *engine.PrepareResult) string {
	if result == nil || result.Artifact == nil {
		return "inspect prepare result"
	}
	if result.Status == engine.PrepareStatusAwaitingArtifactApproval {
		return fmt.Sprintf("fabrikk artifact approve %s", result.RunID)
	}
	if result.Status != engine.PrepareStatusReady && result.NextAction != "" {
		return result.NextAction
	}
	return fmt.Sprintf("fabrikk review %s", result.RunID)
}

type prepareFlags struct {
	specPaths          []string
	options            engine.PrepareOptions
	trustProjectConfig bool
}

func parsePrepareFlags(args []string) (prepareFlags, error) {
	flags := prepareFlags{
		options: engine.DefaultPrepareOptions(),
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--spec":
			value, next, err := requireFlagValue(args, i, "--spec")
			if err != nil {
				return prepareFlags{}, err
			}
			flags.specPaths = append(flags.specPaths, value)
			i = next
		case "--normalize":
			value, next, err := requireFlagValue(args, i, "--normalize")
			if err != nil {
				return prepareFlags{}, err
			}
			switch value {
			case string(state.NormalizationAuto):
				flags.options.NormalizeMode = state.NormalizationAuto
			case string(state.NormalizationAlways):
				flags.options.NormalizeMode = state.NormalizationAlways
			case string(state.NormalizationNever):
				flags.options.NormalizeMode = state.NormalizationNever
			default:
				return prepareFlags{}, fmt.Errorf("unknown --normalize value %q; expected auto, always, or never", value)
			}
			i = next
		case "--allow-llm-normalization":
			flags.options.LLMConsent = true
			flags.options.LLMConsentSource = consentSourceCLI
		case "--trust-project-config":
			flags.trustProjectConfig = true
		case "--fallback-deterministic":
			flags.options.FallbackDeterministic = true
		case "--converter-backend":
			value, next, err := requireFlagValue(args, i, "--converter-backend")
			if err != nil {
				return prepareFlags{}, err
			}
			flags.options.ConverterBackendName = value
			i = next
		case "--verifier-backend":
			value, next, err := requireFlagValue(args, i, "--verifier-backend")
			if err != nil {
				return prepareFlags{}, err
			}
			flags.options.VerifierBackendName = value
			i = next
		default:
			return prepareFlags{}, fmt.Errorf("unknown prepare flag %q", args[i])
		}
	}
	if len(flags.specPaths) == 0 {
		return prepareFlags{}, fmt.Errorf("usage: fabrikk prepare --spec <path> [--spec <path>...]")
	}
	if flags.options.NormalizeMode == state.NormalizationNever && flags.options.FallbackDeterministic {
		return prepareFlags{}, fmt.Errorf("--fallback-deterministic cannot be used with --normalize never")
	}
	return flags, nil
}

func validatePrepareFlags(flags prepareFlags) error {
	if flags.options.NormalizeMode == state.NormalizationAlways && !flags.options.LLMConsent {
		return fmt.Errorf("--normalize always requires --allow-llm-normalization or --trust-project-config with allow_llm_normalization enabled")
	}
	return nil
}

type prepareProjectConfig struct {
	SpecNormalization struct {
		AllowLLMNormalization bool `yaml:"allow_llm_normalization"`
	} `yaml:"spec_normalization"`
	AllowLLMNormalization bool `yaml:"allow_llm_normalization"`
}

func applyTrustedPrepareConfig(wd string, flags *prepareFlags) error {
	if flags == nil || flags.options.LLMConsent || !flags.trustProjectConfig {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(wd, "fabrikk.yaml"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read fabrikk.yaml: %w", err)
	}
	var cfg prepareProjectConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse fabrikk.yaml: %w", err)
	}
	if cfg.SpecNormalization.AllowLLMNormalization || cfg.AllowLLMNormalization {
		flags.options.LLMConsent = true
		flags.options.LLMConsentSource = "config"
	}
	return nil
}

func requireFlagValue(args []string, i int, flag string) (value string, next int, err error) {
	if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
		return "", i, fmt.Errorf("%s requires a value", flag)
	}
	return args[i+1], i + 1, nil
}

func cmdReview(_ context.Context, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: fabrikk review <run-id>")
	}
	runID := args[0]

	wd, err := workDir()
	if err != nil {
		return err
	}

	runDir := state.NewRunDir(wd, runID)
	artifact, err := runDir.ReadArtifact()
	if err != nil {
		return fmt.Errorf("read artifact: %w", err)
	}

	fmt.Printf("=== Run Artifact Review: %s ===\n\n", artifact.RunID)
	fmt.Printf("Schema version: %s\n", artifact.SchemaVersion)
	fmt.Printf("Risk profile:   %s\n", artifact.RiskProfile)

	fmt.Printf("\nSource specs (%d):\n", len(artifact.SourceSpecs))
	for _, s := range artifact.SourceSpecs {
		fmt.Printf("  - %s (sha256:%s…)\n", s.Path, s.Fingerprint[:12])
	}

	fmt.Printf("\nRequirements (%d):\n", len(artifact.Requirements))
	for _, r := range artifact.Requirements {
		fmt.Printf("  %s: %s\n", r.ID, truncate(r.Text, 80))
		if len(r.SourceRefs) > 0 {
			fmt.Printf("    Source refs:\n")
			for _, ref := range r.SourceRefs {
				fmt.Printf("      - %s:%d-%d %s\n", ref.Path, ref.LineStart, ref.LineEnd, truncate(ref.Excerpt, 80))
			}
		}
	}

	printReviewNormalizationEvidence(runDir, artifact)

	if artifact.QualityGate != nil {
		fmt.Printf("\nQuality gate: %s (timeout: %ds, required: %v)\n",
			artifact.QualityGate.Command, artifact.QualityGate.TimeoutSeconds, artifact.QualityGate.Required)
	}

	if artifact.ApprovedAt != nil {
		fmt.Printf("\nApproved at: %s by %s\n", artifact.ApprovedAt.Format("2006-01-02 15:04:05"), artifact.ApprovedBy)
		fmt.Printf("Artifact hash: %s\n", artifact.ArtifactHash)
	} else {
		fmt.Printf("\nStatus: AWAITING APPROVAL\n")
		fmt.Printf("Next: %s\n", reviewNextAction(runDir, artifact))
	}

	return nil
}

func printReviewNormalizationEvidence(runDir *state.RunDir, artifact *state.RunArtifact) {
	metadata := artifact.Normalization
	switch {
	case metadata.UsedLLM:
		fmt.Printf("\nNormalization: llm mode=%s\n", metadata.Mode)
		fmt.Printf("Converter backend: %s\n", metadata.ConverterBackend)
		fmt.Printf("Verifier backend: %s\n", metadata.VerifierBackend)
		fmt.Printf("Source freshness: %s\n", normalizationSourceFreshness(runDir))
		review, err := runDir.ReadSpecNormalizationReview()
		if err != nil {
			fmt.Printf("Normalization review: missing\n")
			return
		}
		fmt.Printf("Normalization review: %s\n", review.Status)
		fmt.Printf("Review summary: %s\n", review.Summary)
		fmt.Printf("Blocking findings: %d\n", len(review.BlockingFindings))
		fmt.Printf("Warnings: %d\n", len(review.Warnings))
		fmt.Printf("reviewed_input_hash: %s\n", review.ReviewedInputHash)
		for _, finding := range review.BlockingFindings {
			fmt.Printf("  - [%s] %s\n", finding.Category, finding.Summary)
			if finding.SuggestedRepair != "" {
				fmt.Printf("    Repair: %s\n", finding.SuggestedRepair)
			}
		}
	case metadata.UsedDeterministic:
		label := "deterministic"
		if metadata.FallbackDeterministic {
			label = "deterministic fallback"
		}
		fmt.Printf("\nNormalization: %s mode=%s\n", label, metadata.Mode)
	}
}

func normalizationSourceFreshness(runDir *state.RunDir) string {
	manifest, err := runDir.ReadSpecNormalizationSourceManifest()
	if err != nil {
		return "unknown"
	}
	for _, source := range manifest.Sources {
		fingerprint, err := state.SHA256File(source.Path)
		if err != nil || fingerprint != source.Fingerprint {
			return "stale"
		}
	}
	return "current"
}

func reviewNextAction(runDir *state.RunDir, artifact *state.RunArtifact) string {
	if artifact.Normalization.UsedLLM {
		review, err := runDir.ReadSpecNormalizationReview()
		if err != nil {
			return "restore or rerun normalization review before approval"
		}
		switch review.Status {
		case state.ReviewPass:
			return fmt.Sprintf("fabrikk artifact approve %s", artifact.RunID)
		case state.ReviewNeedsRevision:
			return fmt.Sprintf("revise the source spec or run fabrikk artifact approve %s --accept-needs-revision after reviewing findings", artifact.RunID)
		case state.ReviewFail:
			return "revise the source spec before approval"
		}
	}
	return fmt.Sprintf("fabrikk approve %s", artifact.RunID)
}

func cmdArtifact(ctx context.Context, args []string) error {
	if len(args) < 2 || args[0] != "approve" {
		return fmt.Errorf("usage: fabrikk artifact approve <run-id> [--accept-needs-revision]")
	}
	runID := args[1]
	options := engine.RunArtifactApprovalOptions{}
	for _, arg := range args[2:] {
		switch arg {
		case "--accept-needs-revision":
			options.AcceptNeedsRevision = true
		default:
			return fmt.Errorf("unknown artifact approve flag %q", arg)
		}
	}
	wd, err := workDir()
	if err != nil {
		return err
	}
	runDir := state.NewRunDir(wd, runID)
	eng := engine.New(runDir, wd)
	approval, err := eng.ApproveRunArtifactWithOptions(ctx, "user", options)
	if err != nil {
		return err
	}
	fmt.Printf("Run artifact approved: %s\n", approval.RunID)
	fmt.Printf("reviewed_input_hash: %s\n", approval.ReviewedInputHash)
	fmt.Printf("Next: fabrikk tech-spec draft %s --from <path>\n", approval.RunID)
	return nil
}

type techSpecFlags struct {
	action      string
	fromPath    string
	runID       string
	noNormalize bool
	remaining   []string
}

func parseTechSpecFlags(args []string) techSpecFlags {
	var f techSpecFlags
	if len(args) > 0 {
		f.action = args[0]
	}
	valuedFlags := map[string]bool{flagFrom: true, "--round": true, "--mode": true, "--persona": true, "--persona-dir": true, "--stagger-delay": true}
	for i := 1; i < len(args); i++ {
		i = parseTechSpecArg(&f, args, i, valuedFlags)
	}
	return f
}

// parseTechSpecArg processes a single argument during tech-spec flag parsing.
// Returns the (possibly advanced) index.
func parseTechSpecArg(f *techSpecFlags, args []string, i int, valuedFlags map[string]bool) int {
	arg := args[i]
	switch {
	case arg == flagFrom:
		i++
		if i < len(args) {
			f.fromPath = args[i]
		}
	case arg == "--no-normalize":
		f.noNormalize = true
	case valuedFlags[arg]:
		f.remaining = append(f.remaining, arg)
		i++
		if i < len(args) {
			f.remaining = append(f.remaining, args[i])
		}
	case strings.HasPrefix(arg, "--"):
		f.remaining = append(f.remaining, arg)
	case f.runID == "":
		f.runID = arg
	default:
		f.remaining = append(f.remaining, arg)
	}
	return i
}

func cmdTechSpec(ctx context.Context, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: fabrikk tech-spec <draft|review|approve> [run-id] [--from <path>] [--no-normalize] [--council] [--force] [--round N] [--stagger-delay N]")
	}

	f := parseTechSpecFlags(args)

	wd, err := workDir()
	if err != nil {
		return err
	}

	// Shortcut: "review --from <path>" without a run-id creates a temporary run.
	if f.action == commandReview && f.runID == "" && f.fromPath != "" {
		return cmdTechSpecReviewFromFile(ctx, wd, f.fromPath, f.noNormalize, f.remaining)
	}

	if f.runID == "" {
		return fmt.Errorf("usage: fabrikk tech-spec <draft|review|approve> <run-id> [--from <path>]")
	}

	runDir := state.NewRunDir(wd, f.runID)
	eng := engine.New(runDir, wd)

	switch f.action {
	case commandDraft:
		fromPath := f.fromPath
		if fromPath == "" {
			fromPath, err = detectTechnicalSpecSource(wd)
			if err != nil {
				return err
			}
		}
		if err := eng.DraftTechnicalSpec(ctx, fromPath, f.noNormalize); err != nil {
			return err
		}
		fmt.Printf("Technical spec recorded: %s\n", runDir.TechnicalSpec())
		return nil
	case commandReview:
		return cmdTechSpecReview(ctx, eng, f.remaining, false)
	case commandApprove:
		approval, err := eng.ApproveTechnicalSpec(ctx, "user")
		if err != nil {
			return err
		}
		fmt.Printf("Technical spec approved: %s (%s)\n", approval.ArtifactPath, approval.ArtifactHash)
		return nil
	default:
		return fmt.Errorf("usage: fabrikk tech-spec <draft|review|approve> [run-id] [--from <path>]")
	}
}

// cmdTechSpecReviewFromFile creates or reuses a run for reviewing a spec file.
// If a previous run exists with the same spec hash, it is reused (reviews/personas cached).
func cmdTechSpecReviewFromFile(ctx context.Context, wd, fromPath string, noNormalize bool, flags []string) error {
	// Hash the spec to find an existing run.
	specData, err := os.ReadFile(fromPath)
	if err != nil {
		return fmt.Errorf("read spec: %w", err)
	}
	specHash := state.SHA256Bytes(specData)

	// Search for existing run with matching spec.
	if runDir, runID := findRunBySpecHash(wd, specHash); runDir != nil {
		fmt.Printf("Reusing run: %s (spec unchanged)\n", runID)
		eng := engine.New(runDir, wd)
		// Re-draft to ensure in-run spec matches source (council may have rewritten it).
		if err := eng.DraftTechnicalSpec(ctx, fromPath, noNormalize); err != nil {
			return fmt.Errorf("re-draft: %w", err)
		}
		return cmdTechSpecReview(ctx, eng, flags, true)
	}

	// Create new run.
	runID := fmt.Sprintf("run-%d", time.Now().Unix())
	runDir := state.NewRunDir(wd, runID)
	if err := runDir.Init(); err != nil {
		return fmt.Errorf("init run dir: %w", err)
	}

	artifact := &state.RunArtifact{
		SchemaVersion: "0.1",
		RunID:         runID,
		SourceSpecs:   []state.SourceSpec{{Path: fromPath, Fingerprint: specHash}},
	}
	if err := runDir.WriteArtifact(artifact); err != nil {
		return fmt.Errorf("write artifact: %w", err)
	}
	if err := runDir.WriteStatus(&state.RunStatus{
		RunID:              runID,
		State:              state.RunAwaitingApproval,
		LastTransitionTime: time.Now(),
		TaskCountsByState:  map[string]int{},
	}); err != nil {
		return fmt.Errorf("write status: %w", err)
	}

	eng := engine.New(runDir, wd)
	fmt.Printf("Run created: %s\n", runID)

	if err := eng.DraftTechnicalSpec(ctx, fromPath, noNormalize); err != nil {
		return fmt.Errorf("draft: %w", err)
	}

	return cmdTechSpecReview(ctx, eng, flags, true)
}

// findRunBySpecHash scans existing runs for one whose source spec matches the given hash.
// Returns the most recent matching run, or nil if none found.
func findRunBySpecHash(wd, specHash string) (runDir *state.RunDir, runID string) {
	runsDir := filepath.Join(wd, fabrikDir, "runs")
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		return nil, ""
	}

	// Iterate newest first (entries are sorted alphabetically; run-<timestamp> sorts chronologically).
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if !e.IsDir() {
			continue
		}
		runDir := state.NewRunDir(wd, e.Name())
		artifact, err := runDir.ReadArtifact()
		if err != nil {
			continue
		}
		for _, src := range artifact.SourceSpecs {
			if src.Fingerprint == specHash {
				return runDir, e.Name()
			}
		}
	}
	return nil, ""
}

// reviewFlags holds parsed flags for the tech-spec review command.
type reviewFlags struct {
	structuralOnly bool
	council        bool
	dryRun         bool
	force          bool
	skipApproval   bool
	rounds         int
	staggerDelay   int
	mode           councilflow.ReviewMode
	personaPaths   []string // repeatable --persona <path>
	personaDirs    []string // repeatable --persona-dir <dir>
}

// parseReviewFlags extracts review-specific flags from the argument list.
func parseReviewFlags(flags []string) (reviewFlags, error) {
	rf := reviewFlags{rounds: 2, staggerDelay: -1, mode: councilflow.ReviewStandard}
	for i := 0; i < len(flags); i++ {
		switch flags[i] {
		case "--structural-only":
			rf.structuralOnly = true
		case "--council":
			rf.council = true
		case "--dry-run":
			rf.dryRun = true
		case "--force":
			rf.force = true
		case "--skip-approval":
			rf.skipApproval = true
		case "--round":
			value, next, err := reviewFlagValue(flags, i, "--round")
			if err != nil {
				return rf, err
			}
			rounds, err := strconv.Atoi(value)
			if err != nil || rounds <= 0 {
				return rf, fmt.Errorf("invalid round count %q (use a positive integer)", value)
			}
			rf.rounds = rounds
			i = next
		case "--mode":
			value, next, err := reviewFlagValue(flags, i, "--mode")
			if err != nil {
				return rf, err
			}
			m := councilflow.ReviewMode(value)
			if !councilflow.ValidReviewModes[m] {
				return rf, fmt.Errorf("invalid review mode %q (use: mvp, standard, production)", value)
			}
			rf.mode = m
			i = next
		case "--stagger-delay":
			value, next, err := reviewFlagValue(flags, i, "--stagger-delay")
			if err != nil {
				return rf, fmt.Errorf("--stagger-delay requires a value in seconds")
			}
			seconds, err := strconv.Atoi(value)
			if err != nil || seconds < 0 {
				return rf, fmt.Errorf("invalid stagger delay %q (use a non-negative number of seconds)", value)
			}
			rf.staggerDelay = seconds
			i = next
		case "--persona":
			value, next, err := reviewFlagValue(flags, i, "--persona")
			if err != nil {
				return rf, err
			}
			rf.personaPaths = append(rf.personaPaths, value)
			i = next
		case "--persona-dir":
			value, next, err := reviewFlagValue(flags, i, "--persona-dir")
			if err != nil {
				return rf, err
			}
			rf.personaDirs = append(rf.personaDirs, value)
			i = next
		}
	}
	return rf, nil
}

func reviewFlagValue(flags []string, index int, flag string) (value string, next int, err error) {
	values := flags[index+1:]
	if len(values) == 0 || strings.HasPrefix(values[0], "-") {
		return "", index, fmt.Errorf("%s requires a value", flag)
	}
	return values[0], index + 1, nil
}

// runStructuralReview runs the pre-flight structural review and returns whether
// council review should proceed. Returns an error if the review fails.
func runStructuralReview(ctx context.Context, eng *engine.Engine, structuralOnly bool) (proceed bool, err error) {
	review, err := eng.ReviewTechnicalSpec(ctx)
	if err != nil {
		return false, err
	}
	data, _ := json.MarshalIndent(review, "", "  ")
	fmt.Println(string(data))

	if structuralOnly {
		return false, nil
	}
	if review.Status != state.ReviewPass {
		return false, fmt.Errorf("structural review failed — fix before running council")
	}
	return true, nil
}

// postCouncilLearnings extracts learnings and runs maintenance after a council review.
func postCouncilLearnings(eng *engine.Engine, result *councilflow.CouncilResult) {
	extractLearningsFromCouncil(eng.WorkDir, result, filepath.Base(eng.RunDir.Root))
	_ = eng.RunDir.AppendEvent(state.Event{
		Timestamp: time.Now(),
		Type:      "learning_extraction",
		RunID:     filepath.Base(eng.RunDir.Root),
		Detail:    fmt.Sprintf("source=council rounds=%d", len(result.Rounds)),
	})
	learnStore := newLearningStore(eng.WorkDir)
	_, _ = learnStore.Maintain(90 * 24 * time.Hour)
}

func cmdTechSpecReview(ctx context.Context, eng *engine.Engine, flags []string, externalSpec bool) error {
	rf, err := parseReviewFlags(flags)
	if err != nil {
		return err
	}

	// Structural review (pre-flight). Skipped for external specs that don't follow our template.
	if !externalSpec {
		proceed, sErr := runStructuralReview(ctx, eng, rf.structuralOnly)
		if sErr != nil {
			return sErr
		}
		if !proceed {
			return nil
		}
	}
	if !rf.council {
		if externalSpec {
			fmt.Println("Council review skipped; pass --council to review external specs.")
		}
		return nil
	}

	// Start daemon for judge context accumulation.
	daemon, daemonCleanup := startJudgeDaemon(ctx, eng)
	defer daemonCleanup()
	eng.InvokeFn = daemon

	cfg := councilflow.DefaultConfig()
	cfg.Rounds = rf.rounds
	cfg.Mode = rf.mode
	cfg.DryRun = rf.dryRun
	cfg.Force = rf.force
	cfg.SkipApproval = rf.skipApproval
	cfg.JudgeInvokeFn = daemon
	if rf.staggerDelay >= 0 {
		cfg.StaggerDelay = rf.staggerDelay
	}

	// Load custom personas from --persona / --persona-dir flags.
	customPersonas, cpErr := loadCustomPersonas(rf)
	if cpErr != nil {
		return cpErr
	}
	cfg.CustomPersonas = customPersonas

	// Inject prevention checks from high-effectiveness learnings.
	learnStore := newLearningStore(eng.WorkDir)
	if prevention := learnStore.LoadPreventionContext(nil); prevention != "" {
		cfg.CodebaseContext += prevention
	}

	result, err := eng.CouncilReviewTechnicalSpec(ctx, cfg)
	if err != nil {
		return err
	}

	printCouncilVerdict(result)

	// Extract learnings from council findings (Phase 2) — skip during dry-run.
	if !rf.dryRun {
		postCouncilLearnings(eng, result)
	}

	return nil
}

// printCouncilVerdict outputs the council review verdict summary.
func printCouncilVerdict(result *councilflow.CouncilResult) {
	fmt.Printf("\nCouncil verdict: %s\n", result.OverallVerdict)
	for i := range result.Rounds {
		round := &result.Rounds[i]
		for j := range round.Reviews {
			r := &round.Reviews[j]
			fmt.Printf("  [round %d] %s: %s (%d findings)\n", round.Round, r.PersonaID, r.Verdict, len(r.Findings))
		}
	}
}

// startJudgeDaemon starts a persistent Claude daemon for judge context accumulation.
// Returns the daemon's InvokeFn (nil if daemon failed to start) and a cleanup function.
func startJudgeDaemon(ctx context.Context, eng *engine.Engine) (invokeFn agentcli.InvokeFn, cleanup func()) {
	noop := func() { /* no daemon to stop */ }
	runID := filepath.Base(eng.RunDir.Root)
	if runID == "" {
		return nil, noop
	}
	daemon := agentcli.NewDaemon(agentcli.DaemonConfig{
		Backend:   agentcli.KnownBackends[agentcli.BackendClaude],
		SocketDir: os.TempDir(),
		RunID:     runID,
	})
	if err := daemon.Start(ctx); err != nil {
		fmt.Printf("  daemon start failed (falling back to one-shot): %v\n", err)
		return nil, noop
	}
	return daemon.QueryFunc(), func() { _ = daemon.Stop() }
}

// loadCustomPersonas loads custom persona prompt files from --persona and --persona-dir flags.
func loadCustomPersonas(rf reviewFlags) ([]councilflow.Persona, error) {
	var personas []councilflow.Persona
	for _, path := range rf.personaPaths {
		p, err := councilflow.LoadCustomPersona(path)
		if err != nil {
			return nil, fmt.Errorf("load persona %s: %w", path, err)
		}
		personas = append(personas, p)
	}
	for _, dir := range rf.personaDirs {
		dirPersonas, err := councilflow.LoadCustomPersonasFromDir(dir)
		if err != nil {
			return nil, fmt.Errorf("load persona dir %s: %w", dir, err)
		}
		personas = append(personas, dirPersonas...)
	}
	if len(personas) > 0 {
		fmt.Printf("  custom personas: %d loaded\n", len(personas))
	}
	return personas, nil
}

func cmdPlan(ctx context.Context, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: fabrikk plan <draft|review|approve> <run-id>")
	}

	action := args[0]
	runID := args[1]

	wd, err := workDir()
	if err != nil {
		return err
	}

	runDir := state.NewRunDir(wd, runID)
	eng := engine.New(runDir, wd)

	switch action {
	case commandDraft:
		plan, err := eng.DraftExecutionPlan(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("Execution plan recorded: %s (%d slices)\n", runDir.ExecutionPlan(), len(plan.Slices))
		return nil
	case commandReview:
		rf, err := parseReviewFlags(args[2:])
		if err != nil {
			return err
		}
		review, err := eng.ReviewExecutionPlan(ctx)
		if err != nil {
			return err
		}
		if review.Status != state.ReviewPass || rf.structuralOnly || !rf.council {
			data, _ := json.MarshalIndent(review, "", "  ")
			fmt.Println(string(data))
			return nil
		}
		daemon, daemonCleanup := startJudgeDaemon(ctx, eng)
		defer daemonCleanup()
		eng.InvokeFn = daemon
		cfg := councilflow.DefaultConfig()
		cfg.Rounds = 1
		cfg.Mode = councilflow.ReviewMVP
		cfg.SkipApproval = true
		cfg.DryRun = rf.dryRun
		cfg.Force = rf.force
		if rf.rounds != 2 {
			cfg.Rounds = rf.rounds
		}
		if rf.mode != councilflow.ReviewStandard {
			cfg.Mode = rf.mode
		}
		if rf.staggerDelay >= 0 {
			cfg.StaggerDelay = rf.staggerDelay
		}
		cfg.SkipJudge = true
		cfg.JudgeInvokeFn = daemon
		customPersonas, cpErr := loadCustomPersonas(rf)
		if cpErr != nil {
			return cpErr
		}
		cfg.CustomPersonas = customPersonas
		result, err := eng.CouncilReviewExecutionPlan(ctx, cfg)
		if err != nil {
			return err
		}
		finalReview, err := eng.RunDir.ReadExecutionPlanReview()
		if err != nil {
			return fmt.Errorf("read execution plan review: %w", err)
		}
		data, _ := json.MarshalIndent(finalReview, "", "  ")
		fmt.Println(string(data))
		printCouncilVerdict(result)
		if result.OverallVerdict == councilflow.VerdictFail {
			return fmt.Errorf("execution plan council review failed")
		}
		return nil
	case commandApprove:
		approval, err := eng.ApproveExecutionPlan(ctx, "user")
		if err != nil {
			return err
		}
		fmt.Printf("Execution plan approved: %s (%s)\n", approval.ArtifactPath, approval.ArtifactHash)
		return nil
	default:
		return fmt.Errorf("usage: fabrikk plan <draft|review|approve> <run-id>")
	}
}

func cmdBoundaries(args []string) error {
	if len(args) < 2 || args[0] != "set" {
		return fmt.Errorf("usage: fabrikk boundaries set <run-id> [--always <item>] [--ask-first <item>] [--never <item>] [--from-tech-spec]")
	}

	runID := args[1]
	var (
		fromTechSpec bool
		direct       state.Boundaries
	)
	for i := 2; i < len(args); i++ {
		switch args[i] {
		case "--always":
			value, next, err := boundaryFlagValue(args, i, "--always")
			if err != nil {
				return err
			}
			direct.Always = append(direct.Always, value)
			i = next
		case "--ask-first":
			value, next, err := boundaryFlagValue(args, i, "--ask-first")
			if err != nil {
				return err
			}
			direct.AskFirst = append(direct.AskFirst, value)
			i = next
		case "--never":
			value, next, err := boundaryFlagValue(args, i, "--never")
			if err != nil {
				return err
			}
			direct.Never = append(direct.Never, value)
			i = next
		case flagFromTechSpec:
			fromTechSpec = true
		default:
			return fmt.Errorf("unknown flag: %s", args[i])
		}
	}

	if fromTechSpec && hasBoundaryValues(direct) {
		return fmt.Errorf("use either direct boundary flags or %s", flagFromTechSpec)
	}
	if !fromTechSpec && !hasBoundaryValues(direct) {
		return fmt.Errorf("usage: fabrikk boundaries set <run-id> [--always <item>] [--ask-first <item>] [--never <item>] [--from-tech-spec]")
	}

	wd, err := workDir()
	if err != nil {
		return err
	}
	runDir := state.NewRunDir(wd, runID)
	artifact, err := runDir.ReadArtifact()
	if err != nil {
		return fmt.Errorf("read artifact: %w", err)
	}

	boundaries := normalizeCLIboundaries(direct)
	if fromTechSpec {
		techSpec, err := runDir.ReadTechnicalSpec()
		if err != nil {
			return fmt.Errorf("read technical spec: %w", err)
		}
		boundaries, err = parseBoundariesFromTechSpec(techSpec)
		if err != nil {
			return fmt.Errorf("parse technical spec boundaries: %w", err)
		}
	}

	artifact.Boundaries = boundaries
	if err := runDir.WriteArtifact(artifact); err != nil {
		return fmt.Errorf("write artifact: %w", err)
	}

	fmt.Printf("Boundaries updated: %s\n", runID)
	return nil
}

func boundaryFlagValue(args []string, index int, flag string) (value string, next int, err error) {
	if index+1 >= len(args) {
		return "", index, fmt.Errorf("missing value for %s", flag)
	}
	if strings.HasPrefix(args[index+1], "-") {
		return "", index, fmt.Errorf("missing value for %s", flag)
	}
	return args[index+1], index + 1, nil
}

func hasBoundaryValues(boundaries state.Boundaries) bool {
	return len(boundaries.Always) > 0 || len(boundaries.AskFirst) > 0 || len(boundaries.Never) > 0
}

func normalizeCLIboundaries(boundaries state.Boundaries) state.Boundaries {
	return state.Boundaries{
		Always:   trimNonEmpty(boundaries.Always),
		AskFirst: trimNonEmpty(boundaries.AskFirst),
		Never:    trimNonEmpty(boundaries.Never),
	}.Normalized()
}

func trimNonEmpty(items []string) []string {
	trimmed := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		trimmed = append(trimmed, item)
	}
	return trimmed
}

func parseBoundariesFromTechSpec(data []byte) (state.Boundaries, error) {
	var (
		boundaries state.Boundaries
		inSection  bool
		sawHeading bool
		current    *[]string
	)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), len(data)+1)
	for scanner.Scan() {
		rawLine := strings.TrimSuffix(scanner.Text(), "\r")
		line := strings.TrimSpace(rawLine)
		if !inSection {
			if line == "## Boundaries" {
				inSection = true
			}
			continue
		}
		if strings.HasPrefix(line, "## ") {
			break
		}
		switch line {
		case "**Always:**":
			current = &boundaries.Always
			sawHeading = true
			continue
		case "**Ask First:**":
			current = &boundaries.AskFirst
			sawHeading = true
			continue
		case "**Never:**":
			current = &boundaries.Never
			sawHeading = true
			continue
		}
		if current == nil || !strings.HasPrefix(line, "-") {
			continue
		}
		item := strings.TrimSpace(strings.TrimPrefix(line, "-"))
		if item == "" {
			continue
		}
		*current = append(*current, item)
	}
	if err := scanner.Err(); err != nil {
		return state.Boundaries{}, fmt.Errorf("scan technical spec boundaries: %w", err)
	}
	if !inSection {
		return state.Boundaries{}, fmt.Errorf("technical spec missing ## Boundaries section")
	}
	if !sawHeading {
		return state.Boundaries{}, fmt.Errorf("## Boundaries section must contain **Always:**, **Ask First:**, or **Never:** headings")
	}
	return normalizeCLIboundaries(boundaries), nil
}

func writeHumanInputRequired(w io.Writer, args []string, err *engine.HumanInputRequiredError) {
	if len(args) >= 3 && args[0] == "plan" && args[1] == "draft" {
		_, _ = fmt.Fprintln(w, "Plan draft requires human input for the following items:")
		for _, item := range err.Items {
			_, _ = fmt.Fprintf(w, "  - %s\n", item)
		}
		_, _ = fmt.Fprintln(w, "Refresh boundaries from your tech spec and re-run:")
		_, _ = fmt.Fprintf(w, "  fabrikk boundaries set %s --from-tech-spec\n", args[2])
		_, _ = fmt.Fprintf(w, "  fabrikk plan draft %s\n", args[2])
		return
	}

	_, _ = fmt.Fprintln(w, "Human input required for the following items:")
	for _, item := range err.Items {
		_, _ = fmt.Fprintf(w, "  - %s\n", item)
	}
}

func cmdApprove(ctx context.Context, args []string) error {
	var runID string
	var launch bool

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--launch":
			launch = true
		default:
			if runID == "" {
				runID = args[i]
			}
		}
	}
	if runID == "" {
		return fmt.Errorf("usage: fabrikk approve <run-id> [--launch]")
	}

	wd, err := workDir()
	if err != nil {
		return err
	}

	eng := newEngine(wd, runID)

	if err := eng.Approve(ctx); err != nil {
		return err
	}

	// Compile tasks.
	result, err := eng.Compile(ctx)
	if err != nil {
		return err
	}

	fmt.Printf("Run %s approved and compiled.\n", runID)
	fmt.Printf("Tasks: %d\n", len(result.Tasks))
	fmt.Printf("Coverage: %d requirements mapped\n", len(result.Coverage))

	for i := range result.Tasks {
		fmt.Printf("  [%s] %s (reqs: %s)\n", result.Tasks[i].TaskID, result.Tasks[i].Title, strings.Join(result.Tasks[i].RequirementIDs, ", "))
	}

	if launch {
		fmt.Printf("\n--launch: detached execution not yet implemented (Phase 4)\n")
	}

	return nil
}

func cmdStatus(_ context.Context, args []string) error {
	wd, err := workDir()
	if err != nil {
		return err
	}

	if len(args) == 0 {
		return listAllRuns(wd)
	}

	return showRunStatus(wd, args[0])
}

// listAllRuns displays a summary of all runs in the project.
func listAllRuns(wd string) error {
	runsDir := filepath.Join(wd, fabrikDir, "runs")
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		return fmt.Errorf("no runs found (is this a fabrikk project?)")
	}
	fmt.Println("Runs:")
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		eng := newEngine(wd, e.Name())
		status, err := eng.ReconcileRunStatus()
		if err != nil {
			fmt.Printf("  %s (status unreadable)\n", e.Name())
			continue
		}
		fmt.Printf("  %s  state=%s\n", status.RunID, status.State)
	}
	showLatestHandoff(wd, "")
	showLearningHealth(wd)
	return nil
}

// showRunStatus displays detailed status for a single run.
func showRunStatus(wd, runID string) error {
	eng := newEngine(wd, runID)
	status, err := eng.ReconcileRunStatus()
	if err != nil {
		return fmt.Errorf("read status: %w", err)
	}

	fmt.Printf("Run: %s\n", status.RunID)
	fmt.Printf("State: %s\n", status.State)
	fmt.Printf("Last transition: %s\n", status.LastTransitionTime.Format("2006-01-02 15:04:05"))

	if len(status.TaskCountsByState) > 0 {
		fmt.Println("Tasks:")
		for st, count := range status.TaskCountsByState {
			fmt.Printf("  %s: %d\n", st, count)
		}
	}

	showLatestHandoff(wd, runID)
	return nil
}

// parseReportArgs parses the report command arguments, returning runID, taskID, and fromPath.
func parseReportArgs(args []string) (runID, taskID, fromPath string, err error) {
	if len(args) < 3 {
		return "", "", "", fmt.Errorf("usage: fabrikk report <run-id> <task-id> --from <path>")
	}
	runID = args[0]
	taskID = args[1]
	for i := 2; i < len(args); i++ {
		if args[i] == flagFrom && i+1 < len(args) {
			fromPath = args[i+1]
			i++
		}
	}
	if fromPath == "" {
		return "", "", "", fmt.Errorf("usage: fabrikk report <run-id> <task-id> --from <path>")
	}
	return runID, taskID, fromPath, nil
}

// loadAndValidateReport reads a completion report from disk and validates it against the taskID.
func loadAndValidateReport(fromPath, taskID string) (state.CompletionReport, error) {
	var report state.CompletionReport
	if err := state.ReadJSON(fromPath, &report); err != nil {
		return report, fmt.Errorf("read completion report: %w", err)
	}
	if report.TaskID == "" {
		report.TaskID = taskID
	}
	if report.TaskID != taskID {
		return report, fmt.Errorf("completion report task_id %q does not match %q", report.TaskID, taskID)
	}
	if report.AttemptID == "" {
		report.AttemptID = "manual-report"
	}
	return report, nil
}

func cmdReport(args []string) error {
	runID, taskID, fromPath, err := parseReportArgs(args)
	if err != nil {
		return err
	}

	wd, err := workDir()
	if err != nil {
		return err
	}

	eng := newEngine(wd, runID)
	if err := requireTaskExists(eng.TaskStore, runID, taskID); err != nil {
		return err
	}

	report, err := loadAndValidateReport(fromPath, taskID)
	if err != nil {
		return err
	}

	runDir := state.NewRunDir(wd, runID)
	reportPath := filepath.Join(runDir.ReportDir(taskID, report.AttemptID), completionReportFile)
	if err := state.WriteJSON(reportPath, &report); err != nil {
		return fmt.Errorf("write completion report: %w", err)
	}
	_ = runDir.AppendEvent(state.Event{
		Timestamp: time.Now(),
		Type:      "completion_report_recorded",
		RunID:     runID,
		TaskID:    taskID,
		Detail:    fmt.Sprintf("attempt=%s", report.AttemptID),
	})

	fmt.Printf("Completion report recorded: %s\n", reportPath)
	return nil
}

// requireTaskExists checks that a task exists in the store, returning an error if not found.
func requireTaskExists(store state.TaskStore, runID, taskID string) error {
	tasks, err := store.ReadTasks(runID)
	if err != nil && !errors.Is(err, state.ErrPartialRead) {
		return fmt.Errorf("read tasks: %w", err)
	}
	for i := range tasks {
		if tasks[i].TaskID == taskID {
			return nil
		}
	}
	return fmt.Errorf("task %s not found", taskID)
}

// findTask looks up a task by ID from the store, returning a pointer or an error.
func findTask(store state.TaskStore, runID, taskID string) (*state.Task, error) {
	tasks, err := store.ReadTasks(runID)
	if err != nil && !errors.Is(err, state.ErrPartialRead) {
		return nil, fmt.Errorf("read tasks: %w", err)
	}
	for i := range tasks {
		if tasks[i].TaskID == taskID {
			return &tasks[i], nil
		}
	}
	return nil, fmt.Errorf("task %s not found", taskID)
}

// handleVerifyPass runs post-verification steps for a passing result.
func handleVerifyPass(wd string) {
	learnStore := newLearningStore(wd)
	report, mErr := learnStore.Maintain(90 * 24 * time.Hour)
	if mErr != nil || report.Skipped {
		return
	}
	if report.Merged > 0 || report.AutoExpired > 0 || report.GCRemoved > 0 {
		fmt.Printf("Learning maintenance: %d merged, %d expired, %d removed\n",
			report.Merged, report.AutoExpired, report.GCRemoved)
	}
}

// handleVerifyFail runs post-verification steps for a failing result.
func handleVerifyFail(eng *engine.Engine, result *state.VerifierResult, task *state.Task) {
	for _, f := range result.BlockingFindings {
		fmt.Printf("  [%s] %s: %s\n", f.Severity, f.Category, f.Summary)
	}
	// Extract learnings from verification failures (Phase 2)
	extractLearningsFromVerifier(eng.WorkDir, result, task)
	_ = eng.RunDir.AppendEvent(state.Event{
		Timestamp: time.Now(),
		Type:      "learning_extraction",
		RunID:     filepath.Base(eng.RunDir.Root),
		TaskID:    task.TaskID,
		Detail:    fmt.Sprintf("source=verifier findings=%d", len(result.BlockingFindings)),
	})
}

func cmdVerify(ctx context.Context, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: fabrikk verify <run-id> <task-id>")
	}
	runID := args[0]
	taskID := args[1]

	wd, err := workDir()
	if err != nil {
		return err
	}

	eng := newEngine(wd, runID)

	task, err := findTask(eng.TaskStore, runID, taskID)
	if err != nil {
		return err
	}

	// Read completion report: check task-level path (backward compat),
	// then scan attempt subdirectories for the latest report.
	var report state.CompletionReport
	if !readLatestCompletionReport(eng.RunDir, taskID, &report) {
		report = state.CompletionReport{
			TaskID:    taskID,
			AttemptID: "manual-verify",
		}
	}

	result, err := eng.VerifyTask(ctx, task, &report)
	if err != nil {
		return err
	}

	data, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(data))

	if result.Pass {
		fmt.Println("\nVerification: PASS")
		handleVerifyPass(wd)
		return nil
	}
	fmt.Println("\nVerification: FAIL")
	handleVerifyFail(eng, result, task)
	return fmt.Errorf("verification failed for %s", taskID)
}

func cmdRetry(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: fabrikk retry <run-id> <task-id>")
	}

	runID := args[0]
	taskID := args[1]

	wd, err := workDir()
	if err != nil {
		return err
	}

	eng := newEngine(wd, runID)
	if err := eng.RetryTask(taskID); err != nil {
		return err
	}

	fmt.Printf("Task requeued: %s\n", taskID)
	return nil
}

func readLatestCompletionReport(runDir *state.RunDir, taskID string, report *state.CompletionReport) bool {
	// Try task-level path first (backward compat).
	taskPath := filepath.Join(runDir.ReportDir(taskID), completionReportFile)
	if state.ReadJSON(taskPath, report) == nil {
		return true
	}
	// Scan attempt subdirectories.
	entries, err := os.ReadDir(runDir.ReportDir(taskID))
	if err != nil {
		return false
	}
	for i := len(entries) - 1; i >= 0; i-- {
		if !entries[i].IsDir() {
			continue
		}
		attemptPath := filepath.Join(runDir.ReportDir(taskID, entries[i].Name()), completionReportFile)
		if state.ReadJSON(attemptPath, report) == nil {
			return true
		}
	}
	return false
}

func truncate(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

func detectTechnicalSpecSource(workDir string) (string, error) {
	matches, err := filepath.Glob(filepath.Join(workDir, "docs", "specs", "*technical-spec*.md"))
	if err != nil {
		return "", fmt.Errorf("find technical spec source: %w", err)
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("no technical spec source found; pass --from <path>")
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("multiple technical spec sources found; pass --from <path>")
	}
	return matches[0], nil
}
