package run

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/kaang/vodafone-case/backend/internal/provider"
	"github.com/kaang/vodafone-case/backend/internal/provider/fake"
)

// Regression tests for the first code review (docs/reviews/2026-11-04-code-1,
// decision D124). Each test names the finding it covers.

// streamThenWait streams one delta, then waits until the context ends.
type streamThenWait struct {
	text    string
	started chan struct{}
}

func (p *streamThenWait) Stream(ctx context.Context, _ provider.Request, cb provider.Callbacks) (provider.Result, error) {
	cb.OnText(p.text)
	close(p.started)
	<-ctx.Done()
	return provider.Result{}, ctx.Err()
}

func (e *env) eventSeqs(runID int64, typ string) []int64 {
	rows, err := e.db.W.Query("SELECT seq FROM run_events WHERE run_id = ? AND type = ? ORDER BY seq", runID, typ)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var s int64
		rows.Scan(&s)
		out = append(out, s)
	}
	return out
}

func (e *env) isActive(eng *Engine, runID int64) bool {
	eng.mu.Lock()
	defer eng.mu.Unlock()
	_, busy := eng.active[runID]
	return busy
}

// F02: a decision that arrives while the worker that set waiting_approval is
// still exiting is not lost; the run continues after approval or rejection.
func TestDecisionWhileWorkerExits(t *testing.T) {
	for _, approve := range []bool{true, false} {
		e := newEnv(t)
		x := &fakeExec{}
		eng := e.engine(&fake.Provider{Steps: []fake.Step{{ToolCall: issueCall()}, {Text: "done"}}}, x)
		runID, _ := eng.CreateRun(context.Background(), e.alice, e.alpha, e.agent, "go", nil)
		e.waitStatus(runID, StWaitingApproval)
		waitFor(t, func() bool { return !e.isActive(eng, runID) })

		// The worker that set waiting_approval has not let go of the run yet.
		eng.mu.Lock()
		eng.active[runID] = func() {}
		eng.mu.Unlock()
		callID, _, rev, _ := e.call(runID)
		var err error
		if approve {
			err = eng.Approve(context.Background(), e.alice, e.alpha, runID, callID, rev)
		} else {
			err = eng.Reject(context.Background(), e.alice, e.alpha, runID, callID, rev, "no")
		}
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(50 * time.Millisecond)
		if s, _ := e.runStatus(runID); s != StRunning || x.count() != 0 {
			t.Fatalf("another worker took the run: %s, %d sent", s, x.count())
		}

		// Now it lets go: it must process the run once more, exactly once.
		ctx, ok := eng.release(runID)
		if !ok {
			t.Fatal("the decision was lost")
		}
		eng.process(ctx, runID)
		if _, again := eng.release(runID); again {
			t.Fatal("processed more than once")
		}
		if s, _ := e.runStatus(runID); s != StSucceeded {
			t.Fatalf("approve=%v: run %s", approve, s)
		}
		if want := map[bool]int{true: 1, false: 0}[approve]; x.count() != want {
			t.Fatalf("approve=%v: sent %d", approve, x.count())
		}
	}
}

