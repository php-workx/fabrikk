// Package compiler implements deterministic task compilation from specs.
package compiler

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/php-workx/fabrikk/internal/state"
)

// requirementPattern matches requirement IDs like AT-FR-001, AT-TS-001, AT-NFR-001, AT-AS-001.
var requirementPattern = regexp.MustCompile(`\*\*(AT-(?:FR|TS|NFR|AS)-\d{3})\*\*[:\s]*(.+)`)

// IngestSpec reads a spec file and extracts requirements with stable IDs.
func IngestSpec(path string) ([]state.Requirement, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open spec %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	var reqs []state.Requirement
	seen := make(map[string]bool)
	scanner := bufio.NewScanner(f)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		matches := requirementPattern.FindStringSubmatch(line)
		if len(matches) < 3 {
			continue
		}

		id := matches[1]
		text := strings.TrimSpace(matches[2])

		if seen[id] {
			return nil, fmt.Errorf("duplicate requirement ID %s at %s:%d", id, path, lineNum)
		}
		seen[id] = true

		reqs = append(reqs, state.Requirement{
			ID:         id,
			Text:       text,
			SourceSpec: path,
			SourceLine: lineNum,
		})
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", path, err)
	}

	return reqs, nil
}

// IngestSpecs reads multiple spec files and returns all requirements and source spec metadata.
func IngestSpecs(paths []string) ([]state.Requirement, []state.SourceSpec, error) {
	var allReqs []state.Requirement
	var sources []state.SourceSpec
	globalSeen := make(map[string]string) // id -> source path

	for _, path := range paths {
		reqs, err := IngestSpec(path)
		if err != nil {
			return nil, nil, err
		}

		for _, r := range reqs {
			if prev, exists := globalSeen[r.ID]; exists {
				return nil, nil, fmt.Errorf("requirement %s found in both %s and %s", r.ID, prev, path)
			}
			globalSeen[r.ID] = path
		}
		allReqs = append(allReqs, reqs...)

		fingerprint, err := state.SHA256File(path)
		if err != nil {
			return nil, nil, fmt.Errorf("hash %s: %w", path, err)
		}
		sources = append(sources, state.SourceSpec{
			Path:        path,
			Fingerprint: fingerprint,
		})
	}

	return allReqs, sources, nil
}
