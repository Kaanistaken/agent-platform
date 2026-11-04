package run

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kaang/vodafone-case/backend/internal/crypto"
	"github.com/kaang/vodafone-case/backend/internal/db"
	"github.com/kaang/vodafone-case/backend/internal/provider"
	"github.com/kaang/vodafone-case/backend/internal/tool"
)

// modelKey decrypts the starter's key for the provider. ok is false when the
// starter has no key; another user's key is never used (R7).
func (e *Engine) modelKey(ctx context.Context, userID int64, prov string) (string, bool, error) {
	var ct, nonce []byte
	err := e.DB.W.QueryRowContext(ctx, "SELECT ciphertext, nonce FROM user_model_keys WHERE user_id = ? AND provider = ?",
		userID, prov).Scan(&ct, &nonce)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	pt, err := e.Box.Open(ct, nonce, crypto.ModelKeyAAD(userID, prov))
	if err != nil {
		return "", false, err
	}
	return string(pt), true, nil
}

func loadMessages(ctx context.Context, q *sql.DB, runID int64) ([]provider.Message, error) {
	rows, err := q.QueryContext(ctx, `SELECT role, content, tool_calls, tool_call_id, raw_content
		FROM run_messages WHERE run_id = ? ORDER BY seq`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []provider.Message
	for rows.Next() {
		var m provider.Message
		var calls, callID, raw sql.NullString
		if err := rows.Scan(&m.Role, &m.Content, &calls, &callID, &raw); err != nil {
			return nil, err
		}
		if calls.Valid {
			if err := json.Unmarshal([]byte(calls.String), &m.ToolCalls); err != nil {
				return nil, err
			}
		}
		m.ToolCallID = callID.String
		if raw.Valid {
			m.Raw = json.RawMessage(raw.String)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func addMessage(ctx context.Context, tx *sql.Tx, runID int64, m provider.Message) error {
	var calls, raw any
	if len(m.ToolCalls) > 0 {
		b, _ := json.Marshal(m.ToolCalls)
		calls = string(b)
	}
	if len(m.Raw) > 0 {
		raw = string(m.Raw)
	}
	var callID any
	if m.ToolCallID != "" {
		callID = m.ToolCallID
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO run_messages (run_id, seq, role, content, tool_calls, tool_call_id, raw_content, created_at)
		VALUES (?, (SELECT COALESCE(MAX(seq), 0) + 1 FROM run_messages WHERE run_id = ?), ?, ?, ?, ?, ?, ?)`,
		runID, runID, m.Role, m.Content, calls, callID, raw, db.Now())
	return err
}

// textBuffer coalesces streamed text into events about every 250 ms (D70).
type textBuffer struct {
	e        *Engine
	ctx      context.Context
	runID    int64
	callID   int64
	attempt  int
	pending  strings.Builder
	last     time.Time
	streamed bool
}

func (b *textBuffer) add(delta string) {
	if delta == "" {
		return
	}
	b.streamed = true
	b.pending.WriteString(delta)
	if b.e.Now().Sub(b.last) >= textFlushInterval {
		b.flush()
	}
}

func (b *textBuffer) flush() {
	if b.pending.Len() == 0 {
		return
	}
	text := b.pending.String()
	b.pending.Reset()
	b.last = b.e.Now()
	b.e.withTx(context.Background(), b.runID, func(tx *sql.Tx) error {
		return emit(b.ctx, tx, b.runID, EvTextDelta, map[string]any{
			"model_call_id": b.callID, "attempt": b.attempt, "text": text})
	})
}

// modelTurn makes one model turn, with retries owned here (D79, D88, D92).
func (e *Engine) modelTurn(ctx context.Context, r *runState) error {
	snap := r.Snapshot
	prov, ok := e.Providers[snap.Agent.Provider]
	if !ok {
		e.finish(context.Background(), r.ID, StFailed, "internal", "provider "+snap.Agent.Provider+" is not configured", "")
		return errStop
	}
	key, ok, err := e.modelKey(ctx, r.StartedBy, snap.Agent.Provider)
	if err != nil {
		return err
	}
	if !ok {
		e.finish(context.Background(), r.ID, StFailed, "model_key_missing",
			fmt.Sprintf("the run's starter has no %s API key", snap.Agent.Provider), "")
		return errStop
	}
	msgs, err := loadMessages(ctx, e.DB.W, r.ID)
	if err != nil {
		return err
	}
	req := provider.Request{
		APIKey: key, Model: snap.Agent.Model, System: snap.Agent.SystemPrompt,
		Messages: msgs, MaxOutputTokens: MaxOutputTokens,
	}
	for _, t := range snap.Tools {
		req.Tools = append(req.Tools, provider.ToolSpec{Name: t.Name, Description: t.Description, Parameters: t.ParamsSchema})
	}
	turn := r.StepCount + 1

	// Attempts already made for this turn (before a restart) count (D94).
	var done int
	e.DB.W.QueryRowContext(ctx, "SELECT COUNT(*) FROM model_calls WHERE run_id = ? AND turn = ?", r.ID, turn).Scan(&done)

	for attempt := done + 1; ; attempt++ {
		if attempt > MaxAttempts {
			e.finish(context.Background(), r.ID, StFailed, "retries_exhausted", "the model provider kept failing after 4 attempts", "")
			return errStop
		}
		res, perr, mcID, streamed := e.attempt(ctx, r, prov, req, turn, attempt)
		if perr == nil {
			return e.acceptTurn(ctx, r, mcID, res)
		}
		if ctx.Err() != nil {
			// Cancelled, shut down or past the run deadline: the caller decides.
			return errStop
		}

		var pe *provider.Error
		if !errors.As(perr, &pe) {
			pe = &provider.Error{Class: provider.Transient, Code: "provider_error", Msg: perr.Error()}
		}
		msg := tool.Mask(pe.Error(), []string{key})
		if pe.Class == provider.Permanent {
			e.endAttempt(mcID, "failed", pe.Code)
			e.finish(context.Background(), r.ID, StFailed, pe.Code, msg, "")
			return errStop
		}

		// Transient: a broken stream's partial output is discarded (D92).
		outcome := "retried"
		if streamed {
			outcome = "discarded"
		}
		if attempt == MaxAttempts {
			e.endAttempt(mcID, "failed", pe.Code)
			e.finish(context.Background(), r.ID, StFailed, "retries_exhausted",
				"the model provider kept failing after 4 attempts: "+msg, "")
			return errStop
		}
		wait := time.Duration(1<<(attempt-1))*time.Second + jitter()
		if pe.HasRetryAfter {
			if pe.RetryAfter > MaxRetryAfter {
				e.endAttempt(mcID, "failed", pe.Code)
				e.finish(context.Background(), r.ID, StFailed, "rate_limited",
					fmt.Sprintf("the provider asked to wait %s (more than 60 s): %s", pe.RetryAfter, msg), "")
				return errStop
			}
			wait = pe.RetryAfter
		}
		e.withTx(context.Background(), r.ID, func(tx *sql.Tx) error {
			if err := endAttemptTx(ctx, tx, mcID, outcome, pe.Code, e.Now()); err != nil {
				return err
			}
			if streamed {
				if err := emit(ctx, tx, r.ID, EvAttemptDiscarded, map[string]any{"model_call_id": mcID, "attempt": attempt}); err != nil {
					return err
				}
			}
			return emit(ctx, tx, r.ID, EvModelRetry, map[string]any{
				"attempt": attempt, "wait_seconds": wait.Seconds(), "reason": msg})
		})
		if err := e.Sleep(ctx, wait); err != nil {
			return errStop // waits are cancellable (D79)
		}
	}
}

// attempt makes one provider call and records it in model_calls (one row per
// attempt, usage kept even when the call fails, D75).
func (e *Engine) attempt(ctx context.Context, r *runState, prov provider.Provider, req provider.Request, turn, attempt int) (provider.Result, error, int64, bool) {
	var mcID int64
	err := e.withTx(context.Background(), 0, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO model_calls (run_id, turn, attempt, provider, model, started_at)
			VALUES (?, ?, ?, ?, ?, ?)`, r.ID, turn, attempt, r.Snapshot.Agent.Provider, r.Snapshot.Agent.Model, db.Format(e.Now()))
		if err != nil {
			return err
		}
		mcID, _ = res.LastInsertId()
		return nil
	})
	if err != nil {
		return provider.Result{}, err, 0, false
	}

	buf := &textBuffer{e: e, ctx: context.Background(), runID: r.ID, callID: mcID, attempt: attempt, last: e.Now()}
	actx, cancel := context.WithTimeout(ctx, ModelAttemptTimeout)
	defer cancel()
	res, err := prov.Stream(actx, req, provider.Callbacks{
		OnText: buf.add,
		OnUsage: func(u provider.Usage) {
			if !u.Known {
				return
			}
			e.DB.W.ExecContext(context.Background(), `UPDATE model_calls SET input_tokens = ?, output_tokens = ?,
				usage_status = 'partial' WHERE id = ?`, u.Input, u.Output, mcID)
		},
	})
	buf.flush()
	if err != nil && actx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
		// The attempt's own timeout is transient (D94).
		err = &provider.Error{Class: provider.Transient, Code: "provider_error", Msg: "the model call timed out"}
	}
	if err != nil && ctx.Err() != nil {
		e.endAttempt(mcID, "failed", "interrupted")
	}
	return res, err, mcID, buf.streamed
}

func (e *Engine) endAttempt(mcID int64, outcome, code string) {
	e.withTx(context.Background(), 0, func(tx *sql.Tx) error {
		return endAttemptTx(context.Background(), tx, mcID, outcome, code, e.Now())
	})
}

func endAttemptTx(ctx context.Context, tx *sql.Tx, mcID int64, outcome, code string, now time.Time) error {
	_, err := tx.ExecContext(ctx, `UPDATE model_calls SET outcome = ?, error_code = NULLIF(?, ''), finished_at = ?,
		usage_status = CASE WHEN input_tokens IS NULL THEN 'unavailable' ELSE 'partial' END
		WHERE id = ? AND finished_at IS NULL`, outcome, code, db.Format(now), mcID)
	return err
}

// acceptTurn stores a complete model turn atomically with its consequence:
// the final answer, an invalid call returned to the model, or a new tool call
// (D94).
func (e *Engine) acceptTurn(ctx context.Context, r *runState, mcID int64, res provider.Result) error {
	stopped := false
	err := e.withTx(context.Background(), r.ID, func(tx *sql.Tx) error {
		now := e.Now()
		// Usage reported by the adapter during streaming is final now.
		if _, err := tx.ExecContext(ctx, `UPDATE model_calls SET outcome = 'succeeded', finished_at = ?,
			usage_status = CASE WHEN input_tokens IS NULL THEN 'unavailable' ELSE 'complete' END
			WHERE id = ?`, db.Format(now), mcID); err != nil {
			return err
		}
		var runStatus string
		if err := tx.QueryRowContext(ctx, "SELECT status FROM runs WHERE id = ?", r.ID).Scan(&runStatus); err != nil {
			return err
		}
		if terminal(runStatus) {
			// Cancelled while the model was answering: keep the usage record,
			// add nothing after the final event.
			stopped = true
			return nil
		}

		var in, out sql.NullInt64
		var status string
		if err := tx.QueryRowContext(ctx, "SELECT input_tokens, output_tokens, usage_status FROM model_calls WHERE id = ?", mcID).
			Scan(&in, &out, &status); err != nil {
			return err
		}
		if err := emit(ctx, tx, r.ID, EvUsage, map[string]any{
			"model_call_id": mcID, "input_tokens": nullInt(in), "output_tokens": nullInt(out), "usage_status": status}); err != nil {
			return err
		}

		calls := res.ToolCalls
		if len(calls) > 1 {
			// Parallel calls are disabled at the provider (D64); keep the first.
			calls = calls[:1]
		}
		if err := addMessage(ctx, tx, r.ID, provider.Message{Role: "assistant", Content: res.Text, ToolCalls: calls, Raw: res.Raw}); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE runs SET step_count = step_count + 1 WHERE id = ?", r.ID); err != nil {
			return err
		}
		if len(calls) == 0 {
			_, err := finishTx(ctx, tx, r.ID, StSucceeded, "", "", res.Text, now)
			return err
		}
		return e.proposeCall(ctx, tx, r, calls[0], now)
	})
	if err == nil && stopped {
		return errStop
	}
	return err
}

