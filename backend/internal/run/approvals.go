package run

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/kaang/vodafone-case/backend/internal/db"
	"github.com/kaang/vodafone-case/backend/internal/provider"
)

// runAccess loads a run of the caller's team. Another team's run is
// indistinguishable from a missing one (D39).
func runAccess(ctx context.Context, tx *sql.Tx, runID, teamID int64) (status string, startedBy int64, err error) {
	err = tx.QueryRowContext(ctx, "SELECT status, started_by FROM runs WHERE id = ? AND team_id = ?", runID, teamID).
		Scan(&status, &startedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, ErrNotFound
	}
	return status, startedBy, err
}

// callState returns a call of the run for a 409 body. A call that is not in
// the run is not found, like another team's run (D39).
func callState(ctx context.Context, tx *sql.Tx, runID, callID int64) (map[string]any, error) {
	var status string
	var rev int
	var reason sql.NullString
	err := tx.QueryRowContext(ctx, "SELECT status, approval_revision, reason_code FROM tool_calls WHERE id = ? AND run_id = ?",
		callID, runID).Scan(&status, &rev, &reason)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"status": status, "approval_revision": rev, "reason_code": reason.String}, nil
}

// errExpired is returned to the starter who decides on a request that has
// already expired; the decision's transaction cancelled the run (D90).
var errExpired = &ConflictError{"the approval request expired; the run was cancelled",
	map[string]string{"status": TcRejected, "reason_code": "approval_expired"}}

