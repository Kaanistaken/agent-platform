package run

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/kaang/vodafone-case/backend/internal/db"
	"github.com/kaang/vodafone-case/backend/internal/tool"
)

// Snapshot is the agent and tool definitions at run start (D25, D42), plus the
// starter's secret ID for every secret name the tools reference (D29). It
// never holds secret values.
type Snapshot struct {
	Agent   SnapAgent        `json:"agent"`
	Tools   []SnapTool       `json:"tools"`
	Secrets map[string]int64 `json:"secrets"`
}

// SnapAgent is the agent part of a snapshot.
type SnapAgent struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	SystemPrompt string `json:"system_prompt"`
	Provider     string `json:"provider"`
	Model        string `json:"model"`
}

// SnapTool is one tool in a snapshot.
type SnapTool struct {
	ID int64 `json:"id"`
	tool.Definition
}

func (s Snapshot) toolByName(name string) (SnapTool, bool) {
	for _, t := range s.Tools {
		if t.Name == name {
			return t, true
		}
	}
	return SnapTool{}, false
}

// Errors returned to the API layer.
var (
	ErrNotFound  = errors.New("not found")
	ErrForbidden = errors.New("forbidden")
)

// ConflictError means the requested change is no longer possible (409).
type ConflictError struct {
	Msg   string
	State any
}

func (e *ConflictError) Error() string { return e.Msg }

// MissingToolsError is the D40/D56 confirmation request: the agent references
// deleted tools.
type MissingToolsError struct{ ToolIDs []int64 }

func (e *MissingToolsError) Error() string { return "the agent references deleted tools" }

// PreconditionError is a clear, user-facing reason a run cannot start (R7).
type PreconditionError struct{ Msg string }

func (e *PreconditionError) Error() string { return e.Msg }

// CreateRun creates a queued run for the caller (the starter) and enqueues it.
func (e *Engine) CreateRun(ctx context.Context, userID, teamID, agentID int64, task string, confirmMissing []int64) (int64, error) {
	task = strings.TrimSpace(task)
	if task == "" {
		return 0, &PreconditionError{"the task is empty"}
	}
	var runID int64
	err := e.withTx(ctx, 0, func(tx *sql.Tx) error {
		var a SnapAgent
		var deleted sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT id, name, system_prompt, provider, model, deleted_at
			FROM agents WHERE id = ? AND team_id = ?`, agentID, teamID).
			Scan(&a.ID, &a.Name, &a.SystemPrompt, &a.Provider, &a.Model, &deleted)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound // another team's agent looks the same as none (D39)
		}
		if err != nil {
			return err
		}
		if deleted.Valid {
			return &PreconditionError{"this agent has been deleted"}
		}

		// The starter's own model key, never anyone else's (R7).
		var hasKey int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM user_model_keys WHERE user_id = ? AND provider = ?",
			userID, a.Provider).Scan(&hasKey); err != nil {
			return err
		}
		if hasKey == 0 {
			return &PreconditionError{fmt.Sprintf("you have no %s API key; add yours under Settings before running this agent", a.Provider)}
		}

		tools, missing, err := agentTools(ctx, tx, agentID, teamID)
		if err != nil {
			return err
		}
		if len(missing) > 0 && !sameIDs(missing, confirmMissing) {
			return &MissingToolsError{ToolIDs: missing}
		}

		snap := Snapshot{Agent: a, Tools: tools, Secrets: map[string]int64{}}
		var absent []string
		for _, t := range tools {
			for _, name := range tool.SecretRefs(t.Definition) {
				if _, done := snap.Secrets[name]; done {
					continue
				}
				var id int64
				err := tx.QueryRowContext(ctx, "SELECT id FROM user_secrets WHERE user_id = ? AND name = ?", userID, name).Scan(&id)
				if errors.Is(err, sql.ErrNoRows) {
					absent = append(absent, name)
					snap.Secrets[name] = 0
					continue
				}
				if err != nil {
					return err
				}
				snap.Secrets[name] = id
			}
		}
		if len(absent) > 0 {
			sort.Strings(absent)
			return &PreconditionError{fmt.Sprintf("you have no secret named %s; add it under Settings → Secrets (it is never shared with teammates)", strings.Join(absent, ", "))}
		}

		snapJSON, err := json.Marshal(snap)
		if err != nil {
			return err
		}
		now := db.Format(e.Now())
		res, err := tx.ExecContext(ctx, `INSERT INTO runs (team_id, agent_id, started_by, task, status, snapshot, created_at)
			VALUES (?, ?, ?, ?, 'queued', ?, ?)`, teamID, agentID, userID, task, string(snapJSON), now)
		if err != nil {
			return err
		}
		runID, _ = res.LastInsertId()
		if _, err := tx.ExecContext(ctx, `INSERT INTO run_messages (run_id, seq, role, content, created_at)
			VALUES (?, 1, 'user', ?, ?)`, runID, task, now); err != nil {
			return err
		}
		return emit(ctx, tx, runID, EvRunStatus, map[string]string{"status": StQueued})
	})
	if err != nil {
		return 0, err
	}
	e.Enqueue(runID)
	return runID, nil
}

// agentTools returns the agent's non-deleted tools and the IDs of deleted ones.
func agentTools(ctx context.Context, tx *sql.Tx, agentID, teamID int64) ([]SnapTool, []int64, error) {
	rows, err := tx.QueryContext(ctx, `SELECT t.id, t.name, t.description, t.method, t.url_template, t.headers_template,
		t.body_template, t.params_schema, t.requires_approval, t.deleted_at
		FROM agent_tools at JOIN tools t ON t.id = at.tool_id
		WHERE at.agent_id = ? AND at.team_id = ? ORDER BY t.id`, agentID, teamID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var tools []SnapTool
	var missing []int64
	for rows.Next() {
		var t SnapTool
		var headers, schema string
		var deleted sql.NullString
		if err := rows.Scan(&t.ID, &t.Name, &t.Description, &t.Method, &t.URLTemplate, &headers,
			&t.BodyTemplate, &schema, &t.RequiresApproval, &deleted); err != nil {
			return nil, nil, err
		}
		if deleted.Valid {
			missing = append(missing, t.ID)
			continue
		}
		if err := json.Unmarshal([]byte(headers), &t.Headers); err != nil {
			return nil, nil, err
		}
		t.ParamsSchema = json.RawMessage(schema)
		tools = append(tools, t)
	}
	return tools, missing, rows.Err()
}

func sameIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	sa := append([]int64(nil), a...)
	sb := append([]int64(nil), b...)
	sort.Slice(sa, func(i, j int) bool { return sa[i] < sa[j] })
	sort.Slice(sb, func(i, j int) bool { return sb[i] < sb[j] })
	for i := range sa {
		if sa[i] != sb[i] {
			return false
		}
	}
	return true
}
