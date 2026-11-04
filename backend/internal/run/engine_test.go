package run

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kaang/vodafone-case/backend/internal/provider"
	"github.com/kaang/vodafone-case/backend/internal/provider/fake"
)

// R5: a pending approval survives a restart; nothing is sent before approval;
// approval sends the request exactly once and the run finishes.
func TestApprovalPersistsAndDispatchesOnce(t *testing.T) {
	e := newEnv(t)
	x := &fakeExec{}
	eng := e.engine(&fake.Provider{Steps: []fake.Step{{ToolCall: issueCall()}}}, x)

	runID, err := eng.CreateRun(context.Background(), e.alice, e.alpha, e.agent, "check deps", nil)
	if err != nil {
		t.Fatal(err)
	}
	e.waitStatus(runID, StWaitingApproval)
	callID, status, rev, _ := e.call(runID)
	if status != TcPending || rev != 1 {
		t.Fatalf("call status %s rev %d", status, rev)
	}
	var expires, preview string
	e.db.W.QueryRow("SELECT approval_expires_at, request_preview FROM tool_calls WHERE id = ?", callID).Scan(&expires, &preview)
	if expires == "" {
		t.Fatal("no approval expiry")
	}
	if strings.Contains(preview, "ghp_test_secret_value") {
		t.Fatal("secret value in the stored request preview")
	}

	// Restart: the same proposal, revision and expiry are still there.
	p2 := &fake.Provider{Steps: []fake.Step{{Text: "Issue opened."}}}
	eng = e.restart(eng, p2, x)
	time.Sleep(50 * time.Millisecond)
	if x.count() != 0 {
		t.Fatal("tool request sent without approval")
	}
	if s, _ := e.runStatus(runID); s != StWaitingApproval {
		t.Fatalf("after restart: %s", s)
	}
	var expires2 string
	e.db.W.QueryRow("SELECT approval_expires_at FROM tool_calls WHERE id = ?", callID).Scan(&expires2)
	if expires2 != expires {
		t.Fatal("expiry changed across restart")
	}

	if err := eng.Approve(context.Background(), e.alice, e.alpha, runID, callID, rev); err != nil {
		t.Fatal(err)
	}
	e.waitStatus(runID, StSucceeded)
	if x.count() != 1 {
		t.Fatalf("tool sent %d times, want 1", x.count())
	}
	// The secret is attached only at send time, from the starter's own secrets.
	if got := x.keys[0]["GITHUB_TOKEN"]; got.Value != "ghp_test_secret_value" || got.Host != "api.github.com" {
		t.Fatalf("secret at send time: %+v", got)
	}
	if !strings.Contains(x.calls[0].Body, `\"quoted\"`) {
		t.Fatalf("body not JSON-encoded: %s", x.calls[0].Body)
	}
	// A second decision on the same call is refused.
	var ce *ConflictError
	if err := eng.Approve(context.Background(), e.alice, e.alpha, runID, callID, rev); !errors.As(err, &ce) {
		t.Fatalf("second approve: %v", err)
	}
	if e.lastEvent(runID) != EvRunFinished {
		t.Fatalf("last event %s", e.lastEvent(runID))
	}
	// The model saw the tool result.
	req := p2.Requests[0]
	last := req.Messages[len(req.Messages)-1]
	if last.Role != "tool" || !strings.Contains(last.Content, "201") {
		t.Fatalf("model did not get the tool result: %+v", last)
	}
}

