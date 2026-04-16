package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/php-workx/fabrikk/internal/state"
)

func TestRunReturnsUsageWhenNoArgs(t *testing.T) {
	var stderr bytes.Buffer

	exitCode := run(context.Background(), nil, &stderr)

	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0", exitCode)
	}
	assertHelpOutput(t, stderr.String())
}

func TestRunReturnsUsageForUnknownCommand(t *testing.T) {
	var stderr bytes.Buffer

	exitCode := run(context.Background(), []string{"mystery"}, &stderr)

	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1", exitCode)
	}
	if !strings.Contains(stderr.String(), "unknown command: mystery") {
		t.Fatalf("stderr = %q, want unknown-command error", stderr.String())
	}
	assertHelpOutput(t, stderr.String())
}

func TestRunReturnsUsageForHelpFlags(t *testing.T) {
	for _, flag := range []string{"help", "--help", "-h"} {
		t.Run(flag, func(t *testing.T) {
			var stderr bytes.Buffer
			exitCode := run(context.Background(), []string{flag}, &stderr)
			if exitCode != 0 {
				t.Fatalf("%s: exit code = %d, want 0", flag, exitCode)
			}
			assertHelpOutput(t, stderr.String())
		})
	}
}

func assertHelpOutput(t *testing.T, output string) {
	t.Helper()
	// Verify header and all group titles are present.
	for _, want := range []string{"fabrikk", "Workflow", "Execution", "Tasks"} {
		if !strings.Contains(output, want) {
			t.Errorf("help output missing %q", want)
		}
	}
	// Verify all commands are listed.
	for _, cmd := range []string{
		"prepare", "review", "artifact", "tech-spec", "plan", "approve",
		"status", "verify", "retry", "report",
		"tasks", "ready", "blocked", "next", "progress",
		"learn", "context",
	} {
		if !strings.Contains(output, cmd) {
			t.Errorf("help output missing command %q", cmd)
		}
	}
}

func TestRunFormatsAskFirstHumanInputGuidance(t *testing.T) {
	baseDir := t.TempDir()
	withWorkingDir(t, baseDir)

	writeRunFixture(t, baseDir, "run-plan-ask-first", runFixture{
		artifact: &state.RunArtifact{
			SchemaVersion: "0.1",
			RunID:         "run-plan-ask-first",
			Requirements:  []state.Requirement{{ID: "AT-FR-001", Text: "The system must ingest specs."}},
			Boundaries: state.Boundaries{
				AskFirst: []string{" confirm rollout window ", "notify compliance"},
			},
		},
	})

	runDir := state.NewRunDir(baseDir, "run-plan-ask-first")
	specContent := []byte("# Spec\n\n## 1. Technical context\nA\n\n## 2. Architecture\nB\n\n## 3. Canonical artifacts and schemas\nC\n\n## 4. Interfaces\nD\n\n## 5. Verification\nE\n\n## 6. Requirement traceability\nF\n\n## 7. Open questions and risks\nG\n\n## 8. Approval\nH\n")
	if err := runDir.WriteTechnicalSpec(specContent); err != nil {
		t.Fatalf("WriteTechnicalSpec: %v", err)
	}
	specHash := "sha256:" + state.SHA256Bytes(specContent)
	if err := runDir.WriteTechnicalSpecApproval(&state.ArtifactApproval{
		SchemaVersion: "0.1",
		RunID:         "run-plan-ask-first",
		ArtifactType:  "technical_spec_approval",
		ArtifactPath:  "technical-spec.md",
		ArtifactHash:  specHash,
		Status:        state.ArtifactApproved,
		ApprovedBy:    "user",
	}); err != nil {
		t.Fatalf("WriteTechnicalSpecApproval: %v", err)
	}

	var stderr bytes.Buffer
	exitCode := run(context.Background(), []string{"plan", "draft", "run-plan-ask-first"}, &stderr)
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1", exitCode)
	}

	output := stderr.String()
	for _, want := range []string{
		"Plan draft requires human input for the following items:",
		"  - confirm rollout window",
		"  - notify compliance",
		"Refresh boundaries from your tech spec and re-run:",
		"  fabrikk boundaries set run-plan-ask-first --from-tech-spec",
		"  fabrikk plan draft run-plan-ask-first",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("stderr = %q, want substring %q", output, want)
		}
	}
	if strings.Contains(output, "error:") {
		t.Fatalf("stderr = %q, want human guidance without generic prefix", output)
	}

	data, err := os.ReadFile(runDir.TechnicalSpec())
	if err != nil {
		t.Fatalf("ReadFile(technical spec): %v", err)
	}
	if !bytes.Equal(data, specContent) {
		t.Fatalf("technical spec changed unexpectedly: %q", string(data))
	}
}

func TestParseBoundariesFromTechSpecHandlesCRLFWithoutWholeFileSplit(t *testing.T) {
	spec := []byte("# Spec\r\n\r\n## Boundaries\r\n\r\n**Always:**\r\n- keep tests focused\r\n\r\n**Ask First:**\r\n- confirm rollout window\r\n\r\n**Never:**\r\n- touch production data\r\n\r\n## Next Section\r\n- ignored\r\n")

	got, err := parseBoundariesFromTechSpec(spec)
	if err != nil {
		t.Fatalf("parseBoundariesFromTechSpec: %v", err)
	}
	if len(got.Always) != 1 || got.Always[0] != "keep tests focused" {
		t.Fatalf("Always = %#v, want keep tests focused", got.Always)
	}
	if len(got.AskFirst) != 1 || got.AskFirst[0] != "confirm rollout window" {
		t.Fatalf("AskFirst = %#v, want confirm rollout window", got.AskFirst)
	}
	if len(got.Never) != 1 || got.Never[0] != "touch production data" {
		t.Fatalf("Never = %#v, want touch production data", got.Never)
	}
}
