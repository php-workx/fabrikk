---
id: att-1lhy
status: closed
deps: []
links: []
created: 2026-03-19T06:12:46Z
type: task
priority: 1
assignee: Ronny Unger
parent: att-jndm
tags: [wave-2]
---
# Add TaskStore interface to internal/state/types.go

Define TaskStore interface with ReadTasks(runID), WriteTasks(runID, tasks), ReadTask, WriteTask, UpdateStatus, CreateRun. No ticket-specific types. Import constraint: nothing in state/ imports ticket/.

