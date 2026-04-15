package compiler_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/php-workx/fabrikk/internal/compiler"
	"github.com/php-workx/fabrikk/internal/state"
)

func TestCompileDeterministic(t *testing.T) {
	// AT-TS-001: Preparing the same approved run artifact twice yields the same initial tasks.
	artifact := &state.RunArtifact{
		SchemaVersion: "0.1",
		RunID:         "test-run",
		Requirements: []state.Requirement{
			{ID: "AT-FR-002", Text: "Second requirement"},
			{ID: "AT-FR-001", Text: "First requirement"},
			{ID: "AT-FR-003", Text: "Third requirement"},
		},
	}

	result1, err := compiler.Compile(artifact)
	if err != nil {
		t.Fatalf("Compile 1: %v", err)
	}

	result2, err := compiler.Compile(artifact)
	if err != nil {
		t.Fatalf("Compile 2: %v", err)
	}

	if len(result1.Tasks) != len(result2.Tasks) {
		t.Fatalf("task count differs: %d vs %d", len(result1.Tasks), len(result2.Tasks))
	}

	for i := range result1.Tasks {
		if result1.Tasks[i].TaskID != result2.Tasks[i].TaskID {
			t.Errorf("task %d ID differs: %q vs %q", i, result1.Tasks[i].TaskID, result2.Tasks[i].TaskID)
		}
		if result1.Tasks[i].Title != result2.Tasks[i].Title {
			t.Errorf("task %d title differs", i)
		}
	}
}

func TestCompileSortedOutput(t *testing.T) {
	artifact := &state.RunArtifact{
		Requirements: []state.Requirement{
			{ID: "AT-FR-003", Text: "C", SourceSpec: "spec.md", SourceLine: 12},
			{ID: "AT-FR-001", Text: "A", SourceSpec: "spec.md", SourceLine: 10},
			{ID: "AT-FR-002", Text: "B", SourceSpec: "spec.md", SourceLine: 11},
		},
	}

	result, err := compiler.Compile(artifact)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Tasks) != 3 {
		t.Fatalf("task count = %d, want 3 single-requirement tasks", len(result.Tasks))
	}
	for i, want := range []string{"AT-FR-001", "AT-FR-002", "AT-FR-003"} {
		if got := result.Tasks[i].RequirementIDs[0]; got != want {
			t.Errorf("task %d requirement = %q, want %q", i, got, want)
		}
		if result.Tasks[i].GroupingReason != "" {
			t.Errorf("task %d GroupingReason = %q, want empty", i, result.Tasks[i].GroupingReason)
		}
		if len(result.Tasks[i].GroupedRequirementIDs) != 0 {
			t.Errorf("task %d GroupedRequirementIDs len = %d, want 0", i, len(result.Tasks[i].GroupedRequirementIDs))
		}
	}
}

func TestCompileCoverage(t *testing.T) {
	artifact := &state.RunArtifact{
		Requirements: []state.Requirement{
			{ID: "AT-FR-001", Text: "Req one"},
			{ID: "AT-FR-002", Text: "Req two"},
		},
	}

	result, err := compiler.Compile(artifact)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Coverage) != 2 {
		t.Fatalf("coverage count = %d, want 2", len(result.Coverage))
	}

	// Check no unassigned requirements.
	unassigned := compiler.CheckCoverage(artifact, result.Coverage)
	if len(unassigned) != 0 {
		t.Errorf("unexpected unassigned: %v", unassigned)
	}
}

func TestCheckCoverageDetectsGaps(t *testing.T) {
	artifact := &state.RunArtifact{
		Requirements: []state.Requirement{
			{ID: "AT-FR-001", Text: "Covered"},
			{ID: "AT-FR-002", Text: "Not covered"},
		},
	}

	// Only AT-FR-001 is covered.
	coverage := []state.RequirementCoverage{
		{RequirementID: "AT-FR-001", CoveringTaskIDs: []string{"task-1"}},
	}

	unassigned := compiler.CheckCoverage(artifact, coverage)
	if len(unassigned) != 1 || unassigned[0] != "AT-FR-002" {
		t.Errorf("unassigned = %v, want [AT-FR-002]", unassigned)
	}
}