// R3: unknown tool, malformed JSON and schema violations go back to the model;
// a valid call resets the streak; three invalid calls in a row fail the run.
func TestLoopValidationAndLimits(t *testing.T) {
	t.Run("invalid calls are returned, then three in a row fail", func(t *testing.T) {
		e := newEnv(t)
		p := &fake.Provider{Steps: []fake.Step{
			{ToolCall: &provider.ToolCall{ID: "a", Name: "no_such_tool", Arguments: "{}"}},
			{ToolCall: &provider.ToolCall{ID: "b", Name: "read_file", Arguments: "{not json"}},
			{ToolCall: readCall("c")}, // valid: resets the streak
			{ToolCall: &provider.ToolCall{ID: "d", Name: "read_file", Arguments: `{"owner":1}`}},
			{ToolCall: &provider.ToolCall{ID: "e", Name: "no_such_tool", Arguments: "{}"}},
			{ToolCall: &provider.ToolCall{ID: "f", Name: "read_file", Arguments: "[]"}},
		}}
		eng := e.engine(p, &fakeExec{})
		runID, _ := eng.CreateRun(context.Background(), e.alice, e.alpha, e.agent, "go", nil)
		e.waitStatus(runID, StFailed)
		if _, code := e.runStatus(runID); code != "invalid_tool_calls" {
			t.Fatalf("code %s", code)
		}
		if p.Calls() != 6 {
			t.Fatalf("model calls %d, want 6", p.Calls())
		}
		// The second request carried the unknown-tool error back to the model.
		msgs := p.Requests[1].Messages
		if m := msgs[len(msgs)-1]; m.Role != "tool" || !strings.Contains(m.Content, "no tool named") {
			t.Fatalf("error not returned to the model: %+v", m)
		}
		var reasons []string
		rows, _ := e.db.W.Query("SELECT COALESCE(reason_code, status) FROM tool_calls WHERE run_id = ? ORDER BY id", runID)
		for rows.Next() {
			var r string
			rows.Scan(&r)
			reasons = append(reasons, r)
		}
		rows.Close()
		want := "unknown_tool,malformed_json,succeeded,schema_violation,unknown_tool,malformed_json"
		if strings.Join(reasons, ",") != want {
			t.Fatalf("reasons %v", reasons)
		}
	})

	t.Run("a final answer on turn 20 is accepted", func(t *testing.T) {
		e := newEnv(t)
		var steps []fake.Step
		for i := 0; i < 19; i++ {
			steps = append(steps, fake.Step{ToolCall: readCall("r")})
		}
		steps = append(steps, fake.Step{Text: "done"})
		eng := e.engine(&fake.Provider{Steps: steps}, &fakeExec{})
		runID, _ := eng.CreateRun(context.Background(), e.alice, e.alpha, e.agent, "go", nil)
		e.waitStatus(runID, StSucceeded)
	})

	t.Run("a 21st turn fails the run", func(t *testing.T) {
		e := newEnv(t)
		var steps []fake.Step
		for i := 0; i < 21; i++ {
			steps = append(steps, fake.Step{ToolCall: readCall("r")})
		}
		p := &fake.Provider{Steps: steps}
		eng := e.engine(p, &fakeExec{})
		runID, _ := eng.CreateRun(context.Background(), e.alice, e.alpha, e.agent, "go", nil)
		e.waitStatus(runID, StFailed)
		if _, code := e.runStatus(runID); code != "step_limit" {
			t.Fatalf("code %s", code)
		}
		if p.Calls() != 20 {
			t.Fatalf("model calls %d", p.Calls())
		}
	})
}