// F15: more resumable runs than the old 1,024-slot queue all finish.
func TestQueueKeepsEveryRun(t *testing.T) {
	e := newEnv(t)
	eng := e.engine(&fake.Provider{}, &fakeExec{})
	eng.Stop()
	first, err := eng.CreateRun(context.Background(), e.alice, e.alpha, e.agent, "go", nil)
	if err != nil {
		t.Fatal(err)
	}
	// 1,100 more queued runs whose active time is used up: each finishes in
	// two transactions without a model call.
	mustExec(t, e.db.W, `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 1100)
		INSERT INTO runs (team_id, agent_id, started_by, task, status, snapshot, created_at)
		SELECT team_id, agent_id, started_by, task, 'queued', snapshot, created_at FROM runs, n WHERE runs.id = ?`, first)
	mustExec(t, e.db.W, "UPDATE runs SET active_ms = 3600000")

	e.restart(eng, &fake.Provider{}, &fakeExec{})
	deadline := time.Now().Add(60 * time.Second)
	for {
		var open int
		e.db.W.QueryRow("SELECT COUNT(*) FROM runs WHERE status NOT IN ('succeeded', 'failed', 'cancelled')").Scan(&open)
		if open == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d runs never ran", open)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// F03: a call ID outside the authorized run is not found; nothing about it
// is returned.
func TestDecisionOnForeignCall(t *testing.T) {
	e := newEnv(t)
	eng := e.engine(&fake.Provider{Steps: []fake.Step{{ToolCall: issueCall()}, {ToolCall: issueCall()}}}, &fakeExec{})
	runA, _ := eng.CreateRun(context.Background(), e.alice, e.alpha, e.agent, "a", nil)
	e.waitStatus(runA, StWaitingApproval)
	runB, _ := eng.CreateRun(context.Background(), e.alice, e.alpha, e.agent, "b", nil)
	e.waitStatus(runB, StWaitingApproval)
	callB, _, rev, _ := e.call(runB)
	if err := eng.Approve(context.Background(), e.alice, e.alpha, runA, callB, rev); !errors.Is(err, ErrNotFound) {
		t.Fatalf("approve: %v", err)
	}
	if err := eng.Reject(context.Background(), e.alice, e.alpha, runA, callB, rev, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reject: %v", err)
	}
}

// F07: an expired request cannot be decided or sent, even before the sweep.
func TestExpiredApprovalNeverActs(t *testing.T) {
	later := func() time.Time { return time.Now().Add(73 * time.Hour) }
	for _, decide := range []string{"approve", "reject"} {
		e := newEnv(t)
		x := &fakeExec{}
		eng := e.engine(&fake.Provider{Steps: []fake.Step{{ToolCall: issueCall()}}}, x)
		runID, _ := eng.CreateRun(context.Background(), e.alice, e.alpha, e.agent, "go", nil)
		e.waitStatus(runID, StWaitingApproval)
		callID, _, rev, _ := e.call(runID)
		eng.Now = later
		var err error
		if decide == "approve" {
			err = eng.Approve(context.Background(), e.alice, e.alpha, runID, callID, rev)
		} else {
			err = eng.Reject(context.Background(), e.alice, e.alpha, runID, callID, rev, "")
		}
		var ce *ConflictError
		if !errors.As(err, &ce) {
			t.Fatalf("%s after expiry: %v", decide, err)
		}
		if s, code := e.runStatus(runID); s != StCancelled || code != "approval_expired" {
			t.Fatalf("%s: run %s %s", decide, s, code)
		}
		time.Sleep(30 * time.Millisecond)
		if x.count() != 0 {
			t.Fatal("expired request sent")
		}
	}

	t.Run("approved but not yet claimed", func(t *testing.T) {
		e := newEnv(t)
		x := &fakeExec{}
		eng := e.engine(&fake.Provider{Steps: []fake.Step{{ToolCall: issueCall()}}}, x)
		runID, _ := eng.CreateRun(context.Background(), e.alice, e.alpha, e.agent, "go", nil)
		e.waitStatus(runID, StWaitingApproval)
		waitFor(t, func() bool { return !e.isActive(eng, runID) })
		mustExec(t, e.db.W, "UPDATE tool_calls SET status = 'approved' WHERE run_id = ?", runID)
		mustExec(t, e.db.W, "UPDATE runs SET status = 'running' WHERE id = ?", runID)
		eng.Now = later
		r, _ := loadRun(context.Background(), e.db.W, runID)
		c, _ := eng.openCall(context.Background(), runID)
		if err := eng.executeCall(context.Background(), r, c); !errors.Is(err, errStop) {
			t.Fatalf("claim: %v", err)
		}
		if x.count() != 0 {
			t.Fatal("expired request sent")
		}
		if s, code := e.runStatus(runID); s != StCancelled || code != "approval_expired" {
			t.Fatalf("run %s %s", s, code)
		}
	})

	t.Run("a stale sweep candidate that was claimed is left alone", func(t *testing.T) {
		e := newEnv(t)
		eng := e.engine(&fake.Provider{Steps: []fake.Step{{ToolCall: issueCall()}}}, &fakeExec{})
		runID, _ := eng.CreateRun(context.Background(), e.alice, e.alpha, e.agent, "go", nil)
		e.waitStatus(runID, StWaitingApproval)
		mustExec(t, e.db.W, "UPDATE tool_calls SET status = 'executing' WHERE run_id = ?", runID)
		inTx(t, e, func(tx *sql.Tx) {
			if done, err := expireTx(context.Background(), tx, runID, later()); err != nil || done {
				t.Fatalf("expired a claimed call: %v %v", done, err)
			}
		})
	})
}

// F08: cancelling while a write is being sent closes it as outcome_unknown
// before run_finished; a late success does not overwrite it.
func TestCancelDuringWrite(t *testing.T) {
	e := newEnv(t)
	x := &fakeExec{gate: make(chan struct{}), late: true}
	eng := e.engine(&fake.Provider{Steps: []fake.Step{{ToolCall: issueCall()}}}, x)
	gateOpen := false
	t.Cleanup(func() { // runs before eng.Stop, so a failed check cannot hang the worker
		if !gateOpen {
			close(x.gate)
		}
	})
	runID, _ := eng.CreateRun(context.Background(), e.alice, e.alpha, e.agent, "go", nil)
	e.waitStatus(runID, StWaitingApproval)
	callID, _, rev, _ := e.call(runID)
	if err := eng.Approve(context.Background(), e.alice, e.alpha, runID, callID, rev); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _, s, _, _ := e.call(runID); return s == TcExecuting })
	if err := eng.Cancel(context.Background(), e.alice, e.alpha, true, runID); err != nil {
		t.Fatal(err)
	}
	check := func() {
		t.Helper()
		if _, s, _, reason := e.call(runID); s != TcOutcomeUnknown || reason != "run_cancelled" {
			t.Fatalf("call %s %s", s, reason)
		}
		if e.lastEvent(runID) != EvRunFinished {
			t.Fatalf("last event %s", e.lastEvent(runID))
		}
	}
	check()
	close(x.gate) // the request completes successfully after the cancel
	gateOpen = true
	waitFor(t, func() bool { return !e.isActive(eng, runID) })
	check()
}

