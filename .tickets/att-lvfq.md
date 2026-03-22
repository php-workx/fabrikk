---
id: att-lvfq
status: closed
deps: []
links: []
created: 2026-03-20T16:09:38Z
type: task
priority: 1
assignee: Ronny Unger
parent: att-2nve
tags: [memory, significant]
---
# Add handoff display to attest status command

Spec §3.3: 'attest status shows handoff summary if < 24h old'. cmdStatus (main.go:525-573) does not call showLatestHandoff or display any handoff info. Fix: call showLatestHandoff(wd) after displaying run status in cmdStatus, both for single-run and list-all modes. Verification step 10 in the spec depends on this.

## Acceptance Criteria

attest status <run-id> shows handoff summary when < 24h old; attest status (list mode) shows handoff summary; test verifies handoff appears in status output