func nullInt(v sql.NullInt64) any {
	if v.Valid {
		return v.Int64
	}
	return nil
}

// proposeCall records the model's tool call. Invalid calls go back to the
// model with the error (R3); three in a row fail the run (D80).
func (e *Engine) proposeCall(ctx context.Context, tx *sql.Tx, r *runState, c provider.ToolCall, now time.Time) error {
	ts := db.Format(now)
	reject := func(code, msg string, toolID any, args any) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO tool_calls (run_id, provider_call_id, tool_id, requested_name, raw_arguments,
			arguments, status, reason_code, created_at, updated_at, finished_at) VALUES (?, ?, ?, ?, ?, ?, 'invalid', ?, ?, ?, ?)`,
			r.ID, c.ID, toolID, c.Name, c.Arguments, args, code, ts, ts, ts)
		if err != nil {
			return err
		}
		id, _ := res.LastInsertId()
		text := "Error: " + msg + " Fix the call and try again, or answer without the tool."
		if err := addMessage(ctx, tx, r.ID, provider.Message{Role: "tool", Content: text, ToolCallID: c.ID}); err != nil {
			return err
		}
		if err := emit(ctx, tx, r.ID, EvToolCall, map[string]any{
			"id": id, "name": c.Name, "raw_arguments": c.Arguments, "status": TcInvalid, "reason_code": code, "error": msg}); err != nil {
			return err
		}
		streak := r.Streak + 1
		if _, err := tx.ExecContext(ctx, "UPDATE runs SET invalid_call_streak = ? WHERE id = ?", streak, r.ID); err != nil {
			return err
		}
		if streak >= MaxInvalidStreak {
			_, err := finishTx(ctx, tx, r.ID, StFailed, "invalid_tool_calls",
				"the model made 3 invalid tool calls in a row", "", now)
			return err
		}
		return nil
	}

	t, ok := r.Snapshot.toolByName(c.Name)
	if !ok {
		names := make([]string, 0, len(r.Snapshot.Tools))
		for _, t := range r.Snapshot.Tools {
			names = append(names, t.Name)
		}
		return reject("unknown_tool", fmt.Sprintf("there is no tool named %q. Available tools: %s.", c.Name, strings.Join(names, ", ")), nil, nil)
	}
	args, err := tool.ParseArgs(t.Definition, c.Arguments)
	var ae *tool.ArgError
	if errors.As(err, &ae) {
		return reject(ae.Code, ae.Msg, t.ID, nil)
	}
	if err != nil {
		return err
	}
	argsJSON, _ := json.Marshal(args)
	req, err := tool.Render(t.Definition, args)
	if errors.As(err, &ae) {
		return reject(ae.Code, ae.Msg, t.ID, string(argsJSON))
	}
	if err != nil {
		return err
	}
	reqJSON, _ := json.Marshal(req)

	status := TcApproved // a tool without approval goes through the same claim (D99)
	var expires any
	if t.RequiresApproval {
		status = TcPending
		expires = db.Format(ApprovalExpiry(now))
	}
	if _, err := tx.ExecContext(ctx, "UPDATE runs SET invalid_call_streak = 0 WHERE id = ?", r.ID); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO tool_calls (run_id, provider_call_id, tool_id, requested_name, raw_arguments,
		arguments, status, approval_expires_at, request_preview, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, c.ID, t.ID, c.Name, c.Arguments, string(argsJSON), status, expires, string(reqJSON), ts, ts)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	return emit(ctx, tx, r.ID, EvToolCall, map[string]any{
		"id": id, "name": c.Name, "arguments": args, "status": status, "requires_approval": t.RequiresApproval,
		"approval_revision": 1, "approval_expires_at": expires, "request": req.Preview()})
}

// ApprovalExpiry is 24 hours after the request, moved to Monday at the same
// time if it falls on a weekend, in server local time (D90).
func ApprovalExpiry(now time.Time) time.Time {
	t := now.Local().Add(24 * time.Hour)
	switch t.Weekday() {
	case time.Saturday:
		t = t.AddDate(0, 0, 2)
	case time.Sunday:
		t = t.AddDate(0, 0, 1)
	}
	return t
}
