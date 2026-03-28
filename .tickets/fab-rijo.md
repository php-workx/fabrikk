---
id: fab-rijo
status: closed
deps: []
links: []
created: 2026-03-28T13:59:29Z
type: task
priority: 1
assignee: Ronny Unger
tags: [daemon, config]
---
# Fix config loading: use LookupEnv, cache with sync.Once

disabledBackendsList() at agentcli.go:128 reads fabrikk.yaml on every BackendFor call (no caching). Also os.Getenv treats empty string same as unset, so FABRIKK_DISABLED_BACKENDS='' cannot override fabrikk.yaml. Fix: (1) Use os.LookupEnv to distinguish unset from empty. (2) Cache result with sync.Once so config is read once per process.

## Acceptance Criteria

BackendFor reads config once. FABRIKK_DISABLED_BACKENDS='' clears config restrictions.

