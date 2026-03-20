package engine_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/runger/attest/internal/compiler"
	"github.com/runger/attest/internal/engine"
	"github.com/runger/attest/internal/state"
	"github.com/runger/attest/internal/ticket"
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

	spec := `# attest Technical Specification

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

func TestDraftTechnicalSpecNormalizesLegacyHeadings(t *testing.T) {
	dir := t.TempDir()
	runDir := state.NewRunDir(dir, "run-normalize")
	if err := runDir.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	sourcePath := filepath.Join(dir, "legacy-technical-spec.md")
	spec := `# attest Technical Specification

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
	if err := os.WriteFile(sourcePath, []byte(spec), 0o644); err != nil {
		t.Fatalf("WriteFile(source): %v", err)
	}

	eng := engine.New(runDir, dir)
	if err := eng.DraftTechnicalSpec(context.Background(), sourcePath); err != nil {
		t.Fatalf("DraftTechnicalSpec: %v", err)
	}

	data, err := runDir.ReadTechnicalSpec()
	if err != nil {
		t.Fatalf("ReadTechnicalSpec: %v", err)
	}
	got := string(data)
	for _, heading := range []string{
		"## 1. Technical context",
		"## 2. Architecture",
		"## 3. Canonical artifacts and schemas",
		"## 4. Interfaces",
		"## 5. Verification",
		"## 6. Requirement traceability",
		"## 7. Open questions and risks",
		"## 8. Approval",
	} {
		if !strings.Contains(got, heading) {
			t.Fatalf("normalized spec missing heading %q:\n%s", heading, got)
		}
	}
	if strings.Contains(got, "## 1. Implementation decisions") {
		t.Fatalf("normalized spec kept legacy heading:\n%s", got)
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
