---
id: att-i34x
status: closed
deps: [att-j8ha]
links: []
created: 2026-03-19T06:12:46Z
type: task
priority: 1
assignee: Ronny Unger
parent: att-jndm
tags: [wave-2]
---
# Wire 6 CLI task commands through TaskStore

Change cmdTasks, cmdReady, cmdBlocked, cmdNext, cmdProgress in tasks.go and cmdReport in main.go to use TaskStore instead of direct runDir.ReadTasks().