// R4: every path ends in exactly one terminal state, and nothing is added
// after the final event.
func TestTerminalStatesAndCancellation(t *testing.T) {
	cases := []struct {
		name  string
		steps []fake.Step
		code  string
	}{
		{"permanent error", []fake.Step{{Err: &provider.Error{Class: provider.Permanent, Code: "provider_error", Status: 401, Msg: "invalid key sk-test-alice"}}}, "provider_error"},
		{"quota", []fake.Step{{Err: &provider.Error{Class: provider.Permanent, Code: "quota_exhausted", Status: 429, Msg: "insufficient_quota"}}}, "quota_exhausted"},
		{"retries exhausted", []fake.Step{
			{Err: &provider.Error{Class: provider.Transient, Code: "provider_error", Status: 503, Msg: "unavailable"}},
			{Err: &provider.Error{Class: provider.Transient, Code: "provider_error", Status: 503, Msg: "unavailable"}},
			{Partial: "half", Err: &provider.Error{Class: provider.Transient, Code: "provider_error", Msg: "stream broken"}},
			{Err: &provider.Error{Class: provider.Transient, Code: "provider_error", Status: 529, Msg: "overloaded"}},
		}, "retries_exhausted"},
		{"retry-after too long", []fake.Step{{Err: &provider.Error{Class: provider.Transient, Code: "rate_limited", Status: 429,
			HasRetryAfter: true, RetryAfter: 120 * time.Second, Msg: "slow down"}}}, "rate_limited"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			eng := e.engine(&fake.Provider{Steps: c.steps}, &fakeExec{})
			runID, _ := eng.CreateRun(context.Background(), e.alice, e.alpha, e.agent, "go", nil)
			e.waitStatus(runID, StFailed)
			if _, code := e.runStatus(runID); code != c.code {
				t.Fatalf("code %s, want %s", code, c.code)
			}
			if e.lastEvent(runID) != EvRunFinished {
				t.Fatal("run_finished is not the last event")
			}
			var msg string
			e.db.W.QueryRow("SELECT error_message FROM runs WHERE id = ?", runID).Scan(&msg)
			if strings.Contains(msg, "sk-test-alice") {
				t.Fatal("model key in the stored error message")
			}
		})
	}

	t.Run("a broken stream is retried and its partial output discarded", func(t *testing.T) {
		e := newEnv(t)
		eng := e.engine(&fake.Provider{Steps: []fake.Step{
			{Partial: "partial ", Usage: provider.Usage{Input: 10, Output: 2, Known: true},
				Err: &provider.Error{Class: provider.Transient, Code: "provider_error", Msg: "stream broken"}},
			{Text: "full answer", Usage: provider.Usage{Input: 10, Output: 5, Known: true}},
		}}, &fakeExec{})
		runID, _ := eng.CreateRun(context.Background(), e.alice, e.alpha, e.agent, "go", nil)
		e.waitStatus(runID, StSucceeded)
		var out string
		e.db.W.QueryRow("SELECT final_output FROM runs WHERE id = ?", runID).Scan(&out)
		if out != "full answer" {
			t.Fatalf("final output %q", out)
		}
		var outcomes string
		e.db.W.QueryRow("SELECT group_concat(outcome || ':' || usage_status, ',') FROM model_calls WHERE run_id = ? ORDER BY id", runID).Scan(&outcomes)
		if outcomes != "discarded:partial,succeeded:complete" {
			t.Fatalf("attempts %s", outcomes)
		}
		var discarded int
		e.db.W.QueryRow("SELECT COUNT(*) FROM run_events WHERE run_id = ? AND type = 'attempt_discarded'", runID).Scan(&discarded)
		if discarded != 1 {
			t.Fatal("no attempt_discarded event")
		}
	})

	t.Run("cancel during a model call", func(t *testing.T) {
		e := newEnv(t)
		eng := e.engine(&fake.Provider{Steps: []fake.Step{{Block: true}}}, &fakeExec{})
		runID, _ := eng.CreateRun(context.Background(), e.alice, e.alpha, e.agent, "go", nil)
		e.waitStatus(runID, StRunning)
		waitFor(t, func() bool {
			var n int
			e.db.W.QueryRow("SELECT COUNT(*) FROM model_calls WHERE run_id = ?", runID).Scan(&n)
			return n == 1
		})
		// bob is a member and not the starter: refused; alice (starter) may.
		if err := eng.Cancel(context.Background(), e.bob, e.alpha, false, runID); !errors.Is(err, ErrForbidden) {
			t.Fatalf("bob cancel: %v", err)
		}
		if err := eng.Cancel(context.Background(), e.alice, e.alpha, true, runID); err != nil {
			t.Fatal(err)
		}
		e.waitStatus(runID, StCancelled)
		waitFor(t, func() bool {
			var n int
			e.db.W.QueryRow("SELECT COUNT(*) FROM model_calls WHERE run_id = ? AND finished_at IS NOT NULL", runID).Scan(&n)
			return n == 1
		})
		var ce *ConflictError
		if err := eng.Cancel(context.Background(), e.alice, e.alpha, true, runID); !errors.As(err, &ce) {
			t.Fatalf("cancel of a finished run: %v", err)
		}
		if e.lastEvent(runID) != EvRunFinished {
			t.Fatal("event after run_finished")
		}
	})

	t.Run("cancel while waiting for approval closes the approval", func(t *testing.T) {
		e := newEnv(t)
		x := &fakeExec{}
		eng := e.engine(&fake.Provider{Steps: []fake.Step{{ToolCall: issueCall()}}}, x)
		runID, _ := eng.CreateRun(context.Background(), e.alice, e.alpha, e.agent, "go", nil)
		e.waitStatus(runID, StWaitingApproval)
		if err := eng.Cancel(context.Background(), e.alice, e.alpha, false, runID); err != nil {
			t.Fatal(err)
		}
		_, status, _, reason := e.call(runID)
		if status != TcRejected || reason != "run_cancelled" {
			t.Fatalf("call %s %s", status, reason)
		}
		callID, _, _, _ := e.call(runID)
		if err := eng.Approve(context.Background(), e.alice, e.alpha, runID, callID, 1); err == nil {
			t.Fatal("approved a call of a cancelled run")
		}
		if x.count() != 0 {
			t.Fatal("request sent")
		}
	})

	t.Run("missing model key fails the run clearly", func(t *testing.T) {
		e := newEnv(t)
		eng := e.engine(&fake.Provider{Steps: []fake.Step{{Gate: make(chan struct{})}}}, &fakeExec{})
		// bob has no key: the run is not created (R7: no fallback to alice's key).
		_, err := eng.CreateRun(context.Background(), e.bob, e.alpha, e.agent, "go", nil)
		var pe *PreconditionError
		if !errors.As(err, &pe) || !strings.Contains(pe.Msg, "openai API key") {
			t.Fatalf("err %v", err)
		}
	})
}

