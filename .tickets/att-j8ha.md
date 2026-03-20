---
id: att-j8ha
status: closed
deps: [att-1lhy, att-l41e]
links: []
created: 2026-03-19T06:12:46Z
type: task
priority: 1
assignee: Ronny Unger
parent: att-jndm
tags: [wave-2]
---
# Add TaskStore field to Engine, replace 11 engine call sites

Add TaskStore field to Engine struct. Add taskStore() helper with RunDir fallback. Replace 7 ReadTasks and 4 WriteTasks calls in engine.go with taskStore().*

