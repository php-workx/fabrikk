package councilflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// stubInvoke returns a stub InvokeFn that maps persona/judge prompts to fixture responses.
func stubInvoke(responses map[string]string) InvokeFn {
	return func(_ context.Context, backend *CLIBackend, prompt string, _ int) (string, error) {
		for key, response := range responses {
			if strings.Contains(prompt, key) {
				return response, nil
			}
		}
		return "", fmt.Errorf("stub: no response for prompt containing backend=%s", backend.Command)
	}
}

func fixtureReviewJSON(personaID string, verdict Verdict, findings []Finding) string {
	review := ReviewOutput{
		PersonaID:      personaID,
		Round:          1,
		Verdict:        verdict,
		Confidence:     ConfidenceHigh,
		KeyInsight:     "test insight",
		Findings:       findings,
		Recommendation: "test recommendation",
		SchemaVersion:  3,
	}
	data, _ := json.MarshalIndent(review, "", "  ")
	return string(data)
}

func fixturePersonaJSON() string {
	personas := []Persona{{
		PersonaID:    "go-engineer",
		DisplayName:  "Go Engineer",
		Type:         PersonaDynamic,
		Perspective:  "Go expert",
		Instructions: "Review for Go patterns.\n\nDo NOT flag security issues.",
		Backend:      BackendClaude,
		Rationale:    "Spec targets Go",
	}}
	data, _ := json.MarshalIndent(personas, "", "  ")
	return string(data)
}

func fixtureJudgeJSON(edits []SpecEdit, rejections []Rejection) string {
	output := struct {
		Edits    []SpecEdit  `json:"edits"`
		Rejected []Rejection `json:"rejected"`
	}{Edits: edits, Rejected: rejections}
	data, _ := json.MarshalIndent(output, "", "  ")
	return string(data)
}

var testSpec = `# Test Spec

## 1. Technical context
Go CLI tool.

## 2. Architecture
Simple architecture with no error handling.

## 3. Canonical artifacts and schemas
JSON files.

## 4. Interfaces
CLI commands.

## 5. Verification
Unit tests only.

## 6. Requirement traceability
None defined.

## 7. Open questions and risks
No risks identified.

## 8. Approval
Pending.
`

func TestRunRoundWithStubBackend(t *testing.T) {
	old := InvokeFunc
	defer func() { InvokeFunc = old }()

	// Keys use "You are: **<name>**" prefix to avoid ambiguous matching — short
	// keys like "Architecture" match focus sections in other personas' prompts,
	// causing flaky results due to map iteration order.
	InvokeFunc = stubInvoke(map[string]string{
		"You are: **Security Engineer**": fixtureReviewJSON("security-engineer", VerdictWarn, []Finding{
			{FindingID: "sec-001", Severity: "significant", Description: "No error handling"},
		}),
		"You are: **Performance Engineer**": fixtureReviewJSON("performance-engineer", VerdictPass, nil),
		"You are: **Testability & Test Design Reviewer**": fixtureReviewJSON("testability-reviewer", VerdictWarn, []Finding{
			{FindingID: "test-001", Severity: "minor", Description: "Missing edge cases"},
		}),
		"You are: **Architecture Reviewer**": fixtureReviewJSON("architecture-reviewer", VerdictPass, nil),
	})

	dir := t.TempDir()
	runner := NewRunner(dir)
	runner.Force = true

	personas := FixedPersonas()
	result, err := runner.RunRound(context.Background(), testSpec, 1, personas, nil, "")
	if err != nil {
		t.Fatalf("RunRound: %v", err)
	}

	if len(result.Reviews) != 4 {
		t.Fatalf("got %d reviews, want 4", len(result.Reviews))
	}
	if result.Consensus != VerdictWarn {
		t.Errorf("consensus = %s, want WARN", result.Consensus)
	}

	// Verify review files were written.
	for _, p := range personas {
		path := filepath.Join(dir, fmt.Sprintf("review-%s.json", p.PersonaID))
		if _, err := os.Stat(path); err != nil {
			t.Errorf("missing review file: %s", path)
		}
	}
}

