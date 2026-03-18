package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/runger/attest/internal/councilflow"
	"github.com/runger/attest/internal/state"
)

type technicalSpecRequirement struct {
	display          string
	canonicalHeading string
	aliases          []string
}

var technicalSpecRequirements = []technicalSpecRequirement{
	{
		display:          "## 1. technical context",
		canonicalHeading: "## 1. Technical context",
		aliases: []string{
			"## 1. technical context",
			"## 1. implementation decisions",
		},
	},
	{
		display:          "## 2. architecture",
		canonicalHeading: "## 2. Architecture",
		aliases: []string{
			"## 2. architecture",
			"## 2. system architecture",
		},
	},
	{
		display:          "## 3. canonical artifacts and schemas",
		canonicalHeading: "## 3. Canonical artifacts and schemas",
		aliases: []string{
			"## 3. canonical artifacts and schemas",
			"## 3. canonical artifacts",
		},
	},
	{
		display:          "## 4. interfaces",
		canonicalHeading: "## 4. Interfaces",
		aliases: []string{
			"## 4. interfaces",
			"## 5. cli surface",
			"## 4. api surface",
		},
	},
	{
		display:          "## 5. verification",
		canonicalHeading: "## 5. Verification",
		aliases: []string{
			"## 5. verification",
			"verification pipeline",
			"## 11. council checkpoints and verification pipeline",
			"## 16. test strategy",
			"## 17. acceptance scenarios",
		},
	},
	{
		display:          "## 6. requirement traceability",
		canonicalHeading: "## 6. Requirement traceability",
		aliases: []string{
			"## 6. requirement traceability",
			"routing traceability",
			"trace to requirement ids from this spec",
		},
	},
	{
		display:          "## 7. open questions and risks",
		canonicalHeading: "## 7. Open questions and risks",
		aliases: []string{
			"## 7. open questions and risks",
			"## 19. open v1 decisions",
		},
	},
	{
		display:          "## 8. approval",
		canonicalHeading: "## 8. Approval",
		aliases: []string{
			"## 8. approval",
			"status: draft",
			"status: approved",
			"approved by:",
		},
	},
}

// DraftTechnicalSpec records a run-scoped technical spec markdown artifact.
func (e *Engine) DraftTechnicalSpec(ctx context.Context, sourcePath string) error {
	_ = ctx

	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return fmt.Errorf("read technical spec source: %w", err)
	}
	data = normalizeTechnicalSpec(data)
	if err := e.RunDir.WriteTechnicalSpec(data); err != nil {
		return fmt.Errorf("write technical spec: %w", err)
	}

	_ = e.RunDir.AppendEvent(state.Event{
		Timestamp: time.Now(),
		Type:      "technical_spec_drafted",
		RunID:     filepathBase(e.RunDir.Root),
		Detail:    sourcePath,
	})

	return nil
}

// ReviewTechnicalSpec runs a deterministic required-section review and persists the result.
func (e *Engine) ReviewTechnicalSpec(ctx context.Context) (*state.TechnicalSpecReview, error) {
	_ = ctx

	data, err := e.RunDir.ReadTechnicalSpec()
	if err != nil {
		return nil, fmt.Errorf("read technical spec: %w", err)
	}

	text := strings.ToLower(string(data))
	review := &state.TechnicalSpecReview{
		SchemaVersion:     "0.1",
		RunID:             filepathBase(e.RunDir.Root),
		ArtifactType:      "technical_spec_review",
		TechnicalSpecHash: sha256Prefix + state.SHA256Bytes(data),
		Status:            state.ReviewPass,
		Summary:           "Technical spec contains all required sections.",
		ReviewedAt:        time.Now(),
	}

	for idx, requirement := range technicalSpecRequirements {
		if containsAny(text, requirement.aliases) {
			continue
		}
		review.Status = state.ReviewFail
		review.BlockingFindings = append(review.BlockingFindings, state.ReviewFinding{
			FindingID:       fmt.Sprintf("tsr-%03d", idx+1),
			Severity:        "high",
			Category:        "missing_section",
			Summary:         fmt.Sprintf("missing required section: %s", requirement.display),
			SuggestedRepair: "Add the missing required section to the technical spec.",
		})
	}
	if review.Status == state.ReviewFail {
		review.Summary = "Technical spec is missing one or more required sections."
	}

	if err := e.RunDir.WriteTechnicalSpecReview(review); err != nil {
		return nil, fmt.Errorf("write technical spec review: %w", err)
	}

	_ = e.RunDir.AppendEvent(state.Event{
		Timestamp: review.ReviewedAt,
		Type:      "technical_spec_reviewed",
		RunID:     review.RunID,
		Detail:    string(review.Status),
	})

	return review, nil
}

