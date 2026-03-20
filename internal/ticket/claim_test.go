package ticket

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/runger/attest/internal/state"
)

func writeClaimableTask(t *testing.T, store *Store, taskID string) {
	t.Helper()
	task := testTask()
	task.TaskID = taskID
	task.ParentTaskID = testRunID
	task.Status = state.TaskPending
	if err := store.WriteTask(&task); err != nil {
		t.Fatal(err)
	}
}

func TestClaimTaskSuccess(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	writeClaimableTask(t, store, "task-1")

	if err := store.ClaimTask("task-1", "agent-a", "claude", 5*time.Minute); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}

	// Verify status changed to claimed.
	task, err := store.ReadTask("task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != state.TaskClaimed {
		t.Errorf("status = %s, want claimed", task.Status)
	}

	// Verify claim fields in frontmatter.
	data, _ := os.ReadFile(filepath.Join(dir, "task-1.md"))
	content := string(data)
	if !strings.Contains(content, "claimed_by: agent-a") {
		t.Error("missing claimed_by in frontmatter")
	}
	if !strings.Contains(content, "claim_backend: claude") {
		t.Error("missing claim_backend in frontmatter")
	}
	if !strings.Contains(content, "claim_expires:") {
		t.Error("missing claim_expires in frontmatter")
	}
}

func TestClaimTaskAlreadyClaimed(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	writeClaimableTask(t, store, "task-1")

	if err := store.ClaimTask("task-1", "agent-a", "claude", 5*time.Minute); err != nil {
		t.Fatal(err)
	}

	err := store.ClaimTask("task-1", "agent-b", "codex", 5*time.Minute)
	if !errors.Is(err, ErrAlreadyClaimed) {
		t.Fatalf("expected ErrAlreadyClaimed, got: %v", err)
	}
}

func TestClaimTaskSameOwnerReclaim(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	writeClaimableTask(t, store, "task-1")

	if err := store.ClaimTask("task-1", "agent-a", "claude", 1*time.Minute); err != nil {
		t.Fatal(err)
	}

	// Same owner re-claim should succeed (idempotent, extends lease).
	if err := store.ClaimTask("task-1", "agent-a", "claude", 10*time.Minute); err != nil {
		t.Fatalf("same-owner re-claim failed: %v", err)
	}

	// Verify lease was extended.
	data, _ := os.ReadFile(filepath.Join(dir, "task-1.md"))
	content := string(data)
	if !strings.Contains(content, "claimed_by: agent-a") {
		t.Error("claim owner lost after re-claim")
	}
}

func TestClaimExpiredTaskReclaimable(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	writeClaimableTask(t, store, "task-1")

	// Claim with a lease that's already expired (negative duration trick).
	if err := store.ClaimTask("task-1", "agent-a", "claude", -1*time.Second); err != nil {
		t.Fatal(err)
	}

	// Different owner should be able to reclaim.
	if err := store.ClaimTask("task-1", "agent-b", "codex", 5*time.Minute); err != nil {
		t.Fatalf("expected expired claim to be reclaimable, got: %v", err)
	}

	task, _ := store.ReadTask("task-1")
	if task.Status != state.TaskClaimed {
		t.Errorf("status = %s, want claimed", task.Status)
	}
}

func TestClaimNonPendingTask(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	task := testTask()
	task.TaskID = "task-done"
	task.Status = state.TaskDone
	if err := store.WriteTask(&task); err != nil {
		t.Fatal(err)
	}

	err := store.ClaimTask("task-done", "agent-a", "claude", 5*time.Minute)
	if !errors.Is(err, ErrNotClaimable) {
		t.Fatalf("expected ErrNotClaimable, got: %v", err)
	}
}

func TestClaimRepairPendingTask(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	task := testTask()
	task.TaskID = "task-repair"
	task.Status = state.TaskRepairPending
	if err := store.WriteTask(&task); err != nil {
		t.Fatal(err)
	}

	if err := store.ClaimTask("task-repair", "agent-a", "claude", 5*time.Minute); err != nil {
		t.Fatalf("expected repair_pending to be claimable, got: %v", err)
	}
}

func TestReleaseClaimSuccess(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	writeClaimableTask(t, store, "task-1")

	if err := store.ClaimTask("task-1", "agent-a", "claude", 5*time.Minute); err != nil {
		t.Fatal(err)
	}

	if err := store.ReleaseClaim("task-1", "agent-a", state.TaskDone, "completed"); err != nil {
		t.Fatalf("ReleaseClaim: %v", err)
	}

	task, _ := store.ReadTask("task-1")
	if task.Status != state.TaskDone {
		t.Errorf("status = %s, want done", task.Status)
	}

	// Verify claim fields cleared.
	data, _ := os.ReadFile(filepath.Join(dir, "task-1.md"))
	content := string(data)
	if strings.Contains(content, "claimed_by:") {
		t.Error("claim fields not cleared after release")
	}
}

func TestReleaseClaimWrongOwner(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	writeClaimableTask(t, store, "task-1")

	if err := store.ClaimTask("task-1", "agent-a", "claude", 5*time.Minute); err != nil {
		t.Fatal(err)
	}

	err := store.ReleaseClaim("task-1", "agent-b", state.TaskDone, "")
	if !errors.Is(err, ErrNotClaimOwner) {
		t.Fatalf("expected ErrNotClaimOwner, got: %v", err)
	}
}

