// Package engine implements the fabrikk run engine lifecycle.
package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/php-workx/fabrikk/internal/agentcli"
	"github.com/php-workx/fabrikk/internal/compiler"
	"github.com/php-workx/fabrikk/internal/state"
	"github.com/php-workx/fabrikk/internal/verifier"
)

const (
	errReadArtifact           = "read artifact: %w"
	errReadTasks              = "read tasks: %w"
	errWriteTasks             = "write tasks: %w"
	errRefreshStatus          = "refresh status: %w"
	errSyncCoverage           = "sync requirement coverage: %w"
	validationCommandTimeout  = 30 * time.Second
	errValidationPathCategory = "validation_path"
)

// Engine is the Phase 1 run engine (spec section 18.3).
// Serial execution, foreground, no council, no detached mode.
type Engine struct {
	RunDir           *state.RunDir
	WorkDir          string                 // repository root
	TaskStore        state.TaskStore        // nil = fall back to RunDir
	LearningEnricher state.LearningEnricher // nil = skip learning enrichment
	InvokeFn         agentcli.InvokeFn      // nil = use agentcli.InvokeFunc; daemon-bound for orchestrator queries
}

// taskStore returns the configured TaskStore or falls back to RunDir.
func (e *Engine) taskStore() state.TaskStore {
	if e.TaskStore != nil {
		return e.TaskStore
	}
	return e.RunDir.AsTaskStore()
}

// runID returns the run ID from the RunDir path.
func (e *Engine) runID() string {
	return filepathBase(e.RunDir.Root)
}

// New creates a new engine for the given run directory.
func New(runDir *state.RunDir, workDir string) *Engine {
	return &Engine{
		RunDir:  runDir,
		WorkDir: workDir,
	}
}

// Prepare ingests spec files and creates a draft run artifact (spec section 7.1).
func (e *Engine) Prepare(ctx context.Context, specPaths []string) (*state.RunArtifact, error) {
	reqs, sources, err := compiler.IngestSpecs(specPaths)
	if err != nil {
		return nil, fmt.Errorf("ingest specs: %w", err)
	}

	if len(reqs) == 0 {
		return nil, fmt.Errorf("no requirements found in specs")
	}

	runID := fmt.Sprintf("run-%d", time.Now().Unix())

	// Detect quality gate (spec section 11.2).
	gate := detectQualityGate(e.WorkDir)

	artifact := &state.RunArtifact{
		SchemaVersion: "0.1",
		RunID:         runID,
		SourceSpecs:   sources,
		Requirements:  reqs,
		RiskProfile:   "standard",
		RoutingPolicy: state.RoutingPolicy{
			DefaultImplementer: "claude-sonnet",
		},
		QualityGate: gate,
	}

	// Initialize run directory.
	e.RunDir = state.NewRunDir(e.WorkDir, runID)
	if err := e.RunDir.Init(); err != nil {
		return nil, fmt.Errorf("init run dir: %w", err)
	}

	// Write draft artifact.
	if err := e.RunDir.WriteArtifact(artifact); err != nil {
		return nil, fmt.Errorf("write artifact: %w", err)
	}

	// Write initial status.
	status := &state.RunStatus{
		RunID:              runID,
		State:              state.RunAwaitingApproval,
		LastTransitionTime: time.Now(),
		TaskCountsByState:  map[string]int{},
		CurrentGate:        "awaiting_approval",
	}
	if err := e.RunDir.WriteStatus(status); err != nil {
		return nil, fmt.Errorf("write status: %w", err)
	}

	_ = e.RunDir.AppendEvent(state.Event{
		Timestamp: time.Now(),
		Type:      "run_state_transition",
		RunID:     runID,
		Detail:    "awaiting_approval",
	})

	return artifact, nil
}

// Approve marks the run artifact as approved and computes the artifact hash (spec section 6.2).
// Validates that the execution plan is approved before persisting, so that a failed Compile
// cannot leave the run in a half-approved state.
func (e *Engine) Approve(ctx context.Context) error {
	artifact, err := e.RunDir.ReadArtifact()
	if err != nil {
		return fmt.Errorf(errReadArtifact, err)
	}

	// Validate execution plan is approved before persisting run approval.
	// This prevents state corruption: without this check, Approve would persist
	// and a subsequent Compile failure would leave the run marked approved
	// with no way to compile.
	if _, err := e.readApprovedExecutionPlan(); err != nil {
		return fmt.Errorf("cannot approve run: approved execution plan required: %w", err)
	}

	// Compute artifact hash for immutability enforcement (spec section 3.2).
	hashData, err := json.Marshal(artifact)
	if err != nil {
		return fmt.Errorf("marshal for hash: %w", err)
	}
	artifact.ArtifactHash = state.SHA256Bytes(hashData)

	now := time.Now()
	artifact.ApprovedAt = &now
	artifact.ApprovedBy = "user"

	if err := e.RunDir.WriteArtifact(artifact); err != nil {
		return fmt.Errorf("write approved artifact: %w", err)
	}

	_ = e.RunDir.AppendEvent(state.Event{
		Timestamp: now,
		Type:      "run_state_transition",
		RunID:     artifact.RunID,
		Detail:    "approved",
	})

	if err := e.refreshRunStatus(state.RunApproved, "approved", nil); err != nil {
		return fmt.Errorf(errRefreshStatus, err)
	}

	return nil
}

