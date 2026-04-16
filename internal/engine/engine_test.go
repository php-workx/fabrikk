package engine_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/php-workx/fabrikk/internal/agentcli"
	"github.com/php-workx/fabrikk/internal/compiler"
	"github.com/php-workx/fabrikk/internal/engine"
	"github.com/php-workx/fabrikk/internal/state"
	"github.com/php-workx/fabrikk/internal/ticket"
)

func writeTestSpec(t *testing.T, dir string) string {
	t.Helper()
	spec := filepath.Join(dir, "test-spec.md")
	content := `# Test Spec

- **AT-FR-001**: The system must ingest specs.
- **AT-FR-002**: The system must compile tasks.
- **AT-TS-001**: Deterministic compilation scenario.
`
	if err := os.WriteFile(spec, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return spec
}

func writeApprovedTechnicalSpec(t *testing.T, runDir *state.RunDir) string {
	t.Helper()

	specContent := []byte("# Spec\n\n## 1. Technical context\nA\n\n## 2. Architecture\nB\n\n## 3. Canonical artifacts and schemas\nC\n\n## 4. Interfaces\nD\n\n## 5. Verification\nE\n\n## 6. Requirement traceability\nF\n\n## 7. Open questions and risks\nG\n\n## 8. Approval\nH\n")
	if err := runDir.WriteTechnicalSpec(specContent); err != nil {
		t.Fatalf("WriteTechnicalSpec: %v", err)
	}
	hash := "sha256:" + state.SHA256Bytes(specContent)
	if err := runDir.WriteTechnicalSpecApproval(&state.ArtifactApproval{
		SchemaVersion: "0.1",
		RunID:         filepathBase(runDir.Root),
		ArtifactType:  "technical_spec_approval",
		ArtifactPath:  "technical-spec.md",
		ArtifactHash:  hash,
		Status:        state.ArtifactApproved,
		ApprovedBy:    "tester",
		ApprovedAt:    time.Now().UTC(),
	}); err != nil {
		t.Fatalf("WriteTechnicalSpecApproval: %v", err)
	}
	return hash
}

func filepathBase(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == os.PathSeparator {
			return path[i+1:]
		}
	}
	return path
}

