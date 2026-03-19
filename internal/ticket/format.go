// Package ticket implements a Ticket-format (wedow/ticket) task store.
// It reads and writes markdown files with YAML frontmatter compatible
// with the tk CLI, while extending the format with attest-specific fields.
package ticket

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/runger/attest/internal/state"
)

// Frontmatter holds all YAML fields (both tk-native and attest-custom).
type Frontmatter struct {
	// tk-native fields — understood and managed by tk CLI.
	ID          string   `yaml:"id"`
	Title       string   `yaml:"title,omitempty"`
	Status      string   `yaml:"status"`
	Deps        []string `yaml:"deps,omitempty"`
	Links       []string `yaml:"links,omitempty"`
	Created     string   `yaml:"created"`
	Type        string   `yaml:"type,omitempty"`
	Priority    int      `yaml:"priority"`
	Assignee    string   `yaml:"assignee,omitempty"`
	ExternalRef string   `yaml:"external-ref,omitempty"`
	Parent      string   `yaml:"parent,omitempty"`
	Tags        []string `yaml:"tags,omitempty"`

	// attest-specific fields — custom YAML keys, preserved by tk as pass-through.
	AttestStatus     string   `yaml:"attest_status,omitempty"`
	RequirementIDs   []string `yaml:"requirement_ids,omitempty"`
	RiskLevel        string   `yaml:"risk_level,omitempty"`
	Order            int      `yaml:"order"`
	ETag             string   `yaml:"etag,omitempty"`
	LineageID        string   `yaml:"lineage_id,omitempty"`
	DefaultModel     string   `yaml:"default_model,omitempty"`
	IsolationMode    string   `yaml:"isolation_mode,omitempty"`
	OwnedPaths       []string `yaml:"owned_paths,omitempty"`
	ReadOnlyPaths    []string `yaml:"read_only_paths,omitempty"`
	SharedPaths      []string `yaml:"shared_paths,omitempty"`
	StatusReason     string   `yaml:"status_reason,omitempty"`
	RequiredEvidence []string `yaml:"required_evidence,omitempty"`
	CreatedFrom      string   `yaml:"created_from,omitempty"`
	UpdatedAt        string   `yaml:"updated_at,omitempty"`

	// Catch-all for unknown fields — forward compatibility with future tk versions.
	Extra map[string]interface{} `yaml:",inline"`
}

// Status mapping: attest 9 states → tk 3 states.
var attestToTicketStatus = map[state.TaskStatus]string{
	state.TaskPending:       "open",
	state.TaskClaimed:       "in_progress",
	state.TaskImplementing:  "in_progress",
	state.TaskVerifying:     "in_progress",
	state.TaskUnderReview:   "in_progress",
	state.TaskRepairPending: "open",
	state.TaskBlocked:       "open",
	state.TaskDone:          "closed",
	state.TaskFailed:        "closed",
}

// StatusToTicket maps an attest TaskStatus to a tk-native status string.
func StatusToTicket(s state.TaskStatus) string {
	if tk, ok := attestToTicketStatus[s]; ok {
		return tk
	}
	return "open"
}

// StatusFromTicket maps tk-native status + attest_status back to TaskStatus.
// Prefers attest_status if present (set by Go); falls back to tk status (set by tk CLI).
func StatusFromTicket(tkStatus, attestStatus string) state.TaskStatus {
	if attestStatus != "" {
		return state.TaskStatus(attestStatus)
	}
	switch tkStatus {
	case "in_progress":
		return state.TaskClaimed
	case "closed":
		return state.TaskDone
	default:
		return state.TaskPending
	}
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		// Try ISO 8601 without timezone (tk sometimes omits 'Z').
		t, _ = time.Parse("2006-01-02T15:04:05", s)
	}
	return t
}

// TaskToFrontmatter converts a state.Task to YAML frontmatter.
func TaskToFrontmatter(task *state.Task) *Frontmatter {
	return &Frontmatter{
		ID:               task.TaskID,
		Title:            task.Title,
		Status:           StatusToTicket(task.Status),
		Deps:             task.DependsOn,
		Created:          formatTime(task.CreatedAt),
		Type:             task.TaskType,
		Priority:         task.Priority,
		Tags:             task.Tags,
		Parent:           task.ParentTaskID,
		AttestStatus:     string(task.Status),
		RequirementIDs:   task.RequirementIDs,
		RiskLevel:        task.RiskLevel,
		Order:            task.Order,
		ETag:             task.ETag,
		LineageID:        task.LineageID,
		DefaultModel:     task.DefaultModel,
		IsolationMode:    task.Scope.IsolationMode,
		OwnedPaths:       task.Scope.OwnedPaths,
		ReadOnlyPaths:    task.Scope.ReadOnlyPaths,
		SharedPaths:      task.Scope.SharedPaths,
		StatusReason:     task.StatusReason,
		RequiredEvidence: task.RequiredEvidence,
		CreatedFrom:      task.CreatedFrom,
		UpdatedAt:        formatTime(task.UpdatedAt),
	}
}

