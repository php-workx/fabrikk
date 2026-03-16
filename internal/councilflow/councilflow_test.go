package councilflow

import (
	"strings"
	"testing"
)

func TestFixedPersonasReturnsExpectedSet(t *testing.T) {
	personas := FixedPersonas()
	if len(personas) != 4 {
		t.Fatalf("got %d fixed personas, want 4", len(personas))
	}

	ids := make(map[string]bool)
	for _, p := range personas {
		ids[p.PersonaID] = true
		if p.Type != PersonaFixed {
			t.Errorf("persona %s type = %s, want fixed", p.PersonaID, p.Type)
		}
		if p.DisplayName == "" {
			t.Errorf("persona %s has empty display name", p.PersonaID)
		}
		if p.Perspective == "" {
			t.Errorf("persona %s has empty perspective", p.PersonaID)
		}
		if p.Instructions == "" {
			t.Errorf("persona %s has empty instructions", p.PersonaID)
		}
		if p.Backend == "" {
			t.Errorf("persona %s has empty backend", p.PersonaID)
		}
	}

	for _, want := range []string{"security-engineer", "performance-engineer", "testability-reviewer", "architecture-reviewer"} {
		if !ids[want] {
			t.Errorf("missing expected persona: %s", want)
		}
	}
}

func TestFixedPersonasUseAllBackends(t *testing.T) {
	personas := FixedPersonas()
	backends := make(map[string]bool)
	for _, p := range personas {
		backends[p.Backend] = true
	}
	for _, want := range []string{BackendClaude, BackendCodex, BackendGemini} {
		if !backends[want] {
			t.Errorf("no persona uses backend %s", want)
		}
	}
}

func TestFixedPersonasHaveFocusedInstructions(t *testing.T) {
	personas := FixedPersonas()
	for _, p := range personas {
		if !strings.Contains(p.Instructions, "Do NOT flag") {
			t.Errorf("persona %s instructions missing scope boundary ('Do NOT flag')", p.PersonaID)
		}
	}
}

func TestBackendForReturnsCorrectBackend(t *testing.T) {
	tests := []struct {
		backend string
		wantCmd string
	}{
		{BackendClaude, "claude"},
		{BackendCodex, "codex"},
		{BackendGemini, "gemini"},
		{"unknown", "claude"}, // fallback
		{"", "claude"},        // fallback
	}
	for _, tt := range tests {
		p := &Persona{Backend: tt.backend}
		got := BackendFor(p)
		if got.Command != tt.wantCmd {
			t.Errorf("BackendFor(%q) command = %s, want %s", tt.backend, got.Command, tt.wantCmd)
		}
	}
}

func TestBuildReviewPromptContainsRequiredSections(t *testing.T) {
	persona := FixedPersonas()[0]
	spec := "# Test Spec\n\n## 1. Architecture\nSome architecture.\n"

	prompt := BuildReviewPrompt(&PromptContext{
		Spec:    spec,
		Persona: persona,
		Round:   1,
	})

	for _, want := range []string{
		"# Review Assignment",
		persona.DisplayName,
		persona.Perspective,
		"## Instructions",
		"## Technical Specification",
		spec,
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}

	if strings.Contains(prompt, "Prior Round Context") {
		t.Error("round 1 prompt should not contain prior round context")
	}
}

func TestBuildReviewPromptIncludesPriorFindingsInRound2(t *testing.T) {
	persona := FixedPersonas()[0]
	spec := "# Test Spec\n"
	prior := []ReviewOutput{
		{
			PersonaID: "testability-reviewer",
			Round:     1,
			Verdict:   VerdictWarn,
			Findings: []Finding{
				{FindingID: "test-001", Severity: "significant", Description: "Missing edge case coverage"},
			},
		},
	}

	prompt := BuildReviewPrompt(&PromptContext{
		Spec:          spec,
		Persona:       persona,
		Round:         2,
		PriorFindings: prior,
	})

	for _, want := range []string{
		"Prior Round Context",
		"testability-reviewer",
		"test-001",
		"Missing edge case coverage",
		"Do not repeat issues",
		"WARN",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("round 2 prompt missing %q", want)
		}
	}
}

