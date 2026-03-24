package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/php-workx/fabrikk/internal/councilflow"
	"github.com/php-workx/fabrikk/internal/learning"
	"github.com/php-workx/fabrikk/internal/state"
)

// existingFindingTags loads all finding/rejection tags from the store for batch dedup.
func existingFindingTags(store *learning.Store, prefixes ...string) map[string]bool {
	allLearnings, err := store.Query(learning.QueryOpts{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: learning store query failed: %v\n", err)
	}
	ids := make(map[string]bool)
	for i := range allLearnings {
		for _, tag := range allLearnings[i].Tags {
			for _, prefix := range prefixes {
				if strings.HasPrefix(tag, prefix) {
					ids[tag] = true
				}
			}
		}
	}
	return ids
}

// rejectedFindingIDs returns the set of finding IDs that the judge rejected.
func rejectedFindingIDs(result *councilflow.CouncilResult) map[string]bool {
	ids := make(map[string]bool)
	if len(result.Consolidations) == 0 {
		return ids
	}
	lastConsol := &result.Consolidations[len(result.Consolidations)-1]
	for i := range lastConsol.RejectionLog.Rejections {
		ids[lastConsol.RejectionLog.Rejections[i].FindingID] = true
	}
	return ids
}

// acceptedFindingIDs returns the set of finding IDs that the judge applied.
func acceptedFindingIDs(result *councilflow.CouncilResult) map[string]bool {
	ids := make(map[string]bool)
	if len(result.Consolidations) == 0 {
		return ids
	}
	lastConsol := &result.Consolidations[len(result.Consolidations)-1]
	for i := range lastConsol.AppliedEdits {
		ids[lastConsol.AppliedEdits[i].FindingID] = true
	}
	return ids
}

// extractLearningsFromCouncil extracts learnings from council review findings.
func extractLearningsFromCouncil(wd string, result *councilflow.CouncilResult, runID string) {
	if len(result.Rounds) == 0 {
		return
	}
	store := newLearningStore(wd)

	const maxExtract = 5
	extracted := 0

	lastRound := &result.Rounds[len(result.Rounds)-1]
	accepted := acceptedFindingIDs(result)
	rejected := rejectedFindingIDs(result)
	existing := existingFindingTags(store, "finding:", "rejected:")

	// Extract from findings (skip rejected — those are handled by extractRejections).
	for i := range lastRound.Reviews {
		for j := range lastRound.Reviews[i].Findings {
			if extracted >= maxExtract {
				break
			}
			f := &lastRound.Reviews[i].Findings[j]
			if existing["finding:"+f.FindingID] || rejected[f.FindingID] {
				continue
			}
			l := learning.FromFinding(learning.ExtractedFinding{
				FindingID: f.FindingID, Severity: f.Severity, Category: f.Category,
				Description: f.Description, Recommendation: f.Recommendation,
				RunID: runID, Accepted: accepted[f.FindingID],
			})
			if err := store.Add(l); err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to add learning for finding %s: %v\n", f.FindingID, err)
			} else {
				existing["finding:"+f.FindingID] = true
				extracted++
				fmt.Printf("  Extracted learning: %s (%s)\n", l.ID, l.Summary)
			}
		}
	}

	// Extract from rejections with rationale.
	extracted += extractRejections(store, result, existing, maxExtract-extracted)

	if extracted > 0 {
		fmt.Printf("Extracted %d learnings from council review.\n", extracted)
	}
}

// extractRejections extracts learnings from judge rejections that have dismissal rationale.
func extractRejections(store *learning.Store, result *councilflow.CouncilResult, existing map[string]bool, remaining int) int {
	if len(result.Consolidations) == 0 || remaining <= 0 {
		return 0
	}
	extracted := 0
	lastConsol := &result.Consolidations[len(result.Consolidations)-1]
	for i := range lastConsol.RejectionLog.Rejections {
		if extracted >= remaining {
			break
		}
		rej := &lastConsol.RejectionLog.Rejections[i]
		if existing["rejected:"+rej.FindingID] {
			continue
		}
		l := learning.FromRejection(learning.ExtractedRejection{
			FindingID: rej.FindingID, Description: rej.Description,
			RejectionReason: rej.RejectionReason, DismissalRationale: rej.DismissalRationale,
			PersonaID: rej.PersonaID,
		})
		if l != nil {
			if err := store.Add(l); err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to add rejection learning for %s: %v\n", rej.FindingID, err)
			} else {
				existing["rejected:"+rej.FindingID] = true
				extracted++
				fmt.Printf("  Extracted rejection learning: %s\n", l.ID)
			}
		}
	}
	return extracted
}

// extractLearningsFromVerifier extracts learnings from verifier blocking findings.
func extractLearningsFromVerifier(wd string, result *state.VerifierResult, task *state.Task) {
	if result.Pass || len(result.BlockingFindings) == 0 {
		return
	}
	store := newLearningStore(wd)

	const maxExtract = 5
	extracted := 0

	existing := existingFindingTags(store, "verifier:")

	for i := range result.BlockingFindings {
		if extracted >= maxExtract {
			break
		}
		f := &result.BlockingFindings[i]
		// Use DedupeKey for dedup (per spec §9.2.3), fall back to FindingID.
		dedup := f.DedupeKey
		if dedup == "" {
			dedup = f.FindingID
		}
		if existing["verifier:"+dedup] {
			continue
		}
		var sourcePaths, extraTags []string
		var taskID string
		if task != nil {
			sourcePaths = task.Scope.OwnedPaths
			taskID = task.TaskID
			extraTags = task.DeriveTags()
		}
		l := learning.FromVerifierFailure(
			f.FindingID, f.Severity, f.Category, f.Summary,
			taskID, "", dedup, sourcePaths, extraTags,
		)
		if err := store.Add(l); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to add verifier learning for %s: %v\n", f.FindingID, err)
		} else {
			existing["verifier:"+dedup] = true
			extracted++
			fmt.Printf("  Extracted verifier learning: %s (%s)\n", l.ID, l.Summary)
		}
	}
	if extracted > 0 {
		fmt.Printf("Extracted %d learnings from verification failure.\n", extracted)
	}
}
