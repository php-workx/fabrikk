package councilflow

import (
	"fmt"
	"strings"
)

// Annotation represents a human-added inline comment in a spec document.
type Annotation struct {
	Line    int    `json:"line"`
	Text    string `json:"text"`
	Context string `json:"context,omitempty"` // surrounding line for reference
}

// ParseAnnotations extracts all @@ annotations from a markdown document.
// Annotations are lines starting with @@ (after optional whitespace).
// Lines inside code blocks (``` fences) are ignored.
func ParseAnnotations(markdown string) []Annotation {
	lines := strings.Split(markdown, "\n")
	var annotations []Annotation
	inCodeBlock := false

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "```") {
			inCodeBlock = !inCodeBlock
			continue
		}
		if inCodeBlock {
			continue
		}

		if strings.HasPrefix(trimmed, "@@") {
			text := strings.TrimSpace(strings.TrimPrefix(trimmed, "@@"))
			ctx := ""
			if i > 0 {
				ctx = strings.TrimSpace(lines[i-1])
			}
			annotations = append(annotations, Annotation{
				Line:    i + 1, // 1-indexed
				Text:    text,
				Context: ctx,
			})
		}
	}

	return annotations
}

// HasUnresolvedAnnotations returns true if the markdown contains any @@ annotations.
func HasUnresolvedAnnotations(markdown string) bool {
	return len(ParseAnnotations(markdown)) > 0
}

// FormatAnnotationsAsFindings converts annotations to a human-readable summary.
func FormatAnnotationsAsFindings(annotations []Annotation) string {
	if len(annotations) == 0 {
		return ""
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d unresolved annotation(s):\n", len(annotations))
	for _, a := range annotations {
		fmt.Fprintf(&b, "  line %d: %s\n", a.Line, a.Text)
		if a.Context != "" {
			fmt.Fprintf(&b, "    after: %s\n", a.Context)
		}
	}
	return b.String()
}