// F09: restoring the secret and the model key in either order returns the
// call to approval.
func TestCredentialRestorationOrder(t *testing.T) {
	for _, secretFirst := range []bool{true, false} {
		e := newEnv(t)
		eng := e.engine(&fake.Provider{Steps: []fake.Step{{ToolCall: issueCall()}}}, &fakeExec{})
		runID, _ := eng.CreateRun(context.Background(), e.alice, e.alpha, e.agent, "go", nil)
		e.waitStatus(runID, StWaitingApproval)
		inTx(t, e, func(tx *sql.Tx) {
			mustTx(t, tx, "DELETE FROM user_secrets WHERE id = ?", e.secret)
			OnSecretDeleted(context.Background(), tx, e.alice, e.secret)
			mustTx(t, tx, "DELETE FROM user_model_keys WHERE user_id = ?", e.alice)
			OnModelKeyChanged(context.Background(), tx, e.alice, "openai", true)
		})
		restoreSecret := func() {
			id := e.addSecret(e.alice, "GITHUB_TOKEN", "api.github.com", "ghp_new")
			inTx(t, e, func(tx *sql.Tx) {
				OnSecretAdded(context.Background(), tx, e.alice, id, "GITHUB_TOKEN", "api.github.com")
			})
		}
		restoreKey := func() {
			e.setModelKey(e.alice, "sk-new")
			inTx(t, e, func(tx *sql.Tx) { OnModelKeyChanged(context.Background(), tx, e.alice, "openai", false) })
		}
		if secretFirst {
			restoreSecret()
			if _, s, _, reason := e.call(runID); s != TcInvalidated || reason != "model_key_deleted" {
				t.Fatalf("after secret: %s %s", s, reason)
			}
			restoreKey()
		} else {
			restoreKey()
			if _, s, _, reason := e.call(runID); s != TcInvalidated || reason != "secret_deleted" {
				t.Fatalf("after key: %s %s", s, reason)
			}
			restoreSecret()
		}
		if _, s, rev, _ := e.call(runID); s != TcPending || rev != 2 {
			t.Fatalf("secretFirst=%v: %s rev %d", secretFirst, s, rev)
		}
	}
}

