package ticket

import (
	"testing"

	"github.com/runger/attest/internal/state"
)

func TestReadyNoDeps(t *testing.T) {
	tasks := []state.Task{
		{TaskID: "a", Status: state.TaskPending},
	}
	ready := ReadyFilter(tasks)
	if len(ready) != 1 || ready[0].TaskID != "a" {
		t.Errorf("ready = %v, want [a]", ready)
	}
}

func TestReadyAllDepsClosed(t *testing.T) {
	tasks := []state.Task{
		{TaskID: "dep", Status: state.TaskDone},
		{TaskID: "a", Status: state.TaskPending, DependsOn: []string{"dep"}},
	}
	ready := ReadyFilter(tasks)
	if len(ready) != 1 || ready[0].TaskID != "a" {
		t.Errorf("ready = %v, want [a]", ready)
	}
}

func TestBlockedOpenDep(t *testing.T) {
	tasks := []state.Task{
		{TaskID: "dep", Status: state.TaskPending},
		{TaskID: "a", Status: state.TaskPending, DependsOn: []string{"dep"}},
	}
	blocked := BlockedFilter(tasks)
	if len(blocked) != 1 || blocked[0].TaskID != "a" {
		t.Errorf("blocked = %v, want [a]", blocked)
	}
}

func TestBlockedMissingDep(t *testing.T) {
	tasks := []state.Task{
		{TaskID: "a", Status: state.TaskPending, DependsOn: []string{"nonexistent"}},
	}
	ready := ReadyFilter(tasks)
	if len(ready) != 0 {
		t.Errorf("ready = %v, want empty (missing dep = not ready)", ready)
	}
	blocked := BlockedFilter(tasks)
	if len(blocked) != 1 {
		t.Errorf("blocked = %v, want [a]", blocked)
	}
}

func TestExplicitlyBlockedNotReady(t *testing.T) {
	tasks := []state.Task{
		{TaskID: "a", Status: state.TaskBlocked},
	}
	ready := ReadyFilter(tasks)
	if len(ready) != 0 {
		t.Errorf("explicitly blocked task should not be ready")
	}
}

func TestCycleDetectionSimple(t *testing.T) {
	tasks := []state.Task{
		{TaskID: "a", Status: state.TaskPending, DependsOn: []string{"b"}},
		{TaskID: "b", Status: state.TaskPending, DependsOn: []string{"a"}},
	}
	cycles := DetectCycles(tasks)
	if len(cycles) != 1 {
		t.Fatalf("got %d cycles, want 1", len(cycles))
	}
}

func TestCycleDetectionComplex(t *testing.T) {
	tasks := []state.Task{
		{TaskID: "a", Status: state.TaskPending, DependsOn: []string{"b"}},
		{TaskID: "b", Status: state.TaskPending, DependsOn: []string{"c"}},
		{TaskID: "c", Status: state.TaskPending, DependsOn: []string{"a"}},
	}
	cycles := DetectCycles(tasks)
	if len(cycles) != 1 {
		t.Fatalf("got %d cycles, want 1", len(cycles))
	}
}

func TestCycleIgnoresClosed(t *testing.T) {
	tasks := []state.Task{
		{TaskID: "a", Status: state.TaskPending, DependsOn: []string{"b"}},
		{TaskID: "b", Status: state.TaskDone, DependsOn: []string{"a"}},
	}
	cycles := DetectCycles(tasks)
	if len(cycles) != 0 {
		t.Errorf("got %d cycles, want 0 (closed nodes excluded)", len(cycles))
	}
}

func TestSelfLoop(t *testing.T) {
	tasks := []state.Task{
		{TaskID: "a", Status: state.TaskPending, DependsOn: []string{"a"}},
	}
	cycles := DetectCycles(tasks)
	if len(cycles) != 1 {
		t.Fatalf("got %d cycles, want 1 (self-loop)", len(cycles))
	}
}

func TestNoCycles(t *testing.T) {
	tasks := []state.Task{
		{TaskID: "a", Status: state.TaskPending},
		{TaskID: "b", Status: state.TaskPending, DependsOn: []string{"a"}},
		{TaskID: "c", Status: state.TaskPending, DependsOn: []string{"b"}},
	}
	cycles := DetectCycles(tasks)
	if len(cycles) != 0 {
		t.Errorf("got %d cycles, want 0", len(cycles))
	}
}

func TestCycleNormalization(t *testing.T) {
	tasks := []state.Task{
		{TaskID: "c", Status: state.TaskPending, DependsOn: []string{"a"}},
		{TaskID: "a", Status: state.TaskPending, DependsOn: []string{"b"}},
		{TaskID: "b", Status: state.TaskPending, DependsOn: []string{"c"}},
	}
	cycles := DetectCycles(tasks)
	if len(cycles) != 1 {
		t.Fatalf("got %d cycles, want 1", len(cycles))
	}
	if cycles[0][0] != "a" {
		t.Errorf("cycle start = %q, want 'a' (lexicographic normalization)", cycles[0][0])
	}
}

func TestClaimedTaskNotReady(t *testing.T) {
	tasks := []state.Task{
		{TaskID: "claimed-task", Status: state.TaskClaimed},
	}
	ready := ReadyFilter(tasks)
	if len(ready) != 0 {
		t.Errorf("got %d ready, want 0 — claimed tasks must not be dispatchable", len(ready))
	}
}

func TestImplementingTaskNotReady(t *testing.T) {
	tasks := []state.Task{
		{TaskID: "impl-task", Status: state.TaskImplementing},
	}
	ready := ReadyFilter(tasks)
	if len(ready) != 0 {
		t.Errorf("got %d ready, want 0 — implementing tasks must not be dispatchable", len(ready))
	}
}

func TestRepairPendingTaskReady(t *testing.T) {
	tasks := []state.Task{
		{TaskID: "repair-task", Status: state.TaskRepairPending},
	}
	ready := ReadyFilter(tasks)
	if len(ready) != 1 {
		t.Errorf("got %d ready, want 1 — repair_pending tasks should be dispatchable", len(ready))
	}
}
