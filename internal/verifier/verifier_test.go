package verifier_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/php-workx/fabrikk/internal/state"
	"github.com/php-workx/fabrikk/internal/verifier"
)

func TestVerifyPassesWithValidEvidence(t *testing.T) {
	task := &state.Task{
		TaskID:           "task-1",
		RequirementIDs:   []string{"AT-FR-001"},
		RequiredEvidence: []string{"quality_gate_pass"},
	}

	report := &state.CompletionReport{
		TaskID:    "task-1",
		AttemptID: "attempt-1",
		CommandResults: []state.CommandResult{
			{CommandID: "cmd-1", Command: "go test", ExitCode: 0, Required: true},
		},
	}

	result, err := verifier.Verify(context.Background(), task, report, nil, t.TempDir())
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !result.Pass {
		t.Errorf("expected pass, got fail with findings: %v", result.BlockingFindings)
	}
}

// AT-TS-003: A task with no independent verifier evidence cannot close.
func TestVerifyFailsWithNoReport(t *testing.T) {
	task := &state.Task{
		TaskID:           "task-1",
		RequirementIDs:   []string{"AT-FR-001"},
		RequiredEvidence: []string{"test_pass"},
	}

	report := &state.CompletionReport{
		TaskID:    "task-1",
		AttemptID: "attempt-1",
		// No command results — missing evidence.
	}

	result, err := verifier.Verify(context.Background(), task, report, nil, t.TempDir())
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if result.Pass {
		t.Error("expected fail for missing evidence, got pass")
	}
	if len(result.BlockingFindings) == 0 {
		t.Error("expected blocking findings")
	}
}

func TestVerifyFailsWithNoRequirementIDs(t *testing.T) {
	task := &state.Task{
		TaskID:         "task-1",
		RequirementIDs: []string{}, // No linked requirements.
	}

	report := &state.CompletionReport{
		TaskID:    "task-1",
		AttemptID: "attempt-1",
	}

	result, err := verifier.Verify(context.Background(), task, report, nil, t.TempDir())
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if result.Pass {
		t.Error("expected fail for no requirement linkage, got pass")
	}
}

func TestVerifyScopeViolation(t *testing.T) {
	task := &state.Task{
		TaskID:         "task-1",
		RequirementIDs: []string{"AT-FR-001"},
		Scope: state.TaskScope{
			OwnedPaths: []string{"internal/engine/"},
		},
		RequiredEvidence: []string{"quality_gate_pass"},
	}

	report := &state.CompletionReport{
		TaskID:    "task-1",
		AttemptID: "attempt-1",
		ChangedFiles: []string{
			"cmd/fabrikk/main.go", // Outside scope.
		},
	}

	result, err := verifier.Verify(context.Background(), task, report, nil, t.TempDir())
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if result.Pass {
		t.Error("expected fail for scope violation, got pass")
	}
}

// AT-TS-025: A task whose implementation fails the quality gate cannot proceed to model review.
func TestVerifyQualityGateBlocksReview(t *testing.T) {
	task := &state.Task{
		TaskID:           "task-1",
		RequirementIDs:   []string{"AT-FR-001"},
		RequiredEvidence: []string{"quality_gate_pass"},
	}

	report := &state.CompletionReport{
		TaskID:    "task-1",
		AttemptID: "attempt-1",
	}

	gate := &state.QualityGate{
		Command:        "false", // Always fails.
		TimeoutSeconds: 10,
		Required:       true,
	}

	result, err := verifier.Verify(context.Background(), task, report, gate, t.TempDir())
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if result.Pass {
		t.Error("expected fail when quality gate fails")
	}

	// Verify the failure is a quality gate finding.
	found := false
	for _, f := range result.BlockingFindings {
		if f.Category == "quality_gate" {
			found = true
		}
	}
	if !found {
		t.Error("expected quality_gate category in findings")
	}
}

