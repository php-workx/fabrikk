---
id: att-0vac
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
# Refactor persistVerifiedTask to use ReadTask/WriteTask

Change persistVerifiedTask from bulk read/write to taskStore().ReadTask(id) + WriteTask(). Enables concurrent verification.

