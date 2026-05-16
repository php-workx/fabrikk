package llmcli

import (
	"errors"
	"testing"

	"github.com/php-workx/fabrikk/llmclient"
)

func TestLabelErrorType(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, "none"},
		{"auth keyword", errors.New("401 unauthorized"), llmclient.ErrTypeAuth},
		{"login keyword", errors.New("not logged in"), llmclient.ErrTypeAuth},
		{"rate_limit underscore", errors.New("rate_limit exceeded"), llmclient.ErrTypeRateLimit},
		{"quota keyword", errors.New("quota exceeded"), llmclient.ErrTypeRateLimit},
		{"bad request keyword", errors.New("bad request: invalid schema"), llmclient.ErrTypeBadRequest},
		{"connection refused", errors.New("connection refused"), llmclient.ErrTypeNetwork},
		{"no such host", errors.New("no such host"), llmclient.ErrTypeNetwork},
		{"upstream keyword", errors.New("upstream error"), llmclient.ErrTypeProvider},
		{"unknown error", errors.New("unknown failure"), llmclient.ErrTypeInternal},
		// auth beats provider when both keywords present
		{"auth beats provider", errors.New("auth failed: upstream provider"), llmclient.ErrTypeAuth},
		// readyNotAuthed hint string contains "provider" — documents current classification
		{"config hint", errors.New("configure a provider in ~/.config/opencode/opencode.json"), llmclient.ErrTypeProvider},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LabelErrorType(tt.err)
			if got != tt.want {
				t.Errorf("LabelErrorType(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}
