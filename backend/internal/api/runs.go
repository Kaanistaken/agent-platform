package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/kaang/vodafone-case/backend/internal/auth"
	"github.com/kaang/vodafone-case/backend/internal/run"
	"github.com/kaang/vodafone-case/backend/internal/tool"
)

func (s *Server) createRun(w http.ResponseWriter, r *http.Request, u auth.User) error {
	agentID, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var body struct {
		Task                  string  `json:"task"`
		ConfirmMissingToolIDs []int64 `json:"confirm_missing_tool_ids"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	if len(body.Task) > 20000 {
		return badRequest("the task is too long")
	}
	id, err := s.Engine.CreateRun(r.Context(), u.ID, u.TeamID, agentID, body.Task, body.ConfirmMissingToolIDs)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
	return nil
}

type runSummary struct {
	ID         int64    `json:"id"`
	Agent      toolRef  `json:"agent"`
	StartedBy  ownerRef `json:"started_by"`
	Task       string   `json:"task"`
	Status     string   `json:"status"`
	ErrorCode  string   `json:"error_code,omitempty"`
	CreatedAt  string   `json:"created_at"`
	FinishedAt string   `json:"finished_at,omitempty"`
}

func (s *Server) listRuns(w http.ResponseWriter, r *http.Request, u auth.User) error {
	// The team's runs, including those of deleted agents (D35).
	rows, err := s.DB.R.QueryContext(r.Context(), `SELECT r.id, a.id, a.name, a.deleted_at IS NOT NULL, u.id, u.username,
		r.task, r.status, COALESCE(r.error_code, ''), r.created_at, COALESCE(r.finished_at, '')
		FROM runs r JOIN agents a ON a.id = r.agent_id JOIN users u ON u.id = r.started_by
		WHERE r.team_id = ? ORDER BY r.id DESC LIMIT 100`, u.TeamID)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []runSummary{}
	for rows.Next() {
		var x runSummary
		if err := rows.Scan(&x.ID, &x.Agent.ID, &x.Agent.Name, &x.Agent.Deleted, &x.StartedBy.ID, &x.StartedBy.Username,
			&x.Task, &x.Status, &x.ErrorCode, &x.CreatedAt, &x.FinishedAt); err != nil {
			return err
		}
		out = append(out, x)
	}
	writeJSON(w, http.StatusOK, out)
	return rows.Err()
}

// canSeeUsage: token counts only for the run's starter and the team lead (D53).
func canSeeUsage(u auth.User, startedBy int64) bool { return u.ID == startedBy || u.IsLead() }

func (s *Server) getRun(w http.ResponseWriter, r *http.Request, u auth.User) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	ctx := r.Context()
	var out struct {
		runSummary
		FinalOutput  string           `json:"final_output"`
		ErrorMessage string           `json:"error_message"`
		StepCount    int              `json:"step_count"`
		Agent        run.SnapAgent    `json:"agent_snapshot"`
		Tools        []map[string]any `json:"tools"`
		Messages     []map[string]any `json:"messages"`
		ToolCalls    []map[string]any `json:"tool_calls"`
		Usage        []map[string]any `json:"usage,omitempty"`
		CanDecide    bool             `json:"can_decide"`
		CanCancel    bool             `json:"can_cancel"`
	}
	var snap string
	err = s.DB.R.QueryRowContext(ctx, `SELECT r.id, a.id, a.name, a.deleted_at IS NOT NULL, u.id, u.username, r.task, r.status,
		COALESCE(r.error_code, ''), r.created_at, COALESCE(r.finished_at, ''), COALESCE(r.final_output, ''),
		COALESCE(r.error_message, ''), r.step_count, r.snapshot
		FROM runs r JOIN agents a ON a.id = r.agent_id JOIN users u ON u.id = r.started_by
		WHERE r.id = ? AND r.team_id = ?`, id, u.TeamID).
		Scan(&out.ID, &out.runSummary.Agent.ID, &out.runSummary.Agent.Name, &out.runSummary.Agent.Deleted, &out.StartedBy.ID,
			&out.StartedBy.Username, &out.Task, &out.Status, &out.ErrorCode, &out.CreatedAt, &out.FinishedAt,
			&out.FinalOutput, &out.ErrorMessage, &out.StepCount, &snap)
	if errors.Is(err, sql.ErrNoRows) {
		return notFound()
	}
	if err != nil {
		return err
	}
	var sn run.Snapshot
	json.Unmarshal([]byte(snap), &sn)
	out.Agent = sn.Agent
	// The snapshot is shown without the secret bindings' details (schema.md).
	out.Tools = []map[string]any{}
	for _, t := range sn.Tools {
		out.Tools = append(out.Tools, map[string]any{"id": t.ID, "name": t.Name, "method": t.Method,
			"url_template": t.URLTemplate, "requires_approval": t.RequiresApproval})
	}
	terminal := out.Status == run.StSucceeded || out.Status == run.StFailed || out.Status == run.StCancelled
	out.CanDecide = u.ID == out.StartedBy.ID && !terminal
	out.CanCancel = (u.ID == out.StartedBy.ID || u.IsLead()) && !terminal

	rows, err := s.DB.R.QueryContext(ctx, `SELECT seq, role, content, COALESCE(tool_calls, ''), COALESCE(tool_call_id, ''), created_at
		FROM run_messages WHERE run_id = ? ORDER BY seq`, id)
	if err != nil {
		return err
	}
	out.Messages = []map[string]any{}
	for rows.Next() {
		var seq int64
		var role, content, calls, callID, at string
		rows.Scan(&seq, &role, &content, &calls, &callID, &at)
		m := map[string]any{"seq": seq, "role": role, "content": content, "created_at": at}
		if calls != "" {
			m["tool_calls"] = json.RawMessage(calls)
		}
		if callID != "" {
			m["tool_call_id"] = callID
		}
		out.Messages = append(out.Messages, m)
	}
	rows.Close()

	calls, err := s.toolCalls(r, "WHERE tc.run_id = ?", id)
	if err != nil {
		return err
	}
	out.ToolCalls = calls

	if canSeeUsage(u, out.StartedBy.ID) {
		rows, err := s.DB.R.QueryContext(ctx, `SELECT id, turn, attempt, provider, model, COALESCE(outcome, 'in_flight'),
			input_tokens, output_tokens, usage_status, COALESCE(error_code, '') FROM model_calls WHERE run_id = ? ORDER BY id`, id)
		if err != nil {
			return err
		}
		out.Usage = []map[string]any{}
		for rows.Next() {
			var mid int64
			var turn, attempt int
			var prov, model, outcome, status, code string
			var in, outT sql.NullInt64
			rows.Scan(&mid, &turn, &attempt, &prov, &model, &outcome, &in, &outT, &status, &code)
			out.Usage = append(out.Usage, map[string]any{"id": mid, "turn": turn, "attempt": attempt, "provider": prov,
				"model": model, "outcome": outcome, "input_tokens": nullable(in), "output_tokens": nullable(outT),
				"usage_status": status, "error_code": code})
		}
		rows.Close()
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func nullable(v sql.NullInt64) any {
	if v.Valid {
		return v.Int64
	}
	return nil
}

// toolCalls lists tool calls with their secret-free request previews (D33).
func (s *Server) toolCalls(r *http.Request, where string, args ...any) ([]map[string]any, error) {
	rows, err := s.DB.R.QueryContext(r.Context(), `SELECT tc.id, tc.run_id, tc.requested_name, tc.raw_arguments, tc.status,
		COALESCE(tc.reason_code, ''), COALESCE(tc.rejection_note, ''), tc.approval_revision, COALESCE(tc.approval_expires_at, ''),
		COALESCE(tc.request_preview, ''), COALESCE(tc.response, ''), tc.created_at, COALESCE(tc.finished_at, '')
		FROM tool_calls tc JOIN runs r ON r.id = tc.run_id `+where+` ORDER BY tc.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, runID int64
		var rev int
		var name, rawArgs, status, reason, note, expires, req, resp, created, finished string
		if err := rows.Scan(&id, &runID, &name, &rawArgs, &status, &reason, &note, &rev, &expires, &req, &resp, &created, &finished); err != nil {
			return nil, err
		}
		c := map[string]any{"id": id, "run_id": runID, "name": name, "raw_arguments": rawArgs, "status": status,
			"reason_code": reason, "rejection_note": note, "approval_revision": rev, "approval_expires_at": expires,
			"response": resp, "created_at": created, "finished_at": finished}
		if req != "" {
			var tr tool.Request
			if json.Unmarshal([]byte(req), &tr) == nil {
				c["request"] = tr.Preview()
			}
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Server) cancelRun(w http.ResponseWriter, r *http.Request, u auth.User) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := s.Engine.Cancel(r.Context(), u.ID, u.TeamID, u.IsLead(), id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) decision(r *http.Request) (runID, callID int64, rev int, note string, err error) {
	if runID, err = pathID(r, "id"); err != nil {
		return
	}
	if callID, err = pathID(r, "callId"); err != nil {
		return
	}
	var body struct {
		ApprovalRevision int    `json:"approval_revision"`
		Note             string `json:"note"`
	}
	if err = readJSON(r, &body); err != nil {
		return
	}
	if body.ApprovalRevision < 1 {
		err = badRequest("approval_revision is required")
		return
	}
	return runID, callID, body.ApprovalRevision, body.Note, nil
}

func (s *Server) approve(w http.ResponseWriter, r *http.Request, u auth.User) error {
	runID, callID, rev, _, err := s.decision(r)
	if err != nil {
		return err
	}
	if err := s.Engine.Approve(r.Context(), u.ID, u.TeamID, runID, callID, rev); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) reject(w http.ResponseWriter, r *http.Request, u auth.User) error {
	runID, callID, rev, note, err := s.decision(r)
	if err != nil {
		return err
	}
	if err := s.Engine.Reject(r.Context(), u.ID, u.TeamID, runID, callID, rev, note); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// approvals lists the caller's own pending and invalidated approvals.
func (s *Server) approvals(w http.ResponseWriter, r *http.Request, u auth.User) error {
	calls, err := s.toolCalls(r, `WHERE r.started_by = ? AND r.team_id = ? AND tc.status IN ('pending_approval', 'invalidated')
		AND r.status NOT IN ('succeeded', 'failed', 'cancelled')`, u.ID, u.TeamID)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, calls)
	return nil
}

// usage: a member sees their own totals; a lead sees every member and the
// team total (D41). Unknown usage is counted, never shown as zero (D75).
func (s *Server) usage(w http.ResponseWriter, r *http.Request, u auth.User) error {
	q := `SELECT u.id, u.username, COUNT(mc.id),
		COALESCE(SUM(CASE WHEN mc.usage_status != 'unavailable' THEN mc.input_tokens END), 0),
		COALESCE(SUM(CASE WHEN mc.usage_status != 'unavailable' THEN mc.output_tokens END), 0),
		COALESCE(SUM(CASE WHEN mc.usage_status = 'unavailable' THEN 1 ELSE 0 END), 0)
		FROM users u LEFT JOIN runs r ON r.started_by = u.id LEFT JOIN model_calls mc ON mc.run_id = r.id
		WHERE u.team_id = ?`
	args := []any{u.TeamID}
	if !u.IsLead() {
		q += " AND u.id = ?"
		args = append(args, u.ID)
	}
	rows, err := s.DB.R.QueryContext(r.Context(), q+" GROUP BY u.id ORDER BY u.username", args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	type line struct {
		UserID       int64  `json:"user_id,omitempty"`
		Username     string `json:"username,omitempty"`
		Attempts     int64  `json:"model_calls"`
		Input        int64  `json:"input_tokens"`
		Output       int64  `json:"output_tokens"`
		UnknownUsage int64  `json:"calls_with_unknown_usage"`
	}
	var users []line
	var total line
	for rows.Next() {
		var l line
		if err := rows.Scan(&l.UserID, &l.Username, &l.Attempts, &l.Input, &l.Output, &l.UnknownUsage); err != nil {
			return err
		}
		users = append(users, l)
		total.Attempts += l.Attempts
		total.Input += l.Input
		total.Output += l.Output
		total.UnknownUsage += l.UnknownUsage
	}
	out := map[string]any{"users": users}
	if u.IsLead() {
		out["team_total"] = total
	}
	writeJSON(w, http.StatusOK, out)
	return rows.Err()
}

// events streams a run's events over SSE (D67). Each event's id is its seq; a
// reconnect with Last-Event-ID replays what was missed, a new page gets
// everything (D94). The session is re-checked at every heartbeat (D71).
func (s *Server) events(w http.ResponseWriter, r *http.Request, u auth.User) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var startedBy int64
	err = s.DB.R.QueryRowContext(r.Context(), "SELECT started_by FROM runs WHERE id = ? AND team_id = ?", id, u.TeamID).Scan(&startedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return notFound()
	}
	if err != nil {
		return err
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		return fmt.Errorf("streaming not supported")
	}
	var last int64
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		last, _ = strconv.ParseInt(v, 10, 64)
	}
	showUsage := canSeeUsage(u, startedBy)
	cookie, _ := r.Cookie(SessionCookie)

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	heartbeat := time.NewTicker(s.Heartbeat)
	defer heartbeat.Stop()
	for {
		wake := s.Engine.Hub.Wait(id) // taken before reading, so nothing is missed
		evs, err := run.Events(r.Context(), s.DB.R, id, last)
		if err != nil {
			return nil
		}
		for _, e := range evs {
			last = e.Seq
			if e.Type == run.EvUsage && !showUsage {
				continue // D53
			}
			fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", e.Seq, e.Type, e.Payload)
			if e.Type == run.EvRunFinished {
				flusher.Flush()
				return nil // the stream closes after the final event
			}
		}
		flusher.Flush()
		select {
		case <-r.Context().Done():
			return nil
		case <-wake:
		case <-heartbeat.C:
			if _, ok := auth.Authenticate(r.Context(), s.DB.R, cookie.Value); !ok {
				return nil // logged out or expired: stop sending
			}
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}
