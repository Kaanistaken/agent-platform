package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/kaang/vodafone-case/backend/internal/auth"
	"github.com/kaang/vodafone-case/backend/internal/db"
	"github.com/kaang/vodafone-case/backend/internal/provider"
	"github.com/kaang/vodafone-case/backend/internal/tool"
)

// Agents and tools are data (R1, R2): creating one needs no code change.

type ownerRef struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

type agentOut struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	SystemPrompt string    `json:"system_prompt"`
	Provider     string    `json:"provider"`
	Model        string    `json:"model"`
	Owner        ownerRef  `json:"owner"`
	Tools        []toolRef `json:"tools"`
	Deleted      bool      `json:"deleted"`
	CreatedAt    string    `json:"created_at"`
	UpdatedAt    string    `json:"updated_at"`
}

type toolRef struct {
	ID               int64  `json:"id"`
	Name             string `json:"name"`
	RequiresApproval bool   `json:"requires_approval"`
	Deleted          bool   `json:"deleted"`
}

type toolOut struct {
	ID               int64             `json:"id"`
	Name             string            `json:"name"`
	Description      string            `json:"description"`
	Method           string            `json:"method"`
	URLTemplate      string            `json:"url_template"`
	Headers          map[string]string `json:"headers_template"`
	BodyTemplate     string            `json:"body_template"`
	ParamsSchema     json.RawMessage   `json:"params_schema"`
	RequiresApproval bool              `json:"requires_approval"`
	Owner            ownerRef          `json:"owner"`
	Deleted          bool              `json:"deleted"`
	CreatedAt        string            `json:"created_at"`
	UpdatedAt        string            `json:"updated_at"`
}

const agentSelect = `SELECT a.id, a.name, a.system_prompt, a.provider, a.model, a.owner_id, u.username,
	a.deleted_at IS NOT NULL, a.created_at, a.updated_at FROM agents a JOIN users u ON u.id = a.owner_id`

func scanAgent(row interface{ Scan(...any) error }) (agentOut, error) {
	var a agentOut
	err := row.Scan(&a.ID, &a.Name, &a.SystemPrompt, &a.Provider, &a.Model, &a.Owner.ID, &a.Owner.Username,
		&a.Deleted, &a.CreatedAt, &a.UpdatedAt)
	a.Tools = []toolRef{}
	return a, err
}

func (s *Server) agentTools(ctx context.Context, q *sql.DB, agentID int64) ([]toolRef, error) {
	rows, err := q.QueryContext(ctx, `SELECT t.id, t.name, t.requires_approval, t.deleted_at IS NOT NULL
		FROM agent_tools at JOIN tools t ON t.id = at.tool_id WHERE at.agent_id = ? ORDER BY t.name`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []toolRef{}
	for rows.Next() {
		var t toolRef
		if err := rows.Scan(&t.ID, &t.Name, &t.RequiresApproval, &t.Deleted); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Server) listAgents(w http.ResponseWriter, r *http.Request, u auth.User) error {
	rows, err := s.DB.R.QueryContext(r.Context(), agentSelect+" WHERE a.team_id = ? AND a.deleted_at IS NULL ORDER BY a.name", u.TeamID)
	if err != nil {
		return err
	}
	var out []agentOut
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			rows.Close()
			return err
		}
		out = append(out, a)
	}
	rows.Close()
	for i := range out {
		if out[i].Tools, err = s.agentTools(r.Context(), s.DB.R, out[i].ID); err != nil {
			return err
		}
	}
	if out == nil {
		out = []agentOut{}
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) loadAgent(ctx context.Context, q *sql.DB, id, teamID int64) (agentOut, error) {
	a, err := scanAgent(q.QueryRowContext(ctx, agentSelect+" WHERE a.id = ? AND a.team_id = ?", id, teamID))
	if errors.Is(err, sql.ErrNoRows) {
		return a, notFound()
	}
	if err != nil {
		return a, err
	}
	a.Tools, err = s.agentTools(ctx, q, id)
	return a, err
}

func (s *Server) getAgent(w http.ResponseWriter, r *http.Request, u auth.User) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	a, err := s.loadAgent(r.Context(), s.DB.R, id, u.TeamID)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, a)
	return nil
}

type agentIn struct {
	Name         string  `json:"name"`
	SystemPrompt string  `json:"system_prompt"`
	Provider     string  `json:"provider"`
	Model        string  `json:"model"`
	ToolIDs      []int64 `json:"tool_ids"`
}

func (in *agentIn) validate() error {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 100 {
		return badRequest("name is required (at most 100 characters)")
	}
	if len(in.SystemPrompt) > 20000 {
		return badRequest("system prompt is too long")
	}
	if !provider.Supported(in.Provider, in.Model) {
		return badRequest("unsupported provider or model")
	}
	return nil
}