func TestBuildReviewPromptIncludesCodebaseContext(t *testing.T) {
	persona := FixedPersonas()[0]
	prompt := BuildReviewPrompt(&PromptContext{
		Spec:            "# Spec\n",
		Persona:         persona,
		Round:           1,
		CodebaseContext: "func main() { fmt.Println(\"hello\") }",
	})

	if !strings.Contains(prompt, "Codebase Context") {
		t.Error("prompt missing codebase context section")
	}
	if !strings.Contains(prompt, "func main()") {
		t.Error("prompt missing codebase content")
	}
}

func TestBuildReviewPromptOmitsCodebaseContextWhenEmpty(t *testing.T) {
	persona := FixedPersonas()[0]
	prompt := BuildReviewPrompt(&PromptContext{
		Spec:    "# Spec\n",
		Persona: persona,
		Round:   1,
	})

	if strings.Contains(prompt, "Codebase Context") {
		t.Error("prompt should not contain codebase context section when empty")
	}
}

func TestBuildNudgePromptContainsPersonaName(t *testing.T) {
	persona := FixedPersonas()[0]
	nudge := BuildNudgePrompt(&persona)

	if !strings.Contains(nudge, persona.DisplayName) {
		t.Errorf("nudge prompt missing persona name %q", persona.DisplayName)
	}
	if !strings.Contains(nudge, "Edge cases") {
		t.Error("nudge prompt missing edge case instruction")
	}
}

func TestExtractJSONFromCodeFence(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "json code fence",
			input: "Here are my findings:\n```json\n{\"findings\": []}\n```\nDone.",
			want:  `{"findings": []}`,
		},
		{
			name:  "plain code fence",
			input: "```\n{\"findings\": [1]}\n```",
			want:  `{"findings": [1]}`,
		},
		{
			name:  "bare json",
			input: `{"findings": [2]}`,
			want:  `{"findings": [2]}`,
		},
		{
			name:  "json with preamble",
			input: `Here is the review output: {"persona_id": "test", "findings": []}`,
			want:  `{"persona_id": "test", "findings": []}`,
		},
		{
			name:  "nested braces",
			input: `{"outer": {"inner": "value"}}`,
			want:  `{"outer": {"inner": "value"}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractJSON(tt.input)
			if got != tt.want {
				t.Errorf("extractJSON() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseReviewOutputValidJSON(t *testing.T) {
	raw := `{
  "persona_id": "security-perf-engineer",
  "round": 1,
  "verdict": "WARN",
  "confidence": "HIGH",
  "key_insight": "Unauthenticated worker channel",
  "findings": [
    {
      "finding_id": "sec-001",
      "severity": "significant",
      "category": "authentication",
      "section": "4. Interfaces",
      "location": "Section 4.2",
      "description": "No auth on worker channel",
      "recommendation": "Add HMAC auth",
      "fix": null,
      "why": "Prevents injection",
      "ref": null
    }
  ],
  "recommendation": "Address authentication gap",
  "schema_version": 3
}`

	review, err := parseReviewOutput(raw)
	if err != nil {
		t.Fatalf("parseReviewOutput: %v", err)
	}
	if review.PersonaID != "security-perf-engineer" {
		t.Errorf("persona_id = %q, want security-perf-engineer", review.PersonaID)
	}
	if review.Verdict != VerdictWarn {
		t.Errorf("verdict = %q, want WARN", review.Verdict)
	}
	if len(review.Findings) != 1 {
		t.Fatalf("findings count = %d, want 1", len(review.Findings))
	}
	if review.Findings[0].FindingID != "sec-001" {
		t.Errorf("finding_id = %q, want sec-001", review.Findings[0].FindingID)
	}
}

func TestParseReviewOutputFromCodeFence(t *testing.T) {
	raw := "Here is my review:\n```json\n" + `{
  "persona_id": "test",
  "round": 1,
  "verdict": "PASS",
  "confidence": "HIGH",
  "key_insight": "No issues",
  "findings": [],
  "recommendation": "Ready to proceed",
  "schema_version": 3
}` + "\n```\nThat's all."

	review, err := parseReviewOutput(raw)
	if err != nil {
		t.Fatalf("parseReviewOutput: %v", err)
	}
	if review.PersonaID != "test" {
		t.Errorf("persona_id = %q, want test", review.PersonaID)
	}
	if review.Verdict != VerdictPass {
		t.Errorf("verdict = %q, want PASS", review.Verdict)
	}
}

