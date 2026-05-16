// Package sanitize provides best-effort sanitization of sensitive data
// before sending to AI providers.
package sanitize

import "regexp"

// Pattern represents a compiled regex pattern for secret detection
type Pattern struct {
	Name        string
	Regex       *regexp.Regexp
	Replacement string
}

// secretPatterns contains compiled regex patterns for detecting sensitive data
// These patterns are applied before any AI provider calls
var secretPatterns = []Pattern{
	{
		Name:        "AWS Access Key",
		Regex:       regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
		Replacement: "[AWS_ACCESS_KEY_REDACTED]",
	},
	{
		Name:        "AWS Secret Key",
		Regex:       regexp.MustCompile(`(?i)(aws_secret_access_key|secret_access_key)\s*[=:]\s*\S+`),
		Replacement: "$1=[AWS_SECRET_REDACTED]",
	},
	{
		Name:        "JWT Token",
		Regex:       regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`),
		Replacement: "[JWT_REDACTED]",
	},
	{
		Name:        "Slack Token",
		Regex:       regexp.MustCompile(`xox[baprs]-[0-9a-zA-Z-]+`),
		Replacement: "[SLACK_TOKEN_REDACTED]",
	},
	{
		Name:        "PEM Block",
		Regex:       regexp.MustCompile(`-{5}BEGIN [A-Z ]+-{5}[\s\S]+?-{5}END [A-Z ]+-{5}`),
		Replacement: "[PEM_BLOCK_REDACTED]",
	},
	{
		Name:        "Generic Secret",
		Regex:       regexp.MustCompile(`(?i)(password|token|secret|api_key)\s*[=:]\s*\S+`),
		Replacement: "$1=[REDACTED]",
	},
	{
		Name:        "GitHub Token",
		Regex:       regexp.MustCompile(`ghp_[A-Za-z0-9]{36}`),
		Replacement: "[GITHUB_TOKEN_REDACTED]",
	},
	{
		Name:        "GitHub OAuth",
		Regex:       regexp.MustCompile(`gho_[A-Za-z0-9]{36}`),
		Replacement: "[GITHUB_OAUTH_REDACTED]",
	},
	{
		Name:        "Private Key Inline",
		Regex:       regexp.MustCompile(`(?i)(private[_-]?key)\s*[=:]\s*\S+`),
		Replacement: "$1=[PRIVATE_KEY_REDACTED]",
	},
	{
		Name:        "Bearer Token (JWT)",
		Regex:       regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`),
		Replacement: "Bearer [TOKEN_REDACTED]",
	},
	{
		Name:        "Bearer Token (Generic)",
		Regex:       regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9_-]{20,}`),
		Replacement: "Bearer [TOKEN_REDACTED]",
	},
	{
		Name:        "Basic Auth",
		Regex:       regexp.MustCompile(`(?i)basic\s+[A-Za-z0-9+/=]{20,}`),
		Replacement: "Basic [CREDENTIALS_REDACTED]",
	},
}

// GetSecretPatterns returns a copy of the secret detection patterns list.
// A copy is returned to prevent callers from mutating the internal patterns.
func GetSecretPatterns() []Pattern {
	result := make([]Pattern, len(secretPatterns))
	copy(result, secretPatterns)
	return result
}
