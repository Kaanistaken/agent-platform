// Package openai is the OpenAI adapter, using the Responses API (D73) with
// store: false and the full history on every turn (D83).
//
// The SDK carries the request and parses the stream; the request body is set
// field by field with option.WithJSONSet so that the provider's own output
// items can be sent back verbatim (D96).
package openai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"

	"github.com/kaang/vodafone-case/backend/internal/provider"
)

// Adapter implements provider.Provider.
type Adapter struct {
	opts []option.RequestOption // extra options, e.g. a test server's base URL
}

// New returns the adapter. Test code may pass option.WithBaseURL.
func New(opts ...option.RequestOption) *Adapter { return &Adapter{opts: opts} }

// Stream makes one streaming call. Retries are disabled in the SDK; the agent
// loop owns them (D79).
func (a *Adapter) Stream(ctx context.Context, req provider.Request, cb provider.Callbacks) (provider.Result, error) {
	input, err := buildInput(req.Messages)
	if err != nil {
		return provider.Result{}, &provider.Error{Class: provider.Permanent, Code: "internal", Msg: err.Error()}
	}
	tools := make([]map[string]any, 0, len(req.Tools))
	for _, t := range req.Tools {
		tools = append(tools, map[string]any{"type": "function", "name": t.Name, "description": t.Description,
			"parameters": t.Parameters, "strict": false})
	}
	opts := append([]option.RequestOption{
		option.WithAPIKey(req.APIKey), // explicit: never an environment key (D12)
		option.WithMaxRetries(0),
		option.WithJSONSet("model", req.Model),
		option.WithJSONSet("instructions", req.System),
		option.WithJSONSet("input", input),
		option.WithJSONSet("store", false),
		option.WithJSONSet("parallel_tool_calls", false), // D64
		option.WithJSONSet("max_output_tokens", req.MaxOutputTokens),
		option.WithJSONSet("include", []string{"reasoning.encrypted_content"}), // replayable reasoning with store: false
	}, a.opts...)
	if len(tools) > 0 {
		opts = append(opts, option.WithJSONSet("tools", tools), option.WithJSONSet("tool_choice", "auto"))
	}

	client := sdk.NewClient(opts...)
	stream := client.Responses.NewStreaming(ctx, responses.ResponseNewParams{})
	defer stream.Close()

	for stream.Next() {
		var ev event
		if err := json.Unmarshal([]byte(stream.Current().RawJSON()), &ev); err != nil {
			continue
		}
		switch ev.Type {
		case "response.output_text.delta":
			if cb.OnText != nil {
				cb.OnText(ev.Delta)
			}
		case "response.completed":
			reportUsage(cb, ev.Response.Usage)
			return result(ev.Response)
		case "response.incomplete":
			reportUsage(cb, ev.Response.Usage)
			reason := ev.Response.IncompleteDetails.Reason
			// D94: incomplete output fails the run; truncated calls never run.
			return provider.Result{}, &provider.Error{Class: provider.Permanent, Code: "incomplete_output",
				Msg: "the model's answer was cut off (" + reason + ")"}
		case "response.failed":
			reportUsage(cb, ev.Response.Usage)
			return provider.Result{}, streamError(ev.Response.Error.Code, ev.Response.Error.Message)
		case "error":
			return provider.Result{}, streamError(ev.Code, ev.Message)
		}
	}
	if err := stream.Err(); err != nil {
		return provider.Result{}, classify(err)
	}
	return provider.Result{}, &provider.Error{Class: provider.Transient, Code: "provider_error",
		Msg: "the stream ended before the response was complete"}
}

// event is the part of a Responses stream event the adapter reads.
type event struct {
	Type     string   `json:"type"`
	Delta    string   `json:"delta"`
	Code     string   `json:"code"`
	Message  string   `json:"message"`
	Response response `json:"response"`
}

