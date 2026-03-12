package compiler

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/runger/attest/internal/state"
)

// CompileResult holds the output of task compilation.
type CompileResult struct {
	Tasks    []state.Task
	Coverage []state.RequirementCoverage
}

// Compile produces a deterministic task graph from an approved run artifact (spec section 7.1).
//
// v1 compilation is rule-based and non-LLM. Each requirement becomes one task.
// Task IDs are derived deterministically from requirement IDs and compiler version.
func Compile(artifact *state.RunArtifact) (*CompileResult, error) {
	if artifact == nil {
		return nil, fmt.Errorf("nil run artifact")
	}
	if len(artifact.Requirements) == 0 {
		return nil, fmt.Errorf("run artifact has no requirements")
	}

	// Sort requirements for deterministic output (spec section 7.1).
	reqs := make([]state.Requirement, len(artifact.Requirements))
	copy(reqs, artifact.Requirements)
	sort.Slice(reqs, func(i, j int) bool {
		return reqs[i].ID < reqs[j].ID
	})

	tasks := make([]state.Task, 0, len(reqs))
	coverageMap := make(map[string]*state.RequirementCoverage)

	// Use a fixed compilation time for determinism within one Compile call.
	now := time.Now().Truncate(time.Second)

	for i, req := range reqs {
		taskID := taskIDFromRequirement(req.ID)
		title := fmt.Sprintf("Implement %s", req.ID)

		task := state.Task{
			TaskID:         taskID,
			Slug:           slugFromID(req.ID),
			Title:          title,
			TaskType:       "implementation",
			Tags:           tagsFromRequirement(req),
			CreatedAt:      now,
			UpdatedAt:      now,
			Order:          i + 1,
			LineageID:      taskID,
			RequirementIDs: []string{req.ID},
			DependsOn:      []string{},
			Scope: state.TaskScope{
				OwnedPaths:    []string{},
				IsolationMode: "direct",
			},
			Priority:         i + 1,
			RiskLevel:        classifyRisk(req),
			DefaultModel:     defaultModel(req),
			Status:           state.TaskPending,
			RequiredEvidence: inferEvidence(req),
		}
		task.ETag = computeETag(&task)
		tasks = append(tasks, task)

		if _, exists := coverageMap[req.ID]; !exists {
			coverageMap[req.ID] = &state.RequirementCoverage{
				RequirementID:   req.ID,
				Status:          "in_progress",
				CoveringTaskIDs: []string{},
			}
		}
		coverageMap[req.ID].CoveringTaskIDs = append(coverageMap[req.ID].CoveringTaskIDs, taskID)
	}

	// Build sorted coverage list.
	coverage := make([]state.RequirementCoverage, 0, len(coverageMap))
	for _, c := range coverageMap {
		coverage = append(coverage, *c)
	}
	sort.Slice(coverage, func(i, j int) bool {
		return coverage[i].RequirementID < coverage[j].RequirementID
	})

	return &CompileResult{
		Tasks:    tasks,
		Coverage: coverage,
	}, nil
}

// CheckCoverage verifies that every requirement is assigned to at least one task (spec section 7.2).
// Returns the IDs of any unassigned requirements.
func CheckCoverage(artifact *state.RunArtifact, coverage []state.RequirementCoverage) []string {
	covered := make(map[string]bool)
	for _, c := range coverage {
		if len(c.CoveringTaskIDs) > 0 || c.Deferred {
			covered[c.RequirementID] = true
		}
	}

	var unassigned []string
	for _, req := range artifact.Requirements {
		if !covered[req.ID] {
			unassigned = append(unassigned, req.ID)
		}
	}
	sort.Strings(unassigned)
	return unassigned
}

// taskIDFromRequirement produces a stable task ID from a requirement ID.
func taskIDFromRequirement(reqID string) string {
	return "task-" + strings.ToLower(reqID)
}

func classifyRisk(req state.Requirement) string {
	text := strings.ToLower(req.Text)
	switch {
	case strings.Contains(text, "must not") || strings.Contains(text, "security"):
		return "high"
	case strings.Contains(text, "must"):
		return "medium"
	default:
		return "low"
	}
}

func defaultModel(_ state.Requirement) string {
	// v1 routes all implementation tasks to Sonnet (spec section 10.1).
	// Routing differentiation is a later-phase concern.
	return "sonnet"
}

// slugFromID produces a short human-readable slug from a requirement ID (spec section 3.4).
func slugFromID(reqID string) string {
	return strings.ToLower(strings.ReplaceAll(reqID, "-", "_"))
}

// tagsFromRequirement derives normalized lowercase tags from a requirement (spec section 3.4).
func tagsFromRequirement(req state.Requirement) []string {
	var tags []string
	prefix := strings.SplitN(req.ID, "-", 3)
	if len(prefix) >= 2 {
		tags = append(tags, strings.ToLower(prefix[0]+"-"+prefix[1]))
	}
	risk := classifyRisk(req)
	if risk == "high" {
		tags = append(tags, "high-risk")
	}
	return tags
}

// computeETag produces a content hash for compare-and-swap consistency (spec section 3.4).
func computeETag(task *state.Task) string {
	// Zero out volatile fields before hashing.
	task.ETag = ""
	task.UpdatedAt = time.Time{}
	data, err := json.Marshal(task)
	if err != nil {
		return ""
	}
	return state.SHA256Bytes(data)[:16]
}

func inferEvidence(req state.Requirement) []string {
	text := strings.ToLower(req.Text)
	var evidence []string

	if strings.Contains(text, "test") {
		evidence = append(evidence, "test_pass")
	}
	if strings.Contains(text, "file") || strings.Contains(text, "artifact") {
		evidence = append(evidence, "file_exists")
	}

	if len(evidence) == 0 {
		evidence = append(evidence, "quality_gate_pass")
	}
	return evidence
}
