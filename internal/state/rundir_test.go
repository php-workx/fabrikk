package state_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/php-workx/fabrikk/internal/state"
)

func TestRunDirInitCreatesDirectoryStructure(t *testing.T) {
	baseDir := t.TempDir()
	runDir := state.NewRunDir(baseDir, "run-123")

	if err := runDir.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	for _, path := range []string{
		runDir.Root,
		filepath.Join(runDir.Root, "claims"),
		filepath.Join(runDir.Root, "reports"),
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat(%s): %v", path, err)
		}
		if !info.IsDir() {
			t.Fatalf("%s is not a directory", path)
		}
	}
}

func TestRunDirPathHelpers(t *testing.T) {
	runDir := state.NewRunDir("/tmp/project", "run-456")

	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "artifact", got: runDir.Artifact(), want: "/tmp/project/.fabrikk/runs/run-456/run-artifact.json"},
		{name: "tasks", got: runDir.Tasks(), want: "/tmp/project/.fabrikk/runs/run-456/tasks.json"},
		{name: "coverage", got: runDir.Coverage(), want: "/tmp/project/.fabrikk/runs/run-456/requirement-coverage.json"},
		{name: "status", got: runDir.Status(), want: "/tmp/project/.fabrikk/runs/run-456/run-status.json"},
		{name: "events", got: runDir.Events(), want: "/tmp/project/.fabrikk/runs/run-456/events.jsonl"},
		{name: "clarifications", got: runDir.Clarifications(), want: "/tmp/project/.fabrikk/runs/run-456/clarifications.json"},
		{name: "engine", got: runDir.Engine(), want: "/tmp/project/.fabrikk/runs/run-456/engine.json"},
		{name: "engine log", got: runDir.EngineLog(), want: "/tmp/project/.fabrikk/runs/run-456/engine.log"},
		{name: "technical spec", got: runDir.TechnicalSpec(), want: "/tmp/project/.fabrikk/runs/run-456/technical-spec.md"},
		{name: "technical spec review", got: runDir.TechnicalSpecReview(), want: "/tmp/project/.fabrikk/runs/run-456/technical-spec-review.json"},
		{name: "technical spec approval", got: runDir.TechnicalSpecApproval(), want: "/tmp/project/.fabrikk/runs/run-456/technical-spec-approval.json"},
		{name: "execution plan", got: runDir.ExecutionPlan(), want: "/tmp/project/.fabrikk/runs/run-456/execution-plan.json"},
		{name: "execution plan markdown", got: runDir.ExecutionPlanMarkdown(), want: "/tmp/project/.fabrikk/runs/run-456/execution-plan.md"},
		{name: "execution plan review", got: runDir.ExecutionPlanReview(), want: "/tmp/project/.fabrikk/runs/run-456/execution-plan-review.json"},
		{name: "execution plan approval", got: runDir.ExecutionPlanApproval(), want: "/tmp/project/.fabrikk/runs/run-456/execution-plan-approval.json"},
		{name: "claim path", got: runDir.ClaimPath("task-1"), want: "/tmp/project/.fabrikk/runs/run-456/claims/task-1.json"},
		{name: "report dir", got: runDir.ReportDir("task-1"), want: "/tmp/project/.fabrikk/runs/run-456/reports/task-1"},
		{name: "attempt path", got: runDir.AttemptPath("task-1"), want: "/tmp/project/.fabrikk/runs/run-456/reports/task-1/attempt.json"},
		{name: "council result path", got: runDir.CouncilResultPath("task-1"), want: "/tmp/project/.fabrikk/runs/run-456/reports/task-1/council-result.json"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Fatalf("got %q, want %q", tt.got, tt.want)
			}
		})
	}
}

type roundTripFixture struct {
	artifact         *state.RunArtifact
	tasks            []state.Task
	coverage         []state.RequirementCoverage
	status           *state.RunStatus
	techReview       *state.TechnicalSpecReview
	execPlan         *state.ExecutionPlan
	execReview       *state.ExecutionPlanReview
	approval         *state.ArtifactApproval
	techApproval     *state.ArtifactApproval
	techSpecMarkdown []byte
	execPlanMarkdown []byte
}

