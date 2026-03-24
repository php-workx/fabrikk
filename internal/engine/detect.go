package engine

import (
	"os"
	"path/filepath"

	"github.com/php-workx/fabrikk/internal/state"
)

// detectQualityGate looks for a Justfile or Makefile with a check target (spec section 11.2).
func detectQualityGate(workDir string) *state.QualityGate {
	// Priority order per spec: Justfile, Makefile, .fabrikk/quality-gate.sh
	candidates := []struct {
		file    string
		command string
	}{
		{"Justfile", "just check"},
		{"Makefile", "make check"},
		{".fabrikk/quality-gate.sh", ".fabrikk/quality-gate.sh"},
	}

	for _, c := range candidates {
		path := filepath.Join(workDir, c.file)
		if _, err := os.Stat(path); err == nil {
			return &state.QualityGate{
				Command:        c.command,
				TimeoutSeconds: 300,
				Required:       true,
			}
		}
	}

	return nil
}
