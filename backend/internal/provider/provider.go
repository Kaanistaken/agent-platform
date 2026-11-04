// Package provider defines the small interface the agent loop uses to talk to
// a model provider (D9). Each adapter (OpenAI, Anthropic, the scripted fake
// used in tests) makes exactly one streaming request per call; retries are
// owned by the agent loop, not by the SDKs (D79).
package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Message is one entry of the provider-independent history (D20).
type Message struct {
	Role       string          // "user", "assistant" or "tool"
	Content    string          // text; for "tool" the tool result
	ToolCalls  []ToolCall      // assistant only
	ToolCallID string          // tool only: the provider's call ID
	Raw        json.RawMessage // assistant only: the provider's own content, sent back verbatim (D96)
}

// ToolCall is a call the model asked for.
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolSpec is what the model sees of a tool: never URLs, headers or secrets (R2).
type ToolSpec struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

// Request is one model call.
type Request struct {
	APIKey          string // the starter's key, passed explicitly (D12)
	Model           string
	System          string
	Messages        []Message
	Tools           []ToolSpec
	MaxOutputTokens int
}

// Usage is the token usage of one attempt. Known is false when the provider
// reported nothing (D75).
type Usage struct {
	Input  int64
	Output int64
	Known  bool
}

// Result is a complete model turn.
type Result struct {
	Text      string
	ToolCalls []ToolCall
	Raw       json.RawMessage
}

// Callbacks receive streamed output while the call runs.
type Callbacks struct {
	OnText  func(delta string)
	OnUsage func(Usage) // may be called several times; the latest value counts
}

// Provider is implemented by every adapter.
type Provider interface {
	Stream(ctx context.Context, req Request, cb Callbacks) (Result, error)
}

// Class says how the agent loop treats an error (D10, D79, D94).
type Class int

const (
	Permanent Class = iota // fail the run with a clear message
	Transient              // retry with backoff
)

// Error is the only error type adapters return for provider failures.
type Error struct {
	Class         Class
	Code          string        // run error_code, e.g. provider_error, quota_exhausted, rate_limited, incomplete_output
	Status        int           // HTTP status, 0 if none
	RetryAfter    time.Duration // from the Retry-After header, 0 if absent
	HasRetryAfter bool
	Msg           string
}

func (e *Error) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("%s (HTTP %d)", e.Msg, e.Status)
	}
	return e.Msg
}

// Models lists the supported models per provider (D78).
var Models = map[string][]string{
	"openai":    {"gpt-6-luna", "gpt-6-sol"},
	"anthropic": {"claude-haiku-5-5", "claude-sonnet-5-5"},
}

// Supported reports whether model belongs to provider.
func Supported(providerName, model string) bool {
	for _, m := range Models[providerName] {
		if m == model {
			return true
		}
	}
	return false
}