type response struct {
	Output            []json.RawMessage `json:"output"`
	Usage             *usage            `json:"usage"`
	IncompleteDetails struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type usage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

func reportUsage(cb provider.Callbacks, u *usage) {
	if u != nil && cb.OnUsage != nil {
		cb.OnUsage(provider.Usage{Input: u.InputTokens, Output: u.OutputTokens, Known: true})
	}
}

// result reads the text and function calls from the output items and keeps
// the items themselves as the raw content to send back next turn (D96).
func result(r response) (provider.Result, error) {
	var res provider.Result
	var text strings.Builder
	for _, raw := range r.Output {
		var item struct {
			Type      string `json:"type"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
			Content   []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			continue
		}
		switch item.Type {
		case "message":
			for _, c := range item.Content {
				if c.Type == "output_text" {
					text.WriteString(c.Text)
				}
			}
		case "function_call":
			res.ToolCalls = append(res.ToolCalls, provider.ToolCall{ID: item.CallID, Name: item.Name, Arguments: item.Arguments})
		}
	}
	res.Text = text.String()
	rawItems, err := json.Marshal(r.Output)
	if err != nil {
		return provider.Result{}, err
	}
	res.Raw = rawItems
	return res, nil
}

// buildInput turns the history into Responses input items.
func buildInput(msgs []provider.Message) ([]json.RawMessage, error) {
	var items []json.RawMessage
	add := func(v any) error {
		b, err := json.Marshal(v)
		items = append(items, b)
		return err
	}
	for _, m := range msgs {
		switch m.Role {
		case "user":
			if err := add(map[string]any{"role": "user", "content": m.Content}); err != nil {
				return nil, err
			}
		case "assistant":
			if len(m.Raw) > 0 {
				var raw []json.RawMessage
				if err := json.Unmarshal(m.Raw, &raw); err != nil {
					return nil, err
				}
				items = append(items, raw...) // verbatim (D96)
				continue
			}
			if m.Content != "" {
				add(map[string]any{"role": "assistant", "content": m.Content})
			}
			for _, c := range m.ToolCalls {
				add(map[string]any{"type": "function_call", "call_id": c.ID, "name": c.Name, "arguments": c.Arguments})
			}
		case "tool":
			if err := add(map[string]any{"type": "function_call_output", "call_id": m.ToolCallID, "output": m.Content}); err != nil {
				return nil, err
			}
		}
	}
	return items, nil
}

// streamError classifies an error reported inside the stream (D94).
func streamError(code, msg string) error {
	switch code {
	case "server_error", "rate_limit_exceeded", "vector_store_timeout":
		return &provider.Error{Class: provider.Transient, Code: "provider_error", Msg: "OpenAI: " + msg}
	case "insufficient_quota":
		return &provider.Error{Class: provider.Permanent, Code: "quota_exhausted", Msg: "OpenAI: " + msg}
	case "context_length_exceeded":
		return &provider.Error{Class: provider.Permanent, Code: "context_too_long", Msg: "OpenAI: " + msg}
	}
	return &provider.Error{Class: provider.Permanent, Code: "provider_error", Msg: "OpenAI: " + code + ": " + msg}
}

// classify maps an HTTP or network error to transient or permanent (D79, D94).
func classify(err error) error {
	var apiErr *sdk.Error
	if !errors.As(err, &apiErr) {
		if errors.Is(err, context.Canceled) {
			return err
		}
		return &provider.Error{Class: provider.Transient, Code: "provider_error", Msg: "network error talking to OpenAI"}
	}
	if apiErr.StatusCode == http.StatusOK {
		// An "error" event inside the stream, surfaced by the SDK (D94).
		return streamError(apiErr.Code, apiErr.Message)
	}
	pe := &provider.Error{Status: apiErr.StatusCode, Msg: "OpenAI: " + apiErr.Message}
	if apiErr.Message == "" {
		pe.Msg = "OpenAI: " + http.StatusText(apiErr.StatusCode)
	}
	if apiErr.Response != nil {
		if d, ok := retryAfter(apiErr.Response.Header); ok {
			pe.RetryAfter, pe.HasRetryAfter = d, true
		}
	}
	code := apiErr.Code
	if apiErr.StatusCode == http.StatusUnauthorized {
		// The provider's message may quote part of the key; it is not stored
		// or streamed (R7).
		pe.Msg = "OpenAI rejected the API key (HTTP 401): check the key under Settings"
	}
	switch {
	case apiErr.StatusCode == 429 && (code == "insufficient_quota" || strings.Contains(code, "billing")):
		pe.Class, pe.Code = provider.Permanent, "quota_exhausted"
	case apiErr.StatusCode == 429:
		pe.Class, pe.Code = provider.Transient, "rate_limited"
	case code == "context_length_exceeded":
		pe.Class, pe.Code = provider.Permanent, "context_too_long"
	case apiErr.StatusCode == 408 || apiErr.StatusCode == 409 || apiErr.StatusCode >= 500:
		pe.Class, pe.Code = provider.Transient, "provider_error"
	default: // 400, 401, 403, 404 and other client errors cannot succeed on retry
		pe.Class, pe.Code = provider.Permanent, "provider_error"
	}
	return pe
}

func retryAfter(h http.Header) (time.Duration, bool) {
	if v := h.Get("Retry-After"); v != "" {
		if s, err := strconv.ParseFloat(v, 64); err == nil {
			return time.Duration(s * float64(time.Second)), true
		}
		if t, err := http.ParseTime(v); err == nil {
			return time.Until(t), true
		}
	}
	return 0, false
}
