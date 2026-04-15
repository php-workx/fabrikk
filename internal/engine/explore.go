package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/php-workx/fabrikk/internal/agentcli"
	"github.com/php-workx/fabrikk/internal/state"
)

const (
	structuralAnalysisTimeout        = 60 * time.Second
	maxStructuralAnalysisOutputBytes = 10 << 20
	codeIntelCandidatesEnv           = "FABRIKK_CODE_INTEL_CANDIDATES"
)

// runStructuralAnalysis executes code_intel.py graph analysis for the current workdir.
// It returns nil without error when the tool is unavailable.
func (e *Engine) runStructuralAnalysis(ctx context.Context) (*state.StructuralAnalysis, error) {
	toolPath := resolveCodeIntel()
	if toolPath == "" {
		return nil, nil //nolint:nilnil // missing structural analysis tool is an expected degraded mode, not an error
	}

	runCtx, cancel := context.WithTimeout(ctx, structuralAnalysisTimeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, toolPath, "graph", "--root", e.WorkDir, "--json") //nolint:gosec // tool path is discovered from trusted local configuration
	cmd.Dir = e.WorkDir

	var stdout, stderr limitedBuffer
	stdout.limit = maxStructuralAnalysisOutputBytes
	stderr.limit = maxStructuralAnalysisOutputBytes
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if isUnavailableToolError(err) {
			return nil, nil //nolint:nilnil // disappearing optional tool still means degrade gracefully
		}
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("run structural analysis: timed out after %s", structuralAnalysisTimeout)
		}
		return nil, fmt.Errorf("run structural analysis: %w (stderr: %s)", err, stderr.String())
	}

	var analysis state.StructuralAnalysis
	if err := json.Unmarshal(stdout.Bytes(), &analysis); err != nil {
		return nil, fmt.Errorf("parse structural analysis: %w (stdout: %s)", err, stdout.String())
	}
	return &analysis, nil
}

// buildExplorationPrompt renders the exploration prompt with or without structural context.
func buildExplorationPrompt(artifact *state.RunArtifact, sa *state.StructuralAnalysis) string {
	var buf strings.Builder

	buf.WriteString("You are preparing a codebase exploration report for fabrikk plan drafting. Map approved requirements to concrete files, symbols, tests, and reuse points.\n\n")
	buf.WriteString("## Requirements\n\n")
	for _, req := range artifact.Requirements {
		fmt.Fprintf(&buf, "- %s: %s\n", req.ID, req.Text)
	}

	if sa != nil {
		buf.WriteString("\n## Mode\n\n")
		buf.WriteString("Structural interpretation mode. Use the provided structural analysis as your primary grounding. Do not spend tokens rediscovering the repository layout. Timeout budget: 180s.\n")
		buf.WriteString("\n## Codebase Structure\n\n```json\n")
		if data, err := json.MarshalIndent(sa, "", "  "); err == nil {
			buf.Write(data)
			buf.WriteByte('\n')
		} else {
			buf.WriteString("{}\n")
		}
		buf.WriteString("```\n")
		buf.WriteString("\nInterpret the structural graph to map requirements to existing symbols, likely files, test files, and reuse points.\n")
	} else {
		buf.WriteString("\n## Mode\n\n")
		buf.WriteString("Tool-exploration mode. Structural analysis is unavailable, so explore the repository directly. Timeout budget: 360s.\n")
		buf.WriteString("\n## Discovery Strategy\n\n")
		buf.WriteString("1. List the relevant source directories before opening files.\n")
		buf.WriteString("2. Grep for requirement keywords in file names, symbol names, and nearby tests.\n")
		buf.WriteString("3. Read the key files identified in steps 1-2 before deciding touched files or symbols.\n")
		buf.WriteString("\nBe explicit about uncertainty instead of inventing paths or symbols.\n")
	}

	buf.WriteString("\n## Output\n\n")
	buf.WriteString("Return ONLY valid JSON matching this schema:\n")
	buf.WriteString("{\n")
	buf.WriteString("  \"file_inventory\": [{\"path\": \"internal/example/file.go\", \"exists\": true, \"is_new\": false, \"line_count\": 0, \"language\": \"go\"}],\n")
	buf.WriteString("  \"symbols\": [{\"file_path\": \"internal/example/file.go\", \"line\": 0, \"kind\": \"func\", \"signature\": \"func Example()\"}],\n")
	buf.WriteString("  \"test_files\": [{\"path\": \"internal/example/file_test.go\", \"test_names\": [\"TestExample\"], \"covers\": \"internal/example/file.go\"}],\n")
	buf.WriteString("  \"reuse_points\": [{\"file_path\": \"internal/example/file.go\", \"line\": 0, \"symbol\": \"Example\", \"relevance\": \"why this code should be reused\"}]\n")
	buf.WriteString("}\n")
	return buf.String()
}

