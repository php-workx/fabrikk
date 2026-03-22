package learning

import "testing"

func TestFromFinding_Critical(t *testing.T) {
	f := ExtractedFinding{
		FindingID:      "sec-001",
		Severity:       "critical",
		Category:       "security",
		Description:    "No input validation on spec paths",
		Recommendation: "Add filepath.Clean validation",
		RunID:          "run-123",
		Accepted:       true,
	}
	l := FromFinding(f)
	if l.Category != CategoryAntiPattern {
		t.Errorf("Category = %q, want anti_pattern for critical", l.Category)
	}
	if l.Confidence != 0.8 {
		t.Errorf("Confidence = %f, want 0.8 for accepted", l.Confidence)
	}
	if l.Source != "council" {
		t.Errorf("Source = %q, want council", l.Source)
	}
}

func TestFromFinding_Minor(t *testing.T) {
	f := ExtractedFinding{
		FindingID:   "doc-001",
		Severity:    "minor",
		Category:    "documentation",
		Description: "Missing example in API docs",
	}
	l := FromFinding(f)
	if l.Category != CategoryCodebase {
		t.Errorf("Category = %q, want codebase for minor", l.Category)
	}
	if l.Confidence != 0.6 {
		t.Errorf("Confidence = %f, want 0.6 for not accepted", l.Confidence)
	}
}

func TestFromRejection_WithRationale(t *testing.T) {
	r := ExtractedRejection{
		FindingID:          "perf-001",
		Description:        "Atomic writes are unnecessary overhead",
		RejectionReason:    "Required for crash safety",
		DismissalRationale: "State files can be corrupted by concurrent access without atomic writes",
		PersonaID:          "performance-engineer",
	}
	l := FromRejection(r)
	if l == nil {
		t.Fatal("expected learning, got nil")
	}
	if l.Category != CategoryPattern {
		t.Errorf("Category = %q, want pattern", l.Category)
	}
	if l.Confidence != 0.7 {
		t.Errorf("Confidence = %f, want 0.7", l.Confidence)
	}
}

func TestFromRejection_NoRationale(t *testing.T) {
	r := ExtractedRejection{
		FindingID:       "misc-001",
		Description:     "Some concern",
		RejectionReason: "Not applicable",
	}
	l := FromRejection(r)
	if l != nil {
		t.Error("expected nil for rejection without rationale")
	}
}

func TestFromVerifierFailure_QualityGate(t *testing.T) {
	l := FromVerifierFailure("qg-001", "critical", "quality_gate", "gofumpt formatting errors", "task-1", "run-1", "", []string{"internal/engine"}, nil)
	if l.Category != CategoryTooling {
		t.Errorf("Category = %q, want tooling for quality_gate", l.Category)
	}
	if l.Confidence != 0.9 {
		t.Errorf("Confidence = %f, want 0.9", l.Confidence)
	}
	if l.Source != "verifier" {
		t.Errorf("Source = %q, want verifier", l.Source)
	}
}

func TestFromVerifierFailure_ScopeViolation(t *testing.T) {
	l := FromVerifierFailure("sv-001", "high", "scope_violation", "Modified files outside owned paths", "task-2", "run-1", "scope-types", nil, nil)
	if l.Category != CategoryAntiPattern {
		t.Errorf("Category = %q, want anti_pattern for scope_violation", l.Category)
	}
}