// R5, D21, D26, D55, D77, D86, D87: decisions race safely; stale revisions
// and changed credentials never lead to a request.
func TestApprovalRacesAndCredentialChanges(t *testing.T) {
	t.Run("approve and reject race: exactly one wins", func(t *testing.T) {
		for i := 0; i < 10; i++ {
			e := newEnv(t)
			x := &fakeExec{}
			eng := e.engine(&fake.Provider{Steps: []fake.Step{{ToolCall: issueCall()}, {Text: "ok"}}}, x)
			runID, _ := eng.CreateRun(context.Background(), e.alice, e.alpha, e.agent, "go", nil)
			e.waitStatus(runID, StWaitingApproval)
			callID, _, rev, _ := e.call(runID)
			var wg sync.WaitGroup
			errs := make([]error, 2)
			wg.Add(2)
			go func() {
				defer wg.Done()
				errs[0] = eng.Approve(context.Background(), e.alice, e.alpha, runID, callID, rev)
			}()
			go func() {
				defer wg.Done()
				errs[1] = eng.Reject(context.Background(), e.alice, e.alpha, runID, callID, rev, "no")
			}()
			wg.Wait()
			if (errs[0] == nil) == (errs[1] == nil) {
				t.Fatalf("approve %v, reject %v: want exactly one success", errs[0], errs[1])
			}
			e.waitStatus(runID, StSucceeded)
			if errs[0] == nil && x.count() != 1 || errs[1] == nil && x.count() != 0 {
				t.Fatalf("requests sent: %d", x.count())
			}
		}
	})

	t.Run("only the starter decides; other teams see nothing", func(t *testing.T) {
		e := newEnv(t)
		eng := e.engine(&fake.Provider{Steps: []fake.Step{{ToolCall: issueCall()}}}, &fakeExec{})
		runID, _ := eng.CreateRun(context.Background(), e.alice, e.alpha, e.agent, "go", nil)
		e.waitStatus(runID, StWaitingApproval)
		callID, _, rev, _ := e.call(runID)
		if err := eng.Approve(context.Background(), e.bob, e.alpha, runID, callID, rev); !errors.Is(err, ErrForbidden) {
			t.Fatalf("teammate: %v", err)
		}
		if err := eng.Approve(context.Background(), e.carol, e.beta, runID, callID, rev); !errors.Is(err, ErrNotFound) {
			t.Fatalf("other team: %v", err)
		}
		if err := eng.Cancel(context.Background(), e.carol, e.beta, true, runID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("other team's lead cancel: %v", err)
		}
		var ce *ConflictError
		if err := eng.Approve(context.Background(), e.alice, e.alpha, runID, callID, rev+1); !errors.As(err, &ce) {
			t.Fatalf("stale revision: %v", err)
		}
	})

	t.Run("deleted secret invalidates; a replacement re-opens with a new revision", func(t *testing.T) {
		e := newEnv(t)
		x := &fakeExec{}
		eng := e.engine(&fake.Provider{Steps: []fake.Step{{ToolCall: issueCall()}, {Text: "ok"}}}, x)
		runID, _ := eng.CreateRun(context.Background(), e.alice, e.alpha, e.agent, "go", nil)
		e.waitStatus(runID, StWaitingApproval)
		callID, _, _, _ := e.call(runID)

		inTx(t, e, func(tx *sql.Tx) {
			mustTx(t, tx, "DELETE FROM user_secrets WHERE id = ?", e.secret)
			if _, err := OnSecretDeleted(context.Background(), tx, e.alice, e.secret); err != nil {
				t.Fatal(err)
			}
		})
		if _, status, _, reason := e.call(runID); status != TcInvalidated || reason != "secret_deleted" {
			t.Fatalf("after delete: %s %s", status, reason)
		}
		if err := eng.Approve(context.Background(), e.alice, e.alpha, runID, callID, 1); err == nil {
			t.Fatal("approved an invalidated call")
		}

		newID := e.addSecret(e.alice, "GITHUB_TOKEN", "api.github.com", "ghp_new_value")
		inTx(t, e, func(tx *sql.Tx) {
			if _, err := OnSecretAdded(context.Background(), tx, e.alice, newID, "GITHUB_TOKEN", "api.github.com"); err != nil {
				t.Fatal(err)
			}
		})
		_, status, rev, _ := e.call(runID)
		if status != TcPending || rev != 2 {
			t.Fatalf("after replacement: %s rev %d", status, rev)
		}
		var ce *ConflictError
		if err := eng.Approve(context.Background(), e.alice, e.alpha, runID, callID, 1); !errors.As(err, &ce) {
			t.Fatalf("old revision accepted: %v", err)
		}
		if err := eng.Approve(context.Background(), e.alice, e.alpha, runID, callID, 2); err != nil {
			t.Fatal(err)
		}
		e.waitStatus(runID, StSucceeded)
		if x.count() != 1 || x.keys[0]["GITHUB_TOKEN"].Value != "ghp_new_value" {
			t.Fatal("the replacement secret was not used")
		}
	})

	t.Run("replacing the model key requires approval again", func(t *testing.T) {
		e := newEnv(t)
		eng := e.engine(&fake.Provider{Steps: []fake.Step{{ToolCall: issueCall()}}}, &fakeExec{})
		runID, _ := eng.CreateRun(context.Background(), e.alice, e.alpha, e.agent, "go", nil)
		e.waitStatus(runID, StWaitingApproval)
		inTx(t, e, func(tx *sql.Tx) {
			if _, err := OnModelKeyChanged(context.Background(), tx, e.alice, "openai", false); err != nil {
				t.Fatal(err)
			}
		})
		_, status, rev, reason := e.call(runID)
		if status != TcPending || rev != 2 || reason != "model_key_changed" {
			t.Fatalf("%s rev %d %s", status, rev, reason)
		}
	})
}

