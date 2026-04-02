---
id: fab-5jyy
status: closed
deps: []
links: []
created: 2026-03-28T13:59:29Z
type: task
priority: 2
assignee: Ronny Unger
tags: [daemon, docs]
---
# Fix config comments and add YAML parse warning

Two doc fixes in agentcli.go: (1) Line 115 comment says .fabrikk/config.yaml but code reads fabrikk.yaml. Line 144 struct comment same issue. Update both. (2) Line 157: yaml.Unmarshal error returns nil silently. Add fmt.Fprintf(os.Stderr) warning so malformed config is visible.

## Acceptance Criteria

Comments reference fabrikk.yaml. Malformed YAML produces stderr warning.

