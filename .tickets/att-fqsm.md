---
id: att-fqsm
status: closed
deps: [att-rapa]
links: []
created: 2026-03-19T06:12:46Z
type: task
priority: 3
assignee: Ronny Unger
parent: att-jndm
tags: [wave-5]
---
# Add attest migrate-tasks command

New CLI command: attest migrate-tasks <run-id> reads tasks.json from existing run and writes .tickets/*.md for backward compat.


## Notes

**2026-03-20T05:50:15Z**

Closed without implementation — no existing runs with tasks.json to migrate. The system is not actively used yet, so backward compatibility migration is unnecessary. Can be reopened if needed when legacy runs exist.