func TestReleaseUnclaimedTask(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	writeClaimableTask(t, store, "task-1")

	err := store.ReleaseClaim("task-1", "agent-a", state.TaskDone, "")
	if !errors.Is(err, ErrNotClaimed) {
		t.Fatalf("expected ErrNotClaimed, got: %v", err)
	}
}

func TestRenewClaimSuccess(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	writeClaimableTask(t, store, "task-1")

	if err := store.ClaimTask("task-1", "agent-a", "claude", 1*time.Minute); err != nil {
		t.Fatal(err)
	}

	// Read initial expiry.
	data1, _ := os.ReadFile(filepath.Join(dir, "task-1.md"))

	time.Sleep(10 * time.Millisecond) // ensure time advances

	if err := store.RenewClaim("task-1", "agent-a", 30*time.Minute); err != nil {
		t.Fatalf("RenewClaim: %v", err)
	}

	// Verify expiry was extended.
	data2, _ := os.ReadFile(filepath.Join(dir, "task-1.md"))
	if bytes.Equal(data1, data2) {
		t.Error("file unchanged after renew — expiry should have been extended")
	}
}

func TestRenewClaimWrongOwner(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	writeClaimableTask(t, store, "task-1")

	if err := store.ClaimTask("task-1", "agent-a", "claude", 5*time.Minute); err != nil {
		t.Fatal(err)
	}

	err := store.RenewClaim("task-1", "agent-b", 5*time.Minute)
	if !errors.Is(err, ErrNotClaimOwner) {
		t.Fatalf("expected ErrNotClaimOwner, got: %v", err)
	}
}

func TestReadClaimsForRun(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	// Create tasks in a run, claim some.
	writeClaimableTask(t, store, "task-1")
	writeClaimableTask(t, store, "task-2")
	writeClaimableTask(t, store, "task-3")

	_ = store.ClaimTask("task-1", "agent-a", "claude", 5*time.Minute)
	_ = store.ClaimTask("task-3", "agent-b", "codex", 5*time.Minute)

	claims, err := store.ReadClaimsForRun("run-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 2 {
		t.Fatalf("got %d claims, want 2", len(claims))
	}

	// Verify claim info.
	claimMap := make(map[string]ClaimInfo)
	for _, c := range claims {
		claimMap[c.TaskID] = c
	}
	if claimMap["task-1"].ClaimedBy != "agent-a" {
		t.Error("task-1 should be claimed by agent-a")
	}
	if claimMap["task-3"].Backend != "codex" {
		t.Error("task-3 should have backend codex")
	}
}

func TestReclaimExpired(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	writeClaimableTask(t, store, "task-1")
	writeClaimableTask(t, store, "task-2")

	// Claim both with already-expired leases.
	_ = store.ClaimTask("task-1", "agent-a", "claude", -1*time.Second)
	_ = store.ClaimTask("task-2", "agent-b", "codex", -1*time.Second)

	reclaimed, err := store.ReclaimExpired("run-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(reclaimed) != 2 {
		t.Fatalf("got %d reclaimed, want 2", len(reclaimed))
	}

	// Verify tasks are back to pending.
	for _, taskID := range reclaimed {
		task, readErr := store.ReadTask(taskID)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if task.Status != state.TaskPending {
			t.Errorf("task %s status = %s, want pending", taskID, task.Status)
		}
	}
}

func TestClaimPreservedThroughUpdateStatus(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	writeClaimableTask(t, store, "task-1")

	if err := store.ClaimTask("task-1", "agent-a", "claude", 5*time.Minute); err != nil {
		t.Fatal(err)
	}

	// UpdateStatus should preserve claim fields.
	if err := store.UpdateStatus("task-1", state.TaskImplementing, "starting work"); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "task-1.md"))
	content := string(data)
	if !strings.Contains(content, "claimed_by: agent-a") {
		t.Error("claim fields lost after UpdateStatus")
	}
	if !strings.Contains(content, "attest_status: implementing") {
		t.Error("attest_status not updated")
	}
}

func TestConcurrentClaimSameTask(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	writeClaimableTask(t, store, "task-race")

	const numAgents = 10
	var (
		wg       sync.WaitGroup
		wins     atomic.Int32
		failures atomic.Int32
	)

	wg.Add(numAgents)
	for i := 0; i < numAgents; i++ {
		go func(n int) {
			defer wg.Done()
			err := store.ClaimTask("task-race", fmt.Sprintf("agent-%d", n), "claude", 5*time.Minute)
			if err == nil {
				wins.Add(1)
			} else if errors.Is(err, ErrAlreadyClaimed) {
				failures.Add(1)
			}
		}(i)
	}
	wg.Wait()

	if wins.Load() != 1 {
		t.Errorf("got %d winners, want exactly 1", wins.Load())
	}
	if failures.Load() != int32(numAgents-1) {
		t.Errorf("got %d ErrAlreadyClaimed, want %d", failures.Load(), numAgents-1)
	}
}