func TestParseReviewOutputRejectsGarbage(t *testing.T) {
	_, err := parseReviewOutput("this is not json at all")
	if err == nil {
		t.Fatal("parseReviewOutput should fail on non-JSON input")
	}
}

func TestComputeConsensus(t *testing.T) {
	tests := []struct {
		name     string
		verdicts []Verdict
		want     Verdict
	}{
		{"all pass", []Verdict{VerdictPass, VerdictPass}, VerdictPass},
		{"any fail", []Verdict{VerdictPass, VerdictFail}, VerdictFail},
		{"all fail", []Verdict{VerdictFail, VerdictFail}, VerdictFail},
		{"mixed warn", []Verdict{VerdictPass, VerdictWarn}, VerdictWarn},
		{"warn and fail", []Verdict{VerdictWarn, VerdictFail}, VerdictFail},
		{"all warn", []Verdict{VerdictWarn, VerdictWarn}, VerdictWarn},
		{"empty", []Verdict{}, VerdictFail},
		{"single pass", []Verdict{VerdictPass}, VerdictPass},
		{"single fail", []Verdict{VerdictFail}, VerdictFail},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reviews := make([]ReviewOutput, len(tt.verdicts))
			for i, v := range tt.verdicts {
				reviews[i] = ReviewOutput{Verdict: v}
			}
			got := ComputeConsensus(reviews)
			if got != tt.want {
				t.Errorf("ComputeConsensus(%v) = %s, want %s", tt.verdicts, got, tt.want)
			}
		})
	}
}

func TestBuildJudgePromptContainsRequiredSections(t *testing.T) {
	spec := "# Test Spec\n\n## 1. Architecture\nContent.\n"
	reviews := []ReviewOutput{
		{
			PersonaID:  "security-perf-engineer",
			Round:      1,
			Verdict:    VerdictFail,
			Confidence: ConfidenceHigh,
			KeyInsight: "Missing auth",
			Findings: []Finding{
				{FindingID: "sec-001", Severity: "significant", Description: "No auth"},
			},
		},
	}

	prompt := buildJudgePrompt(spec, 1, reviews)

	for _, want := range []string{
		"Finding-by-Finding Validation",
		"Process each finding INDIVIDUALLY",
		"DO NOT synthesize",
		"MUST NOT dismiss a high-confidence finding",
		"security-perf-engineer",
		"sec-001",
		"Missing auth",
		"## Current Technical Specification",
		"# Test Spec",
		"edits",
		"rejected",
		"find",
		"replace",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("judge prompt missing %q", want)
		}
	}
}

func TestParseJudgeOutputAppliesEdits(t *testing.T) {
	raw := `{
  "edits": [
    {
      "finding_id": "sec-001",
      "persona_id": "security-perf-engineer",
      "action": "Added auth requirement",
      "section": "## 1. Architecture",
      "find": "No auth defined.",
      "replace": "All inter-component communication must be authenticated."
    }
  ],
  "rejected": [
    {
      "finding_id": "perf-003",
      "persona_id": "security-perf-engineer",
      "severity": "significant",
      "description": "Add connection pooling",
      "rejection_reason": "No database in this system",
      "cross_reference": "Section 3.1"
    }
  ]
}`
	spec := "# Spec\n\n## 1. Architecture\nNo auth defined.\n\n## 2. Other\nStuff.\n"

	result, err := parseJudgeOutput(raw, 1, spec)
	if err != nil {
		t.Fatalf("parseJudgeOutput: %v", err)
	}
	if result.AppliedCount != 1 {
		t.Errorf("applied count = %d, want 1", result.AppliedCount)
	}
	if result.RejectedCount != 1 {
		t.Errorf("rejected count = %d, want 1", result.RejectedCount)
	}
	if !strings.Contains(result.UpdatedSpec, "must be authenticated") {
		t.Error("edit was not applied to spec")
	}
	if strings.Contains(result.UpdatedSpec, "No auth defined") {
		t.Error("old text was not replaced")
	}
	if !strings.Contains(result.UpdatedSpec, "## 2. Other") {
		t.Error("unrelated section was lost")
	}
	if result.RejectionLog.Rejections[0].FindingID != "perf-003" {
		t.Errorf("rejection finding_id = %q, want perf-003", result.RejectionLog.Rejections[0].FindingID)
	}
}