func TestRunRoundPartialFailure(t *testing.T) {
	old := InvokeFunc
	defer func() { InvokeFunc = old }()

	InvokeFunc = stubInvoke(map[string]string{
		"You are: **Security Engineer**": fixtureReviewJSON("security-engineer", VerdictFail, []Finding{
			{FindingID: "sec-001", Severity: "critical", Description: "Critical issue"},
		}),
		// Other personas get no response → fail
	})

	dir := t.TempDir()
	runner := NewRunner(dir)
	runner.Force = true

	result, err := runner.RunRound(context.Background(), testSpec, 1, FixedPersonas(), nil, "")
	if err != nil {
		t.Fatalf("RunRound should not fail on partial failures: %v", err)
	}
	if len(result.Reviews) != 1 {
		t.Fatalf("got %d reviews, want 1 (only security should succeed)", len(result.Reviews))
	}
	if result.Consensus != VerdictFail {
		t.Errorf("consensus = %s, want FAIL", result.Consensus)
	}
}

func TestRunRoundAllFailAborts(t *testing.T) {
	old := InvokeFunc
	defer func() { InvokeFunc = old }()

	InvokeFunc = func(_ context.Context, _ *CLIBackend, _ string, _ int) (string, error) {
		return "", fmt.Errorf("all backends down")
	}

	dir := t.TempDir()
	runner := NewRunner(dir)
	runner.Force = true

	result, err := runner.RunRound(context.Background(), testSpec, 1, FixedPersonas(), nil, "")
	if err != nil {
		t.Fatalf("RunRound should succeed even with all failures: %v", err)
	}
	if len(result.Reviews) != 0 {
		t.Fatalf("got %d reviews, want 0", len(result.Reviews))
	}
}

func TestRunCouncilEmptyReviewAborts(t *testing.T) {
	old := InvokeFunc
	defer func() { InvokeFunc = old }()

	InvokeFunc = func(_ context.Context, _ *CLIBackend, _ string, _ int) (string, error) {
		return "", fmt.Errorf("all backends down")
	}

	dir := t.TempDir()
	cfg := CouncilConfig{Rounds: 1, SkipDynPersonas: true, SkipApproval: true}
	_, err := RunCouncil(context.Background(), testSpec, dir, cfg)
	if err == nil {
		t.Fatal("RunCouncil should fail when all reviewers fail")
	}
	if !strings.Contains(err.Error(), "all reviewers failed") {
		t.Fatalf("error = %q, want 'all reviewers failed'", err)
	}
}

func TestRunJudgeAppliesEdits(t *testing.T) {
	old := InvokeFunc
	defer func() { InvokeFunc = old }()

	InvokeFunc = stubInvoke(map[string]string{
		"Judge": fixtureJudgeJSON(
			[]SpecEdit{{
				FindingID: "sec-001",
				PersonaID: "security-engineer",
				Action:    "Added error handling requirement",
				Section:   "## 2. Architecture",
				Find:      "Simple architecture with no error handling.",
				Replace:   "Simple architecture. All errors must be handled explicitly.",
			}},
			nil,
		),
	})

	dir := t.TempDir()
	reviews := []ReviewOutput{{
		PersonaID:  "security-engineer",
		Round:      1,
		Verdict:    VerdictWarn,
		Confidence: ConfidenceHigh,
		Findings: []Finding{{
			FindingID:   "sec-001",
			Severity:    "significant",
			Description: "No error handling",
		}},
	}}

	result, err := RunJudge(context.Background(), testSpec, 1, reviews, dir, DefaultJudgeConfig())
	if err != nil {
		t.Fatalf("RunJudge: %v", err)
	}
	if result.AppliedCount != 1 {
		t.Errorf("applied = %d, want 1", result.AppliedCount)
	}
	if !strings.Contains(result.UpdatedSpec, "All errors must be handled explicitly") {
		t.Error("edit was not applied to spec")
	}
	if strings.Contains(result.UpdatedSpec, "no error handling") {
		t.Error("old text was not replaced")
	}
}

func TestRunJudgeWithRejections(t *testing.T) {
	old := InvokeFunc
	defer func() { InvokeFunc = old }()

	InvokeFunc = stubInvoke(map[string]string{
		"Judge": fixtureJudgeJSON(
			nil,
			[]Rejection{{FindingID: "sec-002", PersonaID: "test", Severity: "minor", Description: "minor issue", RejectionReason: "not relevant for MVP"}},
		),
	})

	dir := t.TempDir()
	reviews := []ReviewOutput{{PersonaID: "test", Verdict: VerdictWarn, Findings: []Finding{{FindingID: "sec-002", Severity: "significant"}}}}
	result, err := RunJudge(context.Background(), testSpec, 1, reviews, dir, DefaultJudgeConfig())
	if err != nil {
		t.Fatalf("RunJudge: %v", err)
	}
	if result.RejectedCount != 1 {
		t.Errorf("rejected = %d, want 1", result.RejectedCount)
	}
}

