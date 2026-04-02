package councilflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/php-workx/fabrikk/internal/agentcli"
)

const testPersonaFull = `---
persona_id: test-reviewer
display_name: Test Reviewer
perspective: "Reviews for testability and coverage"
focus_sections:
  - "5. Verification"
backend: codex
model_preference: gpt-5.4
---
Review the specification for test coverage gaps.

Focus areas:
1. Are acceptance criteria testable?
2. Are edge cases identified?
`

func TestLoadCustomPersona(t *testing.T) {
	path := writePersonaFile(t, "test.md", testPersonaFull)

	p, err := LoadCustomPersona(path)
	if err != nil {
		t.Fatalf("LoadCustomPersona: %v", err)
	}

	if p.PersonaID != "test-reviewer" {
		t.Errorf("PersonaID = %q, want %q", p.PersonaID, "test-reviewer")
	}
	if p.DisplayName != "Test Reviewer" {
		t.Errorf("DisplayName = %q, want %q", p.DisplayName, "Test Reviewer")
	}
	if p.Perspective != "Reviews for testability and coverage" {
		t.Errorf("Perspective = %q", p.Perspective)
	}
	if p.Backend != agentcli.BackendCodex {
		t.Errorf("Backend = %q, want %q", p.Backend, agentcli.BackendCodex)
	}
	if p.ModelPref != "gpt-5.4" {
		t.Errorf("ModelPref = %q, want %q", p.ModelPref, "gpt-5.4")
	}
	if len(p.FocusSections) != 1 || p.FocusSections[0] != "5. Verification" {
		t.Errorf("FocusSections = %v", p.FocusSections)
	}
}

func TestLoadCustomPersona_DefaultBackend(t *testing.T) {
	content := `---
persona_id: minimal-reviewer
display_name: Minimal Reviewer
perspective: "General review"
---
Review everything.
`
	path := writePersonaFile(t, "minimal.md", content)

	p, err := LoadCustomPersona(path)
	if err != nil {
		t.Fatalf("LoadCustomPersona: %v", err)
	}
	if p.Backend != agentcli.BackendClaude {
		t.Errorf("Backend = %q, want %q (default)", p.Backend, agentcli.BackendClaude)
	}
}

func TestLoadCustomPersona_MissingID(t *testing.T) {
	content := `---
display_name: No ID Reviewer
perspective: "Reviews things"
---
Instructions here.
`
	path := writePersonaFile(t, "noid.md", content)

	_, err := LoadCustomPersona(path)
	if err == nil {
		t.Fatal("expected error for missing persona_id")
	}
	if !strings.Contains(err.Error(), "persona_id is required") {
		t.Errorf("error = %v, want persona_id required", err)
	}
}

func TestLoadCustomPersona_EmptyBody(t *testing.T) {
	content := `---
persona_id: empty-body
display_name: Empty Body
perspective: "Reviews nothing"
---
`
	path := writePersonaFile(t, "empty.md", content)

	_, err := LoadCustomPersona(path)
	if err == nil {
		t.Fatal("expected error for empty body")
	}
	if !strings.Contains(err.Error(), "must not be empty") {
		t.Errorf("error = %v, want body must not be empty", err)
	}
}

func TestLoadCustomPersona_InvalidBackend(t *testing.T) {
	content := `---
persona_id: bad-backend
display_name: Bad Backend
perspective: "Uses unknown backend"
backend: chatgpt
---
Some instructions.
`
	path := writePersonaFile(t, "badbackend.md", content)

	_, err := LoadCustomPersona(path)
	if err == nil {
		t.Fatal("expected error for invalid backend")
	}
	if !strings.Contains(err.Error(), "must be one of") {
		t.Errorf("error = %v, want 'must be one of'", err)
	}
}

func TestLoadCustomPersona_AppendsFindingFormat(t *testing.T) {
	content := `---
persona_id: format-check
display_name: Format Check
perspective: "Checks formatting"
---
Review for format issues.
`
	path := writePersonaFile(t, "format.md", content)

	p, err := LoadCustomPersona(path)
	if err != nil {
		t.Fatalf("LoadCustomPersona: %v", err)
	}
	if !strings.Contains(p.Instructions, "finding_id") {
		t.Error("Instructions should contain findingOutputFormat")
	}
	if !strings.HasPrefix(p.Instructions, "Review for format issues.") {
		t.Error("Instructions should start with the body text")
	}
}

