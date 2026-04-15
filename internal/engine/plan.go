package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/php-workx/fabrikk/internal/compiler"
	"github.com/php-workx/fabrikk/internal/councilflow"
	"github.com/php-workx/fabrikk/internal/state"
)

const (
	sha256Prefix             = "sha256:"
	graphFindingFmtID        = "epr-graph-%03d"
	waveWarningIDFmt         = "epr-w-%03d"
	readTechSpecErrFmt       = "read technical spec: %w"
	sharedFileMitigationText = "Refresh the shared-file base SHA before starting the later wave."
)

// DraftExecutionPlan derives a run-scoped execution plan from an approved technical spec.
func (e *Engine) DraftExecutionPlan(ctx context.Context) (*state.ExecutionPlan, error) {
	techSpecHash, err := e.readApprovedTechnicalSpecHash()
	if err != nil {
		return nil, err
	}

	artifact, err := e.RunDir.ReadArtifact()
	if err != nil {
		return nil, fmt.Errorf("read run artifact: %w", err)
	}
	if askFirst := artifact.Boundaries.Normalized().AskFirst; len(askFirst) > 0 {
		return nil, &HumanInputRequiredError{Items: askFirst}
	}

	compiled, err := compiler.Compile(artifact)
	if err != nil {
		return nil, fmt.Errorf("compile execution slices: %w", err)
	}

	sa, err := e.runStructuralAnalysis(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  explore: structural analysis failed: %v (proceeding without structural context)\n", err)
		sa = nil
	}
	exploration, err := e.exploreForPlan(ctx, artifact, sa)
	if err != nil {
		return nil, fmt.Errorf("explore for plan: %w", err)
	}
	enrichTasksFromExploration(compiled.Tasks, exploration)
	compiled.Tasks = compiler.PruneDependencies(compiled.Tasks)

	waveByTaskID := make(map[string]string, len(compiled.Tasks))
	for _, wave := range compiler.ComputeWaves(compiled.Tasks) {
		for _, taskID := range wave.Tasks {
			waveByTaskID[taskID] = wave.WaveID
		}
	}

	now := time.Now()
	plan := &state.ExecutionPlan{
		SchemaVersion:           "0.1",
		RunID:                   filepathBase(e.RunDir.Root),
		ArtifactType:            "execution_plan",
		SourceTechnicalSpecHash: techSpecHash,
		Status:                  state.ArtifactDrafted,
		Slices:                  make([]state.ExecutionSlice, 0, len(compiled.Tasks)),
		GeneratedAt:             now,
	}

	for i := range compiled.Tasks {
		task := &compiled.Tasks[i]
		slice := state.ExecutionSlice{
			SliceID:              sliceIDFromTaskID(task.TaskID),
			Title:                task.Title,
			Goal:                 goalFromTask(task),
			RequirementIDs:       append([]string(nil), task.RequirementIDs...),
			DependsOn:            sliceIDsFromTaskIDs(task.DependsOn),
			WaveID:               waveByTaskID[task.TaskID],
			FilesLikelyTouched:   filesLikelyTouchedForTask(task),
			OwnedPaths:           append([]string(nil), task.Scope.OwnedPaths...),
			AcceptanceChecks:     acceptanceChecksForTask(artifact, task),
			ImplementationDetail: cloneImplementationDetail(task.ImplementationDetail),
			Risk:                 task.RiskLevel,
			Size:                 executionSliceSize(task.RequirementIDs),
			Notes:                fmt.Sprintf("Derived from approved technical spec requirements: %s", strings.Join(task.RequirementIDs, ", ")),
		}
		slice.ValidationChecks = compiler.DeriveValidationChecks(artifact, &slice)
		plan.Slices = append(plan.Slices, slice)
	}

	if err := e.RunDir.WriteExecutionPlan(plan); err != nil {
		return nil, fmt.Errorf("write execution plan: %w", err)
	}
	if err := e.RunDir.WriteExecutionPlanMarkdown(renderExecutionPlanMarkdown(plan)); err != nil {
		return nil, fmt.Errorf("write execution plan markdown: %w", err)
	}

	_ = e.RunDir.AppendEvent(state.Event{
		Timestamp: now,
		Type:      "execution_plan_drafted",
		RunID:     plan.RunID,
		Detail:    fmt.Sprintf("%d slices", len(plan.Slices)),
	})

	return plan, nil
}

func filesLikelyTouchedForTask(task *state.Task) []string {
	if len(task.FilesLikelyTouched) > 0 {
		return append([]string(nil), task.FilesLikelyTouched...)
	}
	return append([]string(nil), task.Scope.OwnedPaths...)
}

func enrichTasksFromExploration(tasks []state.Task, exploration *state.ExplorationResult) {
	if exploration == nil {
		return
	}
	for i := range tasks {
		tasks[i].FilesLikelyTouched = touchedFilesForTask(tasks[i].Scope.OwnedPaths, exploration)
		tasks[i].ImplementationDetail = implementationDetailForTask(tasks[i].Scope.OwnedPaths, exploration)
	}
}

func touchedFilesForTask(ownedPaths []string, exploration *state.ExplorationResult) []string {
	matched := make([]string, 0, len(exploration.FileInventory)+len(exploration.Symbols)+len(exploration.TestFiles)+len(exploration.ReusePoints))
	for _, file := range exploration.FileInventory {
		if matchesOwnedPath(file.Path, ownedPaths) {
			matched = append(matched, file.Path)
		}
	}
	for _, symbol := range exploration.Symbols {
		if matchesOwnedPath(symbol.FilePath, ownedPaths) {
			matched = append(matched, symbol.FilePath)
		}
	}
	for _, testFile := range exploration.TestFiles {
		if matchesOwnedPath(testFile.Path, ownedPaths) {
			matched = append(matched, testFile.Path)
		}
		if matchesOwnedPath(testFile.Covers, ownedPaths) {
			matched = append(matched, testFile.Covers)
		}
	}
	for _, reuse := range exploration.ReusePoints {
		if matchesOwnedPath(reuse.FilePath, ownedPaths) {
			matched = append(matched, reuse.FilePath)
		}
	}
	return sortedUniquePaths(matched)
}

