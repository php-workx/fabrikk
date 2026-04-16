// Package compiler implements deterministic task compilation from specs.
package compiler

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
	"unicode"

	"github.com/php-workx/fabrikk/internal/state"
)

// requirementPattern matches requirement IDs like AT-FR-001, AT-TS-001, AT-NFR-001, AT-AS-001.
var (
	requirementPattern      = regexp.MustCompile(`\*\*(AT-(?:FR|TS|NFR|AS)-\d{3})\*\*[:\s]*(.+)`)
	markdownHeadingPattern  = regexp.MustCompile(`^\s{0,3}(#{1,6})\s+(.+?)\s*$`)
	markdownListItemPattern = regexp.MustCompile(`^\s*(?:[-*+]\s+|\d+[.)]\s+)(.+)$`)
)

// ExplicitRequirementIDFileScan records explicit stable IDs found in one spec file.
type ExplicitRequirementIDFileScan struct {
	Path           string
	IDs            []string
	HasExplicitIDs bool
}

// ExplicitRequirementIDScan summarizes explicit stable IDs across spec files.
type ExplicitRequirementIDScan struct {
	Files                    []ExplicitRequirementIDFileScan
	IDs                      []string
	HasExplicitIDs           bool
	AllFilesHaveExplicitIDs  bool
	MixedExplicitAndFreeForm bool
}

// IngestSpec reads a spec file and extracts explicit requirements with stable IDs.
func IngestSpec(path string) ([]state.Requirement, error) {
	reqs, _, err := parseExplicitRequirements(path)
	return reqs, err
}

// IngestSpecWithFallback reads a spec file and may synthesize requirements from free-form Markdown.
func IngestSpecWithFallback(path string) ([]state.Requirement, error) {
	reqs, _, err := ingestSpecWithFallback(path)
	return reqs, err
}

// HasExplicitRequirementIDs reports whether a spec contains explicit stable requirement IDs.
func HasExplicitRequirementIDs(path string) (bool, error) {
	reqs, _, err := parseExplicitRequirements(path)
	if err != nil {
		return false, err
	}
	return len(reqs) > 0, nil
}

// ScanExplicitRequirementIDs scans specs for explicit stable IDs without synthesizing requirements.
func ScanExplicitRequirementIDs(paths []string) (ExplicitRequirementIDScan, error) {
	scan := ExplicitRequirementIDScan{
		Files:                   make([]ExplicitRequirementIDFileScan, 0, len(paths)),
		AllFilesHaveExplicitIDs: len(paths) > 0,
	}
	globalSeen := make(map[string]string)

	for _, path := range paths {
		reqs, _, err := parseExplicitRequirements(path)
		if err != nil {
			return ExplicitRequirementIDScan{}, err
		}

		fileScan := ExplicitRequirementIDFileScan{
			Path:           path,
			IDs:            make([]string, 0, len(reqs)),
			HasExplicitIDs: len(reqs) > 0,
		}
		if len(reqs) == 0 {
			scan.AllFilesHaveExplicitIDs = false
		}

		for _, req := range reqs {
			if prev, exists := globalSeen[req.ID]; exists {
				return ExplicitRequirementIDScan{}, fmt.Errorf("requirement %s found in both %s and %s", req.ID, prev, path)
			}
			globalSeen[req.ID] = path
			fileScan.IDs = append(fileScan.IDs, req.ID)
			scan.IDs = append(scan.IDs, req.ID)
		}

		scan.Files = append(scan.Files, fileScan)
	}

	scan.HasExplicitIDs = len(scan.IDs) > 0
	scan.MixedExplicitAndFreeForm = scan.HasExplicitIDs && !scan.AllFilesHaveExplicitIDs
	return scan, nil
}