func TestCompileNilArtifact(t *testing.T) {
	_, err := compiler.Compile(nil)
	if err == nil {
		t.Fatal("expected error for nil artifact")
	}
}

func TestCompileEmptyRequirements(t *testing.T) {
	_, err := compiler.Compile(&state.RunArtifact{})
	if err == nil {
		t.Fatal("expected error for empty requirements")
	}
}

func TestCompileSplitsNearbyRequirementsWithoutExploration(t *testing.T) {
	artifact := &state.RunArtifact{
		Requirements: []state.Requirement{
			{ID: "AT-FR-001", Text: "The system must verify command results.", SourceSpec: "functional.md", SourceLine: 10},
			{ID: "AT-FR-002", Text: "The system must verify required evidence.", SourceSpec: "functional.md", SourceLine: 11},
			{ID: "AT-FR-003", Text: "The system must verify requirement linkage.", SourceSpec: "functional.md", SourceLine: 12},
		},
	}

	result, err := compiler.Compile(artifact)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	if len(result.Tasks) != 3 {
		t.Fatalf("task count = %d, want 3 single-requirement tasks", len(result.Tasks))
	}
	for _, task := range result.Tasks {
		if len(task.RequirementIDs) != 1 {
			t.Fatalf("task %s requirement count = %d, want 1", task.TaskID, len(task.RequirementIDs))
		}
		if task.GroupingReason != "" || len(task.GroupedRequirementIDs) != 0 {
			t.Fatalf("task %s unexpectedly carries grouping metadata", task.TaskID)
		}
	}
}

func TestCompileSplitsDistinctThemesWithinSameFamily(t *testing.T) {
	artifact := &state.RunArtifact{
		Requirements: []state.Requirement{
			{ID: "AT-AS-001", Text: "Given the same approved run artifact, the system produces the same initial task graph.", SourceSpec: "functional.md", SourceLine: 291},
			{ID: "AT-AS-002", Text: "A run continues after the initiating session ends.", SourceSpec: "functional.md", SourceLine: 292},
			{ID: "AT-AS-003", Text: "Verification failures create repair work automatically.", SourceSpec: "functional.md", SourceLine: 293},
			{ID: "AT-AS-004", Text: "Codex or Gemini can block closure even after deterministic verification passes.", SourceSpec: "functional.md", SourceLine: 294},
			{ID: "AT-AS-005", Text: "A run finishes only when every requirement in the approved artifact is verified or explicitly blocked.", SourceSpec: "functional.md", SourceLine: 295},
		},
	}

	result, err := compiler.Compile(artifact)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	if len(result.Tasks) != 5 {
		t.Fatalf("task count = %d, want 5 single-requirement tasks", len(result.Tasks))
	}
	for _, task := range result.Tasks {
		if len(task.RequirementIDs) != 1 {
			t.Fatalf("task %s groups %d requirements, want 1", task.TaskID, len(task.RequirementIDs))
		}
	}
}

func TestCompileDoesNotGroupWithoutSymbolEvidence(t *testing.T) {
	artifact := &state.RunArtifact{
		Requirements: []state.Requirement{
			{ID: "AT-TS-001", Text: "First technical scenario.", SourceSpec: "technical.md", SourceLine: 100},
			{ID: "AT-TS-002", Text: "Second technical scenario.", SourceSpec: "technical.md", SourceLine: 101},
			{ID: "AT-TS-003", Text: "Third technical scenario.", SourceSpec: "technical.md", SourceLine: 102},
			{ID: "AT-TS-004", Text: "Fourth technical scenario.", SourceSpec: "technical.md", SourceLine: 103},
			{ID: "AT-TS-005", Text: "Fifth technical scenario.", SourceSpec: "technical.md", SourceLine: 104},
			{ID: "AT-TS-006", Text: "Sixth technical scenario.", SourceSpec: "technical.md", SourceLine: 105},
			{ID: "AT-TS-007", Text: "Seventh technical scenario.", SourceSpec: "technical.md", SourceLine: 106},
		},
	}

	result, err := compiler.Compile(artifact)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	if len(result.Tasks) != 7 {
		t.Fatalf("task count = %d, want 7 single-requirement tasks", len(result.Tasks))
	}
	for _, task := range result.Tasks {
		if len(task.RequirementIDs) != 1 {
			t.Fatalf("task %s requirement count = %d, want 1", task.TaskID, len(task.RequirementIDs))
		}
	}
}

