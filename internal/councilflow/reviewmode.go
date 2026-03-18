package councilflow

// ReviewMode controls the tone, scope, and strictness of the review pipeline.
type ReviewMode string

const (
	// ReviewMVP focuses on feasibility and implementation-blocking issues only.
	// Skip future-phase concerns, polish, and nice-to-haves.
	ReviewMVP ReviewMode = "mvp"

	// ReviewStandard is the default balanced review mode.
	// Apply valid findings, reject future-phase issues, cover all severities.
	ReviewStandard ReviewMode = "standard"

	// ReviewProduction is thorough and adversarial.
	// Flag everything defensible, low threshold for acceptance.
	ReviewProduction ReviewMode = "production"
)

// ValidReviewModes is the set of accepted review mode values.
var ValidReviewModes = map[ReviewMode]bool{
	ReviewMVP:        true,
	ReviewStandard:   true,
	ReviewProduction: true,
}

// ReviewModeDirective returns the prompt instructions for reviewers.
func ReviewModeDirective(mode ReviewMode) string {
	switch mode {
	case ReviewMVP:
		return mvpReviewerDirective
	case ReviewProduction:
		return productionReviewerDirective
	default:
		return standardReviewerDirective
	}
}

// JudgeModeDirective returns the prompt instructions for the judge.
func JudgeModeDirective(mode ReviewMode) string {
	switch mode {
	case ReviewMVP:
		return mvpJudgeDirective
	case ReviewProduction:
		return productionJudgeDirective
	default:
		return standardJudgeDirective
	}
}

// PersonaModeDirective returns the prompt instructions for dynamic persona generation.
func PersonaModeDirective(mode ReviewMode) string {
	switch mode {
	case ReviewMVP:
		return mvpPersonaDirective
	case ReviewProduction:
		return productionPersonaDirective
	default:
		return standardPersonaDirective
	}
}

const mvpReviewerDirective = `## Review Mode: MVP

This is an MVP spec. The goal is to validate feasibility and identify implementation blockers — NOT to achieve perfection.

Rules for this review:
- ONLY flag issues that would block implementation or cause the MVP to fail
- Do NOT suggest improvements, polish, or future-phase features
- Do NOT flag missing edge cases unless they cause crashes or data loss
- Do NOT recommend architectural changes unless the current approach is unworkable
- Ignore: naming conventions, documentation gaps, performance optimizations, security hardening for future phases
- Focus: Can this be built? What's missing to start? What will break?

Severity guide for MVP mode:
- critical: Implementation cannot proceed without addressing this
- significant: Implementation will hit a wall within days if ignored
- minor: DO NOT USE in MVP mode — skip it entirely
`

const standardReviewerDirective = `## Review Mode: Standard

Balanced review. Identify real issues across all severity levels.

Rules for this review:
- Flag genuine issues at all severity levels
- Distinguish current-phase concerns from future-phase concerns
- Don't over-prescribe — suggest what to fix, not how to architect
- Focus on the spec as written, not what it could be
`

const productionReviewerDirective = `## Review Mode: Production

This spec is headed for production. Be thorough and adversarial.

Rules for this review:
- Flag everything defensible — err on the side of surfacing concerns
- Challenge implicit assumptions explicitly
- Check for missing error paths, edge cases, and failure modes
- Verify that every requirement is unambiguous and testable
- Flag under-specified areas even if they seem obvious
- Consider: What would break at 10x scale? Under adversarial input? After 6 months of maintenance?
`

const mvpJudgeDirective = `## Judge Mode: MVP

You are judging findings for an MVP spec. Be aggressive about rejecting non-essential findings.

- REJECT findings about future phases, polish, or nice-to-haves
- REJECT findings about security hardening unless there's an immediate exploit vector
- REJECT findings about performance unless they cause the MVP to be unusable
- REJECT "minor" severity findings — they should not have been raised in MVP mode
- APPLY only findings that would block implementation or cause the MVP to fail
- When in doubt, REJECT — the goal is to ship, not to be perfect
`

const standardJudgeDirective = `## Judge Mode: Standard

Balanced judgment. Apply valid findings, reject future-phase concerns.
`

const productionJudgeDirective = `## Judge Mode: Production

Thorough judgment. Apply everything defensible.

- APPLY findings at all severity levels if they are factually correct
- Only REJECT findings that are provably wrong or already addressed
- Require explicit counter-evidence for any rejection
- When in doubt, APPLY — the goal is completeness, not speed
`

const mvpPersonaDirective = `This is an MVP spec. Generate personas focused on feasibility and implementation risk, NOT polish or future concerns. Limit to 1-2 domain personas. Skip any persona whose primary value is long-term quality rather than short-term buildability.`

const standardPersonaDirective = `Generate personas appropriate for a standard review. Balance breadth with depth.`

const productionPersonaDirective = `This spec is headed for production. Generate personas that will challenge every assumption. Include domain experts for any area that could fail under real-world conditions. Use all 3 dynamic persona slots.`
