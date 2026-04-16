package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/php-workx/fabrikk/internal/agentcli"
	"github.com/php-workx/fabrikk/internal/state"
)

const (
	defaultSpecNormalizationConverterTimeoutSec = 300
	defaultSpecNormalizationMaxOutputBytes      = 10 << 20
	sourceExcerptContextLines                   = 5
)

var normalizedRequirementIDPattern = regexp.MustCompile(`^AT-(?:FR|TS|NFR|AS)-\d{3}$`)

type specNormalizationConverterOptions struct {
	BackendName    string
	TimeoutSeconds int
	MaxOutputBytes int
}

type specNormalizationVerifierOptions struct {
	BackendName    string
	TimeoutSeconds int
	MaxOutputBytes int
}

func (o specNormalizationConverterOptions) withDefaults() specNormalizationConverterOptions {
	if o.BackendName == "" {
		o.BackendName = agentcli.BackendClaude
	}
	if o.TimeoutSeconds <= 0 {
		o.TimeoutSeconds = defaultSpecNormalizationConverterTimeoutSec
	}
	if o.MaxOutputBytes <= 0 {
		o.MaxOutputBytes = defaultSpecNormalizationMaxOutputBytes
	}
	return o
}

func (o specNormalizationVerifierOptions) withDefaults() specNormalizationVerifierOptions {
	if o.BackendName == "" {
		o.BackendName = agentcli.BackendClaude
	}
	if o.TimeoutSeconds <= 0 {
		o.TimeoutSeconds = defaultSpecNormalizationConverterTimeoutSec
	}
	if o.MaxOutputBytes <= 0 {
		o.MaxOutputBytes = defaultSpecNormalizationMaxOutputBytes
	}
	return o
}

func (e *Engine) convertSpecsToArtifact(ctx context.Context, runID string, bundle *specNormalizationSourceBundle, opts specNormalizationConverterOptions) (*state.RunArtifact, error) {
	if e == nil || e.RunDir == nil {
		return nil, fmt.Errorf("spec normalization converter requires a run directory")
	}
	if bundle == nil || bundle.Manifest == nil {
		return nil, fmt.Errorf("spec normalization converter requires a source manifest")
	}

	opts = opts.withDefaults()
	backend, ok := agentcli.KnownBackends[opts.BackendName]
	if !ok {
		return nil, fmt.Errorf("unknown spec normalization converter backend %q", opts.BackendName)
	}

	prompt, err := buildSpecNormalizationConverterPrompt(runID, bundle)
	if err != nil {
		return nil, err
	}
	if err := e.RunDir.WriteSpecNormalizationConverterPrompt([]byte(prompt)); err != nil {
		return nil, fmt.Errorf("write spec normalization converter prompt: %w", err)
	}

	invoke := e.InvokeFn
	if invoke == nil {
		invoke = agentcli.InvokeFunc
	}

	raw, err := invoke(ctx, &backend, prompt, opts.TimeoutSeconds)
	if writeErr := e.RunDir.WriteSpecNormalizationConverterRaw([]byte(raw)); writeErr != nil {
		return nil, fmt.Errorf("write spec normalization converter raw output: %w", writeErr)
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("spec normalization converter backend %s timed out after %d seconds: %w", opts.BackendName, opts.TimeoutSeconds, err)
		}
		return nil, fmt.Errorf("spec normalization converter backend %s failed; check %s for raw output: %w", opts.BackendName, e.RunDir.SpecNormalizationConverterRaw(), err)
	}
	if len(raw) > opts.MaxOutputBytes {
		return nil, fmt.Errorf("spec normalization converter output is %d bytes, above limit %d bytes; retry with a smaller spec or explicit AT-* requirement IDs", len(raw), opts.MaxOutputBytes)
	}

	jsonBlock := extractSpecNormalizationArtifactJSON(raw)
	if jsonBlock == "" {
		return nil, fmt.Errorf("spec normalization converter output contained no JSON object; check %s for raw output", e.RunDir.SpecNormalizationConverterRaw())
	}

	var artifact state.RunArtifact
	if err := json.Unmarshal([]byte(jsonBlock), &artifact); err != nil {
		return nil, fmt.Errorf("parse spec normalization converter JSON: %w", err)
	}
	if err := validateSpecNormalizationCandidate(runID, &artifact); err != nil {
		return nil, err
	}
	if err := e.RunDir.WriteNormalizedArtifactCandidate(&artifact); err != nil {
		return nil, fmt.Errorf("write normalized artifact candidate: %w", err)
	}

	return &artifact, nil
}

