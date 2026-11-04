package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-go/v3/option"

	"github.com/kaang/vodafone-case/backend/internal/provider"
)

// A hand-written Responses stream: text deltas, then a function call, then
// the completed response with usage.
const toolCallStream = `event: response.created
data: {"type":"response.created","response":{"id":"resp_1","output":[]}}

event: response.output_text.delta
data: {"type":"response.output_text.delta","delta":"Checking "}

event: response.output_text.delta
data: {"type":"response.output_text.delta","delta":"lodash."}

event: response.completed
data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[{"type":"reasoning","id":"rs_1","encrypted_content":"opaque","summary":[]},{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"Checking lodash."}]},{"type":"function_call","id":"fc_1","call_id":"call_1","name":"osv_query","arguments":"{\"name\":\"lodash\",\"version\":\"4.17.15\"}"}],"usage":{"input_tokens":120,"output_tokens":30,"total_tokens":150}}}

`

func server(t *testing.T, status int, headers map[string]string, body string, seen *map[string]any, auth *string) *Adapter {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Errorf("path %s", r.URL.Path)
		}
		*auth = r.Header.Get("Authorization")
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
	return New(option.WithBaseURL(srv.URL + "/"))
}

func request() provider.Request {
	return provider.Request{
		APIKey: "sk-user-key", Model: "gpt-6-luna", System: "sys", MaxOutputTokens: 4096,
		Messages: []provider.Message{
			{Role: "user", Content: "check deps"},
			{Role: "assistant", Raw: json.RawMessage(`[{"type":"function_call","call_id":"call_0","name":"read_file","arguments":"{}"}]`)},
			{Role: "tool", ToolCallID: "call_0", Content: "HTTP 200 OK\n\n{}"},
		},
		Tools: []provider.ToolSpec{{Name: "osv_query", Description: "d", Parameters: json.RawMessage(`{"type":"object"}`)}},
	}
}

func TestToolCallRoundTrip(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-from-environment") // must never be used (D12)
	var body map[string]any
	var auth string
	a := server(t, 200, nil, toolCallStream, &body, &auth)

	var deltas []string
	var usage provider.Usage
	res, err := a.Stream(context.Background(), request(), provider.Callbacks{
		OnText:  func(d string) { deltas = append(deltas, d) },
		OnUsage: func(u provider.Usage) { usage = u },
	})
	if err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer sk-user-key" {
		t.Fatalf("authorization %q", auth)
	}
	if body["store"] != false || body["parallel_tool_calls"] != false || body["model"] != "gpt-6-luna" {
		t.Fatalf("request body %v", body)
	}
	input := body["input"].([]any)
	if len(input) != 3 || input[1].(map[string]any)["call_id"] != "call_0" ||
		input[2].(map[string]any)["type"] != "function_call_output" {
		t.Fatalf("input items %v", input)
	}
	if strings.Join(deltas, "") != "Checking lodash." || res.Text != "Checking lodash." {
		t.Fatalf("text %q / %q", deltas, res.Text)
	}
	if len(res.ToolCalls) != 1 || res.ToolCalls[0].ID != "call_1" || !strings.Contains(res.ToolCalls[0].Arguments, "4.17.15") {
		t.Fatalf("tool calls %+v", res.ToolCalls)
	}
	if usage != (provider.Usage{Input: 120, Output: 30, Known: true}) {
		t.Fatalf("usage %+v", usage)
	}
	// The raw output items, reasoning included, are kept for the next turn.
	if !strings.Contains(string(res.Raw), `"encrypted_content":"opaque"`) {
		t.Fatalf("raw %s", res.Raw)
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
		wait    time.Duration
	}{
		{"rate limit with retry-after", 429, map[string]string{"Retry-After": "2"},
			`{"error":{"message":"Rate limit reached","type":"requests","code":"rate_limit_exceeded"}}`, provider.Transient, "rate_limited", 2 * time.Second},
		{"quota", 429, nil, `{"error":{"message":"You exceeded your current quota","type":"insufficient_quota","code":"insufficient_quota"}}`,
			provider.Permanent, "quota_exhausted", 0},
		{"bad key", 401, nil, `{"error":{"message":"Incorrect API key provided","code":"invalid_api_key"}}`, provider.Permanent, "provider_error", 0},
		{"server error", 503, nil, `{"error":{"message":"overloaded"}}`, provider.Transient, "provider_error", 0},
		{"context", 400, nil, `{"error":{"message":"too long","code":"context_length_exceeded"}}`, provider.Permanent, "context_too_long", 0},
		{"incomplete", 200, nil, "event: response.incomplete\ndata: {\"type\":\"response.incomplete\",\"response\":{\"status\":\"incomplete\",\"incomplete_details\":{\"reason\":\"max_output_tokens\"},\"usage\":{\"input_tokens\":5,\"output_tokens\":4096}}}\n\n",
			provider.Permanent, "incomplete_output", 0},
		{"in-stream failure", 200, nil, "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"server_error\",\"message\":\"boom\"}}}\n\n",
			provider.Transient, "provider_error", 0},
		{"stream cut short", 200, nil, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"par\"}\n\n",
			provider.Transient, "provider_error", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var body map[string]any
			var auth string
			a := server(t, c.status, c.headers, c.body, &body, &auth)
			_, err := a.Stream(context.Background(), request(), provider.Callbacks{})
			var pe *provider.Error
			if !errors.As(err, &pe) {
				t.Fatalf("err %v", err)
			}
			if pe.Class != c.class || pe.Code != c.code || pe.RetryAfter != c.wait {
				t.Fatalf("got class %v code %s wait %v", pe.Class, pe.Code, pe.RetryAfter)
			}
		})
	}
}
