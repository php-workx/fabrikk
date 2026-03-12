package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// RunDir manages the .attest/runs/<run-id>/ directory structure (spec section 3.1).
type RunDir struct {
	Root string // .attest/runs/<run-id>
}

// NewRunDir creates a RunDir for the given base directory and run ID.
func NewRunDir(baseDir, runID string) *RunDir {
	return &RunDir{
		Root: filepath.Join(baseDir, ".attest", "runs", runID),
	}
}

// Init creates the run directory structure.
func (d *RunDir) Init() error {
	dirs := []string{
		d.Root,
		filepath.Join(d.Root, "claims"),
		filepath.Join(d.Root, "reports"),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}
	return nil
}

// Path returns the full path for a file relative to the run directory.
func (d *RunDir) Path(rel string) string {
	return filepath.Join(d.Root, rel)
}

// Artifact returns the path to run-artifact.json.
func (d *RunDir) Artifact() string { return d.Path("run-artifact.json") }

// Tasks returns the path to tasks.json.
func (d *RunDir) Tasks() string { return d.Path("tasks.json") }

// Coverage returns the path to requirement-coverage.json.
func (d *RunDir) Coverage() string { return d.Path("requirement-coverage.json") }

// Status returns the path to run-status.json.
func (d *RunDir) Status() string { return d.Path("run-status.json") }

// Events returns the path to events.jsonl.
func (d *RunDir) Events() string { return d.Path("events.jsonl") }

// Clarifications returns the path to clarifications.json.
func (d *RunDir) Clarifications() string { return d.Path("clarifications.json") }

// Engine returns the path to engine.json.
func (d *RunDir) Engine() string { return d.Path("engine.json") }

// EngineLog returns the path to engine.log.
func (d *RunDir) EngineLog() string { return d.Path("engine.log") }

// ClaimPath returns the path to a specific claim file.
func (d *RunDir) ClaimPath(taskID string) string {
	return filepath.Join(d.Root, "claims", taskID+".json")
}

// ReportDir returns the report directory for a specific task.
func (d *RunDir) ReportDir(taskID string) string {
	return filepath.Join(d.Root, "reports", taskID)
}

// AttemptPath returns the path to attempt.json for a specific task.
func (d *RunDir) AttemptPath(taskID string) string {
	return filepath.Join(d.ReportDir(taskID), "attempt.json")
}

// CouncilResultPath returns the path to council-result.json for a specific task.
func (d *RunDir) CouncilResultPath(taskID string) string {
	return filepath.Join(d.ReportDir(taskID), "council-result.json")
}

// WriteArtifact writes the run artifact atomically.
func (d *RunDir) WriteArtifact(a *RunArtifact) error {
	return WriteJSON(d.Artifact(), a)
}

// ReadArtifact reads the run artifact.
func (d *RunDir) ReadArtifact() (*RunArtifact, error) {
	var a RunArtifact
	if err := ReadJSON(d.Artifact(), &a); err != nil {
		return nil, err
	}
	return &a, nil
}

// WriteTasks writes the task list atomically.
func (d *RunDir) WriteTasks(tasks []Task) error {
	return WriteJSON(d.Tasks(), tasks)
}

// ReadTasks reads the task list.
func (d *RunDir) ReadTasks() ([]Task, error) {
	var tasks []Task
	if err := ReadJSON(d.Tasks(), &tasks); err != nil {
		return nil, err
	}
	return tasks, nil
}

// WriteCoverage writes the requirement coverage atomically.
func (d *RunDir) WriteCoverage(cov []RequirementCoverage) error {
	return WriteJSON(d.Coverage(), cov)
}

// ReadCoverage reads the requirement coverage.
func (d *RunDir) ReadCoverage() ([]RequirementCoverage, error) {
	var cov []RequirementCoverage
	if err := ReadJSON(d.Coverage(), &cov); err != nil {
		return nil, err
	}
	return cov, nil
}

// WriteStatus writes the run status atomically.
func (d *RunDir) WriteStatus(s *RunStatus) error {
	return WriteJSON(d.Status(), s)
}

// ReadStatus reads the run status.
func (d *RunDir) ReadStatus() (*RunStatus, error) {
	var s RunStatus
	if err := ReadJSON(d.Status(), &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// AppendEvent appends an event to the event log.
func (d *RunDir) AppendEvent(e Event) error {
	data, err := encodeJSON(e)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	f, err := os.OpenFile(d.Events(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open events: %w", err)
	}
	defer func() { _ = f.Close() }()

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write event: %w", err)
	}
	return f.Sync()
}

func encodeJSON(v any) ([]byte, error) {
	return json.Marshal(v)
}
