---
id: att-3l84
status: closed
deps: []
links: []
created: 2026-03-24T09:57:55Z
type: bug
priority: 1
assignee: Ronny Unger
tags: [rebrand, correctness, P1]
---
# Fix GenerateID to derive prefix from project directory, not .tickets/

## What's wrong

GenerateID(dir) calls idPrefix(filepath.Base(dir)) where dir is the .tickets/
path. This produces prefix ".ti" (from ".tickets") instead of deriving from the
project directory name.

The tk CLI does it correctly: basename "$(pwd)" gets the project directory name
(e.g., "fabrikk" → "fab", "my-cool-project" → "mcp").

Additionally, the fallback prefix is hardcoded to "att" (from the old "attest"
name). Should be "fab" or better: derived from the project name.

## Current behavior

- GenerateID("/path/to/project/.tickets") → idPrefix(".tickets") → ".ti-xxxx"
- Fallback on empty/edge cases → "att-xxxx" (hardcoded)

## Fix approach (pm-001)

Keep GenerateID(dir string) signature unchanged — callers stay as-is.
Internally, derive the project name from the parent directory:

```go
func GenerateID(dir string) (string, error) {
    // dir is .tickets/ — derive prefix from the project directory (parent).
    projectDir := filepath.Base(filepath.Dir(dir))
    prefix := idPrefix(projectDir)
    ...
}
```

This matches tk's behavior: tk uses `basename "$(pwd)"` which is the project
directory, not the .tickets/ directory.

## Fallback prefix (pm-005)

The fallback should NOT be product-branded ("att" or "fab"). Use a generic
fallback "tkt" (for "ticket") that works for any project. A hardcoded "fab"
has the same problem as "att" — only correct for one project.

## Acceptance criteria

- GenerateID("/path/to/fabrikk/.tickets") produces "fab-xxxx" prefix
- GenerateID("/path/to/my-cool-project/.tickets") produces "mcp-xxxx" prefix
- Fallback prefix is "tkt" not "att" or "fab"
- GenerateID signature unchanged — no caller modifications needed
- Existing tests updated to reflect correct behavior
- tk CLI and Go implementation produce same prefix for same project
- idPrefix comment updated: example shows "fabrikk" → "fab"