func TestCompileCreatesIndependentTasksAcrossLanes(t *testing.T) {
	artifact := &state.RunArtifact{
		Requirements: []state.Requirement{
			{ID: "AT-TS-001", Text: "First technical scenario.", SourceSpec: "technical.md", SourceLine: 100},
			{ID: "AT-FR-001", Text: "The system must verify command results.", SourceSpec: "technical.md", SourceLine: 110},
		},
	}

	result, err := compiler.Compile(artifact)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	if len(result.Tasks) != 2 {
		t.Fatalf("task count = %d, want 2 tasks in different lanes", len(result.Tasks))
	}
	for _, task := range result.Tasks {
		if len(task.DependsOn) != 0 {
			t.Fatalf("task %s DependsOn = %v, want none across lanes", task.TaskID, task.DependsOn)
		}
	}
}

func TestCompileExecutionPlanInjectsAlwaysBoundariesIntoTaskConstraints(t *testing.T) {
	artifact := &state.RunArtifact{
		SchemaVersion: "0.1",
		RunID:         "test-run",
		Requirements: []state.Requirement{
			{ID: "AT-FR-001", Text: "Implement the change."},
		},
		Boundaries: state.Boundaries{
			Always: []string{" keep tests focused ", "preserve audit trail", "keep tests focused"},
		},
	}
	plan := &state.ExecutionPlan{
		Slices: []state.ExecutionSlice{{
			SliceID:        "slice-001",
			Title:          "Implement change",
			Goal:           "Implement change safely",
			RequirementIDs: []string{"AT-FR-001"},
			OwnedPaths:     []string{"internal/engine"},
			Risk:           "low",
			Size:           "S",
		}},
	}

	result, err := compiler.CompileExecutionPlan(artifact, plan)
	if err != nil {
		t.Fatalf("CompileExecutionPlan: %v", err)
	}
	if len(result.Tasks) != 1 {
		t.Fatalf("task count = %d, want 1", len(result.Tasks))
	}
	want := []string{"keep tests focused", "preserve audit trail"}
	if !reflect.DeepEqual(result.Tasks[0].Constraints, want) {
		t.Fatalf("Constraints = %v, want %v", result.Tasks[0].Constraints, want)
	}
}

func TestComputeWavesGroupsTasksByDependencyDepth(t *testing.T) {
	const (
		taskA = "task-a"
		taskB = "task-b"
		taskC = "task-c"
		taskD = "task-d"
	)
	tasks := []state.Task{
		{TaskID: taskA},
		{TaskID: taskB},
		{TaskID: taskC, DependsOn: []string{taskA}},
		{TaskID: taskD, DependsOn: []string{taskB, taskC}},
	}

	waves := compiler.ComputeWaves(tasks)

	if len(waves) != 3 {
		t.Fatalf("wave count = %d, want 3", len(waves))
	}

	if waves[0].WaveID != "wave-0" {
		t.Fatalf("wave 0 ID = %q, want %q", waves[0].WaveID, "wave-0")
	}
	if got := waves[0].Tasks; len(got) != 2 || got[0] != taskA || got[1] != taskB {
		t.Fatalf("wave-0 tasks = %v, want [%s %s]", got, taskA, taskB)
	}

	if waves[1].WaveID != "wave-1" {
		t.Fatalf("wave 1 ID = %q, want %q", waves[1].WaveID, "wave-1")
	}
	if got := waves[1].Tasks; len(got) != 1 || got[0] != taskC {
		t.Fatalf("wave-1 tasks = %v, want [%s]", got, taskC)
	}

	if waves[2].WaveID != "wave-2" {
		t.Fatalf("wave 2 ID = %q, want %q", waves[2].WaveID, "wave-2")
	}
	if got := waves[2].Tasks; len(got) != 1 || got[0] != taskD {
		t.Fatalf("wave-2 tasks = %v, want [%s]", got, taskD)
	}
}

