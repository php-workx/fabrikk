package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/php-workx/fabrikk/internal/agentcli"
	"github.com/php-workx/fabrikk/internal/state"
)

func writeExecutableScript(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
	return path
}

func TestResolveCodeIntelSearchOrder(t *testing.T) {
	tempHome := t.TempDir()
	pathDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(pathDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(pathDir): %v", err)
	}

	envTool := writeExecutableScript(t, t.TempDir(), "env-code-intel.py", "#!/bin/sh\nexit 0\n")
	pathTool := writeExecutableScript(t, pathDir, "code_intel.py", "#!/bin/sh\nexit 0\n")
	commonToolDir := filepath.Join(tempHome, ".claude", "skills", "codereview", "scripts")
	if err := os.MkdirAll(commonToolDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(commonToolDir): %v", err)
	}
	commonTool := writeExecutableScript(t, commonToolDir, "code_intel.py", "#!/bin/sh\nexit 0\n")

	t.Setenv("PATH", pathDir)
	t.Setenv("FABRIKK_CODE_INTEL", envTool)
	if got := resolveCodeIntelWithHome(tempHome); got != envTool {
		t.Fatalf("resolveCodeIntelWithHome() with env = %q, want %q", got, envTool)
	}

	t.Setenv("FABRIKK_CODE_INTEL", "")
	if got := resolveCodeIntelWithHome(tempHome); got != pathTool {
		t.Fatalf("resolveCodeIntelWithHome() with PATH = %q, want %q", got, pathTool)
	}

	t.Setenv("PATH", t.TempDir())
	if got := resolveCodeIntelWithHome(tempHome); got != commonTool {
		t.Fatalf("resolveCodeIntelWithHome() with common locations = %q, want %q", got, commonTool)
	}
}

func TestResolveCodeIntelWithHomeUnavailable(t *testing.T) {
	t.Setenv("FABRIKK_CODE_INTEL", "")
	t.Setenv("PATH", t.TempDir())
	if got := resolveCodeIntelWithHome(t.TempDir()); got != "" {
		t.Fatalf("resolveCodeIntelWithHome() = %q, want empty", got)
	}
}

func TestRunStructuralAnalysisParsesJSON(t *testing.T) {
	workDir := t.TempDir()
	t.Setenv("EXPECTED_ROOT", workDir)
	script := writeExecutableScript(t, t.TempDir(), "code_intel.py", `#!/bin/sh
if [ "$1" != "graph" ] || [ "$2" != "--root" ] || [ "$3" != "$EXPECTED_ROOT" ] || [ "$4" != "--json" ]; then
	echo "unexpected args: $*" >&2
	exit 9
fi
printf '%s' '{"nodes":[{"id":"internal/engine/explore.go::runStructuralAnalysis","kind":"function","file":"internal/engine/explore.go","line":12,"exported":false}],"edges":[{"from":"internal/engine/explore.go::runStructuralAnalysis","to":"internal/state/types.go::StructuralAnalysis","type":"calls","score":0.75}]}'
`)
	t.Setenv("FABRIKK_CODE_INTEL", script)

	eng := New(nil, workDir)
	analysis, err := eng.runStructuralAnalysis(context.Background())
	if err != nil {
		t.Fatalf("runStructuralAnalysis: %v", err)
	}
	if analysis == nil {
		t.Fatal("runStructuralAnalysis returned nil analysis")
	}
	if len(analysis.Nodes) != 1 || analysis.Nodes[0].ID != "internal/engine/explore.go::runStructuralAnalysis" {
		t.Fatalf("analysis.Nodes = %#v", analysis.Nodes)
	}
	if len(analysis.Edges) != 1 || analysis.Edges[0].Type != "calls" {
		t.Fatalf("analysis.Edges = %#v", analysis.Edges)
	}
}

func TestRunStructuralAnalysisReturnsNilWhenUnavailable(t *testing.T) {
	t.Setenv("FABRIKK_CODE_INTEL", filepath.Join(t.TempDir(), "missing-code-intel.py"))
	eng := New(nil, t.TempDir())
	analysis, err := eng.runStructuralAnalysis(context.Background())
	if err != nil {
		t.Fatalf("runStructuralAnalysis: %v", err)
	}
	if analysis != nil {
		t.Fatalf("runStructuralAnalysis returned %#v, want nil", analysis)
	}
}

func TestRunStructuralAnalysisReturnsParseError(t *testing.T) {
	script := writeExecutableScript(t, t.TempDir(), "code_intel.py", "#!/bin/sh\nprintf '%s' '{invalid json'\n")
	t.Setenv("FABRIKK_CODE_INTEL", script)

	eng := New(nil, t.TempDir())
	analysis, err := eng.runStructuralAnalysis(context.Background())
	if err == nil {
		t.Fatal("runStructuralAnalysis error = nil, want parse error")
	}
	if analysis != nil {
		t.Fatalf("runStructuralAnalysis returned %#v, want nil", analysis)
	}
	if !strings.Contains(err.Error(), "parse structural analysis") {
		t.Fatalf("runStructuralAnalysis error = %q, want parse context", err)
	}
}

