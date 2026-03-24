package verifier

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/php-workx/fabrikk/internal/state"
)

// RunQualityGate executes the project quality gate command (spec section 11.2, 11.3).
// The quality gate runs first in the verification pipeline. If it fails,
// no model review tokens are spent.
func RunQualityGate(ctx context.Context, gate *state.QualityGate, workDir string) (*state.CommandResult, error) {
	if gate == nil {
		return &state.CommandResult{
			CommandID: "quality-gate",
			Command:   "(none configured)",
			ExitCode:  0,
			Required:  false,
		}, nil
	}

	timeout := time.Duration(gate.TimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = 5 * time.Minute
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	cmd := exec.CommandContext(ctx, "sh", "-c", gate.Command) //nolint:gosec // G204: command comes from the run artifact's quality gate config, not user input
	cmd.Dir = workDir

	output, err := cmd.CombinedOutput()
	duration := time.Since(start)

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, fmt.Errorf("run quality gate: %w", err)
		}
	}

	result := &state.CommandResult{
		CommandID:  "quality-gate",
		Command:    gate.Command,
		Cwd:        workDir,
		ExitCode:   exitCode,
		DurationMs: duration.Milliseconds(),
		Required:   gate.Required,
	}

	if exitCode != 0 && gate.Required {
		return result, fmt.Errorf("quality gate failed (exit %d): %s", exitCode, string(output))
	}

	return result, nil
}
