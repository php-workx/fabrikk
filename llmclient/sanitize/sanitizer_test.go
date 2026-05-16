//nolint:gosec // Sanitizer tests intentionally contain credential-shaped examples to verify redaction.
package sanitize

import (
	"strings"
	"testing"
)

func TestNewSanitizer(t *testing.T) {
	s := NewSanitizer()
	if s == nil {
		t.Fatal("NewSanitizer() returned nil")
	}
	if len(s.patterns) == 0 {
		t.Error("NewSanitizer() created sanitizer with no patterns")
	}
}

func TestNewSanitizerWithPatterns(t *testing.T) {
	customPatterns := []Pattern{
		{
			Name:        "Custom Pattern",
			Regex:       GetSecretPatterns()[0].Regex,
			Replacement: "[CUSTOM]",
		},
	}

	s := NewSanitizerWithPatterns(customPatterns)
	if s == nil {
		t.Fatal("NewSanitizerWithPatterns() returned nil")
	}
	if len(s.patterns) != 1 {
		t.Errorf("NewSanitizerWithPatterns() created sanitizer with %d patterns, want 1", len(s.patterns))
	}
}

func TestSanitizer_Sanitize(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		shouldMatch []string // Substrings that should NOT appear after sanitization
		shouldKeep  []string // Substrings that should still appear
	}{
		{
			name:        "AWS access key",
			input:       "export AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE",
			shouldMatch: []string{"AKIAIOSFODNN7EXAMPLE"},
			shouldKeep:  []string{"export", "AWS_ACCESS_KEY_ID"},
		},
		{
			name:        "AWS secret key",
			input:       "aws_secret_access_key=wJalrXUtnFEMI/K7MDENG/bPxRfiCY",
			shouldMatch: []string{"wJalrXUtnFEMI/K7MDENG/bPxRfiCY"},
		},
		{
			name:        "JWT token",
			input:       "curl -H 'Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0In0.XXXXXXXXXXXXXX'",
			shouldMatch: []string{"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0In0.XXXXXXXXXXXXXX"},
			shouldKeep:  []string{"curl", "Authorization"},
		},
		{
			name:        "Slack token",
			input:       "SLACK_TOKEN=xoxb-123456789012-abcdefghij",
			shouldMatch: []string{"xoxb-123456789012-abcdefghij"},
		},
		{
			name: "PEM block",
			input: `-----BEGIN RSA PRIVATE KEY-----
MIIEpAIBAAKCAQEA
-----END RSA PRIVATE KEY-----`,
			shouldMatch: []string{"MIIEpAIBAAKCAQEA"},
		},
		{
			name:        "password in command",
			input:       "mysql -u root -p password=secret123 database",
			shouldMatch: []string{"secret123"},
			shouldKeep:  []string{"mysql", "root", "database"},
		},
		{
			name:        "API key",
			input:       "curl -H 'api_key: sk-1234567890abcdef'",
			shouldMatch: []string{"sk-1234567890abcdef"},
			shouldKeep:  []string{"curl"},
		},
		{
			name:        "GitHub token",
			input:       "GITHUB_TOKEN=ghp_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
			shouldMatch: []string{"ghp_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"},
		},
		{
			name:       "no secrets",
			input:      "git push origin main",
			shouldKeep: []string{"git", "push", "origin", "main"},
		},
		{
			name:       "empty input",
			input:      "",
			shouldKeep: []string{},
		},
		{
			name:        "multiple secrets",
			input:       "AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE password=hunter2",
			shouldMatch: []string{"AKIAIOSFODNN7EXAMPLE", "hunter2"},
		},
	}

	s := NewSanitizer()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := s.Sanitize(tt.input)

			// Check that secrets are removed
			for _, secret := range tt.shouldMatch {
				if strings.Contains(result, secret) {
					t.Errorf("Sanitize() result still contains secret %q\nInput: %q\nResult: %q",
						secret, tt.input, result)
				}
			}

			// Check that non-secrets are preserved
			for _, keep := range tt.shouldKeep {
				if !strings.Contains(result, keep) {
					t.Errorf("Sanitize() result missing expected substring %q\nInput: %q\nResult: %q",
						keep, tt.input, result)
				}
			}
		})
	}
}