func TestComputeWavesSerializesOverlappingOwnedPaths(t *testing.T) {
	const (
		taskA = "task-a"
		taskB = "task-b"
		taskC = "task-c"
	)
	tasks := []state.Task{
		{TaskID: taskA, Scope: state.TaskScope{OwnedPaths: []string{"internal/engine"}}},
		{TaskID: taskB, Scope: state.TaskScope{OwnedPaths: []string{"internal/engine/plan.go"}}},
		{TaskID: taskC, DependsOn: []string{taskA}},
	}

	waves := compiler.ComputeWaves(tasks)

	if len(waves) != 2 {
		t.Fatalf("wave count = %d, want 2", len(waves))
	}
	if got := waves[0].Tasks; len(got) != 1 || got[0] != taskA {
		t.Fatalf("wave-0 tasks = %v, want [%s]", got, taskA)
	}
	if got := waves[1].Tasks; len(got) != 2 || got[0] != taskB || got[1] != taskC {
		t.Fatalf("wave-1 tasks = %v, want [%s %s]", got, taskB, taskC)
	}
}

func TestComputeWavesMovesHighestConflictDegreeTaskFirst(t *testing.T) {
	tasks := []state.Task{
		{TaskID: "task-a", Scope: state.TaskScope{OwnedPaths: []string{"internal/engine"}}},
		{TaskID: "task-b", Scope: state.TaskScope{OwnedPaths: []string{"internal/engine/plan.go"}}},
		{TaskID: "task-c", Scope: state.TaskScope{OwnedPaths: []string{"internal/engine/explore.go"}}},
		{TaskID: "task-d", Scope: state.TaskScope{OwnedPaths: []string{"internal/engine/engine.go"}}},
	}

	waves := compiler.ComputeWaves(tasks)

	if len(waves) != 2 {
		t.Fatalf("wave count = %d, want 2", len(waves))
	}
	if got := waves[0].Tasks; !reflect.DeepEqual(got, []string{"task-b", "task-c", "task-d"}) {
		t.Fatalf("wave-0 tasks = %v, want task-b/task-c/task-d", got)
	}
	if got := waves[1].Tasks; !reflect.DeepEqual(got, []string{"task-a"}) {
		t.Fatalf("wave-1 tasks = %v, want task-a", got)
	}
}

func TestComputeWavesPrefersFilesLikelyTouchedOverOwnedPaths(t *testing.T) {
	const (
		taskA = "task-a"
		taskB = "task-b"
	)
	tasks := []state.Task{
		{
			TaskID:             taskA,
			FilesLikelyTouched: []string{"internal/engine/plan.go"},
			Scope:              state.TaskScope{OwnedPaths: []string{"internal/engine"}},
		},
		{
			TaskID:             taskB,
			FilesLikelyTouched: []string{"internal/engine/engine.go"},
			Scope:              state.TaskScope{OwnedPaths: []string{"internal/engine"}},
		},
	}

	waves := compiler.ComputeWaves(tasks)

	if len(waves) != 1 {
		t.Fatalf("wave count = %d, want 1", len(waves))
	}
	if got := waves[0].Tasks; len(got) != 2 || got[0] != taskA || got[1] != taskB {
		t.Fatalf("wave-0 tasks = %v, want [%s %s]", got, taskA, taskB)
	}
}

func TestComputeWavesCascadesDependentsForward(t *testing.T) {
	tasks := []state.Task{
		{TaskID: "task-a", Scope: state.TaskScope{OwnedPaths: []string{"internal/compiler"}}},
		{TaskID: "task-b", Scope: state.TaskScope{OwnedPaths: []string{"internal/compiler/compiler.go"}}},
		{TaskID: "task-c", DependsOn: []string{"task-b"}},
		{TaskID: "task-d", DependsOn: []string{"task-c"}},
	}

	waves := compiler.ComputeWaves(tasks)

	if len(waves) != 4 {
		t.Fatalf("wave count = %d, want 4", len(waves))
	}
	for i, want := range []string{"task-a", "task-b", "task-c", "task-d"} {
		if got := waves[i].Tasks; len(got) != 1 || got[0] != want {
			t.Fatalf("wave-%d tasks = %v, want [%s]", i, got, want)
		}
	}
}

