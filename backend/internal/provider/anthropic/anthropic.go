// Package anthropic is the Anthropic Messages API adapter (D8).
//
// Content blocks are rebuilt from the stream events, including thinking blocks
// with their signatures, and sent back unchanged on the next turn (D96).
package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/kaang/vodafone-case/backend/internal/provider"
)

// Adapter implements provider.Provider.
type Adapter struct {
	opts []option.RequestOption
}

// New returns the adapter. Test code may pass option.WithBaseURL.
func New(opts ...option.RequestOption) *Adapter { return &Adapter{opts: opts} }

// Stream makes one streaming call; the SDK does not retry (D79).
func (a *Adapter) Stream(ctx context.Context, req provider.Request, cb provider.Callbacks) (provider.Result, error) {
	msgs, err := buildMessages(req.Messages)
	if err != nil {
		return provider.Result{}, &provider.Error{Class: provider.Permanent, Code: "internal", Msg: err.Error()}
	}
	opts := append([]option.RequestOption{
		// Only the starter's key: the SDK's environment credentials
		// (ANTHROPIC_API_KEY, ANTHROPIC_AUTH_TOKEN, profiles, base URL) are
		// switched off, so no second credential is sent (R7, D12).
		option.WithoutEnvironmentDefaults(),
		option.WithAPIKey(req.APIKey),
		option.WithMaxRetries(0),
		option.WithJSONSet("model", req.Model),
		option.WithJSONSet("max_tokens", req.MaxOutputTokens),
		option.WithJSONSet("messages", msgs),
	}, a.opts...)
	if req.System != "" {
		opts = append(opts, option.WithJSONSet("system", req.System))
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{"name": t.Name, "description": t.Description, "input_schema": t.Parameters})
		}
		opts = append(opts, option.WithJSONSet("tools", tools),
			option.WithJSONSet("tool_choice", map[string]any{"type": "auto", "disable_parallel_tool_use": true})) // D64, D89
	}

	client := sdk.NewClient(opts...)
	stream := client.Messages.NewStreaming(ctx, sdk.MessageNewParams{})
	defer stream.Close()

	var blocks []*block
	var use provider.Usage
	stop := ""
	for stream.Next() {
		var ev event
		if err := json.Unmarshal([]byte(stream.Current().RawJSON()), &ev); err != nil {
			continue
		}
		switch ev.Type {
		case "message_start":
			// Counters are cumulative; the latest value is stored (D94).
			use = provider.Usage{Input: ev.Message.Usage.InputTokens, Output: ev.Message.Usage.OutputTokens, Known: true}
			report(cb, use)
		case "content_block_start":
			b := &block{}
			json.Unmarshal(ev.ContentBlock, &b.start)
			for int(ev.Index) >= len(blocks) {
				blocks = append(blocks, nil)
			}
			blocks[ev.Index] = b
		case "content_block_delta":
			if int(ev.Index) >= len(blocks) || blocks[ev.Index] == nil {
				continue
			}
			b := blocks[ev.Index]
			switch ev.Delta.Type {
			case "text_delta":
				b.text.WriteString(ev.Delta.Text)
				if cb.OnText != nil {
					cb.OnText(ev.Delta.Text)
				}
			case "input_json_delta":
				b.json.WriteString(ev.Delta.PartialJSON)
			case "thinking_delta":
				b.thinking.WriteString(ev.Delta.Thinking)
			case "signature_delta":
				b.signature += ev.Delta.Signature
			}
		case "message_delta":
			stop = ev.Delta.StopReason
			if ev.Usage.InputTokens > 0 {
				use.Input = ev.Usage.InputTokens
			}
			use.Output = ev.Usage.OutputTokens
			use.Known = true
			report(cb, use)
		case "error":
			return provider.Result{}, streamError(ev.Error.Type, ev.Error.Message)
		case "message_stop":
			return finish(blocks, stop)
		}
	}
	if err := stream.Err(); err != nil {
		return provider.Result{}, classify(err)
	}
	return provider.Result{}, &provider.Error{Class: provider.Transient, Code: "provider_error",
		Msg: "the stream ended before the message was complete"}
}