func TestRunStructuralAnalysisReturnsProcessError(t *testing.T) {
	script := writeExecutableScript(t, t.TempDir(), "code_intel.py", "#!/bin/sh\necho 'boom' >&2\nexit 17\n")
	t.Setenv("FABRIKK_CODE_INTEL", script)

	eng := New(nil, t.TempDir())
	analysis, err := eng.runStructuralAnalysis(context.Background())
	if err == nil {
		t.Fatal("runStructuralAnalysis error = nil, want process error")
	}
	if analysis != nil {
		t.Fatalf("runStructuralAnalysis returned %#v, want nil", analysis)
	}
	if !strings.Contains(err.Error(), "exit status 17") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("runStructuralAnalysis error = %q, want exit code and stderr", err)
	}
}

func TestBuildExplorationPromptWithStructuralAnalysis(t *testing.T) {
	artifact := &state.RunArtifact{
		Requirements: []state.Requirement{{ID: "AT-FR-001", Text: "Map the plan to concrete symbols."}},
	}
	sa := &state.StructuralAnalysis{
		Nodes: []state.StructuralNode{{ID: "internal/engine/plan.go::DraftExecutionPlan", Kind: "function", File: "internal/engine/plan.go", Line: 23, Exported: true}},
	}

	prompt := buildExplorationPrompt(artifact, sa)

	for _, want := range []string{"## Codebase Structure", "AT-FR-001", "DraftExecutionPlan", "Timeout budget: 180s", "Return ONLY valid JSON", "\"file_inventory\"", "\"reuse_points\""} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
	if strings.Contains(prompt, "## Discovery Strategy") {
		t.Fatal("structural prompt should not include fallback discovery strategy")
	}
}