func (e *Engine) verifyNormalizedArtifactCandidate(ctx context.Context, runID string, bundle *specNormalizationSourceBundle, artifact *state.RunArtifact, opts specNormalizationVerifierOptions) (*state.SpecNormalizationReview, error) {
	if e == nil || e.RunDir == nil {
		return nil, fmt.Errorf("spec normalization verifier requires a run directory")
	}
	if bundle == nil || bundle.Manifest == nil {
		return nil, fmt.Errorf("spec normalization verifier requires a source manifest")
	}
	if artifact == nil {
		return nil, fmt.Errorf("spec normalization verifier requires a normalized artifact candidate")
	}

	opts = opts.withDefaults()
	backend, ok := agentcli.KnownBackends[opts.BackendName]
	if !ok {
		return nil, fmt.Errorf("unknown spec normalization verifier backend %q", opts.BackendName)
	}

	artifactHash, err := hashNormalizedArtifactCandidate(artifact)
	if err != nil {
		return nil, err
	}
	boundary, err := specNormalizationReviewedBoundary(runID, bundle, artifact, artifactHash)
	if err != nil {
		return nil, err
	}
	prompt, err := buildSpecNormalizationVerifierPrompt(runID, bundle, artifact, artifactHash, boundary.ReviewedInputHash)
	if err != nil {
		return nil, err
	}
	if err := e.RunDir.WriteSpecNormalizationVerifierPrompt([]byte(prompt)); err != nil {
		return nil, fmt.Errorf("write spec normalization verifier prompt: %w", err)
	}

	invoke := e.InvokeFn
	if invoke == nil {
		invoke = agentcli.InvokeFunc
	}

	raw, err := invoke(ctx, &backend, prompt, opts.TimeoutSeconds)
	if writeErr := e.RunDir.WriteSpecNormalizationVerifierRaw([]byte(raw)); writeErr != nil {
		return nil, fmt.Errorf("write spec normalization verifier raw output: %w", writeErr)
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("spec normalization verifier backend %s timed out after %d seconds: %w", opts.BackendName, opts.TimeoutSeconds, err)
		}
		return nil, fmt.Errorf("spec normalization verifier backend %s failed; check %s for raw output: %w", opts.BackendName, e.RunDir.SpecNormalizationVerifierRaw(), err)
	}
	if len(raw) > opts.MaxOutputBytes {
		return nil, fmt.Errorf("spec normalization verifier output is %d bytes, above limit %d bytes; retry with a smaller spec or explicit AT-* requirement IDs", len(raw), opts.MaxOutputBytes)
	}

	jsonBlock := extractSpecNormalizationArtifactJSON(raw)
	if jsonBlock == "" {
		return nil, fmt.Errorf("spec normalization verifier output contained no JSON object; check %s for raw output", e.RunDir.SpecNormalizationVerifierRaw())
	}
	if err := rejectVerifierArtifactMutation(jsonBlock); err != nil {
		return nil, err
	}

	var review state.SpecNormalizationReview
	if err := json.Unmarshal([]byte(jsonBlock), &review); err != nil {
		return nil, fmt.Errorf("parse spec normalization verifier JSON: %w", err)
	}
	if err := validateSpecNormalizationReview(runID, artifactHash, bundle.ManifestHash, boundary.ReviewedInputHash, &review); err != nil {
		return nil, err
	}
	review.ConverterPromptHash = boundary.ConverterPromptHash
	review.VerifierPromptHash = boundary.VerifierPromptHash
	if err := e.RunDir.WriteSpecNormalizationReview(&review); err != nil {
		return nil, fmt.Errorf("write spec normalization review: %w", err)
	}
	return &review, nil
}

type specNormalizationBoundary struct {
	ConverterPromptHash string
	VerifierPromptHash  string
	ReviewedInputHash   string
}