func newRoundTripFixture(now, approvedAt time.Time) roundTripFixture {
	return roundTripFixture{
		artifact: &state.RunArtifact{
			SchemaVersion: "0.1",
			RunID:         "run-roundtrip",
			SourceSpecs: []state.SourceSpec{
				{Path: "spec.md", Fingerprint: state.SHA256Bytes([]byte("spec"))},
			},
			Requirements: []state.Requirement{
				{ID: "AT-FR-001", Text: "The system must ingest specs."},
			},
			ApprovedAt:   &approvedAt,
			ApprovedBy:   "tester",
			ArtifactHash: state.SHA256Bytes([]byte("artifact")),
		},
		tasks: []state.Task{
			{
				TaskID:           "task-at-fr-001",
				Slug:             "task-at-fr-001",
				Title:            "Implement AT-FR-001",
				TaskType:         "implementation",
				CreatedAt:        now,
				UpdatedAt:        now,
				Order:            1,
				ETag:             state.SHA256Bytes([]byte("task")),
				LineageID:        "task-at-fr-001",
				RequirementIDs:   []string{"AT-FR-001"},
				Scope:            state.TaskScope{IsolationMode: "direct"},
				Priority:         1,
				RiskLevel:        "low",
				DefaultModel:     "sonnet",
				Status:           state.TaskPending,
				RequiredEvidence: []string{"quality_gate_pass"},
			},
		},
		coverage: []state.RequirementCoverage{
			{
				RequirementID:   "AT-FR-001",
				Status:          "in_progress",
				CoveringTaskIDs: []string{"task-at-fr-001"},
			},
		},
		status: &state.RunStatus{
			RunID:              "run-roundtrip",
			State:              state.RunApproved,
			LastTransitionTime: now,
			TaskCountsByState:  map[string]int{"pending": 1},
		},
		techReview: &state.TechnicalSpecReview{
			SchemaVersion:     "0.1",
			RunID:             "run-roundtrip",
			ArtifactType:      "technical_spec_review",
			TechnicalSpecHash: "sha256:technical-spec",
			Status:            state.ReviewPass,
			Summary:           "Technical spec is internally consistent.",
			ReviewedAt:        now,
		},
		execPlan: &state.ExecutionPlan{
			SchemaVersion:           "0.1",
			RunID:                   "run-roundtrip",
			ArtifactType:            "execution_plan",
			SourceTechnicalSpecHash: "sha256:technical-spec",
			Status:                  state.ArtifactDrafted,
			Slices: []state.ExecutionSlice{
				{
					SliceID:            "slice-001",
					Title:              "Add planning artifacts",
					Goal:               "Persist run-scoped planning artifacts.",
					RequirementIDs:     []string{"AT-FR-001"},
					FilesLikelyTouched: []string{"internal/state/types.go"},
					OwnedPaths:         []string{"internal/state"},
					AcceptanceChecks:   []string{"go test ./internal/state"},
					Risk:               "low",
					Size:               "small",
				},
			},
			GeneratedAt: now,
		},
		execReview: &state.ExecutionPlanReview{
			SchemaVersion:     "0.1",
			RunID:             "run-roundtrip",
			ArtifactType:      "execution_plan_review",
			ExecutionPlanHash: "sha256:execution-plan",
			Status:            state.ReviewPass,
			Summary:           "Execution plan is actionable.",
			Warnings: []state.ReviewWarning{{
				WarningID: "epr-w-001",
				SliceID:   "slice-001",
				Summary:   "slice is missing likely file touch points",
			}},
			SharedFileRegistry: []state.SharedFileEntry{{
				File:       "internal/state/types.go",
				Wave1Tasks: []string{"slice-001"},
				Wave2Tasks: []string{"slice-002"},
				Mitigation: "Refresh the shared-file base SHA before starting the later wave.",
			}},
			ReviewedAt: now,
		},
		approval: &state.ArtifactApproval{
			SchemaVersion: "0.1",
			RunID:         "run-roundtrip",
			ArtifactType:  "execution_plan_approval",
			ArtifactPath:  "execution-plan.json",
			ArtifactHash:  "sha256:execution-plan",
			Status:        state.ArtifactApproved,
			ApprovedBy:    "tester",
			ApprovedAt:    approvedAt,
		},
		techApproval: &state.ArtifactApproval{
			SchemaVersion: "0.1",
			RunID:         "run-roundtrip",
			ArtifactType:  "technical_spec_approval",
			ArtifactPath:  "technical-spec.md",
			ArtifactHash:  "sha256:technical-spec",
			Status:        state.ArtifactUnderReview,
			ApprovedBy:    "tester",
			ApprovedAt:    approvedAt,
		},
		techSpecMarkdown: []byte("# Technical Spec\n"),
		execPlanMarkdown: []byte("# Execution Plan\n"),
	}
}