// Compile runs the task compiler and coverage check (spec sections 7.1, 7.2).
func (e *Engine) Compile(ctx context.Context) (*compiler.CompileResult, error) {
	artifact, err := e.RunDir.ReadArtifact()
	if err != nil {
		return nil, fmt.Errorf(errReadArtifact, err)
	}

	plan, err := e.readApprovedExecutionPlan()
	if err != nil {
		return nil, err
	}

	// Validate execution plan is bound to the currently approved technical spec.
	// A re-approved tech spec must trigger a new execution plan draft.
	currentSpecHash, err := e.readApprovedTechnicalSpecHash()
	if err != nil {
		return nil, fmt.Errorf("validate tech spec lineage: %w", err)
	}
	if plan.SourceTechnicalSpecHash != currentSpecHash {
		return nil, fmt.Errorf("execution plan references tech spec %s but current approved tech spec is %s; re-draft the execution plan",
			plan.SourceTechnicalSpecHash, currentSpecHash)
	}

	result, err := compiler.CompileExecutionPlan(artifact, plan)
	if err != nil {
		return nil, fmt.Errorf("compile: %w", err)
	}

	// Coverage check (spec section 7.2): block if any requirement is unassigned.
	unassigned := compiler.CheckCoverage(artifact, result.Coverage)
	if len(unassigned) > 0 {
		return nil, fmt.Errorf("unassigned requirements after compilation (spec 7.2): %v", unassigned)
	}

	// Enrich tasks with learnings (post-compilation, before write).
	if e.LearningEnricher != nil {
		for i := range result.Tasks {
			e.enrichTaskWithLearnings(&result.Tasks[i])
		}
	}

	// Write compiled tasks — ticket.Store is the sole task backend.
	if err := e.taskStore().WriteTasks(e.runID(), result.Tasks); err != nil {
		return nil, fmt.Errorf(errWriteTasks, err)
	}

	if err := e.RunDir.WriteCoverage(result.Coverage); err != nil {
		return nil, fmt.Errorf("write coverage: %w", err)
	}

	_ = e.RunDir.AppendEvent(state.Event{
		Timestamp: time.Now(),
		Type:      "tasks_compiled",
		RunID:     artifact.RunID,
		Detail:    fmt.Sprintf("compiled %d tasks covering %d requirements", len(result.Tasks), len(result.Coverage)),
	})

	if err := e.refreshRunStatus(state.RunRunning, "dispatch_ready", nil); err != nil {
		return nil, fmt.Errorf(errRefreshStatus, err)
	}

	return result, nil
}

// VerifyTask runs the deterministic verification pipeline for a single task (spec section 11.3).
func (e *Engine) VerifyTask(ctx context.Context, task *state.Task, report *state.CompletionReport) (*state.VerifierResult, error) {
	if report == nil || report.AttemptID == "" {
		return nil, fmt.Errorf("verify task: attempt_id is required for per-attempt report isolation")
	}

	artifact, err := e.RunDir.ReadArtifact()
	if err != nil {
		return nil, fmt.Errorf(errReadArtifact, err)
	}

	result, err := verifier.Verify(ctx, task, report, artifact.QualityGate, e.WorkDir)
	if err != nil {
		return nil, fmt.Errorf("verify: %w", err)
	}

	e.applyValidationChecks(ctx, task, artifact, result)
	e.applyNeverBoundaryWarnings(result, artifact, report)

	if err := e.writeVerifierResult(task, report, result); err != nil {
		return nil, err
	}
	if err := e.persistVerificationOutcome(task, result); err != nil {
		return nil, err
	}

	_ = e.RunDir.AppendEvent(state.Event{
		Timestamp: time.Now(),
		Type:      "verifier_completed",
		RunID:     artifact.RunID,
		TaskID:    task.TaskID,
		Detail:    fmt.Sprintf("pass=%v findings=%d", result.Pass, len(result.BlockingFindings)),
	})

	e.recordLearningOutcome(artifact.RunID, task, result.Pass)

	nextState, blockers := verificationRunState(task.TaskID, result)
	if err := e.refreshRunStatus(nextState, "verification", blockers); err != nil {
		return nil, fmt.Errorf(errRefreshStatus, err)
	}

	return result, nil
}

