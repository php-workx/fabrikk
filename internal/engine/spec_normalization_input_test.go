package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/php-workx/fabrikk/internal/state"
)

const (
	specNormalizationSchemaVersion = "0.1"
	specNormalizationTestRunID     = "run-123"
)

func TestBuildSpecNormalizationSourceBundleSingleFile(t *testing.T) {
	dir := t.TempDir()
	spec := writeSpecInput(t, dir, "spec.md", "First line\nSecond line\n")

	bundle, err := buildSpecNormalizationSourceBundle(specNormalizationTestRunID, []string{spec}, 1024)
	if err != nil {
		t.Fatalf("buildSpecNormalizationSourceBundle: %v", err)
	}

	if bundle.Manifest.SchemaVersion != specNormalizationSchemaVersion || bundle.Manifest.RunID != specNormalizationTestRunID {
		t.Fatalf("manifest identity = %+v", bundle.Manifest)
	}
	if len(bundle.Manifest.Sources) != 1 {
		t.Fatalf("got %d sources, want 1", len(bundle.Manifest.Sources))
	}
	source := bundle.Manifest.Sources[0]
	if source.Path != spec {
		t.Fatalf("source path = %q, want %q", source.Path, spec)
	}
	if source.Fingerprint == "" {
		t.Fatal("source fingerprint is empty")
	}
	if source.ByteSize != int64(len("First line\nSecond line\n")) {
		t.Fatalf("byte size = %d, want %d", source.ByteSize, len("First line\nSecond line\n"))
	}
	if source.LineCount != 2 {
		t.Fatalf("line count = %d, want 2", source.LineCount)
	}
	for _, want := range []string{"1 | First line", "2 | Second line"} {
		if !strings.Contains(source.LineNumberedText, want) {
			t.Fatalf("line-numbered source missing %q:\n%s", want, source.LineNumberedText)
		}
		if !strings.Contains(bundle.PromptInput, want) {
			t.Fatalf("prompt input missing %q:\n%s", want, bundle.PromptInput)
		}
	}
	if !strings.HasPrefix(bundle.ManifestHash, "sha256:") {
		t.Fatalf("manifest hash = %q, want sha256 prefix", bundle.ManifestHash)
	}
}

func TestBuildSpecNormalizationSourceBundleMultiFileHashStable(t *testing.T) {
	dir := t.TempDir()
	spec1 := writeSpecInput(t, dir, "a.md", "Alpha\n")
	spec2 := writeSpecInput(t, dir, "b.md", "Beta\nGamma\n")

	first, err := buildSpecNormalizationSourceBundle(specNormalizationTestRunID, []string{spec1, spec2}, 1024)
	if err != nil {
		t.Fatalf("first buildSpecNormalizationSourceBundle: %v", err)
	}
	second, err := buildSpecNormalizationSourceBundle(specNormalizationTestRunID, []string{spec1, spec2}, 1024)
	if err != nil {
		t.Fatalf("second buildSpecNormalizationSourceBundle: %v", err)
	}

	if first.ManifestHash != second.ManifestHash {
		t.Fatalf("manifest hashes differ: %q != %q", first.ManifestHash, second.ManifestHash)
	}
	if first.PromptInput != second.PromptInput {
		t.Fatal("prompt input is not stable across identical inputs")
	}
	if len(first.Manifest.Sources) != 2 {
		t.Fatalf("got %d sources, want 2", len(first.Manifest.Sources))
	}
	if first.Manifest.Sources[0].Path != spec1 || first.Manifest.Sources[1].Path != spec2 {
		t.Fatalf("source order = %+v, want input order", first.Manifest.Sources)
	}
}

func TestSourceManifestHashIgnoresLineNumberedText(t *testing.T) {
	dir := t.TempDir()
	spec := writeSpecInput(t, dir, "spec.md", "Alpha\n")

	bundle, err := buildSpecNormalizationSourceBundle(specNormalizationTestRunID, []string{spec}, 1024)
	if err != nil {
		t.Fatalf("buildSpecNormalizationSourceBundle: %v", err)
	}
	changed := *bundle.Manifest
	changed.Sources = append([]state.SourceManifestEntry(nil), bundle.Manifest.Sources...)
	changed.Sources[0].LineNumberedText = "1 | Tampered audit text"
	hash, err := hashSpecNormalizationSourceManifest(&changed)
	if err != nil {
		t.Fatalf("hashSpecNormalizationSourceManifest: %v", err)
	}

	if hash != bundle.ManifestHash {
		t.Fatalf("manifest hash changed with line-numbered text: %q != %q", hash, bundle.ManifestHash)
	}
}

func TestBuildSpecNormalizationSourceBundleRejectsOversizedInput(t *testing.T) {
	dir := t.TempDir()
	spec := writeSpecInput(t, dir, "large.md", strings.Repeat("x", 64))

	_, err := buildSpecNormalizationSourceBundle(specNormalizationTestRunID, []string{spec}, 8)
	if err == nil {
		t.Fatal("expected oversized source bundle error")
	}
	for _, want := range []string{"source bundle", "limit", "split", "explicit AT-* requirement IDs"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("oversized error %q missing %q", err.Error(), want)
		}
	}
}

func TestBuildSpecNormalizationSourceBundleAcceptsLongLines(t *testing.T) {
	dir := t.TempDir()
	longLine := strings.Repeat("x", 70*1024)
	spec := writeSpecInput(t, dir, "long.md", longLine+"\n")

	bundle, err := buildSpecNormalizationSourceBundle(specNormalizationTestRunID, []string{spec}, int64(len(longLine)+1024))
	if err != nil {
		t.Fatalf("buildSpecNormalizationSourceBundle: %v", err)
	}

	source := bundle.Manifest.Sources[0]
	if source.LineCount != 1 {
		t.Fatalf("line count = %d, want 1", source.LineCount)
	}
	if !strings.Contains(source.LineNumberedText, "1 | "+longLine) {
		t.Fatal("line-numbered text does not contain the long source line")
	}
}

func writeSpecInput(t *testing.T, dir, name, content string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