type event struct {
	Type         string          `json:"type"`
	Index        int64           `json:"index"`
	ContentBlock json.RawMessage `json:"content_block"`
	Message      struct {
		Usage struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
	Delta struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		PartialJSON string `json:"partial_json"`
		Thinking    string `json:"thinking"`
		Signature   string `json:"signature"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Usage struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	} `json:"usage"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// block accumulates one content block from its start event and deltas.
type block struct {
	start     map[string]any
	text      strings.Builder
	json      strings.Builder
	thinking  strings.Builder
	signature string
}

func report(cb provider.Callbacks, u provider.Usage) {
	if cb.OnUsage != nil {
		cb.OnUsage(u)
	}
}

// finish builds the result and the raw content blocks to send back (D96).
func finish(blocks []*block, stop string) (provider.Result, error) {
	if stop == "max_tokens" {
		return provider.Result{}, &provider.Error{Class: provider.Permanent, Code: "incomplete_output",
			Msg: "the model's answer was cut off (max_tokens)"}
	}
	var res provider.Result
	var text strings.Builder
	var content []map[string]any
	for _, b := range blocks {
		if b == nil {
			continue
		}
		typ, _ := b.start["type"].(string)
		out := map[string]any{}
		for k, v := range b.start {
			out[k] = v
		}
		switch typ {
		case "text":
			out["text"] = b.text.String()
			text.WriteString(b.text.String())
		case "tool_use":
			args := b.json.String()
			if strings.TrimSpace(args) == "" {
				args = "{}"
			}
			var input any
			if err := json.Unmarshal([]byte(args), &input); err != nil {
				input = map[string]any{} // malformed: the loop reports it to the model (R3)
			}
			out["input"] = input
			id, _ := b.start["id"].(string)
			name, _ := b.start["name"].(string)
			res.ToolCalls = append(res.ToolCalls, provider.ToolCall{ID: id, Name: name, Arguments: args})
		case "thinking":
			out["thinking"] = b.thinking.String()
			out["signature"] = b.signature
		}
		content = append(content, out)
	}
	res.Text = text.String()
	raw, err := json.Marshal(content)
	if err != nil {
		return provider.Result{}, err
	}
	res.Raw = raw
	return res, nil
}

// buildMessages turns the history into Messages API messages.
func buildMessages(msgs []provider.Message) ([]map[string]any, error) {
	var out []map[string]any
	for _, m := range msgs {
		switch m.Role {
		case "user":
			out = append(out, map[string]any{"role": "user", "content": m.Content})
		case "assistant":
			if len(m.Raw) > 0 {
				var content []json.RawMessage
				if err := json.Unmarshal(m.Raw, &content); err != nil {
					return nil, err
				}
				out = append(out, map[string]any{"role": "assistant", "content": content}) // verbatim (D96)
				continue
			}
			var content []map[string]any
			if m.Content != "" {
				content = append(content, map[string]any{"type": "text", "text": m.Content})
			}
			for _, c := range m.ToolCalls {
				var input any = map[string]any{}
				json.Unmarshal([]byte(c.Arguments), &input)
				content = append(content, map[string]any{"type": "tool_use", "id": c.ID, "name": c.Name, "input": input})
			}
			out = append(out, map[string]any{"role": "assistant", "content": content})
		case "tool":
			out = append(out, map[string]any{"role": "user", "content": []map[string]any{
				{"type": "tool_result", "tool_use_id": m.ToolCallID, "content": m.Content}}})
		}
	}
	return out, nil
}

// streamError classifies an error event inside the stream (D94).
func streamError(typ, msg string) error {
	switch typ {
	case "overloaded_error", "api_error", "rate_limit_error":
		return &provider.Error{Class: provider.Transient, Code: "provider_error", Msg: "Anthropic: " + msg}
	}
	return &provider.Error{Class: provider.Permanent, Code: "provider_error", Msg: "Anthropic: " + typ + ": " + msg}
}

// classify maps an HTTP or network error (D79, D94).
func classify(err error) error {
	var apiErr *sdk.Error
	if !errors.As(err, &apiErr) {
		if errors.Is(err, context.Canceled) {
			return err
		}
		return &provider.Error{Class: provider.Transient, Code: "provider_error", Msg: "network error talking to Anthropic"}
	}
	var body struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	json.Unmarshal([]byte(apiErr.RawJSON()), &body)
	msg := body.Error.Message
	if msg == "" {
		msg = http.StatusText(apiErr.StatusCode)
	}
	if apiErr.StatusCode == http.StatusOK {
		// An error event inside the stream, surfaced by the SDK (D94).
		return streamError(body.Error.Type, msg)
	}
	pe := &provider.Error{Status: apiErr.StatusCode, Msg: "Anthropic: " + msg}
	ra, hasRA := time.Duration(0), false
	if apiErr.Response != nil {
		ra, hasRA = retryAfter(apiErr.Response.Header)
	}
	if apiErr.StatusCode == http.StatusUnauthorized {
		// The provider's message may quote part of the key; it is not stored
		// or streamed (R7).
		pe.Msg = "Anthropic rejected the API key (HTTP 401): check the key under Settings"
	}
	switch {
	case apiErr.StatusCode == 429 && !hasRA:
		// A 429 without retry-after is a spend limit, not rate limiting (D94).
		pe.Class, pe.Code = provider.Permanent, "quota_exhausted"
	case apiErr.StatusCode == 429:
		pe.Class, pe.Code, pe.RetryAfter, pe.HasRetryAfter = provider.Transient, "rate_limited", ra, true
	case apiErr.StatusCode == 402 || strings.Contains(strings.ToLower(msg), "credit balance"):
		pe.Class, pe.Code = provider.Permanent, "quota_exhausted"
	case apiErr.StatusCode == 400 && strings.Contains(strings.ToLower(msg), "prompt is too long"):
		pe.Class, pe.Code = provider.Permanent, "context_too_long"
	case apiErr.StatusCode == 408 || apiErr.StatusCode == 529 || apiErr.StatusCode >= 500:
		pe.Class, pe.Code = provider.Transient, "provider_error"
		pe.RetryAfter, pe.HasRetryAfter = ra, hasRA
	default:
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
