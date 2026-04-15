// Package state implements file-based run state with atomic writes.
package state

import (
	"errors"
	"sort"
	"strings"
	"time"
)

// ErrPartialRead indicates some task files could not be read but partial
// results are available. Callers should use the returned tasks and handle
// the error as a warning, not a fatal failure.
var ErrPartialRead = errors.New("partial read: some task files were skipped")

// TaskStore abstracts task persistence. Implementations include
// RunDir (JSON, backward compat) and ticket.Store (Ticket format).
// Nothing in this package or internal/engine/ imports a specific backend.
type TaskStore interface {
	// Run-scoped operations (filter by parent epic).
	ReadTasks(runID string) ([]Task, error)
	WriteTasks(runID string, tasks []Task) error

	// Single-task operations.
	ReadTask(taskID string) (*Task, error)
	WriteTask(task *Task) error
	UpdateStatus(taskID string, status TaskStatus, reason string) error

	// Run lifecycle.
	CreateRun(runID string) error
}

// ClaimableStore extends TaskStore with exclusive claim operations for
// multi-agent dispatch. Implementations must provide crash-safe leases.
type ClaimableStore interface {
	TaskStore
	ClaimTask(taskID, ownerID, backend string, lease time.Duration) error
	ReleaseClaim(taskID, ownerID string, newStatus TaskStatus, reason string) error
	RenewClaim(taskID, ownerID string, lease time.Duration) error
}

// LearningCategory classifies learnings for enrichment mapping.
type LearningCategory string

// Learning categories used by enrichTaskWithLearnings.
const (
	LearningCategoryAntiPattern LearningCategory = "anti_pattern"
	LearningCategoryCodebase    LearningCategory = "codebase"
	LearningCategoryTooling     LearningCategory = "tooling"
)

// LearningQueryOpts is the subset of query options the engine needs.
type LearningQueryOpts struct {
	Tags             []string
	Paths            []string
	SearchText       string
	MinEffectiveness float64
	Limit            int
}

// LearningEnricher enriches tasks with learnings from a learning store.
// The engine type-asserts to this interface. Implemented by learning.Store.
type LearningEnricher interface {
	QueryLearnings(opts LearningQueryOpts) ([]LearningRef, error)
	RecordOutcome(ids []string, passed bool) error
}

// Boundaries defines human-input boundaries for a run (spec section 2.6).
type Boundaries struct {
	Always   []string `json:"always,omitempty"`
	AskFirst []string `json:"ask_first,omitempty"`
	Never    []string `json:"never,omitempty"`
}

// EmptyBoundaryItem preserves an explicitly blank boundary entry instead of silently dropping it.
// It is an internal marker, not a user-facing display value.
const EmptyBoundaryItem = "\x00fabrikk-boundary-empty"

const boundaryEscapePrefix = "\x00fabrikk-boundary-escaped:"

// IsEmptyBoundaryItem reports whether item is the internal marker for an
// explicitly blank boundary item.
func IsEmptyBoundaryItem(item string) bool {
	return item == EmptyBoundaryItem
}

// BoundaryItemValue decodes an escaped normalized boundary item back to the
// original user value. Empty boundary markers remain encoded so callers can
// handle them explicitly via IsEmptyBoundaryItem.
func BoundaryItemValue(item string) string {
	return strings.TrimPrefix(item, boundaryEscapePrefix)
}

// Normalized returns trimmed boundary items while preserving explicitly empty entries.
func (b Boundaries) Normalized() Boundaries {
	return Boundaries{
		Always:   normalizeBoundaryItems(b.Always),
		AskFirst: normalizeBoundaryItems(b.AskFirst),
		Never:    normalizeBoundaryItems(b.Never),
	}
}

