package run

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kaang/vodafone-case/backend/internal/crypto"
	"github.com/kaang/vodafone-case/backend/internal/db"
	"github.com/kaang/vodafone-case/backend/internal/provider"
	"github.com/kaang/vodafone-case/backend/internal/provider/fake"
	"github.com/kaang/vodafone-case/backend/internal/tool"
)

// fakeExec records tool requests instead of sending them.
type fakeExec struct {
	mu    sync.Mutex
	calls []tool.Request
	keys  []map[string]tool.Secret
	res   tool.Result
	err   error
	gate  chan struct{}
}

func (f *fakeExec) Do(ctx context.Context, r tool.Request, s map[string]tool.Secret) (tool.Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, r)
	f.keys = append(f.keys, s)
	res, err, gate := f.res, f.err, f.gate
	f.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return tool.Result{}, &tool.Failure{Code: "interrupted", Msg: "cancelled", MaySent: true}
		}
	}
	if res.Status == 0 && err == nil {
		res = tool.Result{Status: 201, Text: "HTTP 201 Created\n\n{\"html_url\":\"https://github.com/x/y/issues/1\"}"}
	}
	return res, err
}

func (f *fakeExec) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// env is one test's database and fixtures: two teams, a starter (alice, lead
// of alpha), a teammate (bob) and another team's user (carol).
type env struct {
	t      *testing.T
	path   string
	db     *db.DB
	box    *crypto.Box
	alice  int64
	bob    int64
	carol  int64
	alpha  int64
	beta   int64
	agent  int64
	secret int64
}

func newEnv(t *testing.T) *env {
	t.Helper()
	key := make([]byte, 32)
	box, _ := crypto.New(key)
	e := &env{t: t, path: filepath.Join(t.TempDir(), "test.db"), box: box}
	e.open()
	ctx := context.Background()
	w := e.db.W
	mustExec(t, w, "INSERT INTO teams (id, name) VALUES (1, 'alpha'), (2, 'beta')")
	mustExec(t, w, `INSERT INTO users (id, team_id, username, password_hash, role, created_at) VALUES
		(1, 1, 'alice', 'x', 'lead', 'now'), (2, 1, 'bob', 'x', 'member', 'now'), (3, 2, 'carol', 'x', 'lead', 'now')`)
	e.alice, e.bob, e.carol, e.alpha, e.beta = 1, 2, 3, 1, 2
	e.setModelKey(e.alice, "sk-test-alice")
	e.secret = e.addSecret(e.alice, "GITHUB_TOKEN", "api.github.com", "ghp_test_secret_value")

	mustExec(t, w, `INSERT INTO agents (id, team_id, owner_id, name, system_prompt, provider, model, created_at, updated_at)
		VALUES (1, 1, 1, 'dep-check', 'You check dependencies.', 'openai', 'gpt-6-luna', 'now', 'now')`)
	e.agent = 1
	e.addTool(1, "create_issue", "POST", true,
		"https://api.github.com/repos/{{owner}}/{{repo}}/issues",
		map[string]string{"Authorization": "Bearer {{secret.GITHUB_TOKEN}}"},
		`{"title":"{{title}}","body":"{{body}}"}`,
		`{"type":"object","properties":{"owner":{"type":"string"},"repo":{"type":"string"},"title":{"type":"string"},"body":{"type":"string"}},"required":["owner","repo","title","body"]}`)
	e.addTool(2, "read_file", "GET", false,
		"https://api.github.com/repos/{{owner}}/{{repo}}/contents/{{path}}",
		map[string]string{"Accept": "application/vnd.github.raw+json"}, "",
		`{"type":"object","properties":{"owner":{"type":"string"},"repo":{"type":"string"},"path":{"type":"string"}},"required":["owner","repo","path"]}`)
	_ = ctx
	return e
}

func (e *env) open() {
	d, err := db.Open(context.Background(), e.path)
	if err != nil {
		e.t.Fatal(err)
	}
	e.db = d
	e.t.Cleanup(func() { d.Close() })
}

