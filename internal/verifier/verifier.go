// Package verifier implements deterministic verification checks.
package verifier

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/php-workx/fabrikk/internal/state"
)

// Verify runs the full deterministic verification pipeline for a task (spec section 11.3).
// Order: quality gate -> evidence checks -> scope check -> requirement linkage.
func Verify(ctx context.Context, task *state.Task, report *state.CompletionReport, gate *state.QualityGate, workDir string) (*state.VerifierResult, error) {
	result := &state.VerifierResult{
		TaskID:    task.TaskID,
		AttemptID: report.AttemptID,
		Pass:      true,
	}

	// 1. Quality gate (spec section 11.3: runs first, blocks model review on failure).
	gateResult, gateErr := RunQualityGate(ctx, gate, workDir)
	if gateErr != nil {
		gateDetail := fmt.Sprintf("quality gate failed before producing a command result: %v", gateErr)
		if gateResult != nil {
			gateDetail = gateResult.Command + " exited " + fmt.Sprint(gateResult.ExitCode)
		}
		result.Pass = false
		result.BlockingFindings = append(result.BlockingFindings, state.Finding{
			FindingID: fmt.Sprintf("%s-quality-gate", task.TaskID),
			Severity:  "critical",
			Category:  "quality_gate",
			Summary:   fmt.Sprintf("Quality gate failed: %v", gateErr),
			DedupeKey: fmt.Sprintf("quality-gate:%s", task.TaskID),
		})
		result.EvidenceChecks = append(result.EvidenceChecks, state.Check{
			Name:   "quality_gate",
			Pass:   false,
			Detail: gateDetail,
		})
		return result, nil
	}
	result.EvidenceChecks = append(result.EvidenceChecks, state.Check{
		Name: "quality_gate",
		Pass: true,
	})

	// 2. Evidence checks: validate required reports and command execution.
	evidenceCheck := checkEvidence(task, report)
	result.EvidenceChecks = append(result.EvidenceChecks, evidenceCheck)
	if !evidenceCheck.Pass {
		result.Pass = false
		result.BlockingFindings = append(result.BlockingFindings, state.Finding{
			FindingID: fmt.Sprintf("%s-evidence", task.TaskID),
			Severity:  "high",
			Category:  "missing_evidence",
			Summary:   evidenceCheck.Detail,
			DedupeKey: fmt.Sprintf("evidence:%s", task.TaskID),
		})
	}

	// 3. Scope check: verify changed files are within claimed scope (spec section 8.3).
	scopeCheck := checkScope(task, report, workDir)
	result.ScopeCheck = scopeCheck
	if !scopeCheck.Pass {
		result.Pass = false
		result.BlockingFindings = append(result.BlockingFindings, state.Finding{
			FindingID: fmt.Sprintf("%s-scope", task.TaskID),
			Severity:  "high",
			Category:  "scope_violation",
			Summary:   scopeCheck.Detail,
			DedupeKey: fmt.Sprintf("scope:%s", task.TaskID),
		})
	}

	// 4. Requirement linkage: every task must link to requirement IDs (spec section 11.3).
	reqCheck := checkRequirementLinkage(task)
	result.RequirementCheck = reqCheck
	if !reqCheck.Pass {
		result.Pass = false
		result.BlockingFindings = append(result.BlockingFindings, state.Finding{
			FindingID: fmt.Sprintf("%s-req-link", task.TaskID),
			Severity:  "critical",
			Category:  "requirement_linkage",
			Summary:   reqCheck.Detail,
			DedupeKey: fmt.Sprintf("req-link:%s", task.TaskID),
		})
	}

	return result, nil
}

func checkEvidence(task *state.Task, report *state.CompletionReport) state.Check {
	if report == nil {
		return state.Check{
			Name:   "evidence",
			Pass:   false,
			Detail: "no completion report provided",
		}
	}

	// Check that required commands were executed (spec section 11.3).
	for _, cmd := range report.CommandResults {
		if cmd.Required && cmd.ExitCode != 0 {
			return state.Check{
				Name:   "evidence",
				Pass:   false,
				Detail: fmt.Sprintf("required command %q failed with exit code %d", cmd.Command, cmd.ExitCode),
			}
		}
	}

	// Check required evidence types from the task.
	for _, ev := range task.RequiredEvidence {
		if !hasEvidence(ev, report) {
			return state.Check{
				Name:   "evidence",
				Pass:   false,
				Detail: fmt.Sprintf("missing required evidence: %s", ev),
			}
		}
	}

	return state.Check{
		Name: "evidence",
		Pass: true,
	}
}

func hasEvidence(evidenceType string, report *state.CompletionReport) bool {
	switch evidenceType {
	case "quality_gate_pass":
		// Already checked above as the first step.
		return true
	case "test_pass":
		for _, cmd := range report.CommandResults {
			if strings.Contains(cmd.Command, "test") && cmd.ExitCode == 0 {
				return true
			}
		}
		return false
	case "file_exists":
		return len(report.ChangedFiles) > 0 || len(report.ArtifactsProduced) > 0
	default:
		return false
	}
}

func checkScope(task *state.Task, report *state.CompletionReport, workDir string) state.Check {
	if len(task.Scope.OwnedPaths) == 0 {
		// No scope constraints defined — pass by default in v1.
		return state.Check{
			Name: "scope",
			Pass: true,
		}
	}

	for _, changed := range report.ChangedFiles {
		if !isWithinScope(changed, task.Scope.OwnedPaths, workDir) {
			return state.Check{
				Name:   "scope",
				Pass:   false,
				Detail: fmt.Sprintf("file %s is outside claimed scope %v", changed, task.Scope.OwnedPaths),
			}
		}
	}

	return state.Check{
		Name: "scope",
		Pass: true,
	}
}

func isWithinScope(file string, ownedPaths []string, workDir string) bool {
	// Normalize to relative path.
	rel, err := filepath.Rel(workDir, file)
	if err != nil {
		rel = file
	}

	for _, scope := range ownedPaths {
		if strings.HasPrefix(rel, scope) || rel == scope {
			return true
		}
	}
	return false
}

func checkRequirementLinkage(task *state.Task) state.Check {
	if len(task.RequirementIDs) == 0 {
		return state.Check{
			Name:   "requirement_linkage",
			Pass:   false,
			Detail: "task has no linked requirement IDs",
		}
	}

	// Check that all IDs look valid.
	for _, id := range task.RequirementIDs {
		if !isValidRequirementID(id) {
			return state.Check{
				Name:   "requirement_linkage",
				Pass:   false,
				Detail: fmt.Sprintf("invalid requirement ID format: %s", id),
			}
		}
	}

	return state.Check{
		Name: "requirement_linkage",
		Pass: true,
	}
}

func isValidRequirementID(id string) bool {
	prefixes := []string{"AT-FR-", "AT-TS-", "AT-NFR-", "AT-AS-"}
	for _, p := range prefixes {
		if strings.HasPrefix(id, p) {
			return true
		}
	}
	return false
}

// VerifyFileExists checks that an expected file exists in the work directory.
func VerifyFileExists(workDir, relPath string) bool {
	_, err := os.Stat(filepath.Join(workDir, relPath))
	return err == nil
}