func TestVerifyQualityGateRunErrorDoesNotPanic(t *testing.T) {
	task := &state.Task{
		TaskID:           "task-1",
		RequirementIDs:   []string{"AT-FR-001"},
		RequiredEvidence: []string{"quality_gate_pass"},
	}

	report := &state.CompletionReport{
		TaskID:    "task-1",
		AttemptID: "attempt-1",
	}

	gate := &state.QualityGate{
		Command:        "true",
		TimeoutSeconds: 10,
		Required:       true,
	}

	result, err := verifier.Verify(context.Background(), task, report, gate, filepath.Join(t.TempDir(), "missing"))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if result.Pass {
		t.Fatal("expected fail when quality gate cannot start")
	}
	if len(result.EvidenceChecks) == 0 || !strings.Contains(result.EvidenceChecks[0].Detail, "quality gate failed before producing a command result") {
		t.Fatalf("evidence checks = %+v, want startup failure detail", result.EvidenceChecks)
	}
}

func TestVerifyFailsWhenRequiredCommandFails(t *testing.T) {
	task := &state.Task{
		TaskID:           "task-1",
		RequirementIDs:   []string{"AT-FR-001"},
		RequiredEvidence: []string{"quality_gate_pass"},
	}

	report := &state.CompletionReport{
		TaskID:    "task-1",
		AttemptID: "attempt-1",
		CommandResults: []state.CommandResult{
			{CommandID: "cmd-1", Command: "go test ./...", ExitCode: 1, Required: true},
		},
	}

	result, err := verifier.Verify(context.Background(), task, report, nil, t.TempDir())
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if result.Pass {
		t.Fatal("expected fail when required command fails")
	}
	if len(result.BlockingFindings) == 0 || !strings.Contains(result.BlockingFindings[0].Summary, "required command") {
		t.Fatalf("blocking findings = %+v, want required command failure", result.BlockingFindings)
	}
}

func TestVerifyFailsWithInvalidRequirementID(t *testing.T) {
	task := &state.Task{
		TaskID:           "task-1",
		RequirementIDs:   []string{"BAD-001"},
		RequiredEvidence: []string{"quality_gate_pass"},
	}

	report := &state.CompletionReport{
		TaskID:    "task-1",
		AttemptID: "attempt-1",
	}

	result, err := verifier.Verify(context.Background(), task, report, nil, t.TempDir())
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if result.Pass {
		t.Fatal("expected fail for invalid requirement ID")
	}

	found := false
	for i := range result.BlockingFindings {
		if result.BlockingFindings[i].Category == "requirement_linkage" &&
			strings.Contains(result.BlockingFindings[i].Summary, "invalid requirement ID format") {
			found = true
		}
	}
	if !found {
		t.Fatalf("blocking findings = %+v, want invalid requirement ID finding", result.BlockingFindings)
	}
}

func TestVerifyPassesWithFileExistsEvidence(t *testing.T) {
	task := &state.Task{
		TaskID:           "task-1",
		RequirementIDs:   []string{"AT-FR-001"},
		RequiredEvidence: []string{"file_exists"},
	}

	report := &state.CompletionReport{
		TaskID:            "task-1",
		AttemptID:         "attempt-1",
		ArtifactsProduced: []string{"coverage.out"},
	}

	result, err := verifier.Verify(context.Background(), task, report, nil, t.TempDir())
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !result.Pass {
		t.Fatalf("expected pass, got findings %+v", result.BlockingFindings)
	}
}

func TestVerifyFileExists(t *testing.T) {
	workDir := t.TempDir()
	existing := filepath.Join(workDir, "reports", "result.txt")
	if err := os.MkdirAll(filepath.Dir(existing), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(existing, []byte("ok"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if !verifier.VerifyFileExists(workDir, filepath.Join("reports", "result.txt")) {
		t.Fatal("VerifyFileExists(existing) = false, want true")
	}
	if verifier.VerifyFileExists(workDir, filepath.Join("reports", "missing.txt")) {
		t.Fatal("VerifyFileExists(missing) = true, want false")
	}
}