// F11: a same-name secret for another host is not bound, also when another
// tool of the run uses that name on the new host.
func TestReplacementSecretHost(t *testing.T) {
	e := newEnv(t)
	e.addTool(3, "upload", "POST", true, "https://uploads.github.com/x",
		map[string]string{"Authorization": "Bearer {{secret.GITHUB_TOKEN}}"}, "", `{"type":"object","properties":{}}`)
	eng := e.engine(&fake.Provider{Steps: []fake.Step{{ToolCall: issueCall()}}}, &fakeExec{})
	runID, _ := eng.CreateRun(context.Background(), e.alice, e.alpha, e.agent, "go", nil)
	e.waitStatus(runID, StWaitingApproval)
	inTx(t, e, func(tx *sql.Tx) {
		mustTx(t, tx, "DELETE FROM user_secrets WHERE id = ?", e.secret)
		OnSecretDeleted(context.Background(), tx, e.alice, e.secret)
	})
	for _, host := range []string{"evil.example.com", "uploads.github.com"} {
		mustExec(t, e.db.W, "DELETE FROM user_secrets WHERE user_id = ?", e.alice)
		id := e.addSecret(e.alice, "GITHUB_TOKEN", host, "ghp_other")
		inTx(t, e, func(tx *sql.Tx) { OnSecretAdded(context.Background(), tx, e.alice, id, "GITHUB_TOKEN", host) })
		if _, s, _, _ := e.call(runID); s != TcInvalidated {
			t.Fatalf("%s: call %s", host, s)
		}
		var snap string
		e.db.W.QueryRow("SELECT snapshot FROM runs WHERE id = ?", runID).Scan(&snap)
		var sn Snapshot
		json.Unmarshal([]byte(snap), &sn)
		if sn.Secrets["GITHUB_TOKEN"] != e.secret {
			t.Fatalf("%s: binding replaced", host)
		}
	}
}

// F10: a call that starts waiting later gets an expiry, which the sweep
// honours; an existing expiry never moves.
func TestExpiryForLateWaits(t *testing.T) {
	e := newEnv(t)
	eng := e.engine(&fake.Provider{Steps: []fake.Step{{ToolCall: issueCall()}}}, &fakeExec{})
	runID, _ := eng.CreateRun(context.Background(), e.alice, e.alpha, e.agent, "go", nil)
	e.waitStatus(runID, StWaitingApproval)
	waitFor(t, func() bool { return !e.isActive(eng, runID) })
	expiry := func() string {
		var v sql.NullString
		e.db.W.QueryRow("SELECT approval_expires_at FROM tool_calls WHERE run_id = ?", runID).Scan(&v)
		return v.String
	}
	first := expiry()
	inTx(t, e, func(tx *sql.Tx) {
		mustTx(t, tx, "DELETE FROM user_secrets WHERE id = ?", e.secret)
		OnSecretDeleted(context.Background(), tx, e.alice, e.secret)
	})
	if expiry() != first {
		t.Fatal("invalidation moved the expiry")
	}

	// A no-approval call (no expiry) still approved when the key is replaced.
	mustExec(t, e.db.W, "UPDATE tool_calls SET status = 'approved', approval_expires_at = NULL, reason_code = NULL WHERE run_id = ?", runID)
	newID := e.addSecret(e.alice, "GITHUB_TOKEN", "api.github.com", "ghp_new")
	mustExec(t, e.db.W, "UPDATE runs SET snapshot = json_set(snapshot, '$.secrets.GITHUB_TOKEN', ?) WHERE id = ?", newID, runID)
	inTx(t, e, func(tx *sql.Tx) { OnModelKeyChanged(context.Background(), tx, e.alice, "openai", false) })
	if _, s, _, _ := e.call(runID); s != TcPending || expiry() == "" {
		t.Fatalf("call %s, expiry %q", s, expiry())
	}
	eng.Now = func() time.Time { return time.Now().Add(73 * time.Hour) }
	eng.expireApprovals(context.Background())
	if s, code := e.runStatus(runID); s != StCancelled || code != "approval_expired" {
		t.Fatalf("run %s %s", s, code)
	}
}

