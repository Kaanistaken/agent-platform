package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kaang/vodafone-case/backend/internal/auth"
	"github.com/kaang/vodafone-case/backend/internal/crypto"
	"github.com/kaang/vodafone-case/backend/internal/db"
	"github.com/kaang/vodafone-case/backend/internal/provider"
	"github.com/kaang/vodafone-case/backend/internal/provider/fake"
	"github.com/kaang/vodafone-case/backend/internal/run"
	"github.com/kaang/vodafone-case/backend/internal/tool"
)

const origin = "http://localhost:3000"

type nopExec struct{}

func (nopExec) Do(context.Context, tool.Request, map[string]tool.Secret, int, func() error) (tool.Result, error) {
	return tool.Result{Status: 200, Text: "HTTP 200 OK\n\n{}"}, nil
}

type testAPI struct {
	t    *testing.T
	srv  *httptest.Server
	db   *db.DB
	fake *fake.Provider
}

func newAPI(t *testing.T) *testAPI {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	box, _ := crypto.New(make([]byte, 32))
	for _, u := range []string{"ALICE", "BOB", "CAROL", "DAVE"} {
		t.Setenv("SEED_PASSWORD_"+u, "password-"+strings.ToLower(u))
	}
	if err := auth.Seed(ctx, d.W, auth.DefaultSeed); err != nil {
		t.Fatal(err)
	}
	p := &fake.Provider{}
	eng := run.New(d, box, map[string]provider.Provider{"openai": p}, nopExec{})
	eng.Sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	if err := eng.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.Stop)
	srv := httptest.NewServer((&Server{DB: d, Box: box, Engine: eng, Origin: origin, Heartbeat: 50 * time.Millisecond}).Routes())
	t.Cleanup(srv.Close)
	return &testAPI{t: t, srv: srv, db: d, fake: p}
}

// client is a logged-in browser session.
type client struct {
	a    *testAPI
	http *http.Client
}

func (a *testAPI) login(user string) *client {
	jar, _ := cookiejar.New(nil)
	c := &client{a: a, http: &http.Client{Jar: jar}}
	if code, body := c.do("POST", "/api/login", map[string]string{"username": user, "password": "password-" + user}); code != 200 {
		a.t.Fatalf("login %s: %d %s", user, code, body)
	}
	return c
}

func (c *client) do(method, path string, body any) (int, string) {
	c.a.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.a.srv.URL+path, r)
	req.Header.Set("Origin", origin)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		c.a.t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

func (c *client) id(method, path string, body any) int64 {
	c.a.t.Helper()
	code, out := c.do(method, path, body)
	if code != 201 {
		c.a.t.Fatalf("%s %s: %d %s", method, path, code, out)
	}
	var v struct {
		ID int64 `json:"id"`
	}
	json.Unmarshal([]byte(out), &v)
	return v.ID
}

var readTool = map[string]any{
	"name": "read_file", "description": "Read a file", "method": "GET",
	"url_template":     "https://api.github.com/repos/{{owner}}/{{repo}}/contents/{{path}}",
	"headers_template": map[string]string{"Accept": "application/vnd.github.raw+json"},
	"params_schema":    json.RawMessage(`{"type":"object","properties":{"owner":{"type":"string"},"repo":{"type":"string"},"path":{"type":"string"}},"required":["owner","repo","path"]}`),
}

