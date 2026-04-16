package compiler_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/php-workx/fabrikk/internal/compiler"
)

const (
	testReqIDFR001 = "AT-FR-001"
	testReqIDFR002 = "AT-FR-002"
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

	if reqs[0].ID != testReqIDFR001 {
		t.Errorf("first req ID = %q, want AT-FR-001", reqs[0].ID)
	}
	if reqs[0].Text != "The system must do X." {
		t.Errorf("first req text = %q", reqs[0].Text)
	}
	if reqs[0].SourceSpec != spec {
		t.Errorf("source spec = %q", reqs[0].SourceSpec)
	}
}

func TestIngestSpecWithFallbackSynthesizesRequirementIDsForFreeFormGoals(t *testing.T) {
	dir := t.TempDir()
	spec := filepath.Join(dir, "free-form.md")

	content := `# Free Form Spec

## Goals, non-goals, assumptions

### Goals

1. Accept source specs that do not already contain requirement IDs.
2. Generate stable implementation work from normal Markdown prose
   without requiring authors to rewrite their documents first.

### Non-goals

- Replace human review.
`
	if err := os.WriteFile(spec, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	reqs, err := compiler.IngestSpecWithFallback(spec)
	if err != nil {
		t.Fatalf("IngestSpecWithFallback: %v", err)
	}

	if len(reqs) != 2 {
		t.Fatalf("got %d requirements, want 2", len(reqs))
	}
	if reqs[0].ID != testReqIDFR001 {
		t.Errorf("first req ID = %q, want AT-FR-001", reqs[0].ID)
	}
	if reqs[1].ID != testReqIDFR002 {
		t.Errorf("second req ID = %q, want AT-FR-002", reqs[1].ID)
	}
	wantText := "Generate stable implementation work from normal Markdown prose without requiring authors to rewrite their documents first."
	if reqs[1].Text != wantText {
		t.Errorf("second req text = %q, want %q", reqs[1].Text, wantText)
	}
}

func TestIngestSpecsDoesNotSynthesizeFreeFormByDefault(t *testing.T) {
	dir := t.TempDir()
	spec := writeSpec(t, dir, "free-form.md", "## Goals\n\n1. The system must accept prose specs.\n")

	reqs, sources, err := compiler.IngestSpecs([]string{spec})
	if err != nil {
		t.Fatalf("IngestSpecs: %v", err)
	}
	if len(reqs) != 0 {
		t.Fatalf("got %d reqs, want 0: %+v", len(reqs), reqs)
	}
	if len(sources) != 1 || sources[0].Fingerprint == "" {
		t.Fatalf("sources = %+v, want one fingerprinted source", sources)
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

func TestIngestSpecsWithFallbackRenumbersSynthesizedIDsAcrossMultipleFiles(t *testing.T) {
	dir := t.TempDir()

	spec1 := filepath.Join(dir, "a.md")
	spec2 := filepath.Join(dir, "b.md")

	if err := os.WriteFile(spec1, []byte("## Goals\n\n1. Build the first capability.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(spec2, []byte("## Goals\n\n1. Build the second capability.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	reqs, _, err := compiler.IngestSpecsWithFallback([]string{spec1, spec2})
	if err != nil {
		t.Fatalf("IngestSpecsWithFallback: %v", err)
	}
	if len(reqs) != 2 {
		t.Fatalf("got %d reqs, want 2", len(reqs))
	}
	if reqs[0].ID != testReqIDFR001 || reqs[1].ID != testReqIDFR002 {
		t.Fatalf("IDs = %q, %q; want AT-FR-001, AT-FR-002", reqs[0].ID, reqs[1].ID)
	}
}

func TestIngestSpecsWithFallbackSkipsOccupiedSyntheticIDs(t *testing.T) {
	dir := t.TempDir()

	var explicit strings.Builder
	for i := 1; i <= 999; i++ {
		fmt.Fprintf(&explicit, "- **AT-FR-%03d**: Explicit requirement %d.\n", i, i)
	}
	explicitSpec := writeSpec(t, dir, "explicit.md", explicit.String())
	freeFormSpec := writeSpec(t, dir, "free-form.md", "## Goals\n\n1. Build the overflow capability.\n")

	reqs, _, err := compiler.IngestSpecsWithFallback([]string{explicitSpec, freeFormSpec})
	if err != nil {
		t.Fatalf("IngestSpecsWithFallback: %v", err)
	}
	if got := reqs[len(reqs)-1].ID; got != "AT-FR-1000" {
		t.Fatalf("synthetic ID = %q, want AT-FR-1000", got)
	}
}

func TestIngestSpecsWithFallbackReservesExplicitIDsBeforeSynthesis(t *testing.T) {
	dir := t.TempDir()

	freeFormSpec := writeSpec(t, dir, "free-form.md", "## Goals\n\n1. Build the free-form capability.\n")
	explicitSpec := writeSpec(t, dir, "explicit.md", "- **AT-FR-001**: Preserve the explicit requirement.\n")

	reqs, _, err := compiler.IngestSpecsWithFallback([]string{freeFormSpec, explicitSpec})
	if err != nil {
		t.Fatalf("IngestSpecsWithFallback: %v", err)
	}
	if len(reqs) != 2 {
		t.Fatalf("got %d reqs, want 2", len(reqs))
	}
	if reqs[0].ID != testReqIDFR002 {
		t.Fatalf("free-form req ID = %q, want AT-FR-002 because AT-FR-001 is reserved", reqs[0].ID)
	}
	if reqs[1].ID != testReqIDFR001 {
		t.Fatalf("explicit req ID = %q, want AT-FR-001", reqs[1].ID)
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

func TestHasExplicitRequirementIDs(t *testing.T) {
	dir := t.TempDir()

	explicit := writeSpec(t, dir, "explicit.md", "- **AT-FR-001**: The system must do X.\n")
	freeForm := writeSpec(t, dir, "free-form.md", "## Goals\n\n1. The system must accept prose specs.\n")
	malformed := writeSpec(t, dir, "malformed.md", "- **AT-FR-1**: Missing zero padding.\n")
	duplicate := writeSpec(t, dir, "duplicate.md", "- **AT-FR-001**: First.\n- **AT-FR-001**: Again.\n")

	got, err := compiler.HasExplicitRequirementIDs(explicit)
	if err != nil {
		t.Fatalf("HasExplicitRequirementIDs(explicit): %v", err)
	}
	if !got {
		t.Fatal("HasExplicitRequirementIDs(explicit) = false, want true")
	}

	got, err = compiler.HasExplicitRequirementIDs(freeForm)
	if err != nil {
		t.Fatalf("HasExplicitRequirementIDs(freeForm): %v", err)
	}
	if got {
		t.Fatal("HasExplicitRequirementIDs(freeForm) = true, want false")
	}

	got, err = compiler.HasExplicitRequirementIDs(malformed)
	if err != nil {
		t.Fatalf("HasExplicitRequirementIDs(malformed): %v", err)
	}
	if got {
		t.Fatal("HasExplicitRequirementIDs(malformed) = true, want false")
	}

	if _, err := compiler.HasExplicitRequirementIDs(duplicate); err == nil {
		t.Fatal("expected duplicate explicit ID error")
	}
}

func TestScanExplicitRequirementIDsDetectsMixedInputs(t *testing.T) {
	dir := t.TempDir()

	explicit := writeSpec(t, dir, "explicit.md", "- **AT-FR-001**: The system must do X.\n")
	freeForm := writeSpec(t, dir, "free-form.md", "## Goals\n\n1. The system must accept prose specs.\n")

	scan, err := compiler.ScanExplicitRequirementIDs([]string{explicit, freeForm})
	if err != nil {
		t.Fatalf("ScanExplicitRequirementIDs: %v", err)
	}

	if !scan.HasExplicitIDs {
		t.Fatal("HasExplicitIDs = false, want true")
	}
	if scan.AllFilesHaveExplicitIDs {
		t.Fatal("AllFilesHaveExplicitIDs = true, want false")
	}
	if !scan.MixedExplicitAndFreeForm {
		t.Fatal("MixedExplicitAndFreeForm = false, want true")
	}
	if len(scan.Files) != 2 {
		t.Fatalf("got %d file scans, want 2", len(scan.Files))
	}
	if len(scan.Files[0].IDs) != 1 || scan.Files[0].IDs[0] != testReqIDFR001 {
		t.Fatalf("explicit file IDs = %+v, want [AT-FR-001]", scan.Files[0].IDs)
	}
	if len(scan.Files[1].IDs) != 0 {
		t.Fatalf("free-form file IDs = %+v, want empty", scan.Files[1].IDs)
	}
}

func TestScanExplicitRequirementIDsRejectsCrossFileDuplicates(t *testing.T) {
	dir := t.TempDir()

	spec1 := writeSpec(t, dir, "a.md", "- **AT-FR-001**: In file A.\n")
	spec2 := writeSpec(t, dir, "b.md", "- **AT-FR-001**: In file B.\n")

	if _, err := compiler.ScanExplicitRequirementIDs([]string{spec1, spec2}); err == nil {
		t.Fatal("expected cross-file duplicate error")
	}
}

func TestIngestExplicitRequirementSpecsDoesNotSynthesizeFreeForm(t *testing.T) {
	dir := t.TempDir()

	freeForm := writeSpec(t, dir, "free-form.md", "## Goals\n\n1. The system must accept prose specs.\n")

	_, _, err := compiler.IngestExplicitRequirementSpecs([]string{freeForm})
	if err == nil {
		t.Fatal("expected no explicit requirement IDs error")
	}
}

func TestIngestExplicitRequirementSpecsPreservesExistingExtraction(t *testing.T) {
	dir := t.TempDir()

	spec1 := writeSpec(t, dir, "func.md", "- **AT-FR-001**: Func req.\n")
	spec2 := writeSpec(t, dir, "tech.md", "- **AT-TS-001**: Tech scenario.\n")

	reqs, sources, err := compiler.IngestExplicitRequirementSpecs([]string{spec1, spec2})
	if err != nil {
		t.Fatalf("IngestExplicitRequirementSpecs: %v", err)
	}
	if len(reqs) != 2 {
		t.Fatalf("got %d reqs, want 2", len(reqs))
	}
	if reqs[0].ID != testReqIDFR001 || reqs[1].ID != "AT-TS-001" {
		t.Fatalf("IDs = %q, %q; want AT-FR-001, AT-TS-001", reqs[0].ID, reqs[1].ID)
	}
	if len(sources) != 2 {
		t.Fatalf("got %d sources, want 2", len(sources))
	}
	if sources[0].Fingerprint == "" || sources[1].Fingerprint == "" {
		t.Fatalf("source fingerprints must be populated: %+v", sources)
	}
}

func writeSpec(t *testing.T, dir, name, content string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