func TestParseJudgeOutputHandlesUnmatchedEdits(t *testing.T) {
	raw := `{
  "edits": [
    {
      "finding_id": "sec-001",
      "persona_id": "test",
      "action": "Fix auth",
      "section": "## 1. Intro",
      "find": "text that does not exist in spec",
      "replace": "replacement"
    }
  ],
  "rejected": []
}`
	spec := "# Spec\n\nSome content.\n"

	result, err := parseJudgeOutput(raw, 1, spec)
	if err != nil {
		t.Fatalf("parseJudgeOutput: %v", err)
	}
	if result.AppliedCount != 0 {
		t.Errorf("applied count = %d, want 0 (edit should fail to match)", result.AppliedCount)
	}
	if result.UpdatedSpec != spec {
		t.Error("spec should be unchanged when edit doesn't match")
	}
}

func TestDetectDriftRemovedSection(t *testing.T) {
	before := "# Spec\n\n## 1. Intro\nA\n\n## 2. Architecture\nB\n\n## 3. Details\nC\n"
	after := "# Spec\n\n## 1. Intro\nA\n\n## 2. Architecture\nB\n"

	warnings := DetectDrift(before, after)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "## 3. Details") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected drift warning for removed section, got: %v", warnings)
	}
}

func TestDetectDriftSignificantShrinkage(t *testing.T) {
	before := strings.Repeat("x", 1000)
	after := strings.Repeat("x", 500)

	warnings := DetectDrift(before, after)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "shrank") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected shrinkage warning, got: %v", warnings)
	}
}

func TestDetectDriftNoWarningsWhenClean(t *testing.T) {
	before := "# Spec\n\n## 1. Intro\nA\n"
	after := "# Spec\n\n## 1. Intro\nA with improvements.\n"

	warnings := DetectDrift(before, after)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got: %v", warnings)
	}
}

func TestMergeFindingsDeduplicates(t *testing.T) {
	initial := []Finding{
		{FindingID: "sec-001", Description: "old version"},
		{FindingID: "sec-002", Description: "only in initial"},
	}
	nudge := []Finding{
		{FindingID: "sec-001", Description: "updated version"},
		{FindingID: "sec-003", Description: "only in nudge"},
	}

	merged := mergeFindings(initial, nudge)
	if len(merged) != 3 {
		t.Fatalf("merged count = %d, want 3", len(merged))
	}
	// sec-001 should be the nudge version.
	if merged[0].Description != "updated version" {
		t.Errorf("sec-001 description = %q, want nudge version", merged[0].Description)
	}
	// sec-002 preserved from initial.
	if merged[1].FindingID != "sec-002" {
		t.Errorf("merged[1] = %q, want sec-002", merged[1].FindingID)
	}
	// sec-003 added from nudge.
	if merged[2].FindingID != "sec-003" {
		t.Errorf("merged[2] = %q, want sec-003", merged[2].FindingID)
	}
}

func TestVerdictSeverityOrdering(t *testing.T) {
	if verdictSeverity(VerdictPass) >= verdictSeverity(VerdictWarn) {
		t.Error("PASS should be less severe than WARN")
	}
	if verdictSeverity(VerdictWarn) >= verdictSeverity(VerdictFail) {
		t.Error("WARN should be less severe than FAIL")
	}
}

