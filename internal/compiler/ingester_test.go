package compiler_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/php-workx/fabrikk/internal/compiler"
)

func TestIngestSpec(t *testing.T) {
	dir := t.TempDir()
	spec := filepath.Join(dir, "spec.md")

	content := `# Test Spec

## Requirements

- **AT-FR-001**: The system must do X.
- **AT-FR-002**: The system must do Y.
- **AT-TS-001**: Acceptance scenario one.
`
	if err := os.WriteFile(spec, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	reqs, err := compiler.IngestSpec(spec)
	if err != nil {
		t.Fatalf("IngestSpec: %v", err)
	}

	if len(reqs) != 3 {
		t.Fatalf("got %d requirements, want 3", len(reqs))
	}

	if reqs[0].ID != "AT-FR-001" {
		t.Errorf("first req ID = %q, want AT-FR-001", reqs[0].ID)
	}
	if reqs[0].Text != "The system must do X." {
		t.Errorf("first req text = %q", reqs[0].Text)
	}
	if reqs[0].SourceSpec != spec {
		t.Errorf("source spec = %q", reqs[0].SourceSpec)
	}
}

func TestIngestSpecDuplicateID(t *testing.T) {
	dir := t.TempDir()
	spec := filepath.Join(dir, "spec.md")

	content := `# Test
- **AT-FR-001**: First.
- **AT-FR-001**: Duplicate.
`
	if err := os.WriteFile(spec, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := compiler.IngestSpec(spec)
	if err == nil {
		t.Fatal("expected error for duplicate ID")
	}
}

func TestIngestSpecsMultipleFiles(t *testing.T) {
	dir := t.TempDir()

	spec1 := filepath.Join(dir, "func.md")
	spec2 := filepath.Join(dir, "tech.md")

	if err := os.WriteFile(spec1, []byte("- **AT-FR-001**: Func req.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(spec2, []byte("- **AT-TS-001**: Tech scenario.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	reqs, sources, err := compiler.IngestSpecs([]string{spec1, spec2})
	if err != nil {
		t.Fatalf("IngestSpecs: %v", err)
	}

	if len(reqs) != 2 {
		t.Fatalf("got %d reqs, want 2", len(reqs))
	}
	if len(sources) != 2 {
		t.Fatalf("got %d sources, want 2", len(sources))
	}
	if sources[0].Fingerprint == "" {
		t.Error("source fingerprint is empty")
	}
}

func TestIngestSpecsCrossFileDuplicate(t *testing.T) {
	dir := t.TempDir()

	spec1 := filepath.Join(dir, "a.md")
	spec2 := filepath.Join(dir, "b.md")

	if err := os.WriteFile(spec1, []byte("- **AT-FR-001**: In file A.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(spec2, []byte("- **AT-FR-001**: In file B.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := compiler.IngestSpecs([]string{spec1, spec2})
	if err == nil {
		t.Fatal("expected error for cross-file duplicate")
	}
}