var validationCommandAllowlist = map[string]struct{}{"go": {}, "python3": {}, "npm": {}, "make": {}, "just": {}}

func (e *Engine) applyValidationChecks(ctx context.Context, task *state.Task, artifact *state.RunArtifact, result *state.VerifierResult) {
	for i, check := range task.ValidationChecks {
		if isQualityGateValidationCheck(check, artifact) {
			continue
		}
		checkResult, finding := e.runValidationCheck(ctx, task, i, check)
		result.EvidenceChecks = append(result.EvidenceChecks, checkResult)
		if finding != nil {
			result.Pass = false
			result.BlockingFindings = append(result.BlockingFindings, *finding)
		}
	}
}

func isQualityGateValidationCheck(check state.ValidationCheck, artifact *state.RunArtifact) bool {
	if artifact == nil || artifact.QualityGate == nil || check.Type != "command" || check.Tool == "" {
		return false
	}
	parts := strings.Fields(strings.TrimSpace(artifact.QualityGate.Command))
	if len(parts) == 0 || parts[0] != check.Tool || len(parts[1:]) != len(check.Args) {
		return false
	}
	for i := range check.Args {
		if parts[i+1] != check.Args[i] {
			return false
		}
	}
	return true
}

func (e *Engine) applyNeverBoundaryWarnings(result *state.VerifierResult, artifact *state.RunArtifact, report *state.CompletionReport) {
	if result == nil || artifact == nil || report == nil {
		return
	}
	boundaries := artifact.Boundaries.Normalized().Never
	if len(boundaries) == 0 {
		return
	}
	for _, boundary := range boundaries {
		if boundary == state.EmptyBoundaryItem {
			result.NonBlockingFindings = append(result.NonBlockingFindings, state.Finding{
				FindingID: fmt.Sprintf("%s-boundary-never-empty", result.TaskID),
				Severity:  "low",
				Category:  "boundary_never",
				Summary:   "run artifact contains an empty never boundary item that could not be enforced deterministically",
				DedupeKey: fmt.Sprintf("boundary-never-empty:%s", result.TaskID),
			})
			continue
		}
		violations := boundaryViolations(report.ChangedFiles, boundary, e.WorkDir)
		if len(violations) == 0 {
			continue
		}
		result.NonBlockingFindings = append(result.NonBlockingFindings, state.Finding{
			FindingID: fmt.Sprintf("%s-boundary-never-%d", result.TaskID, len(result.NonBlockingFindings)+1),
			Severity:  "low",
			Category:  "boundary_never",
			Summary:   fmt.Sprintf("Never boundary %q was touched by changed files: %s", boundary, strings.Join(violations, ", ")),
			DedupeKey: fmt.Sprintf("boundary-never:%s:%s", result.TaskID, boundary),
		})
	}
}

func boundaryViolations(changedFiles []string, boundary, workDir string) []string {
	normalizedBoundary := normalizeBoundaryPath(boundary, workDir)
	if normalizedBoundary == "" {
		return nil
	}
	seen := make(map[string]struct{}, len(changedFiles))
	violations := make([]string, 0, len(changedFiles))
	for _, changed := range changedFiles {
		normalizedChanged := normalizeBoundaryPath(changed, workDir)
		if normalizedChanged == "" || !pathWithinBoundary(normalizedChanged, normalizedBoundary) {
			continue
		}
		if _, ok := seen[normalizedChanged]; ok {
			continue
		}
		seen[normalizedChanged] = struct{}{}
		violations = append(violations, normalizedChanged)
	}
	sort.Strings(violations)
	return violations
}

func normalizeBoundaryPath(path, workDir string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	clean := filepath.Clean(trimmed)
	if workDir != "" && filepath.IsAbs(clean) {
		if rel, err := filepath.Rel(workDir, clean); err == nil {
			clean = rel
		}
	}
	clean = filepath.ToSlash(clean)
	if clean == "." {
		return ""
	}
	return strings.Trim(clean, "/")
}

func pathWithinBoundary(path, boundary string) bool {
	return path == boundary || strings.HasPrefix(path, boundary+"/")
}

