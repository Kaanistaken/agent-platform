// Package fake is a scripted provider for tests: each call returns the next
// step of the script. No network, no keys.
package fake

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/kaang/vodafone-case/backend/internal/provider"
)

// Step is one scripted model call.
type Step struct {
	Text     string             // streamed as one delta
	ToolCall *provider.ToolCall // optional tool call
	Usage    provider.Usage
	Err      error         // returned instead of a result
	Partial  string        // streamed before Err (a broken stream)
	Block    bool          // wait until the context is cancelled
	Gate     chan struct{} // wait for this channel before answering
}

// Provider replays Steps in order and records every request.
type Provider struct {
	mu       sync.Mutex
	Steps    []Step
	Requests []provider.Request
}

// ErrScriptExhausted is returned when there are no steps left.
var ErrScriptExhausted = errors.New("fake provider: no scripted step left")

// Stream implements provider.Provider.
func (p *Provider) Stream(ctx context.Context, req provider.Request, cb provider.Callbacks) (provider.Result, error) {
	p.mu.Lock()
	p.Requests = append(p.Requests, req)
	if len(p.Steps) == 0 {
		p.mu.Unlock()
		return provider.Result{}, &provider.Error{Class: provider.Permanent, Code: "provider_error", Msg: ErrScriptExhausted.Error()}
	}
	s := p.Steps[0]
	p.Steps = p.Steps[1:]
	p.mu.Unlock()

	if s.Gate != nil {
		select {
		case <-s.Gate:
		case <-ctx.Done():
			return provider.Result{}, ctx.Err()
		}
	}
	if s.Block {
		<-ctx.Done()
		return provider.Result{}, ctx.Err()
	}
	if s.Partial != "" && cb.OnText != nil {
		cb.OnText(s.Partial)
	}
	if s.Usage.Known && cb.OnUsage != nil {
		cb.OnUsage(s.Usage)
	}
	if s.Err != nil {
		return provider.Result{}, s.Err
	}
	if s.Text != "" && cb.OnText != nil {
		cb.OnText(s.Text)
	}
	res := provider.Result{Text: s.Text}
	if s.ToolCall != nil {
		res.ToolCalls = []provider.ToolCall{*s.ToolCall}
	}
	res.Raw, _ = json.Marshal(map[string]any{"text": s.Text, "tool_calls": res.ToolCalls})
	return res, nil
}

// Calls returns how many requests were made.
func (p *Provider) Calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.Requests)
}
