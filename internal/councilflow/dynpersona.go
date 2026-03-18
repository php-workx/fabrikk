package councilflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// MaxDynamicPersonas is the maximum number of dynamic personas per round.
const MaxDynamicPersonas = 3

// GeneratePersonas analyzes a spec and produces dynamic review personas.
// Returns up to MaxDynamicPersonas personas (language engineer + domain experts).
// Reuses cached personas from outputDir if available.
func GeneratePersonas(ctx context.Context, spec, outputDir string, mode ReviewMode) ([]Persona, error) {
	// Check for cached personas.
	cachedPath := filepath.Join(outputDir, "dynamic-personas.json")
	if data, err := os.ReadFile(cachedPath); err == nil {
		var cached []Persona
		if json.Unmarshal(data, &cached) == nil && len(cached) > 0 {
			fmt.Printf("  dynamic personas: cached (%d personas)\n", len(cached))
			return cached, nil
		}
	}

	prompt := buildPersonaGenerationPrompt(spec, mode)

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}

	if err := os.WriteFile(filepath.Join(outputDir, "persona-generation-prompt.md"), []byte(prompt), 0o644); err != nil {
		return nil, fmt.Errorf("write persona prompt: %w", err)
	}

	fmt.Println("  generating dynamic personas ...")

	backend := KnownBackends[BackendClaude]
	output, err := InvokeFunc(ctx, &backend, prompt, 300)
	if err != nil {
		return nil, fmt.Errorf("invoke persona generator: %w", err)
	}

	personas, err := parsePersonaGenerationOutput(output)
	if err != nil {
		return nil, fmt.Errorf("parse persona output: %w", err)
	}

	if len(personas) > MaxDynamicPersonas {
		personas = personas[:MaxDynamicPersonas]
	}

	if err := writeJSON(filepath.Join(outputDir, "dynamic-personas.json"), personas); err != nil {
		return nil, fmt.Errorf("write dynamic personas: %w", err)
	}

	for i := range personas {
		fmt.Printf("  generated: %s (%s) — %s\n", personas[i].DisplayName, personas[i].Backend, personas[i].Rationale)
	}

	return personas, nil
}

func buildPersonaGenerationPrompt(spec string, mode ReviewMode) string {
	return `# Persona Generation

You are a review planning expert. Analyze the technical specification below and generate review personas.

## Your task

1. Detect the PRIMARY programming language and technology stack from the spec.
2. Generate a Language/Stack Engineer persona with idiomatic expertise for that stack.
3. Identify 1-2 domain areas that need specialized review beyond security, performance, testability, and architecture (those are already covered by fixed personas).
4. Generate a domain expert persona for each area.

## Rules

- Maximum 3 personas total (1 language engineer + up to 2 domain experts).
- Each persona must have a one-sentence rationale tied to specific spec content.
- Domain personas must NOT overlap with the fixed personas (security, performance, testability, architecture).
- Assign each persona a backend: "claude", "codex", or "gemini". Distribute across backends.
- The language engineer should use the backend most aligned with the detected language.

## Output format

Respond with ONLY a JSON array of persona objects:

` + "```json\n" + `[
  {
    "persona_id": "go-engineer",
    "display_name": "Go Engineer",
    "type": "dynamic",
    "perspective": "Idiomatic Go expert focused on ...",
    "review_instructions": "Review the technical spec from the perspective of ... Focus on: ...\n\nFor each issue, produce a JSON finding with:\n- finding_id: short unique ID\n- severity: one of \"critical\", \"significant\", \"minor\"\n- category: short category label\n- section: which spec section\n- location: specific paragraph or sentence reference\n- description: what the problem is\n- recommendation: concrete change to the spec\n- fix: suggested text (or null)\n- why: why this matters (or null)\n- ref: reference to spec/code (or null)\n\nOutput your findings as a JSON object with this schema:\n{\n  \"verdict\": \"PASS|WARN|FAIL\",\n  \"confidence\": \"HIGH|MEDIUM|LOW\",\n  \"key_insight\": \"one-sentence summary\",\n  \"findings\": [...],\n  \"recommendation\": \"overall recommendation\",\n  \"schema_version\": 3\n}\n\nDo NOT flag issues outside your domain.",
    "focus_sections": ["2. Architecture", "4. Interfaces"],
    "backend": "codex",
    "model_preference": "",
    "generated_by": "judge",
    "rationale": "Spec targets a Go CLI tool with concurrent task execution."
  }
]
` + "```\n\n" + PersonaModeDirective(mode) + `

## Technical Specification

` + spec
}

func parsePersonaGenerationOutput(raw string) ([]Persona, error) {
	jsonStr := extractJSON(raw)

	// Try parsing as array first.
	var personas []Persona
	if err := json.Unmarshal([]byte(jsonStr), &personas); err == nil {
		if len(personas) == 0 {
			return nil, fmt.Errorf("%w: model returned empty persona list", ErrPersonaGenerationFailed)
		}
		for i := range personas {
			personas[i].Type = PersonaDynamic
			personas[i].GeneratedBy = "judge"
		}
		return personas, nil
	}

	// Try parsing as object with personas field.
	var wrapper struct {
		Personas []Persona `json:"personas"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &wrapper); err == nil {
		if len(wrapper.Personas) == 0 {
			return nil, fmt.Errorf("%w: model returned empty personas wrapper", ErrPersonaGenerationFailed)
		}
		for i := range wrapper.Personas {
			wrapper.Personas[i].Type = PersonaDynamic
			wrapper.Personas[i].GeneratedBy = "judge"
		}
		return wrapper.Personas, nil
	}

	return nil, fmt.Errorf("%w: could not parse output (raw: %s)", ErrPersonaGenerationFailed, truncateForError(raw, 200))
}
