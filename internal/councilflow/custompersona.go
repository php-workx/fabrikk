package councilflow

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/php-workx/fabrikk/internal/agentcli"
	"gopkg.in/yaml.v3"
)

// ErrInvalidCustomPersona indicates a custom persona file is malformed or incomplete.
var ErrInvalidCustomPersona = errors.New("invalid custom persona")

// personaFrontmatter holds the YAML fields from a custom persona prompt file.
type personaFrontmatter struct {
	PersonaID     string   `yaml:"persona_id"`
	DisplayName   string   `yaml:"display_name"`
	Perspective   string   `yaml:"perspective"`
	FocusSections []string `yaml:"focus_sections,omitempty"`
	Backend       string   `yaml:"backend,omitempty"`
	ModelPref     string   `yaml:"model_preference,omitempty"`
}

var validPersonaID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// LoadCustomPersona reads a markdown persona prompt file and returns a validated Persona.
// The file must have YAML frontmatter (delimited by ---) and a non-empty markdown body.
// The body becomes the persona's Instructions, with the standard finding output format appended.
func LoadCustomPersona(path string) (Persona, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Persona{}, fmt.Errorf("%w: read %s: %v", ErrInvalidCustomPersona, path, err)
	}

	fm, body, err := splitPersonaFrontmatter(data)
	if err != nil {
		return Persona{}, fmt.Errorf("%w: %s: %v", ErrInvalidCustomPersona, path, err)
	}

	p := Persona{
		PersonaID:     fm.PersonaID,
		DisplayName:   fm.DisplayName,
		Type:          PersonaCustom,
		Perspective:   fm.Perspective,
		Instructions:  body + "\n\n" + findingOutputFormat,
		FocusSections: fm.FocusSections,
		Backend:       fm.Backend,
		ModelPref:     fm.ModelPref,
		GeneratedBy:   "custom:" + filepath.Base(path),
	}

	if err := ValidateCustomPersona(&p); err != nil {
		return Persona{}, fmt.Errorf("%s: %w", path, err)
	}

	return p, nil
}

// LoadCustomPersonasFromDir loads all *.md files from a directory as custom personas.
func LoadCustomPersonasFromDir(dir string) ([]Persona, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil {
		return nil, fmt.Errorf("glob %s: %w", dir, err)
	}

	var personas []Persona
	for _, path := range matches {
		p, loadErr := LoadCustomPersona(path)
		if loadErr != nil {
			return nil, loadErr
		}
		personas = append(personas, p)
	}
	return personas, nil
}

// ValidateCustomPersona checks required fields and normalizes defaults.
func ValidateCustomPersona(p *Persona) error {
	if p.PersonaID == "" {
		return fmt.Errorf("%w: persona_id is required", ErrInvalidCustomPersona)
	}
	if !validPersonaID.MatchString(p.PersonaID) {
		return fmt.Errorf("%w: persona_id %q must be lowercase alphanumeric with hyphens", ErrInvalidCustomPersona, p.PersonaID)
	}
	if p.DisplayName == "" {
		return fmt.Errorf("%w: display_name is required", ErrInvalidCustomPersona)
	}
	if p.Perspective == "" {
		return fmt.Errorf("%w: perspective is required", ErrInvalidCustomPersona)
	}
	if strings.TrimSpace(strings.TrimSuffix(p.Instructions, findingOutputFormat)) == "" {
		return fmt.Errorf("%w: prompt body (instructions) must not be empty", ErrInvalidCustomPersona)
	}

	// Default backend to Claude if not specified.
	if p.Backend == "" {
		p.Backend = agentcli.BackendClaude
	}

	// Validate backend is known.
	knownBackends := map[string]bool{
		agentcli.BackendClaude: true,
		agentcli.BackendCodex:  true,
		agentcli.BackendGemini: true,
	}
	if !knownBackends[p.Backend] {
		return fmt.Errorf("%w: backend %q must be one of: claude, codex, gemini", ErrInvalidCustomPersona, p.Backend)
	}

	return nil
}

// splitPersonaFrontmatter splits a persona file into YAML frontmatter and markdown body.
func splitPersonaFrontmatter(data []byte) (*personaFrontmatter, string, error) {
	content := string(data)

	if !strings.HasPrefix(content, "---\n") {
		return nil, "", fmt.Errorf("missing YAML frontmatter (must start with ---)")
	}

	// Find the closing --- delimiter.
	rest := content[4:] // skip opening "---\n"
	idx := strings.Index(rest, "\n---\n")
	if idx < 0 {
		// Try trailing --- at end of file.
		if strings.HasSuffix(strings.TrimRight(rest, "\n"), "\n---") {
			idx = strings.LastIndex(rest, "\n---")
		}
		if idx < 0 {
			return nil, "", fmt.Errorf("missing closing --- delimiter")
		}
	}

	yamlBlock := rest[:idx]
	body := strings.TrimSpace(rest[idx+4:]) // skip "\n---\n"

	var fm personaFrontmatter
	if err := yaml.Unmarshal([]byte(yamlBlock), &fm); err != nil {
		return nil, "", fmt.Errorf("parse frontmatter: %w", err)
	}

	// Handle edge case: closing delimiter might consume the trailing \n---
	if body == "" && bytes.HasSuffix(data, []byte("\n---\n")) {
		body = ""
	}

	return &fm, body, nil
}