// exploreForPlan asks an LLM to map requirements onto the current codebase.
// Exploration is advisory: backend or parsing failures log a warning and return nil.
func (e *Engine) exploreForPlan(ctx context.Context, artifact *state.RunArtifact, sa *state.StructuralAnalysis) (*state.ExplorationResult, error) {
	if artifact == nil {
		return nil, fmt.Errorf("nil run artifact")
	}

	backend := agentcli.KnownBackends[agentcli.BackendClaude]
	invoke := e.InvokeFn
	if invoke == nil {
		invoke = agentcli.InvokeFunc
	}

	timeoutSec := 180
	if sa == nil {
		timeoutSec = 360
	}

	raw, err := invoke(ctx, &backend, buildExplorationPrompt(artifact, sa), timeoutSec)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  explore: invoke failed: %v (proceeding without exploration)\n", err)
		return nil, nil //nolint:nilnil // exploration is advisory; callers continue with deterministic planning only
	}

	jsonStr := extractExplorationJSON(raw)
	if jsonStr == "" {
		fmt.Fprintln(os.Stderr, "  explore: could not locate exploration JSON in model output (proceeding without exploration)")
		return nil, nil //nolint:nilnil // malformed advisory exploration output should not block deterministic planning
	}

	var result state.ExplorationResult
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		fmt.Fprintf(os.Stderr, "  explore: could not parse LLM response: %v (proceeding without exploration)\n", err)
		return nil, nil //nolint:nilnil // invalid advisory exploration output degrades to no exploration
	}

	e.validateExplorationResult(&result)
	if e.RunDir != nil {
		if err := state.WriteJSON(e.RunDir.Path("exploration.json"), &result); err != nil {
			fmt.Fprintf(os.Stderr, "  explore: could not persist exploration result: %v\n", err)
		}
	}

	return &result, nil
}

// extractExplorationJSON returns the first valid exploration-shaped JSON payload in the model output.
func extractExplorationJSON(raw string) string {
	var firstValid string
	for _, candidate := range explorationJSONCandidates(raw) {
		if candidate == "" || !json.Valid([]byte(candidate)) {
			continue
		}
		if firstValid == "" {
			firstValid = candidate
		}
		if looksLikeExplorationResultJSON(candidate) {
			return candidate
		}
	}
	return firstValid
}

func explorationJSONCandidates(raw string) []string {
	candidates := make([]string, 0, 4)
	candidates = append(candidates, codeFenceBlocks(raw)...)

	for offset := 0; offset < len(raw); {
		rel := strings.IndexAny(raw[offset:], "{[")
		if rel < 0 {
			break
		}
		start := offset + rel
		candidate := agentcli.ExtractJSONBlock(raw, start)
		if candidate != "" {
			candidates = append(candidates, candidate)
			offset = start + len(candidate)
			continue
		}
		offset = start + 1
	}

	return candidates
}

func codeFenceBlocks(raw string) []string {
	var blocks []string
	for offset := 0; offset < len(raw); {
		relStart := strings.Index(raw[offset:], "```")
		if relStart < 0 {
			break
		}
		fenceStart := offset + relStart + len("```")
		newline := strings.IndexByte(raw[fenceStart:], '\n')
		if newline < 0 {
			break
		}
		contentStart := fenceStart + newline + 1
		relEnd := strings.Index(raw[contentStart:], "```")
		if relEnd < 0 {
			break
		}
		contentEnd := contentStart + relEnd
		if block := strings.TrimSpace(raw[contentStart:contentEnd]); block != "" {
			blocks = append(blocks, block)
		}
		offset = contentEnd + len("```")
	}
	return blocks
}

func looksLikeExplorationResultJSON(candidate string) bool {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(candidate), &payload); err != nil {
		return false
	}
	for _, key := range []string{"file_inventory", "symbols", "test_files", "reuse_points"} {
		if _, ok := payload[key]; ok {
			return true
		}
	}
	return false
}

