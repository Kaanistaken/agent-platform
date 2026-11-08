// Package run executes agent runs: the agent loop, approvals, cancellation
// and recovery after a restart. One backend process runs a small pool of
// workers (D84); all state lives in SQLite so a restart can resume (R4, R5).
package run

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"github.com/kaang/vodafone-case/backend/internal/crypto"
	"github.com/kaang/vodafone-case/backend/internal/db"
	"github.com/kaang/vodafone-case/backend/internal/provider"
	"github.com/kaang/vodafone-case/backend/internal/tool"
)

// Limits. D74 (deadlines), D80 (steps, invalid calls), D95 (output tokens);
// worker count and sweep interval chosen under D85.
const (
	MaxSteps            = 20
	MaxInvalidStreak    = 3
	RunActiveDeadline   = time.Hour
	ModelAttemptTimeout = 10 * time.Minute
	MaxAttempts         = 4 // "3 retries" (D88)
	MaxRetryAfter       = 60 * time.Second
	MaxOutputTokens     = 16384 // D95, raised before delivery (D135)
	Workers             = 4
	ExpirySweep         = time.Minute
	textFlushInterval   = 250 * time.Millisecond // D70
)

// Run statuses (D60).
const (
	StQueued          = "queued"
	StRunning         = "running"
	StWaitingApproval = "waiting_approval"
	StSucceeded       = "succeeded"
	StFailed          = "failed"
	StCancelled       = "cancelled"
)

// Tool-call statuses (D60, D86).
const (
	TcInvalid        = "invalid"
	TcPending        = "pending_approval"
	TcApproved       = "approved"
	TcRejected       = "rejected"
	TcInvalidated    = "invalidated"
	TcExecuting      = "executing"
	TcSucceeded      = "succeeded"
	TcFailed         = "failed"
	TcOutcomeUnknown = "outcome_unknown"
)

// Executor sends a rendered tool request. *tool.Client implements it; tests
// may pass a fake.
type Executor interface {
	Do(ctx context.Context, r tool.Request, secrets map[string]tool.Secret, maxAttempts int, onAttempt func() error) (tool.Result, error)
}

// Engine runs agent runs.
type Engine struct {
	DB        *db.DB
	Box       *crypto.Box
	Providers map[string]provider.Provider
	Tools     Executor
	Hub       *Hub

	// Injectable for tests (D102): the clock and the retry wait.
	Now   func() time.Time
	Sleep func(ctx context.Context, d time.Duration) error

	// Work queue: an unbounded FIFO, so no run is ever dropped (R4). wake has
	// one slot; a worker that takes work re-signals while more is queued.
	mu      sync.Mutex
	pending []int64
	queued  map[int64]bool               // in pending
	active  map[int64]context.CancelFunc // being processed by a worker
	again   map[int64]bool               // enqueued while active: process once more
	wake    chan struct{}
	wg      sync.WaitGroup
	base    context.Context
	stop    context.CancelFunc
}

