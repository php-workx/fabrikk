package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
)

// helpCommand describes a single CLI command for help display.
type helpCommand struct {
	Name string
	Desc string
}

// helpGroup describes a category of related commands.
type helpGroup struct {
	Title    string
	Commands []helpCommand
}

var helpGroups = []helpGroup{
	{
		Title: "Workflow",
		Commands: []helpCommand{
			{"prepare", "Ingest specs and create a draft run"},
			{"review", "Show the run artifact for review"},
			{"tech-spec", "Manage run-scoped technical specs"},
			{"plan", "Manage run-scoped execution plans"},
			{"approve", "Approve and compile tasks"},
		},
	},
	{
		Title: "Execution",
		Commands: []helpCommand{
			{"status", "Show run status"},
			{"verify", "Run deterministic verification"},
			{"retry", "Requeue a blocked task"},
			{"report", "Import a completion report"},
		},
	},
	{
		Title: "Tasks",
		Commands: []helpCommand{
			{"tasks", "Query tasks with filters"},
			{"ready", "Show dispatchable tasks"},
			{"blocked", "Show blocked tasks"},
			{"next", "Show next task to work on"},
			{"progress", "Show run progress"},
		},
	},
	{
		Title: "Learning",
		Commands: []helpCommand{
			{"learn", "Add, query, or manage learnings"},
			{"context", "Assemble agent context for a task"},
		},
	},
}

func usage(w io.Writer) {
	width := 80
	if f, ok := w.(*os.File); ok && term.IsTerminal(f.Fd()) {
		if tw, _, err := term.GetSize(f.Fd()); err == nil && tw > 0 {
			width = tw
		}
	}
	renderHelp(w, width)
}

func renderHelp(w io.Writer, width int) {
	boxWidth := width - 2
	if boxWidth < 40 {
		boxWidth = 40
	}
	if boxWidth > 110 {
		boxWidth = 110
	}

	dimColor := lipgloss.Color("245")
	accentColor := lipgloss.Color("12")

	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(accentColor)
	descStyle := lipgloss.NewStyle().Foreground(dimColor)
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(dimColor).
		Padding(0, 1).
		Width(boxWidth)
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(accentColor)
	cmdStyle := lipgloss.NewStyle().Bold(true)

	header := headerStyle.Render("attest") + "  " +
		descStyle.Render("Spec-driven autonomous run system for coding agents")

	boxes := make([]string, 0, len(helpGroups))
	for _, group := range helpGroups {
		// Compute column width from longest command name in this group.
		maxName := 0
		for _, cmd := range group.Commands {
			if len(cmd.Name) > maxName {
				maxName = len(cmd.Name)
			}
		}

		var rows []string
		for _, cmd := range group.Commands {
			name := cmdStyle.Render(cmd.Name)
			padded := padToWidth(name, maxName+2)
			rows = append(rows, "  "+padded+descStyle.Render(cmd.Desc))
		}

		content := " " + titleStyle.Render(group.Title) + "\n" + strings.Join(rows, "\n")
		boxes = append(boxes, boxStyle.Render(content))
	}

	output := lipgloss.JoinVertical(lipgloss.Left,
		"",
		" "+header,
		"",
		lipgloss.JoinVertical(lipgloss.Left, boxes...),
	)

	_, _ = fmt.Fprintln(w, output)
}

// padToWidth pads a styled string to a target visible width.
// Uses lipgloss.Width to account for ANSI escape sequences.
func padToWidth(s string, target int) string {
	visible := lipgloss.Width(s)
	if visible >= target {
		return s
	}
	return s + strings.Repeat(" ", target-visible)
}
