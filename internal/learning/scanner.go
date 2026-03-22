package learning

import (
	"os"
	"regexp"
	"strings"
)

// ContentScanner scans learning content for sensitive patterns and redacts them.
type ContentScanner struct {
	patterns    []*regexp.Regexp
	customTerms []string
}

// Built-in patterns for sensitive content detection.
//
//nolint:gochecknoglobals // compiled once, read-only after init
var defaultPatterns = []*regexp.Regexp{
	// API keys, secret keys, access keys.
	regexp.MustCompile(`(?i)(api[_-]?key|secret[_-]?key|access[_-]?key)\s*[:=]\s*\S+`),
	// Passwords.
	regexp.MustCompile(`(?i)(password|passwd|pwd)\s*[:=]\s*\S+`),
	// Tokens (bearer, auth).
	regexp.MustCompile(`(?i)(token|bearer)\s*[:=]\s*\S+`),
	// AWS access key IDs.
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	// Private key headers.
	regexp.MustCompile(`-{5}BEGIN (RSA|EC|OPENSSH) PRIVATE KEY-{5}`),
	// URLs with embedded credentials.
	regexp.MustCompile(`https?://[^:]+:[^@]+@`),
	// Local filesystem paths.
	regexp.MustCompile(`/Users/\S+|/home/\S+|C:\\\\Users\\\\\S+`),
	// Private IP addresses.
	regexp.MustCompile(`\b(?:10|172\.(?:1[6-9]|2\d|3[01])|192\.168)\.\d+\.\d+\b`),
}

// NewContentScanner creates a scanner with built-in patterns.
// If customTermsPath is non-empty and the file exists, custom terms are loaded
// (one per line, case-insensitive). Missing file is silently ignored.
func NewContentScanner(customTermsPath string) *ContentScanner {
	cs := &ContentScanner{
		patterns: defaultPatterns,
	}
	if customTermsPath != "" {
		cs.loadCustomTerms(customTermsPath)
	}
	return cs
}

func (cs *ContentScanner) loadCustomTerms(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return // missing file is silently ignored
	}
	for _, line := range strings.Split(string(data), "\n") {
		term := strings.TrimSpace(line)
		if term != "" && !strings.HasPrefix(term, "#") {
			cs.customTerms = append(cs.customTerms, term)
		}
	}
}

// Scan checks content for sensitive patterns. Returns redacted content
// and whether any redactions were applied.
func (cs *ContentScanner) Scan(content string) (string, bool) {
	redacted := false
	result := content

	for _, p := range cs.patterns {
		if p.MatchString(result) {
			result = p.ReplaceAllString(result, "[REDACTED]")
			redacted = true
		}
	}

	for _, term := range cs.customTerms {
		lower := strings.ToLower(result)
		termLower := strings.ToLower(term)
		if strings.Contains(lower, termLower) {
			// Case-insensitive replacement.
			idx := strings.Index(lower, termLower)
			for idx >= 0 {
				result = result[:idx] + "[REDACTED]" + result[idx+len(term):]
				lower = strings.ToLower(result)
				redacted = true
				idx = strings.Index(lower, termLower)
			}
		}
	}

	return result, redacted
}
