package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/runger/attest/internal/councilflow"
	"github.com/runger/attest/internal/engine"
	"github.com/runger/attest/internal/state"
)

const (
	commandReview  = "review"
	commandApprove = "approve"
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stderr))
}

func run(ctx context.Context, args []string, stderr io.Writer) int {
	if len(args) < 1 {
		usage(stderr)
		return 1
	}

	var err error

	switch args[0] {
	case "prepare":
		err = cmdPrepare(ctx, args[1:])
	case commandReview:
		err = cmdReview(ctx, args[1:])
	case "tech-spec":
		err = cmdTechSpec(ctx, args[1:])
	case "plan":
		err = cmdPlan(ctx, args[1:])
	case commandApprove:
		err = cmdApprove(ctx, args[1:])
	case "status":
		err = cmdStatus(ctx, args[1:])
	case "report":
		err = cmdReport(args[1:])
	case "verify":
		err = cmdVerify(ctx, args[1:])
	case "retry":
		err = cmdRetry(args[1:])
	case "tasks":
		err = cmdTasks(args[1:])
	case "ready":
		err = cmdReady(args[1:])
	case "blocked":
		err = cmdBlocked(args[1:])
	case "next":
		err = cmdNext(args[1:])
	case "progress":
		err = cmdProgress(args[1:])
	default:
		_, _ = fmt.Fprintf(stderr, "unknown command: %s\n", args[0])
		usage(stderr)
		return 1
	}

	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	return 0
}

func usage(w io.Writer) {
	_, _ = fmt.Fprintln(w, `usage: attest <command> [args]

commands:
  prepare --spec <path> [--spec <path>...]   Ingest specs and create a draft run
  review <run-id>                            Show the run artifact for review
  tech-spec <draft|review|approve> ...       Manage run-scoped technical specs
                                              review --from <path>  (one-step council review)
                                              review flags: --mode mvp|standard|production --skip-approval --structural-only --dry-run --force --round N
  plan <draft|review|approve> ...            Manage run-scoped execution plans
  approve <run-id> [--launch]                 Approve and compile tasks
  status [<run-id>]                          Show run status
  report <run-id> <task-id> --from <path>    Import a completion report JSON for a task
  verify <run-id> <task-id>                  Run deterministic verification
  retry <run-id> <task-id>                   Requeue a blocked task for another attempt
  tasks <run-id> [--status X] [--json]       Query tasks with filters
  ready <run-id> [--json]                    Show dispatchable tasks
  blocked <run-id> [--json]                  Show blocked tasks
  next <run-id> [--json]                     Show next task to work on
  progress <run-id> [--json]                 Show run progress`)
}

func workDir() (string, error) {
	return os.Getwd()
}

func cmdPrepare(ctx context.Context, args []string) error {
	var specPaths []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--spec" && i+1 < len(args) {
			specPaths = append(specPaths, args[i+1])
			i++
		}
	}
	if len(specPaths) == 0 {
		return fmt.Errorf("usage: attest prepare --spec <path> [--spec <path>...]")
	}

	wd, err := workDir()
	if err != nil {
		return err
	}

	runDir := state.NewRunDir(wd, "placeholder")
	eng := engine.New(runDir, wd)

	artifact, err := eng.Prepare(ctx, specPaths)
	if err != nil {
		return err
	}

	fmt.Printf("Run prepared: %s\n", artifact.RunID)
	fmt.Printf("Requirements: %d\n", len(artifact.Requirements))
	fmt.Printf("Source specs: %d\n", len(artifact.SourceSpecs))
	if artifact.QualityGate != nil {
		fmt.Printf("Quality gate: %s\n", artifact.QualityGate.Command)
	}
	fmt.Printf("\nRun directory: .attest/runs/%s/\n", artifact.RunID)
	fmt.Printf("Next: attest review %s\n", artifact.RunID)

	return nil
}

