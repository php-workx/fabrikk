package compiler

import (
	"encoding/json"
	"fmt"
	"path/filepath"
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

const (
	riskLow    = "low"
	riskMedium = "medium"
	riskHigh   = "high"
)

// Compile produces a deterministic task graph from an approved run artifact (spec section 7.1).
//
// v1 compilation is rule-based and non-LLM. Nearby related requirements are grouped into
// deterministic chunks so the initial queue is smaller and more usable than one-task-per-ID.
func Compile(artifact *state.RunArtifact) (*CompileResult, error) {
	if artifact == nil {
		return nil, fmt.Errorf("nil run artifact")
	}
	if len(artifact.Requirements) == 0 {
		return nil, fmt.Errorf("run artifact has no requirements")
	}

	reqs := make([]state.Requirement, len(artifact.Requirements))
	copy(reqs, artifact.Requirements)
	sort.Slice(reqs, func(i, j int) bool { return requirementLess(reqs[i], reqs[j]) })

	groups := groupRequirements(reqs)
	tasks := make([]state.Task, 0, len(groups))
	coverageMap := make(map[string]*state.RequirementCoverage)
	previousTaskByLane := make(map[string]string)
	now := time.Now().Truncate(time.Second)

	for i, group := range groups {
		taskID := taskIDForGroup(group)
		laneKey := laneForRequirement(group[0])

		var dependsOn []string
		if prevTaskID, ok := previousTaskByLane[laneKey]; ok {
			dependsOn = append(dependsOn, prevTaskID)
		}

		task := state.Task{
			TaskID:           taskID,
			Slug:             slugForGroup(group),
			Title:            titleForGroup(group),
			TaskType:         "implementation",
			Tags:             tagsForGroup(group),
			CreatedAt:        now,
			UpdatedAt:        now,
			Order:            i + 1,
			LineageID:        taskID,
			RequirementIDs:   requirementIDs(group),
			DependsOn:        dependsOn,
			Scope:            scopeForGroup(group),
			Priority:         i + 1,
			RiskLevel:        riskForGroup(group),
			DefaultModel:     defaultModelForGroup(group),
			Status:           state.TaskPending,
			RequiredEvidence: evidenceForGroup(group),
		}
		task.ETag = computeETag(&task)
		tasks = append(tasks, task)
		previousTaskByLane[laneKey] = taskID

		for _, req := range group {
			if _, exists := coverageMap[req.ID]; !exists {
				coverageMap[req.ID] = &state.RequirementCoverage{
					RequirementID:   req.ID,
					Status:          "in_progress",
					CoveringTaskIDs: []string{},
				}
			}
			coverageMap[req.ID].CoveringTaskIDs = append(coverageMap[req.ID].CoveringTaskIDs, taskID)
		}
	}

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

func requirementLess(left, right state.Requirement) bool {
	if left.SourceSpec != right.SourceSpec {
		return left.SourceSpec < right.SourceSpec
	}
	if laneForRequirement(left) != laneForRequirement(right) {
		return laneForRequirement(left) < laneForRequirement(right)
	}
	if left.SourceLine > 0 && right.SourceLine > 0 && left.SourceLine != right.SourceLine {
		return left.SourceLine < right.SourceLine
	}
	return left.ID < right.ID
}

func groupRequirements(reqs []state.Requirement) [][]state.Requirement {
	const maxGroupSize = 6

	var groups [][]state.Requirement
	for _, req := range reqs {
		if len(groups) == 0 || shouldStartNewGroup(groups[len(groups)-1], req, maxGroupSize) {
			groups = append(groups, []state.Requirement{req})
			continue
		}
		groups[len(groups)-1] = append(groups[len(groups)-1], req)
	}
	return groups
}

func shouldStartNewGroup(current []state.Requirement, next state.Requirement, maxGroupSize int) bool {
	last := current[len(current)-1]

	if len(current) >= maxGroupSize {
		return true
	}
	if current[0].SourceSpec != next.SourceSpec {
		return true
	}
	if laneForRequirement(last) != laneForRequirement(next) {
		return true
	}
	if last.SourceLine > 0 && next.SourceLine > 0 && next.SourceLine-last.SourceLine > 3 {
		return true
	}
	return false
}

// taskIDFromRequirement produces a stable task ID from a requirement ID.
func taskIDFromRequirement(reqID string) string {
	return "task-" + strings.ToLower(reqID)
}

func taskIDForGroup(group []state.Requirement) string {
	if len(group) == 1 {
		return taskIDFromRequirement(group[0].ID)
	}
	return "task-" + strings.ToLower(group[0].ID) + "-to-" + strings.ToLower(group[len(group)-1].ID)
}

func titleForGroup(group []state.Requirement) string {
	if len(group) == 1 {
		return fmt.Sprintf("Implement %s", group[0].ID)
	}
	return fmt.Sprintf("Implement %s to %s", group[0].ID, group[len(group)-1].ID)
}

func slugForGroup(group []state.Requirement) string {
	if len(group) == 1 {
		return slugFromID(group[0].ID)
	}
	return strings.ToLower(strings.ReplaceAll(group[0].ID+"-"+group[len(group)-1].ID, "-", "_"))
}

func requirementIDs(group []state.Requirement) []string {
	ids := make([]string, 0, len(group))
	for _, req := range group {
		ids = append(ids, req.ID)
	}
	return ids
}

func classifyRisk(req state.Requirement) string {
	text := strings.ToLower(req.Text)
	switch {
	case strings.Contains(text, "must not") || strings.Contains(text, "security"):
		return riskHigh
	case strings.Contains(text, "must"):
		return riskMedium
	default:
		return riskLow
	}
}

func riskForGroup(group []state.Requirement) string {
	risk := riskLow
	for _, req := range group {
		reqRisk := classifyRisk(req)
		switch reqRisk {
		case riskHigh:
			return riskHigh
		case riskMedium:
			risk = riskMedium
		}
	}
	return risk
}

func defaultModelForGroup(group []state.Requirement) string {
	text := strings.ToLower(joinRequirementText(group))
	switch {
	case containsAny(text, "council", "review", "synthesis", "strateg"):
		return "opus"
	case containsAny(text, "scout", "information gathering", "list", "inspect"):
		return "haiku"
	default:
		return "sonnet"
	}
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

func tagsForGroup(group []state.Requirement) []string {
	seen := make(map[string]struct{})
	for _, req := range group {
		for _, tag := range tagsFromRequirement(req) {
			seen[tag] = struct{}{}
		}
		if req.SourceSpec != "" {
			seen[strings.TrimSuffix(filepath.Base(req.SourceSpec), filepath.Ext(req.SourceSpec))] = struct{}{}
		}
	}
	tags := make([]string, 0, len(seen))
	for tag := range seen {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	return tags
}

// computeETag produces a content hash for compare-and-swap consistency (spec section 3.4).
func computeETag(task *state.Task) string {
	task.ETag = ""
	task.UpdatedAt = time.Time{}
	data, err := json.Marshal(task)
	if err != nil {
		return ""
	}
	return state.SHA256Bytes(data)[:16]
}

func evidenceForGroup(group []state.Requirement) []string {
	seen := make(map[string]struct{})
	for _, req := range group {
		for _, evidence := range inferEvidence(req) {
			seen[evidence] = struct{}{}
		}
	}
	evidence := make([]string, 0, len(seen))
	for item := range seen {
		evidence = append(evidence, item)
	}
	sort.Strings(evidence)
	return evidence
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

func scopeForGroup(group []state.Requirement) state.TaskScope {
	owned := inferOwnedPaths(joinRequirementText(group))
	if len(owned) == 0 {
		owned = []string{"internal/engine"}
	}
	return state.TaskScope{
		OwnedPaths:    owned,
		IsolationMode: "direct",
	}
}

func joinRequirementText(group []state.Requirement) string {
	parts := make([]string, 0, len(group))
	for _, req := range group {
		parts = append(parts, req.Text)
	}
	return strings.Join(parts, " ")
}

func inferOwnedPaths(text string) []string {
	text = strings.ToLower(text)
	seen := make(map[string]struct{})

	add := func(paths ...string) {
		for _, path := range paths {
			seen[path] = struct{}{}
		}
	}

	if containsAny(text, "prepare", "review", "status", "tasks", "ready", "blocked", "next", "progress", "resume", "launch", "explain", "cli", "command") {
		add("cmd/attest")
	}
	if containsAny(text, "ingest", "spec", "artifact", "requirement", "compile", "task graph") {
		add("internal/compiler", "internal/engine")
	}
	if containsAny(text, "verify", "verification", "evidence", "quality gate") {
		add("internal/verifier", "internal/engine")
	}
	if containsAny(text, "persist", "state", "session", "resume", "reattach", "claim", "wave") {
		add("internal/state", "internal/engine")
	}
	if containsAny(text, "codex", "gemini", "opus", "council", "reviewer", "review") {
		add("internal/councilflow")
	}
	if containsAny(text, "routing", "model tier", "sonnet", "haiku", "opus") {
		add("internal/engine")
	}

	owned := make([]string, 0, len(seen))
	for path := range seen {
		owned = append(owned, path)
	}
	sort.Strings(owned)
	return owned
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func laneForRequirement(req state.Requirement) string {
	return req.SourceSpec + ":" + requirementFamily(req.ID)
}

func requirementFamily(reqID string) string {
	parts := strings.SplitN(reqID, "-", 3)
	if len(parts) >= 2 {
		return parts[0] + "-" + parts[1]
	}
	return reqID
}
