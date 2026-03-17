package councilflow

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// w is a write helper that suppresses errcheck for interactive output.
func w(out io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(out, format, args...)
}

// ApprovePersonas presents personas to the user for interactive approval.
// Returns the approved set (may be modified). If input is nil, uses os.Stdin.
func ApprovePersonas(personas []Persona, in io.Reader, out io.Writer) ([]Persona, error) {
	if in == nil {
		in = os.Stdin
	}
	if out == nil {
		out = os.Stdout
	}

	printPersonaSummary(personas, out)
	w(out, "\n")

	scanner := bufio.NewScanner(in)

	for {
		w(out, "Action: [a]pprove all, [r]emove N, [d]etail N, [q]uit > ")
		if !scanner.Scan() {
			return nil, fmt.Errorf("persona approval: no input")
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		switch {
		case input == "a" || input == "approve":
			w(out, "Approved %d personas.\n", len(personas))
			return personas, nil

		case input == "q" || input == "quit":
			return nil, fmt.Errorf("persona approval: cancelled by user")

		case strings.HasPrefix(input, "r ") || strings.HasPrefix(input, "remove "):
			idx, err := parseIndex(input, len(personas))
			if err != nil {
				w(out, "  %v\n", err)
				continue
			}
			w(out, "  Removed: %s\n", personas[idx].DisplayName)
			personas = append(personas[:idx], personas[idx+1:]...)
			if len(personas) == 0 {
				return nil, fmt.Errorf("persona approval: all personas removed")
			}
			printPersonaSummary(personas, out)

		case strings.HasPrefix(input, "d ") || strings.HasPrefix(input, "detail "):
			idx, err := parseIndex(input, len(personas))
			if err != nil {
				w(out, "  %v\n", err)
				continue
			}
			printPersonaDetail(&personas[idx], out)

		default:
			w(out, "  Unknown command. Use: a (approve), r N (remove), d N (detail), q (quit)\n")
		}
	}
}

func printPersonaSummary(personas []Persona, out io.Writer) {
	w(out, "\n=== Review Personas (%d) ===\n\n", len(personas))
	for i := range personas {
		p := &personas[i]
		typeLabel := "fixed"
		if p.Type == PersonaDynamic {
			typeLabel = "dynamic"
		}
		w(out, "  %d. %-35s [%s] via %s\n", i+1, p.DisplayName, typeLabel, p.Backend)
		w(out, "     %s\n", truncateStr(p.Perspective, 100))
		if p.Rationale != "" {
			w(out, "     Rationale: %s\n", p.Rationale)
		}
	}
}

func printPersonaDetail(p *Persona, out io.Writer) {
	w(out, "\n--- %s ---\n", p.DisplayName)
	w(out, "  ID:          %s\n", p.PersonaID)
	w(out, "  Type:        %s\n", p.Type)
	w(out, "  Backend:     %s\n", p.Backend)
	w(out, "  Model:       %s\n", p.ModelPref)
	w(out, "  Perspective: %s\n", p.Perspective)
	if p.Rationale != "" {
		w(out, "  Rationale:   %s\n", p.Rationale)
	}
	if len(p.FocusSections) > 0 {
		w(out, "  Focus:       %s\n", strings.Join(p.FocusSections, ", "))
	}
	w(out, "  Instructions: %s\n\n", truncateStr(p.Instructions, 200))
}

func parseIndex(input string, count int) (int, error) {
	parts := strings.Fields(input)
	if len(parts) < 2 {
		return 0, fmt.Errorf("usage: %s N (1-%d)", parts[0], count)
	}
	n, err := strconv.Atoi(parts[1])
	if err != nil || n < 1 || n > count {
		return 0, fmt.Errorf("invalid index: %s (must be 1-%d)", parts[1], count)
	}
	return n - 1, nil // convert to 0-indexed
}

func truncateStr(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