func normalizeBoundaryItems(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	result := make([]string, 0, len(items))
	for _, item := range items {
		normalized := strings.TrimSpace(item)
		if normalized == "" {
			normalized = EmptyBoundaryItem
		} else if normalized == EmptyBoundaryItem || strings.HasPrefix(normalized, boundaryEscapePrefix) {
			normalized = boundaryEscapePrefix + normalized
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}
	return result
}

// RunArtifact is the approved normalized contract for a run (spec section 3.2).
type RunArtifact struct {
	SchemaVersion  string          `json:"schema_version"`
	RunID          string          `json:"run_id"`
	SourceSpecs    []SourceSpec    `json:"source_specs"`
	Requirements   []Requirement   `json:"requirements"`
	Assumptions    []string        `json:"assumptions"`
	Clarifications []Clarification `json:"clarifications"`
	Dependencies   []string        `json:"dependencies"`
	RiskProfile    string          `json:"risk_profile"`
	Boundaries     Boundaries      `json:"boundaries"`
	RoutingPolicy  RoutingPolicy   `json:"routing_policy"`
	QualityGate    *QualityGate    `json:"quality_gate,omitempty"`
	ApprovedAt     *time.Time      `json:"approved_at,omitempty"`
	ApprovedBy     string          `json:"approved_by,omitempty"`
	ArtifactHash   string          `json:"artifact_hash,omitempty"`
}

// ArtifactStatus tracks the lifecycle of a generated planning artifact.
type ArtifactStatus string

// Artifact lifecycle states for generated planning artifacts.
const (
	ArtifactDrafted     ArtifactStatus = "drafted"
	ArtifactUnderReview ArtifactStatus = "under_review"
	ArtifactApproved    ArtifactStatus = "approved"
	ArtifactRejected    ArtifactStatus = "rejected"
)

// ReviewStatus captures the outcome of a review checkpoint.
type ReviewStatus string

// Review verdict states for deterministic planning checkpoints.
const (
	ReviewPass          ReviewStatus = "pass"
	ReviewFail          ReviewStatus = "fail"
	ReviewNeedsRevision ReviewStatus = "needs_revision"
)

// SourceSpec identifies an input spec file.
type SourceSpec struct {
	Path        string `json:"path"`
	Fingerprint string `json:"fingerprint"`
}

// Requirement is a single tracked requirement from the source specs.
type Requirement struct {
	ID         string `json:"id"`
	Text       string `json:"text"`
	SourceSpec string `json:"source_spec"`
	SourceLine int    `json:"source_line,omitempty"`
}

// Clarification records an unresolved or resolved question (spec section 3.3).
type Clarification struct {
	QuestionID             string     `json:"question_id"`
	Text                   string     `json:"text"`
	Status                 string     `json:"status"` // pending, resolved
	Answer                 string     `json:"answer,omitempty"`
	AnsweredBy             string     `json:"answered_by,omitempty"`
	AnsweredAt             *time.Time `json:"answered_at,omitempty"`
	AffectedRequirementIDs []string   `json:"affected_requirement_ids,omitempty"`
}

// RoutingPolicy defines model routing hints for the run.
type RoutingPolicy struct {
	DefaultImplementer string `json:"default_implementer"`
	DefaultReviewer    string `json:"default_reviewer,omitempty"`
}

// QualityGate defines the project-level quality checks (spec section 11.2).
type QualityGate struct {
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	Required       bool   `json:"required"`
}

// StructuralAnalysis holds deterministic code-intelligence output for the worktree.
// Nil when code_intel.py is unavailable.
type StructuralAnalysis struct {
	Nodes []StructuralNode `json:"nodes"`
	Edges []StructuralEdge `json:"edges"`
}

// StructuralNode identifies a code symbol discovered by structural analysis.
type StructuralNode struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Exported bool   `json:"exported"`
}

// StructuralEdge relates two structural nodes or files.
type StructuralEdge struct {
	From  string  `json:"from"`
	To    string  `json:"to"`
	Type  string  `json:"type"`
	Score float64 `json:"score,omitempty"`
}

// ExplorationResult holds the LLM's codebase-to-requirements mapping output.
type ExplorationResult struct {
	FileInventory []FileInfo     `json:"file_inventory"`
	Symbols       []SymbolInfo   `json:"symbols"`
	TestFiles     []TestFileInfo `json:"test_files"`
	ReusePoints   []ReusePoint   `json:"reuse_points"`
}

// FileInfo describes a file candidate surfaced during exploration.
type FileInfo struct {
	Path      string `json:"path"`
	Exists    bool   `json:"exists"`
	IsNew     bool   `json:"is_new"`
	LineCount int    `json:"line_count"`
	Language  string `json:"language"`
}

