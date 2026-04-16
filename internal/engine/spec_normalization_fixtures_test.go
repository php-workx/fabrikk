package engine

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSpecNormalizationFixturesBuildSourceBundles(t *testing.T) {
	fixtures := []string{
		"prose.md",
		"goals-non-goals.md",
		"assumptions.md",
		"table.md",
		"ambiguous-and-unsupported.md",
	}

	paths := make([]string, 0, len(fixtures))
	for _, fixture := range fixtures {
		paths = append(paths, filepath.Join("testdata", "spec_normalization", fixture))
	}

	bundle, err := buildSpecNormalizationSourceBundle("run-fixtures", paths, 16<<10)
	if err != nil {
		t.Fatalf("buildSpecNormalizationSourceBundle: %v", err)
	}
	if len(bundle.Manifest.Sources) != len(fixtures) {
		t.Fatalf("sources = %d, want %d", len(bundle.Manifest.Sources), len(fixtures))
	}
	for _, want := range []string{
		"preserve their title",
		"# Non-goals",
		"consent source",
		"| Approval | Refuse stale normalized artifacts. |",
		"must not claim that production deployment is in scope",
	} {
		if !strings.Contains(bundle.PromptInput, want) {
			t.Fatalf("prompt input missing %q:\n%s", want, bundle.PromptInput)
		}
	}
}

func TestSpecNormalizationSourceBundleOversizedFixtureGuard(t *testing.T) {
	path := filepath.Join("testdata", "spec_normalization", "prose.md")
	_, err := buildSpecNormalizationSourceBundle("run-fixtures", []string{path}, 8)
	if err == nil {
		t.Fatal("expected oversized source bundle error")
	}
	if !strings.Contains(err.Error(), "explicit AT-* requirement IDs") {
		t.Fatalf("error = %q, want explicit ID guidance", err.Error())
	}
}
