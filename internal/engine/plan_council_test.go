package engine_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/php-workx/fabrikk/internal/agentcli"
	"github.com/php-workx/fabrikk/internal/councilflow"
	"github.com/php-workx/fabrikk/internal/engine"
	"github.com/php-workx/fabrikk/internal/state"
)

func setupExecutionPlanCouncilTest(t *testing.T) (*engine.Engine, *state.RunDir) {
	t.Helper()
	dir := t.TempDir()
	runDir := state.NewRunDir(dir, "run-plan-council")
	if err := runDir.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	artifact := &state.RunArtifact{
		RunID:        "run-plan-council",
		Requirements: []state.Requirement{{ID: "AT-FR-001", Text: "Plan review must be council-reviewed."}},
	}
	if err := runDir.WriteArtifact(artifact); err != nil {
		t.Fatalf("WriteArtifact: %v", err)
	}
	if err := runDir.WriteTechnicalSpec([]byte("# Technical Spec\n\n## 1. Architecture\nA plan exists.\n")); err != nil {
		t.Fatalf("WriteTechnicalSpec: %v", err)
	}
	plan := &state.ExecutionPlan{
		SchemaVersion:           "0.1",
		RunID:                   artifact.RunID,
		ArtifactType:            "execution_plan",
		SourceTechnicalSpecHash: "sha256:test",
		Status:                  state.ArtifactDrafted,
		Slices: []state.ExecutionSlice{{
			SliceID:          "slice-001",
			Title:            "Slice",
			Goal:             "Goal",
			RequirementIDs:   []string{"AT-FR-001"},
			WaveID:           "wave-0",
			OwnedPaths:       []string{"internal/engine/plan.go"},
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
	if err := runDir.WriteExecutionPlanMarkdown([]byte("# Execution Plan\n\n- slice-001\n")); err != nil {
		t.Fatalf("WriteExecutionPlanMarkdown: %v", err)
	}
	eng := engine.New(runDir, dir)
	if _, err := eng.ReviewExecutionPlan(context.Background()); err != nil {
		t.Fatalf("ReviewExecutionPlan: %v", err)
	}
	return eng, runDir
}

func councilReviewJSON(personaID string, verdict councilflow.Verdict, findings []councilflow.Finding) string {
	payload := councilflow.ReviewOutput{
		PersonaID:      personaID,
		Round:          1,
		Verdict:        verdict,
		Confidence:     councilflow.ConfidenceHigh,
		KeyInsight:     "test insight",
		Findings:       findings,
		Recommendation: "test recommendation",
		SchemaVersion:  3,
	}
	data, _ := json.Marshal(payload)
	return string(data)
}

func TestCouncilReviewExecutionPlanPassesWithCustomPersonas(t *testing.T) {
	eng, runDir := setupExecutionPlanCouncilTest(t)
	origInvoke := agentcli.InvokeFunc
	defer func() { agentcli.InvokeFunc = origInvoke }()

	personas := []councilflow.Persona{
		{PersonaID: "scope", DisplayName: "Scope Reviewer", Type: councilflow.PersonaCustom, Perspective: "scope", Instructions: "scope", Backend: agentcli.BackendClaude},
		{PersonaID: "dependency", DisplayName: "Dependency Reviewer", Type: councilflow.PersonaCustom, Perspective: "dependency", Instructions: "dependency", Backend: agentcli.BackendClaude},
	}
	agentcli.InvokeFunc = func(_ context.Context, _ *agentcli.CLIBackend, prompt string, _ int) (string, error) {
		switch {
		case strings.Contains(prompt, "Scope Reviewer"):
			return councilReviewJSON("scope", councilflow.VerdictPass, nil), nil
		case strings.Contains(prompt, "Dependency Reviewer"):
			return councilReviewJSON("dependency", councilflow.VerdictPass, nil), nil
		default:
			return "", nil
		}
	}

	result, err := eng.CouncilReviewExecutionPlan(context.Background(), councilflow.CouncilConfig{Rounds: 1, SkipJudge: true, SkipApproval: true, CustomPersonas: personas})
	if err != nil {
		t.Fatalf("CouncilReviewExecutionPlan: %v", err)
	}
	if result.OverallVerdict != councilflow.VerdictPass {
		t.Fatalf("OverallVerdict = %s, want pass", result.OverallVerdict)
	}
	stored, err := runDir.ReadExecutionPlanReview()
	if err != nil {
		t.Fatalf("ReadExecutionPlanReview: %v", err)
	}
	if stored.Status != state.ReviewPass {
		t.Fatalf("review status = %s, want pass", stored.Status)
	}
}

func TestCouncilReviewExecutionPlanFailsReviewOnBlockingFindings(t *testing.T) {
	eng, runDir := setupExecutionPlanCouncilTest(t)
	if err := os.WriteFile(runDir.Path("exploration.json"), []byte(`{"file_inventory":[]}`), 0o644); err != nil {
		t.Fatalf("WriteFile(exploration.json): %v", err)
	}
	origInvoke := agentcli.InvokeFunc
	defer func() { agentcli.InvokeFunc = origInvoke }()

	personas := []councilflow.Persona{{PersonaID: "scope", DisplayName: "Scope Reviewer", Type: councilflow.PersonaCustom, Perspective: "scope", Instructions: "scope", Backend: agentcli.BackendClaude}}
	agentcli.InvokeFunc = func(_ context.Context, _ *agentcli.CLIBackend, prompt string, _ int) (string, error) {
		if strings.Contains(prompt, "Scope Reviewer") {
			return councilReviewJSON("scope", councilflow.VerdictFail, []councilflow.Finding{{FindingID: "scope-001", Severity: "significant", Category: "missing_scope", Description: "missing scope detail", Recommendation: "add scope detail"}}), nil
		}
		return "", nil
	}

	result, err := eng.CouncilReviewExecutionPlan(context.Background(), councilflow.CouncilConfig{Rounds: 1, SkipJudge: true, SkipApproval: true, CustomPersonas: personas})
	if err != nil {
		t.Fatalf("CouncilReviewExecutionPlan: %v", err)
	}
	if result.OverallVerdict != councilflow.VerdictFail {
		t.Fatalf("OverallVerdict = %s, want fail", result.OverallVerdict)
	}
	stored, err := runDir.ReadExecutionPlanReview()
	if err != nil {
		t.Fatalf("ReadExecutionPlanReview: %v", err)
	}
	if stored.Status != state.ReviewFail {
		t.Fatalf("review status = %s, want fail", stored.Status)
	}
	if len(stored.BlockingFindings) == 0 || stored.BlockingFindings[len(stored.BlockingFindings)-1].Category != "missing_scope" {
		t.Fatalf("BlockingFindings = %+v, want council finding appended", stored.BlockingFindings)
	}
}

func TestCouncilReviewExecutionPlanWarnKeepsReviewPassing(t *testing.T) {
	eng, runDir := setupExecutionPlanCouncilTest(t)
	origInvoke := agentcli.InvokeFunc
	defer func() { agentcli.InvokeFunc = origInvoke }()

	personas := []councilflow.Persona{{PersonaID: "scope", DisplayName: "Scope Reviewer", Type: councilflow.PersonaCustom, Perspective: "scope", Instructions: "scope", Backend: agentcli.BackendClaude}}
	agentcli.InvokeFunc = func(_ context.Context, _ *agentcli.CLIBackend, prompt string, _ int) (string, error) {
		if strings.Contains(prompt, "Scope Reviewer") {
			return councilReviewJSON("scope", councilflow.VerdictWarn, []councilflow.Finding{{FindingID: "scope-002", Severity: "minor", Category: "scope_warning", Description: "consider splitting a slice", Recommendation: "split if it grows"}}), nil
		}
		return "", nil
	}

	result, err := eng.CouncilReviewExecutionPlan(context.Background(), councilflow.CouncilConfig{Rounds: 1, SkipJudge: true, SkipApproval: true, CustomPersonas: personas})
	if err != nil {
		t.Fatalf("CouncilReviewExecutionPlan: %v", err)
	}
	if result.OverallVerdict != councilflow.VerdictWarn {
		t.Fatalf("OverallVerdict = %s, want warn", result.OverallVerdict)
	}
	stored, err := runDir.ReadExecutionPlanReview()
	if err != nil {
		t.Fatalf("ReadExecutionPlanReview: %v", err)
	}
	if stored.Status != state.ReviewPass {
		t.Fatalf("review status = %s, want pass", stored.Status)
	}
	if len(stored.Warnings) == 0 || !strings.Contains(stored.Warnings[len(stored.Warnings)-1].Summary, "consider splitting a slice") {
		t.Fatalf("Warnings = %+v, want council warning appended", stored.Warnings)
	}
}