// New creates an engine. Call Start to recover and begin processing.
func New(d *db.DB, box *crypto.Box, providers map[string]provider.Provider, tools Executor) *Engine {
	return &Engine{
		DB: d, Box: box, Providers: providers, Tools: tools, Hub: NewHub(),
		Now:    time.Now,
		Sleep:  sleepCtx,
		active: map[int64]context.CancelFunc{},
		queued: map[int64]bool{},
		again:  map[int64]bool{},
		wake:   make(chan struct{}, 1),
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Start recovers interrupted work and starts the workers and the expiry sweep.
func (e *Engine) Start(ctx context.Context) error {
	e.base, e.stop = context.WithCancel(context.Background())
	if err := e.recover(ctx); err != nil {
		return err
	}
	for i := 0; i < Workers; i++ {
		e.wg.Add(1)
		go e.worker()
	}
	e.wg.Add(1)
	go e.sweeper()
	return nil
}

// Stop stops the workers. Runs keep their state and resume on the next start.
func (e *Engine) Stop() {
	if e.stop != nil {
		e.stop()
	}
	e.wg.Wait()
}

// Enqueue asks a worker to process the run. Duplicates are ignored. A run that
// a worker is processing right now is processed once more when that worker
// lets go of it, so a change made while the worker is exiting is not lost.
func (e *Engine) Enqueue(runID int64) {
	e.mu.Lock()
	if _, busy := e.active[runID]; busy {
		e.again[runID] = true
	} else if !e.queued[runID] {
		e.queued[runID] = true
		e.pending = append(e.pending, runID)
	}
	e.mu.Unlock()
	e.signal()
}

func (e *Engine) signal() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

// claim takes the next queued run that no worker is processing and registers
// it as active.
func (e *Engine) claim() (int64, context.Context, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for len(e.pending) > 0 {
		id := e.pending[0]
		e.pending = e.pending[1:]
		delete(e.queued, id)
		if _, busy := e.active[id]; busy {
			e.again[id] = true
			continue
		}
		ctx, cancel := context.WithCancel(e.base)
		e.active[id] = cancel
		if len(e.pending) > 0 {
			e.signal() // wake another worker for the rest
		}
		return id, ctx, true
	}
	return 0, nil, false
}

// release is called by the worker after process returns. If the run was
// enqueued meanwhile, the worker keeps it and gets a fresh context to process
// it again; otherwise the run stops being active.
func (e *Engine) release(id int64) (context.Context, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.active[id]()
	if e.again[id] && e.base.Err() == nil {
		delete(e.again, id)
		ctx, cancel := context.WithCancel(e.base)
		e.active[id] = cancel
		return ctx, true
	}
	delete(e.again, id)
	delete(e.active, id)
	return nil, false
}

func (e *Engine) worker() {
	defer e.wg.Done()
	for e.base.Err() == nil {
		id, ctx, ok := e.claim()
		if !ok {
			select {
			case <-e.base.Done():
			case <-e.wake:
			}
			continue
		}
		for ok {
			e.process(ctx, id)
			ctx, ok = e.release(id)
		}
	}
}

// cancelActive stops the in-flight model call or HTTP request of a run (D66).
func (e *Engine) cancelActive(runID int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if c, ok := e.active[runID]; ok {
		c()
	}
}

// withTx runs fn in a write transaction and wakes the run's SSE streams after
// a successful commit.
func (e *Engine) withTx(ctx context.Context, runID int64, fn func(tx *sql.Tx) error) error {
	tx, err := e.DB.W.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if runID != 0 {
		e.Hub.Notify(runID)
	}
	return nil
}

// runState is what the loop needs from the runs row.
type runState struct {
	ID        int64
	TeamID    int64
	StartedBy int64
	Status    string
	Snapshot  Snapshot
	StepCount int
	Streak    int
	ActiveMS  int64
}

func terminal(status string) bool {
	return status == StSucceeded || status == StFailed || status == StCancelled
}

func loadRun(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, runID int64) (*runState, error) {
	var r runState
	var snap string
	err := q.QueryRowContext(ctx, `SELECT id, team_id, started_by, status, snapshot, step_count,
		invalid_call_streak, active_ms FROM runs WHERE id = ?`, runID).
		Scan(&r.ID, &r.TeamID, &r.StartedBy, &r.Status, &snap, &r.StepCount, &r.Streak, &r.ActiveMS)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(snap), &r.Snapshot); err != nil {
		return nil, err
	}
	return &r, nil
}

// errStop means the loop must stop without changing the run (it was
// cancelled, finished elsewhere or the server is shutting down).
var errStop = errors.New("stop")

// process is the agent loop for one run.
func (e *Engine) process(ctx context.Context, runID int64) {
	r, err := loadRun(ctx, e.DB.W, runID)
	if err != nil {
		log.Printf("run %d: load: %v", runID, err)
		return
	}
	if terminal(r.Status) || r.Status == StWaitingApproval && !e.hasApprovedCall(ctx, runID) {
		return
	}

	err = e.withTx(ctx, runID, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE runs SET status = 'running', started_at = COALESCE(started_at, ?)
			WHERE id = ? AND status IN ('queued', 'waiting_approval')`, db.Format(e.Now()), runID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 1 {
			return emit(ctx, tx, runID, EvRunStatus, map[string]string{"status": StRunning})
		}
		return nil
	})
	if err != nil {
		log.Printf("run %d: start: %v", runID, err)
		return
	}

	// Active time (D74): only time spent here counts, not waiting for approval
	// or server downtime. It is saved after every step so it survives restarts.
	remaining := RunActiveDeadline - time.Duration(r.ActiveMS)*time.Millisecond
	if remaining <= 0 {
		e.finish(context.Background(), runID, StFailed, "run_deadline", "the run exceeded its 1 hour active-time limit", "")
		return
	}
	dctx, cancel := context.WithTimeout(ctx, remaining)
	defer cancel()
	last := e.Now()
	saveActive := func() {
		now := e.Now()
		ms := now.Sub(last).Milliseconds()
		last = now
		e.DB.W.ExecContext(context.Background(), "UPDATE runs SET active_ms = active_ms + ? WHERE id = ?", ms, runID)
	}
	defer saveActive()

	for {
		if dctx.Err() != nil {
			if ctx.Err() == nil { // the deadline, not a cancel or shutdown
				e.finish(context.Background(), runID, StFailed, "run_deadline", "the run exceeded its 1 hour active-time limit", "")
			}
			return
		}
		r, err = loadRun(dctx, e.DB.W, runID)
		if err != nil || terminal(r.Status) {
			return
		}

		call, err := e.openCall(dctx, runID)
		if err != nil {
			log.Printf("run %d: %v", runID, err)
			return
		}
		if call != nil {
			switch call.Status {
			case TcPending, TcInvalidated:
				e.setWaiting(dctx, runID)
				return
			case TcApproved:
				if err := e.executeCall(dctx, r, call); err != nil {
					if !errors.Is(err, errStop) {
						log.Printf("run %d: tool call %d: %v", runID, call.ID, err)
					}
					if dctx.Err() != nil {
						continue // handled at the top of the loop
					}
					return
				}
				saveActive()
				continue
			}
		}

		if r.StepCount >= MaxSteps {
			e.finish(context.Background(), runID, StFailed, "step_limit",
				"the run reached the limit of 20 model turns", "")
			return
		}
		if err := e.modelTurn(dctx, r); err != nil {
			if !errors.Is(err, errStop) {
				log.Printf("run %d: model turn: %v", runID, err)
			}
			if dctx.Err() != nil {
				continue
			}
			return
		}
		saveActive()
	}
}

func (e *Engine) hasApprovedCall(ctx context.Context, runID int64) bool {
	var n int
	e.DB.W.QueryRowContext(ctx, "SELECT COUNT(*) FROM tool_calls WHERE run_id = ? AND status = 'approved'", runID).Scan(&n)
	return n > 0
}

func (e *Engine) setWaiting(ctx context.Context, runID int64) {
	e.withTx(ctx, runID, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, "UPDATE runs SET status = 'waiting_approval' WHERE id = ? AND status = 'running'", runID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 1 {
			return emit(ctx, tx, runID, EvRunStatus, map[string]string{"status": StWaitingApproval})
		}
		return nil
	})
}

// finish moves a non-terminal run to a terminal state in one conditional
// transaction that also writes the final event (D94). It reports whether this
// call made the transition.
func (e *Engine) finish(ctx context.Context, runID int64, status, code, msg, output string) bool {
	done := false
	e.withTx(ctx, runID, func(tx *sql.Tx) error {
		var ok bool
		var err error
		ok, err = finishTx(ctx, tx, runID, status, code, msg, output, e.Now())
		done = ok
		return err
	})
	return done
}

func finishTx(ctx context.Context, tx *sql.Tx, runID int64, status, code, msg, output string, now time.Time) (bool, error) {
	res, err := tx.ExecContext(ctx, `UPDATE runs SET status = ?, error_code = NULLIF(?, ''), error_message = NULLIF(?, ''),
		final_output = CASE WHEN ? = '' THEN final_output ELSE ? END, finished_at = ?
		WHERE id = ? AND status NOT IN ('succeeded', 'failed', 'cancelled')`,
		status, code, msg, output, output, db.Format(now), runID)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return false, nil
	}
	// Open approvals of a finished run are closed.
	reason := "run_cancelled"
	if code == "approval_expired" {
		reason = "approval_expired"
	}
	if _, err := tx.ExecContext(ctx, `UPDATE tool_calls SET status = 'rejected', reason_code = ?, updated_at = ?
		WHERE run_id = ? AND status IN ('pending_approval', 'approved', 'invalidated')`, reason, db.Format(now), runID); err != nil {
		return false, err
	}
	// A call still being sent is closed here, before the final event, so the
	// record never shows it executing forever; a late HTTP completion cannot
	// overwrite it (completeCall only updates executing calls). A write may
	// have had its effect (D63, D66).
	executing, _, err := runCalls(ctx, tx, runID, TcExecuting)
	if err != nil {
		return false, err
	}
	for _, c := range executing {
		st := TcOutcomeUnknown
		if c.Request.Method == http.MethodGet {
			st = TcFailed
		}
		if _, err := tx.ExecContext(ctx, `UPDATE tool_calls SET status = ?, reason_code = 'run_cancelled', finished_at = ?,
			updated_at = ? WHERE id = ? AND status = 'executing'`, st, db.Format(now), db.Format(now), c.ID); err != nil {
			return false, err
		}
		if err := emit(ctx, tx, runID, EvToolCallStatus, map[string]any{"id": c.ID, "status": st, "reason_code": "run_cancelled"}); err != nil {
			return false, err
		}
	}
	payload := map[string]any{"status": status, "error_code": code, "error_message": msg}
	if output != "" {
		payload["final_output"] = output
	}
	return true, emit(ctx, tx, runID, EvRunFinished, payload)
}

func jitter() time.Duration { return time.Duration(rand.IntN(250)) * time.Millisecond }