func implementationDetailForTask(ownedPaths []string, exploration *state.ExplorationResult) state.ImplementationDetail {
	detail := state.ImplementationDetail{}
	for _, file := range exploration.FileInventory {
		if !matchesOwnedPath(file.Path, ownedPaths) {
			continue
		}
		change := "Modify existing implementation surfaced during exploration"
		if file.IsNew {
			change = "Create implementation file surfaced during exploration"
		}
		detail.FilesToModify = append(detail.FilesToModify, state.FileChange{
			Path:   file.Path,
			Change: change,
			IsNew:  file.IsNew,
		})
	}
	for _, reuse := range exploration.ReusePoints {
		if matchesOwnedPath(reuse.FilePath, ownedPaths) {
			detail.SymbolsToUse = append(detail.SymbolsToUse, formatReusePoint(reuse))
		}
	}
	for _, testFile := range exploration.TestFiles {
		if !matchesOwnedPath(testFile.Path, ownedPaths) && !matchesOwnedPath(testFile.Covers, ownedPaths) {
			continue
		}
		detail.TestsToAdd = append(detail.TestsToAdd, testFile.TestNames...)
	}
	sort.Slice(detail.FilesToModify, func(i, j int) bool {
		return detail.FilesToModify[i].Path < detail.FilesToModify[j].Path
	})
	detail.SymbolsToAdd = sortedUniqueStrings(detail.SymbolsToAdd)
	detail.SymbolsToUse = sortedUniqueStrings(detail.SymbolsToUse)
	detail.TestsToAdd = sortedUniqueStrings(detail.TestsToAdd)
	return detail
}

func matchesOwnedPath(path string, ownedPaths []string) bool {
	cleanPath := filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
	if cleanPath == "." || cleanPath == "" {
		return false
	}
	for _, owned := range ownedPaths {
		cleanOwned := filepath.ToSlash(filepath.Clean(strings.TrimSpace(owned)))
		if cleanOwned == "." || cleanOwned == "" {
			continue
		}
		if cleanPath == cleanOwned || strings.HasPrefix(cleanPath, cleanOwned+"/") {
			return true
		}
	}
	return false
}

func formatReusePoint(reuse state.ReusePoint) string {
	if reuse.Line > 0 {
		return fmt.Sprintf("%s at %s:%d", reuse.Symbol, reuse.FilePath, reuse.Line)
	}
	return fmt.Sprintf("%s at %s", reuse.Symbol, reuse.FilePath)
}

func sortedUniquePaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		clean := filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
		if clean == "." || clean == "" {
			continue
		}
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		result = append(result, clean)
	}
	sort.Strings(result)
	return result
}

func sortedUniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
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

func cloneImplementationDetail(detail state.ImplementationDetail) state.ImplementationDetail {
	return state.ImplementationDetail{
		FilesToModify: append([]state.FileChange(nil), detail.FilesToModify...),
		SymbolsToAdd:  append([]string(nil), detail.SymbolsToAdd...),
		SymbolsToUse:  append([]string(nil), detail.SymbolsToUse...),
		TestsToAdd:    append([]string(nil), detail.TestsToAdd...),
	}
}

// ReviewExecutionPlan runs deterministic structural checks over the plan artifact.
// ctx is accepted for interface symmetry with other lifecycle methods but is not used:
// review work is local I/O only and finishes well within typical caller timeouts.
func (e *Engine) ReviewExecutionPlan(_ context.Context) (*state.ExecutionPlanReview, error) {
	plan, _, err := e.readExecutionPlanWithHash()
	if err != nil {
		return nil, err
	}

	review := &state.ExecutionPlanReview{
		SchemaVersion: "0.1",
		RunID:         plan.RunID,
		ArtifactType:  "execution_plan_review",
		Status:        state.ReviewPass,
		Summary:       "Execution plan slices are actionable.",
		ReviewedAt:    time.Now(),
	}

	if len(plan.Slices) == 0 {
		review.Status = state.ReviewFail
		review.BlockingFindings = append(review.BlockingFindings, state.ReviewFinding{
			FindingID:       "epr-001",
			Severity:        "high",
			Category:        "missing_slices",
			Summary:         "execution plan does not contain any slices",
			SuggestedRepair: "Generate at least one implementation slice before review.",
		})
	}

	validateSliceGraph(plan.Slices, review)
	validateSliceWaves(plan.Slices, review)
	for i := range plan.Slices {
		reviewSlice(&plan.Slices[i], review)
	}

	// Cross-slice requirement coverage: check all artifact requirements are covered.
	artifact, artifactErr := e.RunDir.ReadArtifact()
	if artifactErr == nil {
		validateRequirementCoverage(artifact, plan.Slices, review)
	} else {
		review.Status = state.ReviewFail
		review.BlockingFindings = append(review.BlockingFindings, state.ReviewFinding{
			FindingID:       fmt.Sprintf(graphFindingFmtID, len(review.BlockingFindings)+1),
			Severity:        "high",
			Category:        "artifact_read_failure",
			Summary:         fmt.Sprintf("could not read run artifact for requirement coverage validation: %v", artifactErr),
			SuggestedRepair: "Restore a readable run-artifact.json before reviewing the execution plan.",
		})
	}

	review.Reviewers = []state.ReviewSummary{{
		ReviewerID: "deterministic",
		Pass:       review.Status != state.ReviewFail,
		Summary:    "Structural plan review completed.",
	}}
	if review.Status == state.ReviewFail {
		review.Summary = "Execution plan has blocking structural issues."
	}

	if plan.Status != state.ArtifactApproved {
		plan.Status = state.ArtifactUnderReview
		if err := e.RunDir.WriteExecutionPlan(plan); err != nil {
			return nil, fmt.Errorf("update execution plan status: %w", err)
		}
		if err := e.RunDir.WriteExecutionPlanMarkdown(renderExecutionPlanMarkdown(plan)); err != nil {
			return nil, fmt.Errorf("update execution plan markdown: %w", err)
		}
	}
	_, planHash, err := e.readExecutionPlanWithHash()
	if err != nil {
		return nil, err
	}
	review.ExecutionPlanHash = planHash
	if err := e.RunDir.WriteExecutionPlanReview(review); err != nil {
		return nil, fmt.Errorf("write execution plan review: %w", err)
	}

	_ = e.RunDir.AppendEvent(state.Event{
		Timestamp: review.ReviewedAt,
		Type:      "execution_plan_reviewed",
		RunID:     review.RunID,
		Detail:    string(review.Status),
	})

	return review, nil
}

