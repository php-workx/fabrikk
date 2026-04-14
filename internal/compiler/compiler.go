package compiler

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/php-workx/fabrikk/internal/state"
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

	defaultOwnedPath = "internal/engine"
)

// Compile produces a deterministic task graph from an approved run artifact (spec section 7.1).
//
// v1 compilation is rule-based and non-LLM. Each requirement currently becomes
// its own task until exploration-driven same-symbol grouping is implemented.
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

	alwaysConstraints := artifact.Boundaries.Normalized().Always

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
			Constraints:      append([]string(nil), alwaysConstraints...),
		}
		// Grouping metadata remains empty until the exploration-driven
		// same-symbol exception is implemented. With maxGroupSize=1,
		// compilation currently emits one requirement per task.
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

// PruneDependencies removes lane-only ordering edges that do not represent real coupling.
func PruneDependencies(tasks []state.Task) []state.Task {
	pruned := make([]state.Task, len(tasks))
	for i := range tasks {
		pruned[i] = tasks[i]
		pruned[i].DependsOn = append([]string(nil), tasks[i].DependsOn...)
	}

	taskByID := make(map[string]*state.Task, len(pruned))
	for i := range pruned {
		taskByID[pruned[i].TaskID] = &pruned[i]
	}

	for i := range pruned {
		kept := pruned[i].DependsOn[:0]
		for _, depID := range pruned[i].DependsOn {
			dep, ok := taskByID[depID]
			if !ok || shouldKeepDependency(dep, &pruned[i]) {
				kept = append(kept, depID)
			}
		}
		pruned[i].DependsOn = append([]string(nil), kept...)
	}

	return pruned
}

func shouldKeepDependency(dep, task *state.Task) bool {
	return pathSetsOverlap(dep.Scope.OwnedPaths, task.Scope.OwnedPaths) ||
		requirementSetsOverlap(dep.RequirementIDs, task.RequirementIDs) ||
		pathSetsOverlap(dep.Scope.OwnedPaths, task.Scope.ReadOnlyPaths)
}

func requirementSetsOverlap(left, right []string) bool {
	seen := make(map[string]struct{}, len(left))
	for _, id := range left {
		seen[id] = struct{}{}
	}
	for _, id := range right {
		if _, ok := seen[id]; ok {
			return true
		}
	}
	return false
}

func pathSetsOverlap(left, right []string) bool {
	for _, leftPath := range left {
		for _, rightPath := range right {
			if pathsOverlap(leftPath, rightPath) {
				return true
			}
		}
	}
	return false
}

func pathsOverlap(left, right string) bool {
	left = normalizeScopedPath(left)
	right = normalizeScopedPath(right)
	if left == "" || right == "" {
		return false
	}
	return left == right || strings.HasPrefix(left, right+"/") || strings.HasPrefix(right, left+"/")
}

func normalizeScopedPath(path string) string {
	path = strings.Trim(strings.ReplaceAll(path, `\\`, "/"), "/")
	if path == "" || path == "." {
		return ""
	}
	return path
}

// ComputeWaves groups tasks into dependency-depth waves and then serializes intra-wave file conflicts.
func ComputeWaves(tasks []state.Task) []state.Wave {
	taskMap := make(map[string]*state.Task, len(tasks))
	order := make(map[string]int, len(tasks))
	for i := range tasks {
		taskMap[tasks[i].TaskID] = &tasks[i]
		order[tasks[i].TaskID] = i
	}

	depth := make(map[string]int, len(tasks))
	for i := range tasks {
		computeDepth(tasks[i].TaskID, taskMap, depth)
	}

	waves := groupByDepth(tasks, depth)
	reverseDeps := buildReverseDeps(tasks, order)
	for changed := true; changed; {
		changed = false
		for i := range waves {
			conflicts := detectFileConflicts(waves[i], taskMap)
			if len(conflicts) == 0 {
				continue
			}
			waves = resolveConflicts(waves, i, conflicts, reverseDeps, order)
			changed = true
			break
		}
	}

	return waves
}