// CouncilReviewTechnicalSpec runs the multi-persona council review pipeline.
// Structural checks must pass first (call ReviewTechnicalSpec before this).
func (e *Engine) CouncilReviewTechnicalSpec(ctx context.Context, cfg councilflow.CouncilConfig) (*councilflow.CouncilResult, error) {
	data, err := e.RunDir.ReadTechnicalSpec()
	if err != nil {
		return nil, fmt.Errorf("read technical spec: %w", err)
	}

	// Set spec path so reviewers can read from file instead of inline content.
	if cfg.SpecPath == "" {
		cfg.SpecPath = e.RunDir.TechnicalSpec()
	}

	councilDir := filepath.Join(e.RunDir.Root, "council")
	result, err := councilflow.RunCouncil(ctx, string(data), councilDir, cfg)
	if err != nil {
		return nil, fmt.Errorf("council review: %w", err)
	}

	// Update the active technical spec with the council-improved version,
	// then re-run structural review to refresh the stored hash so approval works.
	if result.FinalSpec != "" {
		if err := e.RunDir.WriteTechnicalSpec([]byte(result.FinalSpec)); err != nil {
			return nil, fmt.Errorf("update technical spec: %w", err)
		}
		if _, err := e.ReviewTechnicalSpec(ctx); err != nil {
			return nil, fmt.Errorf("refresh structural review after council: %w", err)
		}
	}

	_ = e.RunDir.AppendEvent(state.Event{
		Timestamp: time.Now(),
		Type:      "technical_spec_council_reviewed",
		RunID:     filepathBase(e.RunDir.Root),
		Detail:    fmt.Sprintf("verdict=%s rounds=%d", result.OverallVerdict, len(result.Rounds)),
	})

	return result, nil
}

// ApproveTechnicalSpec records explicit approval for the run-scoped technical spec.
func (e *Engine) ApproveTechnicalSpec(ctx context.Context, approvedBy string) (*state.ArtifactApproval, error) {
	_ = ctx

	review, err := e.RunDir.ReadTechnicalSpecReview()
	if err != nil {
		return nil, fmt.Errorf("read technical spec review: %w", err)
	}
	if review.Status != state.ReviewPass {
		return nil, fmt.Errorf("technical spec review is not passing")
	}

	data, err := e.RunDir.ReadTechnicalSpec()
	if err != nil {
		return nil, fmt.Errorf("read technical spec: %w", err)
	}
	currentHash := sha256Prefix + state.SHA256Bytes(data)
	if review.TechnicalSpecHash != currentHash {
		return nil, fmt.Errorf("technical spec review hash does not match current artifact")
	}

	// Block approval if the spec contains unresolved @@ annotations.
	annotations := councilflow.ParseAnnotations(string(data))
	if len(annotations) > 0 {
		return nil, fmt.Errorf("cannot approve: %s", councilflow.FormatAnnotationsAsFindings(annotations))
	}

	approval := &state.ArtifactApproval{
		SchemaVersion: "0.1",
		RunID:         filepathBase(e.RunDir.Root),
		ArtifactType:  "technical_spec_approval",
		ArtifactPath:  "technical-spec.md",
		ArtifactHash:  currentHash,
		Status:        state.ArtifactApproved,
		ApprovedBy:    approvedBy,
		ApprovedAt:    time.Now(),
	}
	if err := e.RunDir.WriteTechnicalSpecApproval(approval); err != nil {
		return nil, fmt.Errorf("write technical spec approval: %w", err)
	}

	_ = e.RunDir.AppendEvent(state.Event{
		Timestamp: approval.ApprovedAt,
		Type:      "technical_spec_approved",
		RunID:     approval.RunID,
		Detail:    approval.ApprovedBy,
	})

	return approval, nil
}