func specNormalizationReviewedBoundary(runID string, bundle *specNormalizationSourceBundle, artifact *state.RunArtifact, artifactHash string) (specNormalizationBoundary, error) {
	converterPrompt, err := buildSpecNormalizationConverterPrompt(runID, bundle)
	if err != nil {
		return specNormalizationBoundary{}, err
	}
	verifierPromptSeed, err := buildSpecNormalizationVerifierPrompt(runID, bundle, artifact, artifactHash, "")
	if err != nil {
		return specNormalizationBoundary{}, err
	}
	boundary := specNormalizationBoundary{
		ConverterPromptHash: hashSpecNormalizationPromptText(converterPrompt),
		VerifierPromptHash:  hashSpecNormalizationPromptText(verifierPromptSeed),
	}
	boundary.ReviewedInputHash, err = hashSpecNormalizationReviewedInput(state.SpecNormalizationReviewedInput{
		SourceManifestHash:     bundle.ManifestHash,
		NormalizedArtifactHash: artifactHash,
		ConverterPromptHash:    boundary.ConverterPromptHash,
		VerifierPromptHash:     boundary.VerifierPromptHash,
	})
	if err != nil {
		return specNormalizationBoundary{}, err
	}
	return boundary, nil
}

func buildSpecNormalizationConverterPrompt(runID string, bundle *specNormalizationSourceBundle) (string, error) {
	base, err := loadSpecNormalizationConverterPrompt()
	if err != nil {
		return "", err
	}

	var prompt strings.Builder
	prompt.WriteString("# spec normalization converter\n\n")
	prompt.WriteString(base)
	prompt.WriteString("\n\n## Invocation\n\n")
	prompt.WriteString("Run ID: ")
	prompt.WriteString(runID)
	prompt.WriteString("\nSource manifest hash: ")
	prompt.WriteString(bundle.ManifestHash)
	prompt.WriteString("\n\n## Source documents\n\n")
	prompt.WriteString(bundle.PromptInput)
	prompt.WriteString("\n")
	return prompt.String(), nil
}

func buildSpecNormalizationVerifierPrompt(runID string, bundle *specNormalizationSourceBundle, artifact *state.RunArtifact, artifactHash, reviewedInputHash string) (string, error) {
	base, err := loadSpecNormalizationVerifierPrompt()
	if err != nil {
		return "", err
	}
	artifactJSON, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal normalized artifact candidate for verifier: %w", err)
	}

	var prompt strings.Builder
	prompt.WriteString("# spec normalization verifier\n\n")
	prompt.WriteString(base)
	prompt.WriteString("\n\n## Invocation\n\n")
	prompt.WriteString("Run ID: ")
	prompt.WriteString(runID)
	prompt.WriteString("\nNormalized artifact hash: ")
	prompt.WriteString(artifactHash)
	prompt.WriteString("\nSource manifest hash: ")
	prompt.WriteString(bundle.ManifestHash)
	prompt.WriteString("\nReviewed input hash: ")
	prompt.WriteString(reviewedInputHash)
	prompt.WriteString("\n\n## Source documents\n\n")
	prompt.WriteString(bundle.PromptInput)
	prompt.WriteString("\n\n## Normalized artifact candidate\n\n```json\n")
	prompt.Write(artifactJSON)
	prompt.WriteString("\n```\n")
	return prompt.String(), nil
}

func extractSpecNormalizationArtifactJSON(raw string) string {
	for offset := 0; offset < len(raw); {
		rel := strings.IndexByte(raw[offset:], '{')
		if rel < 0 {
			return ""
		}
		start := offset + rel
		candidate := agentcli.ExtractJSONBlock(raw, start)
		if candidate != "" {
			return candidate
		}
		// Always advance past the unmatched opener so malformed prefixes cannot stall extraction.
		offset = start + 1
	}
	return ""
}

func validateSpecNormalizationCandidate(runID string, artifact *state.RunArtifact) error {
	if artifact.SchemaVersion != "0.1" {
		return fmt.Errorf("spec normalization converter JSON schema_version = %q, want 0.1", artifact.SchemaVersion)
	}
	if artifact.RunID != runID {
		return fmt.Errorf("spec normalization converter JSON run_id = %q, want %q", artifact.RunID, runID)
	}
	if artifact.ApprovedAt != nil || artifact.ApprovedBy != "" || artifact.ArtifactHash != "" {
		return fmt.Errorf("spec normalization converter JSON must not include approval fields")
	}
	if len(artifact.SourceSpecs) == 0 {
		return fmt.Errorf("spec normalization converter JSON must include source_specs")
	}
	if len(artifact.Requirements) == 0 {
		return fmt.Errorf("spec normalization converter JSON must include requirements")
	}
	for _, req := range artifact.Requirements {
		if strings.TrimSpace(req.ID) == "" || strings.TrimSpace(req.Text) == "" {
			return fmt.Errorf("spec normalization converter JSON contains a requirement without id or text")
		}
		if len(req.SourceRefs) == 0 {
			return fmt.Errorf("spec normalization converter JSON requirement %s has no source_refs", req.ID)
		}
		for _, ref := range req.SourceRefs {
			if strings.TrimSpace(ref.Path) == "" || ref.LineStart <= 0 || ref.LineEnd < ref.LineStart {
				return fmt.Errorf("spec normalization converter JSON requirement %s has invalid source_refs", req.ID)
			}
		}
	}
	if strings.TrimSpace(artifact.RoutingPolicy.DefaultImplementer) == "" {
		return fmt.Errorf("spec normalization converter JSON must include routing_policy.default_implementer")
	}
	return nil
}