func cmdReview(_ context.Context, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: attest review <run-id>")
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
	}

	if artifact.QualityGate != nil {
		fmt.Printf("\nQuality gate: %s (timeout: %ds, required: %v)\n",
			artifact.QualityGate.Command, artifact.QualityGate.TimeoutSeconds, artifact.QualityGate.Required)
	}

	if artifact.ApprovedAt != nil {
		fmt.Printf("\nApproved at: %s by %s\n", artifact.ApprovedAt.Format("2006-01-02 15:04:05"), artifact.ApprovedBy)
		fmt.Printf("Artifact hash: %s\n", artifact.ArtifactHash)
	} else {
		fmt.Printf("\nStatus: AWAITING APPROVAL\n")
		fmt.Printf("Next: attest approve %s\n", artifact.RunID)
	}

	return nil
}

func cmdTechSpec(ctx context.Context, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: attest tech-spec <draft|review|approve> [run-id] [--from <path>] [--council] [--force] [--round N]")
	}

	action := args[0]

	// Parse --from from remaining args (needed for both draft and review shortcut).
	var fromPath, runID string
	var remaining []string
	// Flags that consume the next arg as their value.
	valuedFlags := map[string]bool{"--from": true, "--round": true, "--mode": true}
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg == "--from" {
			i++
			if i < len(args) {
				fromPath = args[i]
			}
			continue
		}
		if valuedFlags[arg] {
			remaining = append(remaining, arg)
			i++
			if i < len(args) {
				remaining = append(remaining, args[i])
			}
			continue
		}
		if strings.HasPrefix(arg, "--") {
			remaining = append(remaining, arg)
			continue
		}
		if runID == "" {
			runID = arg
			continue
		}
		remaining = append(remaining, arg)
	}

	wd, err := workDir()
	if err != nil {
		return err
	}

	// Shortcut: "review --from <path>" without a run-id creates a temporary run.
	if action == commandReview && runID == "" && fromPath != "" {
		return cmdTechSpecReviewFromFile(ctx, wd, fromPath, remaining)
	}

	if runID == "" {
		return fmt.Errorf("usage: attest tech-spec <draft|review|approve> <run-id> [--from <path>]")
	}

	runDir := state.NewRunDir(wd, runID)
	eng := engine.New(runDir, wd)

	switch action {
	case "draft":
		if fromPath == "" {
			fromPath, err = detectTechnicalSpecSource(wd)
			if err != nil {
				return err
			}
		}
		if err := eng.DraftTechnicalSpec(ctx, fromPath); err != nil {
			return err
		}
		fmt.Printf("Technical spec recorded: %s\n", runDir.TechnicalSpec())
		return nil
	case commandReview:
		return cmdTechSpecReview(ctx, eng, remaining, false)
	case commandApprove:
		approval, err := eng.ApproveTechnicalSpec(ctx, "user")
		if err != nil {
			return err
		}
		fmt.Printf("Technical spec approved: %s (%s)\n", approval.ArtifactPath, approval.ArtifactHash)
		return nil
	default:
		return fmt.Errorf("usage: attest tech-spec <draft|review|approve> [run-id] [--from <path>]")
	}
}

