package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/runger/attest/internal/state"
	"github.com/runger/attest/internal/ticket"
)

const errReadTasks = "read tasks: %w"

// taskStoreForRun returns the TaskStore for a given run.
// This is the single backend-selection rule — newEngine delegates here too.
var taskStoreForRun = func(wd, _ string) state.TaskStore {
	return ticket.NewStore(filepath.Join(wd, ".tickets"))
}

// taskFilter holds parsed filter flags for task queries (spec section 5.2).
type taskFilter struct {
	status        string
	taskType      string
	priority      int
	tag           string
	requirementID string
	wave          string
	limit         int
	ready         bool
	blocked       bool
	jsonOutput    bool
}

func parseTaskFilter(args []string) (runID string, f taskFilter) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--status":
			if i+1 < len(args) {
				f.status = args[i+1]
				i++
			}
		case "--task-type":
			if i+1 < len(args) {
				f.taskType = args[i+1]
				i++
			}
		case "--tag":
			if i+1 < len(args) {
				f.tag = args[i+1]
				i++
			}
		case "--requirement-id":
			if i+1 < len(args) {
				f.requirementID = args[i+1]
				i++
			}
		case "--wave":
			if i+1 < len(args) {
				f.wave = args[i+1]
				i++
			}
		case "--limit":
			if i+1 < len(args) {
				_, _ = fmt.Sscanf(args[i+1], "%d", &f.limit)
				i++
			}
		case "--priority":
			if i+1 < len(args) {
				_, _ = fmt.Sscanf(args[i+1], "%d", &f.priority)
				i++
			}
		case "--ready":
			f.ready = true
		case "--blocked":
			f.blocked = true
		case "--json":
			f.jsonOutput = true
		default:
			if runID == "" {
				runID = args[i]
			}
		}
	}
	return runID, f
}

func (f taskFilter) matches(t *state.Task, doneStates map[string]state.TaskStatus) bool {
	if f.status != "" && string(t.Status) != f.status {
		return false
	}
	if f.taskType != "" && t.TaskType != f.taskType {
		return false
	}
	if f.tag != "" && !slices.Contains(t.Tags, f.tag) {
		return false
	}
	if f.requirementID != "" && !slices.Contains(t.RequirementIDs, f.requirementID) {
		return false
	}
	if f.priority > 0 && t.Priority != f.priority {
		return false
	}
	if f.ready && (t.Status != state.TaskPending || !depsReady(t, doneStates)) {
		return false
	}
	if f.blocked && t.Status != state.TaskBlocked {
		return false
	}
	return true
}

func filterTasks(tasks []state.Task, f taskFilter, doneStates map[string]state.TaskStatus) []state.Task {
	var result []state.Task
	for i := range tasks {
		if f.matches(&tasks[i], doneStates) {
			result = append(result, tasks[i])
		}
	}
	if f.limit > 0 && len(result) > f.limit {
		result = result[:f.limit]
	}
	return result
}

func depsReady(t *state.Task, taskStates map[string]state.TaskStatus) bool {
	for _, dep := range t.DependsOn {
		if taskStates[dep] != state.TaskDone {
			return false
		}
	}
	return true
}

func buildTaskStates(tasks []state.Task) map[string]state.TaskStatus {
	m := make(map[string]state.TaskStatus, len(tasks))
	for i := range tasks {
		m[tasks[i].TaskID] = tasks[i].Status
	}
	return m
}

func outputTasks(tasks []state.Task, jsonOutput bool) {
	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(tasks)
		return
	}

	for i := range tasks {
		etag := tasks[i].ETag
		if len(etag) > 8 {
			etag = etag[:8]
		}
		fmt.Printf("  [%s] %-12s %s (reqs: %s) etag:%s\n",
			tasks[i].Status, tasks[i].Slug, tasks[i].Title,
			strings.Join(tasks[i].RequirementIDs, ","),
			etag)
	}
}

// cmdTasks implements `attest tasks` (spec section 5.2).
func cmdTasks(args []string) error {
	runID, f := parseTaskFilter(args)
	if runID == "" {
		return fmt.Errorf("usage: attest tasks <run-id> [--status X] [--task-type X] [--tag X] [--json]")
	}

	wd, err := workDir()
	if err != nil {
		return err
	}

	store := taskStoreForRun(wd, runID)
	tasks, err := store.ReadTasks(runID)
	if err != nil {
		return fmt.Errorf(errReadTasks, err)
	}

	taskStates := buildTaskStates(tasks)
	filtered := filterTasks(tasks, f, taskStates)

	if f.jsonOutput {
		outputTasks(filtered, true)
	} else {
		fmt.Printf("Tasks (%d of %d):\n", len(filtered), len(tasks))
		outputTasks(filtered, false)
	}
	return nil
}

// cmdReady implements `attest ready` (spec section 5.2).
func cmdReady(args []string) error {
	runID, f := parseTaskFilter(args)
	if runID == "" {
		return fmt.Errorf("usage: attest ready <run-id> [--json]")
	}

	wd, err := workDir()
	if err != nil {
		return err
	}

	store := taskStoreForRun(wd, runID)
	tasks, err := store.ReadTasks(runID)
	if err != nil {
		return fmt.Errorf(errReadTasks, err)
	}

	taskStates := buildTaskStates(tasks)
	f.ready = true
	ready := filterTasks(tasks, f, taskStates)

	// Sort by priority, then order, then task_id (spec section 5.2).
	sort.Slice(ready, func(i, j int) bool {
		if ready[i].Priority != ready[j].Priority {
			return ready[i].Priority < ready[j].Priority
		}
		if ready[i].Order != ready[j].Order {
			return ready[i].Order < ready[j].Order
		}
		return ready[i].TaskID < ready[j].TaskID
	})

	if f.jsonOutput {
		outputTasks(ready, true)
	} else {
		fmt.Printf("Ready tasks (%d):\n", len(ready))
		outputTasks(ready, false)
	}
	return nil
}