func (e *Engine) runValidationCheck(ctx context.Context, task *state.Task, index int, check state.ValidationCheck) (state.Check, *state.Finding) {
	checkName := fmt.Sprintf("validation_check_%02d", index+1)
	switch check.Type {
	case "files_exist":
		missing := make([]string, 0, len(check.Paths))
		for _, path := range check.Paths {
			validatedPath, err := e.validationPath(path)
			if err != nil {
				detail := fmt.Sprintf("files_exist has invalid path %q: %v", path, err)
				return state.Check{Name: checkName, Pass: false, Detail: detail}, validationFinding(task, index, errValidationPathCategory, detail)
			}
			if _, err := os.Stat(validatedPath); err != nil {
				missing = append(missing, path)
			}
		}
		if len(missing) == 0 {
			return state.Check{Name: checkName, Pass: true, Detail: "all required files exist"}, nil
		}
		detail := fmt.Sprintf("missing required files: %s", strings.Join(missing, ", "))
		return state.Check{Name: checkName, Pass: false, Detail: detail}, validationFinding(task, index, "validation_files_exist", detail)
	case "content_check":
		if check.File == "" || check.Pattern == "" {
			detail := "content_check requires both file and pattern"
			return state.Check{Name: checkName, Pass: false, Detail: detail}, validationFinding(task, index, "validation_content_check", detail)
		}
		validatedPath, err := e.validationPath(check.File)
		if err != nil {
			detail := fmt.Sprintf("content_check has invalid file path %q: %v", check.File, err)
			return state.Check{Name: checkName, Pass: false, Detail: detail}, validationFinding(task, index, errValidationPathCategory, detail)
		}
		data, err := os.ReadFile(validatedPath)
		if err != nil {
			detail := fmt.Sprintf("content_check could not read %s: %v", check.File, err)
			return state.Check{Name: checkName, Pass: false, Detail: detail}, validationFinding(task, index, "validation_content_check", detail)
		}
		matched, err := regexp.Match(check.Pattern, data)
		if err != nil {
			detail := fmt.Sprintf("content_check has invalid pattern %q: %v", check.Pattern, err)
			return state.Check{Name: checkName, Pass: false, Detail: detail}, validationFinding(task, index, "validation_content_check", detail)
		}
		if matched {
			return state.Check{Name: checkName, Pass: true, Detail: fmt.Sprintf("pattern matched in %s", check.File)}, nil
		}
		detail := fmt.Sprintf("pattern %q not found in %s", check.Pattern, check.File)
		return state.Check{Name: checkName, Pass: false, Detail: detail}, validationFinding(task, index, "validation_content_check", detail)
	case "tests", "command":
		return e.runValidationCommand(ctx, task, index, checkName, check)
	default:
		detail := fmt.Sprintf("unsupported validation check type %q", check.Type)
		return state.Check{Name: checkName, Pass: false, Detail: detail}, validationFinding(task, index, "validation_type", detail)
	}
}

func (e *Engine) runValidationCommand(ctx context.Context, task *state.Task, index int, checkName string, check state.ValidationCheck) (state.Check, *state.Finding) {
	if check.Tool == "" {
		detail := fmt.Sprintf("%s requires a tool", check.Type)
		return state.Check{Name: checkName, Pass: false, Detail: detail}, validationFinding(task, index, "validation_tool", detail)
	}
	if _, ok := validationCommandAllowlist[check.Tool]; !ok {
		detail := fmt.Sprintf("tool %q is not allowlisted", check.Tool)
		return state.Check{Name: checkName, Pass: false, Detail: detail}, validationFinding(task, index, "validation_tool_allowlist", detail)
	}

	runCtx, cancel := context.WithTimeout(ctx, validationCommandTimeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, check.Tool, check.Args...) //nolint:gosec // validation checks execute allowlisted local developer tools without shell interpretation
	cmd.Dir = e.WorkDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		exitCode := -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
		var detail string
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			detail = fmt.Sprintf("%s %s timed out after %s: %s", check.Tool, strings.Join(check.Args, " "), validationCommandTimeout, strings.TrimSpace(stderr.String()))
		} else {
			detail = fmt.Sprintf("%s %s failed with exit code %d: %s", check.Tool, strings.Join(check.Args, " "), exitCode, strings.TrimSpace(stderr.String()))
		}
		return state.Check{Name: checkName, Pass: false, Detail: detail}, validationFinding(task, index, "validation_command", detail)
	}
	return state.Check{Name: checkName, Pass: true, Detail: strings.TrimSpace(stdout.String())}, nil
}

func (e *Engine) validationPath(path string) (string, error) {
	return resolveWorktreePath(e.WorkDir, path)
}

