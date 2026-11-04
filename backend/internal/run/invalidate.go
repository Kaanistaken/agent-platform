package run

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"

	"github.com/kaang/vodafone-case/backend/internal/db"
)

// These functions run inside the transaction of the API request that changes a
// credential, so the credential and the affected approvals change together
// (D29, D55, D77, D86, D87, D94). Changed run IDs are returned so the caller
// can wake their streams after commit.

type activeRun struct {
	id   int64
	snap Snapshot
}

func startersActiveRuns(ctx context.Context, tx *sql.Tx, userID int64) ([]activeRun, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, snapshot FROM runs WHERE started_by = ?
		AND status NOT IN ('succeeded', 'failed', 'cancelled')`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []activeRun
	for rows.Next() {
		var r activeRun
		var s string
		if err := rows.Scan(&r.id, &s); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(s), &r.snap); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func runCalls(ctx context.Context, tx *sql.Tx, runID int64, statuses ...string) ([]*callRow, []string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT `+callCols+`, COALESCE(reason_code, '') FROM tool_calls WHERE run_id = ?`, runID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var calls []*callRow
	var reasons []string
	for rows.Next() {
		var c callRow
		var req, reason string
		if err := rows.Scan(&c.ID, &c.ProviderCallID, &c.Name, &c.Status, &c.Revision, &req, &reason); err != nil {
			return nil, nil, err
		}
		if !slices.Contains(statuses, c.Status) {
			continue
		}
		json.Unmarshal([]byte(req), &c.Request)
		calls = append(calls, &c)
		reasons = append(reasons, reason)
	}
	return calls, reasons, rows.Err()
}

func setCall(ctx context.Context, tx *sql.Tx, runID int64, c *callRow, status, reason string, bump bool) error {
	rev := c.Revision
	if bump {
		rev++
	}
	if _, err := tx.ExecContext(ctx, `UPDATE tool_calls SET status = ?, reason_code = NULLIF(?, ''), approval_revision = ?,
		updated_at = ? WHERE id = ?`, status, reason, rev, db.Now(), c.ID); err != nil {
		return err
	}
	return emit(ctx, tx, runID, EvToolCallStatus, map[string]any{
		"id": c.ID, "status": status, "reason_code": reason, "approval_revision": rev})
}

// prereqsOK reports whether the starter has the model key and every secret
// the call needs (D94: a call returns to approval only when all are restored).
func prereqsOK(ctx context.Context, tx *sql.Tx, userID int64, r activeRun, c *callRow) (bool, error) {
	var n int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM user_model_keys WHERE user_id = ? AND provider = ?",
		userID, r.snap.Agent.Provider).Scan(&n); err != nil || n == 0 {
		return false, err
	}
	for _, name := range c.Request.SecretNames() {
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM user_secrets WHERE id = ? AND user_id = ?",
			r.snap.Secrets[name], userID).Scan(&n); err != nil || n == 0 {
			return false, err
		}
	}
	return true, nil
}

// OnModelKeyChanged re-opens (replaced key) or invalidates (deleted key) the
// starter's pending and approved calls in runs on that provider (D77).
func OnModelKeyChanged(ctx context.Context, tx *sql.Tx, userID int64, prov string, deleted bool) ([]int64, error) {
	runs, err := startersActiveRuns(ctx, tx, userID)
	if err != nil {
		return nil, err
	}
	var changed []int64
	for _, r := range runs {
		if r.snap.Agent.Provider != prov {
			continue
		}
		calls, reasons, err := runCalls(ctx, tx, r.id, TcPending, TcApproved, TcInvalidated)
		if err != nil {
			return nil, err
		}
		for i, c := range calls {
			switch {
			case deleted && c.Status != TcInvalidated:
				err = setCall(ctx, tx, r.id, c, TcInvalidated, "model_key_deleted", false)
			case !deleted && c.Status != TcInvalidated:
				err = setCall(ctx, tx, r.id, c, TcPending, "model_key_changed", true)
			case !deleted && reasons[i] == "model_key_deleted":
				ok, perr := prereqsOK(ctx, tx, userID, r, c)
				if perr != nil {
					return nil, perr
				}
				if ok {
					err = setCall(ctx, tx, r.id, c, TcPending, "model_key_changed", true)
				}
			default:
				continue
			}
			if err != nil {
				return nil, err
			}
			changed = append(changed, r.id)
		}
	}
	return changed, nil
}

// OnSecretDeleted invalidates pending and approved calls that need the secret
// (D29, D86).
func OnSecretDeleted(ctx context.Context, tx *sql.Tx, userID, secretID int64) ([]int64, error) {
	runs, err := startersActiveRuns(ctx, tx, userID)
	if err != nil {
		return nil, err
	}
	var changed []int64
	for _, r := range runs {
		calls, _, err := runCalls(ctx, tx, r.id, TcPending, TcApproved)
		if err != nil {
			return nil, err
		}
		for _, c := range calls {
			uses := false
			for _, name := range c.Request.SecretNames() {
				if r.snap.Secrets[name] == secretID {
					uses = true
				}
			}
			if !uses {
				continue
			}
			if err := setCall(ctx, tx, r.id, c, TcInvalidated, "secret_deleted", false); err != nil {
				return nil, err
			}
			changed = append(changed, r.id)
		}
	}
	return changed, nil
}

// OnSecretAdded binds a new secret in place of a deleted one with the same
// name (D76, D87, D99): the snapshot binding is replaced, and an invalidated
// call whose host matches returns to approval with a new revision.
func OnSecretAdded(ctx context.Context, tx *sql.Tx, userID, secretID int64, name, host string) ([]int64, error) {
	runs, err := startersActiveRuns(ctx, tx, userID)
	if err != nil {
		return nil, err
	}
	var changed []int64
	for _, r := range runs {
		oldID, ok := r.snap.Secrets[name]
		if !ok {
			continue
		}
		var exists int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM user_secrets WHERE id = ? AND user_id = ?", oldID, userID).
			Scan(&exists); err != nil {
			return nil, err
		}
		if exists > 0 {
			continue // the run's secret still exists; nothing to replace
		}
		r.snap.Secrets[name] = secretID
		snapJSON, _ := json.Marshal(r.snap)
		if _, err := tx.ExecContext(ctx, "UPDATE runs SET snapshot = ? WHERE id = ?", string(snapJSON), r.id); err != nil {
			return nil, err
		}
		calls, reasons, err := runCalls(ctx, tx, r.id, TcInvalidated)
		if err != nil {
			return nil, err
		}
		for i, c := range calls {
			if reasons[i] != "secret_deleted" || c.Request.Host != host || !slices.Contains(c.Request.SecretNames(), name) {
				continue
			}
			ok, err := prereqsOK(ctx, tx, userID, r, c)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			if err := setCall(ctx, tx, r.id, c, TcPending, "", true); err != nil {
				return nil, err
			}
			changed = append(changed, r.id)
		}
	}
	return changed, nil
}

// Notify wakes the streams of the given runs (after the caller's commit).
func (e *Engine) Notify(runIDs []int64) {
	for _, id := range runIDs {
		e.Hub.Notify(id)
	}
}
