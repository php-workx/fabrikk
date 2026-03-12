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

	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1", exitCode)
	}
	if !strings.Contains(stderr.String(), "usage: attest <command> [args]") {
		t.Fatalf("stderr = %q, want usage text", stderr.String())
	}
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
	if !strings.Contains(stderr.String(), "usage: attest <command> [args]") {
		t.Fatalf("stderr = %q, want usage text", stderr.String())
	}
}