func resolveWorktreePath(workDir, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("empty path")
	}
	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		return "", fmt.Errorf("resolve workdir: %w", err)
	}
	var target string
	if filepath.IsAbs(path) {
		target = filepath.Clean(path)
	} else {
		target = filepath.Join(absWorkDir, filepath.Clean(path))
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	rel, err := filepath.Rel(absWorkDir, absTarget)
	if err != nil {
		return "", fmt.Errorf("relativize path: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("path escapes workdir")
	}
	return absTarget, nil
}

func validationFinding(task *state.Task, index int, category, detail string) *state.Finding {
	return &state.Finding{
		FindingID: fmt.Sprintf("%s-validation-%02d", task.TaskID, index+1),
		Severity:  "high",
		Category:  category,
		Summary:   detail,
		DedupeKey: fmt.Sprintf("validation:%s:%d:%s", task.TaskID, index, category),
	}
}

// writeVerifierResult persists the verifier result to the attempt-scoped report directory.
func (e *Engine) writeVerifierResult(task *state.Task, report *state.CompletionReport, result *state.VerifierResult) error {
	reportDir := e.RunDir.ReportDir(task.TaskID, report.AttemptID)
	if err := state.WriteJSON(fmt.Sprintf("%s/verifier-result.json", reportDir), result); err != nil {
		return fmt.Errorf("write verifier result: %w", err)
	}
	return nil
}

// persistVerificationOutcome updates the task status and syncs coverage based on the verifier result.
func (e *Engine) persistVerificationOutcome(task *state.Task, result *state.VerifierResult) error {
	taskStatus := state.TaskDone
	statusReason := ""
	if !result.Pass {
		taskStatus = state.TaskBlocked
		statusReason = summarizeFindings(result.BlockingFindings)
	}
	if err := e.persistVerifiedTask(task, taskStatus, statusReason); err != nil {
		return fmt.Errorf("persist task verification outcome: %w", err)
	}
	if err := e.syncCoverageFromTasks(); err != nil {
		return fmt.Errorf(errSyncCoverage, err)
	}
	return nil
}

// recordLearningOutcome records verification outcomes for learning effectiveness tracking.
func (e *Engine) recordLearningOutcome(runID string, task *state.Task, pass bool) {
	if e.LearningEnricher == nil || len(task.LearningIDs) == 0 {
		return
	}
	if outErr := e.LearningEnricher.RecordOutcome(task.LearningIDs, pass); outErr != nil {
		_ = e.RunDir.AppendEvent(state.Event{
			Timestamp: time.Now(),
			Type:      "learning_outcome_failed",
			RunID:     runID,
			TaskID:    task.TaskID,
			Detail:    outErr.Error(),
		})
	}
}

// verificationRunState determines the run state and blockers from a verifier result.
func verificationRunState(taskID string, result *state.VerifierResult) (runState state.RunState, blockers []string) {
	if result.Pass {
		return state.RunRunning, nil
	}
	blockers = make([]string, 0, len(result.BlockingFindings))
	for _, finding := range result.BlockingFindings {
		blockers = append(blockers, fmt.Sprintf("%s: %s", taskID, finding.Summary))
	}
	return state.RunBlocked, blockers
}

// RetryTask clears a blocked task for another manual attempt.
func (e *Engine) RetryTask(taskID string) error {
	// Validate task exists and is retryable before updating.
	task, err := e.taskStore().ReadTask(taskID)
	if err != nil {
		return fmt.Errorf("read task: %w", err)
	}
	if task.Status != state.TaskBlocked && task.Status != state.TaskRepairPending {
		return fmt.Errorf("task %s is not retryable from status %s", taskID, task.Status)
	}

	if err := e.taskStore().UpdateStatus(taskID, state.TaskPending, ""); err != nil {
		return fmt.Errorf("update task status: %w", err)
	}
	if err := e.syncCoverageFromTasks(); err != nil {
		return fmt.Errorf(errSyncCoverage, err)
	}

	_ = e.RunDir.AppendEvent(state.Event{
		Timestamp: time.Now(),
		Type:      "task_retried",
		RunID:     filepathBase(e.RunDir.Root),
		TaskID:    taskID,
		Detail:    "manual retry requested",
	})

	if err := e.refreshRunStatus(state.RunRunning, "dispatch_ready", nil); err != nil {
		return fmt.Errorf(errRefreshStatus, err)
	}

	return nil
}

// ReconcileRunStatus repairs stale run-status.json from canonical task/artifact state.
func (e *Engine) ReconcileRunStatus() (*state.RunStatus, error) {
	status, err := e.RunDir.ReadStatus()
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("read status: %w", err)
		}
		status = &state.RunStatus{
			RunID:             filepathBase(e.RunDir.Root),
			TaskCountsByState: map[string]int{},
		}
	}

	artifact, err := e.RunDir.ReadArtifact()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf(errReadArtifact, err)
	}

	tasks, err := e.taskStore().ReadTasks(e.runID())
	if err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, state.ErrPartialRead) {
		return nil, fmt.Errorf(errReadTasks, err)
	}

	inferred := inferredRunStatus(status, artifact, tasks)
	if !inferred.ok {
		return status, nil
	}
	if !statusNeedsRefresh(status, inferred.state, inferred.gate, inferred.blockers, tasks) {
		return status, nil
	}
	if err := e.refreshRunStatus(inferred.state, inferred.gate, inferred.blockers); err != nil {
		return nil, fmt.Errorf(errRefreshStatus, err)
	}
	return e.RunDir.ReadStatus()
}

