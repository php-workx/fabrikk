---
id: att-drm1
status: closed
deps: [att-7aio]
links: []
created: 2026-03-19T15:25:36Z
type: task
priority: 2
assignee: Ronny Unger
parent: att-jndm
tags: [claim-wave-2]
---
# Refactor ReadyFilter to use isDispatchable, fix GetPendingTasks

Replace tk-status check in ReadyFilter with isDispatchable (only TaskPending + TaskRepairPending). Fix GetPendingTasks to also accept TaskRepairPending. Add 3 tests: TestClaimedTaskNotReady, TestImplementingTaskNotReady, TestRepairPendingTaskReady.