func TestFileConflictJSONRoundTrip(t *testing.T) {
	want := state.FileConflict{
		FilePath: "internal/compiler/compiler.go",
		TaskA:    "task-a",
		TaskB:    "task-b",
	}

	data, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got state.FileConflict
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got != want {
		t.Fatalf("round trip = %#v, want %#v", got, want)
	}
}

func TestPruneDependenciesDropsLaneOnlyOrderingDeps(t *testing.T) {
	tasks := []state.Task{
		{TaskID: "task-a", RequirementIDs: []string{"AT-FR-001"}, Scope: state.TaskScope{OwnedPaths: []string{"cmd/fabrikk"}}},
		{TaskID: "task-b", RequirementIDs: []string{"AT-FR-002"}, DependsOn: []string{"task-a"}, Scope: state.TaskScope{OwnedPaths: []string{"internal/councilflow"}}},
		{TaskID: "task-c", RequirementIDs: []string{"AT-FR-003"}, DependsOn: []string{"task-b"}, Scope: state.TaskScope{OwnedPaths: []string{"internal/councilflow/review"}}},
		{TaskID: "task-d", RequirementIDs: []string{"AT-FR-002"}, DependsOn: []string{"task-b"}, Scope: state.TaskScope{OwnedPaths: []string{"internal/engine"}}},
		{TaskID: "task-e", RequirementIDs: []string{"AT-FR-005"}, DependsOn: []string{"task-a"}, Scope: state.TaskScope{OwnedPaths: []string{"internal/engine"}, ReadOnlyPaths: []string{"cmd/fabrikk/status.go"}}},
		{TaskID: "task-f", RequirementIDs: []string{"AT-FR-006"}, DependsOn: []string{"task-missing"}, Scope: state.TaskScope{OwnedPaths: []string{"internal/verifier"}}},
	}

	pruned := compiler.PruneDependencies(tasks)

	if len(pruned) != len(tasks) {
		t.Fatalf("task count = %d, want %d", len(pruned), len(tasks))
	}

	for i, want := range []string{"task-a", "task-b", "task-c", "task-d", "task-e", "task-f"} {
		if got := pruned[i].TaskID; got != want {
			t.Fatalf("task %d ID = %q, want %q", i, got, want)
		}
	}

	if got := pruned[1].DependsOn; len(got) != 0 {
		t.Fatalf("task-b DependsOn = %v, want none", got)
	}
	if got := pruned[2].DependsOn; len(got) != 1 || got[0] != "task-b" {
		t.Fatalf("task-c DependsOn = %v, want [task-b]", got)
	}
	if got := pruned[3].DependsOn; len(got) != 1 || got[0] != "task-b" {
		t.Fatalf("task-d DependsOn = %v, want [task-b]", got)
	}
	if got := pruned[4].DependsOn; len(got) != 1 || got[0] != "task-a" {
		t.Fatalf("task-e DependsOn = %v, want [task-a]", got)
	}
	if got := pruned[5].DependsOn; len(got) != 1 || got[0] != "task-missing" {
		t.Fatalf("task-f DependsOn = %v, want [task-missing]", got)
	}
}