// UpdateTaskStatus updates a single task's status via per-ticket write (spec section 4.1).
func (e *Engine) UpdateTaskStatus(taskID string, newStatus state.TaskStatus, reason string) error {
	if err := e.taskStore().UpdateStatus(taskID, newStatus, reason); err != nil {
		return fmt.Errorf("update task status: %w", err)
	}
	if err := e.syncCoverageFromTasks(); err != nil {
		return fmt.Errorf(errSyncCoverage, err)
	}
	return nil
}

// GetPendingTasks returns tasks in pending state with satisfied dependencies.
func (e *Engine) GetPendingTasks() ([]state.Task, error) {
	tasks, err := e.taskStore().ReadTasks(e.runID())
	if err != nil && !errors.Is(err, state.ErrPartialRead) {
		return nil, err
	}

	taskStates := make(map[string]state.TaskStatus)
	for i := range tasks {
		taskStates[tasks[i].TaskID] = tasks[i].Status
	}

	var pending []state.Task
	for i := range tasks {
		if tasks[i].Status != state.TaskPending && tasks[i].Status != state.TaskRepairPending {
			continue
		}
		// Check dependencies (spec section 7.4).
		ready := true
		for _, dep := range tasks[i].DependsOn {
			if taskStates[dep] != state.TaskDone {
				ready = false
				break
			}
		}
		if ready {
			pending = append(pending, tasks[i])
		}
	}
	return pending, nil
}

// ClaimAndDispatch claims a task for an agent, transitioning it to TaskClaimed.
// If the store does not support claims (e.g. RunDir), falls back to UpdateStatus.
func (e *Engine) ClaimAndDispatch(taskID, ownerID, backend string, lease time.Duration) error {
	cs, ok := e.taskStore().(state.ClaimableStore)
	if !ok {
		return e.taskStore().UpdateStatus(taskID, state.TaskClaimed, "claimed by "+ownerID)
	}
	if err := cs.ClaimTask(taskID, ownerID, backend, lease); err != nil {
		return fmt.Errorf("claim task %s: %w", taskID, err)
	}
	_ = e.RunDir.AppendEvent(state.Event{
		Timestamp: time.Now(),
		Type:      "task_claimed",
		RunID:     e.runID(),
		TaskID:    taskID,
		Detail:    fmt.Sprintf("claimed by %s (%s)", ownerID, backend),
	})
	return nil
}

// ReleaseTask releases a claim and sets the task to a new status.
// Falls back to UpdateStatus if the store does not support claims.
func (e *Engine) ReleaseTask(taskID, ownerID string, newStatus state.TaskStatus, reason string) error {
	cs, ok := e.taskStore().(state.ClaimableStore)
	if !ok {
		return e.taskStore().UpdateStatus(taskID, newStatus, reason)
	}
	if err := cs.ReleaseClaim(taskID, ownerID, newStatus, reason); err != nil {
		return fmt.Errorf("release task %s: %w", taskID, err)
	}
	if err := e.syncCoverageFromTasks(); err != nil {
		return fmt.Errorf(errSyncCoverage, err)
	}
	_ = e.RunDir.AppendEvent(state.Event{
		Timestamp: time.Now(),
		Type:      "task_released",
		RunID:     e.runID(),
		TaskID:    taskID,
		Detail:    fmt.Sprintf("released by %s → %s", ownerID, newStatus),
	})
	return nil
}

// SweepExpiredClaims reclaims tasks with expired leases, resetting them to pending.
func (e *Engine) SweepExpiredClaims() ([]string, error) {
	type expiredSweeper interface {
		ReclaimExpired(runID string) ([]string, error)
	}
	sweeper, ok := e.taskStore().(expiredSweeper)
	if !ok {
		return nil, nil
	}
	reclaimed, err := sweeper.ReclaimExpired(e.runID())
	if err != nil {
		return nil, fmt.Errorf("sweep expired claims: %w", err)
	}
	for _, taskID := range reclaimed {
		_ = e.RunDir.AppendEvent(state.Event{
			Timestamp: time.Now(),
			Type:      "claim_expired",
			RunID:     e.runID(),
			TaskID:    taskID,
			Detail:    "claim expired, task reset to pending",
		})
	}
	return reclaimed, nil
}

func (e *Engine) refreshRunStatus(nextState state.RunState, currentGate string, openBlockers []string) error {
	status, err := e.readOrInitStatus()
	if err != nil {
		return err
	}

	status.State = nextState
	status.CurrentGate = currentGate
	status.OpenBlockers = openBlockers
	status.LastTransitionTime = time.Now()
	status.TaskCountsByState = map[string]int{}
	status.TaskDetails = nil

	tasks, err := e.populateTaskCounts(status, currentGate)
	if err != nil {
		return err
	}

	if err := e.populateUncoveredCount(status); err != nil {
		return err
	}

	applyCompletionOverride(status, len(tasks))

	return e.RunDir.WriteStatus(status)
}

