package ticket

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gofrs/flock"

	"github.com/php-workx/fabrikk/internal/state"
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
	if err := ValidateID(runID); err != nil {
		return fmt.Errorf("invalid run ID: %w", err)
	}
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
// On state.ErrPartialRead, returns successfully-parsed tasks alongside the error.
func (s *Store) ReadTasks(runID string) ([]state.Task, error) {
	all, err := s.readAll()
	if err != nil && !errors.Is(err, state.ErrPartialRead) {
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
// Removes orphaned tasks from previous compilations (tasks with this runID
// as parent that are not in the new task set).
// Does not mutate the input slice — works on copies.
func (s *Store) WriteTasks(runID string, tasks []state.Task) error {
	if err := s.CreateRun(runID); err != nil {
		return fmt.Errorf("create run epic: %w", err)
	}

	newIDs := make(map[string]struct{}, len(tasks))
	for i := range tasks {
		newIDs[tasks[i].TaskID] = struct{}{}
	}

	for i := range tasks {
		if err := s.writeOrUpdateTask(runID, &tasks[i]); err != nil {
			return err
		}
	}

	s.removeOrphanedTasks(runID, newIDs)
	return nil
}

// writeOrUpdateTask writes a single task, preserving the body if the file already exists.
func (s *Store) writeOrUpdateTask(runID string, task *state.Task) error {
	taskCopy := *task // copy — do not mutate caller's slice
	if err := ValidateID(taskCopy.TaskID); err != nil {
		return fmt.Errorf("invalid task ID %s: %w", taskCopy.TaskID, err)
	}
	taskCopy.ParentTaskID = runID
	path := filepath.Join(s.Dir, taskCopy.TaskID+".md")

	// If file exists, preserve body via frontmatter-only update.
	if existing, readErr := os.ReadFile(path); readErr == nil {
		if updated, fmErr := UpdateFrontmatter(existing, &taskCopy); fmErr == nil {
			if writeErr := atomicWrite(path, updated); writeErr != nil {
				return fmt.Errorf("update task %s: %w", taskCopy.TaskID, writeErr)
			}
			return nil
		}
	}

	// New file — full marshal.
	data, err := MarshalTicket(&taskCopy)
	if err != nil {
		return fmt.Errorf("marshal task %s: %w", taskCopy.TaskID, err)
	}
	if err := s.writeFile(taskCopy.TaskID, data); err != nil {
		return fmt.Errorf("write task %s: %w", taskCopy.TaskID, err)
	}
	return nil
}

// removeOrphanedTasks removes tasks from previous compilations that are not in the new set.
func (s *Store) removeOrphanedTasks(runID string, newIDs map[string]struct{}) {
	existing, _ := s.ReadTasks(runID)
	for i := range existing {
		if _, ok := newIDs[existing[i].TaskID]; !ok {
			orphanPath := filepath.Join(s.Dir, existing[i].TaskID+".md")
			_ = os.Remove(orphanPath) // best-effort cleanup
		}
	}
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
		return nil, fmt.Errorf(errWrapFmt, ErrTicketNotFound, err)
	}
	task, err := UnmarshalTicket(data)
	if err != nil {
		return nil, err
	}
	return task, nil
}

// WriteTask updates an existing task's frontmatter while preserving the body.
func (s *Store) WriteTask(task *state.Task) error {
	if err := ValidateID(task.TaskID); err != nil {
		return fmt.Errorf("invalid task ID: %w", err)
	}
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

// UpdateStatus updates a task's status (both extended_status and tk status).
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
			return fmt.Errorf(errWrapFmt, ErrTicketNotFound, readErr)
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
			return fmt.Errorf(errWrapFmt, ErrTicketNotFound, readErr)
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
	// Validate and canonicalize the dependency target.
	resolvedDepID, depErr := ResolveID(s.Dir, depID)
	if depErr != nil {
		return fmt.Errorf("dep target %s: %w", depID, depErr)
	}
	path := filepath.Join(s.Dir, resolvedID+".md")

	return s.withLock(path, func() error {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf(errWrapFmt, ErrTicketNotFound, readErr)
		}
		task, parseErr := UnmarshalTicket(data)
		if parseErr != nil {
			return parseErr
		}
		for _, d := range task.DependsOn {
			if d == resolvedDepID {
				return nil // already exists
			}
		}
		task.DependsOn = append(task.DependsOn, resolvedDepID)
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
	// Canonicalize depID so it matches what AddDep stored.
	resolvedDepID, depErr := ResolveID(s.Dir, depID)
	if depErr != nil {
		resolvedDepID = depID // fall back to raw input if target doesn't exist
	}
	path := filepath.Join(s.Dir, resolvedID+".md")

	return s.withLock(path, func() error {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf(errWrapFmt, ErrTicketNotFound, readErr)
		}
		task, parseErr := UnmarshalTicket(data)
		if parseErr != nil {
			return parseErr
		}
		filtered := make([]string, 0, len(task.DependsOn))
		for _, d := range task.DependsOn {
			if d != resolvedDepID {
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
	if err != nil && !errors.Is(err, state.ErrPartialRead) {
		return nil, err
	}
	return ReadyFilter(tasks), err
}

// BlockedTasks returns tasks scoped to a run that are open with unresolved deps.
func (s *Store) BlockedTasks(runID string) ([]state.Task, error) {
	tasks, err := s.ReadTasks(runID)
	if err != nil && !errors.Is(err, state.ErrPartialRead) {
		return nil, err
	}
	return BlockedFilter(tasks), err
}

// DetectCycles finds dependency cycles scoped to a run.
func (s *Store) DetectCycles(runID string) ([][]string, error) {
	tasks, err := s.ReadTasks(runID)
	if err != nil && !errors.Is(err, state.ErrPartialRead) {
		return nil, err
	}
	return DetectCycles(tasks), err
}

// Link creates a bidirectional link between two tickets.
func (s *Store) Link(id, targetID string) error {
	resolvedID, err := ResolveID(s.Dir, id)
	if err != nil {
		return err
	}
	resolvedTargetID, err := ResolveID(s.Dir, targetID)
	if err != nil {
		return fmt.Errorf("link target %s: %w", targetID, err)
	}

	path := filepath.Join(s.Dir, resolvedID+".md")
	targetPath := filepath.Join(s.Dir, resolvedTargetID+".md")
	return s.withLocks([]string{path, targetPath}, func() error {
		return updateBidirectionalTicketLinks(path, resolvedTargetID, targetPath, resolvedID, appendUniqueLink)
	})
}

// Unlink removes a bidirectional link.
func (s *Store) Unlink(id, targetID string) error {
	resolvedID, err := ResolveID(s.Dir, id)
	if err != nil {
		return err
	}
	resolvedTargetID, err := ResolveID(s.Dir, targetID)
	if err != nil {
		return fmt.Errorf("link target %s: %w", targetID, err)
	}

	path := filepath.Join(s.Dir, resolvedID+".md")
	targetPath := filepath.Join(s.Dir, resolvedTargetID+".md")
	return s.withLocks([]string{path, targetPath}, func() error {
		return updateBidirectionalTicketLinks(path, resolvedTargetID, targetPath, resolvedID, removeLink)
	})
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
		return tasks, fmt.Errorf("%w: %s", state.ErrPartialRead, strings.Join(skipped, "; "))
	}
	return tasks, nil
}

// writeFile writes a ticket file with atomic write and directory creation.
func (s *Store) writeFile(id string, data []byte) error {
	if err := ValidateID(id); err != nil {
		return fmt.Errorf("invalid ticket ID: %w", err)
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return fmt.Errorf("create tickets dir: %w", err)
	}
	path := filepath.Join(s.Dir, id+".md")
	return atomicWrite(path, data)
}

// withLock executes fn while holding an exclusive file lock.
func (s *Store) withLock(path string, fn func() error) error {
	return s.withLocks([]string{path}, fn)
}

// withLocks executes fn while holding exclusive locks in stable path order.
func (s *Store) withLocks(paths []string, fn func() error) error {
	paths = uniqueSortedPaths(paths)
	locks := make([]*flock.Flock, 0, len(paths))
	for _, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("create lock dir %s: %w", filepath.Dir(path), err)
		}
		lockPath := path + ".lock"
		fl := flock.New(lockPath)
		if err := fl.Lock(); err != nil {
			for i := len(locks) - 1; i >= 0; i-- {
				_ = locks[i].Unlock()
			}
			return fmt.Errorf("acquire lock %s: %w", lockPath, err)
		}
		locks = append(locks, fl)
	}
	defer func() {
		for i := len(locks) - 1; i >= 0; i-- {
			_ = locks[i].Unlock()
		}
	}()
	return fn()
}

func uniqueSortedPaths(paths []string) []string {
	sorted := append([]string(nil), paths...)
	sort.Strings(sorted)
	unique := sorted[:0]
	for _, path := range sorted {
		if len(unique) == 0 || unique[len(unique)-1] != path {
			unique = append(unique, path)
		}
	}
	return unique
}

type ticketLinkUpdate struct {
	path     string
	previous []byte
	updated  []byte
	changed  bool
}

func updateBidirectionalTicketLinks(path, linkID, targetPath, targetID string, mutate func([]string, string) ([]string, bool)) error {
	first, err := prepareTicketLinkUpdate(path, linkID, mutate)
	if err != nil {
		return err
	}
	if targetPath == path {
		if !first.changed {
			return nil
		}
		return atomicWrite(first.path, first.updated)
	}
	second, err := prepareTicketLinkUpdate(targetPath, targetID, mutate)
	if err != nil {
		return err
	}
	return applyTicketLinkUpdates(first, second)
}

func prepareTicketLinkUpdate(path, linkID string, mutate func([]string, string) ([]string, bool)) (ticketLinkUpdate, error) {
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		return ticketLinkUpdate{}, fmt.Errorf(errWrapFmt, ErrTicketNotFound, readErr)
	}
	fm, body, err := splitFrontmatterBody(data)
	if err != nil {
		return ticketLinkUpdate{}, err
	}
	links, changed := mutate(fm.Links, linkID)
	if !changed {
		return ticketLinkUpdate{path: path, previous: data, changed: false}, nil
	}
	fm.Links = links
	fm.UpdatedAt = formatTime(time.Now())

	updated, err := renderFrontmatterBody(fm, body)
	if err != nil {
		return ticketLinkUpdate{}, err
	}
	return ticketLinkUpdate{path: path, previous: data, updated: updated, changed: true}, nil
}

func applyTicketLinkUpdates(updates ...ticketLinkUpdate) error {
	applied := make([]ticketLinkUpdate, 0, len(updates))
	for _, update := range updates {
		if !update.changed {
			continue
		}
		if err := atomicWrite(update.path, update.updated); err != nil {
			for i := len(applied) - 1; i >= 0; i-- {
				if rollbackErr := atomicWrite(applied[i].path, applied[i].previous); rollbackErr != nil {
					return fmt.Errorf("%w (rollback %s: %v)", err, applied[i].path, rollbackErr)
				}
			}
			return err
		}
		applied = append(applied, update)
	}
	return nil
}

func appendUniqueLink(links []string, linkID string) ([]string, bool) {
	for _, link := range links {
		if link == linkID {
			return links, false
		}
	}
	return append(links, linkID), true
}

func removeLink(links []string, linkID string) ([]string, bool) {
	filtered := links[:0]
	changed := false
	for _, link := range links {
		if link == linkID {
			changed = true
			continue
		}
		filtered = append(filtered, link)
	}
	return filtered, changed
}

// atomicWrite writes data to path using temp file + fsync + rename + dir fsync.
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	f, err := os.CreateTemp(dir, ".fabrikk-ticket-*")
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