// cmdBlocked implements `attest blocked` (spec section 5.2).
func cmdBlocked(args []string) error {
	runID, f := parseTaskFilter(args)
	if runID == "" {
		return fmt.Errorf("usage: attest blocked <run-id> [--json]")
	}

	wd, err := workDir()
	if err != nil {
		return err
	}

	store := taskStoreForRun(wd, runID)
	tasks, err := store.ReadTasks(runID)
	if err != nil {
		return fmt.Errorf(errReadTasks, err)
	}

	taskStates := buildTaskStates(tasks)
	f.blocked = true
	blocked := filterTasks(tasks, f, taskStates)

	if f.jsonOutput {
		outputTasks(blocked, true)
	} else {
		if len(blocked) == 0 {
			fmt.Println("No blocked tasks.")
			return nil
		}
		// Group by reason (spec section 5.2).
		byReason := make(map[string][]state.Task)
		for i := range blocked {
			reason := blocked[i].StatusReason
			if reason == "" {
				reason = "(no reason recorded)"
			}
			byReason[reason] = append(byReason[reason], blocked[i])
		}
		for reason, tasks := range byReason {
			fmt.Printf("Blocked: %s (%d tasks)\n", reason, len(tasks))
			outputTasks(tasks, false)
		}
	}
	return nil
}

// cmdNext implements `attest next` (spec section 5.2).
func cmdNext(args []string) error {
	runID, f := parseTaskFilter(args)
	if runID == "" {
		return fmt.Errorf("usage: attest next <run-id> [--json]")
	}

	wd, err := workDir()
	if err != nil {
		return err
	}

	store := taskStoreForRun(wd, runID)
	tasks, err := store.ReadTasks(runID)
	if err != nil {
		return fmt.Errorf(errReadTasks, err)
	}

	taskStates := buildTaskStates(tasks)

	// Find ready tasks sorted by priority.
	var ready []state.Task
	for i := range tasks {
		if tasks[i].Status == state.TaskPending && depsReady(&tasks[i], taskStates) {
			ready = append(ready, tasks[i])
		}
	}
	sort.Slice(ready, func(i, j int) bool {
		if ready[i].Priority != ready[j].Priority {
			return ready[i].Priority < ready[j].Priority
		}
		return ready[i].Order < ready[j].Order
	})

	if len(ready) > 0 {
		if f.jsonOutput {
			outputTasks(ready[:1], true)
		} else {
			fmt.Println("Next task:")
			outputTasks(ready[:1], false)
		}
		return nil
	}

	// No ready task — point to highest-impact blocker (spec section 5.2).
	var blocked []state.Task
	for i := range tasks {
		if tasks[i].Status == state.TaskBlocked {
			blocked = append(blocked, tasks[i])
		}
	}
	if len(blocked) > 0 {
		sort.Slice(blocked, func(i, j int) bool {
			return blocked[i].Priority < blocked[j].Priority
		})
		if f.jsonOutput {
			outputTasks(blocked[:1], true)
		} else {
			fmt.Println("No ready tasks. Highest-impact blocker:")
			outputTasks(blocked[:1], false)
			fmt.Printf("  Reason: %s\n", blocked[0].StatusReason)
		}
		return nil
	}

	fmt.Println("No ready or blocked tasks.")
	return nil
}

// cmdProgress implements `attest progress` (spec section 5.2).
func cmdProgress(args []string) error {
	runID, f := parseTaskFilter(args)
	if runID == "" {
		return fmt.Errorf("usage: attest progress <run-id> [--json]")
	}

	wd, err := workDir()
	if err != nil {
		return err
	}

	store := taskStoreForRun(wd, runID)
	tasks, err := store.ReadTasks(runID)
	if err != nil {
		return fmt.Errorf(errReadTasks, err)
	}

	// Count by task state.
	counts := make(map[string]int)
	for i := range tasks {
		counts[string(tasks[i].Status)]++
	}

	// Requirement coverage.
	var covCounts map[string]int
	runDir := state.NewRunDir(wd, runID)
	coverage, err := runDir.ReadCoverage()
	if err == nil {
		covCounts = make(map[string]int)
		for _, c := range coverage {
			covCounts[c.Status]++
		}
	}

	// Completion percentage.
	total := len(tasks)
	done := counts["done"]
	pct := 0
	if total > 0 {
		pct = done * 100 / total
	}

	if f.jsonOutput {
		out := map[string]any{
			"total_tasks":        total,
			"task_counts":        counts,
			"completion_percent": pct,
		}
		if covCounts != nil {
			out["requirement_coverage"] = covCounts
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
		return nil
	}

	fmt.Printf("Progress: %d%% (%d/%d tasks done)\n\n", pct, done, total)
	fmt.Println("Tasks by state:")
	for st, count := range counts {
		fmt.Printf("  %-16s %d\n", st, count)
	}
	if covCounts != nil {
		fmt.Println("\nRequirement coverage:")
		for st, count := range covCounts {
			fmt.Printf("  %-16s %d\n", st, count)
		}
	}
	return nil
}