// SymbolInfo identifies a concrete symbol relevant to a requirement.
type SymbolInfo struct {
	FilePath  string `json:"file_path"`
	Line      int    `json:"line"`
	Kind      string `json:"kind"`
	Signature string `json:"signature"`
}

// TestFileInfo describes an existing or planned test file.
type TestFileInfo struct {
	Path      string   `json:"path"`
	TestNames []string `json:"test_names"`
	Covers    string   `json:"covers"`
}

// ReusePoint points to existing code the plan should build on.
type ReusePoint struct {
	FilePath  string `json:"file_path"`
	Line      int    `json:"line"`
	Symbol    string `json:"symbol"`
	Relevance string `json:"relevance"`
}

// ImplementationDetail captures concrete implementation guidance surfaced during planning.
type ImplementationDetail struct {
	FilesToModify []FileChange `json:"files_to_modify,omitempty" yaml:"files_to_modify,omitempty"`
	SymbolsToAdd  []string     `json:"symbols_to_add,omitempty" yaml:"symbols_to_add,omitempty"`
	SymbolsToUse  []string     `json:"symbols_to_use,omitempty" yaml:"symbols_to_use,omitempty"`
	TestsToAdd    []string     `json:"tests_to_add,omitempty" yaml:"tests_to_add,omitempty"`
}

// IsZero reports whether the detail contains any implementation guidance.
func (d ImplementationDetail) IsZero() bool {
	return len(d.FilesToModify) == 0 && len(d.SymbolsToAdd) == 0 && len(d.SymbolsToUse) == 0 && len(d.TestsToAdd) == 0
}

// FileChange describes one planned file-level modification.
type FileChange struct {
	Path   string `json:"path" yaml:"path"`
	Change string `json:"change" yaml:"change"`
	IsNew  bool   `json:"is_new,omitempty" yaml:"is_new,omitempty"`
}

// Task is a single work item in the task graph (spec section 3.4).
type Task struct {
	TaskID               string               `json:"task_id"`
	Slug                 string               `json:"slug"`
	Title                string               `json:"title"`
	TaskType             string               `json:"task_type"` // implementation, repair, review_followup, clarification_followup
	Tags                 []string             `json:"tags"`
	CreatedAt            time.Time            `json:"created_at"`
	UpdatedAt            time.Time            `json:"updated_at"`
	Order                int                  `json:"order"`
	ETag                 string               `json:"etag"`
	LineageID            string               `json:"lineage_id"`
	RequirementIDs       []string             `json:"requirement_ids"`
	DependsOn            []string             `json:"depends_on"`
	Scope                TaskScope            `json:"scope"`
	FilesLikelyTouched   []string             `json:"files_likely_touched,omitempty"`
	Priority             int                  `json:"priority"`
	RiskLevel            string               `json:"risk_level"` // low, medium, high
	DefaultModel         string               `json:"default_model"`
	Status               TaskStatus           `json:"status"`
	StatusReason         string               `json:"status_reason,omitempty"`
	RequiredEvidence     []string             `json:"required_evidence"`
	ValidationChecks     []ValidationCheck    `json:"validation_checks,omitempty"`
	ImplementationDetail ImplementationDetail `json:"implementation_detail,omitzero"`
	ParentTaskID         string               `json:"parent_task_id,omitempty"`
	CreatedFrom          string               `json:"created_from,omitempty"`

	// Grouping metadata — set when same-symbol exception groups >1 requirement (spec §2.1).
	GroupingReason        string   `json:"grouping_reason,omitempty"`
	GroupedRequirementIDs []string `json:"grouped_requirement_ids,omitempty"`

	// Learning context — injected by engine post-compilation.
	Intent          string        `json:"intent,omitempty"`
	Constraints     []string      `json:"constraints,omitempty"`
	Warnings        []string      `json:"warnings,omitempty"`
	LearningIDs     []string      `json:"learning_ids,omitempty"`
	LearningContext []LearningRef `json:"learning_context,omitempty"`
}

