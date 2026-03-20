---
id: att-l41e
status: closed
deps: [att-1lhy]
links: []
created: 2026-03-19T06:12:46Z
type: task
priority: 1
assignee: Ronny Unger
parent: att-jndm
tags: [wave-2]
---
# Add RunDir TaskStore wrapper methods

Add ReadTask, WriteTask, UpdateStatus, CreateRun, ReadTasks(runID), WriteTasks(runID, tasks) to RunDir as wrappers around existing JSON read/write. Comment: not concurrent-safe.