// CouncilReviewExecutionPlan runs council review over the drafted execution plan after structural review passes.
func (e *Engine) CouncilReviewExecutionPlan(ctx context.Context, cfg councilflow.CouncilConfig) (*councilflow.CouncilResult, error) {
	review, err := e.RunDir.ReadExecutionPlanReview()
	if err != nil {
		return nil, fmt.Errorf("read execution plan review: %w", err)
	}
	if review.Status != state.ReviewPass {
		return nil, fmt.Errorf("execution plan review is not passing")
	}

	planMarkdown, err := e.RunDir.ReadExecutionPlanMarkdown()
	if err != nil {
		return nil, fmt.Errorf("read execution plan markdown: %w", err)
	}
	if cfg.SpecPath == "" {
		cfg.SpecPath = e.RunDir.ExecutionPlanMarkdown()
	}
	if len(cfg.CustomPersonas) == 0 {
		cfg.CustomPersonas = executionPlanCouncilPersonas()
		cfg.SkipDynPersonas = true
	}
	if cfg.CodebaseContext == "" {
		cfg.CodebaseContext = executionPlanCouncilContext(e)
	}

	councilDir := filepath.Join(e.RunDir.Root, "execution-plan-council")
	result, err := councilflow.RunCouncil(ctx, string(planMarkdown), councilDir, cfg)
	if err != nil {
		return nil, fmt.Errorf("council review: %w", err)
	}

	review.ReviewedAt = time.Now()
	switch result.OverallVerdict {
	case councilflow.VerdictFail:
		review.Status = state.ReviewFail
		review.Summary = "Execution plan has blocking council findings."
		review.BlockingFindings = append(review.BlockingFindings, executionPlanCouncilFindings(result)...)
	case councilflow.VerdictWarn:
		review.Summary = "Execution plan passed council review with warnings."
		review.Warnings = append(review.Warnings, executionPlanCouncilWarnings(result)...)
	default:
		review.Summary = "Execution plan passed council review."
	}
	if err := e.RunDir.WriteExecutionPlanReview(review); err != nil {
		return nil, fmt.Errorf("write execution plan review after council: %w", err)
	}

	_ = e.RunDir.AppendEvent(state.Event{
		Timestamp: review.ReviewedAt,
		Type:      "execution_plan_council_reviewed",
		RunID:     review.RunID,
		Detail:    fmt.Sprintf("verdict=%s rounds=%d", result.OverallVerdict, len(result.Rounds)),
	})
	return result, nil
}

func executionPlanCouncilPersonas() []councilflow.Persona {
	return []councilflow.Persona{
		{PersonaID: "plan-scope", DisplayName: "Plan Scope Reviewer", Type: councilflow.PersonaCustom, Perspective: "Reviews execution-plan scope completeness and requirement mapping.", Instructions: "Review the execution plan ONLY for scope coverage, requirement completeness, and missing implementation detail. Do NOT flag runtime verifier mechanics.", Backend: "claude", ModelPref: "opus"},
		{PersonaID: "plan-dependency", DisplayName: "Plan Dependency Reviewer", Type: councilflow.PersonaCustom, Perspective: "Reviews dependency ordering, wave assignment, and conflict serialization.", Instructions: "Review the execution plan ONLY for dependency correctness, wave ordering, and shared-file conflicts. Do NOT flag stylistic issues.", Backend: "codex", ModelPref: "gpt-5.4"},
		{PersonaID: "plan-feasibility", DisplayName: "Plan Feasibility Reviewer", Type: councilflow.PersonaCustom, Perspective: "Reviews whether slices are actionable for implementation agents.", Instructions: "Review the execution plan ONLY for actionable implementation detail, realistic file touch sets, and feasible validation checks.", Backend: "claude", ModelPref: "opus"},
		{PersonaID: "plan-completeness", DisplayName: "Plan Completeness Reviewer", Type: councilflow.PersonaCustom, Perspective: "Reviews whether supporting artifacts are present for downstream implementation.", Instructions: "Review the execution plan ONLY for missing exploration context, missing validation checks, and missing test guidance. Do NOT restate deterministic findings already captured by structural review.", Backend: "codex", ModelPref: "gpt-5.4"},
	}
}