// DeriveTags produces query tags from task metadata for learning lookup.
// Tags are derived from owned paths, requirement IDs, and task type.
func (t *Task) DeriveTags() []string {
	seen := make(map[string]struct{})
	for _, p := range t.Scope.OwnedPaths {
		for _, part := range strings.Split(p, "/") {
			part = strings.ToLower(part)
			if part != "" && part != "." && part != ".." {
				seen[part] = struct{}{}
			}
		}
	}
	prefixMap := map[string]string{
		"AT-FR": "functional", "AT-TS": "testing",
		"AT-NFR": "non-functional", "AT-AS": "assumption",
	}
	for _, reqID := range t.RequirementIDs {
		for prefix, tag := range prefixMap {
			if strings.HasPrefix(reqID, prefix) {
				seen[tag] = struct{}{}
				break
			}
		}
	}
	if t.TaskType != "" {
		seen[strings.ToLower(t.TaskType)] = struct{}{}
	}
	tags := make([]string, 0, len(seen))
	for tag := range seen {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	return tags
}

// LearningRef is a lightweight reference to a learning for ticket body rendering.
type LearningRef struct {
	ID       string `json:"id"`
	Category string `json:"category"`
	Summary  string `json:"summary"`
}

// ValidationCheck defines a single validation check for a task (spec §2.4).
type ValidationCheck struct {
	Type    string   `json:"type" yaml:"type"`                       // e.g. "command", "file_exists", "grep"
	Paths   []string `json:"paths,omitempty" yaml:"paths,omitempty"` // file paths to check
	File    string   `json:"file,omitempty" yaml:"file,omitempty"`   // single file target
	Pattern string   `json:"pattern,omitempty" yaml:"pattern,omitempty"`
	Tool    string   `json:"tool,omitempty" yaml:"tool,omitempty"` // tool to run (e.g. "go test")
	Args    []string `json:"args,omitempty" yaml:"args,omitempty"` // arguments for the tool
}

// TaskScope defines the file-level boundaries for a task (spec section 3.4).
type TaskScope struct {
	OwnedPaths    []string `json:"owned_paths"`
	ReadOnlyPaths []string `json:"read_only_paths,omitempty"`
	SharedPaths   []string `json:"shared_paths,omitempty"`
	IsolationMode string   `json:"isolation_mode"` // direct, patch_handoff
}

// TaskStatus represents the current state of a task (spec section 6.3).
type TaskStatus string

// Task lifecycle states (spec section 6.3).
const (
	TaskPending       TaskStatus = "pending"
	TaskClaimed       TaskStatus = "claimed"
	TaskImplementing  TaskStatus = "implementing"
	TaskVerifying     TaskStatus = "verifying"
	TaskUnderReview   TaskStatus = "under_review"
	TaskRepairPending TaskStatus = "repair_pending"
	TaskBlocked       TaskStatus = "blocked"
	TaskDone          TaskStatus = "done"
	TaskFailed        TaskStatus = "failed"
)

// Claim represents an exclusive worker lease on a task (spec section 3.6).
type Claim struct {
	TaskID            string    `json:"task_id"`
	WaveID            string    `json:"wave_id"`
	OwnerID           string    `json:"owner_id"`
	Backend           string    `json:"backend"`
	ClaimedAt         time.Time `json:"claimed_at"`
	LeaseExpiresAt    time.Time `json:"lease_expires_at"`
	HeartbeatAt       time.Time `json:"heartbeat_at"`
	ScopeReservations []string  `json:"scope_reservations"`
}

// RequirementCoverage tracks coverage for one requirement (spec section 3.5).
type RequirementCoverage struct {
	RequirementID   string     `json:"requirement_id"`
	Status          string     `json:"status"` // unassigned, in_progress, satisfied, deferred, blocked
	CoveringTaskIDs []string   `json:"covering_task_ids"`
	Deferred        bool       `json:"deferred"`
	DeferredReason  string     `json:"deferred_reason,omitempty"`
	DeferredBy      string     `json:"deferred_by,omitempty"`
	DeferredAt      *time.Time `json:"deferred_at,omitempty"`
}

// RunStatus is the current run state summary (spec section 13.1).
type RunStatus struct {
	RunID                     string         `json:"run_id"`
	State                     RunState       `json:"state"`
	ActiveWaveID              string         `json:"active_wave_id,omitempty"`
	CurrentGate               string         `json:"current_gate,omitempty"`
	ActiveBackend             string         `json:"active_backend,omitempty"`
	ActiveModel               string         `json:"active_model,omitempty"`
	TaskCountsByState         map[string]int `json:"task_counts_by_state"`
	ActiveClaims              int            `json:"active_claims"`
	UncoveredRequirementCount int            `json:"uncovered_requirement_count"`
	OpenBlockers              []string       `json:"open_blockers,omitempty"`
	RetryCount                int            `json:"retry_count"`
	NextAutomaticAction       string         `json:"next_automatic_action,omitempty"`
	LastTransitionTime        time.Time      `json:"last_transition_time"`
	LastSuccessfulCouncilTime *time.Time     `json:"last_successful_council_time,omitempty"`
	TaskDetails               []TaskDetail   `json:"task_details,omitempty"`
}

// RunState represents the current state of a run (spec section 6.1).
type RunState string

// Run lifecycle states (spec section 6.1).
const (
	RunPreparing             RunState = "preparing"
	RunAwaitingClarification RunState = "awaiting_clarification"
	RunAwaitingApproval      RunState = "awaiting_approval"
	RunApproved              RunState = "approved"
	RunLaunching             RunState = "launching"
	RunRunning               RunState = "running"
	RunBlocked               RunState = "blocked"
	RunFailed                RunState = "failed"
	RunCompleted             RunState = "completed"
	RunStopped               RunState = "stopped"
)

// CompletionReport is the worker's output report (spec section 3.7).
type CompletionReport struct {
	TaskID             string          `json:"task_id"`
	AttemptID          string          `json:"attempt_id"`
	ChangedFiles       []string        `json:"changed_files"`
	CommandResults     []CommandResult `json:"command_results"`
	ArtifactsProduced  []string        `json:"artifacts_produced,omitempty"`
	KnownGaps          []string        `json:"known_gaps,omitempty"`
	ImplementerSummary string          `json:"implementer_summary,omitempty"`
}

// CommandResult records the outcome of a command execution (spec section 3.7).
type CommandResult struct {
	CommandID  string `json:"command_id"`
	Command    string `json:"command"`
	Cwd        string `json:"cwd"`
	ExitCode   int    `json:"exit_code"`
	DurationMs int64  `json:"duration_ms"`
	Required   bool   `json:"required"`
	LogPath    string `json:"log_path,omitempty"`
	LogHash    string `json:"log_hash,omitempty"`
}

// VerifierResult is the deterministic verifier output (spec section 3.7).
type VerifierResult struct {
	TaskID              string    `json:"task_id"`
	AttemptID           string    `json:"attempt_id"`
	EvidenceChecks      []Check   `json:"evidence_checks"`
	ScopeCheck          Check     `json:"scope_check"`
	RequirementCheck    Check     `json:"requirement_check"`
	Pass                bool      `json:"pass"`
	BlockingFindings    []Finding `json:"blocking_findings,omitempty"`
	NonBlockingFindings []Finding `json:"non_blocking_findings,omitempty"`
}

// Check is a single verification check result.
type Check struct {
	Name   string `json:"name"`
	Pass   bool   `json:"pass"`
	Detail string `json:"detail,omitempty"`
}

// Finding is a normalized finding object (spec section 3.7).
type Finding struct {
	FindingID   string `json:"finding_id"`
	Severity    string `json:"severity"` // critical, high, medium, low
	Category    string `json:"category"`
	Summary     string `json:"summary"`
	EvidenceRef string `json:"evidence_ref,omitempty"`
	DedupeKey   string `json:"dedupe_key"`
}

// Attempt records metadata for a single worker attempt (spec section 3.7).
type Attempt struct {
	TaskID              string    `json:"task_id"`
	AttemptID           string    `json:"attempt_id"`
	WaveID              string    `json:"wave_id"`
	SelectedBackend     string    `json:"selected_backend"`
	SelectedModelTier   string    `json:"selected_model_tier"`
	BackendCLIVersion   string    `json:"backend_cli_version,omitempty"`
	BackendModelVersion string    `json:"backend_model_version,omitempty"`
	PromptTemplateID    string    `json:"prompt_template_id,omitempty"`
	InputBundleHash     string    `json:"input_bundle_hash,omitempty"`
	WorkerInputContract string    `json:"worker_input_contract,omitempty"`
	StartedAt           time.Time `json:"started_at"`
	FinishedAt          time.Time `json:"finished_at,omitempty"`
	ExitStatus          string    `json:"exit_status,omitempty"`
	EscalationCause     string    `json:"escalation_cause,omitempty"`
	RetryCount          int       `json:"retry_count"`
}

// ReviewResult is the output from a Codex or Gemini review (spec section 3.7).
type ReviewResult struct {
	TaskID              string    `json:"task_id"`
	AttemptID           string    `json:"attempt_id"`
	Verdict             string    `json:"verdict"` // pass, pass_with_findings, fail
	Confidence          float64   `json:"confidence"`
	BlockingFindings    []Finding `json:"blocking_findings,omitempty"`
	NonBlockingFindings []Finding `json:"non_blocking_findings,omitempty"`
	FindingIDs          []string  `json:"finding_ids,omitempty"`
}

// CouncilResult is the Opus synthesis output (spec section 3.7).
type CouncilResult struct {
	TaskID              string    `json:"task_id"`
	AttemptID           string    `json:"attempt_id"`
	Verdict             string    `json:"verdict"` // pass, pass_with_findings, fail
	SynthesizedFindings []Finding `json:"synthesized_findings,omitempty"`
	DismissalRationale  string    `json:"dismissal_rationale,omitempty"`
	FollowUpAction      string    `json:"follow_up_action"` // none, reopen, create_child_repair, block, escalate
}

// ReviewSummary records one reviewer verdict inside a planning review.
type ReviewSummary struct {
	ReviewerID string `json:"reviewer_id"`
	Pass       bool   `json:"pass"`
	Summary    string `json:"summary"`
}

// ReviewWarning records a non-blocking issue in a planning review.
type ReviewWarning struct {
	WarningID string `json:"warning_id"`
	SliceID   string `json:"slice_id,omitempty"`
	Summary   string `json:"summary"`
}

// ReviewFinding records a blocking issue in a planning review.
type ReviewFinding struct {
	FindingID       string   `json:"finding_id"`
	Severity        string   `json:"severity"`
	Category        string   `json:"category"`
	SliceID         string   `json:"slice_id,omitempty"`
	Summary         string   `json:"summary"`
	RequirementIDs  []string `json:"requirement_ids,omitempty"`
	SuggestedRepair string   `json:"suggested_repair,omitempty"`
}

// TechnicalSpecReview is the council-style review result for a technical spec draft.
type TechnicalSpecReview struct {
	SchemaVersion     string          `json:"schema_version"`
	RunID             string          `json:"run_id"`
	ArtifactType      string          `json:"artifact_type"`
	TechnicalSpecHash string          `json:"technical_spec_hash"`
	Status            ReviewStatus    `json:"status"`
	Summary           string          `json:"summary"`
	BlockingFindings  []ReviewFinding `json:"blocking_findings,omitempty"`
	Warnings          []ReviewWarning `json:"warnings,omitempty"`
	Reviewers         []ReviewSummary `json:"reviewers,omitempty"`
	ReviewedAt        time.Time       `json:"reviewed_at"`
}

// Wave is a conflict-free execution batch produced by the scheduler. The initial
// grouping is by dependency depth, but compiler.ComputeWaves shifts tasks whose
// file scopes collide into later waves, so the Tasks in a Wave are guaranteed to
// be safe to dispatch in parallel.
type Wave struct {
	WaveID string   `json:"wave_id"`
	Tasks  []string `json:"tasks"`
}

// FileConflict records two tasks that cannot share a wave because their file scopes overlap.
type FileConflict struct {
	FilePath string `json:"file_path"`
	TaskA    string `json:"task_a"`
	TaskB    string `json:"task_b"`
}

// SharedFileEntry records a file overlap that is allowed only because the slices run in different waves.
type SharedFileEntry struct {
	File       string   `json:"file"`
	Wave1Tasks []string `json:"wave1_tasks,omitempty"`
	Wave2Tasks []string `json:"wave2_tasks,omitempty"`
	Mitigation string   `json:"mitigation,omitempty"`
}

// ExecutionSlice is one implementation slice in an approved execution plan.
type ExecutionSlice struct {
	SliceID              string               `json:"slice_id"`
	Title                string               `json:"title"`
	Goal                 string               `json:"goal"`
	RequirementIDs       []string             `json:"requirement_ids"`
	DependsOn            []string             `json:"depends_on,omitempty"`
	WaveID               string               `json:"wave_id,omitempty"`
	FilesLikelyTouched   []string             `json:"files_likely_touched,omitempty"`
	OwnedPaths           []string             `json:"owned_paths,omitempty"`
	AcceptanceChecks     []string             `json:"acceptance_checks,omitempty"`
	ValidationChecks     []ValidationCheck    `json:"validation_checks,omitempty"`
	ImplementationDetail ImplementationDetail `json:"implementation_detail,omitzero"`
	Risk                 string               `json:"risk"`
	Size                 string               `json:"size"`
	Notes                string               `json:"notes,omitempty"`
}

// ExecutionPlan is the run-scoped implementation decomposition artifact.
type ExecutionPlan struct {
	SchemaVersion           string           `json:"schema_version"`
	RunID                   string           `json:"run_id"`
	ArtifactType            string           `json:"artifact_type"`
	SourceTechnicalSpecHash string           `json:"source_technical_spec_hash"`
	Status                  ArtifactStatus   `json:"status"`
	Slices                  []ExecutionSlice `json:"slices"`
	GeneratedAt             time.Time        `json:"generated_at"`
}

// ExecutionPlanReview is the council-style review result for an execution plan.
type ExecutionPlanReview struct {
	SchemaVersion      string            `json:"schema_version"`
	RunID              string            `json:"run_id"`
	ArtifactType       string            `json:"artifact_type"`
	ExecutionPlanHash  string            `json:"execution_plan_hash"`
	Status             ReviewStatus      `json:"status"`
	Summary            string            `json:"summary"`
	BlockingFindings   []ReviewFinding   `json:"blocking_findings,omitempty"`
	Warnings           []ReviewWarning   `json:"warnings,omitempty"`
	SharedFileRegistry []SharedFileEntry `json:"shared_file_registry,omitempty"`
	Reviewers          []ReviewSummary   `json:"reviewers,omitempty"`
	ReviewedAt         time.Time         `json:"reviewed_at"`
}

// ArtifactApproval records explicit approval of a generated planning artifact.
type ArtifactApproval struct {
	SchemaVersion string         `json:"schema_version"`
	RunID         string         `json:"run_id"`
	ArtifactType  string         `json:"artifact_type"`
	ArtifactPath  string         `json:"artifact_path"`
	ArtifactHash  string         `json:"artifact_hash"`
	Status        ArtifactStatus `json:"status"`
	ApprovedBy    string         `json:"approved_by"`
	ApprovedAt    time.Time      `json:"approved_at"`
	Reason        string         `json:"reason,omitempty"`
}

// EngineInfo records the engine process metadata (spec section 2.4).
type EngineInfo struct {
	RunID       string    `json:"run_id"`
	PID         int       `json:"pid"`
	StartedAt   time.Time `json:"started_at"`
	HeartbeatAt time.Time `json:"heartbeat_at"`
	Version     string    `json:"version"`
	State       string    `json:"state"`
}

// TaskDetail is a per-task operational record within run-status.json (spec section 13.1).
type TaskDetail struct {
	TaskID                 string `json:"task_id"`
	CurrentGate            string `json:"current_gate,omitempty"`
	ActiveBackend          string `json:"active_backend,omitempty"`
	ActiveModel            string `json:"active_model,omitempty"`
	ClaimAgeSeconds        int    `json:"claim_age_seconds,omitempty"`
	HeartbeatAgeSeconds    int    `json:"heartbeat_age_seconds,omitempty"`
	RetryCount             int    `json:"retry_count"`
	NextAutomaticAction    string `json:"next_automatic_action,omitempty"`
	BlockingFindingID      string `json:"blocking_finding_id,omitempty"`
	BlockingFindingSummary string `json:"blocking_finding_summary,omitempty"`
	HumanInputRequired     bool   `json:"human_input_required"`
}

// Event is a single entry in the run event stream (spec section 13.3).
type Event struct {
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"`
	RunID     string    `json:"run_id"`
	TaskID    string    `json:"task_id,omitempty"`
	Detail    string    `json:"detail"`
}
