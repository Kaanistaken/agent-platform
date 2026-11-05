package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/kaang/vodafone-case/backend/internal/provider"
)

// A hand-written Messages stream with a thinking block (and its signature),
// text and a tool_use block whose input arrives in pieces.
const toolUseStream = `event: message_start
data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-haiku-5-5","usage":{"input_tokens":200,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Need OSV."}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-abc"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Querying "}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"OSV."}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: content_block_start
data: {"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_1","name":"osv_query","input":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"name\":\"lod"}}

event: content_block_delta
data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"ash\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":2}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":42}}

event: message_stop
data: {"type":"message_stop"}

`

func server(t *testing.T, status int, headers map[string]string, body string, seen *map[string]any, key *string) *Adapter {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path %s", r.URL.Path)
		}
		*key = r.Header.Get("X-Api-Key")
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, seen)
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		if status == 200 {
			w.Header().Set("Content-Type", "text/event-stream")
		} else {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return New(option.WithBaseURL(srv.URL))
}

func request() provider.Request {
	return provider.Request{
		APIKey: "sk-ant-user", Model: "claude-haiku-5-5", System: "sys", MaxOutputTokens: 4096,
		Messages: []provider.Message{
			{Role: "user", Content: "check deps"},
			{Role: "assistant", Raw: json.RawMessage(`[{"type":"thinking","thinking":"t","signature":"s0"},{"type":"tool_use","id":"toolu_0","name":"read_file","input":{}}]`)},
			{Role: "tool", ToolCallID: "toolu_0", Content: "HTTP 200 OK"},
		},
		Tools: []provider.ToolSpec{{Name: "osv_query", Description: "d", Parameters: json.RawMessage(`{"type":"object"}`)}},
	}
}

func TestToolUseRoundTrip(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-from-environment") // must never be used (D12)
	var body map[string]any
	var key string
	a := server(t, 200, nil, toolUseStream, &body, &key)
	var text strings.Builder
	var last provider.Usage
	res, err := a.Stream(context.Background(), request(), provider.Callbacks{
		OnText:  func(d string) { text.WriteString(d) },
		OnUsage: func(u provider.Usage) { last = u },
	})
	if err != nil {
		t.Fatal(err)
	}
	if key != "sk-ant-user" {
		t.Fatalf("api key %q", key)
	}
	choice := body["tool_choice"].(map[string]any)
	if choice["disable_parallel_tool_use"] != true || choice["type"] != "auto" {
		t.Fatalf("tool_choice %v", choice)
	}
	msgs := body["messages"].([]any)
	asst := msgs[1].(map[string]any)["content"].([]any)
	if asst[0].(map[string]any)["signature"] != "s0" {
		t.Fatal("thinking block not sent back verbatim")
	}
	result := msgs[2].(map[string]any)["content"].([]any)[0].(map[string]any)
	if result["type"] != "tool_result" || result["tool_use_id"] != "toolu_0" {
		t.Fatalf("tool result %v", result)
	}
	if text.String() != "Querying OSV." || res.Text != "Querying OSV." {
		t.Fatalf("text %q", res.Text)
	}
	if len(res.ToolCalls) != 1 || res.ToolCalls[0].ID != "toolu_1" || res.ToolCalls[0].Arguments != `{"name":"lodash"}` {
		t.Fatalf("tool call %+v", res.ToolCalls)
	}
	if last != (provider.Usage{Input: 200, Output: 42, Known: true}) {
		t.Fatalf("usage %+v", last)
	}
	if !strings.Contains(string(res.Raw), `"signature":"sig-abc"`) || !strings.Contains(string(res.Raw), `"name":"lodash"`) {
		t.Fatalf("raw content %s", res.Raw)
	}
}

func TestErrorClassification(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		headers map[string]string
		body    string
		class   provider.Class
		code    string
	}{
		{"overloaded 529", 529, nil, `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`, provider.Transient, "provider_error"},
		{"rate limit", 429, map[string]string{"Retry-After": "3"}, `{"type":"error","error":{"type":"rate_limit_error","message":"slow"}}`, provider.Transient, "rate_limited"},
		{"spend cap (429 without retry-after)", 429, nil, `{"type":"error","error":{"type":"rate_limit_error","message":"limit"}}`, provider.Permanent, "quota_exhausted"},
		{"credit", 400, nil, `{"type":"error","error":{"type":"invalid_request_error","message":"Your credit balance is too low"}}`, provider.Permanent, "quota_exhausted"},
		{"bad key", 401, nil, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`, provider.Permanent, "provider_error"},
		{"prompt too long", 400, nil, `{"type":"error","error":{"type":"invalid_request_error","message":"prompt is too long: 300000 tokens"}}`, provider.Permanent, "context_too_long"},
		{"in-stream overload", 200, nil, "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}\n\n", provider.Transient, "provider_error"},
		{"max tokens", 200, nil, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"max_tokens\"},\"usage\":{\"output_tokens\":4096}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", provider.Permanent, "incomplete_output"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var body map[string]any
			var key string
			a := server(t, c.status, c.headers, c.body, &body, &key)
			_, err := a.Stream(context.Background(), request(), provider.Callbacks{})
			var pe *provider.Error
			if !errors.As(err, &pe) || pe.Class != c.class || pe.Code != c.code {
				t.Fatalf("err %v (%+v)", err, pe)
			}
		})
	}
}

// R7, D12: an environment bearer token is never sent next to the starter's key.
func TestEnvironmentCredentialsIgnored(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "") // so the env bearer token would be the SDK's choice
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "env-bearer-token")
	var auth, key string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, key = r.Header.Get("Authorization"), r.Header.Get("X-Api-Key")
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, toolUseStream)
	}))
	t.Cleanup(srv.Close)
	if _, err := New(option.WithBaseURL(srv.URL)).Stream(context.Background(), request(), provider.Callbacks{}); err != nil {
		t.Fatal(err)
	}
	if auth != "" || key != "sk-ant-user" {
		t.Fatalf("Authorization %q, X-Api-Key %q", auth, key)
	}
}
