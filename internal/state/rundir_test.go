package state_test

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/runger/attest/internal/state"
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
		{name: "artifact", got: runDir.Artifact(), want: "/tmp/project/.attest/runs/run-456/run-artifact.json"},
		{name: "tasks", got: runDir.Tasks(), want: "/tmp/project/.attest/runs/run-456/tasks.json"},
		{name: "coverage", got: runDir.Coverage(), want: "/tmp/project/.attest/runs/run-456/requirement-coverage.json"},
		{name: "status", got: runDir.Status(), want: "/tmp/project/.attest/runs/run-456/run-status.json"},
		{name: "events", got: runDir.Events(), want: "/tmp/project/.attest/runs/run-456/events.jsonl"},
		{name: "clarifications", got: runDir.Clarifications(), want: "/tmp/project/.attest/runs/run-456/clarifications.json"},
		{name: "engine", got: runDir.Engine(), want: "/tmp/project/.attest/runs/run-456/engine.json"},
		{name: "engine log", got: runDir.EngineLog(), want: "/tmp/project/.attest/runs/run-456/engine.log"},
		{name: "claim path", got: runDir.ClaimPath("task-1"), want: "/tmp/project/.attest/runs/run-456/claims/task-1.json"},
		{name: "report dir", got: runDir.ReportDir("task-1"), want: "/tmp/project/.attest/runs/run-456/reports/task-1"},
		{name: "attempt path", got: runDir.AttemptPath("task-1"), want: "/tmp/project/.attest/runs/run-456/reports/task-1/attempt.json"},
		{name: "council result path", got: runDir.CouncilResultPath("task-1"), want: "/tmp/project/.attest/runs/run-456/reports/task-1/council-result.json"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Fatalf("got %q, want %q", tt.got, tt.want)
			}
		})
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

	artifact := &state.RunArtifact{
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
	}
	tasks := []state.Task{
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
	}
	coverage := []state.RequirementCoverage{
		{
			RequirementID:   "AT-FR-001",
			Status:          "in_progress",
			CoveringTaskIDs: []string{"task-at-fr-001"},
		},
	}
	status := &state.RunStatus{
		RunID:              "run-roundtrip",
		State:              state.RunApproved,
		LastTransitionTime: now,
		TaskCountsByState:  map[string]int{"pending": 1},
	}

	if err := runDir.WriteArtifact(artifact); err != nil {
		t.Fatalf("WriteArtifact: %v", err)
	}
	if err := runDir.WriteTasks(tasks); err != nil {
		t.Fatalf("WriteTasks: %v", err)
	}
	if err := runDir.WriteCoverage(coverage); err != nil {
		t.Fatalf("WriteCoverage: %v", err)
	}
	if err := runDir.WriteStatus(status); err != nil {
		t.Fatalf("WriteStatus: %v", err)
	}

	gotArtifact, err := runDir.ReadArtifact()
	if err != nil {
		t.Fatalf("ReadArtifact: %v", err)
	}
	if gotArtifact.RunID != artifact.RunID || gotArtifact.ApprovedBy != artifact.ApprovedBy {
		t.Fatalf("unexpected artifact: %+v", gotArtifact)
	}

	gotTasks, err := runDir.ReadTasks()
	if err != nil {
		t.Fatalf("ReadTasks: %v", err)
	}
	if len(gotTasks) != 1 || gotTasks[0].TaskID != tasks[0].TaskID {
		t.Fatalf("unexpected tasks: %+v", gotTasks)
	}

	gotCoverage, err := runDir.ReadCoverage()
	if err != nil {
		t.Fatalf("ReadCoverage: %v", err)
	}
	if len(gotCoverage) != 1 || gotCoverage[0].RequirementID != coverage[0].RequirementID {
		t.Fatalf("unexpected coverage: %+v", gotCoverage)
	}

	gotStatus, err := runDir.ReadStatus()
	if err != nil {
		t.Fatalf("ReadStatus: %v", err)
	}
	if gotStatus.RunID != status.RunID || gotStatus.TaskCountsByState["pending"] != 1 {
		t.Fatalf("unexpected status: %+v", gotStatus)
	}
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