func TestCompileExecutionPlanPropagatesImplementationDetail(t *testing.T) {
	artifact := &state.RunArtifact{
		RunID:        "run-123",
		Requirements: []state.Requirement{{ID: "AT-FR-001", Text: "Implement requirement"}},
		QualityGate:  &state.QualityGate{Command: "make check", Required: true},
	}
	plan := &state.ExecutionPlan{
		Slices: []state.ExecutionSlice{{
			SliceID:            "slice-1",
			Title:              "Implement slice",
			Goal:               "Propagate detail",
			RequirementIDs:     []string{"AT-FR-001"},
			FilesLikelyTouched: []string{"internal/compiler/compiler.go", "internal/compiler/compiler_test.go"},
			OwnedPaths:         []string{"internal/compiler"},
			Risk:               "medium",
			Size:               "S",
			ImplementationDetail: state.ImplementationDetail{
				FilesToModify: []state.FileChange{
					{Path: "internal/compiler/compiler.go", Change: "Add propagation logic"},
					{Path: "internal/compiler/compiler_new_test.go", Change: "Add regression tests", IsNew: true},
				},
				SymbolsToAdd: []string{"func cloneImplementationDetail(detail state.ImplementationDetail) state.ImplementationDetail"},
				SymbolsToUse: []string{"scopeForSlice at compiler.go:556"},
				TestsToAdd:   []string{"TestCompileExecutionPlanPropagatesImplementationDetail"},
			},
		}},
	}

	result, err := compiler.CompileExecutionPlan(artifact, plan)
	if err != nil {
		t.Fatalf("CompileExecutionPlan: %v", err)
	}
	if len(result.Tasks) != 1 {
		t.Fatalf("task count = %d, want 1", len(result.Tasks))
	}

	task := result.Tasks[0]
	assertImplementationDetail(t, task.ImplementationDetail, state.ImplementationDetail{
		FilesToModify: []state.FileChange{
			{Path: "internal/compiler/compiler.go", Change: "Add propagation logic"},
			{Path: "internal/compiler/compiler_new_test.go", Change: "Add regression tests", IsNew: true},
		},
		SymbolsToAdd: []string{"func cloneImplementationDetail(detail state.ImplementationDetail) state.ImplementationDetail"},
		SymbolsToUse: []string{"scopeForSlice at compiler.go:556"},
		TestsToAdd:   []string{"TestCompileExecutionPlanPropagatesImplementationDetail"},
	})
	assertValidationChecks(t, task.ValidationChecks, []state.ValidationCheck{
		{Type: "files_exist", Paths: []string{"internal/compiler/compiler_new_test.go"}},
		{Type: "content_check", File: "internal/compiler/compiler.go", Pattern: `func cloneImplementationDetail\(detail state\.ImplementationDetail\) state\.ImplementationDetail`},
		{Type: "tests", Tool: "go", Args: []string{"test", "-run", "^TestCompileExecutionPlanPropagatesImplementationDetail$", "./internal/compiler/..."}},
		{Type: "command", Tool: "make", Args: []string{"check"}},
	})

	plan.Slices[0].ImplementationDetail.SymbolsToAdd[0] = "mutated after compile"
	if task.ImplementationDetail.SymbolsToAdd[0] != "func cloneImplementationDetail(detail state.ImplementationDetail) state.ImplementationDetail" {
		t.Fatal("compiled task shares implementation detail backing storage with plan slice")
	}
}

func assertImplementationDetail(t *testing.T, got, want state.ImplementationDetail) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ImplementationDetail = %#v, want %#v", got, want)
	}
}

func assertValidationChecks(t *testing.T, got, want []state.ValidationCheck) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ValidationChecks = %#v, want %#v", got, want)
	}
}

func TestDeriveValidationChecksRejectsNonAllowlistedTool(t *testing.T) {
	artifact := &state.RunArtifact{QualityGate: &state.QualityGate{Command: "ruby verify.rb", Required: true}}
	checks := compiler.DeriveValidationChecks(artifact, &state.ExecutionSlice{SliceID: "slice-1"})
	if len(checks) != 0 {
		t.Fatalf("check count = %d, want 0", len(checks))
	}
}