// R6, D39, D51: teams are isolated on every path; inside a team, ownership
// limits changes.
func TestTeamAndUsageAuthorization(t *testing.T) {
	a := newAPI(t)
	alice, bob, carol, dave := a.login("alice"), a.login("bob"), a.login("carol"), a.login("dave")

	toolID := alice.id("POST", "/api/tools", readTool)
	agentID := alice.id("POST", "/api/agents", map[string]any{"name": "dep-check", "system_prompt": "s",
		"provider": "openai", "model": "gpt-6-luna", "tool_ids": []int64{toolID}})
	if code, _ := alice.do("PUT", "/api/me/model-keys/openai", map[string]string{"key": "sk-alice"}); code != 204 {
		t.Fatalf("put key: %d", code)
	}
	a.fake.Steps = []fake.Step{{Text: "hello", Usage: provider.Usage{Input: 10, Output: 3, Known: true}}}
	runID := alice.id("POST", fmt.Sprintf("/api/agents/%d/runs", agentID), map[string]string{"task": "go"})

	// Another team: every path is 404, including IDs inside a request body.
	other := []struct {
		method, path string
		body         any
	}{
		{"GET", fmt.Sprintf("/api/agents/%d", agentID), nil},
		{"PUT", fmt.Sprintf("/api/agents/%d", agentID), map[string]any{"name": "x", "provider": "openai", "model": "gpt-6-luna"}},
		{"DELETE", fmt.Sprintf("/api/agents/%d", agentID), nil},
		{"POST", fmt.Sprintf("/api/agents/%d/clone", agentID), nil},
		{"POST", fmt.Sprintf("/api/agents/%d/runs", agentID), map[string]string{"task": "x"}},
		{"GET", fmt.Sprintf("/api/tools/%d", toolID), nil},
		{"PUT", fmt.Sprintf("/api/tools/%d", toolID), readTool},
		{"DELETE", fmt.Sprintf("/api/tools/%d", toolID), nil},
		{"POST", fmt.Sprintf("/api/tools/%d/clone", toolID), nil},
		{"GET", fmt.Sprintf("/api/runs/%d", runID), nil},
		{"GET", fmt.Sprintf("/api/runs/%d/events", runID), nil},
		{"POST", fmt.Sprintf("/api/runs/%d/cancel", runID), nil},
		{"POST", fmt.Sprintf("/api/runs/%d/tool-calls/1/approve", runID), map[string]int{"approval_revision": 1}},
		{"POST", "/api/agents", map[string]any{"name": "x", "provider": "openai", "model": "gpt-6-luna", "tool_ids": []int64{toolID}}},
	}
	for _, c := range other {
		if code, body := carol.do(c.method, c.path, c.body); code != 404 {
			t.Errorf("carol %s %s: %d %s", c.method, c.path, code, body)
		}
	}
	for _, list := range []string{"/api/agents", "/api/tools", "/api/runs"} {
		if _, body := dave.do("GET", list, nil); body != "[]\n" {
			t.Errorf("dave %s: %s", list, body)
		}
	}

	// Same team: visible, runnable by bob with his own key only (R7), but not
	// changeable (403).
	if code, _ := bob.do("GET", fmt.Sprintf("/api/agents/%d", agentID), nil); code != 200 {
		t.Errorf("bob get: %d", code)
	}
	if code, _ := bob.do("PUT", fmt.Sprintf("/api/agents/%d", agentID), map[string]any{"name": "x", "provider": "openai", "model": "gpt-6-luna"}); code != 403 {
		t.Errorf("bob put: %d", code)
	}
	if code, _ := bob.do("DELETE", fmt.Sprintf("/api/tools/%d", toolID), nil); code != 403 {
		t.Errorf("bob delete tool: %d", code)
	}
	code, body := bob.do("POST", fmt.Sprintf("/api/agents/%d/runs", agentID), map[string]string{"task": "go"})
	if code != 400 || !strings.Contains(body, "API key") {
		t.Errorf("bob run without key: %d %s", code, body)
	}
	if code, _ := bob.do("POST", fmt.Sprintf("/api/runs/%d/cancel", runID), nil); code != 403 && code != 409 {
		t.Errorf("bob cancel: %d", code)
	}

	// Usage: alice (lead) sees the team; bob only himself; token counts in
	// run details only for the starter and the lead (D41, D53).
	waitRun(t, alice, runID, "succeeded")
	_, body = bob.do("GET", fmt.Sprintf("/api/runs/%d", runID), nil)
	if strings.Contains(body, `"usage"`) {
		t.Error("bob sees alice's token usage")
	}
	_, body = alice.do("GET", fmt.Sprintf("/api/runs/%d", runID), nil)
	if !strings.Contains(body, `"input_tokens":10`) {
		t.Errorf("alice run usage: %s", body)
	}
	_, body = bob.do("GET", "/api/usage", nil)
	if strings.Contains(body, "alice") || strings.Contains(body, "team_total") {
		t.Errorf("bob usage: %s", body)
	}
	_, body = alice.do("GET", "/api/usage", nil)
	if !strings.Contains(body, `"team_total"`) || !strings.Contains(body, `"input_tokens":10`) {
		t.Errorf("alice usage: %s", body)
	}

	// The model key is never returned.
	_, body = alice.do("GET", "/api/me/model-keys", nil)
	if strings.Contains(body, "sk-alice") || !strings.Contains(body, `"set":true`) {
		t.Errorf("model keys: %s", body)
	}
}