func TestSanitizer_SanitizeMultiple(t *testing.T) {
	s := NewSanitizer()

	inputs := []string{
		"password=secret1",
		"password=secret2",
		"no secrets here",
	}

	results := s.SanitizeMultiple(inputs)

	if len(results) != len(inputs) {
		t.Fatalf("SanitizeMultiple() returned %d results, want %d", len(results), len(inputs))
	}

	// Check first two have been sanitized
	if strings.Contains(results[0], "secret1") {
		t.Error("SanitizeMultiple() did not sanitize first input")
	}
	if strings.Contains(results[1], "secret2") {
		t.Error("SanitizeMultiple() did not sanitize second input")
	}

	// Check third is unchanged (no secrets)
	if results[2] != inputs[2] {
		t.Errorf("SanitizeMultiple() modified input with no secrets: got %q, want %q",
			results[2], inputs[2])
	}
}

func TestSanitizer_IsDestructive(t *testing.T) {
	s := NewSanitizer()

	tests := []struct {
		command  string
		expected bool
	}{
		{"rm -rf /", true},
		{"ls -la", false},
		{"git push --force", true},
		{"git status", false},
	}

	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			result := s.IsDestructive(tt.command)
			if result != tt.expected {
				t.Errorf("IsDestructive(%q) = %v, want %v", tt.command, result, tt.expected)
			}
		})
	}
}

func TestSanitizer_GetRiskLevel(t *testing.T) {
	s := NewSanitizer()

	if s.GetRiskLevel("rm -rf /") != RiskDestructive {
		t.Error("GetRiskLevel() should return RiskDestructive for 'rm -rf /'")
	}
	if s.GetRiskLevel("ls -la") != RiskSafe {
		t.Error("GetRiskLevel() should return RiskSafe for 'ls -la'")
	}
}

func TestDefaultSanitizer(t *testing.T) {
	if DefaultSanitizer == nil {
		t.Fatal("DefaultSanitizer is nil")
	}
}

func TestSanitize_PackageLevel(t *testing.T) {
	input := "password=secret123"
	result := Sanitize(input)

	if strings.Contains(result, "secret123") {
		t.Errorf("Sanitize() package function did not sanitize secret: %q", result)
	}
}

func TestSanitizer_Idempotent(t *testing.T) {
	s := NewSanitizer()
	input := "password=secret123"

	result1 := s.Sanitize(input)
	result2 := s.Sanitize(result1)

	if result1 != result2 {
		t.Errorf("Sanitize() is not idempotent:\nFirst: %q\nSecond: %q", result1, result2)
	}
}

func TestSanitizer_PreservesStructure(t *testing.T) {
	s := NewSanitizer()

	// Command with multiple parts - structure should be preserved
	input := "export AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE && echo 'done'"
	result := s.Sanitize(input)

	if !strings.Contains(result, "export") {
		t.Error("Sanitize() did not preserve 'export'")
	}
	if !strings.Contains(result, "&&") {
		t.Error("Sanitize() did not preserve '&&'")
	}
	if !strings.Contains(result, "echo") {
		t.Error("Sanitize() did not preserve 'echo'")
	}
	if !strings.Contains(result, "done") {
		t.Error("Sanitize() did not preserve 'done'")
	}
}

func BenchmarkSanitize(b *testing.B) {
	s := NewSanitizer()
	input := "export AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE password=secret123 token=eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0In0.XXXXXX"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.Sanitize(input)
	}
}

func BenchmarkSanitize_NoSecrets(b *testing.B) {
	s := NewSanitizer()
	input := "git commit -m 'Add new feature' && git push origin main"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.Sanitize(input)
	}
}

func BenchmarkIsDestructive(b *testing.B) {
	command := "rm -rf /tmp/test"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		IsDestructive(command)
	}
}