func writeRoundTripFixture(t *testing.T, runDir *state.RunDir, fixture roundTripFixture) {
	t.Helper()

	writes := []struct {
		name string
		run  func() error
	}{
		{name: "WriteArtifact", run: func() error { return runDir.WriteArtifact(fixture.artifact) }},
		{name: "WriteTasks", run: func() error { return runDir.WriteTasks(fixture.tasks) }},
		{name: "WriteCoverage", run: func() error { return runDir.WriteCoverage(fixture.coverage) }},
		{name: "WriteStatus", run: func() error { return runDir.WriteStatus(fixture.status) }},
		{name: "WriteTechnicalSpecReview", run: func() error { return runDir.WriteTechnicalSpecReview(fixture.techReview) }},
		{name: "WriteExecutionPlan", run: func() error { return runDir.WriteExecutionPlan(fixture.execPlan) }},
		{name: "WriteExecutionPlanReview", run: func() error { return runDir.WriteExecutionPlanReview(fixture.execReview) }},
		{name: "WriteExecutionPlanApproval", run: func() error { return runDir.WriteExecutionPlanApproval(fixture.approval) }},
		{name: "WriteTechnicalSpecApproval", run: func() error { return runDir.WriteTechnicalSpecApproval(fixture.techApproval) }},
		{name: "WriteTechnicalSpec", run: func() error { return runDir.WriteTechnicalSpec(fixture.techSpecMarkdown) }},
		{name: "WriteExecutionPlanMarkdown", run: func() error { return runDir.WriteExecutionPlanMarkdown(fixture.execPlanMarkdown) }},
	}

	for _, write := range writes {
		if err := write.run(); err != nil {
			t.Fatalf("%s: %v", write.name, err)
		}
	}
}

func assertRoundTripFixture(t *testing.T, runDir *state.RunDir, fixture roundTripFixture) {
	t.Helper()

	assertRoundTripCoreRecords(t, runDir, fixture)
	assertRoundTripPlanningRecords(t, runDir, fixture)
	assertRoundTripMarkdown(t, runDir, fixture)
}

func assertRoundTripCoreRecords(t *testing.T, runDir *state.RunDir, fixture roundTripFixture) {
	t.Helper()

	gotArtifact, err := runDir.ReadArtifact()
	if err != nil {
		t.Fatalf("ReadArtifact: %v", err)
	}
	if gotArtifact.RunID != fixture.artifact.RunID || gotArtifact.ApprovedBy != fixture.artifact.ApprovedBy {
		t.Fatalf("unexpected artifact: %+v", gotArtifact)
	}

	gotTasks, err := runDir.ReadTasks()
	if err != nil {
		t.Fatalf("ReadTasks: %v", err)
	}
	if len(gotTasks) != 1 || gotTasks[0].TaskID != fixture.tasks[0].TaskID {
		t.Fatalf("unexpected tasks: %+v", gotTasks)
	}

	gotCoverage, err := runDir.ReadCoverage()
	if err != nil {
		t.Fatalf("ReadCoverage: %v", err)
	}
	if len(gotCoverage) != 1 || gotCoverage[0].RequirementID != fixture.coverage[0].RequirementID {
		t.Fatalf("unexpected coverage: %+v", gotCoverage)
	}

	gotStatus, err := runDir.ReadStatus()
	if err != nil {
		t.Fatalf("ReadStatus: %v", err)
	}
	if gotStatus.RunID != fixture.status.RunID || gotStatus.TaskCountsByState["pending"] != 1 {
		t.Fatalf("unexpected status: %+v", gotStatus)
	}
}