func containsAny(haystack string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(haystack, needle) {
			return true
		}
	}
	return false
}

type markdownSection struct {
	heading string
	body    string
}

func normalizeTechnicalSpec(data []byte) []byte {
	text := string(data)
	lower := strings.ToLower(text)
	if hasCanonicalTechnicalSpecHeadings(lower) || !hasLegacyTechnicalSpecHeadings(lower) {
		return data
	}

	title, preamble, sections := parseTechnicalSpecMarkdown(text)
	var builder strings.Builder

	if title == "" {
		title = "# Technical Specification"
	}
	builder.WriteString(title)
	builder.WriteString("\n\n")
	if preamble != "" {
		builder.WriteString(strings.TrimSpace(preamble))
		builder.WriteString("\n\n")
	}

	for _, requirement := range technicalSpecRequirements {
		content := normalizedSectionContent(requirement, text, sections)
		if content == "" {
			continue
		}
		builder.WriteString(requirement.canonicalHeading)
		builder.WriteString("\n\n")
		builder.WriteString(content)
		builder.WriteString("\n\n")
	}

	return []byte(strings.TrimSpace(builder.String()) + "\n")
}

func hasCanonicalTechnicalSpecHeadings(text string) bool {
	for _, requirement := range technicalSpecRequirements {
		if !strings.Contains(text, strings.ToLower(requirement.canonicalHeading)) {
			return false
		}
	}
	return true
}

func hasLegacyTechnicalSpecHeadings(text string) bool {
	for _, requirement := range technicalSpecRequirements {
		for _, alias := range requirement.aliases[1:] {
			if strings.Contains(text, alias) {
				return true
			}
		}
	}
	return false
}

func parseTechnicalSpecMarkdown(text string) (title, preamble string, sections []markdownSection) {
	lines := strings.Split(text, "\n")
	var (
		currentHeading string
		currentBody    []string
		preambleLines  []string
	)

	flush := func() {
		if currentHeading == "" {
			return
		}
		sections = append(sections, markdownSection{
			heading: currentHeading,
			body:    strings.TrimSpace(strings.Join(currentBody, "\n")),
		})
		currentBody = nil
	}

	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "# ") && title == "":
			title = line
		case strings.HasPrefix(line, "## "):
			flush()
			currentHeading = strings.TrimSpace(strings.TrimPrefix(line, "## "))
		case currentHeading == "":
			preambleLines = append(preambleLines, line)
		default:
			currentBody = append(currentBody, line)
		}
	}
	flush()

	return title, strings.TrimSpace(strings.Join(preambleLines, "\n")), sections
}

func normalizedSectionContent(requirement technicalSpecRequirement, fullText string, sections []markdownSection) string {
	var matched []markdownSection
	for _, section := range sections {
		heading := "## " + strings.ToLower(section.heading)
		for _, alias := range requirement.aliases {
			if heading == alias {
				matched = append(matched, section)
				break
			}
		}
	}

	if len(matched) > 0 {
		var builder strings.Builder
		for idx, section := range matched {
			if idx > 0 {
				builder.WriteString("\n\n")
			}
			if "## "+strings.ToLower(section.heading) != strings.ToLower(requirement.canonicalHeading) {
				builder.WriteString("### ")
				builder.WriteString(stripSectionNumber(section.heading))
				builder.WriteString("\n\n")
			}
			builder.WriteString(strings.TrimSpace(section.body))
		}
		return strings.TrimSpace(builder.String())
	}

	if evidence := matchingLine(fullText, requirement.aliases); evidence != "" {
		return "- Source evidence: " + evidence
	}

	return ""
}

func stripSectionNumber(heading string) string {
	parts := strings.SplitN(heading, ". ", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return heading
}

func matchingLine(text string, aliases []string) string {
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		for _, alias := range aliases {
			if strings.Contains(lower, alias) {
				return trimmed
			}
		}
	}
	return ""
}
