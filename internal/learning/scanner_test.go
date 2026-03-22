package learning

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanDetectsAPIKeys(t *testing.T) {
	cs := NewContentScanner("")
	redacted, was := cs.Scan("Found api_key=sk-abc123def in config")
	if !was {
		t.Error("expected redaction for API key")
	}
	if redacted == "Found api_key=sk-abc123def in config" {
		t.Error("content should be redacted")
	}
	if !contains(redacted, "[REDACTED]") {
		t.Errorf("expected [REDACTED] in output, got %q", redacted)
	}
}

func TestScanDetectsPasswords(t *testing.T) {
	cs := NewContentScanner("")
	redacted, was := cs.Scan("password=hunter2 in env")
	if !was {
		t.Error("expected redaction for password")
	}
	if !contains(redacted, "[REDACTED]") {
		t.Errorf("expected [REDACTED], got %q", redacted)
	}
}

func TestScanDetectsTokens(t *testing.T) {
	cs := NewContentScanner("")
	redacted, was := cs.Scan("bearer=ghp_xxxx1234 in header")
	if !was {
		t.Error("expected redaction for token")
	}
	if !contains(redacted, "[REDACTED]") {
		t.Errorf("expected [REDACTED], got %q", redacted)
	}
}

func TestScanDetectsAWSKeys(t *testing.T) {
	cs := NewContentScanner("")
	redacted, was := cs.Scan("key is AKIAIOSFODNN7EXAMPLE")
	if !was {
		t.Error("expected redaction for AWS key")
	}
	if !contains(redacted, "[REDACTED]") {
		t.Errorf("expected [REDACTED], got %q", redacted)
	}
}

func TestScanDetectsLocalPaths(t *testing.T) {
	cs := NewContentScanner("")
	redacted, was := cs.Scan("file at /Users/jane/projects/secret/main.go")
	if !was {
		t.Error("expected redaction for local path")
	}
	if !contains(redacted, "[REDACTED]") {
		t.Errorf("expected [REDACTED], got %q", redacted)
	}
}

func TestScanDetectsPrivateIPs(t *testing.T) {
	cs := NewContentScanner("")
	redacted, was := cs.Scan("server at 192.168.1.100")
	if !was {
		t.Error("expected redaction for private IP")
	}
	if !contains(redacted, "[REDACTED]") {
		t.Errorf("expected [REDACTED], got %q", redacted)
	}
}

func TestScanDetectsURLsWithCredentials(t *testing.T) {
	cs := NewContentScanner("")
	redacted, was := cs.Scan("db at https://user:pass@db.internal.com/mydb")
	if !was {
		t.Error("expected redaction for URL with credentials")
	}
	if !contains(redacted, "[REDACTED]") {
		t.Errorf("expected [REDACTED], got %q", redacted)
	}
}

func TestScanDetectsPrivateKeyHeader(t *testing.T) {
	cs := NewContentScanner("")
	redacted, was := cs.Scan("found -----BEGIN RSA PRIVATE KEY----- in file")
	if !was {
		t.Error("expected redaction for private key header")
	}
	if !contains(redacted, "[REDACTED]") {
		t.Errorf("expected [REDACTED], got %q", redacted)
	}
}

func TestScanPreservesCleanContent(t *testing.T) {
	cs := NewContentScanner("")
	content := "Use flock for concurrent file access in internal/ticket"
	redacted, was := cs.Scan(content)
	if was {
		t.Error("clean content should not be redacted")
	}
	if redacted != content {
		t.Errorf("clean content changed: %q → %q", content, redacted)
	}
}

func TestScanCustomTerms(t *testing.T) {
	dir := t.TempDir()
	termsPath := filepath.Join(dir, "scan-terms.txt")
	if err := os.WriteFile(termsPath, []byte("acme-corp\nproject-phoenix\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cs := NewContentScanner(termsPath)
	redacted, was := cs.Scan("deployed to acme-corp staging")
	if !was {
		t.Error("expected redaction for custom term")
	}
	if contains(redacted, "acme-corp") {
		t.Errorf("custom term should be redacted, got %q", redacted)
	}
}

func TestScanCustomTermsMissingFile(t *testing.T) {
	// Should not panic or error.
	cs := NewContentScanner("/nonexistent/path/scan-terms.txt")
	content := "normal content"
	redacted, was := cs.Scan(content)
	if was {
		t.Error("should not redact with missing terms file")
	}
	if redacted != content {
		t.Error("content should be unchanged")
	}
}
