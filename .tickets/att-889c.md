---
id: att-889c
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
# Refactor UpdateTaskStatus to use per-ticket WriteTask

Change UpdateTaskStatus from read-all/mutate-one/write-all to taskStore().UpdateStatus(id, status, reason). Single-file operation.

