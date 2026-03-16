package engine

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/runger/attest/internal/compiler"
	"github.com/runger/attest/internal/state"
)

const (
	sha256Prefix      = "sha256:"
	graphFindingFmtID = "epr-graph-%03d"
)

// DraftExecutionPlan derives a run-scoped execution plan from an approved technical spec.
func (e *Engine) DraftExecutionPlan(ctx context.Context) (*state.ExecutionPlan, error) {
	_ = ctx

	techSpecHash, err := e.readApprovedTechnicalSpecHash()
	if err != nil {
		return nil, err
	}

	artifact, err := e.RunDir.ReadArtifact()
	if err != nil {
		return nil, fmt.Errorf("read run artifact: %w", err)
	}

	compiled, err := compiler.Compile(artifact)
	if err != nil {
		return nil, fmt.Errorf("compile execution slices: %w", err)
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
		plan.Slices = append(plan.Slices, state.ExecutionSlice{
			SliceID:            sliceIDFromTaskID(task.TaskID),
			Title:              task.Title,
			Goal:               goalFromTask(task),
			RequirementIDs:     append([]string(nil), task.RequirementIDs...),
			DependsOn:          sliceIDsFromTaskIDs(task.DependsOn),
			FilesLikelyTouched: append([]string(nil), task.Scope.OwnedPaths...),
			OwnedPaths:         append([]string(nil), task.Scope.OwnedPaths...),
			AcceptanceChecks:   acceptanceChecksForTask(artifact, task),
			Risk:               task.RiskLevel,
			Size:               executionSliceSize(task.RequirementIDs),
			Notes:              fmt.Sprintf("Derived from approved technical spec and grouped requirements: %s", strings.Join(task.RequirementIDs, ", ")),
		})
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

// ReviewExecutionPlan runs deterministic structural checks over the plan artifact.
func (e *Engine) ReviewExecutionPlan(ctx context.Context) (*state.ExecutionPlanReview, error) {
	_ = ctx

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
	for i := range plan.Slices {
		reviewSlice(&plan.Slices[i], review)
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

// validateSliceGraph checks the execution plan's dependency graph for structural issues:
// duplicate IDs, case-colliding IDs, unknown depends_on references, self-dependencies, and cycles.
func validateSliceGraph(slices []state.ExecutionSlice, review *state.ExecutionPlanReview) {
	idSet := validateSliceIDs(slices, review)
	adj := validateSliceDeps(slices, idSet, review)
	detectSliceCycles(slices, adj, review)
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

func validateSliceDeps(slices []state.ExecutionSlice, idSet map[string]bool, review *state.ExecutionPlanReview) map[string][]string {
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
			adj[id] = append(adj[id], dep)
		}
	}
	return adj
}

func detectSliceCycles(slices []state.ExecutionSlice, adj map[string][]string, review *state.ExecutionPlanReview) {
	const (
		colorWhite = 0 // unvisited
		colorGray  = 1 // in current path
		colorBlack = 2 // fully explored
	)
	color := make(map[string]int, len(slices))
	var cycleNode string

	var dfs func(node string) bool
	dfs = func(node string) bool {
		color[node] = colorGray
		for _, dep := range adj[node] {
			switch color[dep] {
			case colorGray:
				cycleNode = dep
				return true
			case colorWhite:
				if dfs(dep) {
					return true
				}
			default:
				// colorBlack — already fully explored, skip.
			}
		}
		color[node] = colorBlack
		return false
	}

	for i := range slices {
		id := slices[i].SliceID
		if id != "" && color[id] == colorWhite {
			if dfs(id) {
				review.Status = state.ReviewFail
				review.BlockingFindings = append(review.BlockingFindings, state.ReviewFinding{
					FindingID:       fmt.Sprintf(graphFindingFmtID, len(review.BlockingFindings)+1),
					Severity:        "high",
					Category:        "dependency_cycle",
					SliceID:         cycleNode,
					Summary:         fmt.Sprintf("dependency cycle detected involving slice %q", cycleNode),
					SuggestedRepair: "Break the dependency cycle so the graph is a DAG.",
				})
				return // one cycle finding is sufficient
			}
		}
	}
}

var (
	validRisks = map[string]bool{"low": true, "medium": true, "high": true}
	validSizes = map[string]bool{"small": true, "medium": true, "large": true}
)

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
			WarningID: fmt.Sprintf("epr-w-%03d", len(review.Warnings)+1),
			SliceID:   slice.SliceID,
			Summary:   "slice covers more than four requirements; consider splitting if execution drifts",
		})
	}
	if len(slice.FilesLikelyTouched) == 0 {
		review.Warnings = append(review.Warnings, state.ReviewWarning{
			WarningID: fmt.Sprintf("epr-w-%03d", len(review.Warnings)+1),
			SliceID:   slice.SliceID,
			Summary:   "slice is missing likely file touch points",
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
		checks = append(checks, "attest verify <run-id> <task-id>")
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
