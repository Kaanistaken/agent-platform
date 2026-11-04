package run

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync"

	"github.com/kaang/vodafone-case/backend/internal/db"
)

// Event types (docs/schema.md, "Event types").
const (
	EvRunStatus        = "run_status"
	EvTextDelta        = "text_delta"
	EvAttemptDiscarded = "attempt_discarded"
	EvModelRetry       = "model_retry"
	EvToolCall         = "tool_call"
	EvToolCallStatus   = "tool_call_status"
	EvToolResult       = "tool_result"
	EvUsage            = "usage"
	EvRunFinished      = "run_finished"
)

// Event is one stored run event. Seq is the SSE event ID (D94).
type Event struct {
	Seq     int64           `json:"seq"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
	At      string          `json:"created_at"`
}

// emit writes an event inside the transaction that makes the change it
// reports, so the stream never shows something that was rolled back (D94).
func emit(ctx context.Context, tx *sql.Tx, runID int64, typ string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO run_events (run_id, seq, type, payload, created_at)
		VALUES (?, (SELECT COALESCE(MAX(seq), 0) + 1 FROM run_events WHERE run_id = ?), ?, ?, ?)`,
		runID, runID, typ, string(data), db.Now())
	return err
}

// Events returns the events of a run after seq, in order.
func Events(ctx context.Context, r *sql.DB, runID, after int64) ([]Event, error) {
	rows, err := r.QueryContext(ctx, `SELECT seq, type, payload, created_at FROM run_events
		WHERE run_id = ? AND seq > ? ORDER BY seq`, runID, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var p string
		if err := rows.Scan(&e.Seq, &e.Type, &p, &e.At); err != nil {
			return nil, err
		}
		e.Payload = json.RawMessage(p)
		out = append(out, e)
	}
	return out, rows.Err()
}

// Hub wakes SSE connections when new events of a run are committed. It only
// carries "something changed"; the events themselves are read from the
// database (D98).
type Hub struct {
	mu   sync.Mutex
	subs map[int64]chan struct{}
}

// NewHub creates an empty hub.
func NewHub() *Hub { return &Hub{subs: map[int64]chan struct{}{}} }

// Wait returns a channel that is closed at the next notification for runID.
// Callers take the channel before reading the database, so no event is missed.
func (h *Hub) Wait(runID int64) <-chan struct{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch, ok := h.subs[runID]
	if !ok {
		ch = make(chan struct{})
		h.subs[runID] = ch
	}
	return ch
}

// Notify wakes everyone waiting on runID.
func (h *Hub) Notify(runID int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if ch, ok := h.subs[runID]; ok {
		close(ch)
		delete(h.subs, runID)
	}
}