func executionPlanCouncilContext(e *Engine) string {
	var b strings.Builder
	if techSpec, err := e.RunDir.ReadTechnicalSpec(); err == nil {
		b.WriteString("## Technical Specification\n\n")
		b.Write(techSpec)
		b.WriteString("\n\n")
	}
	if exploration, err := os.ReadFile(e.RunDir.Path("exploration.json")); err == nil {
		b.WriteString("## Exploration Result\n\n```json\n")
		b.Write(exploration)
		b.WriteString("\n```\n")
	}
	return b.String()
}

func executionPlanCouncilFindings(result *councilflow.CouncilResult) []state.ReviewFinding {
	if result == nil || len(result.Rounds) == 0 {
		return nil
	}
	latest := result.Rounds[len(result.Rounds)-1]
	findings := make([]state.ReviewFinding, 0)
	for i := range latest.Reviews {
		review := &latest.Reviews[i]
		for j := range review.Findings {
			finding := review.Findings[j]
			summary := finding.Description
			if summary == "" {
				summary = review.Recommendation
			}
			findings = append(findings, state.ReviewFinding{
				FindingID:       finding.FindingID,
				Severity:        councilSeverityToState(finding.Severity),
				Category:        finding.Category,
				Summary:         summary,
				SuggestedRepair: firstNonEmpty(finding.Fix, finding.Recommendation),
			})
		}
	}
	return findings
}

func executionPlanCouncilWarnings(result *councilflow.CouncilResult) []state.ReviewWarning {
	if result == nil || len(result.Rounds) == 0 {
		return nil
	}
	latest := result.Rounds[len(result.Rounds)-1]
	warnings := make([]state.ReviewWarning, 0)
	for i := range latest.Reviews {
		review := &latest.Reviews[i]
		for j := range review.Findings {
			finding := review.Findings[j]
			summary := finding.Description
			if summary == "" {
				summary = review.Recommendation
			}
			warnings = append(warnings, state.ReviewWarning{
				WarningID: finding.FindingID,
				Summary:   summary,
			})
		}
	}
	return warnings
}

