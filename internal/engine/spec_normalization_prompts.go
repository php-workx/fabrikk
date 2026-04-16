package engine

import (
	"embed"
	"fmt"
	"strings"
)

const (
	specNormalizationConverterPromptFile = "spec-normalize-converter.md"
	specNormalizationVerifierPromptFile  = "spec-normalize-verifier.md"
)

//go:embed prompts/spec-normalize-converter.md prompts/spec-normalize-verifier.md
var specNormalizationPromptFS embed.FS

func loadSpecNormalizationConverterPrompt() (string, error) {
	return loadSpecNormalizationPrompt(specNormalizationConverterPromptFile)
}

func loadSpecNormalizationVerifierPrompt() (string, error) {
	return loadSpecNormalizationPrompt(specNormalizationVerifierPromptFile)
}

func loadSpecNormalizationPrompt(name string) (string, error) {
	data, err := specNormalizationPromptFS.ReadFile("prompts/" + name)
	if err != nil {
		return "", fmt.Errorf("read spec normalization prompt %s: %w", name, err)
	}
	return strings.TrimSpace(string(data)), nil
}
