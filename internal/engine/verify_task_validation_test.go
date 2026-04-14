package engine_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/php-workx/fabrikk/internal/engine"
	"github.com/php-workx/fabrikk/internal/state"
)

func setupValidationEngine(t *testing.T) (*engine.Engine, *state.RunDir, string) {
	t.Helper()
	baseDir := t.TempDir()
	runDir := state.NewRunDir(baseDir, "run-verify-validation")
	if err := runDir.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := runDir.WriteArtifact(&state.RunArtifact{RunID: "run-verify-validation"}); err != nil {
		t.Fatalf("WriteArtifact: %v", err)
	}
	return engine.New(runDir, baseDir), runDir, baseDir
}

func baseValidationTask() *state.Task {
	return &state.Task{
		TaskID:         "task-validation",
		Title:          "Validate checks",
		TaskType:       "implementation",
		Status:         state.TaskPending,
		RequirementIDs: []string{"AT-FR-001"},
	}
}

func baseValidationReport() *state.CompletionReport {
	return &state.CompletionReport{TaskID: "task-validation", AttemptID: "attempt-1"}
}

func TestVerifyTaskRunsAllowlistedValidationCommand(t *testing.T) {
	eng, _, _ := setupValidationEngine(t)
	task := baseValidationTask()
	task.ValidationChecks = []state.ValidationCheck{{Type: "command", Tool: "go", Args: []string{"env", "GOROOT"}}}

	result, err := eng.VerifyTask(context.Background(), task, baseValidationReport())
	if err != nil {
		t.Fatalf("VerifyTask: %v", err)
	}
	if !result.Pass {
		t.Fatalf("VerifyTask pass = false, findings = %+v", result.BlockingFindings)
	}
	found := false
	for _, check := range result.EvidenceChecks {
		if check.Name == "validation_check_01" {
			found = true
			if !check.Pass {
				t.Fatalf("validation check failed: %+v", check)
			}
		}
	}
	if !found {
		t.Fatal("missing validation_check_01 evidence entry")
	}
}

func TestVerifyTaskRejectsNonAllowlistedValidationTool(t *testing.T) {
	eng, _, _ := setupValidationEngine(t)
	task := baseValidationTask()
	task.ValidationChecks = []state.ValidationCheck{{Type: "command", Tool: "ruby", Args: []string{"verify.rb"}}}

	result, err := eng.VerifyTask(context.Background(), task, baseValidationReport())
	if err != nil {
		t.Fatalf("VerifyTask: %v", err)
	}
	if result.Pass {
		t.Fatal("VerifyTask unexpectedly passed")
	}
	if len(result.BlockingFindings) == 0 || result.BlockingFindings[0].Category != "validation_tool_allowlist" {
		t.Fatalf("BlockingFindings = %+v, want allowlist rejection", result.BlockingFindings)
	}
}

func TestVerifyTaskCapturesFailedValidationCommandOutput(t *testing.T) {
	eng, _, _ := setupValidationEngine(t)
	task := baseValidationTask()
	task.ValidationChecks = []state.ValidationCheck{{Type: "command", Tool: "go", Args: []string{"not-a-real-subcommand"}}}

	result, err := eng.VerifyTask(context.Background(), task, baseValidationReport())
	if err != nil {
		t.Fatalf("VerifyTask: %v", err)
	}
	if result.Pass {
		t.Fatal("VerifyTask unexpectedly passed")
	}
	if len(result.BlockingFindings) == 0 || result.BlockingFindings[0].Category != "validation_command" {
		t.Fatalf("BlockingFindings = %+v, want validation_command finding", result.BlockingFindings)
	}
	if !strings.Contains(result.BlockingFindings[0].Summary, "exit code") {
		t.Fatalf("finding summary = %q, want exit code detail", result.BlockingFindings[0].Summary)
	}
}

func TestVerifyTaskHandlesFileAndContentChecks(t *testing.T) {
	eng, _, baseDir := setupValidationEngine(t)
	task := baseValidationTask()
	relPath := filepath.Join("internal", "engine", "validation.txt")
	absPath := filepath.Join(baseDir, relPath)
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(absPath, []byte("validation-content"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	task.ValidationChecks = []state.ValidationCheck{
		{Type: "files_exist", Paths: []string{relPath}},
		{Type: "content_check", File: relPath, Pattern: "validation-content"},
	}

	result, err := eng.VerifyTask(context.Background(), task, baseValidationReport())
	if err != nil {
		t.Fatalf("VerifyTask: %v", err)
	}
	if !result.Pass {
		t.Fatalf("VerifyTask pass = false, findings = %+v", result.BlockingFindings)
	}
}

func TestVerifyTaskRejectsValidationPathsOutsideWorkDir(t *testing.T) {
	eng, _, _ := setupValidationEngine(t)
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "outside.txt")
	if err := os.WriteFile(outsideFile, []byte("outside"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	task := baseValidationTask()
	task.ValidationChecks = []state.ValidationCheck{{Type: "files_exist", Paths: []string{outsideFile}}}

	result, err := eng.VerifyTask(context.Background(), task, baseValidationReport())
	if err != nil {
		t.Fatalf("VerifyTask: %v", err)
	}
	if result.Pass {
		t.Fatal("VerifyTask unexpectedly passed")
	}
	if len(result.BlockingFindings) == 0 || result.BlockingFindings[0].Category != "validation_path" {
		t.Fatalf("BlockingFindings = %+v, want validation_path rejection", result.BlockingFindings)
	}
}

func TestVerifyTaskSkipsDuplicateQualityGateValidationCheck(t *testing.T) {
	eng, runDir, _ := setupValidationEngine(t)
	if err := runDir.WriteArtifact(&state.RunArtifact{
		RunID:       "run-verify-validation",
		QualityGate: &state.QualityGate{Command: "go env GOROOT", Required: true},
	}); err != nil {
		t.Fatalf("WriteArtifact: %v", err)
	}

	task := baseValidationTask()
	task.ValidationChecks = []state.ValidationCheck{{Type: "command", Tool: "go", Args: []string{"env", "GOROOT"}}}

	result, err := eng.VerifyTask(context.Background(), task, baseValidationReport())
	if err != nil {
		t.Fatalf("VerifyTask: %v", err)
	}
	if !result.Pass {
		t.Fatalf("VerifyTask pass = false, findings = %+v", result.BlockingFindings)
	}
	for _, check := range result.EvidenceChecks {
		if check.Name == "validation_check_01" {
			t.Fatalf("quality gate validation duplicated in evidence checks: %+v", result.EvidenceChecks)
		}
	}
}
