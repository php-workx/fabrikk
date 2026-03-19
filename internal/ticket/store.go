package ticket

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"

	"github.com/runger/attest/internal/state"
)

// Store implements state.TaskStore using Ticket-format markdown files.
// Compatible with the tk CLI (wedow/ticket).
type Store struct {
	Dir string // path to .tickets/ directory
}

// NewStore creates a ticket store for the given directory.
func NewStore(dir string) *Store {
	return &Store{Dir: dir}
}

// CreateRun creates an epic ticket for a run. Idempotent — skips if epic exists.
func (s *Store) CreateRun(runID string) error {
	epicPath := filepath.Join(s.Dir, runID+".md")
	if _, err := os.Stat(epicPath); err == nil {
		return nil // epic already exists
	}

	epic := &state.Task{
		TaskID:    runID,
		Title:     fmt.Sprintf("Run %s", runID),
		TaskType:  "epic",
		Status:    state.TaskPending,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	data, err := MarshalTicket(epic)
	if err != nil {
		return fmt.Errorf("marshal epic: %w", err)
	}
	return s.writeFile(runID, data)
}

// ReadTasks returns all tasks scoped to a run (where parent == runID).
// On ErrPartialRead, returns successfully-parsed tasks alongside the error.
func (s *Store) ReadTasks(runID string) ([]state.Task, error) {
	all, err := s.readAll()
	if err != nil && !errors.Is(err, ErrPartialRead) {
		return nil, err
	}
	var tasks []state.Task
	for i := range all {
		if all[i].ParentTaskID == runID {
			tasks = append(tasks, all[i])
		}
	}
	return tasks, err
}

// WriteTasks creates an epic for the run and writes all tasks as children.
// For existing tickets, preserves the markdown body (notes, descriptions).
// Does not mutate the input slice — works on copies.
func (s *Store) WriteTasks(runID string, tasks []state.Task) error {
	if err := s.CreateRun(runID); err != nil {
		return fmt.Errorf("create run epic: %w", err)
	}
	for i := range tasks {
		task := tasks[i] // copy — do not mutate caller's slice
		task.ParentTaskID = runID
		path := filepath.Join(s.Dir, task.TaskID+".md")

		// If file exists, preserve body via frontmatter-only update.
		if existing, readErr := os.ReadFile(path); readErr == nil {
			updated, fmErr := UpdateFrontmatter(existing, &task)
			if fmErr == nil {
				if writeErr := atomicWrite(path, updated); writeErr != nil {
					return fmt.Errorf("update task %s: %w", task.TaskID, writeErr)
				}
				continue
			}
		}

		// New file — full marshal.
		data, err := MarshalTicket(&task)
		if err != nil {
			return fmt.Errorf("marshal task %s: %w", task.TaskID, err)
		}
		if err := s.writeFile(task.TaskID, data); err != nil {
			return fmt.Errorf("write task %s: %w", task.TaskID, err)
		}
	}
	return nil
}

// ReadTask reads a single task by ID (supports partial matching).
func (s *Store) ReadTask(taskID string) (*state.Task, error) {
	resolvedID, err := ResolveID(s.Dir, taskID)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(s.Dir, resolvedID+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTicketNotFound, err)
	}
	task, err := UnmarshalTicket(data)
	if err != nil {
		return nil, err
	}
	return task, nil
}

// WriteTask updates an existing task's frontmatter while preserving the body.
func (s *Store) WriteTask(task *state.Task) error {
	path := filepath.Join(s.Dir, task.TaskID+".md")

	return s.withLock(path, func() error {
		existing, err := os.ReadFile(path)
		if err != nil {
			// File doesn't exist yet — write full ticket.
			data, marshalErr := MarshalTicket(task)
			if marshalErr != nil {
				return marshalErr
			}
			return atomicWrite(path, data)
		}

		// Update frontmatter only, preserve body.
		task.UpdatedAt = time.Now()
		updated, err := UpdateFrontmatter(existing, task)
		if err != nil {
			return err
		}
		return atomicWrite(path, updated)
	})
}

// UpdateStatus updates a task's status (both attest_status and tk status).
// Read and write are inside the same lock to prevent race conditions.
func (s *Store) UpdateStatus(taskID string, status state.TaskStatus, reason string) error {
	resolvedID, err := ResolveID(s.Dir, taskID)
	if err != nil {
		return err
	}
	path := filepath.Join(s.Dir, resolvedID+".md")

	return s.withLock(path, func() error {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("%w: %v", ErrTicketNotFound, readErr)
		}
		task, parseErr := UnmarshalTicket(data)
		if parseErr != nil {
			return parseErr
		}
		task.Status = status
		task.StatusReason = reason
		task.UpdatedAt = time.Now()

		updated, fmErr := UpdateFrontmatter(data, task)
		if fmErr != nil {
			return fmErr
		}
		return atomicWrite(path, updated)
	})
}

// AddNote appends a timestamped note to a ticket's Notes section.
func (s *Store) AddNote(id, text string) error {
	resolvedID, err := ResolveID(s.Dir, id)
	if err != nil {
		return err
	}
	path := filepath.Join(s.Dir, resolvedID+".md")

	return s.withLock(path, func() error {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("%w: %v", ErrTicketNotFound, readErr)
		}

		content := string(data)
		note := fmt.Sprintf("\n**%s**\n\n%s\n", time.Now().UTC().Format(time.RFC3339), text)

		if !strings.Contains(content, "## Notes") {
			content += "\n## Notes\n"
		}
		content += note

		return atomicWrite(path, []byte(content))
	})
}