func (e *env) addTool(id int64, name, method string, approval bool, url string, headers map[string]string, body, schema string) {
	h, _ := json.Marshal(headers)
	def := tool.Definition{Name: name, Description: name, Method: method, URLTemplate: url, Headers: headers,
		BodyTemplate: body, ParamsSchema: json.RawMessage(schema), RequiresApproval: approval}
	if err := tool.Validate(def); err != nil {
		e.t.Fatalf("tool %s: %v", name, err)
	}
	mustExec(e.t, e.db.W, `INSERT INTO tools (id, team_id, owner_id, name, description, method, url_template, headers_template,
		body_template, params_schema, requires_approval, created_at, updated_at) VALUES (?, 1, 1, ?, ?, ?, ?, ?, ?, ?, ?, 'now', 'now')`,
		id, name, name, method, url, string(h), body, schema, approval)
	mustExec(e.t, e.db.W, "INSERT INTO agent_tools (agent_id, tool_id, team_id) VALUES (1, ?, 1)", id)
}

func (e *env) setModelKey(user int64, key string) {
	ct, nonce, _ := e.box.Seal([]byte(key), crypto.ModelKeyAAD(user, "openai"))
	mustExec(e.t, e.db.W, `INSERT INTO user_model_keys (user_id, provider, ciphertext, nonce, key_version, aad_version, updated_at)
		VALUES (?, 'openai', ?, ?, 1, 1, 'now') ON CONFLICT (user_id, provider) DO UPDATE SET ciphertext = excluded.ciphertext,
		nonce = excluded.nonce`, user, ct, nonce)
}

func (e *env) addSecret(user int64, name, host, value string) int64 {
	res, err := e.db.W.Exec(`INSERT INTO user_secrets (user_id, name, allowed_host, ciphertext, nonce, key_version, aad_version, updated_at)
		VALUES (?, ?, ?, x'', x'', 1, 1, 'now')`, user, name, host)
	if err != nil {
		e.t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	ct, nonce, _ := e.box.Seal([]byte(value), crypto.SecretAAD(id, user))
	mustExec(e.t, e.db.W, "UPDATE user_secrets SET ciphertext = ?, nonce = ? WHERE id = ?", ct, nonce, id)
	return id
}

func (e *env) engine(p *fake.Provider, x Executor) *Engine {
	eng := New(e.db, e.box, map[string]provider.Provider{"openai": p}, x)
	eng.Sleep = func(ctx context.Context, d time.Duration) error { return ctx.Err() } // no real waiting
	if err := eng.Start(context.Background()); err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(eng.Stop)
	return eng
}

// restart simulates a server restart: stop the engine, reopen the database.
func (e *env) restart(old *Engine, p *fake.Provider, x Executor) *Engine {
	old.Stop()
	e.db.Close()
	e.open()
	return e.engine(p, x)
}

func (e *env) runStatus(runID int64) (status, code string) {
	var c sql.NullString
	e.db.W.QueryRow("SELECT status, error_code FROM runs WHERE id = ?", runID).Scan(&status, &c)
	return status, c.String
}

func (e *env) waitStatus(runID int64, want string) {
	e.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s, _ := e.runStatus(runID); s == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	s, c := e.runStatus(runID)
	e.t.Fatalf("run %d: status %s (%s), want %s", runID, s, c, want)
}

func (e *env) call(runID int64) (id int64, status string, rev int, reason string) {
	var r sql.NullString
	e.db.W.QueryRow(`SELECT id, status, approval_revision, reason_code FROM tool_calls WHERE run_id = ?
		ORDER BY id DESC LIMIT 1`, runID).Scan(&id, &status, &rev, &r)
	return id, status, rev, r.String
}

func (e *env) lastEvent(runID int64) string {
	var typ string
	e.db.W.QueryRow("SELECT type FROM run_events WHERE run_id = ? ORDER BY seq DESC LIMIT 1", runID).Scan(&typ)
	return typ
}

func mustExec(t *testing.T, w *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := w.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func issueCall() *provider.ToolCall {
	return &provider.ToolCall{ID: "call_1", Name: "create_issue",
		Arguments: `{"owner":"kaang","repo":"vodafone-case-sandbox","title":"Vulnerable deps","body":"lodash 4.17.15\n\"quoted\""}`}
}

func readCall(id string) *provider.ToolCall {
	return &provider.ToolCall{ID: id, Name: "read_file", Arguments: `{"owner":"kaang","repo":"sandbox","path":"package.json"}`}
}