func TestRunJudgeRejectsEmptyReplace(t *testing.T) {
	old := InvokeFunc
	defer func() { InvokeFunc = old }()

	InvokeFunc = stubInvoke(map[string]string{
		"Judge": fixtureJudgeJSON(
			[]SpecEdit{{FindingID: "sec-001", Find: "Simple architecture", Replace: ""}},
			nil,
		),
	})

	dir := t.TempDir()
	reviews := []ReviewOutput{{PersonaID: "test", Verdict: VerdictWarn, Findings: []Finding{{FindingID: "sec-001", Severity: "significant"}}}}

	result, err := RunJudge(context.Background(), testSpec, 1, reviews, dir, DefaultJudgeConfig())
	if err != nil {
		t.Fatalf("RunJudge: %v", err)
	}
	if result.AppliedCount != 0 {
		t.Errorf("applied = %d, want 0 (empty replace should be rejected)", result.AppliedCount)
	}
}

func TestFullPipelineWithStubs(t *testing.T) {
	old := InvokeFunc
	defer func() { InvokeFunc = old }()

	var callCount atomic.Int32
	InvokeFunc = func(_ context.Context, backend *CLIBackend, prompt string, _ int) (string, error) {
		callCount.Add(1)
		// Persona generation
		if strings.Contains(prompt, "Persona Generation") {
			return fixturePersonaJSON(), nil
		}
		// Judge
		if strings.Contains(prompt, "Judge") {
			return fixtureJudgeJSON(
				[]SpecEdit{{
					FindingID: "sec-001",
					PersonaID: "security-engineer",
					Action:    "fix",
					Section:   "## 2. Architecture",
					Find:      "Simple architecture with no error handling.",
					Replace:   "Architecture with proper error handling.",
				}},
				nil,
			), nil
		}
		// Reviewer
		personaID := "unknown"
		for _, p := range append(FixedPersonas(), Persona{PersonaID: "go-engineer", DisplayName: "Go Engineer"}) {
			if strings.Contains(prompt, p.DisplayName) {
				personaID = p.PersonaID
				break
			}
		}
		return fixtureReviewJSON(personaID, VerdictWarn, []Finding{
			{FindingID: personaID + "-001", Severity: "significant", Description: "Issue from " + personaID},
		}), nil
	}

	dir := t.TempDir()
	cfg := CouncilConfig{Rounds: 1, SkipApproval: true}
	result, err := RunCouncil(context.Background(), testSpec, dir, cfg)
	if err != nil {
		t.Fatalf("RunCouncil: %v", err)
	}

	if len(result.Rounds) != 1 {
		t.Fatalf("got %d rounds, want 1", len(result.Rounds))
	}
	if result.OverallVerdict != VerdictWarn {
		t.Errorf("verdict = %s, want WARN", result.OverallVerdict)
	}
	// Should have 4 fixed + dynamic personas worth of reviews.
	if len(result.Rounds[0].Reviews) < 4 {
		t.Errorf("got %d reviews, want at least 4", len(result.Rounds[0].Reviews))
	}
	// Judge should have been called and applied edits.
	if len(result.Consolidations) != 1 {
		t.Fatalf("got %d consolidations, want 1", len(result.Consolidations))
	}
	if result.Consolidations[0].AppliedCount < 1 {
		t.Errorf("applied = %d, want at least 1", result.Consolidations[0].AppliedCount)
	}
	// Final spec should reflect edits.
	if result.FinalSpec == "" {
		t.Error("FinalSpec is empty")
	}
	if !strings.Contains(result.FinalSpec, "proper error handling") {
		t.Error("final spec doesn't contain applied edit")
	}
	if result.FinalSpecPath == "" {
		t.Error("FinalSpecPath is empty")
	}
	// Verify the spec file was written.
	if _, err := os.Stat(result.FinalSpecPath); err != nil {
		t.Errorf("final spec file not found: %v", err)
	}

	t.Logf("Full pipeline: %d calls, %d reviews, %d applied, verdict=%s",
		callCount.Load(), len(result.Rounds[0].Reviews), result.Consolidations[0].AppliedCount, result.OverallVerdict)
}

