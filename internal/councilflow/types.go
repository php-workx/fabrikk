package councilflow

import "time"

// PersonaType distinguishes fixed personas from dynamically generated ones.
type PersonaType string

// Persona type constants.
const (
	PersonaFixed   PersonaType = "fixed"
	PersonaDynamic PersonaType = "dynamic"
)

// Verdict represents the council review outcome.
type Verdict string

// Verdict constants aligned with agentops council schema.
const (
	VerdictPass Verdict = "PASS"
	VerdictWarn Verdict = "WARN"
	VerdictFail Verdict = "FAIL"
)

// Confidence levels for review outputs.
const (
	ConfidenceHigh   = "HIGH"
	ConfidenceMedium = "MEDIUM"
	ConfidenceLow    = "LOW"
)

// Persona defines a review perspective for the council pipeline.
type Persona struct {
	PersonaID     string      `json:"persona_id"`
	DisplayName   string      `json:"display_name"`
	Type          PersonaType `json:"type"`
	Perspective   string      `json:"perspective"`
	Instructions  string      `json:"review_instructions"`
	FocusSections []string    `json:"focus_sections,omitempty"`
	Backend       string      `json:"backend"` // which CLI tool to use: "claude", "codex", "gemini"
	ModelPref     string      `json:"model_preference,omitempty"`
	GeneratedBy   string      `json:"generated_by,omitempty"`
	Rationale     string      `json:"rationale,omitempty"`
}

// Finding is a single issue identified by a reviewer.
type Finding struct {
	FindingID      string `json:"finding_id"`
	Severity       string `json:"severity"` // critical, significant, minor
	Category       string `json:"category"`
	Section        string `json:"section"`
	Location       string `json:"location"`
	Description    string `json:"description"`
	Recommendation string `json:"recommendation"`
	Fix            string `json:"fix,omitempty"`
	Why            string `json:"why,omitempty"`
	Ref            string `json:"ref,omitempty"`
}

// ReviewOutput is the structured output from a single persona review.
type ReviewOutput struct {
	PersonaID      string    `json:"persona_id"`
	Round          int       `json:"round"`
	Verdict        Verdict   `json:"verdict"`
	Confidence     string    `json:"confidence"`
	KeyInsight     string    `json:"key_insight"`
	Findings       []Finding `json:"findings"`
	Recommendation string    `json:"recommendation"`
	ReviewedAt     time.Time `json:"reviewed_at"`
	SchemaVersion  int       `json:"schema_version"`
}

// RoundResult holds all outputs from a single review round.
type RoundResult struct {
	Round     int            `json:"round"`
	Personas  []Persona      `json:"personas"`
	Reviews   []ReviewOutput `json:"reviews"`
	Consensus Verdict        `json:"consensus"`
}

// Rejection records a finding that the judge/editor chose not to apply.
type Rejection struct {
	FindingID          string `json:"finding_id"`
	PersonaID          string `json:"persona_id"`
	Severity           string `json:"severity"`
	Description        string `json:"description"`
	RejectionReason    string `json:"rejection_reason"`
	DismissalRationale string `json:"dismissal_rationale,omitempty"` // required for high-confidence dismissals (spec 11.8)
	CrossReference     string `json:"cross_reference,omitempty"`
}

// RejectionLog holds all rejections from a single round.
type RejectionLog struct {
	Round      int         `json:"round"`
	Rejections []Rejection `json:"rejections"`
}

// ComputeConsensus derives a round verdict from individual review verdicts.
// Rules: all PASS → PASS, any FAIL → FAIL, mixed → WARN.
func ComputeConsensus(reviews []ReviewOutput) Verdict {
	if len(reviews) == 0 {
		return VerdictFail
	}

	hasFail := false
	allPass := true

	for i := range reviews {
		switch reviews[i].Verdict {
		case VerdictFail:
			hasFail = true
			allPass = false
		case VerdictWarn:
			allPass = false
		case VerdictPass:
			// no change
		default:
			allPass = false
		}
	}

	if hasFail {
		return VerdictFail
	}
	if allPass {
		return VerdictPass
	}
	return VerdictWarn
}