// IngestExplicitRequirementSpecs ingests only explicit stable requirement IDs and never synthesizes free-form requirements.
func IngestExplicitRequirementSpecs(paths []string) ([]state.Requirement, []state.SourceSpec, error) {
	var allReqs []state.Requirement
	var sources []state.SourceSpec
	globalSeen := make(map[string]string)

	for _, path := range paths {
		reqs, _, err := parseExplicitRequirements(path)
		if err != nil {
			return nil, nil, err
		}
		if len(reqs) == 0 {
			return nil, nil, fmt.Errorf("no explicit requirement IDs found in %s", path)
		}

		for _, req := range reqs {
			if prev, exists := globalSeen[req.ID]; exists {
				return nil, nil, fmt.Errorf("requirement %s found in both %s and %s", req.ID, prev, path)
			}
			globalSeen[req.ID] = path
			allReqs = append(allReqs, req)
		}

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

func ingestSpecWithFallback(path string) ([]state.Requirement, bool, error) {
	reqs, lines, err := parseExplicitRequirements(path)
	if err != nil {
		return nil, false, err
	}

	if len(reqs) == 0 {
		reqs = synthesizeRequirementsFromMarkdown(path, lines)
		return reqs, true, nil
	}

	return reqs, false, nil
}

func parseExplicitRequirements(path string) ([]state.Requirement, []string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open spec %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	var reqs []state.Requirement
	var lines []string
	seen := make(map[string]bool)
	scanner := bufio.NewScanner(f)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		lines = append(lines, line)

		matches := requirementPattern.FindStringSubmatch(line)
		if len(matches) < 3 {
			continue
		}

		id := matches[1]
		text := strings.TrimSpace(matches[2])

		if seen[id] {
			return nil, nil, fmt.Errorf("duplicate requirement ID %s at %s:%d", id, path, lineNum)
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
		return nil, nil, fmt.Errorf("scan %s: %w", path, err)
	}

	return reqs, lines, nil
}

func synthesizeRequirementsFromMarkdown(path string, lines []string) []state.Requirement {
	var reqs []state.Requirement
	seen := make(map[string]bool)
	headings := make([]string, 6)
	inFence := false
	current := fallbackRequirement{}

	flush := func() {
		if current.line == 0 {
			return
		}
		line := current.line
		text := cleanFallbackRequirementText(strings.Join(current.parts, " "))
		current = fallbackRequirement{}
		if text == "" || seen[text] {
			return
		}
		seen[text] = true
		reqs = append(reqs, state.Requirement{
			ID:         fmt.Sprintf("AT-FR-%03d", len(reqs)+1),
			Text:       text,
			SourceSpec: path,
			SourceLine: line,
		})
	}

	for i, line := range lines {
		lineNum := i + 1
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			flush()
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}

		if matches := markdownHeadingPattern.FindStringSubmatch(line); len(matches) == 3 {
			flush()
			level := len(matches[1])
			headings[level-1] = strings.TrimSpace(matches[2])
			for j := level; j < len(headings); j++ {
				headings[j] = ""
			}
			continue
		}

		if matches := markdownListItemPattern.FindStringSubmatch(line); len(matches) == 2 {
			flush()
			item := strings.TrimSpace(matches[1])
			if allowsFallbackRequirement(headings, item) {
				current = fallbackRequirement{line: lineNum, parts: []string{item}}
			}
			continue
		}

		if current.line != 0 && isMarkdownContinuation(line) {
			current.parts = append(current.parts, trimmed)
			continue
		}

		if trimmed != "" {
			flush()
		}
	}
	flush()

	return reqs
}

type fallbackRequirement struct {
	line  int
	parts []string
}

func allowsFallbackRequirement(headings []string, item string) bool {
	if hasExcludedRequirementHeading(headings) {
		return false
	}
	if hasRequirementHeading(headings) {
		return true
	}
	return containsRequirementCue(item)
}

func hasRequirementHeading(headings []string) bool {
	for _, heading := range headings {
		h := strings.ToLower(heading)
		if containsAnyWord(h, "requirement", "requirements", "goal", "goals", "acceptance", "deliverable", "deliverables", "behavior", "behaviour", "verification") {
			return true
		}
	}
	return false
}

func hasExcludedRequirementHeading(headings []string) bool {
	h := strings.ToLower(deepestHeading(headings))
	if h == "" {
		return false
	}
	return containsAnyWord(h, "non-goal", "non-goals", "assumption", "assumptions", "risk", "risks", "rationale", "comparison", "diff", "reference", "references", "appendix", "deferred", "future")
}

func deepestHeading(headings []string) string {
	for i := len(headings) - 1; i >= 0; i-- {
		if strings.TrimSpace(headings[i]) != "" {
			return headings[i]
		}
	}
	return ""
}

func containsRequirementCue(text string) bool {
	lower := strings.ToLower(text)
	return containsAnyWord(lower, "must", "should", "required", "requires", "support", "supports", "provide", "provides", "enable", "enables", "allow", "allows", "refuse", "refuses")
}

func containsAnyWord(text string, words ...string) bool {
	fields := strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-'
	})
	for _, word := range words {
		for _, field := range fields {
			if field == word {
				return true
			}
		}
	}
	return false
}

func isMarkdownContinuation(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "|") {
		return false
	}
	if markdownHeadingPattern.MatchString(line) || markdownListItemPattern.MatchString(line) {
		return false
	}
	return strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")
}

func cleanFallbackRequirementText(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	text = strings.TrimSpace(text)
	if text == "" || strings.HasSuffix(text, ":") || len(text) < 12 {
		return ""
	}
	return text
}

// IngestSpecs reads multiple spec files and returns explicit requirements and source spec metadata.
func IngestSpecs(paths []string) ([]state.Requirement, []state.SourceSpec, error) {
	var allReqs []state.Requirement
	var sources []state.SourceSpec
	globalSeen := make(map[string]string) // id -> source path

	for _, path := range paths {
		reqs, _, err := parseExplicitRequirements(path)
		if err != nil {
			return nil, nil, err
		}

		for _, r := range reqs {
			if prev, exists := globalSeen[r.ID]; exists {
				return nil, nil, fmt.Errorf("requirement %s found in both %s and %s", r.ID, prev, path)
			}
			globalSeen[r.ID] = path
			allReqs = append(allReqs, r)
		}

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

// IngestSpecsWithFallback reads multiple spec files and may synthesize requirements from free-form Markdown.
func IngestSpecsWithFallback(paths []string) ([]state.Requirement, []state.SourceSpec, error) {
	var allReqs []state.Requirement
	var sources []state.SourceSpec
	globalSeen := make(map[string]string) // id -> source path

	for _, path := range paths {
		reqs, synthesized, err := ingestSpecWithFallback(path)
		if err != nil {
			return nil, nil, err
		}

		for _, r := range reqs {
			if synthesized {
				r.ID = nextSyntheticRequirementID(globalSeen)
			}
			if prev, exists := globalSeen[r.ID]; exists {
				return nil, nil, fmt.Errorf("requirement %s found in both %s and %s", r.ID, prev, path)
			}
			globalSeen[r.ID] = path
			allReqs = append(allReqs, r)
		}

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

func nextSyntheticRequirementID(globalSeen map[string]string) string {
	const maxSequentialSyntheticRequirementID = 999999
	for i := 1; i <= maxSequentialSyntheticRequirementID; i++ {
		id := fmt.Sprintf("AT-FR-%03d", i)
		if _, exists := globalSeen[id]; !exists {
			return id
		}
	}
	return fmt.Sprintf("AT-FR-%03d", maxSequentialSyntheticRequirementID+len(globalSeen)+1)
}