func TestLoadCustomPersona_SetsTypeCustom(t *testing.T) {
	content := `---
persona_id: type-check
display_name: Type Check
perspective: "Checks type"
---
Review types.
`
	path := writePersonaFile(t, "type.md", content)

	p, err := LoadCustomPersona(path)
	if err != nil {
		t.Fatalf("LoadCustomPersona: %v", err)
	}
	if p.Type != PersonaCustom {
		t.Errorf("Type = %q, want %q", p.Type, PersonaCustom)
	}
}

func TestLoadCustomPersona_InvalidID(t *testing.T) {
	content := `---
persona_id: Invalid_ID
display_name: Invalid
perspective: "Bad ID"
---
Instructions.
`
	path := writePersonaFile(t, "badid.md", content)

	_, err := LoadCustomPersona(path)
	if err == nil {
		t.Fatal("expected error for invalid persona_id")
	}
	if !strings.Contains(err.Error(), "lowercase alphanumeric") {
		t.Errorf("error = %v, want lowercase alphanumeric", err)
	}
}

func TestLoadCustomPersonasFromDir(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.md"), `---
persona_id: alpha
display_name: Alpha
perspective: "First"
---
Review alpha.
`)
	writeFile(t, filepath.Join(dir, "b.md"), `---
persona_id: beta
display_name: Beta
perspective: "Second"
---
Review beta.
`)
	// Non-md file should be ignored.
	writeFile(t, filepath.Join(dir, "notes.txt"), "not a persona")

	personas, err := LoadCustomPersonasFromDir(dir)
	if err != nil {
		t.Fatalf("LoadCustomPersonasFromDir: %v", err)
	}
	if len(personas) != 2 {
		t.Fatalf("got %d personas, want 2", len(personas))
	}
}

func TestLoadCustomPersonasFromDir_Empty(t *testing.T) {
	dir := t.TempDir()

	personas, err := LoadCustomPersonasFromDir(dir)
	if err != nil {
		t.Fatalf("LoadCustomPersonasFromDir: %v", err)
	}
	if len(personas) != 0 {
		t.Errorf("got %d personas, want 0", len(personas))
	}
}

func TestSplitPersonaFrontmatter(t *testing.T) {
	data := []byte("---\npersona_id: test\n---\nBody text here.")
	fm, body, err := splitPersonaFrontmatter(data)
	if err != nil {
		t.Fatalf("splitPersonaFrontmatter: %v", err)
	}
	if fm.PersonaID != "test" {
		t.Errorf("PersonaID = %q, want %q", fm.PersonaID, "test")
	}
	if body != "Body text here." {
		t.Errorf("body = %q, want %q", body, "Body text here.")
	}
}

func TestSplitPersonaFrontmatter_NoFrontmatter(t *testing.T) {
	data := []byte("Just plain text, no frontmatter.")
	_, _, err := splitPersonaFrontmatter(data)
	if err == nil {
		t.Fatal("expected error for missing frontmatter")
	}
}

func TestLoadCustomPersona_MissingDisplayName(t *testing.T) {
	content := `---
persona_id: no-name
perspective: "Has perspective but no name"
---
Instructions here.
`
	path := writePersonaFile(t, "noname.md", content)

	_, err := LoadCustomPersona(path)
	if err == nil {
		t.Fatal("expected error for missing display_name")
	}
	if !strings.Contains(err.Error(), "display_name is required") {
		t.Errorf("error = %v, want display_name required", err)
	}
}

func TestLoadCustomPersona_MissingPerspective(t *testing.T) {
	content := `---
persona_id: no-perspective
display_name: No Perspective
---
Instructions here.
`
	path := writePersonaFile(t, "nopersp.md", content)

	_, err := LoadCustomPersona(path)
	if err == nil {
		t.Fatal("expected error for missing perspective")
	}
	if !strings.Contains(err.Error(), "perspective is required") {
		t.Errorf("error = %v, want perspective required", err)
	}
}

func TestLoadCustomPersona_FileNotFound(t *testing.T) {
	_, err := LoadCustomPersona("/nonexistent/path/persona.md")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestSplitPersonaFrontmatter_MissingClosingDelimiter(t *testing.T) {
	data := []byte("---\npersona_id: test\nNo closing delimiter here.")
	_, _, err := splitPersonaFrontmatter(data)
	if err == nil {
		t.Fatal("expected error for missing closing ---")
	}
	if !strings.Contains(err.Error(), "closing ---") {
		t.Errorf("error = %v, want 'closing ---'", err)
	}
}

func writePersonaFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	writeFile(t, path, content)
	return path
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
