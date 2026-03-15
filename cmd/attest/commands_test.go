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

	"github.com/runger/attest/internal/compiler"
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

func TestCmdStatusReconcilesLegacyRunStateFromTasks(t *testing.T) {
	baseDir := t.TempDir()
	withWorkingDir(t, baseDir)

	timestamp := time.Date(2026, time.March, 13, 10, 0, 0, 0, time.UTC)
	writeRunFixture(t, baseDir, "run-legacy", runFixture{
		status: &state.RunStatus{
			RunID:              "run-legacy",
			State:              state.RunPreparing,
			LastTransitionTime: timestamp,
			TaskCountsByState:  map[string]int{},
		},
		artifact: &state.RunArtifact{
			SchemaVersion: "0.1",
			RunID:         "run-legacy",
			ApprovedAt:    ptrTime(timestamp),
			ApprovedBy:    "user",
		},
		tasks: []state.Task{
			newTask("task-done", "Done task", state.TaskDone),
			newTask("task-pending", "Pending task", state.TaskPending),
		},
	})

	output := captureStdout(t, func() {
		if err := cmdStatus(context.Background(), []string{"run-legacy"}); err != nil {
			t.Fatalf("cmdStatus: %v", err)
		}
	})

	assertContains(t, output, "Run: run-legacy")
	assertContains(t, output, "State: running")
	assertContains(t, output, "done: 1")
	assertContains(t, output, "pending: 1")

	runDir := state.NewRunDir(baseDir, "run-legacy")
	status, err := runDir.ReadStatus()
	if err != nil {
		t.Fatalf("ReadStatus(after reconcile): %v", err)
	}
	if status.State != state.RunRunning {
		t.Fatalf("status state after reconcile = %s, want %s", status.State, state.RunRunning)
	}
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

func TestCmdTechSpecDraftReviewAndApprove(t *testing.T) {
	baseDir := t.TempDir()
	withWorkingDir(t, baseDir)

	writeRunFixture(t, baseDir, "run-tech", runFixture{
		artifact: &state.RunArtifact{
			SchemaVersion: "0.1",
			RunID:         "run-tech",
			Requirements: []state.Requirement{
				{ID: "AT-FR-001", Text: "The system must ingest specs."},
			},
		},
		status: &state.RunStatus{
			RunID:              "run-tech",
			State:              state.RunAwaitingApproval,
			LastTransitionTime: time.Date(2026, time.March, 13, 12, 0, 0, 0, time.UTC),
			TaskCountsByState:  map[string]int{},
		},
	})

	specPath := filepath.Join(baseDir, "docs", "specs", "example-technical-spec.md")
	if err := os.MkdirAll(filepath.Dir(specPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(spec dir): %v", err)
	}
	specBody := `# Example Technical Specification

## 1. Technical context
Context

## 2. Architecture
Architecture

## 3. Canonical artifacts and schemas
Artifacts

## 4. Interfaces
Interfaces

## 5. Verification
Verification

## 6. Requirement traceability
Traceability

## 7. Open questions and risks
None

## 8. Approval
Pending
`
	if err := os.WriteFile(specPath, []byte(specBody), 0o644); err != nil {
		t.Fatalf("WriteFile(spec): %v", err)
	}

	draftOutput := captureStdout(t, func() {
		if err := cmdTechSpec(context.Background(), []string{"draft", "run-tech", "--from", specPath}); err != nil {
			t.Fatalf("cmdTechSpec draft: %v", err)
		}
	})
	assertContains(t, draftOutput, "Technical spec recorded")

	runDir := state.NewRunDir(baseDir, "run-tech")
	data, err := os.ReadFile(runDir.TechnicalSpec())
	if err != nil {
		t.Fatalf("ReadFile(technical spec): %v", err)
	}
	if string(data) != specBody {
		t.Fatalf("technical spec body mismatch:\n%s", data)
	}

	reviewOutput := captureStdout(t, func() {
		if err := cmdTechSpec(context.Background(), []string{"review", "run-tech"}); err != nil {
			t.Fatalf("cmdTechSpec review: %v", err)
		}
	})
	assertContains(t, reviewOutput, `"status": "pass"`)

	approveOutput := captureStdout(t, func() {
		if err := cmdTechSpec(context.Background(), []string{"approve", "run-tech"}); err != nil {
			t.Fatalf("cmdTechSpec approve: %v", err)
		}
	})
	assertContains(t, approveOutput, "Technical spec approved")

	approval, err := runDir.ReadTechnicalSpecApproval()
	if err != nil {
		t.Fatalf("ReadTechnicalSpecApproval: %v", err)
	}
	if approval.Status != state.ArtifactApproved || approval.ArtifactPath != "technical-spec.md" {
		t.Fatalf("unexpected approval: %+v", approval)
	}
}

func TestCmdTechSpecReviewReportsMissingSections(t *testing.T) {
	baseDir := t.TempDir()
	withWorkingDir(t, baseDir)

	writeRunFixture(t, baseDir, "run-tech-fail", runFixture{
		artifact: &state.RunArtifact{
			SchemaVersion: "0.1",
			RunID:         "run-tech-fail",
		},
	})

	runDir := state.NewRunDir(baseDir, "run-tech-fail")
	if err := os.WriteFile(runDir.TechnicalSpec(), []byte("# Incomplete Technical Spec\n\n## 1. Technical context\nOnly one section.\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(technical spec): %v", err)
	}

	output := captureStdout(t, func() {
		if err := cmdTechSpec(context.Background(), []string{"review", "run-tech-fail"}); err != nil {
			t.Fatalf("cmdTechSpec review: %v", err)
		}
	})
	assertContains(t, output, `"status": "fail"`)
	assertContains(t, output, "missing required section")
}

func TestCmdPlanDraftReviewAndApprove(t *testing.T) {
	baseDir := t.TempDir()
	withWorkingDir(t, baseDir)

	writeRunFixture(t, baseDir, "run-plan", runFixture{
		artifact: &state.RunArtifact{
			SchemaVersion: "0.1",
			RunID:         "run-plan",
			Requirements: []state.Requirement{
				{ID: "AT-FR-001", Text: "The system must ingest specs."},
				{ID: "AT-TS-001", Text: "The system must compile tasks deterministically."},
			},
			QualityGate: &state.QualityGate{
				Command:        "just check",
				TimeoutSeconds: 600,
				Required:       true,
			},
		},
		status: &state.RunStatus{
			RunID:              "run-plan",
			State:              state.RunAwaitingApproval,
			LastTransitionTime: time.Date(2026, time.March, 13, 12, 0, 0, 0, time.UTC),
			TaskCountsByState:  map[string]int{},
		},
	})

	runDir := state.NewRunDir(baseDir, "run-plan")
	techSpec := []byte(`# Example Technical Specification

## 1. Technical context
Context

## 2. Architecture
Architecture

## 3. Canonical artifacts and schemas
Artifacts

## 4. Interfaces
Interfaces

## 5. Verification
Verification

## 6. Requirement traceability
Traceability

## 7. Open questions and risks
None

## 8. Approval
Pending
`)
	if err := runDir.WriteTechnicalSpec(techSpec); err != nil {
		t.Fatalf("WriteTechnicalSpec: %v", err)
	}
	if err := runDir.WriteTechnicalSpecApproval(&state.ArtifactApproval{
		SchemaVersion: "0.1",
		RunID:         "run-plan",
		ArtifactType:  "technical_spec_approval",
		ArtifactPath:  "technical-spec.md",
		ArtifactHash:  "sha256:" + state.SHA256Bytes(techSpec),
		Status:        state.ArtifactApproved,
		ApprovedBy:    "tester",
		ApprovedAt:    time.Date(2026, time.March, 13, 12, 5, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("WriteTechnicalSpecApproval: %v", err)
	}

	draftOutput := captureStdout(t, func() {
		if err := cmdPlan(context.Background(), []string{"draft", "run-plan"}); err != nil {
			t.Fatalf("cmdPlan draft: %v", err)
		}
	})
	assertContains(t, draftOutput, "Execution plan recorded")

	plan, err := runDir.ReadExecutionPlan()
	if err != nil {
		t.Fatalf("ReadExecutionPlan: %v", err)
	}
	if plan.Status != state.ArtifactDrafted || len(plan.Slices) == 0 {
		t.Fatalf("unexpected plan: %+v", plan)
	}

	planMarkdown, err := runDir.ReadExecutionPlanMarkdown()
	if err != nil {
		t.Fatalf("ReadExecutionPlanMarkdown: %v", err)
	}
	assertContains(t, string(planMarkdown), "# Execution Plan")

	reviewOutput := captureStdout(t, func() {
		if err := cmdPlan(context.Background(), []string{"review", "run-plan"}); err != nil {
			t.Fatalf("cmdPlan review: %v", err)
		}
	})
	assertContains(t, reviewOutput, `"status": "pass"`)

	approveOutput := captureStdout(t, func() {
		if err := cmdPlan(context.Background(), []string{"approve", "run-plan"}); err != nil {
			t.Fatalf("cmdPlan approve: %v", err)
		}
	})
	assertContains(t, approveOutput, "Execution plan approved")

	approval, err := runDir.ReadExecutionPlanApproval()
	if err != nil {
		t.Fatalf("ReadExecutionPlanApproval: %v", err)
	}
	if approval.Status != state.ArtifactApproved || approval.ArtifactPath != "execution-plan.json" {
		t.Fatalf("unexpected plan approval: %+v", approval)
	}
}

func TestCmdPlanDraftRequiresApprovedTechnicalSpec(t *testing.T) {
	baseDir := t.TempDir()
	withWorkingDir(t, baseDir)

	writeRunFixture(t, baseDir, "run-plan-fail", runFixture{
		artifact: &state.RunArtifact{
			SchemaVersion: "0.1",
			RunID:         "run-plan-fail",
			Requirements: []state.Requirement{
				{ID: "AT-FR-001", Text: "The system must ingest specs."},
			},
		},
	})

	err := cmdPlan(context.Background(), []string{"draft", "run-plan-fail"})
	if err == nil {
		t.Fatal("cmdPlan draft error = nil, want technical spec approval error")
	}
	if !strings.Contains(err.Error(), "technical spec approval") {
		t.Fatalf("cmdPlan draft error = %q, want technical spec approval context", err)
	}
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

func TestCmdReportImportsCompletionReportAndVerifyPasses(t *testing.T) {
	baseDir := t.TempDir()
	withWorkingDir(t, baseDir)

	task := newTask("task-at-fr-001", "Implement AT-FR-001", state.TaskPending)
	task.RequirementIDs = []string{"AT-FR-001"}
	task.RequiredEvidence = []string{"file_exists"}

	writeRunFixture(t, baseDir, "run-report", runFixture{
		artifact: &state.RunArtifact{
			SchemaVersion: "0.1",
			RunID:         "run-report",
			Requirements: []state.Requirement{
				{ID: "AT-FR-001", Text: "The system must ingest specs."},
			},
		},
		status: &state.RunStatus{
			RunID:              "run-report",
			State:              state.RunRunning,
			LastTransitionTime: time.Date(2026, time.March, 12, 9, 0, 0, 0, time.UTC),
			TaskCountsByState:  map[string]int{"pending": 1},
		},
		tasks: []state.Task{task},
		coverage: []state.RequirementCoverage{
			{RequirementID: "AT-FR-001", Status: "in_progress", CoveringTaskIDs: []string{task.TaskID}},
		},
	})

	reportFile := filepath.Join(baseDir, "completion-report.json")
	report := state.CompletionReport{
		TaskID:            task.TaskID,
		AttemptID:         "attempt-report",
		ArtifactsProduced: []string{"coverage.out"},
	}
	reportBytes, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("Marshal(report): %v", err)
	}
	if err := os.WriteFile(reportFile, reportBytes, 0o644); err != nil {
		t.Fatalf("WriteFile(report): %v", err)
	}

	output := captureStdout(t, func() {
		if err := cmdReport([]string{"run-report", task.TaskID, "--from", reportFile}); err != nil {
			t.Fatalf("cmdReport: %v", err)
		}
	})
	assertContains(t, output, "Completion report recorded")

	runDir := state.NewRunDir(baseDir, "run-report")
	var persisted state.CompletionReport
	if err := state.ReadJSON(filepath.Join(runDir.ReportDir(task.TaskID, "attempt-report"), "completion-report.json"), &persisted); err != nil {
		t.Fatalf("ReadJSON(persisted report): %v", err)
	}
	if persisted.AttemptID != "attempt-report" {
		t.Fatalf("persisted attempt id = %q, want attempt-report", persisted.AttemptID)
	}

	verifyOutput := captureStdout(t, func() {
		if err := cmdVerify(context.Background(), []string{"run-report", task.TaskID}); err != nil {
			t.Fatalf("cmdVerify: %v", err)
		}
	})
	assertContains(t, verifyOutput, `"pass": true`)
	assertContains(t, verifyOutput, "Verification: PASS")

	tasks, err := runDir.ReadTasks()
	if err != nil {
		t.Fatalf("ReadTasks(after verify): %v", err)
	}
	if len(tasks) != 1 || tasks[0].Status != state.TaskDone {
		t.Fatalf("task status after verify = %+v, want done", tasks)
	}

	progressOutput := captureStdout(t, func() {
		if err := cmdProgress([]string{"run-report", "--json"}); err != nil {
			t.Fatalf("cmdProgress: %v", err)
		}
	})
	assertContains(t, progressOutput, `"completion_percent": 100`)
	assertContains(t, progressOutput, `"done": 1`)
	assertContains(t, progressOutput, `"satisfied": 1`)

	statusOutput := captureStdout(t, func() {
		if err := cmdStatus(context.Background(), []string{"run-report"}); err != nil {
			t.Fatalf("cmdStatus: %v", err)
		}
	})
	assertContains(t, statusOutput, "State: completed")
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
	writeApprovedExecutionPlanFixture(t, state.NewRunDir(baseDir, artifact.RunID), artifact)

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
	if len(tasks) != 1 {
		t.Fatalf("compiled tasks = %+v, want exactly one task", tasks)
	}
	if len(tasks[0].RequirementIDs) == 0 || tasks[0].RequirementIDs[0] != "AT-FR-001" {
		t.Fatalf("compiled task requirements = %+v, want first requirement AT-FR-001", tasks[0].RequirementIDs)
	}
}

func TestCmdApproveRequiresApprovedExecutionPlan(t *testing.T) {
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

	err = cmdApprove(context.Background(), []string{artifact.RunID})
	if err == nil {
		t.Fatal("cmdApprove error = nil, want execution plan approval error")
	}
	if !strings.Contains(err.Error(), "execution plan approval") {
		t.Fatalf("cmdApprove error = %q, want execution plan approval context", err)
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

func TestCmdRetryRequeuesBlockedTask(t *testing.T) {
	baseDir := t.TempDir()
	withWorkingDir(t, baseDir)

	writeRunFixture(t, baseDir, "run-retry", runFixture{
		status: &state.RunStatus{
			RunID:              "run-retry",
			State:              state.RunBlocked,
			LastTransitionTime: time.Date(2026, time.March, 12, 11, 0, 0, 0, time.UTC),
			TaskCountsByState:  map[string]int{"blocked": 1},
			OpenBlockers:       []string{"task-blocked: missing evidence"},
		},
		tasks: []state.Task{
			newTask("task-blocked", "Blocked task", state.TaskBlocked),
		},
		coverage: []state.RequirementCoverage{
			{RequirementID: "AT-FR-001", Status: "blocked", CoveringTaskIDs: []string{"task-blocked"}},
		},
	})

	runDir := state.NewRunDir(baseDir, "run-retry")
	tasks, err := runDir.ReadTasks()
	if err != nil {
		t.Fatalf("ReadTasks(before retry): %v", err)
	}
	tasks[0].StatusReason = "missing evidence"
	if err := runDir.WriteTasks(tasks); err != nil {
		t.Fatalf("WriteTasks(before retry): %v", err)
	}

	output := captureStdout(t, func() {
		if err := cmdRetry([]string{"run-retry", "task-blocked"}); err != nil {
			t.Fatalf("cmdRetry: %v", err)
		}
	})
	assertContains(t, output, "Task requeued: task-blocked")

	tasks, err = runDir.ReadTasks()
	if err != nil {
		t.Fatalf("ReadTasks(after retry): %v", err)
	}
	if len(tasks) != 1 || tasks[0].Status != state.TaskPending || tasks[0].StatusReason != "" {
		t.Fatalf("task after retry = %+v, want pending with cleared reason", tasks)
	}

	coverage, err := runDir.ReadCoverage()
	if err != nil {
		t.Fatalf("ReadCoverage(after retry): %v", err)
	}
	if len(coverage) != 1 || coverage[0].Status != "in_progress" {
		t.Fatalf("coverage after retry = %+v, want in_progress", coverage)
	}

	status, err := runDir.ReadStatus()
	if err != nil {
		t.Fatalf("ReadStatus(after retry): %v", err)
	}
	if status.State != state.RunRunning {
		t.Fatalf("run state after retry = %s, want %s", status.State, state.RunRunning)
	}
}

type runFixture struct {
	status   *state.RunStatus
	artifact *state.RunArtifact
	tasks    []state.Task
	coverage []state.RequirementCoverage
}

func writeApprovedExecutionPlanFixture(t *testing.T, runDir *state.RunDir, artifact *state.RunArtifact) {
	t.Helper()

	// Write tech spec and approval so lineage validation passes.
	specContent := []byte("# Spec\n\n## 1. Technical context\nA\n\n## 2. Architecture\nB\n\n## 3. Canonical artifacts and schemas\nC\n\n## 4. Interfaces\nD\n\n## 5. Verification\nE\n\n## 6. Requirement traceability\nF\n\n## 7. Open questions and risks\nG\n\n## 8. Approval\nH\n")
	if err := runDir.WriteTechnicalSpec(specContent); err != nil {
		t.Fatalf("WriteTechnicalSpec: %v", err)
	}
	specHash := "sha256:" + state.SHA256Bytes(specContent)
	if err := runDir.WriteTechnicalSpecApproval(&state.ArtifactApproval{
		SchemaVersion: "0.1",
		RunID:         artifact.RunID,
		ArtifactType:  "technical_spec_approval",
		ArtifactPath:  "technical-spec.md",
		ArtifactHash:  specHash,
		Status:        state.ArtifactApproved,
		ApprovedBy:    "tester",
		ApprovedAt:    time.Now().UTC(),
	}); err != nil {
		t.Fatalf("WriteTechnicalSpecApproval: %v", err)
	}

	compiled, err := compiler.Compile(artifact)
	if err != nil {
		t.Fatalf("compiler.Compile: %v", err)
	}

	plan := &state.ExecutionPlan{
		SchemaVersion:           "0.1",
		RunID:                   artifact.RunID,
		ArtifactType:            "execution_plan",
		SourceTechnicalSpecHash: specHash,
		Status:                  state.ArtifactApproved,
		GeneratedAt:             time.Now().UTC(),
	}
	for i := range compiled.Tasks {
		task := &compiled.Tasks[i]
		plan.Slices = append(plan.Slices, state.ExecutionSlice{
			SliceID:            strings.TrimPrefix(task.TaskID, "task-"),
			Title:              task.Title,
			Goal:               task.Title,
			RequirementIDs:     append([]string(nil), task.RequirementIDs...),
			DependsOn:          trimTaskIDs(task.DependsOn),
			FilesLikelyTouched: append([]string(nil), task.Scope.OwnedPaths...),
			OwnedPaths:         append([]string(nil), task.Scope.OwnedPaths...),
			AcceptanceChecks:   []string{"just check", "attest verify <run-id> <task-id>"},
			Risk:               task.RiskLevel,
			Size:               "small",
		})
	}

	if err := runDir.WriteExecutionPlan(plan); err != nil {
		t.Fatalf("WriteExecutionPlan: %v", err)
	}
	planData, err := os.ReadFile(runDir.ExecutionPlan())
	if err != nil {
		t.Fatalf("ReadFile(execution plan): %v", err)
	}
	if err := runDir.WriteExecutionPlanApproval(&state.ArtifactApproval{
		SchemaVersion: "0.1",
		RunID:         artifact.RunID,
		ArtifactType:  "execution_plan_approval",
		ArtifactPath:  "execution-plan.json",
		ArtifactHash:  "sha256:" + state.SHA256Bytes(planData),
		Status:        state.ArtifactApproved,
		ApprovedBy:    "tester",
		ApprovedAt:    time.Now().UTC(),
	}); err != nil {
		t.Fatalf("WriteExecutionPlanApproval: %v", err)
	}
}

func trimTaskIDs(taskIDs []string) []string {
	trimmed := make([]string, 0, len(taskIDs))
	for _, taskID := range taskIDs {
		trimmed = append(trimmed, strings.TrimPrefix(taskID, "task-"))
	}
	return trimmed
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

func ptrTime(v time.Time) *time.Time {
	return &v
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

	// Drain reader concurrently to avoid deadlock if output exceeds pipe buffer.
	type result struct {
		data []byte
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		data, err := io.ReadAll(r)
		ch <- result{data, err}
	}()

	defer func() {
		os.Stdout = origStdout
	}()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("Close stdout writer: %v", err)
	}
	res := <-ch
	if res.err != nil {
		t.Fatalf("ReadAll: %v", res.err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close stdout reader: %v", err)
	}
	return string(res.data)
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