// readOrInitStatus reads existing run status or creates a fresh one if missing.
func (e *Engine) readOrInitStatus() (*state.RunStatus, error) {
	status, err := e.RunDir.ReadStatus()
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		return &state.RunStatus{
			RunID:             filepathBase(e.RunDir.Root),
			TaskCountsByState: map[string]int{},
		}, nil
	}
	return status, nil
}

// populateTaskCounts fills task counts and details on the status from the current tasks.
func (e *Engine) populateTaskCounts(status *state.RunStatus, currentGate string) ([]state.Task, error) {
	tasks, err := e.taskStore().ReadTasks(e.runID())
	if err != nil && !errors.Is(err, state.ErrPartialRead) && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	for i := range tasks {
		status.TaskCountsByState[string(tasks[i].Status)]++
		if tasks[i].StatusReason != "" {
			status.TaskDetails = append(status.TaskDetails, state.TaskDetail{
				TaskID:                 tasks[i].TaskID,
				CurrentGate:            currentGate,
				BlockingFindingSummary: tasks[i].StatusReason,
				HumanInputRequired:     tasks[i].Status == state.TaskBlocked,
			})
		}
	}
	return tasks, nil
}

// populateUncoveredCount sets the uncovered requirement count on the status.
func (e *Engine) populateUncoveredCount(status *state.RunStatus) error {
	coverage, err := e.RunDir.ReadCoverage()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	status.UncoveredRequirementCount = 0
	for _, item := range coverage {
		if len(item.CoveringTaskIDs) == 0 && !item.Deferred {
			status.UncoveredRequirementCount++
		}
	}
	return nil
}

// applyCompletionOverride marks the run as completed if all tasks are done.
func applyCompletionOverride(status *state.RunStatus, totalTasks int) {
	doneTasks := status.TaskCountsByState[string(state.TaskDone)]
	if totalTasks > 0 && doneTasks == totalTasks {
		status.State = state.RunCompleted
		status.CurrentGate = "completed"
		status.OpenBlockers = nil
	}
}

func filepathBase(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == os.PathSeparator {
			return path[i+1:]
		}
	}
	return path
}

func (e *Engine) persistVerifiedTask(task *state.Task, status state.TaskStatus, reason string) error {
	// Try per-ticket read first; if not found, write the full task as new.
	existing, err := e.taskStore().ReadTask(task.TaskID)
	if err != nil {
		// Only insert on genuine not-found. Other errors (parse, permission,
		// ambiguous ID) should not silently overwrite the existing file.
		if !errors.Is(err, os.ErrNotExist) && !strings.Contains(err.Error(), "not found") {
			return fmt.Errorf("read task for verification: %w", err)
		}
		// Task doesn't exist yet — write it as new with the verified status.
		newTask := *task
		newTask.Status = status
		newTask.StatusReason = reason
		newTask.UpdatedAt = time.Now()
		return e.taskStore().WriteTask(&newTask)
	}

	existing.Status = status
	existing.StatusReason = reason
	existing.UpdatedAt = time.Now()
	return e.taskStore().WriteTask(existing)
}

// enrichTaskWithLearnings queries the learning store and populates the task's
// learning-related fields. Deterministic: Warnings from anti_pattern summaries,
// Constraints from codebase/tooling summaries. LearningContext for body rendering.
func (e *Engine) enrichTaskWithLearnings(task *state.Task) {
	refs, err := e.LearningEnricher.QueryLearnings(state.LearningQueryOpts{
		Tags:             task.DeriveTags(),
		Paths:            task.Scope.OwnedPaths,
		SearchText:       task.Title,
		MinEffectiveness: 0.3,
		Limit:            5,
	})
	if err != nil {
		_ = e.RunDir.AppendEvent(state.Event{
			Timestamp: time.Now(),
			Type:      "learning_query_failed",
			RunID:     e.runID(),
			TaskID:    task.TaskID,
			Detail:    err.Error(),
		})
		return
	}
	if len(refs) == 0 {
		return
	}

	task.LearningIDs = make([]string, len(refs))
	task.LearningContext = refs
	for i := range refs {
		task.LearningIDs[i] = refs[i].ID
		switch state.LearningCategory(refs[i].Category) {
		case state.LearningCategoryAntiPattern:
			task.Warnings = append(task.Warnings, refs[i].Summary)
		case state.LearningCategoryCodebase, state.LearningCategoryTooling:
			task.Constraints = append(task.Constraints, refs[i].Summary)
		}
	}
	_ = e.RunDir.AppendEvent(state.Event{
		Timestamp: time.Now(),
		Type:      "learning_enrichment",
		RunID:     e.runID(),
		TaskID:    task.TaskID,
		Detail:    fmt.Sprintf("attached %d learnings", len(refs)),
	})
}