// expireTx cancels the run if one of its open calls waited past its approval
// expiry (D90). It runs inside every transaction that would act on such a
// call, so an expired request is never approved, rejected or sent, whether or
// not the sweep has run yet.
func expireTx(ctx context.Context, tx *sql.Tx, runID int64, now time.Time) (bool, error) {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM tool_calls WHERE run_id = ?
		AND status IN ('pending_approval', 'approved', 'invalidated') AND approval_expires_at IS NOT NULL
		AND approval_expires_at <= ?`, runID, db.Format(now)).Scan(&n); err != nil || n == 0 {
		return false, err
	}
	return finishTx(ctx, tx, runID, StCancelled, "approval_expired", "the approval request expired without a decision", "", now)
}

// Approve approves a pending call. Only the run's starter may decide (D26), and
// only for the revision they saw (D55); the conditional update makes exactly
// one of competing decisions win (D21).
func (e *Engine) Approve(ctx context.Context, userID, teamID, runID, callID int64, revision int) error {
	expired := false
	err := e.withTx(ctx, runID, func(tx *sql.Tx) error {
		_, startedBy, err := runAccess(ctx, tx, runID, teamID)
		if err != nil {
			return err
		}
		if startedBy != userID {
			return ErrForbidden
		}
		state, err := callState(ctx, tx, runID, callID)
		if err != nil {
			return err
		}
		if expired, err = expireTx(ctx, tx, runID, e.Now()); err != nil || expired {
			return err
		}
		now := db.Format(e.Now())
		res, err := tx.ExecContext(ctx, `UPDATE tool_calls SET status = 'approved', decided_by = ?, decided_at = ?, updated_at = ?
			WHERE id = ? AND run_id = ? AND status = 'pending_approval' AND approval_revision = ?
			AND EXISTS (SELECT 1 FROM runs WHERE id = ? AND status = 'waiting_approval' AND started_by = ?)`,
			userID, now, now, callID, runID, revision, runID, userID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return &ConflictError{"this approval is no longer pending (or the page is out of date)", state}
		}
		if _, err := tx.ExecContext(ctx, "UPDATE runs SET status = 'running' WHERE id = ?", runID); err != nil {
			return err
		}
		if err := emit(ctx, tx, runID, EvToolCallStatus, map[string]any{"id": callID, "status": TcApproved}); err != nil {
			return err
		}
		return emit(ctx, tx, runID, EvRunStatus, map[string]string{"status": StRunning})
	})
	if err == nil && expired {
		e.cancelActive(runID)
		return errExpired
	}
	if err == nil {
		e.Enqueue(runID)
	}
	return err
}

// Reject rejects a pending call. The model receives the rejection and the
// starter's optional note, and may try something else or finish (D89).
func (e *Engine) Reject(ctx context.Context, userID, teamID, runID, callID int64, revision int, note string) error {
	note = strings.TrimSpace(note)
	if len(note) > 2000 {
		note = note[:2000]
	}
	expired := false
	err := e.withTx(ctx, runID, func(tx *sql.Tx) error {
		_, startedBy, err := runAccess(ctx, tx, runID, teamID)
		if err != nil {
			return err
		}
		if startedBy != userID {
			return ErrForbidden
		}
		state, err := callState(ctx, tx, runID, callID)
		if err != nil {
			return err
		}
		if expired, err = expireTx(ctx, tx, runID, e.Now()); err != nil || expired {
			return err
		}
		now := db.Format(e.Now())
		res, err := tx.ExecContext(ctx, `UPDATE tool_calls SET status = 'rejected', reason_code = 'rejected_by_starter',
			rejection_note = NULLIF(?, ''), decided_by = ?, decided_at = ?, finished_at = ?, updated_at = ?
			WHERE id = ? AND run_id = ? AND status = 'pending_approval' AND approval_revision = ?
			AND EXISTS (SELECT 1 FROM runs WHERE id = ? AND status = 'waiting_approval' AND started_by = ?)`,
			note, userID, now, now, now, callID, runID, revision, runID, userID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return &ConflictError{"this approval is no longer pending (or the page is out of date)", state}
		}
		var providerCallID string
		if err := tx.QueryRowContext(ctx, "SELECT provider_call_id FROM tool_calls WHERE id = ?", callID).Scan(&providerCallID); err != nil {
			return err
		}
		text := "The user rejected this tool call; it was not executed."
		if note != "" {
			text += " The user's note: " + note
		}
		if err := addMessage(ctx, tx, runID, provider.Message{Role: "tool", Content: text, ToolCallID: providerCallID}); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE runs SET status = 'running' WHERE id = ?", runID); err != nil {
			return err
		}
		if err := emit(ctx, tx, runID, EvToolCallStatus, map[string]any{
			"id": callID, "status": TcRejected, "reason_code": "rejected_by_starter", "note": note}); err != nil {
			return err
		}
		return emit(ctx, tx, runID, EvRunStatus, map[string]string{"status": StRunning})
	})
	if err == nil && expired {
		e.cancelActive(runID)
		return errExpired
	}
	if err == nil {
		e.Enqueue(runID)
	}
	return err
}

// Cancel cancels a non-terminal run. The starter and the team's leads may
// cancel (D66, D97). The in-flight model call or HTTP request is stopped.
func (e *Engine) Cancel(ctx context.Context, userID, teamID int64, isLead bool, runID int64) error {
	err := e.withTx(ctx, runID, func(tx *sql.Tx) error {
		status, startedBy, err := runAccess(ctx, tx, runID, teamID)
		if err != nil {
			return err
		}
		if startedBy != userID && !isLead {
			return ErrForbidden
		}
		if terminal(status) {
			return &ConflictError{"the run has already finished", map[string]string{"status": status}}
		}
		if _, err := tx.ExecContext(ctx, "UPDATE runs SET cancelled_by = ? WHERE id = ?", userID, runID); err != nil {
			return err
		}
		ok, err := finishTx(ctx, tx, runID, StCancelled, "cancelled", "the run was cancelled", "", e.Now())
		if err != nil {
			return err
		}
		if !ok {
			return &ConflictError{"the run has already finished", nil}
		}
		return nil
	})
	if err == nil {
		e.cancelActive(runID)
	}
	return err
}

// recover runs once at start-up, before the workers (D62, D84).
func (e *Engine) recover(ctx context.Context) error {
	now := db.Format(e.Now())
	// An attempt that streamed text but never completed, whether the server
	// crashed or stopped, is marked discarded in the stream before the run
	// resumes, so its partial text is not shown as the answer (D62, D92).
	if err := e.withTx(ctx, 0, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT mc.id, mc.run_id, mc.attempt FROM model_calls mc JOIN runs r ON r.id = mc.run_id
			WHERE r.status NOT IN ('succeeded', 'failed', 'cancelled')
			AND (mc.finished_at IS NULL OR mc.outcome = 'failed' AND mc.error_code = 'interrupted')
			AND EXISTS (SELECT 1 FROM run_events ev WHERE ev.run_id = mc.run_id AND ev.type = 'text_delta'
				AND json_extract(ev.payload, '$.model_call_id') = mc.id)
			AND NOT EXISTS (SELECT 1 FROM run_events ev WHERE ev.run_id = mc.run_id AND ev.type = 'attempt_discarded'
				AND json_extract(ev.payload, '$.model_call_id') = mc.id)
			ORDER BY mc.id`)
		if err != nil {
			return err
		}
		type attempt struct{ id, runID, n int64 }
		var found []attempt
		for rows.Next() {
			var a attempt
			if err := rows.Scan(&a.id, &a.runID, &a.n); err != nil {
				rows.Close()
				return err
			}
			found = append(found, a)
		}
		rows.Close()
		for _, a := range found {
			if err := emit(ctx, tx, a.runID, EvAttemptDiscarded, map[string]any{"model_call_id": a.id, "attempt": a.n}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	// Model calls left in flight: usage reported before the crash is kept (D75).
	if _, err := e.DB.W.ExecContext(ctx, `UPDATE model_calls SET outcome = 'interrupted', finished_at = ?,
		usage_status = CASE WHEN input_tokens IS NULL THEN 'unavailable' ELSE 'partial' END
		WHERE finished_at IS NULL`, now); err != nil {
		return err
	}

	// Tool calls left executing: a GET runs again; anything else may have had
	// its effect and becomes outcome_unknown, never retried (D63).
	rows, err := e.DB.W.QueryContext(ctx, `SELECT run_id, `+callCols+` FROM tool_calls WHERE status = 'executing'`)
	if err != nil {
		return err
	}
	type pending struct {
		runID int64
		c     *callRow
	}
	var stuck []pending
	for rows.Next() {
		var runID int64
		var c callRow
		var req string
		if err := rows.Scan(&runID, &c.ID, &c.ProviderCallID, &c.Name, &c.Status, &c.Revision, &req); err != nil {
			rows.Close()
			return err
		}
		json.Unmarshal([]byte(req), &c.Request)
		stuck = append(stuck, pending{runID, &c})
	}
	rows.Close()
	for _, p := range stuck {
		if p.c.Request.Method == "GET" {
			if err := e.releaseCall(p.runID, p.c); err != nil {
				return err
			}
			continue
		}
		if err := e.completeCall(p.runID, p.c, TcOutcomeUnknown, "interrupted",
			"The outcome of this call is unknown: the server stopped while it was being sent. It was not retried. "+
				"Tell the user; do not assume it succeeded.", 0); err != nil {
			return err
		}
	}

	e.expireApprovals(ctx)

	ids, err := e.DB.W.QueryContext(ctx, "SELECT id FROM runs WHERE status IN ('queued', 'running') ORDER BY id")
	if err != nil {
		return err
	}
	var resume []int64
	for ids.Next() {
		var id int64
		ids.Scan(&id)
		resume = append(resume, id)
	}
	ids.Close()
	for _, id := range resume {
		e.Enqueue(id)
	}
	return nil
}

func (e *Engine) sweeper() {
	defer e.wg.Done()
	t := time.NewTicker(ExpirySweep)
	defer t.Stop()
	for {
		select {
		case <-e.base.Done():
			return
		case <-t.C:
			e.expireApprovals(e.base)
		}
	}
}

// expireApprovals cancels runs whose approval waited past its expiry (D90).
func (e *Engine) expireApprovals(ctx context.Context) {
	rows, err := e.DB.W.QueryContext(ctx, `SELECT DISTINCT tc.run_id FROM tool_calls tc JOIN runs r ON r.id = tc.run_id
		WHERE tc.status IN ('pending_approval', 'approved', 'invalidated') AND tc.approval_expires_at IS NOT NULL
		AND tc.approval_expires_at <= ? AND r.status NOT IN ('succeeded', 'failed', 'cancelled')`, db.Format(e.Now()))
	if err != nil {
		log.Printf("expiry sweep: %v", err)
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		// The candidate is re-checked inside the cancelling transaction: it
		// may have been decided or claimed since the query.
		expired := false
		err := e.withTx(ctx, id, func(tx *sql.Tx) error {
			var err error
			expired, err = expireTx(ctx, tx, id, e.Now())
			return err
		})
		if err != nil {
			log.Printf("expiry sweep: run %d: %v", id, err)
		}
		if expired {
			e.cancelActive(id)
		}
	}
}