func councilSeverityToState(severity string) string {
	switch strings.ToLower(severity) {
	case "critical":
		return "critical"
	case "significant":
		return "high"
	default:
		return "low"
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// ApproveExecutionPlan records explicit approval for the reviewed execution plan.
func (e *Engine) ApproveExecutionPlan(ctx context.Context, approvedBy string) (*state.ArtifactApproval, error) {
	_ = ctx

	review, err := e.RunDir.ReadExecutionPlanReview()
	if err != nil {
		return nil, fmt.Errorf("read execution plan review: %w", err)
	}
	if review.Status != state.ReviewPass {
		return nil, fmt.Errorf("execution plan review is not passing")
	}

	plan, planHash, err := e.readExecutionPlanWithHash()
	if err != nil {
		return nil, err
	}
	if review.ExecutionPlanHash != planHash {
		return nil, fmt.Errorf("execution plan review hash does not match current artifact")
	}
	plan.Status = state.ArtifactApproved
	if err := e.RunDir.WriteExecutionPlan(plan); err != nil {
		return nil, fmt.Errorf("write approved execution plan: %w", err)
	}
	if err := e.RunDir.WriteExecutionPlanMarkdown(renderExecutionPlanMarkdown(plan)); err != nil {
		return nil, fmt.Errorf("write approved execution plan markdown: %w", err)
	}

	data, err := os.ReadFile(e.RunDir.ExecutionPlan())
	if err != nil {
		return nil, fmt.Errorf("read approved execution plan: %w", err)
	}

	approval := &state.ArtifactApproval{
		SchemaVersion: "0.1",
		RunID:         plan.RunID,
		ArtifactType:  "execution_plan_approval",
		ArtifactPath:  "execution-plan.json",
		ArtifactHash:  sha256Prefix + state.SHA256Bytes(data),
		Status:        state.ArtifactApproved,
		ApprovedBy:    approvedBy,
		ApprovedAt:    time.Now(),
	}
	if err := e.RunDir.WriteExecutionPlanApproval(approval); err != nil {
		return nil, fmt.Errorf("write execution plan approval: %w", err)
	}

	_ = e.RunDir.AppendEvent(state.Event{
		Timestamp: approval.ApprovedAt,
		Type:      "execution_plan_approved",
		RunID:     approval.RunID,
		Detail:    approval.ApprovedBy,
	})

	return approval, nil
}

// validateSliceGraph enforces graph-level invariants for slice IDs and dependencies:
// duplicate IDs, case-colliding IDs, unknown depends_on references, self-dependencies,
// same-wave dependencies, and cycles.
func validateSliceGraph(slices []state.ExecutionSlice, review *state.ExecutionPlanReview) {
	idSet := validateSliceIDs(slices, review)
	waveBySliceID := collectWaveIDs(slices)
	adj := validateSliceDeps(slices, idSet, waveBySliceID, review)
	detectSliceCycles(slices, adj, review)
}

func collectWaveIDs(slices []state.ExecutionSlice) map[string]string {
	waveBySliceID := make(map[string]string, len(slices))
	for i := range slices {
		if slices[i].SliceID == "" {
			continue
		}
		waveBySliceID[slices[i].SliceID] = slices[i].WaveID
	}
	return waveBySliceID
}

func validateSliceIDs(slices []state.ExecutionSlice, review *state.ExecutionPlanReview) map[string]bool {
	idSet := make(map[string]bool, len(slices))
	lowerSet := make(map[string]string, len(slices)) // lowercase → original

	for i := range slices {
		id := slices[i].SliceID
		if id == "" {
			continue // caught by reviewSlice field-presence check
		}
		if idSet[id] {
			review.Status = state.ReviewFail
			review.BlockingFindings = append(review.BlockingFindings, state.ReviewFinding{
				FindingID:       fmt.Sprintf(graphFindingFmtID, len(review.BlockingFindings)+1),
				Severity:        "high",
				Category:        "duplicate_slice_id",
				SliceID:         id,
				Summary:         fmt.Sprintf("duplicate slice_id %q", id),
				SuggestedRepair: "Each slice must have a unique slice_id.",
			})
		}
		idSet[id] = true

		lower := strings.ToLower(id)
		if existing, collision := lowerSet[lower]; collision && existing != id {
			review.Status = state.ReviewFail
			review.BlockingFindings = append(review.BlockingFindings, state.ReviewFinding{
				FindingID:       fmt.Sprintf(graphFindingFmtID, len(review.BlockingFindings)+1),
				Severity:        "high",
				Category:        "case_colliding_slice_id",
				SliceID:         id,
				Summary:         fmt.Sprintf("slice_id %q case-collides with %q", id, existing),
				SuggestedRepair: "Slice IDs must be unique even when compared case-insensitively.",
			})
		}
		lowerSet[lower] = id
	}
	return idSet
}

func validateSliceDeps(slices []state.ExecutionSlice, idSet map[string]bool, waveBySliceID map[string]string, review *state.ExecutionPlanReview) map[string][]string {
	adj := make(map[string][]string, len(slices))
	for i := range slices {
		id := slices[i].SliceID
		for _, dep := range slices[i].DependsOn {
			if dep == id {
				review.Status = state.ReviewFail
				review.BlockingFindings = append(review.BlockingFindings, state.ReviewFinding{
					FindingID:       fmt.Sprintf(graphFindingFmtID, len(review.BlockingFindings)+1),
					Severity:        "high",
					Category:        "self_dependency",
					SliceID:         id,
					Summary:         fmt.Sprintf("slice %q depends on itself", id),
					SuggestedRepair: "Remove the self-referencing dependency.",
				})
				continue
			}
			if !idSet[dep] {
				review.Status = state.ReviewFail
				review.BlockingFindings = append(review.BlockingFindings, state.ReviewFinding{
					FindingID:       fmt.Sprintf(graphFindingFmtID, len(review.BlockingFindings)+1),
					Severity:        "high",
					Category:        "unknown_dependency",
					SliceID:         id,
					Summary:         fmt.Sprintf("slice %q depends on unknown slice %q", id, dep),
					SuggestedRepair: "All depends_on entries must reference existing slice IDs.",
				})
				continue
			}
			if wave := waveBySliceID[id]; wave != "" && wave == waveBySliceID[dep] {
				review.Status = state.ReviewFail
				review.BlockingFindings = append(review.BlockingFindings, state.ReviewFinding{
					FindingID:       fmt.Sprintf(graphFindingFmtID, len(review.BlockingFindings)+1),
					Severity:        "high",
					Category:        "intra_wave_dependency",
					SliceID:         id,
					Summary:         fmt.Sprintf("slice %q depends on %q in the same wave %q", id, dep, wave),
					SuggestedRepair: "Move one slice to a later wave or remove the dependency edge.",
				})
			}
			adj[id] = append(adj[id], dep)
		}
	}
	return adj
}

func detectSliceCycles(slices []state.ExecutionSlice, adj map[string][]string, review *state.ExecutionPlanReview) {
	cycleNode, found := findCycleInGraph(slices, adj)
	if !found {
		return
	}
	review.Status = state.ReviewFail
	review.BlockingFindings = append(review.BlockingFindings, state.ReviewFinding{
		FindingID:       fmt.Sprintf(graphFindingFmtID, len(review.BlockingFindings)+1),
		Severity:        "high",
		Category:        "dependency_cycle",
		SliceID:         cycleNode,
		Summary:         fmt.Sprintf("dependency cycle detected involving slice %q", cycleNode),
		SuggestedRepair: "Break the dependency cycle so the graph is a DAG.",
	})
}

func validateSliceWaves(slices []state.ExecutionSlice, review *state.ExecutionPlanReview) {
	detectIntraWaveConflicts(slices, review)
	review.SharedFileRegistry = buildSharedFileRegistry(slices)
}

func detectIntraWaveConflicts(slices []state.ExecutionSlice, review *state.ExecutionPlanReview) {
	for i := 0; i < len(slices); i++ {
		left := &slices[i]
		if left.WaveID == "" {
			continue
		}
		for j := i + 1; j < len(slices); j++ {
			right := &slices[j]
			if left.WaveID != right.WaveID {
				continue
			}
			if conflictPath, ok := compiler.ConflictPath(conflictPathsForSlice(left), conflictPathsForSlice(right)); ok {
				review.Status = state.ReviewFail
				review.BlockingFindings = append(review.BlockingFindings, state.ReviewFinding{
					FindingID:       fmt.Sprintf(graphFindingFmtID, len(review.BlockingFindings)+1),
					Severity:        "high",
					Category:        "intra_wave_file_conflict",
					SliceID:         left.SliceID,
					Summary:         fmt.Sprintf("slices %q and %q share conflict path %q inside wave %q", left.SliceID, right.SliceID, conflictPath, left.WaveID),
					SuggestedRepair: "Move one slice to a later wave or narrow the touched-file scope.",
				})
			}
		}
	}
}

type sharedRegistryKey struct {
	file  string
	wave1 string
	wave2 string
}

type sharedRegistryEntry struct {
	wave1Tasks map[string]struct{}
	wave2Tasks map[string]struct{}
}

func buildSharedFileRegistry(slices []state.ExecutionSlice) []state.SharedFileEntry {
	waveOrder := sharedWaveOrder(slices)
	registry := make(map[sharedRegistryKey]*sharedRegistryEntry)
	for i := 0; i < len(slices); i++ {
		left := &slices[i]
		if left.WaveID == "" {
			continue
		}
		for j := i + 1; j < len(slices); j++ {
			registerSharedFilePair(registry, waveOrder, left, &slices[j])
		}
	}
	return buildSharedEntries(registry, waveOrder)
}

func sharedWaveOrder(slices []state.ExecutionSlice) map[string]int {
	waveOrder := make(map[string]int, len(slices))
	nextWaveOrder := 0
	for i := range slices {
		waveID := slices[i].WaveID
		if waveID == "" {
			continue
		}
		if _, ok := waveOrder[waveID]; ok {
			continue
		}
		waveOrder[waveID] = nextWaveOrder
		nextWaveOrder++
	}
	return waveOrder
}

func registerSharedFilePair(registry map[sharedRegistryKey]*sharedRegistryEntry, waveOrder map[string]int, left, right *state.ExecutionSlice) {
	if right.WaveID == "" || left.WaveID == right.WaveID {
		return
	}
	conflictPath, ok := compiler.ConflictPath(conflictPathsForSlice(left), conflictPathsForSlice(right))
	if !ok {
		return
	}
	key, task1, task2 := orderedSharedPair(waveOrder, conflictPath, left, right)
	entry := registry[key]
	if entry == nil {
		entry = &sharedRegistryEntry{
			wave1Tasks: make(map[string]struct{}),
			wave2Tasks: make(map[string]struct{}),
		}
		registry[key] = entry
	}
	if task1 != "" {
		entry.wave1Tasks[task1] = struct{}{}
	}
	if task2 != "" {
		entry.wave2Tasks[task2] = struct{}{}
	}
}

func orderedSharedPair(waveOrder map[string]int, conflictPath string, left, right *state.ExecutionSlice) (key sharedRegistryKey, task1, task2 string) {
	wave1, wave2 := left.WaveID, right.WaveID
	task1, task2 = left.SliceID, right.SliceID
	if waveOrder[wave1] > waveOrder[wave2] {
		wave1, wave2 = wave2, wave1
		task1, task2 = task2, task1
	}
	key = sharedRegistryKey{file: conflictPath, wave1: wave1, wave2: wave2}
	return key, task1, task2
}

func buildSharedEntries(registry map[sharedRegistryKey]*sharedRegistryEntry, waveOrder map[string]int) []state.SharedFileEntry {
	keys := make([]sharedRegistryKey, 0, len(registry))
	for key := range registry {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].file != keys[j].file {
			return keys[i].file < keys[j].file
		}
		if keys[i].wave1 != keys[j].wave1 {
			return waveOrder[keys[i].wave1] < waveOrder[keys[j].wave1]
		}
		return waveOrder[keys[i].wave2] < waveOrder[keys[j].wave2]
	})
	shared := make([]state.SharedFileEntry, 0, len(keys))
	for i := range keys {
		entry := registry[keys[i]]
		shared = append(shared, state.SharedFileEntry{
			File:       keys[i].file,
			Wave1Tasks: sortedSliceIDs(entry.wave1Tasks),
			Wave2Tasks: sortedSliceIDs(entry.wave2Tasks),
			Mitigation: sharedFileMitigationText,
		})
	}
	return shared
}

