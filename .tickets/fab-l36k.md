---
id: fab-l36k
status: closed
deps: []
links: []
created: 2026-03-24T14:25:42Z
type: task
priority: 2
assignee: Ronny Unger
tags: [daemon]
---
# Add stream-json types

Create internal/agentcli/stream.go with StreamMessage, StreamResponse, DaemonRequest, DaemonResponse types and extractResponseText helper. Pure data types, no behavior.

## Acceptance Criteria

go build ./internal/agentcli/... compiles. Types match Claude CLI stream-json format.