// D63, D91: a write interrupted by a crash becomes outcome_unknown and is never
// resent; an interrupted GET runs again.
func TestInterruptedWriteOutcomeUnknown(t *testing.T) {
	e := newEnv(t)
	x := &fakeExec{gate: make(chan struct{})} // the request "hangs" in flight
	eng := e.engine(&fake.Provider{Steps: []fake.Step{{ToolCall: issueCall()}}}, x)
	runID, _ := eng.CreateRun(context.Background(), e.alice, e.alpha, e.agent, "go", nil)
	e.waitStatus(runID, StWaitingApproval)
	callID, _, rev, _ := e.call(runID)
	if err := eng.Approve(context.Background(), e.alice, e.alpha, runID, callID, rev); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _, s, _, _ := e.call(runID); return s == TcExecuting })

	// Simulate a crash while the request is in flight: stop the workers, then
	// put the rows back into the state a crash would have left them in.
	eng.Stop()
	mustExec(t, e.db.W, "UPDATE tool_calls SET status = 'executing', reason_code = NULL WHERE id = ?", callID)
	mustExec(t, e.db.W, "DELETE FROM run_messages WHERE run_id = ? AND role = 'tool'", runID)
	mustExec(t, e.db.W, "UPDATE runs SET status = 'running' WHERE id = ?", runID)

	x2 := &fakeExec{}
	p2 := &fake.Provider{Steps: []fake.Step{{Text: "The issue may or may not exist."}}}
	e.restart(eng, p2, x2)
	e.waitStatus(runID, StSucceeded)
	if _, status, _, reason := e.call(runID); status != TcOutcomeUnknown || reason != "interrupted" {
		t.Fatalf("call %s %s", status, reason)
	}
	if x2.count() != 0 {
		t.Fatal("an interrupted write was resent")
	}
	msgs := p2.Requests[0].Messages
	if m := msgs[len(msgs)-1]; m.Role != "tool" || !strings.Contains(m.Content, "unknown") {
		t.Fatalf("model not told: %+v", m)
	}

	t.Run("an interrupted GET runs again", func(t *testing.T) {
		e := newEnv(t)
		eng := e.engine(&fake.Provider{Steps: []fake.Step{{ToolCall: readCall("r1")}, {Block: true}}}, &fakeExec{})
		runID, _ := eng.CreateRun(context.Background(), e.alice, e.alpha, e.agent, "go", nil)
		waitFor(t, func() bool { _, s, _, _ := e.call(runID); return s == TcSucceeded })
		mustExec(t, e.db.W, "UPDATE tool_calls SET status = 'executing' WHERE run_id = ?", runID)
		mustExec(t, e.db.W, "DELETE FROM run_messages WHERE run_id = ? AND role = 'tool'", runID)
		x2 := &fakeExec{}
		e.restart(eng, &fake.Provider{Steps: []fake.Step{{Text: "done"}}}, x2)
		e.waitStatus(runID, StSucceeded)
		if x2.count() != 1 {
			t.Fatalf("GET re-sent %d times, want 1", x2.count())
		}
	})
}