func TestDuplicatePersonaIDRejected(t *testing.T) {
	dir := t.TempDir()
	runner := NewRunner(dir)
	personas := []Persona{
		{PersonaID: "dup", DisplayName: "A", Backend: BackendClaude},
		{PersonaID: "dup", DisplayName: "B", Backend: BackendCodex},
	}
	_, err := runner.RunRound(context.Background(), testSpec, 1, personas, nil, "")
	if err == nil {
		t.Fatal("should reject duplicate persona_id")
	}
	if !strings.Contains(err.Error(), "duplicate persona_id") {
		t.Fatalf("error = %q, want duplicate persona_id", err)
	}
}

func TestMVPModeFlowsThroughPipeline(t *testing.T) {
	old := InvokeFunc
	defer func() { InvokeFunc = old }()

	var mu sync.Mutex
	var capturedPrompts []string
	InvokeFunc = func(_ context.Context, _ *CLIBackend, prompt string, _ int) (string, error) {
		mu.Lock()
		capturedPrompts = append(capturedPrompts, prompt)
		mu.Unlock()
		if strings.Contains(prompt, "Persona Generation") {
			return fixturePersonaJSON(), nil
		}
		if strings.Contains(prompt, "Judge") {
			return fixtureJudgeJSON(nil, nil), nil
		}
		return fixtureReviewJSON("test", VerdictPass, nil), nil
	}

	dir := t.TempDir()
	cfg := CouncilConfig{Rounds: 1, Mode: ReviewMVP, SkipApproval: true}
	_, err := RunCouncil(context.Background(), testSpec, dir, cfg)
	if err != nil {
		t.Fatalf("RunCouncil: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	foundPersonaMVP := false
	foundReviewMVP := false
	for _, p := range capturedPrompts {
		if strings.Contains(p, "Persona Generation") && strings.Contains(p, "MVP") {
			foundPersonaMVP = true
		}
		if strings.Contains(p, "Review Assignment") && strings.Contains(p, "MVP") {
			foundReviewMVP = true
		}
	}
	if !foundPersonaMVP {
		t.Error("MVP directive not found in persona generation prompt")
	}
	if !foundReviewMVP {
		t.Error("MVP directive not found in reviewer prompts")
	}
}

func TestCacheInvalidatedOnSpecChange(t *testing.T) {
	old := InvokeFunc
	defer func() { InvokeFunc = old }()

	var callCount atomic.Int32
	InvokeFunc = stubInvoke(map[string]string{
		"You are: **Security Engineer**":                  fixtureReviewJSON("security-engineer", VerdictPass, nil),
		"You are: **Performance Engineer**":               fixtureReviewJSON("performance-engineer", VerdictPass, nil),
		"You are: **Testability & Test Design Reviewer**": fixtureReviewJSON("testability-reviewer", VerdictPass, nil),
		"You are: **Architecture Reviewer**":              fixtureReviewJSON("architecture-reviewer", VerdictPass, nil),
	})
	// Wrap to count calls.
	realInvoke := InvokeFunc
	InvokeFunc = func(ctx context.Context, b *CLIBackend, p string, timeout int) (string, error) {
		callCount.Add(1)
		return realInvoke(ctx, b, p, timeout)
	}

	dir := t.TempDir()
	runner := NewRunner(dir)

	// First run.
	_, err := runner.RunRound(context.Background(), testSpec, 1, FixedPersonas(), nil, "")
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	firstCount := callCount.Load()

	// Second run with same spec — should use cache.
	callCount.Store(0)
	_, err = runner.RunRound(context.Background(), testSpec, 1, FixedPersonas(), nil, "")
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if callCount.Load() != 0 {
		t.Errorf("second run made %d calls, want 0 (should use cache)", callCount.Load())
	}

	// Third run with different spec — cache should be invalid.
	callCount.Store(0)
	_, err = runner.RunRound(context.Background(), testSpec+"\nModified.", 1, FixedPersonas(), nil, "")
	if err != nil {
		t.Fatalf("third run: %v", err)
	}
	if callCount.Load() != firstCount {
		t.Errorf("third run made %d calls, want %d (cache should be invalid)", callCount.Load(), firstCount)
	}
}
