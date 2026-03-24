package ticket

import (
	"sort"
	"strings"

	"github.com/php-workx/fabrikk/internal/state"
)

const (
	tkStatusOpen       = "open"
	tkStatusInProgress = "in_progress"
	tkStatusClosed     = "closed"
)

// isDispatchable returns true for task statuses that represent unclaimed,
// ready-for-work states. Only pending and repair_pending tasks can be dispatched.
func isDispatchable(s state.TaskStatus) bool {
	return s == state.TaskPending || s == state.TaskRepairPending
}

// ReadyFilter returns dispatchable tasks (pending/repair_pending) with all
// dependencies satisfied. Missing dependencies are treated as unresolved.
func ReadyFilter(tasks []state.Task) []state.Task {
	statusMap := buildStatusMap(tasks)

	var ready []state.Task
	for i := range tasks {
		t := &tasks[i]
		if !isDispatchable(t.Status) {
			continue
		}
		if allDepsClosed(t.DependsOn, statusMap) {
			ready = append(ready, *t)
		}
	}

	sortByPriorityThenID(ready)
	return ready
}

// BlockedFilter returns tasks that are open/in_progress with at least one unresolved dependency.
func BlockedFilter(tasks []state.Task) []state.Task {
	statusMap := buildStatusMap(tasks)

	var blocked []state.Task
	for i := range tasks {
		t := &tasks[i]
		tkStatus := StatusToTicket(t.Status)
		if tkStatus != tkStatusOpen && tkStatus != tkStatusInProgress {
			continue
		}
		if len(t.DependsOn) == 0 {
			continue
		}
		if !allDepsClosed(t.DependsOn, statusMap) {
			blocked = append(blocked, *t)
		}
	}

	sortByPriorityThenID(blocked)
	return blocked
}

// DetectCycles finds dependency cycles among open/in_progress tasks.
// Uses DFS with 3-state coloring. Returns normalized cycles (lexicographic-smallest ID first).
func DetectCycles(tasks []state.Task) [][]string {
	// Build adjacency map (only open/in_progress tasks).
	adj := make(map[string][]string)
	active := make(map[string]bool)
	for i := range tasks {
		tkStatus := StatusToTicket(tasks[i].Status)
		if tkStatus == tkStatusClosed {
			continue
		}
		id := tasks[i].TaskID
		active[id] = true
		adj[id] = append(adj[id], tasks[i].DependsOn...)
	}

	const (
		white = 0
		gray  = 1
		black = 2
	)

	color := make(map[string]int)
	parent := make(map[string]string)
	var cycles [][]string

	var dfs func(node string)
	dfs = func(node string) {
		color[node] = gray
		for _, dep := range adj[node] {
			if !active[dep] {
				continue
			}
			switch color[dep] {
			case gray:
				// Found cycle — backtrace.
				cycle := extractCycle(parent, node, dep)
				cycles = append(cycles, normalizeCycle(cycle))
			case white:
				parent[dep] = node
				dfs(dep)
			default:
				// black — already explored
			}
		}
		color[node] = black
	}

	for id := range active {
		if color[id] == white {
			dfs(id)
		}
	}

	return deduplicateCycles(cycles)
}

func buildStatusMap(tasks []state.Task) map[string]string {
	m := make(map[string]string, len(tasks))
	for i := range tasks {
		m[tasks[i].TaskID] = StatusToTicket(tasks[i].Status)
	}
	return m
}

func allDepsClosed(deps []string, statusMap map[string]string) bool {
	for _, dep := range deps {
		if statusMap[dep] != "closed" {
			return false
		}
	}
	return true
}

func sortByPriorityThenID(tasks []state.Task) {
	sort.SliceStable(tasks, func(i, j int) bool {
		if tasks[i].Priority != tasks[j].Priority {
			return tasks[i].Priority < tasks[j].Priority
		}
		return tasks[i].TaskID < tasks[j].TaskID
	})
}

func extractCycle(parent map[string]string, from, to string) []string {
	// Self-loop: A depends on A.
	if from == to {
		return []string{to}
	}
	var cycle []string
	cycle = append(cycle, to)
	node := from
	for node != to && node != "" {
		cycle = append(cycle, node)
		node = parent[node]
	}
	return cycle
}

// normalizeCycle rotates a cycle so the lexicographic-smallest ID is first.
func normalizeCycle(cycle []string) []string {
	if len(cycle) == 0 {
		return cycle
	}
	minIdx := 0
	for i := 1; i < len(cycle); i++ {
		if cycle[i] < cycle[minIdx] {
			minIdx = i
		}
	}
	normalized := make([]string, len(cycle))
	for i := range cycle {
		normalized[i] = cycle[(minIdx+i)%len(cycle)]
	}
	return normalized
}

func deduplicateCycles(cycles [][]string) [][]string {
	seen := make(map[string]bool)
	var unique [][]string
	for _, c := range cycles {
		key := strings.Join(c, "→")
		if !seen[key] {
			seen[key] = true
			unique = append(unique, c)
		}
	}
	return unique
}