// D90: an approval left waiting past its expiry cancels the run.
func TestApprovalExpiry(t *testing.T) {
	e := newEnv(t)
	eng := e.engine(&fake.Provider{Steps: []fake.Step{{ToolCall: issueCall()}}}, &fakeExec{})
	runID, _ := eng.CreateRun(context.Background(), e.alice, e.alpha, e.agent, "go", nil)
	e.waitStatus(runID, StWaitingApproval)

	eng.Now = func() time.Time { return time.Now().Add(73 * time.Hour) }
	eng.expireApprovals(context.Background())
	if s, code := e.runStatus(runID); s != StCancelled || code != "approval_expired" {
		t.Fatalf("%s %s", s, code)
	}
	if _, status, _, reason := e.call(runID); status != TcRejected || reason != "approval_expired" {
		t.Fatalf("call %s %s", status, reason)
	}

	// Friday 10:00 → Monday 10:00; Saturday 09:00 → Monday 09:00.
	fri := time.Date(2026, 11, 6, 10, 0, 0, 0, time.Local)
	if got := ApprovalExpiry(fri); got.Weekday() != time.Monday || got.Hour() != 10 {
		t.Fatalf("friday: %v", got)
	}
	if got := ApprovalExpiry(time.Date(2026, 11, 4, 10, 0, 0, 0, time.Local)); got.Weekday() != time.Thursday {
		t.Fatalf("wednesday: %v", got)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not reached")
}

func inTx(t *testing.T, e *env, fn func(tx *sql.Tx)) {
	t.Helper()
	tx, err := e.db.W.Begin()
	if err != nil {
		t.Fatal(err)
	}
	fn(tx)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func mustTx(t *testing.T, tx *sql.Tx, q string, args ...any) {
	t.Helper()
	if _, err := tx.Exec(q, args...); err != nil {
		t.Fatal(err)
	}
}
