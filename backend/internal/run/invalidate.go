package run

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"time"

	"github.com/kaang/vodafone-case/backend/internal/db"
	"github.com/kaang/vodafone-case/backend/internal/tool"
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
	// A call that starts waiting for the starter gets an expiry once; an
	// existing one is never moved (D90).
	waiting := status == TcPending || status == TcInvalidated
	if _, err := tx.ExecContext(ctx, `UPDATE tool_calls SET status = ?, reason_code = NULLIF(?, ''), approval_revision = ?,
		approval_expires_at = CASE WHEN ? THEN COALESCE(approval_expires_at, ?) ELSE approval_expires_at END,
		updated_at = ? WHERE id = ?`, status, reason, rev, waiting, db.Format(ApprovalExpiry(time.Now())), db.Now(), c.ID); err != nil {
		return err
	}
	c.Status, c.Revision = status, rev
	return emit(ctx, tx, runID, EvToolCallStatus, map[string]any{
		"id": c.ID, "status": status, "reason_code": reason, "approval_revision": rev,
		"approval_expires_at": expiresAt(ctx, tx, c.ID)})
}

// missingPrereq returns what the call still lacks: model_key_deleted if the
// starter has no key for the run's provider, secret_deleted if a bound secret
// is gone or bound to another host than the call's (D87), "" if nothing is
// missing (D94).
func missingPrereq(ctx context.Context, tx *sql.Tx, userID int64, r activeRun, c *callRow) (string, error) {
	var n int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM user_model_keys WHERE user_id = ? AND provider = ?",
		userID, r.snap.Agent.Provider).Scan(&n); err != nil {
		return "", err
	}
	if n == 0 {
		return "model_key_deleted", nil
	}
	for _, name := range c.Request.SecretNames() {
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM user_secrets WHERE id = ? AND user_id = ? AND allowed_host = ?",
			r.snap.Secrets[name], userID, c.Request.Host).Scan(&n); err != nil {
			return "", err
		}
		if n == 0 {
			return "secret_deleted", nil
		}
	}
	return "", nil
}

// reevaluate re-checks an invalidated call after a credential was restored,
// whatever its stored reason: with nothing missing it returns to approval with
// a new revision; otherwise it keeps waiting with the reason that is still
// true (D94). It reports whether the call changed.
func reevaluate(ctx context.Context, tx *sql.Tx, userID int64, r activeRun, c *callRow, reason, reopenReason string) (bool, error) {
	missing, err := missingPrereq(ctx, tx, userID, r, c)
	if err != nil {
		return false, err
	}
	switch {
	case missing == "":
		return true, setCall(ctx, tx, r.id, c, TcPending, reopenReason, true)
	case missing != reason:
		return true, setCall(ctx, tx, r.id, c, TcInvalidated, missing, false)
	}
	return false, nil
}

// OnModelKeyChanged re-opens (replaced key) or invalidates (deleted key) the
// starter's pending and approved calls in runs on that provider (D77). A
// restored key re-checks every invalidated call (D94).
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
			did := true
			switch {
			case c.Status != TcInvalidated && deleted:
				err = setCall(ctx, tx, r.id, c, TcInvalidated, "model_key_deleted", false)
			case c.Status != TcInvalidated:
				err = setCall(ctx, tx, r.id, c, TcPending, "model_key_changed", true)
			case deleted:
				continue // already waiting for something else
			default:
				did, err = reevaluate(ctx, tx, userID, r, c, reasons[i], "model_key_changed")
			}
			if err != nil {
				return nil, err
			}
			if did {
				changed = append(changed, r.id)
			}
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
// name and host (D76, D87, D99), then re-checks every invalidated call of the
// run: one with nothing missing returns to approval with a new revision (D94).
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
		calls, reasons, err := runCalls(ctx, tx, r.id, TcInvalidated)
		if err != nil {
			return nil, err
		}
		if !replacementHostOK(r.snap, calls, name, host) {
			continue
		}
		r.snap.Secrets[name] = secretID
		snapJSON, _ := json.Marshal(r.snap)
		if _, err := tx.ExecContext(ctx, "UPDATE runs SET snapshot = ? WHERE id = ?", string(snapJSON), r.id); err != nil {
			return nil, err
		}
		for i, c := range calls {
			did, err := reevaluate(ctx, tx, userID, r, c, reasons[i], "")
			if err != nil {
				return nil, err
			}
			if did {
				changed = append(changed, r.id)
			}
		}
	}
	return changed, nil
}

// replacementHostOK decides whether a new secret on host may take the place of
// the run's deleted secret name (D87). If invalidated calls wait for the name,
// one of them must target host; otherwise one of the run's tools that uses the
// name must.
func replacementHostOK(snap Snapshot, waiting []*callRow, name, host string) bool {
	needed := false
	for _, c := range waiting {
		if slices.Contains(c.Request.SecretNames(), name) {
			needed = true
			if c.Request.Host == host {
				return true
			}
		}
	}
	if needed {
		return false
	}
	for _, t := range snap.Tools {
		if slices.Contains(tool.SecretRefs(t.Definition), name) && tool.Host(t.Definition) == host {
			return true
		}
	}
	return false
}

// Notify wakes the streams of the given runs (after the caller's commit).
func (e *Engine) Notify(runIDs []int64) {
	for _, id := range runIDs {
		e.Hub.Notify(id)
	}
}
