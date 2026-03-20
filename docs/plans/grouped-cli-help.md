# Plan: Modal-style grouped CLI help with lipgloss

## Context

The `attest` CLI has 15 commands in a flat `usage()` list. The user wants Modal-style grouped command help with rounded-border boxes, using charmbracelet/lipgloss. This makes the growing command set scannable at a glance.

## Approach

Add lipgloss, create a new `help.go` file with grouped command rendering, update `main.go` to use it.

### Command Groups

| Group | Commands |
|-------|----------|
| **Workflow** | prepare, review, tech-spec, plan, approve |
| **Execution** | status, verify, retry, report |
| **Tasks** | tasks, ready, blocked, next, progress |

### Files

**New: `cmd/attest/help.go`** (~120 lines)
- `command` struct: Name, Desc
- `commandGroup` struct: Title, Commands
- `commandGroups` var: static data for the 3 groups
- `usage(w io.Writer)`: detects TTY via type-assert to `*os.File`, gets terminal width, renders grouped boxes with lipgloss
- `padRight(s string, width int) string`: ANSI-aware padding using `lipgloss.Width()`
- Lipgloss styles: rounded border, dim border color, bold command names, dim descriptions

**Modify: `cmd/attest/main.go`**
- Remove the old `usage()` function (moved to help.go)
- Add `"help"`, `"--help"`, `"-h"` cases to the switch — return 0
- Change no-args exit code from 1 to 0 (showing help is not an error)

**Modify: `cmd/attest/main_test.go`**
- `TestRunReturnsUsageWhenNoArgs`: expect exit code 0, assert on `"attest"` + `"prepare"` instead of literal `"usage: attest <command> [args]"`
- `TestRunReturnsUsageForUnknownCommand`: relax assertion similarly
- Add `TestRunReturnsUsageForHelpFlag`: test `--help`, `-h`, `help`

**New: `cmd/attest/help_test.go`**
- `TestUsageContainsAllCommands`: verify all 15 command names appear
- `TestUsageGroupHeaders`: verify "Workflow", "Execution", "Tasks" appear

**Modify: `go.mod`**
- `go get github.com/charmbracelet/lipgloss@latest`

### TTY detection

```go
func usage(w io.Writer) {
    width := 80
    if f, ok := w.(*os.File); ok && term.IsTerminal(f.Fd()) {
        if tw, _, err := term.GetSize(f.Fd()); err == nil && tw > 0 {
            width = tw
        }
    }
    // render with lipgloss...
}
```

Non-TTY (piped, tests, CI): lipgloss degrades — no color, but Unicode box-drawing characters still render. Tests assert on command names and group titles, not exact layout.

### Rendering approach

Each group becomes a lipgloss box with `RoundedBorder()`. Inside each box: two-column layout with command name (bold) left-aligned and description right. `padRight` uses `lipgloss.Width()` to handle ANSI-aware padding. Boxes are stacked vertically with `lipgloss.JoinVertical()`.

## Verification

1. `go build ./...` — compiles
2. `go test -race ./cmd/attest/...` — all tests pass
3. `golangci-lint run ./cmd/attest/...` — no lint issues
4. `bin/attest` and `bin/attest --help` — visually inspect grouped boxes in terminal
5. `bin/attest 2>/dev/null | cat` — verify piped output degrades cleanly
6. `bin/attest mystery` — still shows "unknown command" + help with exit code 1