// F14: partial text of an attempt cut by a stop or a crash is marked
// discarded after the restart, before the retried answer.
func TestDiscardMarkerAfterRestart(t *testing.T) {
	for _, crash := range []bool{false, true} {
		e := newEnv(t)
		p := &streamThenWait{text: "partial answer", started: make(chan struct{})}
		eng := New(e.db, e.box, map[string]provider.Provider{"openai": p}, &fakeExec{})
		eng.Sleep = func(ctx context.Context, d time.Duration) error { return ctx.Err() }
		if err := eng.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		runID, _ := eng.CreateRun(context.Background(), e.alice, e.alpha, e.agent, "go", nil)
		<-p.started
		waitFor(t, func() bool { return len(e.eventSeqs(runID, EvTextDelta)) == 1 })
		eng.Stop()
		if crash { // the attempt was never closed
			mustExec(t, e.db.W, "UPDATE model_calls SET finished_at = NULL, outcome = NULL, error_code = NULL")
		}
		e.restart(eng, &fake.Provider{Steps: []fake.Step{{Text: "final answer"}}}, &fakeExec{})
		e.waitStatus(runID, StSucceeded)
		discarded := e.eventSeqs(runID, EvAttemptDiscarded)
		texts := e.eventSeqs(runID, EvTextDelta)
		if len(discarded) != 1 || len(texts) != 2 || !(texts[0] < discarded[0] && discarded[0] < texts[1]) {
			t.Fatalf("crash=%v: discarded %v, text %v", crash, discarded, texts)
		}
	}
}

// F16: text is flushed on a timer while the stream is still open. F06: text
// buffered when the run is cancelled is never written after run_finished.
func TestTextFlushTimer(t *testing.T) {
	e := newEnv(t)
	p := &streamThenWait{text: "hello", started: make(chan struct{})}
	eng := New(e.db, e.box, map[string]provider.Provider{"openai": p}, &fakeExec{})
	if err := eng.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.Stop)
	runID, _ := eng.CreateRun(context.Background(), e.alice, e.alpha, e.agent, "go", nil)
	<-p.started
	waitFor(t, func() bool { return len(e.eventSeqs(runID, EvTextDelta)) == 1 })

	t.Run("no text after run_finished", func(t *testing.T) {
		e := newEnv(t)
		p := &streamThenWait{text: "late text", started: make(chan struct{})}
		eng := New(e.db, e.box, map[string]provider.Provider{"openai": p}, &fakeExec{})
		frozen := time.Now()
		eng.Now = func() time.Time { return frozen } // the delta is always inside the 250 ms window
		if err := eng.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(eng.Stop)
		runID, _ := eng.CreateRun(context.Background(), e.alice, e.alpha, e.agent, "go", nil)
		<-p.started
		if err := eng.Cancel(context.Background(), e.alice, e.alpha, true, runID); err != nil {
			t.Fatal(err)
		}
		waitFor(t, func() bool { return !e.isActive(eng, runID) })
		time.Sleep(400 * time.Millisecond) // past the flush timer
		if e.lastEvent(runID) != EvRunFinished || len(e.eventSeqs(runID, EvTextDelta)) != 0 {
			t.Fatalf("last event %s, text events %v", e.lastEvent(runID), e.eventSeqs(runID, EvTextDelta))
		}
	})
}

// F17: a GET's retry budget survives a restart: after two recorded attempts
// it is not sent again; after one it gets exactly one more.
func TestGetBudgetAcrossRestart(t *testing.T) {
	for used, wantSent := range map[int]int{2: 0, 1: 1} {
		e := newEnv(t)
		eng := e.engine(&fake.Provider{Steps: []fake.Step{{ToolCall: readCall("r1")}, {Block: true}}}, &fakeExec{})
		runID, _ := eng.CreateRun(context.Background(), e.alice, e.alpha, e.agent, "go", nil)
		waitFor(t, func() bool { _, s, _, _ := e.call(runID); return s == TcSucceeded })
		mustExec(t, e.db.W, "UPDATE tool_calls SET status = 'executing', attempt_count = ? WHERE run_id = ?", used, runID)
		mustExec(t, e.db.W, "DELETE FROM run_messages WHERE run_id = ? AND role = 'tool'", runID)
		x2 := &fakeExec{}
		e.restart(eng, &fake.Provider{Steps: []fake.Step{{Text: "done"}}}, x2)
		e.waitStatus(runID, StSucceeded)
		if x2.count() != wantSent {
			t.Fatalf("used %d: sent %d, want %d", used, x2.count(), wantSent)
		}
		if used == 2 {
			if _, s, _, reason := e.call(runID); s != TcFailed || reason != "interrupted" {
				t.Fatalf("call %s %s", s, reason)
			}
		}
	}
}