func computeDepth(taskID string, taskMap map[string]*state.Task, depth map[string]int) int {
	if d, ok := depth[taskID]; ok {
		return d
	}

	task, ok := taskMap[taskID]
	if !ok || task == nil {
		depth[taskID] = 0
		return 0
	}

	maxDepDepth := 0
	for _, depID := range task.DependsOn {
		depDepth := computeDepth(depID, taskMap, depth) + 1
		if depDepth > maxDepDepth {
			maxDepDepth = depDepth
		}
	}

	depth[taskID] = maxDepDepth
	return maxDepDepth
}

func groupByDepth(tasks []state.Task, depth map[string]int) []state.Wave {
	tasksByDepth := make(map[int][]string, len(tasks))
	maxDepth := 0
	for i := range tasks {
		taskDepth := depth[tasks[i].TaskID]
		tasksByDepth[taskDepth] = append(tasksByDepth[taskDepth], tasks[i].TaskID)
		if taskDepth > maxDepth {
			maxDepth = taskDepth
		}
	}

	waves := make([]state.Wave, 0, maxDepth+1)
	for taskDepth := 0; taskDepth <= maxDepth; taskDepth++ {
		taskIDs, ok := tasksByDepth[taskDepth]
		if !ok {
			continue
		}
		waves = append(waves, state.Wave{
			WaveID: fmt.Sprintf("wave-%d", taskDepth),
			Tasks:  append([]string(nil), taskIDs...),
		})
	}

	return waves
}

func buildReverseDeps(tasks []state.Task, order map[string]int) map[string][]string {
	reverse := make(map[string][]string, len(tasks))
	for i := range tasks {
		for _, depID := range tasks[i].DependsOn {
			reverse[depID] = append(reverse[depID], tasks[i].TaskID)
		}
	}
	for depID := range reverse {
		dependents := reverse[depID]
		sort.SliceStable(dependents, func(i, j int) bool {
			return order[dependents[i]] < order[dependents[j]]
		})
		reverse[depID] = dependents
	}
	return reverse
}

func detectFileConflicts(wave state.Wave, taskMap map[string]*state.Task) []state.FileConflict {
	conflicts := make([]state.FileConflict, 0)
	for i := 0; i < len(wave.Tasks); i++ {
		left := taskMap[wave.Tasks[i]]
		if left == nil {
			continue
		}
		for j := i + 1; j < len(wave.Tasks); j++ {
			right := taskMap[wave.Tasks[j]]
			if right == nil {
				continue
			}
			if conflictPath, ok := detectConflictPath(left, right); ok {
				conflicts = append(conflicts, state.FileConflict{
					FilePath: conflictPath,
					TaskA:    left.TaskID,
					TaskB:    right.TaskID,
				})
			}
		}
	}
	return conflicts
}

func detectConflictPath(left, right *state.Task) (string, bool) {
	return ConflictPath(conflictPaths(left), conflictPaths(right))
}

// ConflictPath reports the canonical overlapping path for two task scopes.
func ConflictPath(leftPaths, rightPaths []string) (string, bool) {
	leftPaths = normalizedConflictPaths(leftPaths)
	rightPaths = normalizedConflictPaths(rightPaths)
	for _, leftPath := range leftPaths {
		for _, rightPath := range rightPaths {
			if conflictPath, ok := overlappingConflictPath(leftPath, rightPath); ok {
				return conflictPath, true
			}
		}
	}
	return "", false
}

func conflictPaths(task *state.Task) []string {
	if task == nil {
		return nil
	}
	if len(task.FilesLikelyTouched) > 0 {
		return normalizedConflictPaths(task.FilesLikelyTouched)
	}
	return normalizedConflictPaths(task.Scope.OwnedPaths)
}

func normalizedConflictPaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(paths))
	normalized := make([]string, 0, len(paths))
	for _, path := range paths {
		path = normalizeScopedPath(path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		normalized = append(normalized, path)
	}
	sort.Strings(normalized)
	return normalized
}

func overlappingConflictPath(left, right string) (string, bool) {
	left = normalizeScopedPath(left)
	right = normalizeScopedPath(right)
	if !pathsOverlap(left, right) {
		return "", false
	}
	switch {
	case left == right:
		return left, true
	case strings.HasPrefix(left, right+"/"):
		return left, true
	case strings.HasPrefix(right, left+"/"):
		return right, true
	}
	return "", false
}

func resolveConflicts(waves []state.Wave, waveIndex int, conflicts []state.FileConflict, reverseDeps map[string][]string, order map[string]int) []state.Wave {
	if len(conflicts) == 0 {
		return waves
	}
	movedTaskID := selectConflictTaskToMove(conflicts, order)
	waves = moveTaskToWave(waves, movedTaskID, waveIndex+1, order)
	return cascadeDependents(waves, movedTaskID, reverseDeps, order)
}

func selectConflictTaskToMove(conflicts []state.FileConflict, order map[string]int) string {
	counts := make(map[string]int, len(conflicts)*2)
	candidates := make([]string, 0, len(conflicts)*2)
	seen := make(map[string]struct{}, len(conflicts)*2)
	for _, conflict := range conflicts {
		for _, taskID := range []string{conflict.TaskA, conflict.TaskB} {
			counts[taskID]++
			if _, ok := seen[taskID]; ok {
				continue
			}
			seen[taskID] = struct{}{}
			candidates = append(candidates, taskID)
		}
	}

	movedTaskID := conflicts[0].TaskB
	bestCount := counts[movedTaskID]
	for _, taskID := range candidates {
		count := counts[taskID]
		if count > bestCount || (count == bestCount && order[taskID] > order[movedTaskID]) {
			movedTaskID = taskID
			bestCount = count
		}
	}
	return movedTaskID
}

func cascadeDependents(waves []state.Wave, movedTaskID string, reverseDeps map[string][]string, order map[string]int) []state.Wave {
	queue := []string{movedTaskID}
	for len(queue) > 0 {
		taskID := queue[0]
		queue = queue[1:]
		waveIndex := findWaveIndex(waves, taskID)
		if waveIndex == -1 {
			continue
		}
		for _, dependentID := range reverseDeps[taskID] {
			if findWaveIndex(waves, dependentID) != waveIndex {
				continue
			}
			waves = moveTaskToWave(waves, dependentID, waveIndex+1, order)
			queue = append(queue, dependentID)
		}
	}
	return waves
}

func moveTaskToWave(waves []state.Wave, taskID string, targetWave int, order map[string]int) []state.Wave {
	currentWave := findWaveIndex(waves, taskID)
	if currentWave == -1 || currentWave >= targetWave {
		return waves
	}
	waves[currentWave].Tasks = removeTaskID(waves[currentWave].Tasks, taskID)
	waves = ensureWave(waves, targetWave)
	waves[targetWave].Tasks = insertTaskID(waves[targetWave].Tasks, taskID, order)
	return waves
}

func findWaveIndex(waves []state.Wave, taskID string) int {
	for i := range waves {
		for _, existing := range waves[i].Tasks {
			if existing == taskID {
				return i
			}
		}
	}
	return -1
}

func removeTaskID(taskIDs []string, taskID string) []string {
	filtered := taskIDs[:0]
	for _, existing := range taskIDs {
		if existing != taskID {
			filtered = append(filtered, existing)
		}
	}
	return filtered
}

func ensureWave(waves []state.Wave, waveIndex int) []state.Wave {
	for len(waves) <= waveIndex {
		nextIndex := len(waves)
		waves = append(waves, state.Wave{WaveID: fmt.Sprintf("wave-%d", nextIndex)})
	}
	return waves
}

