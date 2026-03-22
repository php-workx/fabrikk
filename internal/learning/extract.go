package learning

// ExtractedFinding holds fields extracted from a council or verifier finding.
// The caller (CLI layer) maps from councilflow.Finding or state.Finding to this.
type ExtractedFinding struct {
	FindingID      string
	Severity       string // critical, significant, minor / critical, high, medium, low
	Category       string
	Description    string
	Recommendation string
	RunID          string
	SourcePaths    []string
	Accepted       bool // true if judge applied it (not rejected)
}

// ExtractedRejection holds a rejected finding with dismissal rationale.
type ExtractedRejection struct {
	FindingID          string
	Description        string
	RejectionReason    string
	DismissalRationale string
	PersonaID          string
}

// FromFinding creates a Learning from an extracted council/verifier finding.
func FromFinding(f ExtractedFinding) *Learning {
	category := CategoryAntiPattern
	if f.Severity == "minor" || f.Severity == "low" || f.Severity == "medium" {
		category = CategoryCodebase
	}

	content := f.Description
	if f.Recommendation != "" {
		content += "\n\nRecommendation: " + f.Recommendation
	}

	summary := f.Description
	if len(summary) > 100 {
		summary = summary[:100] + "..."
	}

	confidence := 0.6
	if f.Accepted {
		confidence = 0.8
	}

	return &Learning{
		Tags:          []string{"finding:" + f.FindingID, f.Category},
		Category:      category,
		Content:       content,
		Summary:       summary,
		SourceRun:     f.RunID,
		SourcePaths:   f.SourcePaths,
		Source:        "council",
		SourceFinding: f.FindingID,
		Confidence:    confidence,
	}
}

// FromRejection creates a pattern Learning from a judge rejection.
// Returns nil if the rejection has no DismissalRationale (bare rejections aren't useful).
func FromRejection(r ExtractedRejection) *Learning {
	if r.DismissalRationale == "" {
		return nil
	}

	content := "Reviewer flagged: " + r.Description + "\nJudge rejected: " + r.RejectionReason
	content += "\nRationale: " + r.DismissalRationale

	summary := "Rejected: " + r.Description
	if len(summary) > 100 {
		summary = summary[:100] + "..."
	}

	return &Learning{
		Tags:          []string{"rejected:" + r.FindingID, r.PersonaID},
		Category:      CategoryPattern,
		Content:       content,
		Summary:       summary,
		Source:        "rejection",
		SourceFinding: r.FindingID,
		Confidence:    0.7,
	}
}

// FromVerifierFailure creates a Learning from a verifier blocking finding.
// dedupeKey is used for the dedup tag; falls back to findingID if empty.
// extraTags are additional tags (e.g., task-derived) for discoverability.
func FromVerifierFailure(findingID, severity, category, summary, taskID, runID, dedupeKey string, sourcePaths, extraTags []string) *Learning {
	cat := CategoryAntiPattern
	switch category {
	case "quality_gate":
		cat = CategoryTooling
	case "missing_evidence", "requirement_linkage":
		cat = CategoryProcess
	}

	dedupTag := findingID
	if dedupeKey != "" {
		dedupTag = dedupeKey
	}

	tags := make([]string, 0, 2+len(extraTags))
	tags = append(tags, "verifier:"+dedupTag, category)
	tags = append(tags, extraTags...)

	return &Learning{
		Tags:          tags,
		Category:      cat,
		Content:       summary,
		Summary:       summary,
		SourceTask:    taskID,
		SourceRun:     runID,
		SourcePaths:   sourcePaths,
		Source:        "verifier",
		SourceFinding: findingID,
		Confidence:    0.9,
	}
}
