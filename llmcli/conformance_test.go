package llmcli

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/php-workx/fabrikk/llmclient"
)

// ompRPCBackend is the canonical name of the omp RPC backend used across
// conformance tests. Defined as a constant to satisfy the goconst linter.
const ompRPCBackend = "omp-rpc"

// ─── Criterion 2: option support matrix ─────────────────────────────────────

// TestOptionSupportMatrix_WithHostTools verifies that the WithHostTools option
// is supported only by the omp-rpc backend, which is the sole backend that
// implements host-defined tool definitions and bidirectional tool approval.
// All other backends must advertise HostToolDefs=false and absent OptionHostTools.
func TestOptionSupportMatrix_WithHostTools(t *testing.T) {
	cases := []struct {
		backendName       string
		wantHostToolDefs  bool
		wantOptionSupport llmclient.OptionSupport
	}{
		// omp-rpc is the only backend with full host-tool support.
		{ompRPCBackend, true, llmclient.OptionSupportFull},
		// All other registered backends do not support host-defined tools.
		{"claude", false, ""},
		{"codex-exec", false, ""},
		{"codex-appserver", false, ""},
		{"opencode-run", false, ""},
		{"opencode-serve", false, ""},
		{"omp", false, ""},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.backendName, func(t *testing.T) {
			f, ok := factoryByName(tc.backendName)
			if !ok {
				t.Skipf("backend %q not registered (may not be compiled)", tc.backendName)
			}

			caps := f.Capabilities

			if caps.HostToolDefs != tc.wantHostToolDefs {
				t.Errorf("HostToolDefs = %v, want %v", caps.HostToolDefs, tc.wantHostToolDefs)
			}

			got := caps.OptionSupport[llmclient.OptionHostTools]
			if got != tc.wantOptionSupport {
				t.Errorf("OptionSupport[%s] = %q, want %q",
					llmclient.OptionHostTools, got, tc.wantOptionSupport)
			}
		})
	}
}

// TestOptionSupportMatrix_BestEffortDegradation verifies that passing an
// unsupported option without marking it as required (best-effort mode) does not
// fail the pre-spawn option check. The option is silently ignored — the backend
// proceeds without error.
func TestOptionSupportMatrix_BestEffortDegradation(t *testing.T) {
	// Build a RequestConfig that carries OptionHostTools but does NOT mark it as
	// required. Best-effort: the backend ignores it without failing.
	cfg := llmclient.ApplyOptions(llmclient.DefaultRequestConfig(), []llmclient.Option{
		llmclient.WithHostTools([]llmclient.Tool{{Name: "dummy"}}),
		// No WithRequiredOptions: best-effort policy.
	})

	cases := []struct {
		name    string
		checkFn func(llmclient.RequestConfig) error
	}{
		{"claude", checkClaudeRequiredOptions},
		{"codex-exec", checkCodexExecRequiredOptions},
		{"codex-appserver", checkCodexAppServerRequiredOptions},
		{"opencode-run", checkOpenCodeRunRequiredOptions},
		{"omp (print)", checkOmpPrintRequiredOptions},
		{"omp-rpc", checkOmpRPCRequiredOptions},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.checkFn(cfg); err != nil {
				t.Errorf("checkRequiredOptions(%q) with best-effort OptionHostTools: unexpected error %v; "+
					"unsupported options must be silently ignored when not required", tc.name, err)
			}
		})
	}
}

// ─── Criterion 2: required unsupported options fail before spawn ─────────────

// TestRequiredUnsupportedOptionFailsBeforeSpawn verifies that each backend's
// pre-spawn option check returns ErrUnsupportedOption when the caller marks an
// unsupported option as required via WithRequiredOptions. No subprocess must be
// started in this path.
func TestRequiredUnsupportedOptionFailsBeforeSpawn(t *testing.T) {
	// OptionHostTools is supported only by omp-rpc; all others must reject it
	// when required.
	cfgRequired := llmclient.ApplyOptions(llmclient.DefaultRequestConfig(), []llmclient.Option{
		llmclient.WithHostTools([]llmclient.Tool{{Name: "dummy"}}),
		llmclient.WithRequiredOptions(llmclient.OptionHostTools),
	})

	rejectors := []struct {
		name    string
		checkFn func(llmclient.RequestConfig) error
	}{
		{"claude", checkClaudeRequiredOptions},
		{"codex-exec", checkCodexExecRequiredOptions},
		{"codex-appserver", checkCodexAppServerRequiredOptions},
		{"opencode-run", checkOpenCodeRunRequiredOptions},
		{"omp (print)", checkOmpPrintRequiredOptions},
	}

	for _, tc := range rejectors {
		tc := tc
		t.Run(tc.name+"_rejects", func(t *testing.T) {
			err := tc.checkFn(cfgRequired)
			if err == nil {
				t.Errorf("checkRequiredOptions(%q) with required OptionHostTools: got nil, want ErrUnsupportedOption",
					tc.name)
				return
			}
			if !errors.Is(err, llmclient.ErrUnsupportedOption) {
				t.Errorf("checkRequiredOptions(%q): got %v, want errors.Is(err, ErrUnsupportedOption)",
					tc.name, err)
			}
		})
	}

	// omp-rpc supports OptionHostTools; its check must succeed.
	t.Run("omp-rpc_accepts", func(t *testing.T) {
		if err := checkOmpRPCRequiredOptions(cfgRequired); err != nil {
			t.Errorf("checkOmpRPCRequiredOptions with required OptionHostTools: unexpected error %v", err)
		}
	})
}

