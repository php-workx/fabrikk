package councilflow

import (
	"strings"
	"testing"
)

func TestFixedPersonasReturnsExpectedSet(t *testing.T) {
	personas := FixedPersonas()
	if len(personas) != 2 {
		t.Fatalf("got %d fixed personas, want 2", len(personas))
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

	for _, want := range []string{"security-perf-engineer", "testability-reviewer"} {
		if !ids[want] {
			t.Errorf("missing expected persona: %s", want)
		}
	}
}

func TestFixedPersonasUseDistinctBackends(t *testing.T) {
	personas := FixedPersonas()
	backends := make(map[string]string)
	for _, p := range personas {
		backends[p.PersonaID] = p.Backend
	}
	if backends["security-perf-engineer"] != BackendCodex {
		t.Errorf("security-perf-engineer backend = %s, want %s", backends["security-perf-engineer"], BackendCodex)
	}
	if backends["testability-reviewer"] != BackendGemini {
		t.Errorf("testability-reviewer backend = %s, want %s", backends["testability-reviewer"], BackendGemini)
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
