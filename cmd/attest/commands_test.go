package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/runger/attest/internal/engine"
	"github.com/runger/attest/internal/state"
)

func TestCmdStatusListsRunsAndSingleRun(t *testing.T) {
	baseDir := t.TempDir()
	withWorkingDir(t, baseDir)

	timestamp := time.Date(2026, time.March, 12, 9, 30, 0, 0, time.UTC)
	writeRunFixture(t, baseDir, "run-123", runFixture{
		status: &state.RunStatus{
			RunID:              "run-123",
			State:              state.RunApproved,
			LastTransitionTime: timestamp,
			TaskCountsByState: map[string]int{
				"pending": 2,
				"done":    1,
			},
		},
	})
	writeRunFixture(t, baseDir, "run-456", runFixture{
		status: &state.RunStatus{
			RunID:              "run-456",
			State:              state.RunRunning,
			LastTransitionTime: timestamp.Add(2 * time.Hour),
		},
	})

	t.Run("lists all runs", func(t *testing.T) {
		output := captureStdout(t, func() {
			if err := cmdStatus(context.Background(), nil); err != nil {
				t.Fatalf("cmdStatus: %v", err)
			}
		})

		assertContains(t, output, "Runs:")
		assertContains(t, output, "run-123  state=approved")
		assertContains(t, output, "run-456  state=running")
	})

	t.Run("shows a single run summary", func(t *testing.T) {
		output := captureStdout(t, func() {
			if err := cmdStatus(context.Background(), []string{"run-123"}); err != nil {
				t.Fatalf("cmdStatus: %v", err)
			}
		})

		assertContains(t, output, "Run: run-123")
		assertContains(t, output, "State: approved")
		assertContains(t, output, "Last transition: 2026-03-12 09:30:00")
		assertContains(t, output, "pending: 2")
		assertContains(t, output, "done: 1")
	})
}

func TestCmdReviewShowsAwaitingApproval(t *testing.T) {
	baseDir := t.TempDir()
	withWorkingDir(t, baseDir)

	writeRunFixture(t, baseDir, "run-review", runFixture{
		artifact: &state.RunArtifact{
			SchemaVersion: "0.1",
			RunID:         "run-review",
			RiskProfile:   "standard",
			SourceSpecs: []state.SourceSpec{
				{
					Path:        "specs/attest.md",
					Fingerprint: strings.Repeat("a", 64),
				},
			},
			Requirements: []state.Requirement{
				{ID: "AT-FR-001", Text: "The system must ingest specs."},
				{ID: "AT-TS-001", Text: "The system must compile tasks deterministically."},
			},
		},
	})

	output := captureStdout(t, func() {
		if err := cmdReview(context.Background(), []string{"run-review"}); err != nil {
			t.Fatalf("cmdReview: %v", err)
		}
	})

	assertContains(t, output, "=== Run Artifact Review: run-review ===")
	assertContains(t, output, "Status: AWAITING APPROVAL")
	assertContains(t, output, "Next: attest approve run-review")
	assertContains(t, output, "AT-FR-001: The system must ingest specs.")
}

func TestCmdVerifyPrintsFailureDetails(t *testing.T) {
	baseDir := t.TempDir()
	withWorkingDir(t, baseDir)

	task := newTask("task-at-fr-001", "Implement AT-FR-001", state.TaskPending)
	task.RequirementIDs = []string{"AT-FR-001"}
	task.RequiredEvidence = []string{"test_pass"}

	writeRunFixture(t, baseDir, "run-verify", runFixture{
		artifact: &state.RunArtifact{
			SchemaVersion: "0.1",
			RunID:         "run-verify",
			Requirements: []state.Requirement{
				{ID: "AT-FR-001", Text: "The system must ingest specs."},
			},
		},
		tasks: []state.Task{task},
	})

	output := captureStdout(t, func() {
		if err := cmdVerify(context.Background(), []string{"run-verify", task.TaskID}); err != nil {
			t.Fatalf("cmdVerify: %v", err)
		}
	})

	assertContains(t, output, `"pass": false`)
	assertContains(t, output, "Verification: FAIL")
	assertContains(t, output, "missing required evidence: test_pass")
}

func TestCmdPrepareCreatesRunArtifacts(t *testing.T) {
	baseDir := t.TempDir()
	withWorkingDir(t, baseDir)

	specPath := filepath.Join(baseDir, "spec.md")
	if err := os.WriteFile(specPath, []byte("# Spec\n\n- **AT-FR-001**: The system must ingest specs.\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(spec): %v", err)
	}
	if err := os.WriteFile(filepath.Join(baseDir, "Justfile"), []byte("check:\n\t@echo ok\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(Justfile): %v", err)
	}

	output := captureStdout(t, func() {
		if err := cmdPrepare(context.Background(), []string{"--spec", specPath}); err != nil {
			t.Fatalf("cmdPrepare: %v", err)
		}
	})

	assertContains(t, output, "Run prepared: run-")
	assertContains(t, output, "Requirements: 1")
	assertContains(t, output, "Quality gate: just check")
	assertContains(t, output, "Next: attest review run-")

	runID := extractRunID(t, output)
	runDir := state.NewRunDir(baseDir, runID)
	if _, err := runDir.ReadArtifact(); err != nil {
		t.Fatalf("ReadArtifact: %v", err)
	}
	if _, err := runDir.ReadStatus(); err != nil {
		t.Fatalf("ReadStatus: %v", err)
	}
}

