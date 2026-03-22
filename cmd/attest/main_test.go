package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
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
	for _, want := range []string{"attest", "Workflow", "Execution", "Tasks"} {
		if !strings.Contains(output, want) {
			t.Errorf("help output missing %q", want)
		}
	}
	// Verify all commands are listed.
	for _, cmd := range []string{
		"prepare", "review", "tech-spec", "plan", "approve",
		"status", "verify", "retry", "report",
		"tasks", "ready", "blocked", "next", "progress",
		"learn", "context",
	} {
		if !strings.Contains(output, cmd) {
			t.Errorf("help output missing command %q", cmd)
		}
	}
}
