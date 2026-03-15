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

// TechnicalSpec returns the path to technical-spec.md.
func (d *RunDir) TechnicalSpec() string { return d.Path("technical-spec.md") }

// TechnicalSpecReview returns the path to technical-spec-review.json.
func (d *RunDir) TechnicalSpecReview() string { return d.Path("technical-spec-review.json") }

// TechnicalSpecApproval returns the path to technical-spec-approval.json.
func (d *RunDir) TechnicalSpecApproval() string { return d.Path("technical-spec-approval.json") }

// ExecutionPlan returns the path to execution-plan.json.
func (d *RunDir) ExecutionPlan() string { return d.Path("execution-plan.json") }

// ExecutionPlanMarkdown returns the path to execution-plan.md.
func (d *RunDir) ExecutionPlanMarkdown() string { return d.Path("execution-plan.md") }

// ExecutionPlanReview returns the path to execution-plan-review.json.
func (d *RunDir) ExecutionPlanReview() string { return d.Path("execution-plan-review.json") }

// ExecutionPlanApproval returns the path to execution-plan-approval.json.
func (d *RunDir) ExecutionPlanApproval() string { return d.Path("execution-plan-approval.json") }

// ClaimPath returns the path to a specific claim file.
func (d *RunDir) ClaimPath(taskID string) string {
	return filepath.Join(d.Root, "claims", taskID+".json")
}

// ReportDir returns the report directory for a specific task.
// When attemptID is provided, returns an attempt-scoped subdirectory to prevent
// retries from overwriting previous attempt artifacts.
func (d *RunDir) ReportDir(taskID string, attemptID ...string) string {
	base := filepath.Join(d.Root, "reports", taskID)
	if len(attemptID) > 0 && attemptID[0] != "" {
		return filepath.Join(base, attemptID[0])
	}
	return base
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

// WriteTechnicalSpec writes the technical spec markdown.
func (d *RunDir) WriteTechnicalSpec(contents []byte) error {
	return WriteAtomic(d.TechnicalSpec(), contents)
}

// ReadTechnicalSpec reads the technical spec markdown.
func (d *RunDir) ReadTechnicalSpec() ([]byte, error) {
	return os.ReadFile(d.TechnicalSpec())
}

// WriteTechnicalSpecReview writes the technical spec review atomically.
func (d *RunDir) WriteTechnicalSpecReview(review *TechnicalSpecReview) error {
	return WriteJSON(d.TechnicalSpecReview(), review)
}

// ReadTechnicalSpecReview reads the technical spec review.
func (d *RunDir) ReadTechnicalSpecReview() (*TechnicalSpecReview, error) {
	var review TechnicalSpecReview
	if err := ReadJSON(d.TechnicalSpecReview(), &review); err != nil {
		return nil, err
	}
	return &review, nil
}

// WriteTechnicalSpecApproval writes the technical spec approval atomically.
func (d *RunDir) WriteTechnicalSpecApproval(approval *ArtifactApproval) error {
	return WriteJSON(d.TechnicalSpecApproval(), approval)
}

// ReadTechnicalSpecApproval reads the technical spec approval.
func (d *RunDir) ReadTechnicalSpecApproval() (*ArtifactApproval, error) {
	var approval ArtifactApproval
	if err := ReadJSON(d.TechnicalSpecApproval(), &approval); err != nil {
		return nil, err
	}
	return &approval, nil
}

// WriteExecutionPlan writes the execution plan atomically.
func (d *RunDir) WriteExecutionPlan(plan *ExecutionPlan) error {
	return WriteJSON(d.ExecutionPlan(), plan)
}

// WriteExecutionPlanMarkdown writes the execution plan markdown.
func (d *RunDir) WriteExecutionPlanMarkdown(contents []byte) error {
	return WriteAtomic(d.ExecutionPlanMarkdown(), contents)
}

// ReadExecutionPlan reads the execution plan.
func (d *RunDir) ReadExecutionPlan() (*ExecutionPlan, error) {
	var plan ExecutionPlan
	if err := ReadJSON(d.ExecutionPlan(), &plan); err != nil {
		return nil, err
	}
	return &plan, nil
}

// ReadExecutionPlanMarkdown reads the execution plan markdown.
func (d *RunDir) ReadExecutionPlanMarkdown() ([]byte, error) {
	return os.ReadFile(d.ExecutionPlanMarkdown())
}

// WriteExecutionPlanReview writes the execution plan review atomically.
func (d *RunDir) WriteExecutionPlanReview(review *ExecutionPlanReview) error {
	return WriteJSON(d.ExecutionPlanReview(), review)
}

// ReadExecutionPlanReview reads the execution plan review.
func (d *RunDir) ReadExecutionPlanReview() (*ExecutionPlanReview, error) {
	var review ExecutionPlanReview
	if err := ReadJSON(d.ExecutionPlanReview(), &review); err != nil {
		return nil, err
	}
	return &review, nil
}

// WriteExecutionPlanApproval writes the execution plan approval atomically.
func (d *RunDir) WriteExecutionPlanApproval(approval *ArtifactApproval) error {
	return WriteJSON(d.ExecutionPlanApproval(), approval)
}

// ReadExecutionPlanApproval reads the execution plan approval.
func (d *RunDir) ReadExecutionPlanApproval() (*ArtifactApproval, error) {
	var approval ArtifactApproval
	if err := ReadJSON(d.ExecutionPlanApproval(), &approval); err != nil {
		return nil, err
	}
	return &approval, nil
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