func hashNormalizedArtifactCandidate(artifact *state.RunArtifact) (string, error) {
	data, err := json.Marshal(artifact)
	if err != nil {
		return "", fmt.Errorf("marshal normalized artifact candidate for hash: %w", err)
	}
	return sha256Prefix + state.SHA256Bytes(data), nil
}

func hashSpecNormalizationPromptText(prompt string) string {
	return sha256Prefix + state.SHA256Bytes([]byte(normalizeSpecNormalizationPromptForHash(prompt)))
}

func normalizeSpecNormalizationPromptForHash(prompt string) string {
	lines := strings.Split(prompt, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "Reviewed input hash: ") {
			lines[i] = "Reviewed input hash: <self>"
		}
	}
	return strings.Join(lines, "\n")
}

func hashSpecNormalizationReviewedInput(input state.SpecNormalizationReviewedInput) (string, error) {
	data, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("marshal spec normalization reviewed input: %w", err)
	}
	return sha256Prefix + state.SHA256Bytes(data), nil
}

func rejectVerifierArtifactMutation(jsonBlock string) error {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(jsonBlock), &payload); err != nil {
		return fmt.Errorf("parse spec normalization verifier JSON: %w", err)
	}
	for _, key := range []string{
		"source_specs",
		"requirements",
		"assumptions",
		"clarifications",
		"dependencies",
		"risk_profile",
		"boundaries",
		"routing_policy",
		"quality_gate",
		"run_artifact",
		"normalized_artifact",
		"normalized_artifact_candidate",
	} {
		if _, ok := payload[key]; ok {
			return fmt.Errorf("spec normalization verifier output must not include artifact fields such as %q", key)
		}
	}
	return nil
}

func validateSpecNormalizationReview(runID, artifactHash, manifestHash, reviewedInputHash string, review *state.SpecNormalizationReview) error {
	if review.SchemaVersion != "0.1" {
		return fmt.Errorf("spec normalization verifier JSON schema_version = %q, want 0.1", review.SchemaVersion)
	}
	if review.RunID != runID {
		return fmt.Errorf("spec normalization verifier JSON run_id = %q, want %q", review.RunID, runID)
	}
	if review.ArtifactType != state.ArtifactTypeSpecNormalizationReview {
		return fmt.Errorf("spec normalization verifier JSON artifact_type = %q, want %q", review.ArtifactType, state.ArtifactTypeSpecNormalizationReview)
	}
	switch review.Status {
	case state.ReviewPass, state.ReviewFail, state.ReviewNeedsRevision:
	default:
		return fmt.Errorf("spec normalization verifier JSON status = %q, want pass, fail, or needs_revision", review.Status)
	}
	if review.NormalizedArtifactHash != artifactHash {
		return fmt.Errorf("spec normalization verifier JSON normalized_artifact_hash = %q, want %q", review.NormalizedArtifactHash, artifactHash)
	}
	if review.SourceManifestHash != manifestHash {
		return fmt.Errorf("spec normalization verifier JSON source_manifest_hash = %q, want %q", review.SourceManifestHash, manifestHash)
	}
	if review.ReviewedInputHash != reviewedInputHash {
		return fmt.Errorf("spec normalization verifier JSON reviewed_input_hash = %q, want %q", review.ReviewedInputHash, reviewedInputHash)
	}
	if review.ReviewedAt.IsZero() {
		return fmt.Errorf("spec normalization verifier JSON must include reviewed_at")
	}
	return nil
}

