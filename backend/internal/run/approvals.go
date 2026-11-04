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

func callState(ctx context.Context, tx *sql.Tx, callID int64) map[string]any {
	var status string
	var rev int
	var reason sql.NullString
	tx.QueryRowContext(ctx, "SELECT status, approval_revision, reason_code FROM tool_calls WHERE id = ?", callID).Scan(&status, &rev, &reason)
	return map[string]any{"status": status, "approval_revision": rev, "reason_code": reason.String}
}

// Approve approves a pending call. Only the run's starter may decide (D26), and
// only for the revision they saw (D55); the conditional update makes exactly
// one of competing decisions win (D21).
func (e *Engine) Approve(ctx context.Context, userID, teamID, runID, callID int64, revision int) error {
	err := e.withTx(ctx, runID, func(tx *sql.Tx) error {
		_, startedBy, err := runAccess(ctx, tx, runID, teamID)
		if err != nil {
			return err
		}
		if startedBy != userID {
			return ErrForbidden
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
			return &ConflictError{"this approval is no longer pending (or the page is out of date)", callState(ctx, tx, callID)}
		}
		if _, err := tx.ExecContext(ctx, "UPDATE runs SET status = 'running' WHERE id = ?", runID); err != nil {
			return err
		}
		if err := emit(ctx, tx, runID, EvToolCallStatus, map[string]any{"id": callID, "status": TcApproved}); err != nil {
			return err
		}
		return emit(ctx, tx, runID, EvRunStatus, map[string]string{"status": StRunning})
	})
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
	err := e.withTx(ctx, runID, func(tx *sql.Tx) error {
		_, startedBy, err := runAccess(ctx, tx, runID, teamID)
		if err != nil {
			return err
		}
		if startedBy != userID {
			return ErrForbidden
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
			return &ConflictError{"this approval is no longer pending (or the page is out of date)", callState(ctx, tx, callID)}
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
		if e.finish(ctx, id, StCancelled, "approval_expired", "the approval request expired without a decision", "") {
			e.cancelActive(id)
		}
	}
}
