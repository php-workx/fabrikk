//nolint:gosec // Sanitizer tests intentionally contain credential-shaped examples to verify redaction.
package sanitize

import (
	"testing"
)

// mustGetPattern retrieves a pattern by name from GetSecretPatterns.
// It calls t.Fatalf if the pattern is not found.
func mustGetPattern(t *testing.T, name string) Pattern {
	t.Helper()
	for _, p := range GetSecretPatterns() {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("Pattern %q not found in GetSecretPatterns()", name)
	return Pattern{} // unreachable
}

func TestGetSecretPatterns(t *testing.T) {
	patterns := GetSecretPatterns()
	if len(patterns) == 0 {
		t.Error("GetSecretPatterns() returned empty list")
	}

	// Verify all patterns have required fields
	for _, p := range patterns {
		if p.Name == "" {
			t.Error("Pattern has empty name")
		}
		if p.Regex == nil {
			t.Errorf("Pattern %q has nil regex", p.Name)
		}
		if p.Replacement == "" {
			t.Errorf("Pattern %q has empty replacement", p.Name)
		}
	}
}

func TestPatterns_AWSAccessKey(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		matches bool
	}{
		{
			name:    "valid AWS access key",
			input:   "AKIAIOSFODNN7EXAMPLE",
			matches: true,
		},
		{
			name:    "AWS access key in context",
			input:   "export AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE",
			matches: true,
		},
		{
			name:    "invalid prefix",
			input:   "BKIAIOSFODNN7EXAMPLE",
			matches: false,
		},
		{
			name:    "too short",
			input:   "AKIA123456789012345",
			matches: false,
		},
		{
			name:    "lowercase not matched",
			input:   "akiaiosfodnn7example",
			matches: false,
		},
	}

	pattern := mustGetPattern(t, "AWS Access Key")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if pattern.Regex.MatchString(tt.input) != tt.matches {
				t.Errorf("AWS Access Key pattern.MatchString(%q) = %v, want %v",
					tt.input, !tt.matches, tt.matches)
			}
		})
	}
}

func TestPatterns_AWSSecretKey(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		matches bool
	}{
		{
			name:    "aws_secret_access_key with equals",
			input:   "aws_secret_access_key=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			matches: true,
		},
		{
			name:    "AWS_SECRET_ACCESS_KEY uppercase",
			input:   "AWS_SECRET_ACCESS_KEY=somevalue",
			matches: true,
		},
		{
			name:    "secret_access_key with colon",
			input:   "secret_access_key: secretvalue123",
			matches: true,
		},
		{
			name:    "no secret",
			input:   "some random text",
			matches: false,
		},
	}

	pattern := mustGetPattern(t, "AWS Secret Key")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if pattern.Regex.MatchString(tt.input) != tt.matches {
				t.Errorf("AWS Secret Key pattern.MatchString(%q) = %v, want %v",
					tt.input, !tt.matches, tt.matches)
			}
		})
	}
}

func TestPatterns_JWT(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		matches bool
	}{
		{
			name:    "valid JWT",
			input:   "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX",
			matches: true,
		},
		{
			name:    "JWT in Authorization header",
			input:   "Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U",
			matches: true,
		},
		{
			name:    "incomplete JWT",
			input:   "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0",
			matches: false,
		},
		{
			name:    "random base64",
			input:   "dGhpcyBpcyBub3QgYSBqd3Q=",
			matches: false,
		},
	}

	pattern := mustGetPattern(t, "JWT Token")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if pattern.Regex.MatchString(tt.input) != tt.matches {
				t.Errorf("JWT pattern.MatchString(%q) = %v, want %v",
					tt.input, !tt.matches, tt.matches)
			}
		})
	}
}

func TestPatterns_SlackToken(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		matches bool
	}{
		{
			name:    "xoxb bot token",
			input:   "xoxb-fake-bot-token-for-testing",
			matches: true,
		},
		{
			name:    "xoxp user token",
			input:   "xoxp-fake-user-token-for-testing",
			matches: true,
		},
		{
			name:    "xoxa app token",
			input:   "xoxa-fake-app-token",
			matches: true,
		},
		{
			name:    "invalid prefix",
			input:   "xoxz-fake-invalid-prefix",
			matches: false,
		},
	}

	pattern := mustGetPattern(t, "Slack Token")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if pattern.Regex.MatchString(tt.input) != tt.matches {
				t.Errorf("Slack Token pattern.MatchString(%q) = %v, want %v",
					tt.input, !tt.matches, tt.matches)
			}
		})
	}
}

func TestPatterns_PEMBlock(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		matches bool
	}{
		{
			name: "RSA private key",
			input: `-----BEGIN RSA PRIVATE KEY-----
MIIEpAIBAAKCAQEA...
-----END RSA PRIVATE KEY-----`,
			matches: true,
		},
		{
			name: "certificate",
			input: `-----BEGIN CERTIFICATE-----
MIIDXTCCAkWgAwIB...
-----END CERTIFICATE-----`,
			matches: true,
		},
		{
			name:    "partial PEM",
			input:   "-----BEGIN RSA PRIVATE KEY-----",
			matches: false,
		},
	}

	pattern := mustGetPattern(t, "PEM Block")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if pattern.Regex.MatchString(tt.input) != tt.matches {
				t.Errorf("PEM Block pattern.MatchString(%q) = %v, want %v",
					tt.input, !tt.matches, tt.matches)
			}
		})
	}
}

func TestPatterns_GenericSecret(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		matches bool
	}{
		{
			name:    "password with equals",
			input:   "password=hunter2",
			matches: true,
		},
		{
			name:    "PASSWORD uppercase",
			input:   "PASSWORD=secret123",
			matches: true,
		},
		{
			name:    "token with colon",
			input:   "token: abc123xyz",
			matches: true,
		},
		{
			name:    "api_key",
			input:   "api_key=my-api-key-value",
			matches: true,
		},
		{
			name:    "secret with spaces",
			input:   "secret = supersecret",
			matches: true,
		},
		{
			name:    "no secret keyword",
			input:   "username=john",
			matches: false,
		},
	}

	pattern := mustGetPattern(t, "Generic Secret")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if pattern.Regex.MatchString(tt.input) != tt.matches {
				t.Errorf("Generic Secret pattern.MatchString(%q) = %v, want %v",
					tt.input, !tt.matches, tt.matches)
			}
		})
	}
}

func TestPatterns_GitHubToken(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		matches bool
	}{
		{
			name:    "valid github personal access token",
			input:   "ghp_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
			matches: true,
		},
		{
			name:    "github token in env",
			input:   "GITHUB_TOKEN=ghp_abcdefghijklmnopqrstuvwxyz0123456789",
			matches: true,
		},
		{
			name:    "too short",
			input:   "ghp_short",
			matches: false,
		},
		{
			name:    "wrong prefix",
			input:   "ghi_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
			matches: false,
		},
	}

	pattern := mustGetPattern(t, "GitHub Token")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if pattern.Regex.MatchString(tt.input) != tt.matches {
				t.Errorf("GitHub Token pattern.MatchString(%q) = %v, want %v",
					tt.input, !tt.matches, tt.matches)
			}
		})
	}
}