func assertRoundTripPlanningRecords(t *testing.T, runDir *state.RunDir, fixture roundTripFixture) {
	t.Helper()

	gotTechReview, err := runDir.ReadTechnicalSpecReview()
	if err != nil {
		t.Fatalf("ReadTechnicalSpecReview: %v", err)
	}
	if gotTechReview.RunID != fixture.techReview.RunID || gotTechReview.Status != state.ReviewPass {
		t.Fatalf("unexpected technical spec review: %+v", gotTechReview)
	}

	gotExecPlan, err := runDir.ReadExecutionPlan()
	if err != nil {
		t.Fatalf("ReadExecutionPlan: %v", err)
	}
	if len(gotExecPlan.Slices) != 1 || gotExecPlan.Slices[0].SliceID != "slice-001" {
		t.Fatalf("unexpected execution plan: %+v", gotExecPlan)
	}

	gotExecReview, err := runDir.ReadExecutionPlanReview()
	if err != nil {
		t.Fatalf("ReadExecutionPlanReview: %v", err)
	}
	if gotExecReview.ExecutionPlanHash != fixture.execReview.ExecutionPlanHash || gotExecReview.Status != state.ReviewPass {
		t.Fatalf("unexpected execution plan review: %+v", gotExecReview)
	}
	if !reflect.DeepEqual(gotExecReview.SharedFileRegistry, fixture.execReview.SharedFileRegistry) {
		t.Fatalf("unexpected shared file registry: %+v", gotExecReview.SharedFileRegistry)
	}
	if !reflect.DeepEqual(gotExecReview.Warnings, fixture.execReview.Warnings) {
		t.Fatalf("unexpected warnings: %+v", gotExecReview.Warnings)
	}

	gotApproval, err := runDir.ReadExecutionPlanApproval()
	if err != nil {
		t.Fatalf("ReadExecutionPlanApproval: %v", err)
	}
	if gotApproval.ArtifactHash != fixture.approval.ArtifactHash || gotApproval.Status != state.ArtifactApproved {
		t.Fatalf("unexpected execution plan approval: %+v", gotApproval)
	}

	gotTechApproval, err := runDir.ReadTechnicalSpecApproval()
	if err != nil {
		t.Fatalf("ReadTechnicalSpecApproval: %v", err)
	}
	if gotTechApproval.ArtifactHash != fixture.techApproval.ArtifactHash || gotTechApproval.Status != state.ArtifactUnderReview {
		t.Fatalf("unexpected technical spec approval: %+v", gotTechApproval)
	}
}

func assertRoundTripMarkdown(t *testing.T, runDir *state.RunDir, fixture roundTripFixture) {
	t.Helper()

	techSpecData, err := runDir.ReadTechnicalSpec()
	if err != nil {
		t.Fatalf("ReadTechnicalSpec: %v", err)
	}
	if !bytes.Equal(techSpecData, fixture.techSpecMarkdown) {
		t.Fatalf("unexpected technical spec markdown: %q", string(techSpecData))
	}

	execPlanData, err := runDir.ReadExecutionPlanMarkdown()
	if err != nil {
		t.Fatalf("ReadExecutionPlanMarkdown: %v", err)
	}
	if !bytes.Equal(execPlanData, fixture.execPlanMarkdown) {
		t.Fatalf("unexpected execution plan markdown: %q", string(execPlanData))
	}
}

func TestRunDirReadWriteRoundTrip(t *testing.T) {
	baseDir := t.TempDir()
	runDir := state.NewRunDir(baseDir, "run-roundtrip")
	if err := runDir.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	now := time.Date(2026, time.March, 12, 10, 0, 0, 0, time.UTC)
	approvedAt := now.Add(30 * time.Minute)
	fixture := newRoundTripFixture(now, approvedAt)

	writeRoundTripFixture(t, runDir, fixture)
	assertRoundTripFixture(t, runDir, fixture)
}