// setAgentTools replaces the agent's tool links. Tools must belong to the
// caller's team (another team's ID is 404, D39); links to deleted tools that
// already exist may stay, new ones are rejected (D46); names must be unique
// within the agent (D49).
func setAgentTools(ctx context.Context, tx *sql.Tx, agentID, teamID int64, ids []int64) error {
	existing := map[int64]bool{}
	rows, err := tx.QueryContext(ctx, "SELECT tool_id FROM agent_tools WHERE agent_id = ?", agentID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		existing[id] = true
	}
	rows.Close()

	names := map[string]bool{}
	seen := map[int64]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		var name string
		var deleted bool
		err := tx.QueryRowContext(ctx, "SELECT name, deleted_at IS NOT NULL FROM tools WHERE id = ? AND team_id = ?", id, teamID).
			Scan(&name, &deleted)
		if errors.Is(err, sql.ErrNoRows) {
			return notFound()
		}
		if err != nil {
			return err
		}
		if deleted && !existing[id] {
			return badRequest("tool " + name + " is deleted and cannot be added")
		}
		if names[name] {
			return badRequest("two tools named " + name + "; tool names must be unique within an agent")
		}
		names[name] = true
	}
	// Only removed links are deleted and only new ones inserted: a retained
	// link to a deleted tool stays as it is (D46).
	for id := range existing {
		if !seen[id] {
			if _, err := tx.ExecContext(ctx, "DELETE FROM agent_tools WHERE agent_id = ? AND tool_id = ?", agentID, id); err != nil {
				return err
			}
		}
	}
	for id := range seen {
		if existing[id] {
			continue
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO agent_tools (agent_id, tool_id, team_id) VALUES (?, ?, ?)", agentID, id, teamID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) createAgent(w http.ResponseWriter, r *http.Request, u auth.User) error {
	var in agentIn
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if err := in.validate(); err != nil {
		return err
	}
	var id int64
	err := s.tx(r, func(tx *sql.Tx) error {
		now := db.Now()
		res, err := tx.ExecContext(r.Context(), `INSERT INTO agents (team_id, owner_id, name, system_prompt, provider, model, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, u.TeamID, u.ID, in.Name, in.SystemPrompt, in.Provider, in.Model, now, now)
		if err != nil {
			return err
		}
		id, _ = res.LastInsertId()
		return setAgentTools(r.Context(), tx, id, u.TeamID, in.ToolIDs)
	})
	if err != nil {
		return err
	}
	a, err := s.loadAgent(r.Context(), s.DB.W, id, u.TeamID)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, a)
	return nil
}

// ownedRow checks that a row of table exists in the caller's team (404
// otherwise) and returns its owner and deleted flag.
func ownedRow(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, table string, id, teamID int64) (owner int64, deleted bool, err error) {
	err = q.QueryRowContext(ctx, "SELECT owner_id, deleted_at IS NOT NULL FROM "+table+" WHERE id = ? AND team_id = ?", id, teamID).
		Scan(&owner, &deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, notFound()
	}
	return owner, deleted, err
}

func (s *Server) updateAgent(w http.ResponseWriter, r *http.Request, u auth.User) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var in agentIn
	if err := readJSON(r, &in); err != nil {
		return err
	}
	err = s.tx(r, func(tx *sql.Tx) error {
		owner, deleted, err := ownedRow(r.Context(), tx, "agents", id, u.TeamID)
		if err != nil {
			return err
		}
		if owner != u.ID {
			return forbidden("only the owner can change this agent; clone it to make your own copy")
		}
		if deleted {
			return conflict("this agent is deleted")
		}
		if err := in.validate(); err != nil {
			return err
		}
		if _, err := tx.ExecContext(r.Context(), `UPDATE agents SET name = ?, system_prompt = ?, provider = ?, model = ?, updated_at = ?
			WHERE id = ?`, in.Name, in.SystemPrompt, in.Provider, in.Model, db.Now(), id); err != nil {
			return err
		}
		return setAgentTools(r.Context(), tx, id, u.TeamID, in.ToolIDs)
	})
	if err != nil {
		return err
	}
	a, err := s.loadAgent(r.Context(), s.DB.W, id, u.TeamID)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, a)
	return nil
}

func (s *Server) deleteAgent(w http.ResponseWriter, r *http.Request, u auth.User) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	return s.tx(r, func(tx *sql.Tx) error {
		owner, deleted, err := ownedRow(r.Context(), tx, "agents", id, u.TeamID)
		if err != nil {
			return err
		}
		if owner != u.ID && !u.IsLead() { // D44: a lead may delete members' agents
			return forbidden("only the owner or a team lead can delete this agent")
		}
		if !deleted {
			if _, err := tx.ExecContext(r.Context(), "UPDATE agents SET deleted_at = ?, updated_at = ? WHERE id = ?", db.Now(), db.Now(), id); err != nil {
				return err
			}
		}
		w.WriteHeader(http.StatusNoContent)
		return nil
	})
}

func (s *Server) cloneAgent(w http.ResponseWriter, r *http.Request, u auth.User) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var newID int64
	var dropped []int64
	err = s.tx(r, func(tx *sql.Tx) error {
		_, deleted, err := ownedRow(r.Context(), tx, "agents", id, u.TeamID)
		if err != nil {
			return err
		}
		if deleted {
			return conflict("a deleted agent cannot be cloned")
		}
		now := db.Now()
		res, err := tx.ExecContext(r.Context(), `INSERT INTO agents (team_id, owner_id, name, system_prompt, provider, model, created_at, updated_at)
			SELECT team_id, ?, name || ' (copy)', system_prompt, provider, model, ?, ? FROM agents WHERE id = ?`, u.ID, now, now, id)
		if err != nil {
			return err
		}
		newID, _ = res.LastInsertId()
		rows, err := tx.QueryContext(r.Context(), `SELECT t.id, t.deleted_at IS NOT NULL FROM agent_tools at
			JOIN tools t ON t.id = at.tool_id WHERE at.agent_id = ?`, id)
		if err != nil {
			return err
		}
		var keep []int64
		for rows.Next() {
			var tid int64
			var del bool
			rows.Scan(&tid, &del)
			if del {
				dropped = append(dropped, tid) // D52
			} else {
				keep = append(keep, tid)
			}
		}
		rows.Close()
		return setAgentTools(r.Context(), tx, newID, u.TeamID, keep)
	})
	if err != nil {
		return err
	}
	a, err := s.loadAgent(r.Context(), s.DB.W, newID, u.TeamID)
	if err != nil {
		return err
	}
	if dropped == nil {
		dropped = []int64{}
	}
	writeJSON(w, http.StatusCreated, map[string]any{"agent": a, "dropped_tool_ids": dropped})
	return nil
}

// --- tools ---

const toolSelect = `SELECT t.id, t.name, t.description, t.method, t.url_template, t.headers_template, t.body_template,
	t.params_schema, t.requires_approval, t.owner_id, u.username, t.deleted_at IS NOT NULL, t.created_at, t.updated_at
	FROM tools t JOIN users u ON u.id = t.owner_id`

func scanTool(row interface{ Scan(...any) error }) (toolOut, error) {
	var t toolOut
	var headers, schema string
	err := row.Scan(&t.ID, &t.Name, &t.Description, &t.Method, &t.URLTemplate, &headers, &t.BodyTemplate, &schema,
		&t.RequiresApproval, &t.Owner.ID, &t.Owner.Username, &t.Deleted, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return t, err
	}
	json.Unmarshal([]byte(headers), &t.Headers)
	if t.Headers == nil {
		t.Headers = map[string]string{}
	}
	t.ParamsSchema = json.RawMessage(schema)
	return t, nil
}

func (s *Server) listTools(w http.ResponseWriter, r *http.Request, u auth.User) error {
	rows, err := s.DB.R.QueryContext(r.Context(), toolSelect+" WHERE t.team_id = ? AND t.deleted_at IS NULL ORDER BY t.name", u.TeamID)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []toolOut{}
	for rows.Next() {
		t, err := scanTool(rows)
		if err != nil {
			return err
		}
		out = append(out, t)
	}
	writeJSON(w, http.StatusOK, out)
	return rows.Err()
}

func (s *Server) loadTool(ctx context.Context, q *sql.DB, id, teamID int64) (toolOut, error) {
	t, err := scanTool(q.QueryRowContext(ctx, toolSelect+" WHERE t.id = ? AND t.team_id = ?", id, teamID))
	if errors.Is(err, sql.ErrNoRows) {
		return t, notFound()
	}
	return t, err
}

func (s *Server) getTool(w http.ResponseWriter, r *http.Request, u auth.User) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	t, err := s.loadTool(r.Context(), s.DB.R, id, u.TeamID)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, t)
	return nil
}

func readToolDef(r *http.Request) (tool.Definition, error) {
	var d tool.Definition
	if err := readJSON(r, &d); err != nil {
		return d, err
	}
	d.Name = strings.TrimSpace(d.Name)
	if d.Headers == nil {
		d.Headers = map[string]string{}
	}
	if len(d.ParamsSchema) == 0 {
		d.ParamsSchema = json.RawMessage(`{"type":"object","properties":{}}`)
	}
	if err := tool.Validate(d); err != nil {
		return d, err
	}
	return d, nil
}

func (s *Server) createTool(w http.ResponseWriter, r *http.Request, u auth.User) error {
	d, err := readToolDef(r)
	if err != nil {
		return err
	}
	headers, _ := json.Marshal(d.Headers)
	now := db.Now()
	res, err := s.DB.W.ExecContext(r.Context(), `INSERT INTO tools (team_id, owner_id, name, description, method, url_template,
		headers_template, body_template, params_schema, requires_approval, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, u.TeamID, u.ID, d.Name, d.Description, d.Method, d.URLTemplate,
		string(headers), d.BodyTemplate, string(d.ParamsSchema), d.RequiresApproval, now, now)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	t, err := s.loadTool(r.Context(), s.DB.W, id, u.TeamID)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, t)
	return nil
}

func (s *Server) updateTool(w http.ResponseWriter, r *http.Request, u auth.User) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	// Authorization comes before the body: another team's tool is 404 and a
	// teammate's is 403 whatever is sent (D39, D51). It is checked on the
	// read pool first, so a slow body never holds the single write
	// connection (D82), and again inside the transaction.
	if err := toolWriteAccess(r.Context(), s.DB.R, id, u); err != nil {
		return err
	}
	d, err := readToolDef(r)
	if err != nil {
		return err
	}
	headers, _ := json.Marshal(d.Headers)
	err = s.tx(r, func(tx *sql.Tx) error {
		if err := toolWriteAccess(r.Context(), tx, id, u); err != nil {
			return err
		}
		// D49: the new name must stay unique in every agent the tool is
		// linked to, counting every other linked tool, deleted ones included.
		var agent string
		err = tx.QueryRowContext(r.Context(), `SELECT a.name FROM agent_tools mine
			JOIN agent_tools other ON other.agent_id = mine.agent_id AND other.tool_id != mine.tool_id
			JOIN tools t ON t.id = other.tool_id
			JOIN agents a ON a.id = mine.agent_id
			WHERE mine.tool_id = ? AND t.name = ? LIMIT 1`, id, d.Name).Scan(&agent)
		if err == nil {
			return conflict("agent " + agent + " already has a tool named " + d.Name + "; tool names must be unique within an agent")
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		// Past runs keep their snapshot (D25); only new runs see the change.
		_, err = tx.ExecContext(r.Context(), `UPDATE tools SET name = ?, description = ?, method = ?, url_template = ?,
			headers_template = ?, body_template = ?, params_schema = ?, requires_approval = ?, updated_at = ?
			WHERE id = ? AND owner_id = ?`, d.Name, d.Description, d.Method, d.URLTemplate, string(headers), d.BodyTemplate,
			string(d.ParamsSchema), d.RequiresApproval, db.Now(), id, u.ID)
		return err
	})
	if err != nil {
		return err
	}
	t, err := s.loadTool(r.Context(), s.DB.W, id, u.TeamID)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, t)
	return nil
}

// toolWriteAccess allows changes only by the tool's owner, on a tool that is
// not deleted.
func toolWriteAccess(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id int64, u auth.User) error {
	owner, deleted, err := ownedRow(ctx, q, "tools", id, u.TeamID)
	if err != nil {
		return err
	}
	if owner != u.ID {
		return forbidden("only the owner can change this tool; clone it to make your own copy")
	}
	if deleted {
		return conflict("this tool is deleted")
	}
	return nil
}

func (s *Server) deleteTool(w http.ResponseWriter, r *http.Request, u auth.User) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	owner, _, err := ownedRow(r.Context(), s.DB.W, "tools", id, u.TeamID)
	if err != nil {
		return err
	}
	if owner != u.ID { // D34: no lead override for tools
		return forbidden("only the owner can delete this tool")
	}
	if _, err := s.DB.W.ExecContext(r.Context(), "UPDATE tools SET deleted_at = COALESCE(deleted_at, ?), updated_at = ? WHERE id = ?",
		db.Now(), db.Now(), id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) cloneTool(w http.ResponseWriter, r *http.Request, u auth.User) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	_, deleted, err := ownedRow(r.Context(), s.DB.W, "tools", id, u.TeamID)
	if err != nil {
		return err
	}
	if deleted {
		return conflict("a deleted tool cannot be cloned")
	}
	now := db.Now()
	// The definition is copied; no credentials exist on a tool to copy.
	res, err := s.DB.W.ExecContext(r.Context(), `INSERT INTO tools (team_id, owner_id, name, description, method, url_template,
		headers_template, body_template, params_schema, requires_approval, created_at, updated_at)
		SELECT team_id, ?, name, description, method, url_template, headers_template, body_template, params_schema,
		requires_approval, ?, ? FROM tools WHERE id = ?`, u.ID, now, now, id)
	if err != nil {
		return err
	}
	newID, _ := res.LastInsertId()
	t, err := s.loadTool(r.Context(), s.DB.W, newID, u.TeamID)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, t)
	return nil
}
