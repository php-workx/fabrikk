package ticket

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/runger/attest/internal/state"
)

const testRunID = "run-1"

func TestCreateAndRead(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	task := testTask()
	if err := store.WriteTask(&task); err != nil {
		t.Fatalf("WriteTask: %v", err)
	}

	got, err := store.ReadTask("task-slice-1")
	if err != nil {
		t.Fatalf("ReadTask: %v", err)
	}
	if got.TaskID != task.TaskID {
		t.Errorf("TaskID = %q, want %q", got.TaskID, task.TaskID)
	}
	if got.Title != task.Title {
		t.Errorf("Title = %q, want %q", got.Title, task.Title)
	}
}

func TestPartialIDMatch(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	const testID = "abc-1234"
	task := testTask()
	task.TaskID = testID
	if err := store.WriteTask(&task); err != nil {
		t.Fatal(err)
	}

	// Full match.
	got, err := store.ReadTask(testID)
	if err != nil {
		t.Fatalf("full match: %v", err)
	}
	if got.TaskID != testID {
		t.Errorf("full match ID = %q", got.TaskID)
	}

	// Partial match.
	got, err = store.ReadTask("1234")
	if err != nil {
		t.Fatalf("partial match: %v", err)
	}
	if got.TaskID != testID {
		t.Errorf("partial match ID = %q", got.TaskID)
	}
}

func TestAmbiguousID(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	for _, id := range []string{"abc-1234", "abc-1235"} {
		task := testTask()
		task.TaskID = id
		if err := store.WriteTask(&task); err != nil {
			t.Fatal(err)
		}
	}

	_, err := store.ReadTask("abc-123")
	if err == nil {
		t.Fatal("should return ambiguous error")
	}
}

func TestNotFoundID(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	_, err := store.ReadTask("nonexistent")
	if err == nil {
		t.Fatal("should return not found error")
	}
}

func TestUpdateStatus(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	task := testTask()
	if err := store.WriteTask(&task); err != nil {
		t.Fatal(err)
	}

	if err := store.UpdateStatus("task-slice-1", state.TaskDone, "verified"); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}

	got, err := store.ReadTask("task-slice-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != state.TaskDone {
		t.Errorf("Status = %q, want done", got.Status)
	}
	if got.StatusReason != "verified" {
		t.Errorf("StatusReason = %q, want verified", got.StatusReason)
	}
}

func TestWriteTasksCreatesDirectoryAndEpic(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "subdir", ".tickets")
	store := NewStore(dir)

	tasks := []state.Task{testTask()}
	if err := store.WriteTasks("run-123", tasks); err != nil {
		t.Fatalf("WriteTasks: %v", err)
	}

	// Epic should exist.
	epicPath := filepath.Join(dir, "run-123.md")
	if _, err := os.Stat(epicPath); err != nil {
		t.Errorf("epic file missing: %v", err)
	}

	// Task should exist with parent.
	got, err := store.ReadTask("task-slice-1")
	if err != nil {
		t.Fatalf("ReadTask: %v", err)
	}
	if got.ParentTaskID != "run-123" {
		t.Errorf("ParentTaskID = %q, want run-123", got.ParentTaskID)
	}
}

func TestReadTasksScopedToRun(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	// Write tasks for two runs.
	task1 := testTask()
	task1.TaskID = "task-a"
	task1.ParentTaskID = testRunID
	task2 := testTask()
	task2.TaskID = "task-b"
	task2.ParentTaskID = "run-2"

	if err := store.WriteTask(&task1); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteTask(&task2); err != nil {
		t.Fatal(err)
	}

	// ReadTasks for run-1 should only return task-a.
	tasks, err := store.ReadTasks(testRunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].TaskID != "task-a" {
		t.Errorf("ReadTasks(run-1) = %v, want [task-a]", tasks)
	}
}

func TestPartialIDMatchPrefix(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	const testID = "abc-1234"
	task := testTask()
	task.TaskID = testID
	if err := store.WriteTask(&task); err != nil {
		t.Fatal(err)
	}

	// Match by prefix "abc-1".
	got, err := store.ReadTask("abc-1")
	if err != nil {
		t.Fatalf("prefix match: %v", err)
	}
	if got.TaskID != testID {
		t.Errorf("prefix match ID = %q, want %q", got.TaskID, testID)
	}
}

func TestConcurrentWriteSameTicket(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	// Create initial ticket.
	task := testTask()
	task.TaskID = "shared"
	if err := store.WriteTask(&task); err != nil {
		t.Fatal(err)
	}

	// 10 goroutines updating the same ticket concurrently.
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_ = store.UpdateStatus("shared", state.TaskPending, fmt.Sprintf("update-%d", n))
		}(i)
	}
	wg.Wait()

	// Ticket should still be readable and valid.
	got, err := store.ReadTask("shared")
	if err != nil {
		t.Fatalf("ReadTask after concurrent writes: %v", err)
	}
	if got.TaskID != "shared" {
		t.Errorf("TaskID = %q, want shared", got.TaskID)
	}
}

