package sanitize

// Sanitizer provides methods for sanitizing sensitive data from text
type Sanitizer struct {
	patterns []Pattern
}

// NewSanitizer creates a new Sanitizer with default patterns
func NewSanitizer() *Sanitizer {
	return &Sanitizer{
		patterns: GetSecretPatterns(),
	}
}

// NewSanitizerWithPatterns creates a Sanitizer with custom patterns
func NewSanitizerWithPatterns(patterns []Pattern) *Sanitizer {
	return &Sanitizer{
		patterns: patterns,
	}
}

// Sanitize removes sensitive data from the input string
// Returns the sanitized string with secrets replaced by placeholders
func (s *Sanitizer) Sanitize(input string) string {
	if input == "" {
		return input
	}

	result := input
	for _, p := range s.patterns {
		result = p.Regex.ReplaceAllString(result, p.Replacement)
	}
	return result
}

// SanitizeMultiple sanitizes multiple strings and returns them
func (s *Sanitizer) SanitizeMultiple(inputs []string) []string {
	results := make([]string, len(inputs))
	for i, input := range inputs {
		results[i] = s.Sanitize(input)
	}
	return results
}

// IsDestructive checks if a command is potentially destructive
// Delegates to the risk package function
func (s *Sanitizer) IsDestructive(command string) bool {
	return IsDestructive(command)
}

// GetRiskLevel returns the risk level for a command
func (s *Sanitizer) GetRiskLevel(command string) RiskLevel {
	return GetRiskLevel(command)
}

// DefaultSanitizer is a package-level sanitizer for convenience
var DefaultSanitizer = NewSanitizer()

// Sanitize uses the default sanitizer to sanitize input
func Sanitize(input string) string {
	return DefaultSanitizer.Sanitize(input)
}
