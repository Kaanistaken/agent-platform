// Package api is the REST API and the SSE stream (docs/schema.md).
//
// Every handler takes the acting user, team and role from the session, never
// from the request (D45). Another team's resource returns 404, a same-team
// resource the caller may not change returns 403 (D39, D51). Responses are
// built from allowlisted fields, never by serializing rows.
package api

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/kaang/vodafone-case/backend/internal/auth"
	"github.com/kaang/vodafone-case/backend/internal/crypto"
	"github.com/kaang/vodafone-case/backend/internal/db"
	"github.com/kaang/vodafone-case/backend/internal/run"
	"github.com/kaang/vodafone-case/backend/internal/tool"
)

// SessionCookie is the name of the HttpOnly session cookie (D17).
const SessionCookie = "session"

// Server holds the handlers' dependencies.
type Server struct {
	DB     *db.DB
	Box    *crypto.Box
	Engine *run.Engine
	Origin string // allowed browser origin for state-changing requests (D30)

	// Heartbeat is the SSE session re-check interval (D71).
	Heartbeat time.Duration
}

type handler func(w http.ResponseWriter, r *http.Request, u auth.User) error

// Routes returns the HTTP handler for /api.
func (s *Server) Routes() http.Handler {
	if s.Heartbeat == 0 {
		s.Heartbeat = 15 * time.Second
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/logout", s.logout)

	route := func(pattern string, h handler) { mux.Handle(pattern, s.authed(h)) }
	route("GET /api/me", s.me)
	route("GET /api/models", s.models)

	route("GET /api/me/model-keys", s.listModelKeys)
	route("PUT /api/me/model-keys/{provider}", s.putModelKey)
	route("DELETE /api/me/model-keys/{provider}", s.deleteModelKey)
	route("GET /api/me/secrets", s.listSecrets)
	route("POST /api/me/secrets", s.createSecret)
	route("PUT /api/me/secrets/{id}", s.updateSecret)
	route("DELETE /api/me/secrets/{id}", s.deleteSecret)

	route("GET /api/agents", s.listAgents)
	route("POST /api/agents", s.createAgent)
	route("GET /api/agents/{id}", s.getAgent)
	route("PUT /api/agents/{id}", s.updateAgent)
	route("DELETE /api/agents/{id}", s.deleteAgent)
	route("POST /api/agents/{id}/clone", s.cloneAgent)
	route("POST /api/agents/{id}/runs", s.createRun)

	route("GET /api/tools", s.listTools)
	route("POST /api/tools", s.createTool)
	route("GET /api/tools/{id}", s.getTool)
	route("PUT /api/tools/{id}", s.updateTool)
	route("DELETE /api/tools/{id}", s.deleteTool)
	route("POST /api/tools/{id}/clone", s.cloneTool)

	route("GET /api/runs", s.listRuns)
	route("GET /api/runs/{id}", s.getRun)
	route("GET /api/runs/{id}/events", s.events)
	route("POST /api/runs/{id}/cancel", s.cancelRun)
	route("POST /api/runs/{id}/tool-calls/{callId}/approve", s.approve)
	route("POST /api/runs/{id}/tool-calls/{callId}/reject", s.reject)
	route("GET /api/approvals", s.approvals)
	route("GET /api/usage", s.usage)
	return s.checkOrigin(mux)
}

// checkOrigin rejects state-changing requests from another origin (D30).
// GET never changes state.
func (s *Server) checkOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("Origin") != s.Origin {
			writeError(w, http.StatusForbidden, "request origin not allowed")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) authed(h handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(SessionCookie)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "not logged in")
			return
		}
		u, ok := auth.Authenticate(r.Context(), s.DB.R, c.Value)
		if !ok {
			writeError(w, http.StatusUnauthorized, "session expired; log in again")
			return
		}
		if err := h(w, r, u); err != nil {
			s.handleError(w, r, err)
		}
	})
}

// httpError is an error with a status code and a safe message.
type httpError struct {
	status int
	msg    string
	extra  map[string]any
}

func (e *httpError) Error() string { return e.msg }

func badRequest(msg string) error { return &httpError{status: http.StatusBadRequest, msg: msg} }
func notFound() error             { return &httpError{status: http.StatusNotFound, msg: "not found"} }
func forbidden(msg string) error  { return &httpError{status: http.StatusForbidden, msg: msg} }
func conflict(msg string) error   { return &httpError{status: http.StatusConflict, msg: msg} }

func (s *Server) handleError(w http.ResponseWriter, r *http.Request, err error) {
	var he *httpError
	var ce *run.ConflictError
	var me *run.MissingToolsError
	var pe *run.PreconditionError
	switch {
	case errors.As(err, &he):
		body := map[string]any{"error": he.msg}
		for k, v := range he.extra {
			body[k] = v
		}
		writeJSON(w, he.status, body)
	case errors.Is(err, run.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, run.ErrForbidden):
		writeError(w, http.StatusForbidden, "you are not allowed to do this")
	case errors.As(err, &ce):
		writeJSON(w, http.StatusConflict, map[string]any{"error": ce.Msg, "state": ce.State})
	case errors.As(err, &me):
		writeJSON(w, http.StatusConflict, map[string]any{"error": me.Error(), "missing_tool_ids": me.ToolIDs})
	case errors.As(err, &pe):
		writeError(w, http.StatusBadRequest, pe.Msg)
	case errors.Is(err, tool.ErrInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		// Internal details are logged, not returned. Request bodies are never
		// logged (D45).
		log.Printf("%s %s: %v", r.Method, r.URL.Path, err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// readJSON decodes a small JSON body and rejects unknown fields.
func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return badRequest("invalid JSON body: " + err.Error())
	}
	return nil
}

func pathID(r *http.Request, name string) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		return 0, notFound()
	}
	return id, nil
}