func (e *Engine) validateNormalizedArtifactCandidate(artifact *state.RunArtifact, manifest *state.SpecNormalizationSourceManifest) ([]state.ReviewFinding, error) {
	if e == nil || e.RunDir == nil {
		return nil, fmt.Errorf("spec normalization validation requires a run directory")
	}
	if artifact == nil {
		return nil, fmt.Errorf("spec normalization validation requires a normalized artifact candidate")
	}
	if manifest == nil {
		return nil, fmt.Errorf("spec normalization validation requires a source manifest")
	}

	validator := specNormalizationValidator{
		sources: sourceEntriesByPath(manifest),
		lines:   sourceLinesByPath(manifest),
		ids:     make(map[string]struct{}, len(artifact.Requirements)),
	}
	validator.validateSourceSpecsParity(artifact.SourceSpecs)
	validator.validateRequirements(artifact.Requirements)
	validator.validateDependencies(artifact.Dependencies)
	validator.validateBoundaries(artifact.Boundaries)

	if err := e.RunDir.WriteSpecNormalizationValidation(validator.findings); err != nil {
		return nil, fmt.Errorf("write spec normalization validation findings: %w", err)
	}
	return validator.findings, nil
}

type specNormalizationValidator struct {
	sources  map[string]state.SourceManifestEntry
	lines    map[string][]string
	ids      map[string]struct{}
	findings []state.ReviewFinding
}

func (v *specNormalizationValidator) validateSourceSpecsParity(sourceSpecs []state.SourceSpec) {
	if len(sourceSpecs) != len(v.sources) {
		v.addFinding("high", "source_specs_mismatch", "", fmt.Sprintf("normalized artifact has %d source_specs, reviewed manifest has %d sources", len(sourceSpecs), len(v.sources)), nil, "Keep source_specs in one-to-one path and fingerprint parity with the reviewed source manifest.")
	}

	seen := make(map[string]struct{}, len(sourceSpecs))
	for _, spec := range sourceSpecs {
		path := strings.TrimSpace(spec.Path)
		if path == "" {
			v.addFinding("high", "source_specs_mismatch", "", "normalized artifact contains a source_spec without a path", nil, "Every source_spec must identify a reviewed source manifest path.")
			continue
		}
		if _, exists := seen[path]; exists {
			v.addFinding("high", "source_specs_mismatch", "", fmt.Sprintf("normalized artifact contains duplicate source_spec path %q", path), nil, "Each reviewed source manifest path must appear exactly once in source_specs.")
			continue
		}
		seen[path] = struct{}{}

		source, ok := v.sources[path]
		if !ok {
			v.addFinding("high", "source_specs_mismatch", "", fmt.Sprintf("normalized artifact source_spec path %q is not in the reviewed source manifest", path), nil, "Use only source_spec paths from the reviewed source manifest.")
			continue
		}
		if spec.Fingerprint != source.Fingerprint {
			v.addFinding("high", "source_specs_mismatch", "", fmt.Sprintf("normalized artifact source_spec %q fingerprint = %q, reviewed fingerprint = %q", path, spec.Fingerprint, source.Fingerprint), nil, "Preserve reviewed source manifest fingerprints exactly in source_specs.")
		}
	}
}

func (v *specNormalizationValidator) validateRequirements(requirements []state.Requirement) {
	if len(requirements) == 0 {
		v.addFinding("high", "no_requirements", "", "normalized artifact must contain at least one requirement", nil, "Convert actionable source statements into requirements or use explicit AT-* requirement IDs.")
		return
	}

	for _, req := range requirements {
		reqID := strings.TrimSpace(req.ID)
		if reqID == "" || strings.TrimSpace(req.Text) == "" || len(req.SourceRefs) == 0 {
			v.addFinding("high", "missing_requirement_field", reqID, "requirement is missing id, text, or source_refs", requirementIDsForFinding(reqID), "Every requirement must include non-empty id, text, and at least one source_ref.")
		}
		if reqID != "" {
			if !normalizedRequirementIDPattern.MatchString(reqID) {
				v.addFinding("high", "invalid_requirement_id", reqID, fmt.Sprintf("requirement id %q does not match accepted AT-* format", reqID), []string{reqID}, "Use stable IDs such as AT-FR-001, AT-TS-001, AT-NFR-001, or AT-AS-001.")
			}
			if _, exists := v.ids[reqID]; exists {
				v.addFinding("high", "duplicate_requirement_id", reqID, fmt.Sprintf("duplicate requirement id %q", reqID), []string{reqID}, "Each requirement ID must be unique.")
			}
			v.ids[reqID] = struct{}{}
		}
		for _, ref := range req.SourceRefs {
			v.validateSourceRef(reqID, ref)
		}
	}
}