// D30: state-changing requests need the right Origin; sessions are required.
func TestSessionAndOrigin(t *testing.T) {
	a := newAPI(t)
	alice := a.login("alice")
	req, _ := http.NewRequest("POST", a.srv.URL+"/api/tools", strings.NewReader("{}"))
	req.Header.Set("Origin", "https://evil.example")
	resp, _ := alice.http.Do(req)
	if resp.StatusCode != 403 {
		t.Fatalf("cross-origin POST: %d", resp.StatusCode)
	}
	anon := &client{a: a, http: &http.Client{}}
	if code, _ := anon.do("GET", "/api/agents", nil); code != 401 {
		t.Fatalf("anonymous: %d", code)
	}
	if code, _ := anon.do("POST", "/api/login", map[string]string{"username": "alice", "password": "wrong"}); code != 401 {
		t.Fatalf("bad password: %d", code)
	}
	alice.do("POST", "/api/logout", nil)
	if code, _ := alice.do("GET", "/api/me", nil); code != 401 {
		t.Fatalf("after logout: %d", code)
	}
}

// D67, D94: SSE replays from Last-Event-ID, filters usage for teammates and
// closes after the final event.
func TestEventStreamReplay(t *testing.T) {
	a := newAPI(t)
	alice, bob := a.login("alice"), a.login("bob")
	toolID := alice.id("POST", "/api/tools", readTool)
	agentID := alice.id("POST", "/api/agents", map[string]any{"name": "a", "system_prompt": "s",
		"provider": "openai", "model": "gpt-6-luna", "tool_ids": []int64{toolID}})
	alice.do("PUT", "/api/me/model-keys/openai", map[string]string{"key": "sk-alice"})
	a.fake.Steps = []fake.Step{
		{ToolCall: &provider.ToolCall{ID: "c1", Name: "read_file", Arguments: `{"owner":"o","repo":"r","path":"package.json"}`},
			Usage: provider.Usage{Input: 5, Output: 1, Known: true}},
		{Text: "done", Usage: provider.Usage{Input: 9, Output: 2, Known: true}},
	}
	runID := alice.id("POST", fmt.Sprintf("/api/agents/%d/runs", agentID), map[string]string{"task": "go"})
	waitRun(t, alice, runID, "succeeded")

	all := stream(t, alice, runID, "")
	if all[len(all)-1].typ != "run_finished" {
		t.Fatalf("last event %s", all[len(all)-1].typ)
	}
	for i := 1; i < len(all); i++ {
		if all[i].id <= all[i-1].id {
			t.Fatal("event ids not increasing")
		}
	}
	types := map[string]bool{}
	for _, e := range all {
		types[e.typ] = true
	}
	for _, want := range []string{"run_status", "text_delta", "tool_call", "tool_call_status", "tool_result", "usage", "run_finished"} {
		if !types[want] {
			t.Errorf("missing event type %s", want)
		}
	}
	mid := all[len(all)/2].id
	rest := stream(t, alice, runID, fmt.Sprint(mid))
	if rest[0].id != mid+1 {
		t.Fatalf("replay started at %d, want %d", rest[0].id, mid+1)
	}
	for _, e := range stream(t, bob, runID, "") {
		if e.typ == "usage" {
			t.Fatal("teammate received usage events")
		}
	}
}

type sse struct {
	id   int64
	typ  string
	data string
}

