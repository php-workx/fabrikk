// Package state implements file-based run state with atomic writes.
package state

import "time"

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
	RoutingPolicy  RoutingPolicy   `json:"routing_policy"`
	QualityGate    *QualityGate    `json:"quality_gate,omitempty"`
	ApprovedAt     *time.Time      `json:"approved_at,omitempty"`
	ApprovedBy     string          `json:"approved_by,omitempty"`
	ArtifactHash   string          `json:"artifact_hash,omitempty"`
}

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

// Task is a single work item in the task graph (spec section 3.4).
type Task struct {
	TaskID           string     `json:"task_id"`
	Slug             string     `json:"slug"`
	Title            string     `json:"title"`
	TaskType         string     `json:"task_type"` // implementation, repair, review_followup, clarification_followup
	Tags             []string   `json:"tags"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	Order            int        `json:"order"`
	ETag             string     `json:"etag"`
	LineageID        string     `json:"lineage_id"`
	RequirementIDs   []string   `json:"requirement_ids"`
	DependsOn        []string   `json:"depends_on"`
	Scope            TaskScope  `json:"scope"`
	Priority         int        `json:"priority"`
	RiskLevel        string     `json:"risk_level"` // low, medium, high
	DefaultModel     string     `json:"default_model"`
	Status           TaskStatus `json:"status"`
	StatusReason     string     `json:"status_reason,omitempty"`
	RequiredEvidence []string   `json:"required_evidence"`
	ParentTaskID     string     `json:"parent_task_id,omitempty"`
	CreatedFrom      string     `json:"created_from,omitempty"`
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
	TaskID           string    `json:"task_id"`
	AttemptID        string    `json:"attempt_id"`
	EvidenceChecks   []Check   `json:"evidence_checks"`
	ScopeCheck       Check     `json:"scope_check"`
	RequirementCheck Check     `json:"requirement_check"`
	Pass             bool      `json:"pass"`
	BlockingFindings []Finding `json:"blocking_findings,omitempty"`
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