// AddDep adds a directional dependency (id depends on depID).
// The full read-check-write is inside a single lock to prevent TOCTOU races.
// Returns ErrTicketNotFound if depID does not resolve to an existing ticket.
func (s *Store) AddDep(id, depID string) error {
	resolvedID, err := ResolveID(s.Dir, id)
	if err != nil {
		return err
	}
	// Validate that the dependency target exists.
	if _, depErr := ResolveID(s.Dir, depID); depErr != nil {
		return fmt.Errorf("dep target %s: %w", depID, depErr)
	}
	path := filepath.Join(s.Dir, resolvedID+".md")

	return s.withLock(path, func() error {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("%w: %v", ErrTicketNotFound, readErr)
		}
		task, parseErr := UnmarshalTicket(data)
		if parseErr != nil {
			return parseErr
		}
		for _, d := range task.DependsOn {
			if d == depID {
				return nil // already exists
			}
		}
		task.DependsOn = append(task.DependsOn, depID)
		task.UpdatedAt = time.Now()

		updated, fmErr := UpdateFrontmatter(data, task)
		if fmErr != nil {
			return fmErr
		}
		return atomicWrite(path, updated)
	})
}

// RemoveDep removes a dependency.
// The full read-filter-write is inside a single lock to prevent TOCTOU races.
func (s *Store) RemoveDep(id, depID string) error {
	resolvedID, err := ResolveID(s.Dir, id)
	if err != nil {
		return err
	}
	path := filepath.Join(s.Dir, resolvedID+".md")

	return s.withLock(path, func() error {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("%w: %v", ErrTicketNotFound, readErr)
		}
		task, parseErr := UnmarshalTicket(data)
		if parseErr != nil {
			return parseErr
		}
		filtered := make([]string, 0, len(task.DependsOn))
		for _, d := range task.DependsOn {
			if d != depID {
				filtered = append(filtered, d)
			}
		}
		task.DependsOn = filtered
		task.UpdatedAt = time.Now()

		updated, fmErr := UpdateFrontmatter(data, task)
		if fmErr != nil {
			return fmErr
		}
		return atomicWrite(path, updated)
	})
}

// ReadyTasks returns tasks scoped to a run that are open with all deps satisfied.
func (s *Store) ReadyTasks(runID string) ([]state.Task, error) {
	tasks, err := s.ReadTasks(runID)
	if err != nil && !errors.Is(err, ErrPartialRead) {
		return nil, err
	}
	return ReadyFilter(tasks), err
}

// BlockedTasks returns tasks scoped to a run that are open with unresolved deps.
func (s *Store) BlockedTasks(runID string) ([]state.Task, error) {
	tasks, err := s.ReadTasks(runID)
	if err != nil && !errors.Is(err, ErrPartialRead) {
		return nil, err
	}
	return BlockedFilter(tasks), err
}

// DetectCycles finds dependency cycles scoped to a run.
func (s *Store) DetectCycles(runID string) ([][]string, error) {
	tasks, err := s.ReadTasks(runID)
	if err != nil && !errors.Is(err, ErrPartialRead) {
		return nil, err
	}
	return DetectCycles(tasks), err
}

// Link creates a bidirectional link between two tickets.
func (s *Store) Link(_, _ string) error {
	return fmt.Errorf("Link not yet implemented")
}

// Unlink removes a bidirectional link.
func (s *Store) Unlink(_, _ string) error {
	return fmt.Errorf("Unlink not yet implemented")
}

// readAll reads all ticket files from the directory.
func (s *Store) readAll() ([]state.Task, error) {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return nil, fmt.Errorf("create tickets dir: %w", err)
	}
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return nil, fmt.Errorf("read tickets dir: %w", err)
	}

	var tasks []state.Task
	var skipped []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(s.Dir, e.Name())
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			skipped = append(skipped, fmt.Sprintf("%s: %v", e.Name(), readErr))
			continue
		}
		task, parseErr := UnmarshalTicket(data)
		if parseErr != nil {
			skipped = append(skipped, fmt.Sprintf("%s: %v", e.Name(), parseErr))
			continue
		}
		tasks = append(tasks, *task)
	}
	if len(skipped) > 0 {
		return tasks, fmt.Errorf("%w: %s", ErrPartialRead, strings.Join(skipped, "; "))
	}
	return tasks, nil
}

// writeFile writes a ticket file with atomic write and directory creation.
func (s *Store) writeFile(id string, data []byte) error {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return fmt.Errorf("create tickets dir: %w", err)
	}
	path := filepath.Join(s.Dir, id+".md")
	return atomicWrite(path, data)
}

// withLock executes fn while holding an exclusive file lock.
func (s *Store) withLock(path string, fn func() error) error {
	lockPath := path + ".lock"
	fl := flock.New(lockPath)
	if err := fl.Lock(); err != nil {
		return fmt.Errorf("acquire lock %s: %w", lockPath, err)
	}
	defer func() { _ = fl.Unlock() }()
	return fn()
}

// atomicWrite writes data to path using temp file + fsync + rename + dir fsync.
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	f, err := os.CreateTemp(dir, ".attest-ticket-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	// Fsync parent directory for durability.
	// Fsync parent directory — best-effort, rename already succeeded.
	d, openErr := os.Open(dir)
	if openErr == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