// validateExplorationResult reconciles exploratory paths with the local filesystem.
func (e *Engine) validateExplorationResult(result *state.ExplorationResult) {
	if result == nil {
		return
	}

	validatedFiles := result.FileInventory[:0]
	for _, fileInfo := range result.FileInventory {
		path := e.explorationPath(fileInfo.Path)
		if path == "" {
			continue
		}
		if _, err := os.Stat(path); err == nil {
			fileInfo.Exists = true
			fileInfo.IsNew = false
			validatedFiles = append(validatedFiles, fileInfo)
			continue
		}
		if fileInfo.IsNew && !fileInfo.Exists {
			fileInfo.Exists = false
			fileInfo.IsNew = true
			validatedFiles = append(validatedFiles, fileInfo)
		}
	}
	result.FileInventory = validatedFiles

	result.Symbols = filterExisting(result.Symbols, func(symbol state.SymbolInfo) string {
		return e.explorationPath(symbol.FilePath)
	})
	result.TestFiles = filterExplorationTestFiles(result.TestFiles, func(testFile state.TestFileInfo) string {
		return e.explorationPath(testFile.Path)
	}, func(testFile state.TestFileInfo) string {
		return e.explorationPath(testFile.Covers)
	})
	result.ReusePoints = filterExisting(result.ReusePoints, func(reuse state.ReusePoint) string {
		return e.explorationPath(reuse.FilePath)
	})
}

func (e *Engine) explorationPath(path string) string {
	resolved, err := resolveWorktreePath(e.WorkDir, path)
	if err != nil {
		return ""
	}
	return resolved
}

func filterExplorationTestFiles(items []state.TestFileInfo, pathOf, coverOf func(state.TestFileInfo) string) []state.TestFileInfo {
	filtered := items[:0]
	for _, item := range items {
		path := pathOf(item)
		if path != "" {
			if _, err := os.Stat(path); err == nil {
				filtered = append(filtered, item)
				continue
			}
		}
		cover := coverOf(item)
		if cover == "" {
			continue
		}
		if _, err := os.Stat(cover); err == nil {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func filterExisting[T any](items []T, pathOf func(T) string) []T {
	filtered := items[:0]
	for _, item := range items {
		path := pathOf(item)
		if path == "" {
			continue
		}
		if _, err := os.Stat(path); err == nil {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

// resolveCodeIntel discovers the code_intel.py entrypoint.
func resolveCodeIntel() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	return resolveCodeIntelWithHome(home)
}

func resolveCodeIntelWithHome(home string) string {
	if override := strings.TrimSpace(os.Getenv("FABRIKK_CODE_INTEL")); override != "" {
		return expandHomePath(override, home)
	}
	if path, err := exec.LookPath("code_intel.py"); err == nil {
		return path
	}
	for _, candidate := range configuredCodeIntelCandidates(os.Getenv(codeIntelCandidatesEnv), home) {
		if isExecutableFile(candidate) {
			return candidate
		}
	}
	for _, candidate := range codeIntelCandidates(home) {
		if isExecutableFile(candidate) {
			return candidate
		}
	}
	return ""
}

func codeIntelCandidates(home string) []string {
	if home == "" {
		return nil
	}
	return []string{
		filepath.Join(home, ".claude", "skills", "codereview", "scripts", "code_intel.py"),
		filepath.Join(home, "workspaces", "skill-codereview", "scripts", "code_intel.py"),
	}
}

func configuredCodeIntelCandidates(value, home string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	entries := filepath.SplitList(value)
	candidates := make([]string, 0, len(entries)*2)
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		candidate := expandHomePath(entry, home)
		candidates = append(candidates, candidate)
		if filepath.Base(candidate) != "code_intel.py" {
			candidates = append(candidates, filepath.Join(candidate, "code_intel.py"))
		}
	}
	return candidates
}

func expandHomePath(path, home string) string {
	if path == "~" && home != "" {
		return home
	}
	if strings.HasPrefix(path, "~/") && home != "" {
		return filepath.Join(home, strings.TrimPrefix(path, "~/"))
	}
	return path
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode()&0o111 != 0
}

func isUnavailableToolError(err error) bool {
	if errors.Is(err, exec.ErrNotFound) {
		return true
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) && errors.Is(pathErr.Err, fs.ErrNotExist) {
		return true
	}
	return false
}

type limitedBuffer struct {
	limit     int
	truncated bool
	buf       bytes.Buffer
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 {
		return len(p), nil
	}
	remaining := b.limit - b.buf.Len()
	if remaining <= 0 {
		b.truncated = true
		// Report the full write length so exec.Cmd does not fail only because
		// the diagnostic buffer reached its local cap.
		return len(p), nil
	}
	if len(p) > remaining {
		_, _ = b.buf.Write(p[:remaining])
		b.truncated = true
		// Report the full write length so exec.Cmd can keep draining the pipe.
		return len(p), nil
	}
	_, _ = b.buf.Write(p)
	return len(p), nil
}

func (b *limitedBuffer) Bytes() []byte {
	return b.buf.Bytes()
}

func (b *limitedBuffer) String() string {
	if !b.truncated {
		return strings.TrimSpace(b.buf.String())
	}
	return strings.TrimSpace(b.buf.String()) + "...<truncated>"
}