func (v *specNormalizationValidator) validateSourceRef(reqID string, ref state.SourceRef) {
	source, ok := v.sources[ref.Path]
	if !ok {
		v.addFinding("high", "unknown_source_ref", reqID, fmt.Sprintf("requirement %q references source path %q that is not in the source manifest", reqID, ref.Path), requirementIDsForFinding(reqID), "Use only source_ref paths from the source manifest.")
		return
	}
	if ref.LineStart <= 0 || ref.LineEnd < ref.LineStart || ref.LineEnd > source.LineCount {
		v.addFinding("high", "invalid_source_ref_range", reqID, fmt.Sprintf("requirement %q has invalid source line range %d-%d for %q", reqID, ref.LineStart, ref.LineEnd, ref.Path), requirementIDsForFinding(reqID), "Use valid one-based line ranges within the cited source file.")
		return
	}
	if !sourceExcerptMatches(v.lines[ref.Path], ref) {
		v.addFinding("high", "source_excerpt_mismatch", reqID, fmt.Sprintf("requirement %q source excerpt does not match cited lines in %q", reqID, ref.Path), requirementIDsForFinding(reqID), "Cite an excerpt that appears within or near the claimed source lines.")
	}
}

func (v *specNormalizationValidator) validateDependencies(dependencies []string) {
	for _, dep := range dependencies {
		dep = strings.TrimSpace(dep)
		if dep == "" {
			continue
		}
		if _, ok := v.ids[dep]; !ok {
			v.addFinding("high", "unknown_dependency", dep, fmt.Sprintf("dependency %q does not reference a known requirement ID", dep), []string{dep}, "Dependencies must reference requirement IDs present in the normalized artifact.")
		}
	}
}

func (v *specNormalizationValidator) validateBoundaries(boundaries state.Boundaries) {
	if boundaryListAlreadyNormalized(boundaries.Always) &&
		boundaryListAlreadyNormalized(boundaries.AskFirst) &&
		boundaryListAlreadyNormalized(boundaries.Never) {
		return
	}
	v.addFinding("medium", "unnormalized_boundaries", "", "boundaries contain duplicate or untrimmed entries", nil, "Trim boundary entries and remove duplicates using the canonical boundary normalization rules.")
}

func (v *specNormalizationValidator) addFinding(severity, category, subject, summary string, requirementIDs []string, repair string) {
	v.findings = append(v.findings, state.ReviewFinding{
		FindingID:       fmt.Sprintf("snv-%03d", len(v.findings)+1),
		Severity:        severity,
		Category:        category,
		SliceID:         subject,
		Summary:         summary,
		RequirementIDs:  requirementIDs,
		SuggestedRepair: repair,
	})
}

func sourceEntriesByPath(manifest *state.SpecNormalizationSourceManifest) map[string]state.SourceManifestEntry {
	entries := make(map[string]state.SourceManifestEntry, len(manifest.Sources))
	for _, source := range manifest.Sources {
		entries[source.Path] = source
	}
	return entries
}

func sourceLinesByPath(manifest *state.SpecNormalizationSourceManifest) map[string][]string {
	lines := make(map[string][]string, len(manifest.Sources))
	for _, source := range manifest.Sources {
		lines[source.Path] = parseLineNumberedSource(source.LineNumberedText)
	}
	return lines
}

func parseLineNumberedSource(lineNumbered string) []string {
	rawLines := strings.Split(lineNumbered, "\n")
	lines := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		if _, text, ok := strings.Cut(line, " | "); ok {
			lines = append(lines, text)
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

func sourceExcerptMatches(lines []string, ref state.SourceRef) bool {
	excerpt := normalizeWhitespace(ref.Excerpt)
	if excerpt == "" {
		return false
	}
	start := max(ref.LineStart-sourceExcerptContextLines, 1)
	end := min(ref.LineEnd+sourceExcerptContextLines, len(lines))
	if start > end || start <= 0 {
		return false
	}
	window := normalizeWhitespace(strings.Join(lines[start-1:end], " "))
	return strings.Contains(window, excerpt)
}

func normalizeWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func boundaryListAlreadyNormalized(items []string) bool {
	normalized := state.Boundaries{Always: items}.Normalized().Always
	if len(items) == 0 && len(normalized) == 0 {
		return true
	}
	if len(items) != len(normalized) {
		return false
	}
	for i := range items {
		if items[i] != normalized[i] {
			return false
		}
	}
	return true
}

func requirementIDsForFinding(reqID string) []string {
	if reqID == "" {
		return nil
	}
	return []string{reqID}
}