func conflictPathsForSlice(slice *state.ExecutionSlice) []string {
	if slice == nil {
		return nil
	}
	if len(slice.FilesLikelyTouched) > 0 {
		return append([]string(nil), slice.FilesLikelyTouched...)
	}
	return append([]string(nil), slice.OwnedPaths...)
}

func sortedSliceIDs(set map[string]struct{}) []string {
	if len(set) == 0 {
		return nil
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// findCycleInGraph uses DFS three-color marking to find a cycle in the slice dependency graph.
// Returns the node involved in the cycle and true if found, or ("", false) if acyclic.
func findCycleInGraph(slices []state.ExecutionSlice, adj map[string][]string) (string, bool) {
	cs := &cycleSearcher{
		color: make(map[string]int, len(slices)),
		adj:   adj,
	}
	for i := range slices {
		id := slices[i].SliceID
		if id != "" && cs.color[id] == colorWhite {
			if cs.dfs(id) {
				return cs.cycleNode, true
			}
		}
	}
	return "", false
}

var (
	validRisks = map[string]bool{"low": true, "medium": true, "high": true}
	validSizes = map[string]bool{"small": true, "medium": true, "large": true}
)

// DFS three-color constants for cycle detection.
const (
	colorWhite = 0 // unvisited
	colorGray  = 1 // in current path
	colorBlack = 2 // fully explored
)

// cycleSearcher holds state for DFS-based cycle detection.
type cycleSearcher struct {
	color     map[string]int
	adj       map[string][]string
	cycleNode string
}

func (cs *cycleSearcher) dfs(node string) bool {
	cs.color[node] = colorGray
	for _, dep := range cs.adj[node] {
		switch cs.color[dep] {
		case colorGray:
			cs.cycleNode = dep
			return true
		case colorWhite:
			if cs.dfs(dep) {
				return true
			}
		default:
			// colorBlack — already fully explored, skip.
		}
	}
	cs.color[node] = colorBlack
	return false
}

func isValidRisk(v string) bool { return validRisks[v] }
func isValidSize(v string) bool { return validSizes[v] }

func reviewSlice(slice *state.ExecutionSlice, review *state.ExecutionPlanReview) {
	fields := []struct {
		ok       bool
		category string
		summary  string
	}{
		{ok: slice.SliceID != "", category: "missing_slice_id", summary: "slice is missing slice_id"},
		{ok: slice.Title != "", category: "missing_title", summary: "slice is missing title"},
		{ok: slice.Goal != "", category: "missing_goal", summary: "slice is missing goal"},
		{ok: len(slice.RequirementIDs) > 0, category: "missing_requirement_ids", summary: "slice is missing requirement IDs"},
		{ok: len(slice.OwnedPaths) > 0, category: "missing_owned_paths", summary: "slice is missing owned paths"},
		{ok: len(slice.AcceptanceChecks) > 0, category: "missing_acceptance_checks", summary: "slice is missing acceptance checks"},
		{ok: isValidRisk(slice.Risk), category: "invalid_risk", summary: fmt.Sprintf("slice risk %q is not valid (want low, medium, or high)", slice.Risk)},
		{ok: isValidSize(slice.Size), category: "invalid_size", summary: fmt.Sprintf("slice size %q is not valid (want small, medium, or large)", slice.Size)},
	}

	for _, field := range fields {
		if field.ok {
			continue
		}
		review.Status = state.ReviewFail
		review.BlockingFindings = append(review.BlockingFindings, state.ReviewFinding{
			FindingID:       fmt.Sprintf("epr-%03d", len(review.BlockingFindings)+1),
			Severity:        "high",
			Category:        field.category,
			SliceID:         slice.SliceID,
			Summary:         field.summary,
			RequirementIDs:  append([]string(nil), slice.RequirementIDs...),
			SuggestedRepair: "Populate the missing execution plan field before approval.",
		})
	}

	if len(slice.RequirementIDs) > 4 {
		review.Warnings = append(review.Warnings, state.ReviewWarning{
			WarningID: fmt.Sprintf(waveWarningIDFmt, len(review.Warnings)+1),
			SliceID:   slice.SliceID,
			Summary:   "slice covers more than four requirements; consider splitting if execution drifts",
		})
	}
	if len(slice.FilesLikelyTouched) == 0 {
		review.Warnings = append(review.Warnings, state.ReviewWarning{
			WarningID: fmt.Sprintf(waveWarningIDFmt, len(review.Warnings)+1),
			SliceID:   slice.SliceID,
			Summary:   "slice is missing likely file touch points",
		})
	}
	if len(slice.ValidationChecks) == 0 {
		review.Warnings = append(review.Warnings, state.ReviewWarning{
			WarningID: fmt.Sprintf(waveWarningIDFmt, len(review.Warnings)+1),
			SliceID:   slice.SliceID,
			Summary:   fmt.Sprintf("slice %q has no validation checks — consider adding files_exist or content_check assertions.", slice.SliceID),
		})
	}
}

// validateRequirementCoverage checks that every requirement in the run artifact
// is covered by at least one execution slice. Dropped requirements are blocking
// findings; requirements covered by multiple slices produce a warning.
func validateRequirementCoverage(artifact *state.RunArtifact, slices []state.ExecutionSlice, review *state.ExecutionPlanReview) {
	if len(artifact.Requirements) == 0 {
		return
	}

	covered := buildCoverageMap(slices)
	flagUncoveredRequirements(artifact, covered, review)
	warnMultiSliceCoverage(covered, review)
}

// buildCoverageMap builds a map of requirement ID to the set of covering slice IDs.
func buildCoverageMap(slices []state.ExecutionSlice) map[string]map[string]struct{} {
	covered := make(map[string]map[string]struct{}, len(slices))
	for i := range slices {
		for _, reqID := range slices[i].RequirementIDs {
			if covered[reqID] == nil {
				covered[reqID] = make(map[string]struct{})
			}
			covered[reqID][slices[i].SliceID] = struct{}{}
		}
	}
	return covered
}

// flagUncoveredRequirements adds blocking findings for requirements not covered by any slice.
func flagUncoveredRequirements(artifact *state.RunArtifact, covered map[string]map[string]struct{}, review *state.ExecutionPlanReview) {
	for i := range artifact.Requirements {
		reqID := artifact.Requirements[i].ID
		if len(covered[reqID]) == 0 {
			review.Status = state.ReviewFail
			review.BlockingFindings = append(review.BlockingFindings, state.ReviewFinding{
				FindingID:       fmt.Sprintf("epr-cov-%03d", len(review.BlockingFindings)+1),
				Severity:        "high",
				Category:        "uncovered_requirement",
				Summary:         fmt.Sprintf("requirement %s is not covered by any execution slice", reqID),
				RequirementIDs:  []string{reqID},
				SuggestedRepair: "Add the requirement to an existing slice or create a new slice for it.",
			})
		}
	}
}

// warnMultiSliceCoverage adds warnings for requirements covered by more than one slice.
func warnMultiSliceCoverage(covered map[string]map[string]struct{}, review *state.ExecutionPlanReview) {
	reqIDs := make([]string, 0, len(covered))
	for reqID := range covered {
		reqIDs = append(reqIDs, reqID)
	}
	sort.Strings(reqIDs)
	for _, reqID := range reqIDs {
		sliceSet := covered[reqID]
		if len(sliceSet) <= 1 {
			continue
		}
		sliceIDs := make([]string, 0, len(sliceSet))
		for sid := range sliceSet {
			sliceIDs = append(sliceIDs, sid)
		}
		sort.Strings(sliceIDs)
		review.Warnings = append(review.Warnings, state.ReviewWarning{
			WarningID: fmt.Sprintf(waveWarningIDFmt, len(review.Warnings)+1),
			Summary:   fmt.Sprintf("requirement %s is covered by %d slices (%s); verify this is intentional", reqID, len(sliceIDs), strings.Join(sliceIDs, ", ")),
		})
	}
}

func renderExecutionPlanMarkdown(plan *state.ExecutionPlan) []byte {
	var buf strings.Builder
	buf.WriteString("# Execution Plan\n\n")
	buf.WriteString("- Run ID: ")
	buf.WriteString(plan.RunID)
	buf.WriteString("\n")
	buf.WriteString("- Source technical spec hash: ")
	buf.WriteString(plan.SourceTechnicalSpecHash)
	buf.WriteString("\n")
	buf.WriteString("- Generated at: ")
	buf.WriteString(plan.GeneratedAt.Format(time.RFC3339))
	buf.WriteString("\n")
	buf.WriteString("- Status: ")
	buf.WriteString(string(plan.Status))
	buf.WriteString("\n\n")

	for i := range plan.Slices {
		slice := &plan.Slices[i]
		buf.WriteString("## ")
		buf.WriteString(slice.SliceID)
		buf.WriteString(": ")
		buf.WriteString(slice.Title)
		buf.WriteString("\n\n")
		buf.WriteString("- Goal: ")
		buf.WriteString(slice.Goal)
		buf.WriteString("\n")
		buf.WriteString("- Requirement IDs: ")
		buf.WriteString(strings.Join(slice.RequirementIDs, ", "))
		buf.WriteString("\n")
		buf.WriteString("- Depends on: ")
		buf.WriteString(joinOrNone(slice.DependsOn))
		buf.WriteString("\n")
		buf.WriteString("- Files likely touched: ")
		buf.WriteString(joinOrNone(slice.FilesLikelyTouched))
		buf.WriteString("\n")
		buf.WriteString("- Owned paths: ")
		buf.WriteString(joinOrNone(slice.OwnedPaths))
		buf.WriteString("\n")
		buf.WriteString("- Acceptance checks: ")
		buf.WriteString(joinOrNone(slice.AcceptanceChecks))
		buf.WriteString("\n")
		buf.WriteString("- Risk: ")
		buf.WriteString(slice.Risk)
		buf.WriteString("\n")
		buf.WriteString("- Size: ")
		buf.WriteString(slice.Size)
		buf.WriteString("\n")
		if slice.Notes != "" {
			buf.WriteString("- Notes: ")
			buf.WriteString(slice.Notes)
			buf.WriteString("\n")
		}
		buf.WriteString("\n")
	}

	return []byte(buf.String())
}

func readArtifactHash(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return sha256Prefix + state.SHA256Bytes(data), nil
}

func (e *Engine) readApprovedTechnicalSpecHash() (string, error) {
	approval, err := e.RunDir.ReadTechnicalSpecApproval()
	if err != nil {
		return "", fmt.Errorf("read technical spec approval: %w", err)
	}
	if approval.Status != state.ArtifactApproved {
		return "", fmt.Errorf("technical spec approval is not approved")
	}

	hash, err := readArtifactHash(e.RunDir.TechnicalSpec())
	if err != nil {
		return "", fmt.Errorf("read technical spec: %w", err)
	}
	if approval.ArtifactHash != "" && approval.ArtifactHash != hash {
		return "", fmt.Errorf("technical spec approval hash does not match current artifact")
	}

	return hash, nil
}

func (e *Engine) readExecutionPlanWithHash() (*state.ExecutionPlan, string, error) {
	plan, err := e.RunDir.ReadExecutionPlan()
	if err != nil {
		return nil, "", fmt.Errorf("read execution plan: %w", err)
	}
	data, err := os.ReadFile(e.RunDir.ExecutionPlan())
	if err != nil {
		return nil, "", fmt.Errorf("read execution plan: %w", err)
	}
	return plan, sha256Prefix + state.SHA256Bytes(data), nil
}

func (e *Engine) readApprovedExecutionPlan() (*state.ExecutionPlan, error) {
	approval, err := e.RunDir.ReadExecutionPlanApproval()
	if err != nil {
		return nil, fmt.Errorf("read execution plan approval: %w", err)
	}
	if approval.Status != state.ArtifactApproved {
		return nil, fmt.Errorf("execution plan approval is not approved")
	}

	plan, planHash, err := e.readExecutionPlanWithHash()
	if err != nil {
		return nil, err
	}
	if plan.Status != state.ArtifactApproved {
		return nil, fmt.Errorf("execution plan status is %s, want approved", plan.Status)
	}
	if approval.ArtifactHash != "" && approval.ArtifactHash != planHash {
		return nil, fmt.Errorf("execution plan approval hash does not match current artifact")
	}

	return plan, nil
}

func sliceIDFromTaskID(taskID string) string {
	return strings.TrimPrefix(taskID, "task-")
}

func sliceIDsFromTaskIDs(taskIDs []string) []string {
	sliceIDs := make([]string, 0, len(taskIDs))
	for _, taskID := range taskIDs {
		sliceIDs = append(sliceIDs, sliceIDFromTaskID(taskID))
	}
	return sliceIDs
}

func goalFromTask(task *state.Task) string {
	if len(task.RequirementIDs) == 0 {
		return task.Title
	}
	return fmt.Sprintf("Deliver the implementation slice covering %s.", strings.Join(task.RequirementIDs, ", "))
}

func acceptanceChecksForTask(artifact *state.RunArtifact, task *state.Task) []string {
	checks := make([]string, 0, 2)
	if artifact != nil && artifact.QualityGate != nil && artifact.QualityGate.Command != "" {
		checks = append(checks, artifact.QualityGate.Command)
	}
	if len(task.RequiredEvidence) > 0 {
		checks = append(checks, "fabrikk verify <run-id> <task-id>")
	}
	return checks
}

func executionSliceSize(requirementIDs []string) string {
	switch n := len(requirementIDs); {
	case n <= 1:
		return "small"
	case n <= 3:
		return "medium"
	default:
		return "large"
	}
}

func joinOrNone(items []string) string {
	if len(items) == 0 {
		return "none"
	}
	return strings.Join(items, ", ")
}