func TestParsePersonaGenerationOutputArray(t *testing.T) {
	raw := `[
  {
    "persona_id": "go-engineer",
    "display_name": "Go Engineer",
    "perspective": "Idiomatic Go expert",
    "review_instructions": "Review for Go patterns",
    "backend": "codex",
    "rationale": "Spec targets Go CLI"
  },
  {
    "persona_id": "state-machine-expert",
    "display_name": "State Machine Expert",
    "perspective": "Distributed state expert",
    "review_instructions": "Review state transitions",
    "backend": "gemini",
    "rationale": "Spec describes file-based state machine"
  }
]`

	personas, err := parsePersonaGenerationOutput(raw)
	if err != nil {
		t.Fatalf("parsePersonaGenerationOutput: %v", err)
	}
	if len(personas) != 2 {
		t.Fatalf("got %d personas, want 2", len(personas))
	}
	if personas[0].PersonaID != "go-engineer" {
		t.Errorf("persona[0] id = %q, want go-engineer", personas[0].PersonaID)
	}
	if personas[0].Type != PersonaDynamic {
		t.Errorf("persona[0] type = %q, want dynamic", personas[0].Type)
	}
	if personas[0].GeneratedBy == "" {
		t.Error("persona[0] generated_by is empty, want non-empty")
	}
	if personas[1].Backend != "gemini" {
		t.Errorf("persona[1] backend = %q, want gemini", personas[1].Backend)
	}
}

func TestParsePersonaGenerationOutputWrapped(t *testing.T) {
	raw := `{"personas": [{"persona_id": "rust-engineer", "display_name": "Rust Engineer", "backend": "claude", "rationale": "Rust project"}]}`

	personas, err := parsePersonaGenerationOutput(raw)
	if err != nil {
		t.Fatalf("parsePersonaGenerationOutput: %v", err)
	}
	if len(personas) != 1 || personas[0].PersonaID != "rust-engineer" {
		t.Fatalf("unexpected personas: %+v", personas)
	}
}

func TestParsePersonaGenerationOutputRejectsGarbage(t *testing.T) {
	_, err := parsePersonaGenerationOutput("not json at all")
	if err == nil {
		t.Fatal("should reject non-JSON output")
	}
}

func TestParseAnnotationsBasic(t *testing.T) {
	md := `# Spec

## 1. Architecture
Some text here.

@@ This needs more detail about the data flow.

More text.

@@ Another concern about error handling.
`
	annotations := ParseAnnotations(md)
	if len(annotations) != 2 {
		t.Fatalf("got %d annotations, want 2", len(annotations))
	}
	if annotations[0].Line != 6 {
		t.Errorf("annotation[0] line = %d, want 6", annotations[0].Line)
	}
	if annotations[0].Text != "This needs more detail about the data flow." {
		t.Errorf("annotation[0] text = %q", annotations[0].Text)
	}
	if annotations[0].Context != "" {
		t.Errorf("annotation[0] context = %q, want empty (preceding line is blank)", annotations[0].Context)
	}
}

func TestParseAnnotationsIgnoresCodeBlocks(t *testing.T) {
	md := "# Spec\n\n```\n@@ this is inside a code block\n```\n\n@@ this is real\n"
	annotations := ParseAnnotations(md)
	if len(annotations) != 1 {
		t.Fatalf("got %d annotations, want 1", len(annotations))
	}
	if annotations[0].Text != "this is real" {
		t.Errorf("annotation text = %q, want 'this is real'", annotations[0].Text)
	}
}

func TestParseAnnotationsEmptyDoc(t *testing.T) {
	if len(ParseAnnotations("")) != 0 {
		t.Error("empty doc should have no annotations")
	}
	if len(ParseAnnotations("# Spec\nNo annotations here.\n")) != 0 {
		t.Error("doc without @@ should have no annotations")
	}
}

func TestHasUnresolvedAnnotations(t *testing.T) {
	if HasUnresolvedAnnotations("# Spec\nClean.\n") {
		t.Error("clean doc should not have unresolved annotations")
	}
	if !HasUnresolvedAnnotations("# Spec\n@@ Fix this.\n") {
		t.Error("doc with @@ should have unresolved annotations")
	}
}

func TestTruncateForError(t *testing.T) {
	short := "hello"
	if got := truncateForError(short, 10); got != short {
		t.Errorf("truncateForError(%q, 10) = %q", short, got)
	}

	long := strings.Repeat("x", 300)
	got := truncateForError(long, 200)
	if len(got) != 203 { // 200 + "..."
		t.Errorf("truncateForError length = %d, want 203", len(got))
	}
}