func TestBuildExplorationPromptWithoutStructuralAnalysis(t *testing.T) {
	artifact := &state.RunArtifact{
		Requirements: []state.Requirement{{ID: "AT-TS-001", Text: "Explore the repo without structural analysis."}},
	}

	prompt := buildExplorationPrompt(artifact, nil)

	for _, want := range []string{"## Discovery Strategy", "List the relevant source directories", "Grep for requirement keywords", "Read the key files", "Timeout budget: 360s", "AT-TS-001", "Return ONLY valid JSON"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
	if strings.Contains(prompt, "## Codebase Structure") {
		t.Fatal("fallback prompt should not include structural JSON section")
	}
}

func TestExtractExplorationJSONPrefersCodeFence(t *testing.T) {
	raw := "before\n```json\n{\"file_inventory\":[]}\n```\nafter"
	if got := extractExplorationJSON(raw); got != `{"file_inventory":[]}` {
		t.Fatalf("extractExplorationJSON() = %q", got)
	}
}

func TestExploreForPlanReturnsValidatedResultAndPersists(t *testing.T) {
	baseDir := t.TempDir()
	existingRel := filepath.Join("internal", "engine", "plan.go")
	existingPath := filepath.Join(baseDir, existingRel)
	if err := os.MkdirAll(filepath.Dir(existingPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(existingPath, []byte("package engine\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(existingPath): %v", err)
	}

	runDir := state.NewRunDir(baseDir, "run-explore")
	if err := runDir.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	eng := New(runDir, baseDir)
	eng.InvokeFn = func(_ context.Context, _ *agentcli.CLIBackend, prompt string, timeoutSec int) (string, error) {
		if timeoutSec != 360 {
			t.Fatalf("timeoutSec = %d, want 360 for fallback exploration", timeoutSec)
		}
		if !strings.Contains(prompt, "## Discovery Strategy") {
			t.Fatal("fallback exploration prompt missing discovery strategy")
		}
		return "```json\n{\"file_inventory\":[{\"path\":\"" + existingRel + "\",\"exists\":false,\"is_new\":true,\"line_count\":1,\"language\":\"go\"},{\"path\":\"internal/missing.go\",\"exists\":true,\"is_new\":false,\"line_count\":0,\"language\":\"go\"}],\"symbols\":[{\"file_path\":\"" + existingRel + "\",\"line\":1,\"kind\":\"func\",\"signature\":\"func DraftExecutionPlan()\"},{\"file_path\":\"internal/ghost.go\",\"line\":1,\"kind\":\"func\",\"signature\":\"func Ghost()\"}],\"test_files\":[{\"path\":\"internal/ghost_test.go\",\"test_names\":[\"TestGhost\"],\"covers\":\"internal/ghost.go\"}],\"reuse_points\":[{\"file_path\":\"" + existingRel + "\",\"line\":1,\"symbol\":\"DraftExecutionPlan\",\"relevance\":\"existing planner entry point\"},{\"file_path\":\"internal/ghost.go\",\"line\":1,\"symbol\":\"Ghost\",\"relevance\":\"hallucinated\"}]}\n```", nil
	}

	artifact := &state.RunArtifact{Requirements: []state.Requirement{{ID: "AT-FR-001", Text: "Map requirements to symbols."}}}
	result, err := eng.exploreForPlan(context.Background(), artifact, nil)
	if err != nil {
		t.Fatalf("exploreForPlan: %v", err)
	}
	if result == nil {
		t.Fatal("exploreForPlan returned nil result")
	}
	if len(result.FileInventory) != 1 || !result.FileInventory[0].Exists || result.FileInventory[0].IsNew {
		t.Fatalf("file inventory = %#v, want only the existing file entry", result.FileInventory)
	}
	if len(result.Symbols) != 1 || result.Symbols[0].FilePath != existingRel {
		t.Fatalf("validated symbols = %#v, want only existing symbol", result.Symbols)
	}
	if len(result.ReusePoints) != 1 || result.ReusePoints[0].FilePath != existingRel {
		t.Fatalf("validated reuse points = %#v, want only existing reuse point", result.ReusePoints)
	}
	if len(result.TestFiles) != 0 {
		t.Fatalf("validated test files = %#v, want hallucinated entry dropped", result.TestFiles)
	}
	if _, err := os.Stat(runDir.Path("exploration.json")); err != nil {
		t.Fatalf("expected persisted exploration.json: %v", err)
	}
}

func TestExploreForPlanReturnsNilOnInvokeError(t *testing.T) {
	eng := New(nil, t.TempDir())
	eng.InvokeFn = func(_ context.Context, _ *agentcli.CLIBackend, _ string, _ int) (string, error) {
		return "", os.ErrPermission
	}

	artifact := &state.RunArtifact{Requirements: []state.Requirement{{ID: "AT-FR-001", Text: "Map requirements to symbols."}}}
	result, err := eng.exploreForPlan(context.Background(), artifact, nil)
	if err != nil {
		t.Fatalf("exploreForPlan: %v", err)
	}
	if result != nil {
		t.Fatalf("exploreForPlan result = %#v, want nil", result)
	}
}

func TestExploreForPlanReturnsNilOnInvalidJSON(t *testing.T) {
	eng := New(nil, t.TempDir())
	eng.InvokeFn = func(_ context.Context, _ *agentcli.CLIBackend, _ string, _ int) (string, error) {
		return "not-json", nil
	}

	artifact := &state.RunArtifact{Requirements: []state.Requirement{{ID: "AT-FR-001", Text: "Map requirements to symbols."}}}
	result, err := eng.exploreForPlan(context.Background(), artifact, nil)
	if err != nil {
		t.Fatalf("exploreForPlan: %v", err)
	}
	if result != nil {
		t.Fatalf("exploreForPlan result = %#v, want nil", result)
	}
}

func TestValidateExplorationResultPreservesNewTestGuidance(t *testing.T) {
	baseDir := t.TempDir()
	coveredRel := filepath.Join("internal", "engine", "plan.go")
	coveredPath := filepath.Join(baseDir, coveredRel)
	if err := os.MkdirAll(filepath.Dir(coveredPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(coveredPath, []byte("package engine\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(coveredPath): %v", err)
	}
	outsidePath := filepath.Join(t.TempDir(), "outside.go")
	if err := os.WriteFile(outsidePath, []byte("package outside\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(outsidePath): %v", err)
	}

	eng := New(nil, baseDir)
	result := &state.ExplorationResult{
		FileInventory: []state.FileInfo{
			{Path: coveredRel, Exists: true, IsNew: false},
			{Path: outsidePath, Exists: true, IsNew: false},
			{Path: filepath.Join("internal", "ghost.go"), Exists: false, IsNew: false},
		},
		TestFiles: []state.TestFileInfo{
			{Path: filepath.Join("internal", "engine", "plan_new_test.go"), TestNames: []string{"TestPlanNew"}, Covers: coveredRel},
			{Path: filepath.Join("internal", "engine", "ghost_test.go"), TestNames: []string{"TestGhost"}, Covers: filepath.Join("internal", "ghost.go")},
		},
	}

	eng.validateExplorationResult(result)

	if len(result.FileInventory) != 1 || result.FileInventory[0].Path != coveredRel {
		t.Fatalf("validated file inventory = %#v, want only existing path", result.FileInventory)
	}
	if len(result.TestFiles) != 1 || result.TestFiles[0].Path != filepath.Join("internal", "engine", "plan_new_test.go") {
		t.Fatalf("validated test files = %#v, want new test guidance preserved", result.TestFiles)
	}
}
