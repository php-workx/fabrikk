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

// CompileExecutionPlan produces a deterministic task graph from an approved execution plan.
func CompileExecutionPlan(artifact *state.RunArtifact, plan *state.ExecutionPlan) (*CompileResult, error) {
	if artifact == nil {
		return nil, fmt.Errorf("nil run artifact")
	}
	if plan == nil {
		return nil, fmt.Errorf("nil execution plan")
	}
	if len(plan.Slices) == 0 {
		return nil, fmt.Errorf("execution plan has no slices")
	}

	requirementsByID := make(map[string]state.Requirement, len(artifact.Requirements))
	for _, req := range artifact.Requirements {
		requirementsByID[req.ID] = req
	}

	tasks := make([]state.Task, 0, len(plan.Slices))
	coverageMap := make(map[string]*state.RequirementCoverage)
	now := time.Now().Truncate(time.Second)

	for i := range plan.Slices {
		slice := &plan.Slices[i]
		reqs, err := lookupRequirements(requirementsByID, slice.RequirementIDs)
		if err != nil {
			return nil, err
		}

		task := state.Task{
			TaskID:           taskIDFromSliceID(slice.SliceID),
			Slug:             slugFromSliceID(slice.SliceID),
			Title:            slice.Title,
			TaskType:         "implementation",
			Tags:             tagsForSlice(slice, reqs),
			CreatedAt:        now,
			UpdatedAt:        now,
			Order:            i + 1,
			LineageID:        taskIDFromSliceID(slice.SliceID),
			RequirementIDs:   append([]string(nil), slice.RequirementIDs...),
			DependsOn:        taskIDsFromSliceIDs(slice.DependsOn),
			Scope:            scopeForSlice(slice),
			Priority:         i + 1,
			RiskLevel:        riskForSlice(slice, reqs),
			DefaultModel:     defaultModelForSlice(slice, reqs),
			Status:           state.TaskPending,
			RequiredEvidence: evidenceForSlice(slice, reqs),
		}
		task.ETag = computeETag(&task)
		tasks = append(tasks, task)

		for _, reqID := range slice.RequirementIDs {
			if _, exists := coverageMap[reqID]; !exists {
				coverageMap[reqID] = &state.RequirementCoverage{
					RequirementID:   reqID,
					Status:          "in_progress",
					CoveringTaskIDs: []string{},
				}
			}
			coverageMap[reqID].CoveringTaskIDs = append(coverageMap[reqID].CoveringTaskIDs, task.TaskID)
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
	const maxGroupSize = 4

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
	if groupingTheme(last) != groupingTheme(next) {
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

func taskIDFromSliceID(sliceID string) string {
	return "task-" + strings.ToLower(sliceID)
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

func slugFromSliceID(sliceID string) string {
	return strings.ToLower(strings.ReplaceAll(sliceID, "-", "_"))
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
	tmp := *task
	tmp.ETag = ""
	tmp.UpdatedAt = time.Time{}
	data, err := json.Marshal(&tmp)
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

func evidenceForSlice(slice *state.ExecutionSlice, reqs []state.Requirement) []string {
	evidence := evidenceForGroup(reqs)
	if len(evidence) > 0 {
		return evidence
	}
	if len(slice.AcceptanceChecks) > 0 {
		return []string{"quality_gate_pass"}
	}
	return []string{"quality_gate_pass"}
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

func scopeForSlice(slice *state.ExecutionSlice) state.TaskScope {
	owned := append([]string(nil), slice.OwnedPaths...)
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

func lookupRequirements(requirementsByID map[string]state.Requirement, ids []string) ([]state.Requirement, error) {
	reqs := make([]state.Requirement, 0, len(ids))
	for _, id := range ids {
		req, ok := requirementsByID[id]
		if !ok {
			return nil, fmt.Errorf("execution plan references unknown requirement %s", id)
		}
		reqs = append(reqs, req)
	}
	return reqs, nil
}

func taskIDsFromSliceIDs(sliceIDs []string) []string {
	taskIDs := make([]string, 0, len(sliceIDs))
	for _, sliceID := range sliceIDs {
		taskIDs = append(taskIDs, taskIDFromSliceID(sliceID))
	}
	return taskIDs
}

func tagsForSlice(_ *state.ExecutionSlice, reqs []state.Requirement) []string {
	if len(reqs) > 0 {
		return tagsForGroup(reqs)
	}
	return nil
}

func riskForSlice(slice *state.ExecutionSlice, reqs []state.Requirement) string {
	if slice.Risk != "" {
		return slice.Risk
	}
	if len(reqs) > 0 {
		return riskForGroup(reqs)
	}
	return riskLow
}

func defaultModelForSlice(_ *state.ExecutionSlice, reqs []state.Requirement) string {
	if len(reqs) > 0 {
		return defaultModelForGroup(reqs)
	}
	return "sonnet"
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

func groupingTheme(req state.Requirement) string {
	text := strings.ToLower(req.Text)

	switch {
	case containsAny(text, "session", "reattach", "resume", "continues after", "later session"):
		return "persistence"
	case containsAny(text, "codex", "gemini", "opus", "council", "review", "reviewer", "synthesis"):
		return "review"
	case containsAny(text, "verify", "verification", "evidence", "repair"):
		return "verification"
	case containsAny(text, "finish", "finishes", "explicitly blocked", "closure"):
		return "completion"
	case containsAny(text, "task graph", "compile", "deterministic"):
		return "planning"
	case containsAny(text, "approval", "approved", "source of truth"):
		return "approval"
	case containsAny(text, "scope metadata", "overlapping", "owning", "concurrently", "serialize", "split"):
		return "concurrency"
	case containsAny(text, "ingest", "extract", "requirement id", "source spec", "run preparation"):
		return "preparation"
	default:
		return requirementFamily(req.ID)
	}
}
