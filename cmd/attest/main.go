package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/runger/attest/internal/engine"
	"github.com/runger/attest/internal/state"
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
	case "review":
		err = cmdReview(ctx, args[1:])
	case "approve":
		err = cmdApprove(ctx, args[1:])
	case "status":
		err = cmdStatus(ctx, args[1:])
	case "verify":
		err = cmdVerify(ctx, args[1:])
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
  approve <run-id> [--launch]                 Approve and compile tasks
  status [<run-id>]                          Show run status
  verify <run-id> <task-id>                  Run deterministic verification
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
			status, err := runDir.ReadStatus()
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

	status, err := runDir.ReadStatus()
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

	// Read completion report if it exists.
	reportPath := filepath.Join(runDir.ReportDir(taskID), "completion-report.json")
	var report state.CompletionReport
	if err := state.ReadJSON(reportPath, &report); err != nil {
		// Create a minimal report for verification testing.
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

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