// cmdTechSpecReviewFromFile creates or reuses a run for reviewing a spec file.
// If a previous run exists with the same spec hash, it is reused (reviews/personas cached).
func cmdTechSpecReviewFromFile(ctx context.Context, wd, fromPath string, flags []string) error {
	// Hash the spec to find an existing run.
	specData, err := os.ReadFile(fromPath) //nolint:gosec // fromPath is user-provided CLI input, path traversal is intentional
	if err != nil {
		return fmt.Errorf("read spec: %w", err)
	}
	specHash := state.SHA256Bytes(specData)

	// Search for existing run with matching spec.
	if runDir, runID := findRunBySpecHash(wd, specHash); runDir != nil {
		fmt.Printf("Reusing run: %s (spec unchanged)\n", runID)
		eng := engine.New(runDir, wd)
		// Re-draft to ensure in-run spec matches source (council may have rewritten it).
		if err := eng.DraftTechnicalSpec(ctx, fromPath); err != nil {
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

	if err := eng.DraftTechnicalSpec(ctx, fromPath); err != nil {
		return fmt.Errorf("draft: %w", err)
	}

	return cmdTechSpecReview(ctx, eng, flags, true)
}

// findRunBySpecHash scans existing runs for one whose source spec matches the given hash.
// Returns the most recent matching run, or nil if none found.
func findRunBySpecHash(wd, specHash string) (runDir *state.RunDir, runID string) {
	runsDir := filepath.Join(wd, ".attest", "runs")
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

func cmdTechSpecReview(ctx context.Context, eng *engine.Engine, flags []string, externalSpec bool) error {
	structuralOnly := false
	dryRun := false
	force := false
	skipApproval := false
	rounds := 2
	mode := councilflow.ReviewStandard
	for i := 0; i < len(flags); i++ {
		switch flags[i] {
		case "--structural-only":
			structuralOnly = true
		case "--council":
			// Accepted for backward compat but council is now the default.
		case "--dry-run":
			dryRun = true
		case "--force":
			force = true
		case "--skip-approval":
			skipApproval = true
		case "--round":
			if i+1 < len(flags) {
				_, _ = fmt.Sscanf(flags[i+1], "%d", &rounds)
				i++
			}
		case "--mode":
			if i+1 < len(flags) {
				m := councilflow.ReviewMode(flags[i+1])
				if councilflow.ValidReviewModes[m] {
					mode = m
				} else {
					return fmt.Errorf("invalid review mode %q (use: mvp, standard, production)", flags[i+1])
				}
				i++
			}
		}
	}

	// Structural review (pre-flight). Skipped for external specs that don't follow our template.
	if !externalSpec {
		review, err := eng.ReviewTechnicalSpec(ctx)
		if err != nil {
			return err
		}
		data, _ := json.MarshalIndent(review, "", "  ")
		fmt.Println(string(data))

		if structuralOnly {
			return nil
		}
		if review.Status != state.ReviewPass {
			return fmt.Errorf("structural review failed — fix before running council")
		}
	}

	cfg := councilflow.DefaultConfig()
	cfg.Rounds = rounds
	cfg.Mode = mode
	cfg.DryRun = dryRun
	cfg.Force = force
	cfg.SkipApproval = skipApproval
	result, err := eng.CouncilReviewTechnicalSpec(ctx, cfg)
	if err != nil {
		return err
	}

	fmt.Printf("\nCouncil verdict: %s\n", result.OverallVerdict)
	for i := range result.Rounds {
		round := &result.Rounds[i]
		for j := range round.Reviews {
			r := &round.Reviews[j]
			fmt.Printf("  [round %d] %s: %s (%d findings)\n", round.Round, r.PersonaID, r.Verdict, len(r.Findings))
		}
	}
	return nil
}

func cmdPlan(ctx context.Context, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: attest plan <draft|review|approve> <run-id>")
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
	case "draft":
		plan, err := eng.DraftExecutionPlan(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("Execution plan recorded: %s (%d slices)\n", runDir.ExecutionPlan(), len(plan.Slices))
		return nil
	case commandReview:
		review, err := eng.ReviewExecutionPlan(ctx)
		if err != nil {
			return err
		}
		data, _ := json.MarshalIndent(review, "", "  ")
		fmt.Println(string(data))
		return nil
	case commandApprove:
		approval, err := eng.ApproveExecutionPlan(ctx, "user")
		if err != nil {
			return err
		}
		fmt.Printf("Execution plan approved: %s (%s)\n", approval.ArtifactPath, approval.ArtifactHash)
		return nil
	default:
		return fmt.Errorf("usage: attest plan <draft|review|approve> <run-id>")
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
		return fmt.Errorf("usage: attest approve <run-id> [--launch]")
	}

	wd, err := workDir()
	if err != nil {
		return err
	}

	runDir := state.NewRunDir(wd, runID)
	eng := engine.New(runDir, wd)

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
		// List all runs.
		runsDir := filepath.Join(wd, ".attest", "runs")
		entries, err := os.ReadDir(runsDir)
		if err != nil {
			return fmt.Errorf("no runs found (is this an attest project?)")
		}
		fmt.Println("Runs:")
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			runDir := state.NewRunDir(wd, e.Name())
			eng := engine.New(runDir, wd)
			status, err := eng.ReconcileRunStatus()
			if err != nil {
				fmt.Printf("  %s (status unreadable)\n", e.Name())
				continue
			}
			fmt.Printf("  %s  state=%s\n", status.RunID, status.State)
		}
		return nil
	}

	runID := args[0]
	runDir := state.NewRunDir(wd, runID)
	eng := engine.New(runDir, wd)
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

	return nil
}

func cmdReport(args []string) error {
	if len(args) < 3 {
		return fmt.Errorf("usage: attest report <run-id> <task-id> --from <path>")
	}

	runID := args[0]
	taskID := args[1]
	var fromPath string
	for i := 2; i < len(args); i++ {
		if args[i] == "--from" && i+1 < len(args) {
			fromPath = args[i+1]
			i++
		}
	}
	if fromPath == "" {
		return fmt.Errorf("usage: attest report <run-id> <task-id> --from <path>")
	}

	wd, err := workDir()
	if err != nil {
		return err
	}

	runDir := state.NewRunDir(wd, runID)
	tasks, err := runDir.ReadTasks()
	if err != nil {
		return fmt.Errorf("read tasks: %w", err)
	}
	found := false
	for i := range tasks {
		if tasks[i].TaskID == taskID {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("task %s not found", taskID)
	}

	var report state.CompletionReport
	if err := state.ReadJSON(fromPath, &report); err != nil {
		return fmt.Errorf("read completion report: %w", err)
	}
	if report.TaskID == "" {
		report.TaskID = taskID
	}
	if report.TaskID != taskID {
		return fmt.Errorf("completion report task_id %q does not match %q", report.TaskID, taskID)
	}
	if report.AttemptID == "" {
		report.AttemptID = "manual-report"
	}

	reportPath := filepath.Join(runDir.ReportDir(taskID, report.AttemptID), "completion-report.json")
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

func cmdVerify(ctx context.Context, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: attest verify <run-id> <task-id>")
	}
	runID := args[0]
	taskID := args[1]

	wd, err := workDir()
	if err != nil {
		return err
	}

	runDir := state.NewRunDir(wd, runID)
	eng := engine.New(runDir, wd)

	tasks, err := runDir.ReadTasks()
	if err != nil {
		return fmt.Errorf("read tasks: %w", err)
	}

	var task *state.Task
	for i := range tasks {
		if tasks[i].TaskID == taskID {
			task = &tasks[i]
			break
		}
	}
	if task == nil {
		return fmt.Errorf("task %s not found", taskID)
	}

	// Read completion report: check task-level path (backward compat),
	// then scan attempt subdirectories for the latest report.
	var report state.CompletionReport
	if !readLatestCompletionReport(runDir, taskID, &report) {
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
	} else {
		fmt.Println("\nVerification: FAIL")
		for _, f := range result.BlockingFindings {
			fmt.Printf("  [%s] %s: %s\n", f.Severity, f.Category, f.Summary)
		}
	}

	return nil
}

func cmdRetry(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: attest retry <run-id> <task-id>")
	}

	runID := args[0]
	taskID := args[1]

	wd, err := workDir()
	if err != nil {
		return err
	}

	runDir := state.NewRunDir(wd, runID)
	eng := engine.New(runDir, wd)
	if err := eng.RetryTask(taskID); err != nil {
		return err
	}

	fmt.Printf("Task requeued: %s\n", taskID)
	return nil
}

func readLatestCompletionReport(runDir *state.RunDir, taskID string, report *state.CompletionReport) bool {
	// Try task-level path first (backward compat).
	taskPath := filepath.Join(runDir.ReportDir(taskID), "completion-report.json")
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
		attemptPath := filepath.Join(runDir.ReportDir(taskID, entries[i].Name()), "completion-report.json")
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
