package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/php-workx/fabrikk/internal/agentcli"
	"github.com/php-workx/fabrikk/internal/compiler"
	"github.com/php-workx/fabrikk/internal/engine"
	"github.com/php-workx/fabrikk/internal/learning"
	"github.com/php-workx/fabrikk/internal/state"
	"github.com/php-workx/fabrikk/internal/ticket"
)

const emptyExplorationJSON = `{"file_inventory":[],"symbols":[],"test_files":[],"reuse_points":[]}`

func TestParseReviewFlagsStaggerDelay(t *testing.T) {
	rf, err := parseReviewFlags([]string{"--council", "--stagger-delay", "0"})
	if err != nil {
		t.Fatalf("parseReviewFlags: %v", err)
	}
	if !rf.council {
		t.Fatal("council = false, want true")
	}
	if rf.staggerDelay != 0 {
		t.Fatalf("staggerDelay = %d, want 0", rf.staggerDelay)
	}

	if _, err := parseReviewFlags([]string{"--stagger-delay", "-1"}); err == nil {
		t.Fatal("parseReviewFlags negative delay error = nil, want error")
	}
	if _, err := parseReviewFlags([]string{"--stagger-delay"}); err == nil {
		t.Fatal("parseReviewFlags missing delay error = nil, want error")
	}
	if _, err := parseReviewFlags([]string{"--round", "--mode"}); err == nil {
		t.Fatal("parseReviewFlags missing round error = nil, want error")
	}
	if _, err := parseReviewFlags([]string{"--round", "abc"}); err == nil {
		t.Fatal("parseReviewFlags invalid round error = nil, want error")
	}
	if _, err := parseReviewFlags([]string{"--mode", "--council"}); err == nil {
		t.Fatal("parseReviewFlags missing mode error = nil, want error")
	}
}

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
					Path:        "specs/fabrikk.md",
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
	assertContains(t, output, "Next: fabrikk approve run-review")
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
		if err := cmdTechSpec(context.Background(), []string{"review", "run-tech", "--structural-only"}); err != nil {
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
		if err := cmdTechSpec(context.Background(), []string{"review", "run-tech-fail", "--structural-only"}); err != nil {
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

func TestCmdPlanReviewWithCouncil(t *testing.T) {
	baseDir := t.TempDir()
	withWorkingDir(t, baseDir)

	writeRunFixture(t, baseDir, "run-plan-council", runFixture{
		artifact: &state.RunArtifact{
			SchemaVersion: "0.1",
			RunID:         "run-plan-council",
			Requirements: []state.Requirement{{
				ID:         "AT-FR-001",
				Text:       "The system must ingest specs.",
				SourceSpec: "spec.md",
				SourceLine: 10,
			}},
		},
	})
	runDir := state.NewRunDir(baseDir, "run-plan-council")
	techSpec := []byte(`# Technical Spec

## 1. Technical context
A

## 2. Architecture
B

## 3. Canonical artifacts and schemas
C

## 4. Interfaces
D

## 5. Verification
E

## 6. Requirement traceability
F

## 7. Open questions and risks
G

## 8. Approval
H
`)
	if err := runDir.WriteTechnicalSpec(techSpec); err != nil {
		t.Fatalf("WriteTechnicalSpec: %v", err)
	}
	if err := runDir.WriteTechnicalSpecApproval(&state.ArtifactApproval{
		SchemaVersion: "0.1",
		RunID:         "run-plan-council",
		ArtifactType:  "technical_spec_approval",
		ArtifactPath:  "technical-spec.md",
		ArtifactHash:  "sha256:" + state.SHA256Bytes(techSpec),
		Status:        state.ArtifactApproved,
		ApprovedBy:    "tester",
		ApprovedAt:    time.Date(2026, time.April, 11, 9, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("WriteTechnicalSpecApproval: %v", err)
	}

	origInvoke := agentcli.InvokeFunc
	defer func() { agentcli.InvokeFunc = origInvoke }()
	agentcli.InvokeFunc = func(_ context.Context, _ *agentcli.CLIBackend, prompt string, _ int) (string, error) {
		switch {
		case strings.Contains(prompt, "codebase exploration report"):
			return emptyExplorationJSON, nil
		case strings.Contains(prompt, "Plan Scope Reviewer"):
			return `{"persona_id":"plan-scope","round":1,"verdict":"pass","confidence":"HIGH","key_insight":"ok","findings":[],"recommendation":"ok","schema_version":3}`, nil
		case strings.Contains(prompt, "Plan Dependency Reviewer"):
			return `{"persona_id":"plan-dependency","round":1,"verdict":"pass","confidence":"HIGH","key_insight":"ok","findings":[],"recommendation":"ok","schema_version":3}`, nil
		case strings.Contains(prompt, "Plan Feasibility Reviewer"):
			return `{"persona_id":"plan-feasibility","round":1,"verdict":"pass","confidence":"HIGH","key_insight":"ok","findings":[],"recommendation":"ok","schema_version":3}`, nil
		case strings.Contains(prompt, "Plan Completeness Reviewer"):
			return `{"persona_id":"plan-completeness","round":1,"verdict":"pass","confidence":"HIGH","key_insight":"ok","findings":[],"recommendation":"ok","schema_version":3}`, nil
		default:
			return emptyExplorationJSON, nil
		}
	}

	if err := cmdPlan(context.Background(), []string{"draft", "run-plan-council"}); err != nil {
		t.Fatalf("cmdPlan draft: %v", err)
	}
	output := captureStdout(t, func() {
		if err := cmdPlan(context.Background(), []string{"review", "run-plan-council", "--council", "--stagger-delay", "0"}); err != nil {
			t.Fatalf("cmdPlan review --council: %v", err)
		}
	})
	assertContains(t, output, `"status": "pass"`)
	assertContains(t, output, "Council verdict: pass")
}

func TestCmdPlanReviewWithCouncilReturnsErrorOnCouncilFailure(t *testing.T) {
	baseDir := t.TempDir()
	withWorkingDir(t, baseDir)

	writeRunFixture(t, baseDir, "run-plan-council-fail", runFixture{
		artifact: &state.RunArtifact{
			SchemaVersion: "0.1",
			RunID:         "run-plan-council-fail",
			Requirements: []state.Requirement{{
				ID:         "AT-FR-001",
				Text:       "The system must ingest specs.",
				SourceSpec: "spec.md",
				SourceLine: 10,
			}},
		},
	})
	runDir := state.NewRunDir(baseDir, "run-plan-council-fail")
	techSpec := []byte(`# Technical Spec

## 1. Technical context
A

## 2. Architecture
B

## 3. Canonical artifacts and schemas
C

## 4. Interfaces
D

## 5. Verification
E

## 6. Requirement traceability
F

## 7. Open questions and risks
G

## 8. Approval
H
`)
	if err := runDir.WriteTechnicalSpec(techSpec); err != nil {
		t.Fatalf("WriteTechnicalSpec: %v", err)
	}
	if err := runDir.WriteTechnicalSpecApproval(&state.ArtifactApproval{
		SchemaVersion: "0.1",
		RunID:         "run-plan-council-fail",
		ArtifactType:  "technical_spec_approval",
		ArtifactPath:  "technical-spec.md",
		ArtifactHash:  "sha256:" + state.SHA256Bytes(techSpec),
		Status:        state.ArtifactApproved,
		ApprovedBy:    "tester",
		ApprovedAt:    time.Date(2026, time.April, 11, 9, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("WriteTechnicalSpecApproval: %v", err)
	}

	origInvoke := agentcli.InvokeFunc
	defer func() { agentcli.InvokeFunc = origInvoke }()
	agentcli.InvokeFunc = func(_ context.Context, _ *agentcli.CLIBackend, prompt string, _ int) (string, error) {
		switch {
		case strings.Contains(prompt, "codebase exploration report"):
			return emptyExplorationJSON, nil
		case strings.Contains(prompt, "Plan Scope Reviewer"):
			return `{"persona_id":"plan-scope","round":1,"verdict":"fail","confidence":"HIGH","key_insight":"bad","findings":[{"finding_id":"scope-001","severity":"significant","category":"missing_scope","section":"execution","location":"slice-001","description":"missing scope detail","recommendation":"add scope detail"}],"recommendation":"fix","schema_version":3}`, nil
		case strings.Contains(prompt, "Plan Dependency Reviewer"):
			return `{"persona_id":"plan-dependency","round":1,"verdict":"pass","confidence":"HIGH","key_insight":"ok","findings":[],"recommendation":"ok","schema_version":3}`, nil
		case strings.Contains(prompt, "Plan Feasibility Reviewer"):
			return `{"persona_id":"plan-feasibility","round":1,"verdict":"pass","confidence":"HIGH","key_insight":"ok","findings":[],"recommendation":"ok","schema_version":3}`, nil
		case strings.Contains(prompt, "Plan Completeness Reviewer"):
			return `{"persona_id":"plan-completeness","round":1,"verdict":"pass","confidence":"HIGH","key_insight":"ok","findings":[],"recommendation":"ok","schema_version":3}`, nil
		default:
			return emptyExplorationJSON, nil
		}
	}

	if err := cmdPlan(context.Background(), []string{"draft", "run-plan-council-fail"}); err != nil {
		t.Fatalf("cmdPlan draft: %v", err)
	}
	var reviewErr error
	output := captureStdout(t, func() {
		reviewErr = cmdPlan(context.Background(), []string{"review", "run-plan-council-fail", "--council", "--stagger-delay", "0"})
	})
	if reviewErr == nil {
		t.Fatal("cmdPlan review --council error = nil, want council failure")
	}
	assertContains(t, output, "Council verdict: fail")
	assertContains(t, output, `"status": "fail"`)
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

func TestCmdBoundariesSetPersistsDirectValues(t *testing.T) {
	baseDir := t.TempDir()
	withWorkingDir(t, baseDir)

	writeRunFixture(t, baseDir, "run-boundaries-direct", runFixture{
		artifact: &state.RunArtifact{
			SchemaVersion: "0.1",
			RunID:         "run-boundaries-direct",
			Requirements:  []state.Requirement{{ID: "AT-FR-001", Text: "The system must ingest specs."}},
			QualityGate:   &state.QualityGate{Command: "just check", TimeoutSeconds: 60, Required: true},
		},
	})

	if err := cmdBoundaries([]string{
		"set",
		"run-boundaries-direct",
		"--always", " keep tests focused ",
		"--always", "",
		"--ask-first", " confirm rollout window ",
		"--never", " docs/ ",
	}); err != nil {
		t.Fatalf("cmdBoundaries set: %v", err)
	}

	runDir := state.NewRunDir(baseDir, "run-boundaries-direct")
	artifact, err := runDir.ReadArtifact()
	if err != nil {
		t.Fatalf("ReadArtifact: %v", err)
	}

	if artifact.QualityGate == nil || artifact.QualityGate.Command != "just check" {
		t.Fatalf("QualityGate = %+v, want preserved", artifact.QualityGate)
	}
	if got, want := artifact.Boundaries.Always, []string{"keep tests focused"}; !equalStrings(got, want) {
		t.Fatalf("Boundaries.Always = %v, want %v", got, want)
	}
	if got, want := artifact.Boundaries.AskFirst, []string{"confirm rollout window"}; !equalStrings(got, want) {
		t.Fatalf("Boundaries.AskFirst = %v, want %v", got, want)
	}
	if got, want := artifact.Boundaries.Never, []string{"docs/"}; !equalStrings(got, want) {
		t.Fatalf("Boundaries.Never = %v, want %v", got, want)
	}
}

func TestCmdBoundariesSetParsesTechSpecSection(t *testing.T) {
	baseDir := t.TempDir()
	withWorkingDir(t, baseDir)

	writeRunFixture(t, baseDir, "run-boundaries-spec", runFixture{
		artifact: &state.RunArtifact{
			SchemaVersion: "0.1",
			RunID:         "run-boundaries-spec",
			Requirements:  []state.Requirement{{ID: "AT-FR-001", Text: "The system must ingest specs."}},
		},
	})

	runDir := state.NewRunDir(baseDir, "run-boundaries-spec")
	spec := []byte(`# Example Technical Specification

## 1. Technical context
Context

## Boundaries

**Always:**
- keep tests focused
-   
- preserve audit trail

**Ask First:**
- confirm rollout window

**Never:**
- docs/
- generated/
`)
	if err := runDir.WriteTechnicalSpec(spec); err != nil {
		t.Fatalf("WriteTechnicalSpec: %v", err)
	}

	if err := cmdBoundaries([]string{"set", "run-boundaries-spec", "--from-tech-spec"}); err != nil {
		t.Fatalf("cmdBoundaries set --from-tech-spec: %v", err)
	}

	artifact, err := runDir.ReadArtifact()
	if err != nil {
		t.Fatalf("ReadArtifact: %v", err)
	}

	if got, want := artifact.Boundaries.Always, []string{"keep tests focused", "preserve audit trail"}; !equalStrings(got, want) {
		t.Fatalf("Boundaries.Always = %v, want %v", got, want)
	}
	if got, want := artifact.Boundaries.AskFirst, []string{"confirm rollout window"}; !equalStrings(got, want) {
		t.Fatalf("Boundaries.AskFirst = %v, want %v", got, want)
	}
	if got, want := artifact.Boundaries.Never, []string{"docs/", "generated/"}; !equalStrings(got, want) {
		t.Fatalf("Boundaries.Never = %v, want %v", got, want)
	}
}

func equalStrings(got, want []string) bool {
	return slices.Equal(got, want)
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

	var verifyErr error
	output := captureStdout(t, func() {
		verifyErr = cmdVerify(context.Background(), []string{"run-verify", task.TaskID})
	})
	if verifyErr == nil {
		t.Fatal("cmdVerify error = nil, want verification failure")
	}

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

	verifyStore := ticket.NewStore(filepath.Join(baseDir, ".tickets"))
	tasks, err := verifyStore.ReadTasks("run-report")
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
	assertContains(t, output, "Next: fabrikk review run-")

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

	approveStore := ticket.NewStore(filepath.Join(baseDir, ".tickets"))
	tasks, err := approveStore.ReadTasks(artifact.RunID)
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

	retryStore := ticket.NewStore(filepath.Join(baseDir, ".tickets"))
	tasks, err := retryStore.ReadTasks("run-retry")
	if err != nil {
		t.Fatalf("ReadTasks(before retry): %v", err)
	}
	tasks[0].StatusReason = "missing evidence"
	if err := retryStore.WriteTasks("run-retry", tasks); err != nil {
		t.Fatalf("WriteTasks(before retry): %v", err)
	}

	output := captureStdout(t, func() {
		if err := cmdRetry([]string{"run-retry", "task-blocked"}); err != nil {
			t.Fatalf("cmdRetry: %v", err)
		}
	})
	assertContains(t, output, "Task requeued: task-blocked")

	tasks, err = retryStore.ReadTasks("run-retry")
	if err != nil {
		t.Fatalf("ReadTasks(after retry): %v", err)
	}
	if len(tasks) != 1 || tasks[0].Status != state.TaskPending || tasks[0].StatusReason != "" {
		t.Fatalf("task after retry = %+v, want pending with cleared reason", tasks)
	}

	retryRunDir := state.NewRunDir(baseDir, "run-retry")
	coverage, err := retryRunDir.ReadCoverage()
	if err != nil {
		t.Fatalf("ReadCoverage(after retry): %v", err)
	}
	if len(coverage) != 1 || coverage[0].Status != "in_progress" {
		t.Fatalf("coverage after retry = %+v, want in_progress", coverage)
	}

	status, err := retryRunDir.ReadStatus()
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
		// Write to ticket store (sole task backend).
		ticketStore := ticket.NewStore(filepath.Join(baseDir, ".tickets"))
		if err := ticketStore.WriteTasks(runID, fixture.tasks); err != nil {
			t.Fatalf("WriteTasks (ticket store): %v", err)
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

func TestCmdLearnAddAndQuery(t *testing.T) {
	baseDir := t.TempDir()
	withWorkingDir(t, baseDir)

	// Add a learning.
	output := captureStdout(t, func() {
		if err := cmdLearn([]string{
			"Use withLock for atomic read-check-write",
			"--tag", "concurrency,flock",
			"--category", "pattern",
			"--path", "internal/ticket",
		}); err != nil {
			t.Fatalf("cmdLearn add: %v", err)
		}
	})
	assertContains(t, output, "Learning added:")
	assertContains(t, output, "pattern")

	// Query by tag.
	output = captureStdout(t, func() {
		if err := cmdLearn([]string{"query", "--tag", "concurrency"}); err != nil {
			t.Fatalf("cmdLearn query: %v", err)
		}
	})
	assertContains(t, output, "withLock")

	// List all.
	output = captureStdout(t, func() {
		if err := cmdLearn([]string{"list"}); err != nil {
			t.Fatalf("cmdLearn list: %v", err)
		}
	})
	assertContains(t, output, "All learnings (1)")
}

func TestCmdLearnHandoff(t *testing.T) {
	baseDir := t.TempDir()
	withWorkingDir(t, baseDir)

	output := captureStdout(t, func() {
		if err := cmdLearn([]string{
			"handoff",
			"--summary", "Implemented learning store, tests passing",
			"--next", "Wire into engine",
			"--run", "run-1234",
		}); err != nil {
			t.Fatalf("cmdLearn handoff: %v", err)
		}
	})
	assertContains(t, output, "Handoff saved:")
	assertContains(t, output, "Wire into engine")
}

func TestCmdContext(t *testing.T) {
	baseDir := t.TempDir()
	withWorkingDir(t, baseDir)

	runID := "run-ctx-test"

	// Create a task via the ticket store.
	ticketStore := ticket.NewStore(filepath.Join(baseDir, ".tickets"))
	task := state.Task{
		TaskID:       "task-test-ctx",
		Title:        "Test context task",
		Tags:         []string{"testctx"},
		Status:       state.TaskPending,
		Scope:        state.TaskScope{OwnedPaths: []string{"internal/test"}},
		ParentTaskID: runID,
	}
	_ = ticketStore.WriteTasks(runID, []state.Task{task})

	// Create a learning store and add a learning with matching tags and paths.
	learnStore := learning.NewStore(filepath.Join(baseDir, ".fabrikk", "learnings"))
	_ = learnStore.Add(&learning.Learning{
		Tags:        []string{"testctx"},
		Category:    learning.CategoryPattern,
		Content:     "Context assembly test content",
		Summary:     "Context test learning summary",
		Confidence:  0.8,
		SourcePaths: []string{"internal/test"},
	})

	output := captureStdout(t, func() {
		if err := cmdContext([]string{runID, "task-test-ctx"}); err != nil {
			t.Fatalf("cmdContext: %v", err)
		}
	})

	assertContains(t, output, "Context for")
	assertContains(t, output, "Context test learning summary")
}

func TestCmdLearnMaintain(t *testing.T) {
	baseDir := t.TempDir()
	withWorkingDir(t, baseDir)

	output := captureStdout(t, func() {
		if err := cmdLearn([]string{"maintain"}); err != nil {
			t.Fatalf("cmdLearn maintain: %v", err)
		}
	})
	assertContains(t, output, "Learning store maintenance:")
	assertContains(t, output, "Auto-expired:")
}

func TestCmdLearnGC(t *testing.T) {
	baseDir := t.TempDir()
	withWorkingDir(t, baseDir)

	output := captureStdout(t, func() {
		if err := cmdLearn([]string{"gc"}); err != nil {
			t.Fatalf("cmdLearn gc: %v", err)
		}
	})
	assertContains(t, output, "Garbage collected: 0")
}

func TestCmdTechSpecDraftNoNormalize(t *testing.T) {
	baseDir := t.TempDir()
	runID := "run-no-norm-cli"

	runDir := state.NewRunDir(baseDir, runID)
	if err := runDir.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := runDir.WriteArtifact(&state.RunArtifact{
		SchemaVersion: "0.1",
		RunID:         runID,
	}); err != nil {
		t.Fatalf("WriteArtifact: %v", err)
	}

	specPath := filepath.Join(baseDir, "non-canonical.md")
	spec := "# My Custom Spec\n\n## Overview\nImportant content.\n"
	if err := os.WriteFile(specPath, []byte(spec), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	withWorkingDir(t, baseDir)

	output := captureStdout(t, func() {
		if err := cmdTechSpec(context.Background(), []string{"draft", runID, "--from", specPath, "--no-normalize"}); err != nil {
			t.Fatalf("cmdTechSpec draft --no-normalize: %v", err)
		}
	})
	assertContains(t, output, "Technical spec recorded")

	// Verify content passed through unchanged.
	data, err := runDir.ReadTechnicalSpec()
	if err != nil {
		t.Fatalf("ReadTechnicalSpec: %v", err)
	}
	if string(data) != spec {
		t.Fatalf("--no-normalize did not preserve content:\n  got:  %q\n  want: %q", string(data), spec)
	}
}
