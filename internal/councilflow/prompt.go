package councilflow

import (
	"encoding/json"
	"fmt"
	"strings"
)

// PromptContext holds all inputs needed to construct a review prompt.
type PromptContext struct {
	Spec            string         // full technical spec markdown
	Persona         Persona        // the reviewer persona
	Round           int            // 1 or 2
	PriorFindings   []ReviewOutput // findings from previous round (empty for round 1)
	CodebaseContext string         // optional: relevant source code or structure summary
}

// BuildReviewPrompt constructs the full prompt for a single persona review.
func BuildReviewPrompt(ctx *PromptContext) string {
	var b strings.Builder

	b.WriteString("# Review Assignment\n\n")
	fmt.Fprintf(&b, "You are: **%s**\n\n", ctx.Persona.DisplayName)
	fmt.Fprintf(&b, "Perspective: %s\n\n", ctx.Persona.Perspective)

	if len(ctx.Persona.FocusSections) > 0 {
		fmt.Fprintf(&b, "Focus sections: %s\n\n", strings.Join(ctx.Persona.FocusSections, ", "))
	}

	b.WriteString("## Instructions\n\n")
	b.WriteString(ctx.Persona.Instructions)
	b.WriteString("\n\n")

	if ctx.Round > 1 && len(ctx.PriorFindings) > 0 {
		b.WriteString("## Prior Round Context\n\n")
		b.WriteString("The following findings were produced in the previous review round. ")
		b.WriteString("Do not repeat issues that have already been addressed in the updated spec. ")
		b.WriteString("Focus on new issues, issues that were inadequately addressed, or issues the previous reviewers missed.\n\n")
		for i := range ctx.PriorFindings {
			review := &ctx.PriorFindings[i]
			fmt.Fprintf(&b, "### %s (Round %d) — %s\n\n", review.PersonaID, review.Round, review.Verdict)
			data, err := json.MarshalIndent(review.Findings, "", "  ")
			if err == nil {
				b.WriteString("```json\n")
				b.Write(data)
				b.WriteString("\n```\n\n")
			}
		}
	}

	if ctx.CodebaseContext != "" {
		b.WriteString("## Codebase Context\n\n")
		b.WriteString("The following source code is relevant to this spec. ")
		b.WriteString("Use it to validate whether the spec accurately describes the existing system.\n\n")
		b.WriteString("```\n")
		b.WriteString(ctx.CodebaseContext)
		b.WriteString("\n```\n\n")
	}

	b.WriteString("## Technical Specification\n\n")
	b.WriteString("Review the following technical specification:\n\n")
	b.WriteString(ctx.Spec)
	b.WriteString("\n")

	return b.String()
}

// BuildNudgePrompt constructs the follow-up nudge prompt.
func BuildNudgePrompt(persona *Persona) string {
	return fmt.Sprintf(`Good findings so far. However, we expected a more thorough review from a %s.

Please review the spec again with fresh eyes. Pay particular attention to:
- Edge cases and boundary conditions you may have skimmed
- Implicit assumptions that are not stated explicitly
- Requirements that are under-specified or ambiguous
- Interactions between components that could produce unexpected behavior

Add any new findings to your previous output. Return the COMPLETE findings list (previous + new) as a single JSON object in the same format.`, persona.DisplayName)
}