func TestCompileExecutionPlanPreservesReviewedValidationChecksAndWaveBarriers(t *testing.T) {
	artifact := &state.RunArtifact{
		RunID:        "run-wave-barrier",
		Requirements: []state.Requirement{{ID: "AT-FR-001", Text: "First"}, {ID: "AT-FR-002", Text: "Second"}},
		QualityGate:  &state.QualityGate{Command: "make check", Required: true},
	}
	plan := &state.ExecutionPlan{
		Slices: []state.ExecutionSlice{
			{
				SliceID:          "slice-1",
				Title:            "Slice one",
				Goal:             "First wave",
				RequirementIDs:   []string{"AT-FR-001"},
				WaveID:           "wave-0",
				OwnedPaths:       []string{"internal/compiler"},
				ValidationChecks: []state.ValidationCheck{{Type: "files_exist", Paths: []string{"internal/compiler/compiler.go"}}},
				Risk:             "low",
				Size:             "S",
			},
			{
				SliceID:          "slice-2",
				Title:            "Slice two",
				Goal:             "Second wave",
				RequirementIDs:   []string{"AT-FR-002"},
				WaveID:           "wave-1",
				OwnedPaths:       []string{"internal/engine"},
				ValidationChecks: []state.ValidationCheck{{Type: "content_check", File: "internal/engine/plan.go", Pattern: "DraftExecutionPlan"}},
				Risk:             "low",
				Size:             "S",
			},
		},
	}

	result, err := compiler.CompileExecutionPlan(artifact, plan)
	if err != nil {
		t.Fatalf("CompileExecutionPlan: %v", err)
	}
	if len(result.Tasks) != 2 {
		t.Fatalf("task count = %d, want 2", len(result.Tasks))
	}
	if got := result.Tasks[1].DependsOn; len(got) != 1 || got[0] != result.Tasks[0].TaskID {
		t.Fatalf("wave barrier DependsOn = %v, want [%s]", got, result.Tasks[0].TaskID)
	}
	if !reflect.DeepEqual(result.Tasks[0].ValidationChecks, plan.Slices[0].ValidationChecks) {
		t.Fatalf("task 0 ValidationChecks = %#v, want %#v", result.Tasks[0].ValidationChecks, plan.Slices[0].ValidationChecks)
	}
	if !reflect.DeepEqual(result.Tasks[1].ValidationChecks, plan.Slices[1].ValidationChecks) {
		t.Fatalf("task 1 ValidationChecks = %#v, want %#v", result.Tasks[1].ValidationChecks, plan.Slices[1].ValidationChecks)
	}
}

func TestCompileExecutionPlanPreservesWaveBarriersWhenSlicesOutOfOrder(t *testing.T) {
	artifact := &state.RunArtifact{
		RunID:        "run-wave-barrier-out-of-order",
		Requirements: []state.Requirement{{ID: "AT-FR-001", Text: "First"}, {ID: "AT-FR-002", Text: "Second"}},
	}
	plan := &state.ExecutionPlan{
		Slices: []state.ExecutionSlice{
			{
				SliceID:        "slice-2",
				Title:          "Slice two",
				Goal:           "Second wave",
				RequirementIDs: []string{"AT-FR-002"},
				WaveID:         "wave-1",
				OwnedPaths:     []string{"internal/engine"},
				Risk:           "low",
				Size:           "S",
			},
			{
				SliceID:        "slice-1",
				Title:          "Slice one",
				Goal:           "First wave",
				RequirementIDs: []string{"AT-FR-001"},
				WaveID:         "wave-0",
				OwnedPaths:     []string{"internal/compiler"},
				Risk:           "low",
				Size:           "S",
			},
		},
	}

	result, err := compiler.CompileExecutionPlan(artifact, plan)
	if err != nil {
		t.Fatalf("CompileExecutionPlan: %v", err)
	}
	tasksByID := make(map[string]state.Task, len(result.Tasks))
	for _, task := range result.Tasks {
		tasksByID[task.TaskID] = task
	}
	waveOneTask := tasksByID["task-slice-2"]
	waveZeroTask := tasksByID["task-slice-1"]
	if !reflect.DeepEqual(waveOneTask.DependsOn, []string{waveZeroTask.TaskID}) {
		t.Fatalf("wave-1 DependsOn = %v, want [%s]", waveOneTask.DependsOn, waveZeroTask.TaskID)
	}
	if len(waveZeroTask.DependsOn) != 0 {
		t.Fatalf("wave-0 DependsOn = %v, want none", waveZeroTask.DependsOn)
	}
}

func TestDeriveValidationChecksRejectsShellFormQualityGate(t *testing.T) {
	artifact := &state.RunArtifact{QualityGate: &state.QualityGate{Command: `make check && go test ./...`, Required: true}}
	checks := compiler.DeriveValidationChecks(artifact, &state.ExecutionSlice{SliceID: "slice-1"})
	if len(checks) != 0 {
		t.Fatalf("check count = %d, want 0 for shell-form command", len(checks))
	}
}
