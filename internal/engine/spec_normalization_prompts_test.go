package engine

import (
	"strings"
	"testing"
)

func TestLoadSpecNormalizationPrompts(t *testing.T) {
	converter, err := loadSpecNormalizationConverterPrompt()
	if err != nil {
		t.Fatalf("loadSpecNormalizationConverterPrompt: %v", err)
	}
	verifier, err := loadSpecNormalizationVerifierPrompt()
	if err != nil {
		t.Fatalf("loadSpecNormalizationVerifierPrompt: %v", err)
	}

	for name, prompt := range map[string]string{
		"converter": converter,
		"verifier":  verifier,
	} {
		if strings.TrimSpace(prompt) == "" {
			t.Fatalf("%s prompt is empty", name)
		}
		if strings.Contains(prompt, "docs/prompts") {
			t.Fatalf("%s prompt references docs/prompts path", name)
		}
		if !strings.Contains(prompt, "Return ONLY valid JSON") {
			t.Fatalf("%s prompt does not require strict JSON output", name)
		}
	}

	for _, want := range []string{
		"line-numbered",
		"source_refs",
		"Do not invent",
		"non-goals",
		"Ask First",
		"existing requirement IDs",
		"clarifications",
	} {
		if !strings.Contains(converter, want) {
			t.Fatalf("converter prompt missing %q", want)
		}
	}

	for _, want := range []string{
		"independent verifier",
		"Do not edit",
		"lost_information",
		"unsupported_addition",
		"scope_change",
		"missing_source_evidence",
		"non_goal_promoted",
		"ambiguous_requirement",
		"pass",
	} {
		if !strings.Contains(verifier, want) {
			t.Fatalf("verifier prompt missing %q", want)
		}
	}
}