func TestCmdApproveCompilesTasksAndLaunchNotice(t *testing.T) {
	baseDir := t.TempDir()
	withWorkingDir(t, baseDir)

	specPath := filepath.Join(baseDir, "spec.md")
	if err := os.WriteFile(specPath, []byte("# Spec\n\n- **AT-FR-001**: The system must ingest specs.\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(spec): %v", err)
	}

	runDir := state.NewRunDir(baseDir, "placeholder")
	eng := engine.New(runDir, baseDir)
	artifact, err := eng.Prepare(context.Background(), []string{specPath})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	output := captureStdout(t, func() {
		if err := cmdApprove(context.Background(), []string{artifact.RunID, "--launch"}); err != nil {
			t.Fatalf("cmdApprove: %v", err)
		}
	})

	assertContains(t, output, "approved and compiled")
	assertContains(t, output, "Tasks: 1")
	assertContains(t, output, "--launch: detached execution not yet implemented (Phase 4)")

	compiledRunDir := state.NewRunDir(baseDir, artifact.RunID)
	tasks, err := compiledRunDir.ReadTasks()
	if err != nil {
		t.Fatalf("ReadTasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].RequirementIDs[0] != "AT-FR-001" {
		t.Fatalf("compiled tasks = %+v, want one AT-FR-001 task", tasks)
	}
}

func TestCmdTasksReturnsFilteredJSON(t *testing.T) {
	baseDir := t.TempDir()
	withWorkingDir(t, baseDir)

	writeRunFixture(t, baseDir, "run-tasks", taskCommandFixture())

	output := captureStdout(t, func() {
		if err := cmdTasks([]string{"run-tasks", "--status", "pending", "--json"}); err != nil {
			t.Fatalf("cmdTasks: %v", err)
		}
	})

	var tasks []state.Task
	if err := json.Unmarshal([]byte(output), &tasks); err != nil {
		t.Fatalf("unmarshal tasks: %v\noutput=%s", err, output)
	}
	if len(tasks) != 2 {
		t.Fatalf("got %d tasks, want 2", len(tasks))
	}
	for i := range tasks {
		if tasks[i].Status != state.TaskPending {
			t.Fatalf("task %s status = %s, want pending", tasks[i].TaskID, tasks[i].Status)
		}
	}
}

func TestCmdReadyShowsOnlyDispatchableTasks(t *testing.T) {
	baseDir := t.TempDir()
	withWorkingDir(t, baseDir)

	writeRunFixture(t, baseDir, "run-tasks", taskCommandFixture())

	output := captureStdout(t, func() {
		if err := cmdReady([]string{"run-tasks"}); err != nil {
			t.Fatalf("cmdReady: %v", err)
		}
	})

	assertContains(t, output, "Ready tasks (1):")
	assertContains(t, output, "Ready task")
	assertNotContains(t, output, "Waiting task")
}

func TestCmdNextPrefersHighestImpactBlocker(t *testing.T) {
	baseDir := t.TempDir()
	withWorkingDir(t, baseDir)

	fixture := taskCommandFixture()
	writeRunFixture(t, baseDir, "run-blocked", runFixture{
		tasks: []state.Task{fixture.tasks[3], fixture.tasks[2]},
	})

	output := captureStdout(t, func() {
		if err := cmdNext([]string{"run-blocked"}); err != nil {
			t.Fatalf("cmdNext: %v", err)
		}
	})

	assertContains(t, output, "No ready tasks. Highest-impact blocker:")
	assertContains(t, output, "Blocked task")
	assertContains(t, output, "Reason: awaiting clarification")
}

func TestCmdProgressReturnsJSONSummary(t *testing.T) {
	baseDir := t.TempDir()
	withWorkingDir(t, baseDir)

	writeRunFixture(t, baseDir, "run-tasks", taskCommandFixture())

	output := captureStdout(t, func() {
		if err := cmdProgress([]string{"run-tasks", "--json"}); err != nil {
			t.Fatalf("cmdProgress: %v", err)
		}
	})

	var summary struct {
		TotalTasks          int            `json:"total_tasks"`
		CompletionPercent   int            `json:"completion_percent"`
		TaskCounts          map[string]int `json:"task_counts"`
		RequirementCoverage map[string]int `json:"requirement_coverage"`
	}
	if err := json.Unmarshal([]byte(output), &summary); err != nil {
		t.Fatalf("unmarshal summary: %v\noutput=%s", err, output)
	}
	if summary.TotalTasks != 4 {
		t.Fatalf("got total tasks %d, want 4", summary.TotalTasks)
	}
	if summary.CompletionPercent != 25 {
		t.Fatalf("got completion percent %d, want 25", summary.CompletionPercent)
	}
	if summary.TaskCounts["done"] != 1 || summary.TaskCounts["pending"] != 2 {
		t.Fatalf("unexpected task counts: %+v", summary.TaskCounts)
	}
	if summary.RequirementCoverage["blocked"] != 1 {
		t.Fatalf("unexpected requirement coverage: %+v", summary.RequirementCoverage)
	}
}

type runFixture struct {
	status   *state.RunStatus
	artifact *state.RunArtifact
	tasks    []state.Task
	coverage []state.RequirementCoverage
}

func taskCommandFixture() runFixture {
	doneTask := newTask("task-done", "Complete groundwork", state.TaskDone)
	doneTask.Priority = 1
	doneTask.Order = 1
	doneTask.RequirementIDs = []string{"AT-FR-001"}

	readyTask := newTask("task-ready", "Ready task", state.TaskPending)
	readyTask.Priority = 2
	readyTask.Order = 2
	readyTask.RequirementIDs = []string{"AT-FR-002"}

	waitingTask := newTask("task-waiting", "Waiting task", state.TaskPending)
	waitingTask.Priority = 3
	waitingTask.Order = 3
	waitingTask.DependsOn = []string{"task-blocked"}
	waitingTask.RequirementIDs = []string{"AT-FR-003"}

	blockedTask := newTask("task-blocked", "Blocked task", state.TaskBlocked)
	blockedTask.Priority = 1
	blockedTask.Order = 4
	blockedTask.StatusReason = "awaiting clarification"
	blockedTask.RequirementIDs = []string{"AT-FR-004"}

	return runFixture{
		tasks: []state.Task{doneTask, readyTask, waitingTask, blockedTask},
		coverage: []state.RequirementCoverage{
			{RequirementID: "AT-FR-001", Status: "satisfied", CoveringTaskIDs: []string{"task-done"}},
			{RequirementID: "AT-FR-002", Status: "in_progress", CoveringTaskIDs: []string{"task-ready"}},
			{RequirementID: "AT-FR-003", Status: "blocked", CoveringTaskIDs: []string{"task-waiting"}},
		},
	}
}

func writeRunFixture(t *testing.T, baseDir, runID string, fixture runFixture) {
	t.Helper()

	runDir := state.NewRunDir(baseDir, runID)
	if err := runDir.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if fixture.status != nil {
		if err := runDir.WriteStatus(fixture.status); err != nil {
			t.Fatalf("WriteStatus: %v", err)
		}
	}
	if fixture.artifact != nil {
		if err := runDir.WriteArtifact(fixture.artifact); err != nil {
			t.Fatalf("WriteArtifact: %v", err)
		}
	}
	if fixture.tasks != nil {
		if err := runDir.WriteTasks(fixture.tasks); err != nil {
			t.Fatalf("WriteTasks: %v", err)
		}
	}
	if fixture.coverage != nil {
		if err := runDir.WriteCoverage(fixture.coverage); err != nil {
			t.Fatalf("WriteCoverage: %v", err)
		}
	}
}

func newTask(taskID, title string, status state.TaskStatus) state.Task {
	now := time.Date(2026, time.March, 12, 9, 0, 0, 0, time.UTC)
	return state.Task{
		TaskID:           taskID,
		Slug:             strings.ReplaceAll(taskID, "_", "-"),
		Title:            title,
		TaskType:         "implementation",
		CreatedAt:        now,
		UpdatedAt:        now,
		Order:            1,
		ETag:             state.SHA256Bytes([]byte(taskID)),
		LineageID:        taskID,
		Scope:            state.TaskScope{IsolationMode: "direct"},
		Priority:         1,
		RiskLevel:        "low",
		DefaultModel:     "sonnet",
		Status:           status,
		RequiredEvidence: []string{"quality_gate_pass"},
	}
}

func withWorkingDir(t *testing.T, dir string) {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir(%s): %v", dir, err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(wd); err != nil {
			t.Fatalf("restore working directory: %v", err)
		}
	})
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	os.Stdout = w

	defer func() {
		os.Stdout = origStdout
	}()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("Close stdout writer: %v", err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close stdout reader: %v", err)
	}
	return string(data)
}

func assertContains(t *testing.T, output, want string) {
	t.Helper()
	if !strings.Contains(output, want) {
		t.Fatalf("output %q does not contain %q", output, want)
	}
}

func assertNotContains(t *testing.T, output, want string) {
	t.Helper()
	if strings.Contains(output, want) {
		t.Fatalf("output %q unexpectedly contains %q", output, want)
	}
}

func extractRunID(t *testing.T, output string) string {
	t.Helper()

	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "Run prepared: ") {
			runID := strings.TrimSpace(strings.TrimPrefix(line, "Run prepared: "))
			if runID != "" {
				return runID
			}
		}
	}

	t.Fatalf("output %q did not contain run ID", output)
	return ""
}
