package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/php-workx/fabrikk/llmclient"
)

// Built-in op names supported by the shipped llmcli binary.
const (
	OpReady      = "ready"
	OpText       = "text"
	OpJSON       = "json"
	OpStructured = "structured"
)

type collectRequest struct {
	Context          *llmclient.Context     `json:"context,omitempty"`
	Model            string                 `json:"model,omitempty"`
	SessionID        string                 `json:"session_id,omitempty"`
	Temperature      *float64               `json:"temperature,omitempty"`
	WorkingDirectory string                 `json:"working_directory,omitempty"`
	TimeoutMS        int                    `json:"timeout_ms,omitempty"`
	ReasoningEffort  string                 `json:"reasoning_effort,omitempty"`
	JSONSchema       map[string]interface{} `json:"json_schema,omitempty"`
}

type readyResult struct {
	Backend string `json:"backend"`
	State   string `json:"state"`
	Detail  string `json:"detail,omitempty"`
}

type textResult struct {
	Text   string `json:"text"`
	Reason string `json:"reason,omitempty"`
}

type jsonResult struct {
	Raw   string          `json:"raw"`
	Value json.RawMessage `json:"value"`
}

// RegisterBuiltinHandlers registers the generic ops shipped by cmd/llmcli.
func RegisterBuiltinHandlers() {
	Register(HandlerFunc{Operation: OpReady, Fn: handleReady})
	Register(HandlerFunc{Operation: OpText, Fn: handleText})
	Register(HandlerFunc{Operation: OpJSON, Fn: handleJSON})
	Register(HandlerFunc{Operation: OpStructured, Fn: handleStructured})
}

func handleReady(ctx context.Context, _ Request, deps Deps) (json.RawMessage, error) {
	report := deps.Backend.Ready(ctx)
	return marshalResult(readyResult{
		Backend: deps.Backend.Name(),
		State:   report.State.String(),
		Detail:  report.Detail,
	})
}

func handleText(ctx context.Context, req Request, deps Deps) (json.RawMessage, error) {
	input, opts, err := collectInput(req)
	if err != nil {
		return nil, err
	}
	ch, err := deps.Backend.Stream(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	text, reason, err := llmclient.Collect(ctx, ch)
	if err != nil {
		return nil, err
	}
	return marshalResult(textResult{Text: text, Reason: string(reason)})
}

func handleJSON(ctx context.Context, req Request, deps Deps) (json.RawMessage, error) {
	input, opts, err := collectInput(req)
	if err != nil {
		return nil, err
	}
	var value json.RawMessage
	raw, err := llmclient.CollectJSON(ctx, deps.Backend, input, &value, opts...)
	if err != nil {
		return nil, err
	}
	return marshalResult(jsonResult{Raw: raw, Value: value})
}

func handleStructured(ctx context.Context, req Request, deps Deps) (json.RawMessage, error) {
	input, opts, err := collectInput(req)
	if err != nil {
		return nil, err
	}
	ch, err := deps.Backend.Stream(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	result, err := llmclient.CollectStructured(ctx, ch)
	if err != nil {
		return nil, err
	}
	return marshalResult(result)
}

func collectInput(req Request) (*llmclient.Context, []llmclient.Option, error) {
	var payload collectRequest
	if len(req.Payload) > 0 {
		if err := json.Unmarshal(req.Payload, &payload); err != nil {
			return nil, nil, &llmclient.EventErrorError{
				Type:    llmclient.ErrTypeBadRequest,
				Message: fmt.Sprintf("invalid payload: %s", err),
			}
		}
	}

	input := payload.Context
	if len(req.Context) > 0 {
		input = new(llmclient.Context)
		if err := json.Unmarshal(req.Context, input); err != nil {
			return nil, nil, &llmclient.EventErrorError{
				Type:    llmclient.ErrTypeBadRequest,
				Message: fmt.Sprintf("invalid context: %s", err),
			}
		}
	}
	if input == nil {
		return nil, nil, &llmclient.EventErrorError{
			Type:    llmclient.ErrTypeBadRequest,
			Message: "context is required",
		}
	}

	return input, collectOptions(payload), nil
}

func collectOptions(payload collectRequest) []llmclient.Option {
	var opts []llmclient.Option
	if payload.Model != "" {
		opts = append(opts, llmclient.WithModel(payload.Model))
	}
	if payload.SessionID != "" {
		opts = append(opts, llmclient.WithSession(payload.SessionID))
	}
	if payload.Temperature != nil {
		opts = append(opts, llmclient.WithTemperature(*payload.Temperature))
	}
	if payload.WorkingDirectory != "" {
		opts = append(opts, llmclient.WithWorkingDirectory(payload.WorkingDirectory))
	}
	if payload.TimeoutMS > 0 {
		opts = append(opts, llmclient.WithTimeout(time.Duration(payload.TimeoutMS)*time.Millisecond))
	}
	if payload.ReasoningEffort != "" {
		opts = append(opts, llmclient.WithReasoningEffort(payload.ReasoningEffort))
	}
	if payload.JSONSchema != nil {
		opts = append(opts, llmclient.WithJSONSchema(payload.JSONSchema))
	}
	return opts
}

func marshalResult(v interface{}) (json.RawMessage, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return data, nil
}
