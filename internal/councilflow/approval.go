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
		input, err := readInput(scanner)
		if err != nil {
			return nil, err
		}
		if input == "" {
			continue
		}

		result, err := handleApprovalCommand(input, &personas, out)
		if err != nil {
			return nil, err
		}
		if result != nil {
			return result, nil
		}
	}
}

// readInput reads a trimmed line from the scanner, returning an error on EOF or scan failure.
func readInput(scanner *bufio.Scanner) (string, error) {
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", fmt.Errorf("persona approval: %w", err)
		}
		return "", fmt.Errorf("persona approval: no input (EOF)")
	}
	return strings.TrimSpace(scanner.Text()), nil
}

// handleApprovalCommand processes a single approval command.
// Returns (personas, nil) to accept, (nil, error) to abort, or (nil, nil) to continue the loop.
func handleApprovalCommand(input string, personas *[]Persona, out io.Writer) ([]Persona, error) {
	switch {
	case input == "a" || input == "approve":
		w(out, "Approved %d personas.\n", len(*personas))
		return *personas, nil

	case input == "q" || input == "quit":
		return nil, fmt.Errorf("persona approval: cancelled by user")

	case strings.HasPrefix(input, "r ") || strings.HasPrefix(input, "remove "):
		return handleRemove(input, personas, out)

	case strings.HasPrefix(input, "d ") || strings.HasPrefix(input, "detail "):
		handleDetail(input, *personas, out)
		printPersonaSummary(*personas, out)
		return nil, nil

	default:
		w(out, "  Unknown command. Use: a (approve), r N (remove), d N (detail), q (quit)\n")
		return nil, nil
	}
}

// handleRemove processes a remove command. Returns (nil, nil) to continue,
// or (nil, error) if all personas were removed.
func handleRemove(input string, personas *[]Persona, out io.Writer) ([]Persona, error) {
	idx, err := parseIndex(input, len(*personas))
	if err != nil {
		w(out, "  %v\n", err)
		return nil, nil
	}
	w(out, "  Removed: %s\n", (*personas)[idx].DisplayName)
	*personas = append((*personas)[:idx], (*personas)[idx+1:]...)
	if len(*personas) == 0 {
		return nil, fmt.Errorf("persona approval: all personas removed")
	}
	printPersonaSummary(*personas, out)
	return nil, nil
}

// handleDetail processes a detail command.
func handleDetail(input string, personas []Persona, out io.Writer) {
	idx, err := parseIndex(input, len(personas))
	if err != nil {
		w(out, "  %v\n", err)
		return
	}
	printPersonaDetail(&personas[idx], out)
}

func printPersonaSummary(personas []Persona, out io.Writer) {
	w(out, "\n=== Review Personas (%d) ===\n\n", len(personas))
	for i := range personas {
		p := &personas[i]
		typeLabel := string(p.Type)
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