func writeApprovedExecutionPlanForArtifact(t *testing.T, runDir *state.RunDir, artifact *state.RunArtifact) {
	t.Helper()

	techSpecHash := writeApprovedTechnicalSpec(t, runDir)

	compiled, err := compiler.Compile(artifact)
	if err != nil {
		t.Fatalf("compiler.Compile: %v", err)
	}

	plan := &state.ExecutionPlan{
		SchemaVersion:           "0.1",
		RunID:                   artifact.RunID,
		ArtifactType:            "execution_plan",
		SourceTechnicalSpecHash: techSpecHash,
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
			DependsOn:          trimTaskPrefix(task.DependsOn),
			FilesLikelyTouched: append([]string(nil), task.Scope.OwnedPaths...),
			OwnedPaths:         append([]string(nil), task.Scope.OwnedPaths...),
			AcceptanceChecks:   []string{"just check", "fabrikk verify <run-id> <task-id>"},
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

func trimTaskPrefix(taskIDs []string) []string {
	trimmed := make([]string, 0, len(taskIDs))
	for _, taskID := range taskIDs {
		trimmed = append(trimmed, strings.TrimPrefix(taskID, "task-"))
	}
	return trimmed
}

func TestPrepareAndCompile(t *testing.T) {
	dir := t.TempDir()
	specPath := writeTestSpec(t, dir)

	runDir := state.NewRunDir(dir, "placeholder")
	eng := engine.New(runDir, dir)

	ctx := context.Background()

	// Prepare.
	artifact, err := eng.Prepare(ctx, []string{specPath})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if len(artifact.Requirements) != 3 {
		t.Fatalf("got %d requirements, want 3", len(artifact.Requirements))
	}
	if artifact.RunID == "" {
		t.Error("run ID is empty")
	}
	status, err := eng.RunDir.ReadStatus()
	if err != nil {
		t.Fatalf("ReadStatus after Prepare: %v", err)
	}
	if status.State != state.RunAwaitingApproval {
		t.Fatalf("status state after Prepare = %s, want %s", status.State, state.RunAwaitingApproval)
	}

	// Set up execution plan before approval (atomicity: Approve validates plan exists).
	writeApprovedExecutionPlanForArtifact(t, eng.RunDir, artifact)

	// Approve.
	if err := eng.Approve(ctx); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	status, err = eng.RunDir.ReadStatus()
	if err != nil {
		t.Fatalf("ReadStatus after Approve: %v", err)
	}
	if status.State != state.RunApproved {
		t.Fatalf("status state after Approve = %s, want %s", status.State, state.RunApproved)
	}

	// Compile.
	result, err := eng.Compile(ctx)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(result.Tasks) == 0 {
		t.Fatal("got 0 tasks, want at least 1")
	}
	status, err = eng.RunDir.ReadStatus()
	if err != nil {
		t.Fatalf("ReadStatus after Compile: %v", err)
	}
	if status.State != state.RunRunning {
		t.Fatalf("status state after Compile = %s, want %s", status.State, state.RunRunning)
	}
	if status.TaskCountsByState[string(state.TaskPending)] != len(result.Tasks) {
		t.Fatalf("pending count after Compile = %d, want %d", status.TaskCountsByState[string(state.TaskPending)], len(result.Tasks))
	}

	// All tasks should be pending.
	for _, task := range result.Tasks {
		if task.Status != state.TaskPending {
			t.Errorf("task %s status = %s, want pending", task.TaskID, task.Status)
		}
	}
}

func TestReconcileRunStatusPreservesArtifactApprovalGate(t *testing.T) {
	dir := t.TempDir()
	runID := "run-artifact-approval"
	runDir := state.NewRunDir(dir, runID)
	if err := runDir.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	artifact := &state.RunArtifact{
		SchemaVersion: "0.1",
		RunID:         runID,
		SourceSpecs:   []state.SourceSpec{{Path: "spec.md", Fingerprint: "sha256:spec"}},
	}
	if err := runDir.WriteArtifact(artifact); err != nil {
		t.Fatalf("WriteArtifact: %v", err)
	}

	if err := runDir.WriteStatus(&state.RunStatus{
		RunID:              runID,
		State:              state.RunAwaitingArtifactApproval,
		CurrentGate:        "awaiting_artifact_approval",
		TaskCountsByState:  map[string]int{},
		LastTransitionTime: time.Now(),
	}); err != nil {
		t.Fatalf("WriteStatus: %v", err)
	}

	eng := engine.New(runDir, dir)
	status, err := eng.ReconcileRunStatus()
	if err != nil {
		t.Fatalf("ReconcileRunStatus: %v", err)
	}
	if status.State != state.RunAwaitingArtifactApproval {
		t.Fatalf("status state after ReconcileRunStatus = %s, want %s", status.State, state.RunAwaitingArtifactApproval)
	}
	if status.CurrentGate != "awaiting_artifact_approval" {
		t.Fatalf("current gate after ReconcileRunStatus = %q, want awaiting_artifact_approval", status.CurrentGate)
	}
}

// AT-TS-001: Preparing the same artifact twice yields the same tasks.
func TestCompileDeterminism(t *testing.T) {
	dir := t.TempDir()
	specPath := writeTestSpec(t, dir)
	ctx := context.Background()

	// First run.
	runDir1 := state.NewRunDir(dir, "placeholder1")
	eng1 := engine.New(runDir1, dir)
	if _, err := eng1.Prepare(ctx, []string{specPath}); err != nil {
		t.Fatal(err)
	}
	artifact1, err := eng1.RunDir.ReadArtifact()
	if err != nil {
		t.Fatal(err)
	}
	writeApprovedExecutionPlanForArtifact(t, eng1.RunDir, artifact1)
	if err := eng1.Approve(ctx); err != nil {
		t.Fatal(err)
	}
	result1, err := eng1.Compile(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Second run.
	runDir2 := state.NewRunDir(dir, "placeholder2")
	eng2 := engine.New(runDir2, dir)
	if _, err := eng2.Prepare(ctx, []string{specPath}); err != nil {
		t.Fatal(err)
	}
	artifact2, err := eng2.RunDir.ReadArtifact()
	if err != nil {
		t.Fatal(err)
	}
	writeApprovedExecutionPlanForArtifact(t, eng2.RunDir, artifact2)
	if err := eng2.Approve(ctx); err != nil {
		t.Fatal(err)
	}
	result2, err := eng2.Compile(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Task IDs must be identical.
	if len(result1.Tasks) != len(result2.Tasks) {
		t.Fatalf("task count differs: %d vs %d", len(result1.Tasks), len(result2.Tasks))
	}
	for i := range result1.Tasks {
		if result1.Tasks[i].TaskID != result2.Tasks[i].TaskID {
			t.Errorf("task %d ID differs: %q vs %q", i, result1.Tasks[i].TaskID, result2.Tasks[i].TaskID)
		}
	}
}

func TestGetPendingTasks(t *testing.T) {
	dir := t.TempDir()
	specPath := writeTestSpec(t, dir)
	ctx := context.Background()

	runDir := state.NewRunDir(dir, "placeholder")
	eng := engine.New(runDir, dir)

	if _, err := eng.Prepare(ctx, []string{specPath}); err != nil {
		t.Fatal(err)
	}
	artifact, err := eng.RunDir.ReadArtifact()
	if err != nil {
		t.Fatal(err)
	}
	writeApprovedExecutionPlanForArtifact(t, eng.RunDir, artifact)
	if err := eng.Approve(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Compile(ctx); err != nil {
		t.Fatal(err)
	}

	pending, err := eng.GetPendingTasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 2 {
		t.Fatalf("got %d pending tasks, want 2 grouped tasks", len(pending))
	}
}

func TestUpdateTaskStatus(t *testing.T) {
	dir := t.TempDir()
	specPath := writeTestSpec(t, dir)
	ctx := context.Background()

	runDir := state.NewRunDir(dir, "placeholder")
	eng := engine.New(runDir, dir)

	if _, err := eng.Prepare(ctx, []string{specPath}); err != nil {
		t.Fatal(err)
	}
	artifact, err := eng.RunDir.ReadArtifact()
	if err != nil {
		t.Fatal(err)
	}
	writeApprovedExecutionPlanForArtifact(t, eng.RunDir, artifact)
	if err := eng.Approve(ctx); err != nil {
		t.Fatal(err)
	}
	result, err := eng.Compile(ctx)
	if err != nil {
		t.Fatal(err)
	}

	taskID := result.Tasks[0].TaskID
	if err := eng.UpdateTaskStatus(taskID, state.TaskDone, "verified"); err != nil {
		t.Fatalf("UpdateTaskStatus: %v", err)
	}

	// Verify the change persisted.
	tasks, err := eng.RunDir.ReadTasks()
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if task.TaskID == taskID && task.Status != state.TaskDone {
			t.Errorf("task %s status = %s after update, want done", taskID, task.Status)
		}
	}
}

func TestApproveWithoutArtifactFails(t *testing.T) {
	eng := engine.New(state.NewRunDir(t.TempDir(), "missing"), t.TempDir())

	err := eng.Approve(context.Background())
	if err == nil {
		t.Fatal("Approve() error = nil, want read artifact error")
	}
	if !strings.Contains(err.Error(), "read artifact") {
		t.Fatalf("Approve() error = %q, want read artifact context", err)
	}
}

func TestApproveRequiresApprovedExecutionPlan(t *testing.T) {
	dir := t.TempDir()
	specPath := writeTestSpec(t, dir)
	ctx := context.Background()

	runDir := state.NewRunDir(dir, "placeholder")
	eng := engine.New(runDir, dir)

	if _, err := eng.Prepare(ctx, []string{specPath}); err != nil {
		t.Fatal(err)
	}

	// Approve must fail when no execution plan is approved (atomicity guard).
	err := eng.Approve(ctx)
	if err == nil {
		t.Fatal("Approve() error = nil, want execution plan approval error")
	}
	if !strings.Contains(err.Error(), "approved execution plan required") {
		t.Fatalf("Approve() error = %q, want 'approved execution plan required' context", err)
	}

	// Verify run artifact was NOT marked as approved (no state corruption).
	artifact, err := eng.RunDir.ReadArtifact()
	if err != nil {
		t.Fatalf("ReadArtifact: %v", err)
	}
	if artifact.ApprovedAt != nil {
		t.Fatal("run artifact was marked approved despite Approve() failure — state corruption")
	}
}

func TestReviewTechnicalSpecAcceptsLegacyHeadings(t *testing.T) {
	dir := t.TempDir()
	runDir := state.NewRunDir(dir, "run-techspec")
	if err := runDir.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	spec := `# fabrikk Technical Specification

Status: Draft (post-review revision)

## 1. Implementation decisions
Context

## 2. System architecture
Architecture

## 3. Canonical artifacts
Artifacts

## 5. CLI surface
Interfaces

## 11. Council checkpoints and verification pipeline
Verification

## 17. Acceptance scenarios
- **AT-TS-001**: Deterministic review.

## 19. Open v1 decisions
Open questions.

- every implementation task must trace to requirement IDs from this spec
`
	if err := runDir.WriteTechnicalSpec([]byte(spec)); err != nil {
		t.Fatalf("WriteTechnicalSpec: %v", err)
	}

	eng := engine.New(runDir, dir)
	review, err := eng.ReviewTechnicalSpec(context.Background())
	if err != nil {
		t.Fatalf("ReviewTechnicalSpec: %v", err)
	}
	if review.Status != state.ReviewPass {
		t.Fatalf("review status = %s, want %s; findings = %+v", review.Status, state.ReviewPass, review.BlockingFindings)
	}
}

func TestDraftTechnicalSpec_NoNormalizePassThrough(t *testing.T) {
	dir := t.TempDir()
	runDir := state.NewRunDir(dir, "run-no-norm")
	if err := runDir.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	sourcePath := filepath.Join(dir, "legacy-technical-spec.md")
	spec := `# fabrikk Technical Specification

Status: Draft (post-review revision)

## 1. Implementation decisions
Context paragraph with important details.

## 2. System architecture
Architecture paragraph with important details.

## 3. Canonical artifacts
Artifacts paragraph with important details.
`
	if err := os.WriteFile(sourcePath, []byte(spec), 0o644); err != nil {
		t.Fatalf("WriteFile(source): %v", err)
	}

	eng := engine.New(runDir, dir)
	// noNormalize=true: agent should not be called, original content preserved.
	if err := eng.DraftTechnicalSpec(context.Background(), sourcePath, true); err != nil {
		t.Fatalf("DraftTechnicalSpec: %v", err)
	}

	data, err := runDir.ReadTechnicalSpec()
	if err != nil {
		t.Fatalf("ReadTechnicalSpec: %v", err)
	}
	got := string(data)

	// Original headings must be preserved verbatim.
	for _, heading := range []string{
		"## 1. Implementation decisions",
		"## 2. System architecture",
		"## 3. Canonical artifacts",
	} {
		if !strings.Contains(got, heading) {
			t.Fatalf("original heading %q was lost — content must be preserved:\n%s", heading, got)
		}
	}
	if len(data) < len([]byte(spec))-10 {
		t.Fatalf("content truncated: output %d bytes, input %d bytes", len(data), len([]byte(spec)))
	}
}

func TestDraftTechnicalSpec_AgentFallbackOnError(t *testing.T) {
	dir := t.TempDir()
	runDir := state.NewRunDir(dir, "run-agent-err")
	if err := runDir.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	sourcePath := filepath.Join(dir, "non-canonical.md")
	spec := `# My Custom Spec

## Overview
This is a free-form document with no canonical headings.

## Details
Lots of important content here that must not be lost.
`
	if err := os.WriteFile(sourcePath, []byte(spec), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Stub agent to fail.
	original := agentcli.InvokeFunc
	agentcli.InvokeFunc = func(_ context.Context, _ *agentcli.CLIBackend, _ string, _ int) (string, error) {
		return "", fmt.Errorf("agent unavailable")
	}
	t.Cleanup(func() { agentcli.InvokeFunc = original })

	eng := engine.New(runDir, dir)
	if err := eng.DraftTechnicalSpec(context.Background(), sourcePath, false); err != nil {
		t.Fatalf("DraftTechnicalSpec: %v", err)
	}

	data, err := runDir.ReadTechnicalSpec()
	if err != nil {
		t.Fatalf("ReadTechnicalSpec: %v", err)
	}
	// Original content must be preserved when agent fails.
	if !strings.Contains(string(data), "## Overview") {
		t.Fatalf("original content lost after agent failure:\n%s", string(data))
	}
	if !strings.Contains(string(data), "Lots of important content") {
		t.Fatalf("original content lost after agent failure:\n%s", string(data))
	}
}

func TestDraftTechnicalSpec_AgentFallbackOnShortOutput(t *testing.T) {
	dir := t.TempDir()
	runDir := state.NewRunDir(dir, "run-agent-short")
	if err := runDir.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	sourcePath := filepath.Join(dir, "non-canonical.md")
	spec := `# My Custom Spec

## Overview
This is a free-form document with content that spans multiple paragraphs.

## Details
Lots of important content here that must not be lost. This paragraph
contains enough text that a 10-byte agent response would clearly be
too short to preserve the content.
`
	if err := os.WriteFile(sourcePath, []byte(spec), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Stub agent to return suspiciously short output.
	original := agentcli.InvokeFunc
	agentcli.InvokeFunc = func(_ context.Context, _ *agentcli.CLIBackend, _ string, _ int) (string, error) {
		return "too short", nil
	}
	t.Cleanup(func() { agentcli.InvokeFunc = original })

	eng := engine.New(runDir, dir)
	if err := eng.DraftTechnicalSpec(context.Background(), sourcePath, false); err != nil {
		t.Fatalf("DraftTechnicalSpec: %v", err)
	}

	data, err := runDir.ReadTechnicalSpec()
	if err != nil {
		t.Fatalf("ReadTechnicalSpec: %v", err)
	}
	// Original content must be preserved when agent output is too short.
	if !strings.Contains(string(data), "## Overview") {
		t.Fatalf("original content lost after short agent output:\n%s", string(data))
	}
}

func TestDraftTechnicalSpec_AgentFallbackOnGarbledOutput(t *testing.T) {
	dir := t.TempDir()
	runDir := state.NewRunDir(dir, "run-agent-garble")
	if err := runDir.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	sourcePath := filepath.Join(dir, "non-canonical.md")
	spec := `# My Custom Spec

## Overview
This document has no canonical headings at all.

## Implementation Notes
Some important content that must survive.
`
	if err := os.WriteFile(sourcePath, []byte(spec), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Stub agent to return text without canonical headings (long enough to pass length check).
	original := agentcli.InvokeFunc
	agentcli.InvokeFunc = func(_ context.Context, _ *agentcli.CLIBackend, _ string, _ int) (string, error) {
		return "# Garbled Output\n\nThis text has no canonical headings.\n\nIt is long enough to pass the length check but has zero canonical section headings.\n\nMore padding text to exceed 50% of input length.", nil
	}
	t.Cleanup(func() { agentcli.InvokeFunc = original })

	eng := engine.New(runDir, dir)
	if err := eng.DraftTechnicalSpec(context.Background(), sourcePath, false); err != nil {
		t.Fatalf("DraftTechnicalSpec: %v", err)
	}

	data, err := runDir.ReadTechnicalSpec()
	if err != nil {
		t.Fatalf("ReadTechnicalSpec: %v", err)
	}
	// Original content must be preserved when agent output is garbled.
	if !strings.Contains(string(data), "## Overview") {
		t.Fatalf("original content lost after garbled agent output:\n%s", string(data))
	}
}

func TestDraftTechnicalSpec_AgentSuccess(t *testing.T) {
	dir := t.TempDir()
	runDir := state.NewRunDir(dir, "run-agent-ok")
	if err := runDir.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	sourcePath := filepath.Join(dir, "non-canonical.md")
	spec := `# My Custom Spec

## Overview
Some content.
`
	if err := os.WriteFile(sourcePath, []byte(spec), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Stub agent to return well-formed normalized output with canonical headings.
	normalized := `# Technical Specification

## 1. Technical context

Some content.

## 2. Architecture

Derived from overview.

## 3. Canonical artifacts and schemas

No artifacts specified.

## 4. Interfaces

No interfaces specified.

## 5. Verification

No verification specified.

## 7. Open questions and risks

None identified.
`
	original := agentcli.InvokeFunc
	agentcli.InvokeFunc = func(_ context.Context, _ *agentcli.CLIBackend, _ string, _ int) (string, error) {
		return normalized, nil
	}
	t.Cleanup(func() { agentcli.InvokeFunc = original })

	eng := engine.New(runDir, dir)
	if err := eng.DraftTechnicalSpec(context.Background(), sourcePath, false); err != nil {
		t.Fatalf("DraftTechnicalSpec: %v", err)
	}

	data, err := runDir.ReadTechnicalSpec()
	if err != nil {
		t.Fatalf("ReadTechnicalSpec: %v", err)
	}
	got := string(data)
	// Agent's normalized output should be used.
	if !strings.Contains(got, "## 1. Technical context") {
		t.Fatalf("expected canonical heading in output:\n%s", got)
	}
	if !strings.Contains(got, "Some content.") {
		t.Fatalf("expected original content preserved in normalized output:\n%s", got)
	}
}

func TestDraftTechnicalSpec_AgentSectionLossAccepted(t *testing.T) {
	dir := t.TempDir()
	runDir := state.NewRunDir(dir, "run-section-loss")
	if err := runDir.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	sourcePath := filepath.Join(dir, "many-sections.md")
	// Input has 10 sections.
	spec := `# Big Spec
## Section A
Content A.
## Section B
Content B.
## Section C
Content C.
## Section D
Content D.
## Section E
Content E.
## Section F
Content F.
## Section G
Content G.
## Section H
Content H.
## Section I
Content I.
## Section J
Content J.
`
	if err := os.WriteFile(sourcePath, []byte(spec), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Agent consolidates 10 input sections into 8 canonical ones (losing 5 sections).
	// This should produce a warning but NOT reject the output.
	normalized := `# Technical Specification

## 1. Technical context
Content A. Content B.

## 2. Architecture
Content C.

## 3. Canonical artifacts and schemas
Content D.

## 4. Interfaces
Content E.

## 5. Verification
Content F. Content G.

## 6. Requirement traceability
Content H.

## 7. Open questions and risks
Content I.

## 8. Approval
Content J.
`
	original := agentcli.InvokeFunc
	agentcli.InvokeFunc = func(_ context.Context, _ *agentcli.CLIBackend, _ string, _ int) (string, error) {
		return normalized, nil
	}
	t.Cleanup(func() { agentcli.InvokeFunc = original })

	eng := engine.New(runDir, dir)
	// Should succeed despite section count drop (10 → 8). Warning printed but not a rejection.
	if err := eng.DraftTechnicalSpec(context.Background(), sourcePath, false); err != nil {
		t.Fatalf("DraftTechnicalSpec: %v", err)
	}

	data, err := runDir.ReadTechnicalSpec()
	if err != nil {
		t.Fatalf("ReadTechnicalSpec: %v", err)
	}
	// Normalized output should be used (not the original).
	if !strings.Contains(string(data), "## 1. Technical context") {
		t.Fatalf("expected canonical heading in output:\n%s", string(data))
	}
}

func TestDraftTechnicalSpec_NormalConsolidationNoWarning(t *testing.T) {
	dir := t.TempDir()
	runDir := state.NewRunDir(dir, "run-no-warn")
	if err := runDir.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	sourcePath := filepath.Join(dir, "few-sections.md")
	// Input has 4 sections — consolidating to 8 canonical ADDS sections.
	spec := `# Small Spec
## Overview
Content.
## Design
Design content.
## Tests
Test content.
## Risks
Risk content.
`
	if err := os.WriteFile(sourcePath, []byte(spec), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	normalized := `# Technical Specification

## 1. Technical context
Content.

## 2. Architecture
Design content.

## 3. Canonical artifacts and schemas
No artifacts.

## 4. Interfaces
No interfaces.

## 5. Verification
Test content.

## 6. Requirement traceability
No traceability.

## 7. Open questions and risks
Risk content.

## 8. Approval
Status: Draft
`
	original := agentcli.InvokeFunc
	agentcli.InvokeFunc = func(_ context.Context, _ *agentcli.CLIBackend, _ string, _ int) (string, error) {
		return normalized, nil
	}
	t.Cleanup(func() { agentcli.InvokeFunc = original })

	eng := engine.New(runDir, dir)
	if err := eng.DraftTechnicalSpec(context.Background(), sourcePath, false); err != nil {
		t.Fatalf("DraftTechnicalSpec: %v", err)
	}

	data, err := runDir.ReadTechnicalSpec()
	if err != nil {
		t.Fatalf("ReadTechnicalSpec: %v", err)
	}
	if !strings.Contains(string(data), "## 1. Technical context") {
		t.Fatalf("expected normalized output:\n%s", string(data))
	}
}

func TestApproveTechnicalSpecRejectsArtifactChangedAfterReview(t *testing.T) {
	dir := t.TempDir()
	runDir := state.NewRunDir(dir, "run-tech-approve")
	if err := runDir.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	original := []byte("# Spec\n\n## 1. Technical context\nA\n\n## 2. Architecture\nB\n\n## 3. Canonical artifacts and schemas\nC\n\n## 4. Interfaces\nD\n\n## 5. Verification\nE\n\n## 6. Requirement traceability\nF\n\n## 7. Open questions and risks\nG\n\n## 8. Approval\nH\n")
	if err := runDir.WriteTechnicalSpec(original); err != nil {
		t.Fatalf("WriteTechnicalSpec: %v", err)
	}
	if err := runDir.WriteTechnicalSpecReview(&state.TechnicalSpecReview{
		SchemaVersion:     "0.1",
		RunID:             "run-tech-approve",
		ArtifactType:      "technical_spec_review",
		TechnicalSpecHash: "sha256:" + state.SHA256Bytes(original),
		Status:            state.ReviewPass,
		ReviewedAt:        time.Now().UTC(),
	}); err != nil {
		t.Fatalf("WriteTechnicalSpecReview: %v", err)
	}
	if err := runDir.WriteTechnicalSpec(append(original, []byte("\nmutated\n")...)); err != nil {
		t.Fatalf("WriteTechnicalSpec(mutated): %v", err)
	}

	eng := engine.New(runDir, dir)
	_, err := eng.ApproveTechnicalSpec(context.Background(), "tester")
	if err == nil {
		t.Fatal("ApproveTechnicalSpec() error = nil, want stale review hash error")
	}
	if !strings.Contains(err.Error(), "review hash") {
		t.Fatalf("ApproveTechnicalSpec() error = %q, want review hash context", err)
	}
}

func TestApproveExecutionPlanRejectsArtifactChangedAfterReview(t *testing.T) {
	dir := t.TempDir()
	runDir := state.NewRunDir(dir, "run-plan-approve")
	if err := runDir.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	plan := &state.ExecutionPlan{
		SchemaVersion:           "0.1",
		RunID:                   "run-plan-approve",
		ArtifactType:            "execution_plan",
		SourceTechnicalSpecHash: "sha256:technical-spec",
		Status:                  state.ArtifactDrafted,
		Slices: []state.ExecutionSlice{{
			SliceID:          "slice-001",
			Title:            "Slice",
			Goal:             "Goal",
			RequirementIDs:   []string{"AT-FR-001"},
			OwnedPaths:       []string{"internal/state"},
			AcceptanceChecks: []string{"go test ./internal/state"},
			Risk:             "low",
			Size:             "small",
		}},
		GeneratedAt: time.Now().UTC(),
	}
	if err := runDir.WriteExecutionPlan(plan); err != nil {
		t.Fatalf("WriteExecutionPlan: %v", err)
	}
	planData, err := os.ReadFile(runDir.ExecutionPlan())
	if err != nil {
		t.Fatalf("ReadFile(execution plan): %v", err)
	}
	if err := runDir.WriteExecutionPlanReview(&state.ExecutionPlanReview{
		SchemaVersion:     "0.1",
		RunID:             "run-plan-approve",
		ArtifactType:      "execution_plan_review",
		ExecutionPlanHash: "sha256:" + state.SHA256Bytes(planData),
		Status:            state.ReviewPass,
		ReviewedAt:        time.Now().UTC(),
	}); err != nil {
		t.Fatalf("WriteExecutionPlanReview: %v", err)
	}
	plan.Slices[0].Notes = "mutated"
	if err := runDir.WriteExecutionPlan(plan); err != nil {
		t.Fatalf("WriteExecutionPlan(mutated): %v", err)
	}

	eng := engine.New(runDir, dir)
	_, err = eng.ApproveExecutionPlan(context.Background(), "tester")
	if err == nil {
		t.Fatal("ApproveExecutionPlan() error = nil, want stale review hash error")
	}
	if !strings.Contains(err.Error(), "review hash") {
		t.Fatalf("ApproveExecutionPlan() error = %q, want review hash context", err)
	}
}

func TestReviewExecutionPlanReviewerSummaryTracksFailure(t *testing.T) {
	dir := t.TempDir()
	runDir := state.NewRunDir(dir, "run-plan-review")
	if err := runDir.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := runDir.WriteExecutionPlan(&state.ExecutionPlan{
		SchemaVersion:           "0.1",
		RunID:                   "run-plan-review",
		ArtifactType:            "execution_plan",
		SourceTechnicalSpecHash: "sha256:technical-spec",
		Status:                  state.ArtifactDrafted,
		Slices: []state.ExecutionSlice{
			{
				SliceID:          "slice-001",
				Title:            "Valid",
				Goal:             "Goal",
				RequirementIDs:   []string{"AT-FR-001"},
				OwnedPaths:       []string{"internal/state"},
				AcceptanceChecks: []string{"go test ./internal/state"},
				Risk:             "low",
				Size:             "small",
			},
			{
				SliceID:        "slice-002",
				RequirementIDs: []string{"AT-FR-002"},
				OwnedPaths:     []string{"internal/engine"},
				Risk:           "low",
				Size:           "small",
			},
		},
		GeneratedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("WriteExecutionPlan: %v", err)
	}

	eng := engine.New(runDir, dir)
	review, err := eng.ReviewExecutionPlan(context.Background())
	if err != nil {
		t.Fatalf("ReviewExecutionPlan: %v", err)
	}
	if review.Status != state.ReviewFail {
		t.Fatalf("review status = %s, want %s", review.Status, state.ReviewFail)
	}
	if len(review.Reviewers) != 1 || review.Reviewers[0].Pass {
		t.Fatalf("review reviewers = %+v, want single failing reviewer summary", review.Reviewers)
	}
}

func TestVerifyTaskWritesVerifierResult(t *testing.T) {
	dir := t.TempDir()
	runDir := state.NewRunDir(dir, "run-verify")
	if err := runDir.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := runDir.WriteArtifact(&state.RunArtifact{
		SchemaVersion: "0.1",
		RunID:         "run-verify",
	}); err != nil {
		t.Fatalf("WriteArtifact: %v", err)
	}

	task := &state.Task{
		TaskID:           "task-verify",
		RequirementIDs:   []string{"AT-FR-001"},
		RequiredEvidence: []string{"quality_gate_pass"},
	}
	report := &state.CompletionReport{
		TaskID:    task.TaskID,
		AttemptID: "attempt-verify",
	}

	eng := engine.New(runDir, dir)
	result, err := eng.VerifyTask(context.Background(), task, report)
	if err != nil {
		t.Fatalf("VerifyTask: %v", err)
	}
	if !result.Pass {
		t.Fatalf("VerifyTask pass = false, findings = %+v", result.BlockingFindings)
	}

	var persisted state.VerifierResult
	resultPath := filepath.Join(runDir.ReportDir(task.TaskID, report.AttemptID), "verifier-result.json")
	if err := state.ReadJSON(resultPath, &persisted); err != nil {
		t.Fatalf("ReadJSON(verifier-result): %v", err)
	}
	if persisted.TaskID != task.TaskID || persisted.AttemptID != report.AttemptID {
		t.Fatalf("persisted verifier result = %+v, want task %q attempt %q", persisted, task.TaskID, report.AttemptID)
	}

	status, err := runDir.ReadStatus()
	if err != nil {
		t.Fatalf("ReadStatus after VerifyTask: %v", err)
	}
	if status.State != state.RunCompleted {
		t.Fatalf("status state after successful VerifyTask = %s, want %s", status.State, state.RunCompleted)
	}

	tasks, err := runDir.ReadTasks()
	if err != nil {
		t.Fatalf("ReadTasks after VerifyTask: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Status != state.TaskDone {
		t.Fatalf("task status after successful VerifyTask = %+v, want done", tasks)
	}
}

func TestVerifyTaskSatisfiesCoverageAndCompletesRun(t *testing.T) {
	dir := t.TempDir()
	runDir := state.NewRunDir(dir, "run-complete")
	if err := runDir.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := runDir.WriteArtifact(&state.RunArtifact{
		SchemaVersion: "0.1",
		RunID:         "run-complete",
	}); err != nil {
		t.Fatalf("WriteArtifact: %v", err)
	}
	if err := runDir.WriteTasks([]state.Task{{
		TaskID:           "task-complete",
		Status:           state.TaskPending,
		RequirementIDs:   []string{"AT-FR-001"},
		RequiredEvidence: []string{"quality_gate_pass"},
	}}); err != nil {
		t.Fatalf("WriteTasks: %v", err)
	}
	if err := runDir.WriteCoverage([]state.RequirementCoverage{{
		RequirementID:   "AT-FR-001",
		Status:          "in_progress",
		CoveringTaskIDs: []string{"task-complete"},
	}}); err != nil {
		t.Fatalf("WriteCoverage: %v", err)
	}
	if err := runDir.WriteStatus(&state.RunStatus{
		RunID:              "run-complete",
		State:              state.RunRunning,
		LastTransitionTime: time.Now(),
		TaskCountsByState:  map[string]int{"pending": 1},
	}); err != nil {
		t.Fatalf("WriteStatus: %v", err)
	}

	task := &state.Task{
		TaskID:           "task-complete",
		RequirementIDs:   []string{"AT-FR-001"},
		RequiredEvidence: []string{"quality_gate_pass"},
	}
	report := &state.CompletionReport{
		TaskID:    task.TaskID,
		AttemptID: "attempt-complete",
	}

	eng := engine.New(runDir, dir)
	result, err := eng.VerifyTask(context.Background(), task, report)
	if err != nil {
		t.Fatalf("VerifyTask: %v", err)
	}
	if !result.Pass {
		t.Fatalf("VerifyTask pass = false, findings = %+v", result.BlockingFindings)
	}

	coverage, err := runDir.ReadCoverage()
	if err != nil {
		t.Fatalf("ReadCoverage after VerifyTask: %v", err)
	}
	if len(coverage) != 1 || coverage[0].Status != "satisfied" {
		t.Fatalf("coverage after successful VerifyTask = %+v, want satisfied", coverage)
	}

	status, err := runDir.ReadStatus()
	if err != nil {
		t.Fatalf("ReadStatus after VerifyTask: %v", err)
	}
	if status.State != state.RunCompleted {
		t.Fatalf("status state after successful VerifyTask = %s, want %s", status.State, state.RunCompleted)
	}
	if status.CurrentGate != "completed" {
		t.Fatalf("current gate after successful VerifyTask = %q, want completed", status.CurrentGate)
	}
}

func TestUpdateTaskStatusReturnsErrorWhenTaskMissing(t *testing.T) {
	dir := t.TempDir()
	runDir := state.NewRunDir(dir, "run-update-missing")
	if err := runDir.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := runDir.WriteTasks([]state.Task{{TaskID: "task-1"}}); err != nil {
		t.Fatalf("WriteTasks: %v", err)
	}

	eng := engine.New(runDir, dir)
	err := eng.UpdateTaskStatus("task-missing", state.TaskDone, "verified")
	if err == nil {
		t.Fatal("UpdateTaskStatus() error = nil, want not found")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("UpdateTaskStatus() error = %q, want not found", err)
	}
}

func TestGetPendingTasksRespectsDependencies(t *testing.T) {
	dir := t.TempDir()
	runDir := state.NewRunDir(dir, "run-pending")
	if err := runDir.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := runDir.WriteTasks([]state.Task{
		{TaskID: "task-ready", Status: state.TaskPending},
		{TaskID: "task-done", Status: state.TaskDone},
		{TaskID: "task-blocked", Status: state.TaskPending, DependsOn: []string{"task-done", "task-missing"}},
		{TaskID: "task-waiting", Status: state.TaskPending, DependsOn: []string{"task-done"}},
		{TaskID: "task-failed", Status: state.TaskFailed},
	}); err != nil {
		t.Fatalf("WriteTasks: %v", err)
	}

	eng := engine.New(runDir, dir)
	pending, err := eng.GetPendingTasks()
	if err != nil {
		t.Fatalf("GetPendingTasks: %v", err)
	}

	if len(pending) != 2 {
		t.Fatalf("len(pending) = %d, want 2", len(pending))
	}
	if pending[0].TaskID != "task-ready" || pending[1].TaskID != "task-waiting" {
		t.Fatalf("pending tasks = %+v, want ready/waiting only", pending)
	}
}

func TestVerifyTaskFailureBlocksRun(t *testing.T) {
	dir := t.TempDir()
	runDir := state.NewRunDir(dir, "run-blocked")
	if err := runDir.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := runDir.WriteArtifact(&state.RunArtifact{
		SchemaVersion: "0.1",
		RunID:         "run-blocked",
	}); err != nil {
		t.Fatalf("WriteArtifact: %v", err)
	}
	if err := runDir.WriteTasks([]state.Task{{
		TaskID:           "task-blocked",
		Status:           state.TaskPending,
		RequirementIDs:   []string{"AT-FR-001"},
		RequiredEvidence: []string{"file_exists"},
	}}); err != nil {
		t.Fatalf("WriteTasks: %v", err)
	}
	if err := runDir.WriteCoverage([]state.RequirementCoverage{{
		RequirementID:   "AT-FR-001",
		Status:          "in_progress",
		CoveringTaskIDs: []string{"task-blocked"},
	}}); err != nil {
		t.Fatalf("WriteCoverage: %v", err)
	}
	if err := runDir.WriteStatus(&state.RunStatus{
		RunID:              "run-blocked",
		State:              state.RunRunning,
		LastTransitionTime: time.Now(),
		TaskCountsByState:  map[string]int{},
	}); err != nil {
		t.Fatalf("WriteStatus: %v", err)
	}

	task := &state.Task{
		TaskID:           "task-blocked",
		RequirementIDs:   []string{"AT-FR-001"},
		RequiredEvidence: []string{"file_exists"},
	}
	report := &state.CompletionReport{
		TaskID:    task.TaskID,
		AttemptID: "attempt-blocked",
	}

	eng := engine.New(runDir, dir)
	result, err := eng.VerifyTask(context.Background(), task, report)
	if err != nil {
		t.Fatalf("VerifyTask: %v", err)
	}
	if result.Pass {
		t.Fatal("VerifyTask unexpectedly passed")
	}

	status, err := runDir.ReadStatus()
	if err != nil {
		t.Fatalf("ReadStatus after failed VerifyTask: %v", err)
	}
	if status.State != state.RunBlocked {
		t.Fatalf("status state after failed VerifyTask = %s, want %s", status.State, state.RunBlocked)
	}
	if len(status.OpenBlockers) == 0 {
		t.Fatal("OpenBlockers after failed VerifyTask = empty, want blocker detail")
	}

	tasks, err := runDir.ReadTasks()
	if err != nil {
		t.Fatalf("ReadTasks after failed VerifyTask: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Status != state.TaskBlocked {
		t.Fatalf("task status after failed VerifyTask = %+v, want blocked", tasks)
	}

	coverage, err := runDir.ReadCoverage()
	if err != nil {
		t.Fatalf("ReadCoverage after failed VerifyTask: %v", err)
	}
	if len(coverage) != 1 || coverage[0].Status != "blocked" {
		t.Fatalf("coverage after failed VerifyTask = %+v, want blocked", coverage)
	}
}

func TestVerifyTaskSurfacesNeverBoundariesAsWarnings(t *testing.T) {
	dir := t.TempDir()
	runDir := state.NewRunDir(dir, "run-never-warning")
	if err := runDir.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := runDir.WriteArtifact(&state.RunArtifact{
		SchemaVersion: "0.1",
		RunID:         "run-never-warning",
		Boundaries: state.Boundaries{
			Never: []string{" internal/engine/errors.go "},
		},
	}); err != nil {
		t.Fatalf("WriteArtifact: %v", err)
	}
	if err := runDir.WriteTasks([]state.Task{{
		TaskID:           "task-warning",
		Status:           state.TaskPending,
		RequirementIDs:   []string{"AT-FR-001"},
		RequiredEvidence: []string{"file_exists"},
		Scope:            state.TaskScope{OwnedPaths: []string{"internal/engine"}},
	}}); err != nil {
		t.Fatalf("WriteTasks: %v", err)
	}
	if err := runDir.WriteCoverage([]state.RequirementCoverage{{
		RequirementID:   "AT-FR-001",
		Status:          "in_progress",
		CoveringTaskIDs: []string{"task-warning"},
	}}); err != nil {
		t.Fatalf("WriteCoverage: %v", err)
	}
	if err := runDir.WriteStatus(&state.RunStatus{
		RunID:              "run-never-warning",
		State:              state.RunRunning,
		LastTransitionTime: time.Now(),
		TaskCountsByState:  map[string]int{"pending": 1},
	}); err != nil {
		t.Fatalf("WriteStatus: %v", err)
	}

	task := &state.Task{
		TaskID:           "task-warning",
		RequirementIDs:   []string{"AT-FR-001"},
		RequiredEvidence: []string{"file_exists"},
		Scope:            state.TaskScope{OwnedPaths: []string{"internal/engine"}},
	}
	report := &state.CompletionReport{
		TaskID:       task.TaskID,
		AttemptID:    "attempt-warning",
		ChangedFiles: []string{filepath.Join(dir, "internal", "engine", "errors.go")},
	}

	eng := engine.New(runDir, dir)
	result, err := eng.VerifyTask(context.Background(), task, report)
	if err != nil {
		t.Fatalf("VerifyTask: %v", err)
	}
	if !result.Pass {
		t.Fatalf("VerifyTask pass = false, findings = %+v", result.BlockingFindings)
	}
	if len(result.BlockingFindings) != 0 {
		t.Fatalf("BlockingFindings = %+v, want none", result.BlockingFindings)
	}
	if len(result.NonBlockingFindings) != 1 {
		t.Fatalf("NonBlockingFindings len = %d, want 1", len(result.NonBlockingFindings))
	}
	if got := result.NonBlockingFindings[0]; got.Category != "boundary_never" || !strings.Contains(got.Summary, "internal/engine/errors.go") {
		t.Fatalf("NonBlockingFindings[0] = %+v, want boundary_never warning mentioning path", got)
	}

	status, err := runDir.ReadStatus()
	if err != nil {
		t.Fatalf("ReadStatus after VerifyTask: %v", err)
	}
	if status.State != state.RunCompleted {
		t.Fatalf("status state after warning-only VerifyTask = %s, want %s", status.State, state.RunCompleted)
	}

	var persisted state.VerifierResult
	resultPath := filepath.Join(runDir.ReportDir(task.TaskID, report.AttemptID), "verifier-result.json")
	if err := state.ReadJSON(resultPath, &persisted); err != nil {
		t.Fatalf("ReadJSON(verifier-result): %v", err)
	}
	if len(persisted.NonBlockingFindings) != 1 {
		t.Fatalf("persisted NonBlockingFindings len = %d, want 1", len(persisted.NonBlockingFindings))
	}
}

func TestRetryTaskRequeuesBlockedTaskAndRestoresCoverage(t *testing.T) {
	dir := t.TempDir()
	runDir := state.NewRunDir(dir, "run-retry")
	if err := runDir.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := runDir.WriteTasks([]state.Task{{
		TaskID:         "task-retry",
		Status:         state.TaskBlocked,
		StatusReason:   "missing evidence",
		RequirementIDs: []string{"AT-FR-001"},
	}}); err != nil {
		t.Fatalf("WriteTasks: %v", err)
	}
	if err := runDir.WriteCoverage([]state.RequirementCoverage{{
		RequirementID:   "AT-FR-001",
		Status:          "blocked",
		CoveringTaskIDs: []string{"task-retry"},
	}}); err != nil {
		t.Fatalf("WriteCoverage: %v", err)
	}
	if err := runDir.WriteStatus(&state.RunStatus{
		RunID:              "run-retry",
		State:              state.RunBlocked,
		LastTransitionTime: time.Now(),
		TaskCountsByState:  map[string]int{"blocked": 1},
		OpenBlockers:       []string{"task-retry: missing evidence"},
	}); err != nil {
		t.Fatalf("WriteStatus: %v", err)
	}

	eng := engine.New(runDir, dir)
	if err := eng.RetryTask("task-retry"); err != nil {
		t.Fatalf("RetryTask: %v", err)
	}

	tasks, err := runDir.ReadTasks()
	if err != nil {
		t.Fatalf("ReadTasks after RetryTask: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Status != state.TaskPending || tasks[0].StatusReason != "" {
		t.Fatalf("task after RetryTask = %+v, want pending with cleared reason", tasks)
	}

	coverage, err := runDir.ReadCoverage()
	if err != nil {
		t.Fatalf("ReadCoverage after RetryTask: %v", err)
	}
	if len(coverage) != 1 || coverage[0].Status != "in_progress" {
		t.Fatalf("coverage after RetryTask = %+v, want in_progress", coverage)
	}

	status, err := runDir.ReadStatus()
	if err != nil {
		t.Fatalf("ReadStatus after RetryTask: %v", err)
	}
	if status.State != state.RunRunning {
		t.Fatalf("status state after RetryTask = %s, want %s", status.State, state.RunRunning)
	}
	if len(status.OpenBlockers) != 0 {
		t.Fatalf("OpenBlockers after RetryTask = %+v, want empty", status.OpenBlockers)
	}
}

func TestReviewExecutionPlanRejectsDuplicateSliceIDs(t *testing.T) {
	dir := t.TempDir()
	runDir := state.NewRunDir(dir, "run-dup-ids")
	if err := runDir.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := runDir.WriteExecutionPlan(&state.ExecutionPlan{
		SchemaVersion:           "0.1",
		RunID:                   "run-dup-ids",
		ArtifactType:            "execution_plan",
		SourceTechnicalSpecHash: "sha256:test",
		Status:                  state.ArtifactDrafted,
		Slices: []state.ExecutionSlice{
			{SliceID: "slice-001", Title: "A", Goal: "G", RequirementIDs: []string{"AT-FR-001"}, OwnedPaths: []string{"a"}, AcceptanceChecks: []string{"check"}, Risk: "low", Size: "small"},
			{SliceID: "slice-001", Title: "B", Goal: "G", RequirementIDs: []string{"AT-FR-002"}, OwnedPaths: []string{"b"}, AcceptanceChecks: []string{"check"}, Risk: "low", Size: "small"},
		},
		GeneratedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("WriteExecutionPlan: %v", err)
	}

	eng := engine.New(runDir, dir)
	review, err := eng.ReviewExecutionPlan(context.Background())
	if err != nil {
		t.Fatalf("ReviewExecutionPlan: %v", err)
	}
	if review.Status != state.ReviewFail {
		t.Fatal("review should fail for duplicate slice IDs")
	}
	found := false
	for _, f := range review.BlockingFindings {
		if f.Category == "duplicate_slice_id" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected duplicate_slice_id finding, got: %+v", review.BlockingFindings)
	}
}

func TestReviewExecutionPlanRejectsSelfDependency(t *testing.T) {
	dir := t.TempDir()
	runDir := state.NewRunDir(dir, "run-self-dep")
	if err := runDir.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := runDir.WriteExecutionPlan(&state.ExecutionPlan{
		SchemaVersion:           "0.1",
		RunID:                   "run-self-dep",
		ArtifactType:            "execution_plan",
		SourceTechnicalSpecHash: "sha256:test",
		Status:                  state.ArtifactDrafted,
		Slices: []state.ExecutionSlice{
			{SliceID: "slice-001", Title: "A", Goal: "G", RequirementIDs: []string{"AT-FR-001"}, DependsOn: []string{"slice-001"}, OwnedPaths: []string{"a"}, AcceptanceChecks: []string{"check"}, Risk: "low", Size: "small"},
		},
		GeneratedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("WriteExecutionPlan: %v", err)
	}

	eng := engine.New(runDir, dir)
	review, err := eng.ReviewExecutionPlan(context.Background())
	if err != nil {
		t.Fatalf("ReviewExecutionPlan: %v", err)
	}
	if review.Status != state.ReviewFail {
		t.Fatal("review should fail for self-dependency")
	}
	found := false
	for _, f := range review.BlockingFindings {
		if f.Category == "self_dependency" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected self_dependency finding, got: %+v", review.BlockingFindings)
	}
}

func TestReviewExecutionPlanRejectsUnknownDependency(t *testing.T) {
	dir := t.TempDir()
	runDir := state.NewRunDir(dir, "run-unknown-dep")
	if err := runDir.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := runDir.WriteExecutionPlan(&state.ExecutionPlan{
		SchemaVersion:           "0.1",
		RunID:                   "run-unknown-dep",
		ArtifactType:            "execution_plan",
		SourceTechnicalSpecHash: "sha256:test",
		Status:                  state.ArtifactDrafted,
		Slices: []state.ExecutionSlice{
			{SliceID: "slice-001", Title: "A", Goal: "G", RequirementIDs: []string{"AT-FR-001"}, DependsOn: []string{"nonexistent"}, OwnedPaths: []string{"a"}, AcceptanceChecks: []string{"check"}, Risk: "low", Size: "small"},
		},
		GeneratedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("WriteExecutionPlan: %v", err)
	}

	eng := engine.New(runDir, dir)
	review, err := eng.ReviewExecutionPlan(context.Background())
	if err != nil {
		t.Fatalf("ReviewExecutionPlan: %v", err)
	}
	if review.Status != state.ReviewFail {
		t.Fatal("review should fail for unknown dependency")
	}
	found := false
	for _, f := range review.BlockingFindings {
		if f.Category == "unknown_dependency" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected unknown_dependency finding, got: %+v", review.BlockingFindings)
	}
}

func TestReviewExecutionPlanRejectsCycle(t *testing.T) {
	dir := t.TempDir()
	runDir := state.NewRunDir(dir, "run-cycle")
	if err := runDir.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := runDir.WriteExecutionPlan(&state.ExecutionPlan{
		SchemaVersion:           "0.1",
		RunID:                   "run-cycle",
		ArtifactType:            "execution_plan",
		SourceTechnicalSpecHash: "sha256:test",
		Status:                  state.ArtifactDrafted,
		Slices: []state.ExecutionSlice{
			{SliceID: "slice-a", Title: "A", Goal: "G", RequirementIDs: []string{"AT-FR-001"}, DependsOn: []string{"slice-b"}, OwnedPaths: []string{"a"}, AcceptanceChecks: []string{"check"}, Risk: "low", Size: "small"},
			{SliceID: "slice-b", Title: "B", Goal: "G", RequirementIDs: []string{"AT-FR-002"}, DependsOn: []string{"slice-a"}, OwnedPaths: []string{"b"}, AcceptanceChecks: []string{"check"}, Risk: "low", Size: "small"},
		},
		GeneratedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("WriteExecutionPlan: %v", err)
	}

	eng := engine.New(runDir, dir)
	review, err := eng.ReviewExecutionPlan(context.Background())
	if err != nil {
		t.Fatalf("ReviewExecutionPlan: %v", err)
	}
	if review.Status != state.ReviewFail {
		t.Fatal("review should fail for dependency cycle")
	}
	found := false
	for _, f := range review.BlockingFindings {
		if f.Category == "dependency_cycle" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected dependency_cycle finding, got: %+v", review.BlockingFindings)
	}
}

func TestReviewExecutionPlanRejectsCaseCollidingIDs(t *testing.T) {
	dir := t.TempDir()
	runDir := state.NewRunDir(dir, "run-case-collide")
	if err := runDir.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := runDir.WriteExecutionPlan(&state.ExecutionPlan{
		SchemaVersion:           "0.1",
		RunID:                   "run-case-collide",
		ArtifactType:            "execution_plan",
		SourceTechnicalSpecHash: "sha256:test",
		Status:                  state.ArtifactDrafted,
		Slices: []state.ExecutionSlice{
			{SliceID: "Slice-001", Title: "A", Goal: "G", RequirementIDs: []string{"AT-FR-001"}, OwnedPaths: []string{"a"}, AcceptanceChecks: []string{"check"}, Risk: "low", Size: "small"},
			{SliceID: "slice-001", Title: "B", Goal: "G", RequirementIDs: []string{"AT-FR-002"}, OwnedPaths: []string{"b"}, AcceptanceChecks: []string{"check"}, Risk: "low", Size: "small"},
		},
		GeneratedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("WriteExecutionPlan: %v", err)
	}

	eng := engine.New(runDir, dir)
	review, err := eng.ReviewExecutionPlan(context.Background())
	if err != nil {
		t.Fatalf("ReviewExecutionPlan: %v", err)
	}
	if review.Status != state.ReviewFail {
		t.Fatal("review should fail for case-colliding slice IDs")
	}
	found := false
	for _, f := range review.BlockingFindings {
		if f.Category == "case_colliding_slice_id" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected case_colliding_slice_id finding, got: %+v", review.BlockingFindings)
	}
}

func TestReviewExecutionPlanRejectsIntraWaveFileConflict(t *testing.T) {
	dir := t.TempDir()
	runDir := state.NewRunDir(dir, "run-same-wave-conflict")
	if err := runDir.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := runDir.WriteExecutionPlan(&state.ExecutionPlan{
		SchemaVersion:           "0.1",
		RunID:                   "run-same-wave-conflict",
		ArtifactType:            "execution_plan",
		SourceTechnicalSpecHash: "sha256:test",
		Status:                  state.ArtifactDrafted,
		Slices: []state.ExecutionSlice{
			{SliceID: "slice-001", Title: "A", Goal: "G", RequirementIDs: []string{"AT-FR-001"}, WaveID: "wave-0", FilesLikelyTouched: []string{"internal/engine/plan.go"}, OwnedPaths: []string{"internal/alpha"}, AcceptanceChecks: []string{"check"}, ValidationChecks: []state.ValidationCheck{{Type: "files_exist", Paths: []string{"internal/engine/plan.go"}}}, Risk: "low", Size: "small"},
			{SliceID: "slice-002", Title: "B", Goal: "G", RequirementIDs: []string{"AT-FR-002"}, WaveID: "wave-0", FilesLikelyTouched: []string{"internal/engine/plan.go"}, OwnedPaths: []string{"internal/beta"}, AcceptanceChecks: []string{"check"}, ValidationChecks: []state.ValidationCheck{{Type: "files_exist", Paths: []string{"internal/engine/plan.go"}}}, Risk: "low", Size: "small"},
		},
		GeneratedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("WriteExecutionPlan: %v", err)
	}

	eng := engine.New(runDir, dir)
	review, err := eng.ReviewExecutionPlan(context.Background())
	if err != nil {
		t.Fatalf("ReviewExecutionPlan: %v", err)
	}
	if review.Status != state.ReviewFail {
		t.Fatal("review should fail for same-wave file conflicts")
	}
	found := false
	for _, f := range review.BlockingFindings {
		if f.Category == "intra_wave_file_conflict" {
			found = true
			if !strings.Contains(f.Summary, "internal/engine/plan.go") {
				t.Fatalf("expected conflict summary to include canonical conflict path, got %q", f.Summary)
			}
			break
		}
	}
	if !found {
		t.Fatalf("expected intra_wave_file_conflict finding, got: %+v", review.BlockingFindings)
	}
}

func TestReviewExecutionPlanRejectsIntraWaveDependency(t *testing.T) {
	dir := t.TempDir()
	runDir := state.NewRunDir(dir, "run-same-wave-dep")
	if err := runDir.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := runDir.WriteExecutionPlan(&state.ExecutionPlan{
		SchemaVersion:           "0.1",
		RunID:                   "run-same-wave-dep",
		ArtifactType:            "execution_plan",
		SourceTechnicalSpecHash: "sha256:test",
		Status:                  state.ArtifactDrafted,
		Slices: []state.ExecutionSlice{
			{SliceID: "slice-001", Title: "A", Goal: "G", RequirementIDs: []string{"AT-FR-001"}, WaveID: "wave-0", OwnedPaths: []string{"a"}, AcceptanceChecks: []string{"check"}, ValidationChecks: []state.ValidationCheck{{Type: "files_exist", Paths: []string{"a"}}}, Risk: "low", Size: "small"},
			{SliceID: "slice-002", Title: "B", Goal: "G", RequirementIDs: []string{"AT-FR-002"}, DependsOn: []string{"slice-001"}, WaveID: "wave-0", OwnedPaths: []string{"b"}, AcceptanceChecks: []string{"check"}, ValidationChecks: []state.ValidationCheck{{Type: "files_exist", Paths: []string{"b"}}}, Risk: "low", Size: "small"},
		},
		GeneratedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("WriteExecutionPlan: %v", err)
	}

	eng := engine.New(runDir, dir)
	review, err := eng.ReviewExecutionPlan(context.Background())
	if err != nil {
		t.Fatalf("ReviewExecutionPlan: %v", err)
	}
	if review.Status != state.ReviewFail {
		t.Fatal("review should fail for same-wave dependencies")
	}
	found := false
	for _, f := range review.BlockingFindings {
		if f.Category == "intra_wave_dependency" {
			found = true
			if !strings.Contains(f.Summary, "wave-0") {
				t.Fatalf("expected dependency summary to include wave id, got %q", f.Summary)
			}
			break
		}
	}
	if !found {
		t.Fatalf("expected intra_wave_dependency finding, got: %+v", review.BlockingFindings)
	}
}

func TestReviewExecutionPlanBuildsSharedFileRegistryAndWarnsOnMissingValidationChecks(t *testing.T) {
	dir := t.TempDir()
	runDir := state.NewRunDir(dir, "run-shared-files")
	if err := runDir.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := runDir.WriteArtifact(&state.RunArtifact{
		SchemaVersion: "0.1",
		RunID:         "run-shared-files",
		Requirements: []state.Requirement{
			{ID: "AT-FR-001", Text: "Requirement 1"},
			{ID: "AT-FR-002", Text: "Requirement 2"},
		},
	}); err != nil {
		t.Fatalf("WriteArtifact: %v", err)
	}
	if err := runDir.WriteExecutionPlan(&state.ExecutionPlan{
		SchemaVersion:           "0.1",
		RunID:                   "run-shared-files",
		ArtifactType:            "execution_plan",
		SourceTechnicalSpecHash: "sha256:test",
		Status:                  state.ArtifactDrafted,
		Slices: []state.ExecutionSlice{
			{SliceID: "slice-001", Title: "A", Goal: "G", RequirementIDs: []string{"AT-FR-001"}, WaveID: "wave-0", FilesLikelyTouched: []string{"internal/engine/plan.go"}, OwnedPaths: []string{"internal/alpha"}, AcceptanceChecks: []string{"check"}, ValidationChecks: []state.ValidationCheck{{Type: "files_exist", Paths: []string{"internal/engine/plan.go"}}}, Risk: "low", Size: "small"},
			{SliceID: "slice-002", Title: "B", Goal: "G", RequirementIDs: []string{"AT-FR-002"}, WaveID: "wave-1", FilesLikelyTouched: []string{"internal/engine/plan.go"}, OwnedPaths: []string{"internal/beta"}, AcceptanceChecks: []string{"check"}, Risk: "low", Size: "small"},
		},
		GeneratedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("WriteExecutionPlan: %v", err)
	}

	eng := engine.New(runDir, dir)
	review, err := eng.ReviewExecutionPlan(context.Background())
	if err != nil {
		t.Fatalf("ReviewExecutionPlan: %v", err)
	}
	if review.Status != state.ReviewPass {
		t.Fatalf("review should pass when only warnings remain, got %s with findings %+v", review.Status, review.BlockingFindings)
	}
	if len(review.SharedFileRegistry) != 1 {
		t.Fatalf("SharedFileRegistry len = %d, want 1", len(review.SharedFileRegistry))
	}
	entry := review.SharedFileRegistry[0]
	if entry.File != "internal/engine/plan.go" {
		t.Fatalf("SharedFileRegistry[0].File = %q, want internal/engine/plan.go", entry.File)
	}
	if !reflect.DeepEqual([]string{"slice-001"}, entry.Wave1Tasks) {
		t.Fatalf("Wave1Tasks = %v, want [slice-001]", entry.Wave1Tasks)
	}
	if !reflect.DeepEqual([]string{"slice-002"}, entry.Wave2Tasks) {
		t.Fatalf("Wave2Tasks = %v, want [slice-002]", entry.Wave2Tasks)
	}
	if entry.Mitigation == "" {
		t.Fatal("expected shared file mitigation guidance")
	}
	warningFound := false
	for _, w := range review.Warnings {
		if w.SliceID == "slice-002" && strings.Contains(w.Summary, "no validation checks") {
			warningFound = true
			break
		}
	}
	if !warningFound {
		t.Fatalf("expected missing validation warning for slice-002, got: %+v", review.Warnings)
	}

	stored, err := runDir.ReadExecutionPlanReview()
	if err != nil {
		t.Fatalf("ReadExecutionPlanReview: %v", err)
	}
	if !reflect.DeepEqual(review.SharedFileRegistry, stored.SharedFileRegistry) {
		t.Fatalf("stored SharedFileRegistry = %+v, want %+v", stored.SharedFileRegistry, review.SharedFileRegistry)
	}
}

func TestCompileRejectsStaleExecutionPlan(t *testing.T) {
	dir := t.TempDir()
	specPath := writeTestSpec(t, dir)
	ctx := context.Background()

	runDir := state.NewRunDir(dir, "placeholder")
	eng := engine.New(runDir, dir)

	artifact, err := eng.Prepare(ctx, []string{specPath})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	// Write execution plan with one tech spec hash.
	writeApprovedExecutionPlanForArtifact(t, eng.RunDir, artifact)

	if err := eng.Approve(ctx); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	// Now re-write the tech spec with different content, simulating a re-approval.
	newSpec := []byte("# Changed Spec\n\n## 1. Technical context\nX\n\n## 2. Architecture\nY\n\n## 3. Canonical artifacts and schemas\nZ\n\n## 4. Interfaces\nW\n\n## 5. Verification\nV\n\n## 6. Requirement traceability\nU\n\n## 7. Open questions and risks\nT\n\n## 8. Approval\nS\n")
	newHash := "sha256:" + state.SHA256Bytes(newSpec)
	if err := eng.RunDir.WriteTechnicalSpec(newSpec); err != nil {
		t.Fatalf("WriteTechnicalSpec: %v", err)
	}
	if err := eng.RunDir.WriteTechnicalSpecApproval(&state.ArtifactApproval{
		SchemaVersion: "0.1",
		RunID:         artifact.RunID,
		ArtifactType:  "technical_spec_approval",
		ArtifactPath:  "technical-spec.md",
		ArtifactHash:  newHash,
		Status:        state.ArtifactApproved,
		ApprovedBy:    "tester",
		ApprovedAt:    time.Now().UTC(),
	}); err != nil {
		t.Fatalf("WriteTechnicalSpecApproval: %v", err)
	}

	// Compile should reject because the execution plan references the old spec hash.
	_, err = eng.Compile(ctx)
	if err == nil {
		t.Fatal("Compile() error = nil, want stale tech spec lineage error")
	}
	if !strings.Contains(err.Error(), "re-draft the execution plan") {
		t.Fatalf("Compile() error = %q, want re-draft context", err)
	}
}

func TestCompileWritesToTicketStore(t *testing.T) {
	dir := t.TempDir()
	specPath := writeTestSpec(t, dir)
	ctx := context.Background()

	runDir := state.NewRunDir(dir, "placeholder")
	eng := engine.New(runDir, dir)

	if _, err := eng.Prepare(ctx, []string{specPath}); err != nil {
		t.Fatal(err)
	}
	artifact, err := eng.RunDir.ReadArtifact()
	if err != nil {
		t.Fatal(err)
	}
	writeApprovedExecutionPlanForArtifact(t, eng.RunDir, artifact)
	if err := eng.Approve(ctx); err != nil {
		t.Fatal(err)
	}

	// Wire ticket store as sole task backend.
	ticketDir := filepath.Join(dir, ".tickets")
	ticketStore := ticket.NewStore(ticketDir)
	eng.TaskStore = ticketStore

	result, err := eng.Compile(ctx)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	// Verify tasks exist in ticket store.
	ticketTasks, err := ticketStore.ReadTasks(artifact.RunID)
	if err != nil {
		t.Fatalf("read .tickets/: %v", err)
	}
	if len(ticketTasks) != len(result.Tasks) {
		t.Errorf(".tickets/ has %d tasks, want %d", len(ticketTasks), len(result.Tasks))
	}

	// Verify task IDs match.
	taskIDs := make(map[string]bool, len(result.Tasks))
	for i := range result.Tasks {
		taskIDs[result.Tasks[i].TaskID] = true
	}
	for i := range ticketTasks {
		if !taskIDs[ticketTasks[i].TaskID] {
			t.Errorf("unexpected task %s in .tickets/", ticketTasks[i].TaskID)
		}
	}
}

func TestClaimAndDispatchWithTicketStore(t *testing.T) {
	dir := t.TempDir()
	specPath := writeTestSpec(t, dir)
	ctx := context.Background()

	runDir := state.NewRunDir(dir, "placeholder")
	eng := engine.New(runDir, dir)

	if _, err := eng.Prepare(ctx, []string{specPath}); err != nil {
		t.Fatal(err)
	}
	artifact, err := eng.RunDir.ReadArtifact()
	if err != nil {
		t.Fatal(err)
	}
	writeApprovedExecutionPlanForArtifact(t, eng.RunDir, artifact)
	if err := eng.Approve(ctx); err != nil {
		t.Fatal(err)
	}

	// Wire ticket store.
	ticketDir := filepath.Join(dir, ".tickets")
	ticketStore := ticket.NewStore(ticketDir)
	eng.TaskStore = ticketStore

	result, err := eng.Compile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Tasks) == 0 {
		t.Fatal("no tasks compiled")
	}

	taskID := result.Tasks[0].TaskID
	if err := eng.ClaimAndDispatch(taskID, "test-agent", "claude", 5*time.Minute); err != nil {
		t.Fatalf("ClaimAndDispatch: %v", err)
	}

	// Verify task is now claimed.
	task, err := ticketStore.ReadTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != state.TaskClaimed {
		t.Errorf("status = %s, want claimed", task.Status)
	}
}

func TestClaimAndDispatchFallbackWithoutClaimableStore(t *testing.T) {
	dir := t.TempDir()
	specPath := writeTestSpec(t, dir)
	ctx := context.Background()

	runDir := state.NewRunDir(dir, "placeholder")
	eng := engine.New(runDir, dir)

	if _, err := eng.Prepare(ctx, []string{specPath}); err != nil {
		t.Fatal(err)
	}
	artifact, err := eng.RunDir.ReadArtifact()
	if err != nil {
		t.Fatal(err)
	}
	writeApprovedExecutionPlanForArtifact(t, eng.RunDir, artifact)
	if err := eng.Approve(ctx); err != nil {
		t.Fatal(err)
	}
	result, err := eng.Compile(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// No ticket store set — engine falls back to RunDir via UpdateStatus.
	taskID := result.Tasks[0].TaskID

	if err := eng.ClaimAndDispatch(taskID, "test-agent", "claude", 5*time.Minute); err != nil {
		t.Fatalf("ClaimAndDispatch fallback: %v", err)
	}

	// Verify task is claimed via the engine's RunDir (which Prepare may have changed).
	tasks, err := eng.RunDir.AsTaskStore().ReadTasks(artifact.RunID)
	if err != nil {
		t.Fatal(err)
	}
	for i := range tasks {
		if tasks[i].TaskID == taskID {
			if tasks[i].Status != state.TaskClaimed {
				t.Errorf("status = %s, want claimed", tasks[i].Status)
			}
			return
		}
	}
	t.Error("task not found after ClaimAndDispatch fallback")
}

func TestReleaseTaskWithTicketStore(t *testing.T) {
	dir := t.TempDir()
	specPath := writeTestSpec(t, dir)
	ctx := context.Background()

	runDir := state.NewRunDir(dir, "placeholder")
	eng := engine.New(runDir, dir)

	if _, err := eng.Prepare(ctx, []string{specPath}); err != nil {
		t.Fatal(err)
	}
	artifact, err := eng.RunDir.ReadArtifact()
	if err != nil {
		t.Fatal(err)
	}
	writeApprovedExecutionPlanForArtifact(t, eng.RunDir, artifact)
	if err := eng.Approve(ctx); err != nil {
		t.Fatal(err)
	}

	ticketStore := ticket.NewStore(filepath.Join(dir, ".tickets"))
	eng.TaskStore = ticketStore

	result, err := eng.Compile(ctx)
	if err != nil {
		t.Fatal(err)
	}

	taskID := result.Tasks[0].TaskID

	// Claim then release.
	if err := eng.ClaimAndDispatch(taskID, "agent-a", "claude", 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := eng.ReleaseTask(taskID, "agent-a", state.TaskDone, "completed"); err != nil {
		t.Fatalf("ReleaseTask: %v", err)
	}

	task, err := ticketStore.ReadTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != state.TaskDone {
		t.Errorf("status = %s, want done", task.Status)
	}
}

func TestSweepExpiredClaimsWithTicketStore(t *testing.T) {
	dir := t.TempDir()
	specPath := writeTestSpec(t, dir)
	ctx := context.Background()

	runDir := state.NewRunDir(dir, "placeholder")
	eng := engine.New(runDir, dir)

	if _, err := eng.Prepare(ctx, []string{specPath}); err != nil {
		t.Fatal(err)
	}
	artifact, err := eng.RunDir.ReadArtifact()
	if err != nil {
		t.Fatal(err)
	}
	writeApprovedExecutionPlanForArtifact(t, eng.RunDir, artifact)
	if err := eng.Approve(ctx); err != nil {
		t.Fatal(err)
	}

	ticketStore := ticket.NewStore(filepath.Join(dir, ".tickets"))
	eng.TaskStore = ticketStore

	result, err := eng.Compile(ctx)
	if err != nil {
		t.Fatal(err)
	}

	taskID := result.Tasks[0].TaskID

	// Claim with already-expired lease.
	if err := ticketStore.ClaimTask(taskID, "agent-a", "claude", -1*time.Second); err != nil {
		t.Fatal(err)
	}

	reclaimed, err := eng.SweepExpiredClaims()
	if err != nil {
		t.Fatalf("SweepExpiredClaims: %v", err)
	}
	if len(reclaimed) != 1 || reclaimed[0] != taskID {
		t.Errorf("reclaimed = %v, want [%s]", reclaimed, taskID)
	}

	task, err := ticketStore.ReadTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != state.TaskPending {
		t.Errorf("status = %s, want pending after sweep", task.Status)
	}
}

// mockLearningEnricher implements state.LearningEnricher for testing.
type mockLearningEnricher struct {
	refs     []state.LearningRef
	outcomes []struct {
		ids    []string
		passed bool
	}
}

func (m *mockLearningEnricher) QueryLearnings(_ state.LearningQueryOpts) ([]state.LearningRef, error) {
	return m.refs, nil
}

func (m *mockLearningEnricher) RecordOutcome(ids []string, passed bool) error {
	m.outcomes = append(m.outcomes, struct {
		ids    []string
		passed bool
	}{ids: ids, passed: passed})
	return nil
}

func TestCompileEnrichesTasksWithLearnings(t *testing.T) {
	dir := t.TempDir()
	specPath := writeTestSpec(t, dir)
	ctx := context.Background()

	runDir := state.NewRunDir(dir, "placeholder")
	eng := engine.New(runDir, dir)

	if _, err := eng.Prepare(ctx, []string{specPath}); err != nil {
		t.Fatal(err)
	}
	artifact, err := eng.RunDir.ReadArtifact()
	if err != nil {
		t.Fatal(err)
	}
	writeApprovedExecutionPlanForArtifact(t, eng.RunDir, artifact)
	if err := eng.Approve(ctx); err != nil {
		t.Fatal(err)
	}

	// Wire ticket store + mock learning enricher.
	ticketStore := ticket.NewStore(filepath.Join(dir, ".tickets"))
	eng.TaskStore = ticketStore

	mock := &mockLearningEnricher{
		refs: []state.LearningRef{
			{ID: "lrn-test1", Category: "anti_pattern", Summary: "Avoid X"},
			{ID: "lrn-test2", Category: "codebase", Summary: "Use Y"},
		},
	}
	eng.LearningEnricher = mock

	result, err := eng.Compile(ctx)
	if err != nil {
		t.Fatalf("Compile with learnings: %v", err)
	}

	// Verify tasks got enriched.
	for i := range result.Tasks {
		task := &result.Tasks[i]
		if len(task.LearningIDs) != 2 {
			t.Errorf("task %s: LearningIDs len = %d, want 2", task.TaskID, len(task.LearningIDs))
		}
		if len(task.Warnings) == 0 {
			t.Errorf("task %s: expected Warnings from anti_pattern learning", task.TaskID)
		}
		if len(task.Constraints) == 0 {
			t.Errorf("task %s: expected Constraints from codebase learning", task.TaskID)
		}
		if len(task.LearningContext) != 2 {
			t.Errorf("task %s: LearningContext len = %d, want 2", task.TaskID, len(task.LearningContext))
		}
	}

	// Outcome recording happens at verify time, not compile time.
	// Verify no outcomes recorded yet (compile doesn't call RecordOutcome).
	if len(mock.outcomes) != 0 {
		t.Errorf("expected no outcomes at compile time, got %d", len(mock.outcomes))
	}
}

func TestCompileWithoutLearningEnricher(t *testing.T) {
	dir := t.TempDir()
	specPath := writeTestSpec(t, dir)
	ctx := context.Background()

	runDir := state.NewRunDir(dir, "placeholder")
	eng := engine.New(runDir, dir)

	if _, err := eng.Prepare(ctx, []string{specPath}); err != nil {
		t.Fatal(err)
	}
	artifact, err := eng.RunDir.ReadArtifact()
	if err != nil {
		t.Fatal(err)
	}
	writeApprovedExecutionPlanForArtifact(t, eng.RunDir, artifact)
	if err := eng.Approve(ctx); err != nil {
		t.Fatal(err)
	}

	ticketStore := ticket.NewStore(filepath.Join(dir, ".tickets"))
	eng.TaskStore = ticketStore
	// No LearningEnricher set — should compile without enrichment.

	result, err := eng.Compile(ctx)
	if err != nil {
		t.Fatalf("Compile without enricher: %v", err)
	}

	for i := range result.Tasks {
		if len(result.Tasks[i].LearningIDs) != 0 {
			t.Errorf("task %s: expected no LearningIDs without enricher", result.Tasks[i].TaskID)
		}
	}
}

func TestReviewExecutionPlanRejectsUncoveredRequirement(t *testing.T) {
	dir := t.TempDir()
	runDir := state.NewRunDir(dir, "run-uncovered")
	if err := runDir.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	// Artifact has 3 requirements; plan only covers 2.
	if err := runDir.WriteArtifact(&state.RunArtifact{
		SchemaVersion: "0.1",
		RunID:         "run-uncovered",
		Requirements: []state.Requirement{
			{ID: "AT-FR-001", Text: "Requirement 1"},
			{ID: "AT-FR-002", Text: "Requirement 2"},
			{ID: "AT-FR-003", Text: "Requirement 3"},
		},
	}); err != nil {
		t.Fatalf("WriteArtifact: %v", err)
	}
	if err := runDir.WriteExecutionPlan(&state.ExecutionPlan{
		SchemaVersion:           "0.1",
		RunID:                   "run-uncovered",
		ArtifactType:            "execution_plan",
		SourceTechnicalSpecHash: "sha256:test",
		Status:                  state.ArtifactDrafted,
		Slices: []state.ExecutionSlice{
			{SliceID: "slice-001", Title: "A", Goal: "G", RequirementIDs: []string{"AT-FR-001"}, OwnedPaths: []string{"a"}, AcceptanceChecks: []string{"check"}, Risk: "low", Size: "small"},
			{SliceID: "slice-002", Title: "B", Goal: "G", RequirementIDs: []string{"AT-FR-002"}, OwnedPaths: []string{"b"}, AcceptanceChecks: []string{"check"}, Risk: "low", Size: "small"},
		},
		GeneratedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("WriteExecutionPlan: %v", err)
	}

	eng := engine.New(runDir, dir)
	review, err := eng.ReviewExecutionPlan(context.Background())
	if err != nil {
		t.Fatalf("ReviewExecutionPlan: %v", err)
	}
	if review.Status != state.ReviewFail {
		t.Fatal("review should fail when a requirement is uncovered")
	}
	found := false
	for _, f := range review.BlockingFindings {
		if f.Category == "uncovered_requirement" {
			found = true
			if len(f.RequirementIDs) != 1 || f.RequirementIDs[0] != "AT-FR-003" {
				t.Errorf("expected AT-FR-003 in finding, got: %v", f.RequirementIDs)
			}
			break
		}
	}
	if !found {
		t.Fatalf("expected uncovered_requirement finding, got: %+v", review.BlockingFindings)
	}
}

func TestReviewExecutionPlanWarnsOnDuplicateCoverage(t *testing.T) {
	dir := t.TempDir()
	runDir := state.NewRunDir(dir, "run-dup-cov")
	if err := runDir.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := runDir.WriteArtifact(&state.RunArtifact{
		SchemaVersion: "0.1",
		RunID:         "run-dup-cov",
		Requirements: []state.Requirement{
			{ID: "AT-FR-001", Text: "Requirement 1"},
		},
	}); err != nil {
		t.Fatalf("WriteArtifact: %v", err)
	}
	// Two slices cover the same requirement.
	if err := runDir.WriteExecutionPlan(&state.ExecutionPlan{
		SchemaVersion:           "0.1",
		RunID:                   "run-dup-cov",
		ArtifactType:            "execution_plan",
		SourceTechnicalSpecHash: "sha256:test",
		Status:                  state.ArtifactDrafted,
		Slices: []state.ExecutionSlice{
			{SliceID: "slice-001", Title: "A", Goal: "G", RequirementIDs: []string{"AT-FR-001"}, OwnedPaths: []string{"a"}, AcceptanceChecks: []string{"check"}, Risk: "low", Size: "small"},
			{SliceID: "slice-002", Title: "B", Goal: "G", RequirementIDs: []string{"AT-FR-001"}, OwnedPaths: []string{"b"}, AcceptanceChecks: []string{"check"}, Risk: "low", Size: "small"},
		},
		GeneratedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("WriteExecutionPlan: %v", err)
	}

	eng := engine.New(runDir, dir)
	review, err := eng.ReviewExecutionPlan(context.Background())
	if err != nil {
		t.Fatalf("ReviewExecutionPlan: %v", err)
	}
	// Should pass (duplicate coverage is a warning, not a blocker).
	if review.Status != state.ReviewPass {
		t.Fatalf("review should pass with duplicate coverage (warning only), got: %s", review.Status)
	}
	found := false
	for _, w := range review.Warnings {
		if strings.Contains(w.Summary, "AT-FR-001") && strings.Contains(w.Summary, "2 slices") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected duplicate coverage warning for AT-FR-001, got warnings: %+v", review.Warnings)
	}
}

func TestReviewExecutionPlanPassesWithFullCoverage(t *testing.T) {
	dir := t.TempDir()
	runDir := state.NewRunDir(dir, "run-full-cov")
	if err := runDir.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := runDir.WriteArtifact(&state.RunArtifact{
		SchemaVersion: "0.1",
		RunID:         "run-full-cov",
		Requirements: []state.Requirement{
			{ID: "AT-FR-001", Text: "Requirement 1"},
			{ID: "AT-FR-002", Text: "Requirement 2"},
		},
	}); err != nil {
		t.Fatalf("WriteArtifact: %v", err)
	}
	if err := runDir.WriteExecutionPlan(&state.ExecutionPlan{
		SchemaVersion:           "0.1",
		RunID:                   "run-full-cov",
		ArtifactType:            "execution_plan",
		SourceTechnicalSpecHash: "sha256:test",
		Status:                  state.ArtifactDrafted,
		Slices: []state.ExecutionSlice{
			{SliceID: "slice-001", Title: "A", Goal: "G", RequirementIDs: []string{"AT-FR-001"}, OwnedPaths: []string{"a"}, AcceptanceChecks: []string{"check"}, Risk: "low", Size: "small"},
			{SliceID: "slice-002", Title: "B", Goal: "G", RequirementIDs: []string{"AT-FR-002"}, OwnedPaths: []string{"b"}, AcceptanceChecks: []string{"check"}, Risk: "low", Size: "small"},
		},
		GeneratedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("WriteExecutionPlan: %v", err)
	}

	eng := engine.New(runDir, dir)
	review, err := eng.ReviewExecutionPlan(context.Background())
	if err != nil {
		t.Fatalf("ReviewExecutionPlan: %v", err)
	}
	if review.Status != state.ReviewPass {
		t.Fatalf("review should pass with full coverage, got: %s (findings: %+v)", review.Status, review.BlockingFindings)
	}
}

func TestDraftTechnicalSpec_CanonicalPassThrough(t *testing.T) {
	dir := t.TempDir()
	runDir := state.NewRunDir(dir, "run-canonical")
	if err := runDir.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	sourcePath := filepath.Join(dir, "canonical-spec.md")
	spec := `# Technical Specification

## 1. Technical context
Context content.

## 2. Architecture
Architecture content.

## 3. Canonical artifacts and schemas
Artifacts content.

## 4. Interfaces
Interfaces content.

## 5. Verification
Verification content.

## 6. Requirement traceability
Traceability content.

## 7. Open questions and risks
Risks content.

## 8. Approval
Status: Draft
`
	if err := os.WriteFile(sourcePath, []byte(spec), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Track whether InvokeFunc is called — it should NOT be for canonical docs.
	invokeCalled := false
	original := agentcli.InvokeFunc
	agentcli.InvokeFunc = func(_ context.Context, _ *agentcli.CLIBackend, _ string, _ int) (string, error) {
		invokeCalled = true
		return "", fmt.Errorf("should not be called")
	}
	t.Cleanup(func() { agentcli.InvokeFunc = original })

	eng := engine.New(runDir, dir)
	if err := eng.DraftTechnicalSpec(context.Background(), sourcePath, false); err != nil {
		t.Fatalf("DraftTechnicalSpec: %v", err)
	}

	if invokeCalled {
		t.Fatal("agent InvokeFunc was called for canonical doc — should have been skipped")
	}

	data, err := runDir.ReadTechnicalSpec()
	if err != nil {
		t.Fatalf("ReadTechnicalSpec: %v", err)
	}
	if string(data) != spec {
		t.Fatalf("canonical doc was modified:\n  got:  %d bytes\n  want: %d bytes", len(data), len(spec))
	}
}

const (
	testWave0 = "wave-0"
	testWave1 = "wave-1"
	testWave2 = "wave-2"
)

func locateRepoRoot(t *testing.T) string {
	t.Helper()
	workDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(workDir, "go.mod")); statErr == nil {
			return workDir
		}
		parent := filepath.Dir(workDir)
		if parent == workDir {
			t.Fatalf("could not locate repo root from %q", workDir)
		}
		workDir = parent
	}
}

func assertExplorationSlice(t *testing.T, slice *state.ExecutionSlice) {
	t.Helper()
	if !reflect.DeepEqual(slice.FilesLikelyTouched, []string{"internal/engine/plan.go", "internal/engine/plan_new_test.go"}) {
		t.Fatalf("FilesLikelyTouched = %v", slice.FilesLikelyTouched)
	}
	if !reflect.DeepEqual(slice.ImplementationDetail, state.ImplementationDetail{
		FilesToModify: []state.FileChange{
			{Path: "internal/engine/plan.go", Change: "Modify existing implementation surfaced during exploration", IsNew: false},
			{Path: "internal/engine/plan_new_test.go", Change: "Create implementation file surfaced during exploration", IsNew: true},
		},
		SymbolsToUse: []string{"DraftExecutionPlan at internal/engine/plan.go:24"},
		TestsToAdd:   []string{"TestDraftExecutionPlanIntegratesExplorationDetailAndValidation"},
	}) {
		t.Fatalf("ImplementationDetail = %#v", slice.ImplementationDetail)
	}
	if !reflect.DeepEqual(slice.ValidationChecks, []state.ValidationCheck{
		{Type: "files_exist", Paths: []string{"internal/engine/plan_new_test.go"}},
		{Type: "tests", Tool: "go", Args: []string{"test", "-run", "^TestDraftExecutionPlanIntegratesExplorationDetailAndValidation$", "./internal/engine/..."}},
		{Type: "command", Tool: "make", Args: []string{"check"}},
	}) {
		t.Fatalf("ValidationChecks = %#v", slice.ValidationChecks)
	}
}

func TestDraftExecutionPlanPopulatesWaveIDs(t *testing.T) {
	dir := t.TempDir()
	runDir := state.NewRunDir(dir, "run-plan-waves")
	if err := runDir.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	artifact := &state.RunArtifact{
		SchemaVersion: "0.1",
		RunID:         "run-plan-waves",
		Requirements: []state.Requirement{
			{ID: "AT-FR-001", Text: "First functional requirement.", SourceSpec: "spec.md", SourceLine: 10},
			{ID: "AT-FR-002", Text: "Second functional requirement.", SourceSpec: "spec.md", SourceLine: 11},
			{ID: "AT-FR-003", Text: "Third functional requirement.", SourceSpec: "spec.md", SourceLine: 12},
		},
	}
	if err := runDir.WriteArtifact(artifact); err != nil {
		t.Fatalf("WriteArtifact: %v", err)
	}

	techSpecHash := writeApprovedTechnicalSpec(t, runDir)

	eng := engine.New(runDir, dir)
	t.Setenv("FABRIKK_CODE_INTEL", filepath.Join(t.TempDir(), "missing-code-intel.py"))
	eng.InvokeFn = func(_ context.Context, _ *agentcli.CLIBackend, _ string, _ int) (string, error) {
		return "", os.ErrPermission
	}
	plan, err := eng.DraftExecutionPlan(context.Background())
	if err != nil {
		t.Fatalf("DraftExecutionPlan: %v", err)
	}

	if plan.SourceTechnicalSpecHash != techSpecHash {
		t.Fatalf("SourceTechnicalSpecHash = %q, want %q", plan.SourceTechnicalSpecHash, techSpecHash)
	}
	if len(plan.Slices) != 3 {
		t.Fatalf("slice count = %d, want 3", len(plan.Slices))
	}
	for i, want := range []string{testWave0, testWave1, testWave2} {
		if got := plan.Slices[i].WaveID; got != want {
			t.Fatalf("slice %d WaveID = %q, want %q", i, got, want)
		}
	}

	stored, err := runDir.ReadExecutionPlan()
	if err != nil {
		t.Fatalf("ReadExecutionPlan: %v", err)
	}
	for i, want := range []string{testWave0, testWave1, testWave2} {
		if got := stored.Slices[i].WaveID; got != want {
			t.Fatalf("stored slice %d WaveID = %q, want %q", i, got, want)
		}
	}
}

func TestDraftExecutionPlanReturnsHumanInputRequiredForAskFirstBoundaries(t *testing.T) {
	dir := t.TempDir()
	runDir := state.NewRunDir(dir, "run-plan-ask-first")
	if err := runDir.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	artifact := &state.RunArtifact{
		SchemaVersion: "0.1",
		RunID:         "run-plan-ask-first",
		Requirements: []state.Requirement{
			{ID: "AT-FR-001", Text: "First functional requirement.", SourceSpec: "spec.md", SourceLine: 10},
		},
		Boundaries: state.Boundaries{
			AskFirst: []string{" confirm rollout window ", "notify compliance"},
		},
	}
	if err := runDir.WriteArtifact(artifact); err != nil {
		t.Fatalf("WriteArtifact: %v", err)
	}

	writeApprovedTechnicalSpec(t, runDir)

	invokeCalled := false
	eng := engine.New(runDir, dir)
	eng.InvokeFn = func(_ context.Context, _ *agentcli.CLIBackend, _ string, _ int) (string, error) {
		invokeCalled = true
		return "", nil
	}

	_, err := eng.DraftExecutionPlan(context.Background())
	if err == nil {
		t.Fatal("DraftExecutionPlan error = nil, want HumanInputRequiredError")
	}
	var humanErr *engine.HumanInputRequiredError
	if !errors.As(err, &humanErr) {
		t.Fatalf("DraftExecutionPlan error = %T, want HumanInputRequiredError", err)
	}
	want := []string{"confirm rollout window", "notify compliance"}
	if !reflect.DeepEqual(humanErr.Items, want) {
		t.Fatalf("HumanInputRequiredError.Items = %v, want %v", humanErr.Items, want)
	}
	if invokeCalled {
		t.Fatal("InvokeFn called despite ask-first boundary")
	}
}

func TestDraftExecutionPlanSerializesConflictingOwnedPaths(t *testing.T) {
	dir := t.TempDir()
	runDir := state.NewRunDir(dir, "run-plan-conflicts")
	if err := runDir.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	artifact := &state.RunArtifact{
		SchemaVersion: "0.1",
		RunID:         "run-plan-conflicts",
		Requirements: []state.Requirement{
			{ID: "AT-FR-001", Text: "Compile approved specs into the task graph.", SourceSpec: "spec.md", SourceLine: 10},
			{ID: "AT-TS-001", Text: "Deterministic task graph compilation remains verifiable.", SourceSpec: "spec.md", SourceLine: 11},
		},
	}
	if err := runDir.WriteArtifact(artifact); err != nil {
		t.Fatalf("WriteArtifact: %v", err)
	}

	writeApprovedTechnicalSpec(t, runDir)

	eng := engine.New(runDir, dir)
	t.Setenv("FABRIKK_CODE_INTEL", filepath.Join(t.TempDir(), "missing-code-intel.py"))
	eng.InvokeFn = func(_ context.Context, _ *agentcli.CLIBackend, _ string, _ int) (string, error) {
		return "", os.ErrPermission
	}
	plan, err := eng.DraftExecutionPlan(context.Background())
	if err != nil {
		t.Fatalf("DraftExecutionPlan: %v", err)
	}
	if len(plan.Slices) != 2 {
		t.Fatalf("slice count = %d, want 2", len(plan.Slices))
	}
	if got := plan.Slices[0].WaveID; got != testWave0 {
		t.Fatalf("slice 0 WaveID = %q, want %q", got, testWave0)
	}
	if got := plan.Slices[1].WaveID; got != testWave1 {
		t.Fatalf("slice 1 WaveID = %q, want %q", got, testWave1)
	}
	if got := plan.Slices[1].DependsOn; len(got) != 0 {
		t.Fatalf("slice 1 DependsOn = %v, want none", got)
	}
}

func TestDraftExecutionPlanPrunesLaneOnlyDependenciesWithoutOverlap(t *testing.T) {
	dir := t.TempDir()
	runDir := state.NewRunDir(dir, "run-plan-prune")
	if err := runDir.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	artifact := &state.RunArtifact{
		SchemaVersion: "0.1",
		RunID:         "run-plan-prune",
		Requirements: []state.Requirement{
			{ID: "AT-FR-001", Text: "Prepare the CLI command output.", SourceSpec: "spec.md", SourceLine: 10},
			{ID: "AT-FR-002", Text: "Council synthesis aggregates findings.", SourceSpec: "spec.md", SourceLine: 11},
		},
	}
	if err := runDir.WriteArtifact(artifact); err != nil {
		t.Fatalf("WriteArtifact: %v", err)
	}

	writeApprovedTechnicalSpec(t, runDir)

	eng := engine.New(runDir, dir)
	t.Setenv("FABRIKK_CODE_INTEL", filepath.Join(t.TempDir(), "missing-code-intel.py"))
	eng.InvokeFn = func(_ context.Context, _ *agentcli.CLIBackend, _ string, _ int) (string, error) {
		return "", os.ErrPermission
	}
	plan, err := eng.DraftExecutionPlan(context.Background())
	if err != nil {
		t.Fatalf("DraftExecutionPlan: %v", err)
	}

	if len(plan.Slices) != 2 {
		t.Fatalf("slice count = %d, want 2", len(plan.Slices))
	}
	for i := range plan.Slices {
		if got := plan.Slices[i].WaveID; got != testWave0 {
			t.Fatalf("slice %d WaveID = %q, want %q", i, got, testWave0)
		}
		if got := plan.Slices[i].DependsOn; len(got) != 0 {
			t.Fatalf("slice %d DependsOn = %v, want none", i, got)
		}
	}

	stored, err := runDir.ReadExecutionPlan()
	if err != nil {
		t.Fatalf("ReadExecutionPlan: %v", err)
	}
	if got := stored.Slices[1].WaveID; got != testWave0 {
		t.Fatalf("stored slice 1 WaveID = %q, want %q", got, testWave0)
	}
	if got := stored.Slices[1].DependsOn; len(got) != 0 {
		t.Fatalf("stored slice 1 DependsOn = %v, want none", got)
	}
}

func TestDraftExecutionPlanIntegratesExplorationDetailAndValidation(t *testing.T) {
	dir := t.TempDir()
	runDir := state.NewRunDir(dir, "run-plan-exploration")
	if err := runDir.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	artifact := &state.RunArtifact{
		SchemaVersion: "0.1",
		RunID:         "run-plan-exploration",
		Requirements: []state.Requirement{{
			ID:         "AT-FR-001",
			Text:       "Update the plan drafting flow.",
			SourceSpec: "spec.md",
			SourceLine: 10,
		}},
		QualityGate: &state.QualityGate{Command: "make check", Required: true},
	}
	if err := runDir.WriteArtifact(artifact); err != nil {
		t.Fatalf("WriteArtifact: %v", err)
	}
	writeApprovedTechnicalSpec(t, runDir)

	t.Setenv("FABRIKK_CODE_INTEL", filepath.Join(t.TempDir(), "missing-code-intel.py"))
	eng := engine.New(runDir, locateRepoRoot(t))
	eng.InvokeFn = func(_ context.Context, _ *agentcli.CLIBackend, _ string, timeoutSec int) (string, error) {
		if timeoutSec != 360 {
			t.Fatalf("timeoutSec = %d, want 360", timeoutSec)
		}
		return `{"file_inventory":[{"path":"internal/engine/plan.go","exists":true,"is_new":false,"line_count":120,"language":"go"},{"path":"internal/engine/plan_new_test.go","exists":false,"is_new":true,"line_count":0,"language":"go"}],"symbols":[{"file_path":"internal/engine/plan.go","line":24,"kind":"func","signature":"func (e *Engine) DraftExecutionPlan(ctx context.Context) (*state.ExecutionPlan, error)"}],"test_files":[{"path":"internal/engine/plan_new_test.go","test_names":["TestDraftExecutionPlanIntegratesExplorationDetailAndValidation"],"covers":"internal/engine/plan.go"}],"reuse_points":[{"file_path":"internal/engine/plan.go","line":24,"symbol":"DraftExecutionPlan","relevance":"planner entry point"}]}`, nil
	}

	plan, err := eng.DraftExecutionPlan(context.Background())
	if err != nil {
		t.Fatalf("DraftExecutionPlan: %v", err)
	}
	if len(plan.Slices) != 1 {
		t.Fatalf("slice count = %d, want 1", len(plan.Slices))
	}
	assertExplorationSlice(t, &plan.Slices[0])
}

func TestReviewExecutionPlanFailsWhenArtifactUnreadable(t *testing.T) {
	dir := t.TempDir()
	runDir := state.NewRunDir(dir, "run-review-missing-artifact")
	if err := runDir.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	plan := &state.ExecutionPlan{
		SchemaVersion: "0.1",
		RunID:         "run-review-missing-artifact",
		ArtifactType:  "execution_plan",
		Status:        state.ArtifactDrafted,
		Slices: []state.ExecutionSlice{{
			SliceID:          "slice-001",
			Title:            "A",
			Goal:             "G",
			RequirementIDs:   []string{"AT-FR-001"},
			WaveID:           "wave-0",
			OwnedPaths:       []string{"internal/engine"},
			AcceptanceChecks: []string{"check"},
			ValidationChecks: []state.ValidationCheck{{Type: "files_exist", Paths: []string{"internal/engine/plan.go"}}},
			Risk:             "low",
			Size:             "small",
		}},
		GeneratedAt: time.Now().UTC(),
	}
	if err := runDir.WriteExecutionPlan(plan); err != nil {
		t.Fatalf("WriteExecutionPlan: %v", err)
	}
	if err := runDir.WriteExecutionPlanMarkdown([]byte("# Execution Plan\n")); err != nil {
		t.Fatalf("WriteExecutionPlanMarkdown: %v", err)
	}

	eng := engine.New(runDir, dir)
	review, err := eng.ReviewExecutionPlan(context.Background())
	if err != nil {
		t.Fatalf("ReviewExecutionPlan: %v", err)
	}
	if review.Status != state.ReviewFail {
		t.Fatalf("review status = %s, want fail", review.Status)
	}
	found := false
	for _, finding := range review.BlockingFindings {
		if finding.Category == "artifact_read_failure" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("BlockingFindings = %+v, want artifact_read_failure", review.BlockingFindings)
	}
}
