// Package learning provides a project-scoped learning store for cross-session
// knowledge persistence. Learnings are tagged entries stored in JSONL format
// with outcome-based effectiveness scoring.
package learning

import "time"

// Category classifies the kind of learning.
type Category string

// Learning categories.
const (
	CategoryPattern     Category = "pattern"      // Reusable approach that worked
	CategoryAntiPattern Category = "anti_pattern" // Approach that failed
	CategoryTooling     Category = "tooling"      // Build/lint/test insight
	CategoryCodebase    Category = "codebase"     // Project-specific knowledge
	CategoryProcess     Category = "process"      // Workflow/dispatch insight
)

// Learning is a single entry in the learning store.
type Learning struct {
	ID             string     `json:"id"`
	CreatedAt      time.Time  `json:"created_at"`
	Tags           []string   `json:"tags"`
	Category       Category   `json:"category"`
	Content        string     `json:"content"`
	Summary        string     `json:"summary"`
	SourceTask     string     `json:"source_task,omitempty"`
	SourceRun      string     `json:"source_run,omitempty"`
	SourcePaths    []string   `json:"source_paths,omitempty"`
	Confidence     float64    `json:"confidence"`
	SupersededBy   string     `json:"superseded_by,omitempty"`
	Expired        bool       `json:"expired"`
	Source         string     `json:"source,omitempty"`         // "manual", "council", "verifier", "rejection"
	SourceFinding  string     `json:"source_finding,omitempty"` // finding ID if extracted
	AttachCount    int        `json:"attach_count"`             // times attached to a task
	SuccessCount   int        `json:"success_count"`            // times the attached task passed verification
	LastAttachedAt *time.Time `json:"last_attached_at,omitempty"`
}

// Effectiveness returns the outcome-based quality score.
// When no outcome data exists (AttachCount == 0), falls back to Confidence.
func (l *Learning) Effectiveness() float64 {
	if l.AttachCount == 0 {
		return l.Confidence
	}
	return float64(l.SuccessCount) / float64(l.AttachCount)
}

// SessionHandoff captures session continuity state.
type SessionHandoff struct {
	ID            string    `json:"id"`
	CreatedAt     time.Time `json:"created_at"`
	RunID         string    `json:"run_id,omitempty"`
	TaskID        string    `json:"task_id,omitempty"`
	Summary       string    `json:"summary"`
	NextSteps     []string  `json:"next_steps,omitempty"`
	OpenQuestions []string  `json:"open_questions,omitempty"`
	LearningIDs   []string  `json:"learning_ids,omitempty"`
}

// QueryOpts controls learning retrieval filtering.
type QueryOpts struct {
	Tags             []string // match any tag (union with Paths)
	Category         Category // exact category match (empty = any)
	Paths            []string // match learnings with overlapping SourcePaths (union with Tags)
	MinEffectiveness float64  // effectiveness threshold (default 0.0)
	Limit            int      // max results (0 = unlimited)
	SortBy           string   // "effectiveness" (default), "created_at"
}

// TagIndex is the inverted tag index for fast lookups.
type TagIndex struct {
	Tags    map[string][]string `json:"tags"`
	Version int                 `json:"version"`
}

// ContextBundle is the assembled agent context for a task.
type ContextBundle struct {
	TaskID      string          `json:"task_id"`
	Learnings   []Learning      `json:"learnings"`
	Handoff     *SessionHandoff `json:"handoff,omitempty"`
	TokensUsed  int             `json:"tokens_used"`
	TokenBudget int             `json:"token_budget"`
}