func stream(t *testing.T, c *client, runID int64, lastID string) []sse {
	t.Helper()
	req, _ := http.NewRequest("GET", fmt.Sprintf("%s/api/runs/%d/events", c.a.srv.URL, runID), nil)
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := c.http.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out []sse
	var cur sse
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "id: "):
			fmt.Sscan(line[4:], &cur.id)
		case strings.HasPrefix(line, "event: "):
			cur.typ = line[7:]
		case strings.HasPrefix(line, "data: "):
			cur.data = line[6:]
		case line == "" && cur.typ != "":
			out = append(out, cur)
			cur = sse{}
		}
	}
	return out
}

func waitRun(t *testing.T, c *client, runID int64, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, body := c.do("GET", fmt.Sprintf("/api/runs/%d", runID), nil)
		if strings.Contains(body, `"status":"`+want+`"`) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("run %d did not reach %s", runID, want)
}

// D46, D49: an agent update keeps a retained link to a deleted tool, and a
// tool rename may not duplicate a name inside an agent it is linked to.
func TestAgentToolLinks(t *testing.T) {
	a := newAPI(t)
	alice := a.login("alice")
	keep := alice.id("POST", "/api/tools", readTool)
	other := map[string]any{}
	for k, v := range readTool {
		other[k] = v
	}
	other["name"] = "list_files"
	second := alice.id("POST", "/api/tools", other)
	agentID := alice.id("POST", "/api/agents", map[string]any{"name": "dep-check", "system_prompt": "s",
		"provider": "openai", "model": "gpt-6-luna", "tool_ids": []int64{keep, second}})

	// F13: renaming list_files to read_file would give the agent two tools
	// with one name.
	other["name"] = "read_file"
	if code, body := alice.do("PUT", fmt.Sprintf("/api/tools/%d", second), other); code != 409 {
		t.Fatalf("rename collision: %d %s", code, body)
	}
	var name string
	a.db.W.QueryRow("SELECT name FROM tools WHERE id = ?", second).Scan(&name)
	if name != "list_files" {
		t.Fatalf("tool renamed anyway: %s", name)
	}

	// F12: the deleted tool stays linked when the agent is updated with it.
	if code, _ := alice.do("DELETE", fmt.Sprintf("/api/tools/%d", keep), nil); code != 204 {
		t.Fatalf("delete tool: %d", code)
	}
	update := map[string]any{"name": "dep-check 2", "system_prompt": "s", "provider": "openai", "model": "gpt-6-luna",
		"tool_ids": []int64{keep, second}}
	if code, body := alice.do("PUT", fmt.Sprintf("/api/agents/%d", agentID), update); code != 200 {
		t.Fatalf("update with retained deleted tool: %d %s", code, body)
	}
	var links int
	a.db.W.QueryRow("SELECT COUNT(*) FROM agent_tools WHERE agent_id = ? AND tool_id = ?", agentID, keep).Scan(&links)
	if links != 1 {
		t.Fatal("the retained link to the deleted tool is gone")
	}
	// A deleted tool's name still counts (F13): renaming to it is refused.
	if code, _ := alice.do("PUT", fmt.Sprintf("/api/tools/%d", second), other); code != 409 {
		t.Fatalf("rename to a deleted linked tool's name: %d", code)
	}
}

// D39, D51: authorization is decided before the request body is validated,
// so an invalid body does not turn a 404 or 403 into a 400.
func TestToolUpdateAuthorizationFirst(t *testing.T) {
	a := newAPI(t)
	alice, bob, carol := a.login("alice"), a.login("bob"), a.login("carol")
	toolID := alice.id("POST", "/api/tools", readTool)
	invalid := map[string]any{"name": ""}
	if code, body := carol.do("PUT", fmt.Sprintf("/api/tools/%d", toolID), invalid); code != 404 {
		t.Errorf("other team: %d %s", code, body)
	}
	if code, body := bob.do("PUT", fmt.Sprintf("/api/tools/%d", toolID), invalid); code != 403 {
		t.Errorf("teammate: %d %s", code, body)
	}
	if code, _ := alice.do("PUT", fmt.Sprintf("/api/tools/%d", toolID), invalid); code != 400 {
		t.Errorf("owner with an invalid body: %d", code)
	}
}