func insertTaskID(taskIDs []string, taskID string, order map[string]int) []string {
	insertAt := len(taskIDs)
	for i, existing := range taskIDs {
		if order[taskID] < order[existing] {
			insertAt = i
			break
		}
	}
	taskIDs = append(taskIDs, "")
	copy(taskIDs[insertAt+1:], taskIDs[insertAt:])
	taskIDs[insertAt] = taskID
	return taskIDs
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

	alwaysConstraints := artifact.Boundaries.Normalized().Always

	tasks := make([]state.Task, 0, len(plan.Slices))
	coverageMap := make(map[string]*state.RequirementCoverage)
	now := time.Now().Truncate(time.Second)

	for i := range plan.Slices {
		slice := &plan.Slices[i]
		reqs, err := lookupRequirements(requirementsByID, slice.RequirementIDs)
		if err != nil {
			return nil, err
		}

		validationChecks := cloneValidationChecks(slice.ValidationChecks)
		if len(validationChecks) == 0 {
			validationChecks = DeriveValidationChecks(artifact, slice)
		}
		dependsOn := mergeTaskIDs(
			taskIDsFromSliceIDs(slice.DependsOn),
			waveBarrierTaskIDs(plan.Slices, i),
		)

		task := state.Task{
			TaskID:               taskIDFromSliceID(slice.SliceID),
			Slug:                 slugFromSliceID(slice.SliceID),
			Title:                slice.Title,
			TaskType:             "implementation",
			Tags:                 tagsForSlice(slice, reqs),
			CreatedAt:            now,
			UpdatedAt:            now,
			Order:                i + 1,
			LineageID:            taskIDFromSliceID(slice.SliceID),
			RequirementIDs:       append([]string(nil), slice.RequirementIDs...),
			DependsOn:            dependsOn,
			Scope:                scopeForSlice(slice),
			FilesLikelyTouched:   append([]string(nil), slice.FilesLikelyTouched...),
			Priority:             i + 1,
			RiskLevel:            riskForSlice(slice, reqs),
			DefaultModel:         defaultModelForSlice(slice, reqs),
			Status:               state.TaskPending,
			RequiredEvidence:     evidenceForSlice(slice, reqs),
			ValidationChecks:     validationChecks,
			ImplementationDetail: cloneImplementationDetail(slice.ImplementationDetail),
			Constraints:          append([]string(nil), alwaysConstraints...),
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
	groups := make([][]state.Requirement, 0, len(reqs))
	for _, req := range reqs {
		group := []state.Requirement{req}
		groups = append(groups, group)
	}
	return groups
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
		default:
			// riskLow — no change needed.
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

func evidenceForSlice(_ *state.ExecutionSlice, reqs []state.Requirement) []string {
	evidence := evidenceForGroup(reqs)
	if len(evidence) > 0 {
		return evidence
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
		owned = []string{defaultOwnedPath}
	}
	return state.TaskScope{
		OwnedPaths:    owned,
		IsolationMode: "direct",
	}
}

func scopeForSlice(slice *state.ExecutionSlice) state.TaskScope {
	owned := append([]string(nil), slice.OwnedPaths...)
	if len(owned) == 0 {
		owned = []string{defaultOwnedPath}
	}
	return state.TaskScope{
		OwnedPaths:    owned,
		IsolationMode: "direct",
	}
}

func cloneImplementationDetail(detail state.ImplementationDetail) state.ImplementationDetail {
	return state.ImplementationDetail{
		FilesToModify: append([]state.FileChange(nil), detail.FilesToModify...),
		SymbolsToAdd:  append([]string(nil), detail.SymbolsToAdd...),
		SymbolsToUse:  append([]string(nil), detail.SymbolsToUse...),
		TestsToAdd:    append([]string(nil), detail.TestsToAdd...),
	}
}

var allowedValidationTools = map[string]struct{}{
	"go":      {},
	"just":    {},
	"make":    {},
	"npm":     {},
	"python3": {},
}

// DeriveValidationChecks produces deterministic validation checks from slice detail and the project quality gate.
func DeriveValidationChecks(artifact *state.RunArtifact, slice *state.ExecutionSlice) []state.ValidationCheck {
	checks := make([]state.ValidationCheck, 0, 4)
	if slice != nil {
		if newFiles := newFileValidationPaths(slice.ImplementationDetail); len(newFiles) > 0 {
			checks = append(checks, state.ValidationCheck{Type: "files_exist", Paths: newFiles})
		}
		if file := contentCheckTargetFile(slice); file != "" {
			for _, symbol := range sortedUniqueNonEmpty(slice.ImplementationDetail.SymbolsToAdd) {
				checks = append(checks, state.ValidationCheck{
					Type:    "content_check",
					File:    file,
					Pattern: regexp.QuoteMeta(symbol),
				})
			}
		}
		if pkg := testPackagePattern(slice); pkg != "" && isValidationToolAllowed("go") {
			for _, testName := range sortedUniqueNonEmpty(slice.ImplementationDetail.TestsToAdd) {
				checks = append(checks, state.ValidationCheck{
					Type: "tests",
					Tool: "go",
					Args: []string{"test", "-run", "^" + regexp.QuoteMeta(testName) + "$", pkg},
				})
			}
		}
	}
	if qualityGateCheck, ok := qualityGateValidationCheck(artifact); ok {
		checks = append(checks, qualityGateCheck)
	}
	return checks
}

func newFileValidationPaths(detail state.ImplementationDetail) []string {
	paths := make([]string, 0, len(detail.FilesToModify))
	for _, file := range detail.FilesToModify {
		if !file.IsNew {
			continue
		}
		paths = append(paths, file.Path)
	}
	return sortedUniqueNonEmpty(paths)
}

func contentCheckTargetFile(slice *state.ExecutionSlice) string {
	if slice == nil {
		return ""
	}
	if file := firstNonTestFile(filePaths(slice.ImplementationDetail.FilesToModify)); file != "" {
		return file
	}
	if file := firstNonTestFile(slice.FilesLikelyTouched); file != "" {
		return file
	}
	if file := firstExistingPath(filePaths(slice.ImplementationDetail.FilesToModify)); file != "" {
		return file
	}
	return firstExistingPath(slice.FilesLikelyTouched)
}

func testPackagePattern(slice *state.ExecutionSlice) string {
	if slice == nil {
		return ""
	}
	if dir := firstTestDirectory(filePaths(slice.ImplementationDetail.FilesToModify)); dir != "" {
		return packagePatternForDir(dir)
	}
	if dir := firstTestDirectory(slice.FilesLikelyTouched); dir != "" {
		return packagePatternForDir(dir)
	}
	if file := contentCheckTargetFile(slice); file != "" {
		return packagePatternForDir(filepath.Dir(file))
	}
	if file := firstExistingPath(filePaths(slice.ImplementationDetail.FilesToModify)); file != "" {
		return packagePatternForDir(filepath.Dir(file))
	}
	if file := firstExistingPath(slice.FilesLikelyTouched); file != "" {
		return packagePatternForDir(filepath.Dir(file))
	}
	return ""
}

func qualityGateValidationCheck(artifact *state.RunArtifact) (state.ValidationCheck, bool) {
	if artifact == nil || artifact.QualityGate == nil {
		return state.ValidationCheck{}, false
	}
	command := strings.TrimSpace(artifact.QualityGate.Command)
	if command == "" || strings.ContainsAny(command, "\"'&;|<>$`") {
		return state.ValidationCheck{}, false
	}
	parts := strings.Fields(command)
	if len(parts) == 0 || !isValidationToolAllowed(parts[0]) {
		return state.ValidationCheck{}, false
	}
	return state.ValidationCheck{
		Type: "command",
		Tool: parts[0],
		Args: append([]string(nil), parts[1:]...),
	}, true
}

func isValidationToolAllowed(tool string) bool {
	_, ok := allowedValidationTools[strings.TrimSpace(tool)]
	return ok
}

func filePaths(files []state.FileChange) []string {
	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.Path)
	}
	return paths
}

func firstNonTestFile(paths []string) string {
	for _, path := range sortedUniqueNonEmpty(paths) {
		if !strings.HasSuffix(path, "_test.go") {
			return path
		}
	}
	return ""
}

func firstExistingPath(paths []string) string {
	sorted := sortedUniqueNonEmpty(paths)
	if len(sorted) == 0 {
		return ""
	}
	return sorted[0]
}

func firstTestDirectory(paths []string) string {
	for _, path := range sortedUniqueNonEmpty(paths) {
		if strings.HasSuffix(path, "_test.go") {
			return filepath.Dir(path)
		}
	}
	return ""
}

func packagePatternForDir(dir string) string {
	clean := filepath.Clean(strings.TrimSpace(dir))
	if clean == "." || clean == "" {
		return "./..."
	}
	return "./" + filepath.ToSlash(clean) + "/..."
}

func sortedUniqueNonEmpty(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	result := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	sort.Strings(result)
	return result
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

func waveBarrierTaskIDs(slices []state.ExecutionSlice, index int) []string {
	if index < 0 || index >= len(slices) {
		return nil
	}
	currentWave, ok := waveIndex(slices[index].WaveID)
	if !ok || currentWave == 0 {
		return nil
	}
	previousWave := currentWave - 1
	barrier := make([]string, 0, len(slices))
	for i := 0; i < index; i++ {
		wave, ok := waveIndex(slices[i].WaveID)
		if !ok || wave != previousWave {
			continue
		}
		barrier = append(barrier, taskIDFromSliceID(slices[i].SliceID))
	}
	return barrier
}

func waveIndex(waveID string) (int, bool) {
	trimmed := strings.TrimSpace(waveID)
	if trimmed == "" {
		return 0, false
	}
	raw, ok := strings.CutPrefix(trimmed, "wave-")
	if !ok {
		return 0, false
	}
	index, err := strconv.Atoi(raw)
	if err != nil || index < 0 {
		return 0, false
	}
	return index, true
}

func mergeTaskIDs(groups ...[]string) []string {
	seen := make(map[string]struct{})
	merged := make([]string, 0)
	for _, group := range groups {
		for _, id := range group {
			if strings.TrimSpace(id) == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			merged = append(merged, id)
		}
	}
	return merged
}

func cloneValidationChecks(checks []state.ValidationCheck) []state.ValidationCheck {
	if len(checks) == 0 {
		return nil
	}
	cloned := make([]state.ValidationCheck, 0, len(checks))
	for _, check := range checks {
		cloned = append(cloned, state.ValidationCheck{
			Type:    check.Type,
			Paths:   append([]string(nil), check.Paths...),
			File:    check.File,
			Pattern: check.Pattern,
			Tool:    check.Tool,
			Args:    append([]string(nil), check.Args...),
		})
	}
	return cloned
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
		add("cmd/fabrikk")
	}
	if containsAny(text, "ingest", "spec", "artifact", "requirement", "compile", "task graph") {
		add("internal/compiler", defaultOwnedPath)
	}
	if containsAny(text, "verify", "verification", "evidence", "quality gate") {
		add("internal/verifier", defaultOwnedPath)
	}
	if containsAny(text, "persist", "state", "session", "resume", "reattach", "claim", "wave") {
		add("internal/state", defaultOwnedPath)
	}
	if containsAny(text, "codex", "gemini", "opus", "council", "reviewer", "review") {
		add("internal/councilflow")
	}
	if containsAny(text, "routing", "model tier", "sonnet", "haiku", "opus") {
		add(defaultOwnedPath)
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