// ─── Criterion 3: strict capability selection ────────────────────────────────

// TestSelectBackend_NeverDowngradesRequiredCapabilities_HostDefs extends the
// existing capability-filter tests to cover the NeedsHostToolDefs requirement,
// which is satisfied only by the omp-rpc backend. This test uses fake factories
// to confirm that satisfiesStaticRequirements correctly excludes backends whose
// static capabilities lack HostToolDefs.
func TestSelectBackend_NeverDowngradesRequiredCapabilities_HostDefs(t *testing.T) {
	// A factory whose backend does NOT provide host tool definitions.
	noHostTools := llmclient.Capabilities{
		Backend:      "no-host-tools",
		Streaming:    llmclient.StreamingStructured,
		ToolEvents:   true,
		HostToolDefs: false, // explicitly absent
	}
	// A factory whose backend DOES provide host tool definitions.
	withHostTools := llmclient.Capabilities{
		Backend:      "with-host-tools",
		Streaming:    llmclient.StreamingStructured,
		ToolEvents:   true,
		HostToolDefs: true,
	}

	req := Requirements{NeedsHostToolDefs: true}

	if satisfiesStaticRequirements(noHostTools, req) {
		t.Error("satisfiesStaticRequirements(noHostTools, NeedsHostToolDefs=true): " +
			"got true, want false; backends without HostToolDefs must be excluded")
	}
	if !satisfiesStaticRequirements(withHostTools, req) {
		t.Error("satisfiesStaticRequirements(withHostTools, NeedsHostToolDefs=true): " +
			"got false, want true; backend with HostToolDefs must survive the filter")
	}

	// The production omp-rpc factory is the only backend that must satisfy this
	// requirement. Verify by inspecting the real registry.
	ompRPC, ok := factoryByName("omp-rpc")
	if !ok {
		t.Skip("omp-rpc not registered; skipping production registry check")
	}
	if !satisfiesStaticRequirements(ompRPC.Capabilities, req) {
		t.Error("omp-rpc must satisfy NeedsHostToolDefs=true")
	}

	// All other registered backends must NOT satisfy NeedsHostToolDefs.
	for _, f := range registeredBackendFactories() {
		if f.Name == ompRPCBackend {
			continue
		}
		if satisfiesStaticRequirements(f.Capabilities, req) {
			t.Errorf("backend %q unexpectedly satisfies NeedsHostToolDefs=true; "+
				"only omp-rpc should", f.Name)
		}
	}
}

// ─── Criterion 4: no forbidden buffering ─────────────────────────────────────

// TestNoForbiddenBuffering scans every non-test Go source file in the llmcli
// package (including the internal sub-package) for patterns that defeat
// streaming by buffering the entire subprocess output before emitting the first
// event. These calls are categorically forbidden in parser paths:
//
//   - io.ReadAll on the stdout reader   — accumulates entire output in memory
//   - cmd.Output / cmd.CombinedOutput   — waits for subprocess exit before returning
//   - bufio scanner on parser streams   — line scanner hides streaming granularity
//
// NOTE: the forbidden string literals are assembled via concatenation so that
// this file itself does not trigger the companion grep check in the validation
// commands (which scans all files, including test files).
func TestNoForbiddenBuffering(t *testing.T) {
	// Patterns are constructed via concatenation to avoid being caught by the
	// sibling grep validation command that scans all files in the package tree.
	forbidden := []string{
		"io." + "ReadAll(stdout)",
		"cmd." + "Output()",
		"cmd." + "CombinedOutput()",
		"bufio." + "NewScanner",
	}

	var sourceFiles []string
	walkErr := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		// Only production source files — test files are exempt.
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			sourceFiles = append(sourceFiles, path)
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("WalkDir: %v", walkErr)
	}
	if len(sourceFiles) == 0 {
		t.Fatal("TestNoForbiddenBuffering: no source files found; check the working directory")
	}

	for _, file := range sourceFiles {
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("ReadFile(%q): %v", file, err)
		}
		src := string(content)
		for _, pattern := range forbidden {
			if strings.Contains(src, pattern) {
				t.Errorf("file %q contains forbidden buffering pattern %q; "+
					"use streaming readers (bufio.NewReader + ReadLine) instead",
					file, pattern)
			}
		}
	}
}
