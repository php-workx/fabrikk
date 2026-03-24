package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/runger/attest/internal/agentcli"
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
// If noNormalize is false, attempts agent-based normalization for non-canonical docs.
func (e *Engine) DraftTechnicalSpec(ctx context.Context, sourcePath string, noNormalize bool) error {
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return fmt.Errorf("read technical spec source: %w", err)
	}
	if !noNormalize && !hasCanonicalTechnicalSpecHeadings(strings.ToLower(string(data))) {
		fmt.Println("Document doesn't match canonical headings — attempting agent normalization ...")
		if normalized, normErr := normalizeWithAgent(ctx, data); normErr == nil {
			data = normalized
			fmt.Println("Agent normalization applied successfully.")
		} else {
			fmt.Printf("Agent normalization unavailable (%v) — using original document\n", normErr)
		}
	}
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

func hasCanonicalTechnicalSpecHeadings(text string) bool {
	for _, requirement := range technicalSpecRequirements {
		if !strings.Contains(text, strings.ToLower(requirement.canonicalHeading)) {
			return false
		}
	}
	return true
}

const technicalSpecNormalizationPrompt = `You are a technical document reformatter. Your job is to reorganize an existing document into a canonical 8-section template.

## Canonical Sections

## 1. Technical context — Language, runtime, dependencies, constraints
## 2. Architecture — Components, data flow, concurrency model
## 3. Canonical artifacts and schemas — File paths, producers, consumers, schemas
## 4. Interfaces — CLI/API surface, state machines, transitions
## 5. Verification — Quality gates, acceptance scenarios, evidence requirements
## 6. Requirement traceability — Requirement IDs mapped to technical mechanisms
## 7. Open questions and risks — Unresolved decisions and their impact
## 8. Approval — Draft/review/approval status

## Rules

1. Preserve ALL content from the source document. Do not summarize, abbreviate, or omit any information.
2. Map source content to the closest matching canonical section. If content does not clearly fit any section, place it under "## Additional Content" at the end rather than dropping it.
3. Do not invent content that is not in the source document.
4. Preserve code blocks, tables, lists, and formatting from the source.
5. If the source has sections beyond the 8 canonical ones, include them after the canonical sections.

## Output

Return ONLY the reformatted markdown document. No explanation, no commentary, no wrapping code fences.
`

// normalizeWithAgent uses an LLM to reformat a document into the canonical
// 8-section technical spec template. Returns an error if the agent is
// unavailable, times out, or produces suspicious output.
func normalizeWithAgent(ctx context.Context, data []byte) ([]byte, error) {
	prompt := technicalSpecNormalizationPrompt + "\n## Source Document\n\n" + string(data)

	backend := agentcli.KnownBackends[agentcli.BackendClaude]
	output, err := agentcli.InvokeFunc(ctx, &backend, prompt, 120)
	if err != nil {
		return nil, fmt.Errorf("agent invocation: %w", err)
	}

	// Strip code fences if the model wrapped the output.
	if stripped := agentcli.ExtractFromCodeFence(output); stripped != "" {
		output = stripped
	}
	result := []byte(strings.TrimSpace(output))

	// Output validation: reject suspiciously short output.
	if len(result) < len(data)/2 {
		return nil, fmt.Errorf("output too short (%d bytes vs %d input bytes)", len(result), len(data))
	}

	// Output validation: require at least 4 canonical headings as actual section lines
	// (not substring matches, which could match the echoed prompt text inside the output).
	headingCount := countCanonicalHeadings(string(result))
	if headingCount < 4 {
		return nil, fmt.Errorf("output has only %d of 8 canonical headings (need at least 4)", headingCount)
	}

	// Output validation: warn if sections were lost (not a rejection — consolidation is expected).
	inputSections := countMarkdownSections(string(data))
	outputSections := countMarkdownSections(string(result))
	if inputSections > outputSections+2 {
		fmt.Printf("Warning: agent output has %d sections vs %d in input (-%d); verify no content was dropped\n",
			outputSections, inputSections, inputSections-outputSections)
	}

	return result, nil
}

// countMarkdownSections counts lines starting with "## " in a markdown document.
func countMarkdownSections(text string) int {
	count := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "## ") {
			count++
		}
	}
	return count
}

// countCanonicalHeadings counts how many of the 8 canonical headings appear
// as actual section lines in the document (not as substring matches inside
// code blocks or echoed prompt text).
func countCanonicalHeadings(text string) int {
	canonicalSet := make(map[string]bool, len(technicalSpecRequirements))
	for _, req := range technicalSpecRequirements {
		canonicalSet[strings.ToLower(req.canonicalHeading)] = true
	}

	count := 0
	inFence := false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if canonicalSet[strings.ToLower(trimmed)] {
			count++
		}
	}
	return count
}
