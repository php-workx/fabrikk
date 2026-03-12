// Package engine implements the attest run engine lifecycle.
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/runger/attest/internal/compiler"
	"github.com/runger/attest/internal/state"
	"github.com/runger/attest/internal/verifier"
)

const errReadArtifact = "read artifact: %w"

// Engine is the Phase 1 run engine (spec section 18.3).
// Serial execution, foreground, no council, no detached mode.
type Engine struct {
	RunDir  *state.RunDir
	WorkDir string // repository root
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
		State:              state.RunPreparing,
		LastTransitionTime: time.Now(),
		TaskCountsByState:  map[string]int{},
	}
	if err := e.RunDir.WriteStatus(status); err != nil {
		return nil, fmt.Errorf("write status: %w", err)
	}

	_ = e.RunDir.AppendEvent(state.Event{
		Timestamp: time.Now(),
		Type:      "run_state_transition",
		RunID:     runID,
		Detail:    "preparing",
	})

	return artifact, nil
}

// Approve marks the run artifact as approved and computes the artifact hash (spec section 6.2).
func (e *Engine) Approve(ctx context.Context) error {
	artifact, err := e.RunDir.ReadArtifact()
	if err != nil {
		return fmt.Errorf(errReadArtifact, err)
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

	return nil
}

// Compile runs the task compiler and coverage check (spec sections 7.1, 7.2).
func (e *Engine) Compile(ctx context.Context) (*compiler.CompileResult, error) {
	artifact, err := e.RunDir.ReadArtifact()
	if err != nil {
		return nil, fmt.Errorf(errReadArtifact, err)
	}

	result, err := compiler.Compile(artifact)
	if err != nil {
		return nil, fmt.Errorf("compile: %w", err)
	}

	// Coverage check (spec section 7.2): block if any requirement is unassigned.
	unassigned := compiler.CheckCoverage(artifact, result.Coverage)
	if len(unassigned) > 0 {
		return nil, fmt.Errorf("unassigned requirements after compilation (spec 7.2): %v", unassigned)
	}

	// Write compiled tasks and coverage.
	if err := e.RunDir.WriteTasks(result.Tasks); err != nil {
		return nil, fmt.Errorf("write tasks: %w", err)
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

	return result, nil
}

// VerifyTask runs the deterministic verification pipeline for a single task (spec section 11.3).
func (e *Engine) VerifyTask(ctx context.Context, task *state.Task, report *state.CompletionReport) (*state.VerifierResult, error) {
	artifact, err := e.RunDir.ReadArtifact()
	if err != nil {
		return nil, fmt.Errorf(errReadArtifact, err)
	}

	result, err := verifier.Verify(ctx, task, report, artifact.QualityGate, e.WorkDir)
	if err != nil {
		return nil, fmt.Errorf("verify: %w", err)
	}

	// Write verifier result to the task's report directory.
	reportDir := e.RunDir.ReportDir(task.TaskID)
	if err := state.WriteJSON(fmt.Sprintf("%s/verifier-result.json", reportDir), result); err != nil {
		return nil, fmt.Errorf("write verifier result: %w", err)
	}

	_ = e.RunDir.AppendEvent(state.Event{
		Timestamp: time.Now(),
		Type:      "verifier_completed",
		RunID:     artifact.RunID,
		TaskID:    task.TaskID,
		Detail:    fmt.Sprintf("pass=%v findings=%d", result.Pass, len(result.BlockingFindings)),
	})

	return result, nil
}

// UpdateTaskStatus updates a task's status in tasks.json (single-writer rule, spec section 4.1).
func (e *Engine) UpdateTaskStatus(taskID string, newStatus state.TaskStatus, reason string) error {
	tasks, err := e.RunDir.ReadTasks()
	if err != nil {
		return fmt.Errorf("read tasks: %w", err)
	}

	found := false
	for i := range tasks {
		if tasks[i].TaskID == taskID {
			tasks[i].Status = newStatus
			tasks[i].StatusReason = reason
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("task %s not found", taskID)
	}

	return e.RunDir.WriteTasks(tasks)
}

// GetPendingTasks returns tasks in pending state with satisfied dependencies.
func (e *Engine) GetPendingTasks() ([]state.Task, error) {
	tasks, err := e.RunDir.ReadTasks()
	if err != nil {
		return nil, err
	}

	taskStates := make(map[string]state.TaskStatus)
	for i := range tasks {
		taskStates[tasks[i].TaskID] = tasks[i].Status
	}

	var pending []state.Task
	for i := range tasks {
		if tasks[i].Status != state.TaskPending {
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
