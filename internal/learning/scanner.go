package learning

import (
	"fmt"
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
	regexp.MustCompile(`/Users/\S+|/home/\S+|C:\\Users\\\S+`),
	// Private IP addresses (10/8, 172.16-31/12, 192.168/16).
	regexp.MustCompile(`\b(?:10\.\d+\.\d+\.\d+|172\.(?:1[6-9]|2\d|3[01])\.\d+\.\d+|192\.168\.\d+\.\d+)\b`),
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
		if !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "warning: failed to read custom scan terms %s: %v\n", path, err)
		}
		return
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
		termLower := strings.ToLower(term)
		// Build result by scanning from left to right, skipping past replacements.
		var out strings.Builder
		lower := strings.ToLower(result)
		pos := 0
		found := false
		for {
			idx := strings.Index(lower[pos:], termLower)
			if idx < 0 {
				break
			}
			out.WriteString(result[pos : pos+idx])
			out.WriteString("[REDACTED]")
			pos += idx + len(term)
			found = true
		}
		if found {
			out.WriteString(result[pos:])
			result = out.String()
			redacted = true
		}
	}

	return result, redacted
}
