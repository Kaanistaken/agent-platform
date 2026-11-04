package run

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/kaang/vodafone-case/backend/internal/crypto"
	"github.com/kaang/vodafone-case/backend/internal/db"
	"github.com/kaang/vodafone-case/backend/internal/provider"
	"github.com/kaang/vodafone-case/backend/internal/tool"
)

// callRow is the part of a tool_calls row the loop needs.
type callRow struct {
	ID             int64
	ProviderCallID string
	Name           string
	Status         string
	Revision       int
	Request        tool.Request
}

const callCols = "id, provider_call_id, requested_name, status, approval_revision, COALESCE(request_preview, '{}')"

func scanCall(row interface{ Scan(...any) error }) (*callRow, error) {
	var c callRow
	var req string
	if err := row.Scan(&c.ID, &c.ProviderCallID, &c.Name, &c.Status, &c.Revision, &req); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(req), &c.Request); err != nil {
		return nil, err
	}
	return &c, nil
}

// openCall returns the run's call that still needs work, if any. At most one
// exists because the model makes one call per turn (D64).
func (e *Engine) openCall(ctx context.Context, runID int64) (*callRow, error) {
	c, err := scanCall(e.DB.W.QueryRowContext(ctx, `SELECT `+callCols+` FROM tool_calls
		WHERE run_id = ? AND status IN ('pending_approval', 'approved', 'invalidated') ORDER BY id DESC LIMIT 1`, runID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return c, err
}

// executeCall claims an approved call, sends it and stores the result together
// with the tool message for the model (D86, D94).
func (e *Engine) executeCall(ctx context.Context, r *runState, c *callRow) error {
	// Re-check prerequisites at the moment of the claim (D86): the starter's
	// model key and every secret the request needs.
	secrets := map[string]tool.Secret{}
	var values []string
	claimed := false
	err := e.withTx(context.Background(), r.ID, func(tx *sql.Tx) error {
		now := db.Format(e.Now())
		var keyCount int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM user_model_keys WHERE user_id = ? AND provider = ?",
			r.StartedBy, r.Snapshot.Agent.Provider).Scan(&keyCount); err != nil {
			return err
		}
		reason := ""
		if keyCount == 0 {
			reason = "model_key_deleted"
		}
		for _, name := range c.Request.SecretNames() {
			s, ok, err := e.loadSecret(ctx, tx, r.StartedBy, r.Snapshot.Secrets[name])
			if err != nil {
				return err
			}
			if !ok {
				reason = "secret_deleted"
				break
			}
			secrets[name] = s
			values = append(values, s.Value)
		}
		if reason != "" {
			res, err := tx.ExecContext(ctx, `UPDATE tool_calls SET status = 'invalidated', reason_code = ?, updated_at = ?
				WHERE id = ? AND status = 'approved'`, reason, now, c.ID)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 1 {
				return emit(ctx, tx, r.ID, EvToolCallStatus, map[string]any{"id": c.ID, "status": TcInvalidated, "reason_code": reason})
			}
			return nil
		}
		res, err := tx.ExecContext(ctx, `UPDATE tool_calls SET status = 'executing', started_at = ?, updated_at = ?,
			attempt_count = attempt_count + 1 WHERE id = ? AND status = 'approved'`, now, now, c.ID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return nil // changed meanwhile (cancelled, invalidated)
		}
		claimed = true
		return emit(ctx, tx, r.ID, EvToolCallStatus, map[string]any{"id": c.ID, "status": TcExecuting})
	})
	if err != nil || !claimed {
		return err
	}

	res, err := e.Tools.Do(ctx, c.Request, secrets)
	if err == nil {
		return e.completeCall(r.ID, c, TcSucceeded, "", res.Text, res.Status)
	}

	var f *tool.Failure
	if !errors.As(err, &f) {
		f = &tool.Failure{Code: "interrupted", Msg: tool.Mask(err.Error(), values), MaySent: true}
	}
	if ctx.Err() != nil && !f.MaySent || ctx.Err() != nil && c.Request.Method == http.MethodGet {
		// Stopped before anything was sent, or a GET: safe to run again after
		// a restart. If the run was cancelled, the call is closed instead.
		return e.releaseCall(r.ID, c)
	}
	if f.MaySent && c.Request.Method != http.MethodGet {
		// Never retried automatically: it may have had its effect (D63).
		return e.completeCall(r.ID, c, TcOutcomeUnknown, reasonFor(f.Code),
			"The outcome of this call is unknown: it may or may not have been executed ("+f.Msg+"). It was not retried. "+
				"Tell the user; do not assume it succeeded.", 0)
	}
	return e.completeCall(r.ID, c, TcFailed, reasonFor(f.Code), "Error: the request was not completed: "+f.Msg, 0)
}

func reasonFor(code string) string {
	switch code {
	case "secret_missing", "host_mismatch", "address_refused", "timeout":
		return code
	}
	return "interrupted"
}

// completeCall stores the final state of a call. While the run is still
// active, the result also becomes the tool message for the model, in the same
// transaction (D94).
func (e *Engine) completeCall(runID int64, c *callRow, status, reason, text string, httpStatus int) error {
	ctx := context.Background()
	return e.withTx(ctx, runID, func(tx *sql.Tx) error {
		now := db.Format(e.Now())
		if _, err := tx.ExecContext(ctx, `UPDATE tool_calls SET status = ?, reason_code = NULLIF(?, ''), response = ?,
			finished_at = ?, updated_at = ? WHERE id = ? AND status = 'executing'`, status, reason, text, now, now, c.ID); err != nil {
			return err
		}
		var runStatus string
		if err := tx.QueryRowContext(ctx, "SELECT status FROM runs WHERE id = ?", runID).Scan(&runStatus); err != nil {
			return err
		}
		if terminal(runStatus) {
			return nil // nothing is added after the final event
		}
		if err := addMessage(ctx, tx, runID, provider.Message{Role: "tool", Content: text, ToolCallID: c.ProviderCallID}); err != nil {
			return err
		}
		return emit(ctx, tx, runID, EvToolResult, map[string]any{
			"id": c.ID, "status": status, "reason_code": reason, "http_status": httpStatus, "text": text})
	})
}

// releaseCall handles a call stopped before it could have had an effect: on
// shutdown it returns to approved and runs after the restart; in a cancelled
// run it is closed as failed.
func (e *Engine) releaseCall(runID int64, c *callRow) error {
	ctx := context.Background()
	return e.withTx(ctx, runID, func(tx *sql.Tx) error {
		var runStatus string
		if err := tx.QueryRowContext(ctx, "SELECT status FROM runs WHERE id = ?", runID).Scan(&runStatus); err != nil {
			return err
		}
		status := TcApproved
		if terminal(runStatus) {
			status = TcFailed
		}
		_, err := tx.ExecContext(ctx, `UPDATE tool_calls SET status = ?, reason_code = CASE WHEN ? = 'failed' THEN 'interrupted' END,
			updated_at = ? WHERE id = ? AND status = 'executing'`, status, status, db.Now(), c.ID)
		return err
	})
}

// loadSecret decrypts one of the starter's secrets by ID. ok is false if it
// no longer exists or belongs to someone else.
func (e *Engine) loadSecret(ctx context.Context, tx *sql.Tx, userID, secretID int64) (tool.Secret, bool, error) {
	var host string
	var ct, nonce []byte
	err := tx.QueryRowContext(ctx, "SELECT allowed_host, ciphertext, nonce FROM user_secrets WHERE id = ? AND user_id = ?",
		secretID, userID).Scan(&host, &ct, &nonce)
	if errors.Is(err, sql.ErrNoRows) {
		return tool.Secret{}, false, nil
	}
	if err != nil {
		return tool.Secret{}, false, err
	}
	pt, err := e.Box.Open(ct, nonce, crypto.SecretAAD(secretID, userID))
	if err != nil {
		return tool.Secret{}, false, fmt.Errorf("decrypt secret %d: %w", secretID, err)
	}
	return tool.Secret{Value: string(pt), Host: host}, true, nil
}