func TestConcurrentWritesDifferentTickets(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			task := testTask()
			task.TaskID = fmt.Sprintf("task-%d", n)
			_ = store.WriteTask(&task)
		}(i)
	}
	wg.Wait()

	// All 10 should exist.
	tasks, err := store.readAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 10 {
		t.Errorf("got %d tasks, want 10", len(tasks))
	}
}

func TestReadAllReturnsPartialReadOnCorruptFile(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	// Write a valid task.
	task := testTask()
	task.TaskID = "valid-task"
	task.ParentTaskID = testRunID
	if err := store.WriteTask(&task); err != nil {
		t.Fatal(err)
	}

	// Write a corrupt .md file.
	corruptPath := filepath.Join(dir, "corrupt.md")
	if err := os.WriteFile(corruptPath, []byte("not valid yaml at all"), 0o644); err != nil {
		t.Fatal(err)
	}

	// readAll should return the valid task AND an ErrPartialRead error.
	tasks, err := store.readAll()
	if err == nil {
		t.Fatal("expected ErrPartialRead, got nil")
	}
	if !errors.Is(err, ErrPartialRead) {
		t.Fatalf("expected ErrPartialRead, got: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("got %d tasks, want 1 (the valid one)", len(tasks))
	}
	if tasks[0].TaskID != "valid-task" {
		t.Errorf("got task ID %q, want valid-task", tasks[0].TaskID)
	}
}

func TestReadTasksPropagatesPartialRead(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	// Write a valid task scoped to a run.
	task := testTask()
	task.TaskID = "task-ok"
	task.ParentTaskID = testRunID
	if err := store.WriteTask(&task); err != nil {
		t.Fatal(err)
	}

	// Write a corrupt file.
	if err := os.WriteFile(filepath.Join(dir, "bad.md"), []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}

	// ReadTasks should return the valid task and propagate ErrPartialRead.
	tasks, err := store.ReadTasks(testRunID)
	if !errors.Is(err, ErrPartialRead) {
		t.Fatalf("expected ErrPartialRead, got: %v", err)
	}
	if len(tasks) != 1 || tasks[0].TaskID != "task-ok" {
		t.Errorf("got tasks %v, want [task-ok]", tasks)
	}
}

func TestAtomicWriteDoesNotCorruptOnPartialFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "task-atomic.md")

	// Write initial content.
	original := []byte("---\nid: task-atomic\nstatus: open\npriority: 0\norder: 0\n---\n# Original\n")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}

	// Simulate a "crash" by writing a leftover temp file (as if atomicWrite
	// was interrupted after creating the temp but before rename).
	leftover := filepath.Join(dir, ".attest-ticket-leftover")
	if err := os.WriteFile(leftover, []byte("partial garbage"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The original file should be intact — leftover temp doesn't affect it.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, original) {
		t.Error("original file was corrupted by leftover temp")
	}

	// A subsequent atomicWrite should succeed, overwriting the original.
	replacement := []byte("---\nid: task-atomic\nstatus: closed\npriority: 0\norder: 0\n---\n# Updated\n")
	if err := atomicWrite(path, replacement); err != nil {
		t.Fatalf("atomicWrite after simulated crash: %v", err)
	}

	// Verify the new content is correct.
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, replacement) {
		t.Error("atomicWrite did not produce expected content after simulated crash")
	}

	// Verify the temp file from the successful write was cleaned up
	// (os.CreateTemp creates new files, doesn't reuse the leftover).
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != "task-atomic.md" && e.Name() != ".attest-ticket-leftover" {
			t.Errorf("unexpected file left behind: %s", e.Name())
		}
	}
}

func TestAddDepValidatesDepTargetExists(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	task := testTask()
	task.TaskID = "task-source"
	if err := store.WriteTask(&task); err != nil {
		t.Fatal(err)
	}

	// Adding a dep on a nonexistent target should fail.
	err := store.AddDep("task-source", "nonexistent-dep")
	if err == nil {
		t.Fatal("expected error when dep target does not exist")
	}
	if !errors.Is(err, ErrTicketNotFound) {
		t.Fatalf("expected ErrTicketNotFound, got: %v", err)
	}

	// Adding a dep on an existing target should succeed.
	dep := testTask()
	dep.TaskID = "task-dep"
	if err := store.WriteTask(&dep); err != nil {
		t.Fatal(err)
	}
	if err := store.AddDep("task-source", "task-dep"); err != nil {
		t.Fatalf("AddDep on existing target: %v", err)
	}

	// Verify the dep was recorded.
	updated, err := store.ReadTask("task-source")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range updated.DependsOn {
		if d == "task-dep" {
			found = true
		}
	}
	if !found {
		t.Error("dep was not recorded on source task")
	}
}