// FrontmatterToTask converts a Frontmatter + body to a state.Task.
func FrontmatterToTask(fm *Frontmatter) *state.Task {
	return &state.Task{
		TaskID:         fm.ID,
		Slug:           strings.ToLower(strings.ReplaceAll(fm.ID, "-", "_")),
		Title:          fm.Title,
		TaskType:       fm.Type,
		Tags:           fm.Tags,
		CreatedAt:      parseTime(fm.Created),
		UpdatedAt:      parseTime(fm.UpdatedAt),
		Order:          fm.Order,
		ETag:           fm.ETag,
		LineageID:      fm.LineageID,
		RequirementIDs: fm.RequirementIDs,
		DependsOn:      fm.Deps,
		Scope: state.TaskScope{
			OwnedPaths:    fm.OwnedPaths,
			ReadOnlyPaths: fm.ReadOnlyPaths,
			SharedPaths:   fm.SharedPaths,
			IsolationMode: fm.IsolationMode,
		},
		Priority:         fm.Priority,
		RiskLevel:        fm.RiskLevel,
		DefaultModel:     fm.DefaultModel,
		Status:           StatusFromTicket(fm.Status, fm.AttestStatus),
		StatusReason:     fm.StatusReason,
		RequiredEvidence: fm.RequiredEvidence,
		ParentTaskID:     fm.Parent,
		CreatedFrom:      fm.CreatedFrom,
	}
}

// MarshalTicket converts a state.Task to a full ticket markdown file.
func MarshalTicket(task *state.Task) ([]byte, error) {
	fm := TaskToFrontmatter(task)

	yamlData, err := yaml.Marshal(fm)
	if err != nil {
		return nil, fmt.Errorf("marshal frontmatter: %w", err)
	}

	var buf bytes.Buffer
	buf.WriteString("---\n")
	buf.Write(yamlData)
	buf.WriteString("---\n")
	buf.WriteString("# ")
	buf.WriteString(task.Title)
	buf.WriteString("\n")

	if len(task.Scope.OwnedPaths) > 0 {
		buf.WriteString("\n## Scope\n\n")
		buf.WriteString("Owned paths: ")
		buf.WriteString(strings.Join(task.Scope.OwnedPaths, ", "))
		buf.WriteString("\n")
		if len(task.Scope.ReadOnlyPaths) > 0 {
			buf.WriteString("Read-only paths: ")
			buf.WriteString(strings.Join(task.Scope.ReadOnlyPaths, ", "))
			buf.WriteString("\n")
		}
	}

	if len(task.RequiredEvidence) > 0 {
		buf.WriteString("\n## Acceptance Criteria\n\n")
		for _, e := range task.RequiredEvidence {
			buf.WriteString("- ")
			buf.WriteString(e)
			buf.WriteString("\n")
		}
	}

	return buf.Bytes(), nil
}

// UnmarshalTicket parses a ticket markdown file into a state.Task.
func UnmarshalTicket(data []byte) (*state.Task, error) {
	fm, _, err := splitFrontmatterBody(data)
	if err != nil {
		return nil, err
	}
	return FrontmatterToTask(fm), nil
}

// splitFrontmatterBody splits a ticket file into frontmatter and body.
func splitFrontmatterBody(data []byte) (*Frontmatter, string, error) {
	content := string(data)
	if !strings.HasPrefix(content, "---\n") {
		return nil, "", fmt.Errorf("%w: missing opening frontmatter delimiter", ErrCorruptYAML)
	}

	end := strings.Index(content[4:], "\n---\n")
	if end < 0 {
		// Try end-of-file case.
		end = strings.Index(content[4:], "\n---")
		if end < 0 {
			return nil, "", fmt.Errorf("%w: missing closing frontmatter delimiter", ErrCorruptYAML)
		}
	}

	yamlStr := content[4 : 4+end]
	body := ""
	bodyStart := 4 + end + 5 // past "\n---\n" (5 chars)
	if bodyStart < len(content) {
		body = content[bodyStart:]
	}

	var fm Frontmatter
	if err := yaml.Unmarshal([]byte(yamlStr), &fm); err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrCorruptYAML, err)
	}

	// Extract title from body if not in frontmatter.
	if fm.Title == "" && strings.HasPrefix(strings.TrimSpace(body), "# ") {
		lines := strings.SplitN(strings.TrimSpace(body), "\n", 2)
		fm.Title = strings.TrimPrefix(lines[0], "# ")
	}

	return &fm, body, nil
}

// UpdateFrontmatter replaces the frontmatter of a ticket file while preserving the body.
func UpdateFrontmatter(existingData []byte, task *state.Task) ([]byte, error) {
	_, body, err := splitFrontmatterBody(existingData)
	if err != nil {
		return nil, err
	}

	fm := TaskToFrontmatter(task)
	yamlData, err := yaml.Marshal(fm)
	if err != nil {
		return nil, fmt.Errorf("marshal frontmatter: %w", err)
	}

	var buf bytes.Buffer
	buf.WriteString("---\n")
	buf.Write(yamlData)
	buf.WriteString("---\n")
	buf.WriteString(body)

	return buf.Bytes(), nil
}