func summarizeFindings(findings []state.Finding) string {
	if len(findings) == 0 {
		return ""
	}

	parts := make([]string, 0, len(findings))
	for _, finding := range findings {
		if finding.Summary == "" {
			continue
		}
		parts = append(parts, finding.Summary)
	}
	return strings.Join(parts, "; ")
}

type inferredStatus struct {
	state    state.RunState
	gate     string
	blockers []string
	ok       bool
}

func inferredRunStatus(current *state.RunStatus, artifact *state.RunArtifact, tasks []state.Task) inferredStatus {
	if len(tasks) > 0 {
		blockers := blockedTaskSummaries(tasks)
		if len(blockers) > 0 {
			return inferredStatus{state: state.RunBlocked, gate: "verification", blockers: blockers, ok: true}
		}
		if allTasksDone(tasks) {
			return inferredStatus{state: state.RunCompleted, gate: "completed", ok: true}
		}
		return inferredStatus{state: state.RunRunning, gate: "dispatch_ready", ok: true}
	}

	if artifact != nil {
		if artifact.ApprovedAt != nil {
			return inferredStatus{state: state.RunApproved, gate: "approved", ok: true}
		}
		return inferredStatus{state: state.RunAwaitingApproval, gate: "awaiting_approval", ok: true}
	}

	if current != nil && current.State != "" {
		return inferredStatus{state: current.State, gate: current.CurrentGate, blockers: current.OpenBlockers}
	}

	return inferredStatus{state: state.RunPreparing, gate: "preparing", ok: true}
}

func statusNeedsRefresh(current *state.RunStatus, nextState state.RunState, gate string, blockers []string, tasks []state.Task) bool {
	if current == nil {
		return true
	}
	if current.State != nextState || current.CurrentGate != gate || !slices.Equal(current.OpenBlockers, blockers) {
		return true
	}

	expectedCounts := make(map[string]int)
	for i := range tasks {
		expectedCounts[string(tasks[i].Status)]++
	}
	if len(expectedCounts) != len(current.TaskCountsByState) {
		return true
	}
	for k, v := range expectedCounts {
		if current.TaskCountsByState[k] != v {
			return true
		}
	}
	return false
}

func blockedTaskSummaries(tasks []state.Task) []string {
	var blockers []string
	for i := range tasks {
		if tasks[i].Status != state.TaskBlocked {
			continue
		}
		summary := tasks[i].StatusReason
		if summary == "" {
			summary = "task is blocked"
		}
		blockers = append(blockers, fmt.Sprintf("%s: %s", tasks[i].TaskID, summary))
	}
	return blockers
}

func allTasksDone(tasks []state.Task) bool {
	if len(tasks) == 0 {
		return false
	}
	for i := range tasks {
		if tasks[i].Status != state.TaskDone {
			return false
		}
	}
	return true
}

func (e *Engine) syncCoverageFromTasks() error {
	coverage, err := e.RunDir.ReadCoverage()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read coverage: %w", err)
	}

	tasks, err := e.taskStore().ReadTasks(e.runID())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, state.ErrPartialRead) {
			// Partial read: use whatever tasks we got. Not-exist: nothing to sync.
			if !errors.Is(err, state.ErrPartialRead) {
				return nil
			}
		} else {
			return fmt.Errorf(errReadTasks, err)
		}
	}

	taskStates := make(map[string]state.TaskStatus, len(tasks))
	for i := range tasks {
		taskStates[tasks[i].TaskID] = tasks[i].Status
	}

	for i := range coverage {
		coverage[i].Status = deriveCoverageStatus(&coverage[i], taskStates)
	}

	if err := e.RunDir.WriteCoverage(coverage); err != nil {
		return fmt.Errorf("write coverage: %w", err)
	}
	return nil
}

func deriveCoverageStatus(cov *state.RequirementCoverage, taskStates map[string]state.TaskStatus) string {
	if cov.Deferred {
		return "deferred"
	}
	if len(cov.CoveringTaskIDs) == 0 {
		return "unassigned"
	}

	hasInProgress := false
	hasBlocked := false
	for _, taskID := range cov.CoveringTaskIDs {
		status, ok := taskStates[taskID]
		if !ok {
			continue
		}
		switch status {
		case state.TaskDone:
			return "satisfied"
		case state.TaskPending, state.TaskClaimed, state.TaskImplementing, state.TaskVerifying, state.TaskUnderReview, state.TaskRepairPending:
			hasInProgress = true
		case state.TaskBlocked, state.TaskFailed:
			hasBlocked = true
		default:
			// Unknown status — treat as in-progress to avoid false negatives.
			hasInProgress = true
		}
	}
	if hasInProgress {
		return "in_progress"
	}
	if hasBlocked {
		return "blocked"
	}
	return "unassigned"
}
