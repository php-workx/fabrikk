---
id: att-mhtt
status: closed
deps: [att-lk0b]
links: []
created: 2026-03-19T06:12:46Z
type: task
priority: 2
assignee: Ronny Unger
parent: att-jndm
tags: [wave-4]
---
# Refactor RetryTask to use per-ticket UpdateStatus

Change RetryTask from read-all/find/mutate/write-all to taskStore().UpdateStatus(id, TaskPending, empty). Single-file operation.