func TestRunDirAppendEventWritesJSONLines(t *testing.T) {
	baseDir := t.TempDir()
	runDir := state.NewRunDir(baseDir, "run-events")
	if err := runDir.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	events := []state.Event{
		{
			Timestamp: time.Date(2026, time.March, 12, 11, 0, 0, 0, time.UTC),
			Type:      "run_state_transition",
			RunID:     "run-events",
			Detail:    "approved",
		},
		{
			Timestamp: time.Date(2026, time.March, 12, 11, 1, 0, 0, time.UTC),
			Type:      "tasks_compiled",
			RunID:     "run-events",
			TaskID:    "task-at-fr-001",
			Detail:    "compiled 1 task",
		},
	}

	for i := range events {
		if err := runDir.AppendEvent(events[i]); err != nil {
			t.Fatalf("AppendEvent(%d): %v", i, err)
		}
	}

	file, err := os.Open(runDir.Events())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = file.Close() }()

	var decoded []state.Event
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var event state.Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("Unmarshal event: %v", err)
		}
		decoded = append(decoded, event)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(decoded) != len(events) {
		t.Fatalf("got %d events, want %d", len(decoded), len(events))
	}
	if decoded[1].TaskID != "task-at-fr-001" || decoded[1].Detail != "compiled 1 task" {
		t.Fatalf("unexpected decoded events: %+v", decoded)
	}
}

func TestRunDirTaskStoreReadWriteTask(t *testing.T) {
	baseDir := t.TempDir()
	runDir := state.NewRunDir(baseDir, "run-store-test")
	if err := runDir.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	store := runDir.AsTaskStore()

	// Write tasks via store.
	tasks := []state.Task{
		{TaskID: "task-1", Title: "First", Status: state.TaskPending},
		{TaskID: "task-2", Title: "Second", Status: state.TaskPending},
	}
	if err := store.WriteTasks("run-store-test", tasks); err != nil {
		t.Fatalf("WriteTasks: %v", err)
	}

	// Read all tasks.
	got, err := store.ReadTasks("run-store-test")
	if err != nil {
		t.Fatalf("ReadTasks: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d tasks, want 2", len(got))
	}

	// Read single task.
	task, err := store.ReadTask("task-1")
	if err != nil {
		t.Fatalf("ReadTask: %v", err)
	}
	if task.Title != "First" {
		t.Errorf("Title = %q, want First", task.Title)
	}

	// Read nonexistent.
	_, err = store.ReadTask("nonexistent")
	if err == nil {
		t.Error("ReadTask should error on nonexistent")
	}

	// WriteTask updates existing.
	task.Status = state.TaskDone
	if err := store.WriteTask(task); err != nil {
		t.Fatalf("WriteTask: %v", err)
	}
	updated, _ := store.ReadTask("task-1")
	if updated.Status != state.TaskDone {
		t.Errorf("Status = %q, want done", updated.Status)
	}

	// WriteTask appends new.
	if err := store.WriteTask(&state.Task{TaskID: "task-3", Title: "Third"}); err != nil {
		t.Fatalf("WriteTask (new): %v", err)
	}
	all, _ := store.ReadTasks("run-store-test")
	if len(all) != 3 {
		t.Fatalf("got %d tasks after append, want 3", len(all))
	}
}

func TestRunDirTaskStoreUpdateStatus(t *testing.T) {
	baseDir := t.TempDir()
	runDir := state.NewRunDir(baseDir, "run-status-test")
	if err := runDir.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	store := runDir.AsTaskStore()
	if err := store.WriteTasks("run-status-test", []state.Task{
		{TaskID: "task-1", Status: state.TaskPending},
	}); err != nil {
		t.Fatal(err)
	}

	if err := store.UpdateStatus("task-1", state.TaskDone, "verified"); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}

	task, _ := store.ReadTask("task-1")
	if task.Status != state.TaskDone {
		t.Errorf("Status = %q, want done", task.Status)
	}
	if task.StatusReason != "verified" {
		t.Errorf("StatusReason = %q, want verified", task.StatusReason)
	}
	if task.UpdatedAt.IsZero() {
		t.Error("UpdatedAt should be set")
	}

	// Nonexistent task.
	err := store.UpdateStatus("nonexistent", state.TaskDone, "")
	if err == nil {
		t.Error("UpdateStatus should error on nonexistent")
	}
}

func TestRunDirTaskStoreCreateRunNoop(t *testing.T) {
	baseDir := t.TempDir()
	runDir := state.NewRunDir(baseDir, "run-noop")
	store := runDir.AsTaskStore()
	if err := store.CreateRun("run-noop"); err != nil {
		t.Fatalf("CreateRun should be no-op: %v", err)
	}
}
